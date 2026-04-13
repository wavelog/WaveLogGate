//go:build windows

package hamlib

import (
	"os/exec"
	"syscall"
)

// setCmdAttrs configures the exec.Cmd so rigctld runs as a hidden background
// process on Windows.
//
// Without CREATE_NO_WINDOW, Windows tries to allocate a console for the child
// process and dispatches WM_PARENTNOTIFY / console-allocation messages to the
// parent's window procedure (the WebView2 host).  When those messages pile up
// faster than the Wails message loop drains them, WebView2 input handling
// freezes — clicks stop registering.
func setCmdAttrs(cmd *exec.Cmd) {
	const createNoWindow = 0x08000000
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindow | syscall.CREATE_NEW_PROCESS_GROUP,
	}
}
