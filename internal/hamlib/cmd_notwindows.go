//go:build !windows

package hamlib

import "os/exec"

// setCmdAttrs is a no-op on non-Windows platforms.
func setCmdAttrs(cmd *exec.Cmd) {}
