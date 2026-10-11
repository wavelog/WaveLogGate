package wavelog

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"waveloggate/internal/config"
)

func TestV2RadioUpdateCarriesObservationIdentity(t *testing.T) {
	var payload radioPayload
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/radio" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer wl2_test" {
			t.Fatalf("missing bearer token")
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"observation": map[string]any{
			"protocol": payload.SourceProtocol, "session_id": payload.SourceSessionID,
			"session_started_at": payload.SourceSessionStartedAt, "sequence": payload.SourceSequence,
			"observed_at": payload.SourceObservedAt, "payload_hash": radioObservationPayloadHash(payload),
		}}})
	}))
	defer server.Close()

	client := New(&config.Profile{WavelogURL: server.URL, WavelogKey: "wl2_test", WavelogRadioname: "RGO One"}, "test")
	observedAt := time.Date(2026, 10, 3, 2, 50, 0, 123000000, time.UTC)
	startedAt := observedAt.Add(-time.Minute)
	err := client.UpdateRadioObservation(context.Background(), RadioData{Frequency: 14042300, Mode: "USB", SourceProtocol: RadioObservationProtocol, SourceSessionID: "session-a", SourceSessionStartedAt: startedAt, SourceSequence: 42, SourceObservedAt: observedAt})
	if err != nil {
		t.Fatal(err)
	}
	if payload.SourceSessionID != "session-a" || payload.SourceSequence != 42 || payload.SourceObservedAt != observedAt.Format(time.RFC3339Nano) || payload.SourceSessionStartedAt != startedAt.Format(time.RFC3339Nano) {
		t.Fatalf("missing observation correlation: %#v", payload)
	}
}

func TestRadioUpdateReportsServerDeliveryFailure(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	client := New(&config.Profile{WavelogURL: server.URL, WavelogKey: "wl2_test", WavelogRadioname: "RGO One"}, "test")
	if err := client.UpdateRadioStatus(RadioData{Frequency: 14042300, Mode: "USB"}); err == nil {
		t.Fatal("non-2xx radio delivery must fail")
	}
}

func TestObservationDeliveryRejectsHTTPFailuresAndUnrelatedSuccess(t *testing.T) {
	statuses := []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests, http.StatusInternalServerError}
	for _, status := range statuses {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, http.StatusText(status), status)
			}))
			defer server.Close()
			client := New(&config.Profile{WavelogURL: server.URL, WavelogKey: "wl2_test", WavelogRadioname: "RGO One"}, "test")
			now := time.Now().UTC()
			if err := client.UpdateRadioObservation(context.Background(), RadioData{Frequency: 14042300, Mode: "USB", SourceProtocol: RadioObservationProtocol, SourceSessionID: "session-a", SourceSessionStartedAt: now.Add(-time.Second), SourceSequence: 1, SourceObservedAt: now}); err == nil {
				t.Fatalf("HTTP %d must not acknowledge delivery", status)
			}
		})
	}
	t.Run("unrelated 2xx", func(t *testing.T) {
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"status":"updated"}}`))
		}))
		defer server.Close()
		client := New(&config.Profile{WavelogURL: server.URL, WavelogKey: "wl2_test", WavelogRadioname: "RGO One"}, "test")
		now := time.Now().UTC()
		if err := client.UpdateRadioObservation(context.Background(), RadioData{Frequency: 14042300, Mode: "USB", SourceProtocol: RadioObservationProtocol, SourceSessionID: "session-a", SourceSessionStartedAt: now.Add(-time.Second), SourceSequence: 1, SourceObservedAt: now}); err == nil {
			t.Fatal("unrelated success body must not acknowledge correlated delivery")
		}
	})
}

func TestV1RadioUpdateOmitsObservationExtension(t *testing.T) {
	var payload map[string]any
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/radio" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := New(&config.Profile{WavelogURL: server.URL, WavelogKey: "legacy-key", WavelogRadioname: "RGO One"}, "test")
	err := client.UpdateRadioStatus(RadioData{
		Frequency:        14042300,
		Mode:             "USB",
		SourceSessionID:  "session-a",
		SourceSequence:   42,
		SourceObservedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"source_session_id", "source_sequence", "source_observed_at"} {
		if _, present := payload[field]; present {
			t.Fatalf("legacy v1 payload gained %s: %#v", field, payload)
		}
	}
	if payload["key"] != "legacy-key" {
		t.Fatalf("legacy key handling changed: %#v", payload)
	}
}
