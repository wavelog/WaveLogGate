package main

import (
	"context"
	"time"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"waveloggate/internal/config"
)

// restoreWindowPosition moves the window to its last saved position, if that
// position is still on screen. Size is intentionally not restored: the
// frontend manages it (full / mini mode).
func restoreWindowPosition(ctx context.Context) {
	ws, ok := config.LoadWindow()
	if !ok {
		return
	}
	// Window coordinates are relative to the current screen. Reject positions
	// that would leave the window (almost) off-screen, e.g. after a monitor
	// was unplugged.
	if screens, err := wailsruntime.ScreenGetAll(ctx); err == nil {
		for _, s := range screens {
			if s.IsCurrent {
				if ws.X < -100 || ws.Y < 0 || ws.X > s.Width-100 || ws.Y > s.Height-60 {
					return
				}
				break
			}
		}
	}
	wailsruntime.WindowSetPosition(ctx, ws.X, ws.Y)
}

// trackWindowPosition saves the window position whenever it changes.
// Polling is used because on macOS closing only hides the window (the app
// keeps running), so a save-on-shutdown hook alone would miss moves.
func trackWindowPosition(ctx context.Context) {
	last, _ := config.LoadWindow()
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if wailsruntime.WindowIsMinimised(ctx) || wailsruntime.WindowIsFullscreen(ctx) {
				continue
			}
			x, y := wailsruntime.WindowGetPosition(ctx)
			cur := config.WindowState{X: x, Y: y}
			if cur != last {
				if config.SaveWindow(cur) == nil {
					last = cur
				}
			}
		}
	}
}
