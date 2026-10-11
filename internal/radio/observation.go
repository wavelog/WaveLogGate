package radio

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

// PollEventState identifies evidence produced by the radio poller. Only
// PollObserved is a successful radio observation. Transport and delivery
// events must never be used to advance radio freshness.
type PollEventState string

const (
	PollObserved         PollEventState = "observed"
	PollFailed           PollEventState = "poll_failed"
	ServerDelivered      PollEventState = "server_delivered"
	ServerDeliveryFailed PollEventState = "server_delivery_failed"
)

// PollEvent identifies one poll attempt inside one client process session.
// Sequence is monotonic within SessionID and advances for every attempt, so a
// recovery at an unchanged frequency remains distinguishable from cached data.
type PollEvent struct {
	State            PollEventState
	Status           RigStatus
	RadioName        string
	SessionID        string
	SessionStartedAt time.Time
	Sequence         uint64
	ObservedAt       time.Time
	EventAt          time.Time
	FailureCode      string
	Correlated       bool
}

// PollCallback receives bounded evidence about poll and delivery outcomes.
type PollCallback func(event PollEvent)

func newSessionID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err == nil {
		return hex.EncodeToString(value[:])
	}
	return fmt.Sprintf("fallback-%d", time.Now().UnixNano())
}
