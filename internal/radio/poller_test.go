package radio

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"waveloggate/internal/config"
	"waveloggate/internal/wavelog"
)

type pollReply struct {
	status RigStatus
	err    error
}

type fixtureRadioClient struct {
	replies []pollReply
	index   int
}

func (f *fixtureRadioClient) GetStatus() (RigStatus, error) {
	reply := f.replies[f.index]
	if f.index < len(f.replies)-1 {
		f.index++
	}
	return reply.status, reply.err
}
func (*fixtureRadioClient) SetFreqMode(int64, string) error { return nil }
func (*fixtureRadioClient) SetTxFreq(int64) error           { return nil }
func (*fixtureRadioClient) GetModes() ([]string, error)     { return nil, nil }

type blockingRadioClient struct {
	started chan struct{}
	release chan struct{}
	status  RigStatus
}

func (b *blockingRadioClient) GetStatus() (RigStatus, error) {
	b.started <- struct{}{}
	<-b.release
	return b.status, nil
}
func (*blockingRadioClient) SetFreqMode(int64, string) error { return nil }
func (*blockingRadioClient) SetTxFreq(int64) error           { return nil }
func (*blockingRadioClient) GetModes() ([]string, error)     { return nil, nil }

func testPoller(now *time.Time, client RadioClient, events *[]PollEvent) *Poller {
	p := NewPoller(&config.Profile{}, nil, func(event PollEvent) { *events = append(*events, event) })
	p.client = client
	p.sessionID = "session-a"
	p.reportInterval = 5 * time.Second
	p.correlated = true
	p.now = func() time.Time { return *now }
	return p
}

func TestUnchangedSuccessfulPollsReportAtBoundedCadence(t *testing.T) {
	now := time.Date(2026, 10, 3, 2, 50, 0, 0, time.UTC)
	status := RigStatus{FreqA: 14042300, Mode: "USB"}
	events := []PollEvent{}
	p := testPoller(&now, &fixtureRadioClient{replies: []pollReply{{status: status}}}, &events)

	p.poll()
	now = now.Add(time.Second)
	p.poll()
	now = now.Add(4 * time.Second)
	p.poll()

	if len(events) != 2 {
		t.Fatalf("expected first and five-second observations, got %d", len(events))
	}
	if events[0].State != PollObserved || events[0].Sequence != 1 {
		t.Fatalf("unexpected first observation: %+v", events[0])
	}
	if events[1].State != PollObserved || events[1].Sequence != 3 {
		t.Fatalf("unchanged observation did not retain poll-attempt ordering: %+v", events[1])
	}
	if !events[1].ObservedAt.Equal(now) || events[1].Status != status {
		t.Fatalf("unexpected refreshed observation: %+v", events[1])
	}
}

func TestFailureDoesNotAdvanceObservationAndSameStateRecoveryReports(t *testing.T) {
	now := time.Date(2026, 10, 3, 2, 50, 0, 0, time.UTC)
	status := RigStatus{FreqA: 14042300, Mode: "USB"}
	client := &fixtureRadioClient{replies: []pollReply{
		{status: status},
		{err: errors.New("controller unavailable")},
		{err: errors.New("controller unavailable")},
		{status: status},
	}}
	events := []PollEvent{}
	p := testPoller(&now, client, &events)

	for range 4 {
		p.poll()
		now = now.Add(time.Second)
	}

	if len(events) != 3 {
		t.Fatalf("expected observation, first failure and same-state recovery, got %#v", events)
	}
	if events[0].State != PollObserved || events[0].Sequence != 1 {
		t.Fatalf("unexpected initial event: %+v", events[0])
	}
	if events[1].State != PollFailed || events[1].Sequence != 2 || events[1].FailureCode != "RADIO_POLL_FAILED" {
		t.Fatalf("unexpected failure event: %+v", events[1])
	}
	if !events[1].ObservedAt.IsZero() || !events[1].EventAt.Equal(events[0].ObservedAt.Add(time.Second)) {
		t.Fatalf("poll failure must not claim a successful observation: %+v", events[1])
	}
	if events[2].State != PollObserved || events[2].Sequence != 4 || events[2].Status != status {
		t.Fatalf("unchanged recovery was not a new observation: %+v", events[2])
	}
}

func TestPollerRestartCreatesNewSession(t *testing.T) {
	first := NewPoller(nil, nil, nil)
	second := NewPoller(nil, nil, nil)
	if first.sessionID == "" || second.sessionID == "" || first.sessionID == second.sessionID {
		t.Fatalf("poller sessions must be nonempty and unique: %q %q", first.sessionID, second.sessionID)
	}
}

