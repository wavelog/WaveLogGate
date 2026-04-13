//go:build !windows

package hamlib

import (
	"os"
	"syscall"
)

func terminateProcess(p *os.Process) error {
	return p.Signal(syscall.SIGTERM)
}
