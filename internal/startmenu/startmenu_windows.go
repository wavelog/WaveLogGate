package startmenu

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// EnsureShortcut creates a per-user Start Menu shortcut for the running
// executable if one does not already exist. No UAC elevation is required
// because the target is inside %APPDATA%.
func EnsureShortcut(appName string) error {
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("get executable path: %w", err)
	}

	appData := os.Getenv("APPDATA")
	if appData == "" {
		return fmt.Errorf("APPDATA environment variable not set")
	}

	linkPath := filepath.Join(appData, `Microsoft\Windows\Start Menu\Programs`, appName+".lnk")

	exeEscaped := strings.ReplaceAll(exePath, `'`, `''`)
	linkEscaped := strings.ReplaceAll(linkPath, `'`, `''`)
	nameEscaped := strings.ReplaceAll(appName, `'`, `''`)

	// If the shortcut already exists and points to the current exe, nothing to do.
	ps := fmt.Sprintf(
		`$ws = New-Object -ComObject WScript.Shell; `+
			`$link = '%s'; `+
			`if ((Test-Path $link) -and ($ws.CreateShortcut($link).TargetPath -eq '%s')) { exit 0 }; `+
			`$sc = $ws.CreateShortcut($link); `+
			`$sc.TargetPath = '%s'; `+
			`$sc.Description = '%s'; `+
			`$sc.IconLocation = '%s,0'; `+
			`$sc.Save()`,
		linkEscaped, exeEscaped, exeEscaped, nameEscaped, exeEscaped,
	)

	const createNoWindow = 0x08000000
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", ps)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindow,
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("create shortcut: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