func TestProfileChangeDiscardsInFlightRadioRead(t *testing.T) {
	now := time.Now().UTC()
	events := []PollEvent{}
	blocking := &blockingRadioClient{started: make(chan struct{}, 1), release: make(chan struct{}), status: RigStatus{FreqA: 14042300, Mode: "USB"}}
	p := testPoller(&now, blocking, &events)

	done := make(chan struct{})
	go func() { p.poll(); close(done) }()
	<-blocking.started
	p.UpdateConfig(&config.Profile{WavelogRadioname: "new-radio"})
	close(blocking.release)
	<-done
	if len(events) != 0 {
		t.Fatalf("old profile read escaped after profile generation changed: %#v", events)
	}
}

func TestProfileChangeCancelsBoundDeliveryBeforeCredentialSwap(t *testing.T) {
	requestStarted := make(chan string, 1)
	releaseOldServer := make(chan struct{})
	oldServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestStarted <- r.Header.Get("Authorization")
		select {
		case <-r.Context().Done():
		case <-releaseOldServer:
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer oldServer.Close()
	newAuthorization := make(chan string, 1)
	newServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		newAuthorization <- r.Header.Get("Authorization")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer newServer.Close()

	oldProfile := &config.Profile{WavelogURL: oldServer.URL, WavelogKey: "wl2_old", WavelogRadioname: "old-radio"}
	newProfile := &config.Profile{WavelogURL: newServer.URL, WavelogKey: "wl2_new", WavelogRadioname: "new-radio"}
	wlClient := wavelog.New(oldProfile, "test")
	events := make(chan PollEvent, 4)
	p := NewPoller(oldProfile, wlClient, func(event PollEvent) { events <- event })
	p.client = &fixtureRadioClient{replies: []pollReply{{status: RigStatus{FreqA: 14042300, Mode: "USB"}}}}
	p.ConfigureObservationCapability(&wavelog.RadioObservationOffer{Version: 1, ReportIntervalMS: 1000, Acknowledgement: "echo", MaxClockSkewMS: 300000})
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	p.wg.Add(1)
	go p.deliveryLoop(ctx)

	p.poll()
	if observed := <-events; observed.State != PollObserved || observed.RadioName != "old-radio" {
		t.Fatalf("unexpected old-profile observation: %+v", observed)
	}
	if authorization := <-requestStarted; authorization != "Bearer wl2_old" {
		t.Fatalf("in-flight request used wrong credential: %q", authorization)
	}
	switchStarted := time.Now()
	p.UpdateConfig(newProfile)
	if elapsed := time.Since(switchStarted); elapsed > 100*time.Millisecond {
		t.Fatalf("profile switch waited for old delivery: %s", elapsed)
	}
	close(releaseOldServer)
	select {
	case event := <-events:
		t.Fatalf("retired delivery emitted evidence after profile switch: %+v", event)
	case <-time.After(25 * time.Millisecond):
	}
	if err := wlClient.UpdateRadioStatus(wavelog.RadioData{Frequency: 14042300, Mode: "USB"}); err != nil {
		t.Fatal(err)
	}
	if authorization := <-newAuthorization; authorization != "Bearer wl2_new" {
		t.Fatalf("client did not switch to new credential: %q", authorization)
	}
	cancel()
	p.wg.Wait()
}

func TestSlowServerDeliveryDoesNotBlockRadioPollingAndShutdownCancelsRequest(t *testing.T) {
	requestStarted := make(chan struct{}, 1)
	releaseServer := make(chan struct{})
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestStarted <- struct{}{}
		select {
		case <-r.Context().Done():
		case <-releaseServer:
		}
	}))
	defer server.Close()
	now := time.Now().UTC()
	status := RigStatus{FreqA: 14042300, Mode: "USB"}
	profile := &config.Profile{WavelogURL: server.URL, WavelogKey: "wl2_fixture", WavelogRadioname: "Fixture"}
	p := NewPoller(profile, wavelog.New(profile, "test"), nil)
	p.client = &fixtureRadioClient{replies: []pollReply{{status: status}}}
	p.now = func() time.Time { return now }
	p.sessionStartedAt = now.Add(-time.Second)
	p.ConfigureObservationCapability(&wavelog.RadioObservationOffer{Version: 1, ReportIntervalMS: 1000, Acknowledgement: "echo", MaxClockSkewMS: 300000})
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	p.wg.Add(1)
	go p.deliveryLoop(ctx)

	started := time.Now()
	p.poll()
	select {
	case <-requestStarted:
	case <-time.After(time.Second):
		t.Fatal("delivery did not start")
	}
	for range 20 {
		now = now.Add(time.Second)
		p.poll()
	}
	if elapsed := time.Since(started); elapsed > 250*time.Millisecond {
		t.Fatalf("slow HTTP delivery blocked radio polling: %s", elapsed)
	}
	if queued := len(p.delivery); queued > 1 {
		t.Fatalf("delivery queue exceeded bound: %d", queued)
	}
	cancel()
	done := make(chan struct{})
	go func() { p.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not cancel in-flight delivery")
	}
	close(releaseServer)
}
