//go:build windows

package hamlib

import "os"

func terminateProcess(p *os.Process) error {
	// Windows has no SIGTERM; Kill is the closest equivalent.
	return p.Kill()
}
