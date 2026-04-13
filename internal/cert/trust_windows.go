package cert

import (
	"crypto/sha1"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

const createNoWindow = 0x08000000

func hiddenCmd(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindow,
	}
	return cmd
}

// IsCertInstalled reports whether the exact Root CA (matched by SHA-1 thumbprint)
// is present in the Windows Trusted Root Certification Authorities store.
func IsCertInstalled(caCertPath string) bool {
	rawPEM, err := os.ReadFile(caCertPath)
	if err != nil {
		return false
	}
	block, _ := pem.Decode(rawPEM)
	if block == nil {
		return false
	}
	caCert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return false
	}

	// Windows certutil uses the SHA-1 thumbprint (no colons, uppercase).
	thumbprint := fmt.Sprintf("%X", sha1.Sum(caCert.Raw))

	out, err := hiddenCmd("certutil", "-store", "Root").Output()
	if err != nil || len(out) == 0 {
		return false
	}
	return strings.Contains(strings.ToUpper(string(out)), strings.ToUpper(thumbprint))
}

// Install adds the Root CA to the Windows Trusted Root store via a UAC-elevated
// certutil call. The UAC dialog appears automatically — no manual steps needed.
func Install(caCertPath string) InstallResult {
	// PowerShell: start certutil elevated, wait for it to finish.
	psArgs := fmt.Sprintf(
		"Start-Process -FilePath certutil -ArgumentList '-addstore','Root','%s' -Verb RunAs -Wait",
		strings.ReplaceAll(caCertPath, `'`, `''`),
	)

	out, err := hiddenCmd("powershell", "-NoProfile", "-NonInteractive",
		"-Command", psArgs).CombinedOutput()
	if err != nil {
		return InstallResult{
			Success: false,
			Message: "Installation failed: " + strings.TrimSpace(string(out)),
			Command: `certutil -addstore Root "` + caCertPath + `"`,
		}
	}
	return InstallResult{
		Success: true,
		Message: "Certificate installed. Please restart your browser.",
	}
}
