package wavelog

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"waveloggate/internal/config"
)

func tokenServer(t *testing.T, data any, status int) *httptest.Server {
	t.Helper()
	return httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/token" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
}

func TestRadioObservationCapabilityRequiresExactOptIn(t *testing.T) {
	server := tokenServer(t, map[string]any{
		"name": "fixture", "owner": "operator", "scopes": RequiredScopes,
		"extensions": map[string]any{RadioObservationProtocol: map[string]any{
			"version": 1, "report_interval_ms": 5000, "acknowledgement": "echo", "max_clock_skew_ms": 300000,
		}},
	}, http.StatusOK)
	defer server.Close()
	client := New(&config.Profile{WavelogURL: server.URL, WavelogKey: "wl2_fixture"}, "test")
	offer, err := client.RadioObservationCapability()
	if err != nil || offer == nil || offer.ReportIntervalMS != 5000 {
		t.Fatalf("exact extension was not negotiated: offer=%+v err=%v", offer, err)
	}
}

func TestV2TokenWithoutCapabilityKeepsLegacyBehaviour(t *testing.T) {
	server := tokenServer(t, map[string]any{"name": "fixture", "owner": "operator", "scopes": RequiredScopes}, http.StatusOK)
	defer server.Close()
	client := New(&config.Profile{WavelogURL: server.URL, WavelogKey: "wl2_fixture"}, "test")
	offer, err := client.RadioObservationCapability()
	if err != nil || offer != nil {
		t.Fatalf("v2 token alone enabled the extension: offer=%+v err=%v", offer, err)
	}
}

func TestMalformedCapabilityFailsClosed(t *testing.T) {
	server := tokenServer(t, map[string]any{
		"name": "fixture", "owner": "operator", "scopes": RequiredScopes,
		"extensions": map[string]any{RadioObservationProtocol: map[string]any{
			"version": 2, "report_interval_ms": 100, "acknowledgement": "ignored", "max_clock_skew_ms": 0,
		}},
	}, http.StatusOK)
	defer server.Close()
	client := New(&config.Profile{WavelogURL: server.URL, WavelogKey: "wl2_fixture"}, "test")
	if offer, err := client.RadioObservationCapability(); err == nil || offer != nil {
		t.Fatalf("malformed capability did not fail closed: offer=%+v err=%v", offer, err)
	}
}
