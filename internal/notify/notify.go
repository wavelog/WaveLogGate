// Package notify fires desktop system notifications for logged QSOs.
// Best-effort only: failures are logged and never propagate to callers.
package notify

import (
	"fmt"
	"strings"

	"waveloggate/internal/debug"
	"waveloggate/internal/wavelog"

	"github.com/gen2brain/beeep"
)

func init() {
	beeep.AppName = "WavelogGate"
}

// QSOResult shows a system notification for a QSO logging result.
// It never returns an error and recovers any panic from the underlying
// platform notifier so a broken notification can never affect logging.
func QSOResult(r *wavelog.QSOResult) {
	defer func() {
		if rec := recover(); rec != nil {
			debug.Log("[NOTIFY] recovered panic: %v", rec)
		}
	}()
	if r == nil {
		return
	}

	var title, body string
	if r.Success {
		title = "QSO logged"
		parts := []string{}
		if r.Call != "" {
			parts = append(parts, r.Call)
		}
		bandMode := strings.TrimSpace(r.Band + " " + r.Mode)
		if bandMode != "" {
			parts = append(parts, bandMode)
		}
		if r.RstSent != "" || r.RstRcvd != "" {
			parts = append(parts, r.RstSent+"/"+r.RstRcvd)
		}
		if r.TimeOn != "" {
			parts = append(parts, r.TimeOn)
		}
		body = "✓ " + strings.Join(parts, " · ")
	} else {
		title = "QSO not logged"
		call := r.Call
		if call == "" {
			call = "QSO"
		}
		reason := r.Reason
		if reason == "" {
			reason = "unknown error"
		}
		body = fmt.Sprintf("✗ %s: %s", call, reason)
	}

	if err := beeep.Notify(title, body, ""); err != nil {
		debug.Log("[NOTIFY] %v", err)
	}
}
