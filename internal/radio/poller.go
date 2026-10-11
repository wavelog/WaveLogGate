package radio

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"

	"waveloggate/internal/config"
	"waveloggate/internal/debug"
	"waveloggate/internal/wavelog"
)

const legacyReportInterval = 30 * time.Minute
const maxSafeObservationSequence = uint64(9_007_199_254_740_991)

type radioDelivery struct {
	data       wavelog.RadioData
	event      PollEvent
	profile    *config.Profile
	generation uint64
}

// Poller polls a RadioClient every second and reports changes.
type Poller struct {
	mu                sync.Mutex
	client            RadioClient
	cfg               *config.Profile
	wlClient          *wavelog.Client
	onPoll            PollCallback
	lastStatus        RigStatus
	lastReport        time.Time
	lastFailureReport time.Time
	failed            bool
	sessionID         string
	sessionStartedAt  time.Time
	sequence          uint64
	reportInterval    time.Duration
	correlated        bool
	delivery          chan radioDelivery
	profileGeneration uint64
	deliveryCancel    context.CancelFunc
	now               func() time.Time
	cancel            context.CancelFunc
	wg                sync.WaitGroup
}

// NewPoller creates a Poller. Successful unchanged reads are reported at a
// bounded cadence so consumers can distinguish observation freshness from
// cached transport state.
func NewPoller(cfg *config.Profile, wlClient *wavelog.Client, onPoll PollCallback) *Poller {
	return &Poller{
		cfg:               cfg,
		wlClient:          wlClient,
		onPoll:            onPoll,
		sessionID:         newSessionID(),
		sessionStartedAt:  time.Now().UTC().Truncate(time.Millisecond),
		reportInterval:    legacyReportInterval,
		delivery:          make(chan radioDelivery, 1),
		profileGeneration: 1,
		now:               time.Now,
	}
}

// ConfigureObservationCapability enables correlated heartbeat reporting only
// after an exact server capability was negotiated. nil restores v2.1.1 cadence.
func (p *Poller) ConfigureObservationCapability(capability *wavelog.RadioObservationOffer) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.correlated = capability != nil
	p.reportInterval = legacyReportInterval
	if capability != nil {
		p.reportInterval = time.Duration(capability.ReportIntervalMS) * time.Millisecond
	}
}

// UpdateConfig updates the profile and radio client.
func (p *Poller) UpdateConfig(cfg *config.Profile) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.profileGeneration++
	if p.deliveryCancel != nil {
		p.deliveryCancel()
		p.deliveryCancel = nil
	}
	for len(p.delivery) > 0 {
		<-p.delivery
	}
	p.cfg = cfg
	p.client = buildClient(cfg)
	if p.wlClient != nil {
		p.wlClient.UpdateProfile(cfg)
	}
	p.sessionID = newSessionID()
	p.sessionStartedAt = p.now().UTC().Truncate(time.Millisecond)
	p.sequence = 0
	p.lastStatus = RigStatus{}
	p.lastReport = time.Time{}
	p.lastFailureReport = time.Time{}
	p.failed = false
	p.correlated = false
	p.reportInterval = legacyReportInterval
}

