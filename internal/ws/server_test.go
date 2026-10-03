package ws

import (
	"encoding/json"
	"fmt"
	"testing"
)

func decodeMessage(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestReconnectMarksRetainedStatusCachedWithoutChangingIdentity(t *testing.T) {
	hub := NewHub()
	hub.BroadcastStatus(RadioStatusMsg{Type: "radio_status", Frequency: 14042300, Mode: "USB", Radio: "RGO One", Timestamp: 1000, Protocol: "shackcq.radio-observation.v1", SessionID: "session-a", Sequence: 7})

	client := &client{send: make(chan []byte, 4)}
	hub.add(client)
	<-client.send // welcome
	replayed := decodeMessage(t, <-client.send)
	if replayed["cached"] != true || replayed["session_id"] != "session-a" || replayed["sequence"] != float64(7) || replayed["timestamp"] != float64(1000) {
		t.Fatalf("replay changed observation identity: %#v", replayed)
	}

	hub.BroadcastStatus(RadioStatusMsg{Type: "radio_status", Frequency: 14042300, Mode: "USB", Radio: "RGO One", Timestamp: 2000, SessionID: "session-a", Sequence: 8})
	live := decodeMessage(t, <-client.send)
	if _, present := live["cached"]; present {
		t.Fatalf("live observation must not be marked cached: %#v", live)
	}
	if live["sequence"] != float64(8) || live["timestamp"] != float64(2000) {
		t.Fatalf("unexpected live observation: %#v", live)
	}
}

func TestFailureEvidenceIsTransientAndNeverReplayedAsObservation(t *testing.T) {
	hub := NewHub()
	existing := &client{send: make(chan []byte, 4)}
	hub.add(existing)
	<-existing.send
	hub.BroadcastEvidence(RadioEvidenceMsg{Type: "radio_poll_status", State: "poll_failed", SessionID: "session-a", Sequence: 9, Timestamp: 3000, FailureCode: "RADIO_POLL_FAILED"})
	failure := decodeMessage(t, <-existing.send)
	if failure["state"] != "poll_failed" || failure["sequence"] != float64(9) {
		t.Fatalf("unexpected failure evidence: %#v", failure)
	}

	reconnected := &client{send: make(chan []byte, 2)}
	hub.add(reconnected)
	<-reconnected.send
	select {
	case replayed := <-reconnected.send:
		t.Fatalf("failure evidence must not be replayed: %s", replayed)
	default:
	}
}

type referenceDualPathCursor struct {
	seen          map[string]uint8
	watermark     map[string]uint64
	activeSession string
	retired       map[string]bool
}

func (c *referenceDualPathCursor) offer(path, session string, sequence uint64, observed, cached bool) bool {
	if c.retired == nil {
		c.retired = map[string]bool{}
	}
	if !observed || cached || session == "" || sequence == 0 || sequence <= c.watermark[session] || c.retired[session] {
		return false
	}
	key := fmt.Sprintf("%s:%d", session, sequence)
	if path == "local" {
		c.seen[key] |= 1
	} else if path == "server" {
		c.seen[key] |= 2
	} else {
		return false
	}
	if c.seen[key] != 3 {
		return false
	}
	c.watermark[session] = sequence
	if c.activeSession != "" && c.activeSession != session {
		c.retired[c.activeSession] = true
	}
	c.activeSession = session
	return true
}

func TestReferenceDualPathAcceptanceRejectsReplayReorderingAndDeliveryLoss(t *testing.T) {
	cursor := referenceDualPathCursor{seen: map[string]uint8{}, watermark: map[string]uint64{}}

	if cursor.offer("local", "session-a", 7, true, true) {
		t.Fatal("cached browser reconnect advanced freshness")
	}
	if cursor.offer("server", "session-a", 7, true, false) {
		t.Fatal("one credential-bound path advanced freshness")
	}
	if cursor.offer("local", "session-a", 8, true, false) || !cursor.offer("server", "session-a", 8, true, false) {
		t.Fatal("matching live local and server evidence was not accepted")
	}
	if cursor.offer("local", "session-a", 7, true, false) {
		t.Fatal("delayed older observation advanced freshness")
	}
	if cursor.offer("server", "session-a", 9, true, false) {
		t.Fatal("local WSS delivery loss advanced freshness")
	}
	if cursor.offer("local", "session-a", 10, true, false) {
		t.Fatal("credential-bound server delivery loss advanced freshness")
	}
	if cursor.offer("local", "session-a", 11, true, false) || !cursor.offer("server", "session-a", 11, true, false) {
		t.Fatal("same-frequency recovery did not become acceptable with matching evidence")
	}
	if cursor.offer("local", "session-b", 1, true, false) || !cursor.offer("server", "session-b", 1, true, false) {
		t.Fatal("new client session was not accepted after both paths matched")
	}
	if cursor.offer("local", "session-b", 2, false, false) || cursor.offer("server", "session-b", 2, false, false) {
		t.Fatal("failed poll advanced freshness")
	}
}

func TestReferenceDualPathAcceptanceRejectsDelayedRetiredSession(t *testing.T) {
	cursor := referenceDualPathCursor{seen: map[string]uint8{}, watermark: map[string]uint64{}}
	if cursor.offer("local", "session-a", 7, true, false) || !cursor.offer("server", "session-a", 7, true, false) {
		t.Fatal("initial session was not accepted")
	}
	if cursor.offer("local", "session-a", 8, true, false) {
		t.Fatal("one-path observation advanced freshness")
	}
	if cursor.offer("local", "session-b", 1, true, false) || !cursor.offer("server", "session-b", 1, true, false) {
		t.Fatal("replacement session was not accepted")
	}
	if cursor.offer("server", "session-a", 8, true, false) {
		t.Fatal("delayed evidence from a retired session advanced freshness")
	}
}
