package debug

import "log"

// Verbose enables debug logging when set to true (via -vv flag).
var Verbose bool

// Log prints a formatted message when Verbose is true.
func Log(format string, args ...interface{}) {
	if Verbose {
		log.Printf(format, args...)
	}
}