// Start begins polling in a background goroutine.
func (p *Poller) Start(ctx context.Context) {
	p.mu.Lock()
	p.client = buildClient(p.cfg)
	p.mu.Unlock()

	ctx, cancel := context.WithCancel(ctx)
	p.cancel = cancel
	p.wg.Add(1)
	go p.deliveryLoop(ctx)

	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		for {
			p.poll()
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

// Stop stops the poller.
func (p *Poller) Stop() {
	if p.cancel != nil {
		p.cancel()
	}
	p.wg.Wait()
}

// SetFreqMode issues a QSY command through the current radio client.
func (p *Poller) SetFreqMode(hz int64, mode string) error {
	p.mu.Lock()
	client := p.client
	cfg := p.cfg
	p.mu.Unlock()

	if client == nil {
		return fmt.Errorf("no radio client configured")
	}

	// Reverse satellite offset: the incoming frequency is the displayed
	// (offset-corrected) value. Subtract the offset so the radio receives
	// the IF frequency.
	if cfg.SatEnabled && cfg.SatRxOffsetMHz != 0 {
		hz = ReverseSatOffset(hz, cfg.SatRxOffsetMHz)
		debug.Log("[QSY] sat reverse RX offset → %d Hz", hz)
	}

	modes, _ := client.GetModes()
	debug.Log("[QSY] requested mode=%q freq=%d available=%v", mode, hz, modes)

	targetMode := SelectMode(mode, hz, modes)
	debug.Log("[QSY] resolved targetMode=%q WavelogPmode=%v", targetMode, cfg.WavelogPmode)

	setMode := ""
	if cfg.WavelogPmode && targetMode != "" {
		setMode = targetMode
	}

	debug.Log("[QSY] sending to radio: freq=%d mode=%q", hz, setMode)
	return client.SetFreqMode(hz, setMode)
}

// SetTxFreq sets the TX (VFO B) frequency in split mode.
func (p *Poller) SetTxFreq(hz int64) error {
	p.mu.Lock()
	client := p.client
	cfg := p.cfg
	p.mu.Unlock()

	if client == nil {
		return fmt.Errorf("no radio client configured")
	}

	// Reverse satellite offset for TX frequency.
	if cfg.SatEnabled && cfg.SatTxOffsetMHz != 0 {
		hz = ReverseSatOffset(hz, cfg.SatTxOffsetMHz)
		debug.Log("[QSY] sat reverse TX offset → %d Hz", hz)
	}

	return client.SetTxFreq(hz)
}

func (p *Poller) poll() {
	p.mu.Lock()
	client := p.client
	cfg := p.cfg
	if client == nil {
		p.mu.Unlock()
		return
	}
	if p.sequence >= maxSafeObservationSequence {
		p.sessionID = newSessionID()
		p.sessionStartedAt = p.now().UTC().Truncate(time.Millisecond)
		p.sequence = 0
	}
	p.sequence++
	sessionID := p.sessionID
	sessionStartedAt := p.sessionStartedAt
	sequence := p.sequence
	correlated := p.correlated
	generation := p.profileGeneration
	radioName := ""
	if cfg != nil {
		radioName = cfg.WavelogRadioname
	}
	p.mu.Unlock()

	status, err := client.GetStatus()
	if err != nil {
		now := p.now().UTC().Truncate(time.Millisecond)
		p.mu.Lock()
		if generation != p.profileGeneration || sessionID != p.sessionID {
			p.mu.Unlock()
			return
		}
		report := !p.failed || p.lastFailureReport.IsZero() || now.Sub(p.lastFailureReport) >= p.reportInterval
		p.failed = true
		if report {
			p.lastFailureReport = now
		}
		p.mu.Unlock()
		if report && p.onPoll != nil && correlated {
			p.onPoll(PollEvent{State: PollFailed, SessionID: sessionID, SessionStartedAt: sessionStartedAt, Sequence: sequence, EventAt: now, FailureCode: "RADIO_POLL_FAILED", Correlated: true})
		}
		return
	}
	now := p.now().UTC().Truncate(time.Millisecond)

	// Optionally zero out power. HamlibClient already skips the RFPOWER read
	// when this is set; this is the backstop that also covers FLRig, which
	// reports power unconditionally.
	if cfg != nil && cfg.IgnorePwr {
		status.Power = 0
	}

	// Check for unsupported float power values
	if math.IsNaN(status.Power) || math.IsInf(status.Power, 0) {
		status.Power = 0
	}

	// Apply satellite/transverter frequency offsets.
	if cfg != nil {
		ApplySatOffsets(&status, cfg)
	}

	p.mu.Lock()
	if generation != p.profileGeneration || sessionID != p.sessionID {
		p.mu.Unlock()
		return
	}
	changed := !statusEqual(status, p.lastStatus)
	recovered := p.failed
	reportDue := p.lastReport.IsZero() || now.Sub(p.lastReport) >= p.reportInterval
	p.failed = false
	if changed || recovered || reportDue {
		p.lastStatus = status
		p.lastReport = now
		p.mu.Unlock()

		event := PollEvent{State: PollObserved, Status: status, RadioName: radioName, SessionID: sessionID, SessionStartedAt: sessionStartedAt, Sequence: sequence, ObservedAt: now, EventAt: now, Correlated: correlated}
		if p.onPoll != nil {
			p.onPoll(event)
		}

		// Send to Wavelog.
		if p.wlClient != nil {
			data := wavelog.RadioData{
				Frequency: int64(math.Round(status.FreqA)),
				Mode:      status.Mode,
				Power:     status.Power,
				Split:     status.Split,
			}
			if correlated {
				data.SourceProtocol = wavelog.RadioObservationProtocol
				data.SourceSessionID = sessionID
				data.SourceSessionStartedAt = sessionStartedAt
				data.SourceSequence = sequence
				data.SourceObservedAt = now
			}
			if status.Split {
				data.FrequencyRx = int64(math.Round(status.FreqB))
				data.ModeRx = status.ModeB
			}
			if cfg != nil && cfg.SatEnabled {
				data.PropMode = "SAT"
				data.SatName = cfg.SatName
				data.SatMode = cfg.SatMode
			}
			if correlated {
				p.enqueueDelivery(radioDelivery{data: data, event: event, profile: cfg, generation: generation})
			} else {
				_ = p.wlClient.UpdateRadioStatusForProfile(data, cfg)
			}
		}
	} else {
		p.mu.Unlock()
	}
}

func (p *Poller) enqueueDelivery(next radioDelivery) {
	select {
	case p.delivery <- next:
		return
	default:
	}
	select {
	case dropped := <-p.delivery:
		if p.onPoll != nil {
			dropped.event.State = ServerDeliveryFailed
			dropped.event.EventAt = p.now()
			dropped.event.FailureCode = "SERVER_DELIVERY_COALESCED"
			p.onPoll(dropped.event)
		}
	default:
	}
	select {
	case p.delivery <- next:
	default:
	}
}

func (p *Poller) deliveryLoop(ctx context.Context) {
	defer p.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case delivery := <-p.delivery:
			p.mu.Lock()
			if delivery.generation != p.profileGeneration {
				p.mu.Unlock()
				continue
			}
			requestContext, cancel := context.WithCancel(ctx)
			p.deliveryCancel = cancel
			p.mu.Unlock()

			err := p.wlClient.UpdateRadioObservationForProfile(requestContext, delivery.data, delivery.profile)
			cancel()
			p.mu.Lock()
			stale := delivery.generation != p.profileGeneration
			if !stale {
				p.deliveryCancel = nil
			}
			p.mu.Unlock()
			if stale {
				continue
			}
			delivery.event.EventAt = p.now()
			if err != nil {
				delivery.event.State = ServerDeliveryFailed
				delivery.event.FailureCode = "SERVER_DELIVERY_FAILED"
			} else {
				delivery.event.State = ServerDelivered
			}
			if p.onPoll != nil {
				p.onPoll(delivery.event)
			}
		}
	}
}

func statusEqual(a, b RigStatus) bool {
	return a.FreqA == b.FreqA &&
		a.FreqB == b.FreqB &&
		a.Mode == b.Mode &&
		a.ModeB == b.ModeB &&
		a.Power == b.Power &&
		a.Split == b.Split &&
		a.PTT == b.PTT
}

func buildClient(cfg *config.Profile) RadioClient {
	if cfg == nil {
		return nil
	}
	switch {
	case cfg.FlrigEna:
		return NewFLRig(cfg.FlrigHost, cfg.FlrigPort)
	case cfg.HamlibEna:
		// Connect to rigctld via TCP for both internal (managed) and external modes
		// Internal mode: hamlib manager starts rigctld, poller connects to it
		// External mode: user runs rigctld manually, poller connects to it
		return NewHamlib(cfg.HamlibHost, cfg.HamlibPort, !cfg.IgnorePwr, cfg.HamlibMaxPower)
	default:
		return nil
	}
}
