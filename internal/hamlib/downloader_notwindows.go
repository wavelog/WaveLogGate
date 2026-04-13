//go:build !windows

package hamlib

import (
	"context"
	"fmt"
)

// listWindowsCOMPorts is a no-op on non-Windows platforms.
func listWindowsCOMPorts() []string {
	return nil
}

// Download is not supported on non-Windows platforms.
// Use the platform package manager to install Hamlib instead.
func Download(_ context.Context, _ chan<- int) error {
	return fmt.Errorf("automatic download is only available on Windows; %s", InstallGuide())
}

// CanDownload reports whether automatic rigctld download is supported on this platform.
func CanDownload() bool { return false }
