//go:build !windows

package startmenu

// EnsureShortcut is a no-op on non-Windows platforms.
func EnsureShortcut(_ string) error {
	return nil
}
