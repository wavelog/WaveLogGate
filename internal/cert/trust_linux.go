package cert

import (
	"crypto/sha1"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// distro describes a Linux distribution's CA trust store layout.
type distro struct {
	certDir       string // where to copy the CA cert file
	updateCmd     string // command to run after copying (requires root)
	updateArgs    []string
	certExtension string // required file extension (e.g. ".crt")
}

var knownDistros = []distro{
	{
		// Debian / Ubuntu
		certDir:       "/usr/local/share/ca-certificates",
		updateCmd:     "update-ca-certificates",
		certExtension: ".crt",
	},
	{
		// Fedora / RHEL / CentOS
		certDir:       "/etc/pki/ca-trust/source/anchors",
		updateCmd:     "update-ca-trust",
		certExtension: ".crt",
	},
	{
		// Arch Linux
		certDir:       "/etc/ca-certificates/trust-source/anchors",
		updateCmd:     "update-ca-trust",
		certExtension: ".crt",
	},
	{
		// openSUSE
		certDir:       "/etc/pki/trust/anchors",
		updateCmd:     "update-ca-certificates",
		certExtension: ".pem",
	},
}

// activeDistro returns the first distro whose certDir exists, or nil.
func activeDistro() *distro {
	for i := range knownDistros {
		if _, err := os.Stat(knownDistros[i].certDir); err == nil {
			return &knownDistros[i]
		}
	}
	return nil
}

// nssDBPath returns the path to the user's Chrome/Chromium NSS database, or "".
func nssDBPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	p := filepath.Join(home, ".pki", "nssdb")
	if _, err := os.Stat(p); err != nil {
		return ""
	}
	return p
}

// sha1Hex returns the uppercase hex SHA-1 fingerprint of a DER cert.
func sha1Hex(der []byte) string {
	sum := sha1.Sum(der)
	return fmt.Sprintf("%X", sum)
}

// IsCertInstalled reports whether the exact Root CA is present in either the
// system trust store or the user's NSS database.
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
	fp := sha1Hex(caCert.Raw)

	// Check system trust bundle (Debian/Ubuntu path as canonical check).
	bundles := []string{
		"/etc/ssl/certs/ca-certificates.crt",                // Debian/Ubuntu
		"/etc/pki/ca-trust/extracted/pem/tls-ca-bundle.pem", // Fedora/RHEL
		"/etc/ssl/ca-bundle.pem",                            // openSUSE
	}
	for _, bundle := range bundles {
		if certInBundle(bundle, fp) {
			return true
		}
	}

	// Check NSS database (Chrome/Chromium).
	if db := nssDBPath(); db != "" {
		if certInNSS(db, fp) {
			return true
		}
	}

	return false
}

// certInBundle scans a PEM bundle file for a cert matching the given SHA-1 fingerprint.
func certInBundle(bundlePath, fp string) bool {
	raw, err := os.ReadFile(bundlePath)
	if err != nil {
		return false
	}
	rest := raw
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if sha1Hex(block.Bytes) == fp {
			return true
		}
	}
	return false
}

// certInNSS checks whether a cert with the given SHA-1 fingerprint exists in
// the NSS database at dbPath using certutil.
func certInNSS(dbPath, fp string) bool {
	out, err := exec.Command("certutil", "-L", "-d", "sql:"+dbPath).Output()
	if err != nil {
		return false
	}
	// certutil -L lists nicknames; we look for our label.
	if !strings.Contains(string(out), "WavelogGate CA") {
		return false
	}
	// Export the cert and compare fingerprints to be sure.
	der, err := exec.Command("certutil", "-L", "-d", "sql:"+dbPath,
		"-n", "WavelogGate CA", "-a").Output()
	if err != nil {
		return false
	}
	block, _ := pem.Decode(der)
	if block == nil {
		return false
	}
	return sha1Hex(block.Bytes) == fp
}

// Install copies the Root CA into the system trust store (via pkexec for
// privilege elevation) and imports it into the user's NSS database for
// Chrome/Chromium. Returns a combined result.
func Install(caCertPath string) InstallResult {
	var msgs []string
	var cmds []string
	anyFailed := false

	// --- System trust store ---
	d := activeDistro()
	if d == nil {
		anyFailed = true
		cmds = append(cmds, "# Could not detect Linux distribution trust store")
	} else {
		destName := "waveloggate-ca" + d.certExtension
		destPath := filepath.Join(d.certDir, destName)

		// Build a shell one-liner to copy + update (run via pkexec).
		shellCmd := fmt.Sprintf("cp %q %q && %s", caCertPath, destPath, d.updateCmd)
		if len(d.updateArgs) > 0 {
			shellCmd += " " + strings.Join(d.updateArgs, " ")
		}

		out, err := exec.Command("pkexec", "sh", "-c", shellCmd).CombinedOutput()
		if err != nil {
			anyFailed = true
			cmds = append(cmds,
				fmt.Sprintf(`sudo cp "%s" "%s" && sudo %s`, caCertPath, destPath, d.updateCmd),
			)
			msgs = append(msgs, "System store: "+strings.TrimSpace(string(out)))
		} else {
			msgs = append(msgs, "System trust store updated.")
		}
	}

	// --- NSS database (Chrome/Chromium) ---
	if db := nssDBPath(); db != "" {
		// Remove any stale entry first, ignore errors.
		exec.Command("certutil", "-D", "-d", "sql:"+db, "-n", "WavelogGate CA").Run() //nolint:errcheck

		out, err := exec.Command("certutil", "-A",
			"-d", "sql:"+db,
			"-t", "C,,",
			"-n", "WavelogGate CA",
			"-i", caCertPath).CombinedOutput()
		if err != nil {
			anyFailed = true
			cmds = append(cmds,
				fmt.Sprintf(`certutil -A -d sql:%s -t "C,," -n "WavelogGate CA" -i "%s"`, db, caCertPath),
			)
			msgs = append(msgs, "NSS (Chrome): "+strings.TrimSpace(string(out)))
		} else {
			msgs = append(msgs, "Chrome/Chromium NSS database updated.")
		}
	} else {
		msgs = append(msgs, "No Chrome NSS database found (~/.pki/nssdb) — skipped.")
	}

	return InstallResult{
		Success: !anyFailed,
		Message: strings.Join(msgs, " "),
		Command: strings.Join(cmds, "\n"),
	}
}
