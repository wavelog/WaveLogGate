package cert

import (
	"crypto/sha1"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// IsCertInstalled reports whether the exact Root CA (matched by SHA-1 fingerprint)
// is present in any macOS keychain (System or Login).
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

	// security -Z prints "SHA-1 hash: AABBCC..." — match against that.
	sum := sha1.Sum(caCert.Raw)
	fp := fmt.Sprintf("%X", sum)

	// Search all keychains (no keychain path argument = default search list).
	out, err := exec.Command("/usr/bin/security", "find-certificate",
		"-c", "WavelogGate CA", "-Z", "-a").Output()
	if err != nil || len(out) == 0 {
		return false
	}
	return strings.Contains(strings.ToUpper(string(out)), strings.ToUpper(fp))
}

// Install writes a .mobileconfig profile containing the Root CA and opens it
// with the macOS profile installer (System Preferences / System Settings).
// The user confirms installation with their password — no osascript hacks needed.
func Install(caCertPath string) InstallResult {
	rawPEM, err := os.ReadFile(caCertPath)
	if err != nil {
		return InstallResult{Success: false, Message: "CA certificate could not be read: " + err.Error()}
	}
	block, _ := pem.Decode(rawPEM)
	if block == nil {
		return InstallResult{Success: false, Message: "Invalid PEM format"}
	}
	certB64 := base64.StdEncoding.EncodeToString(block.Bytes)

	profile := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>PayloadContent</key>
	<array>
		<dict>
			<key>PayloadCertificateFileName</key>
			<string>WavelogGate CA</string>
			<key>PayloadContent</key>
			<data>%s</data>
			<key>PayloadDescription</key>
			<string>WavelogGate Root CA — allows HTTPS/WSS on localhost</string>
			<key>PayloadDisplayName</key>
			<string>WavelogGate CA</string>
			<key>PayloadIdentifier</key>
			<string>org.wavelog.waveloggate.cert</string>
			<key>PayloadType</key>
			<string>com.apple.security.root</string>
			<key>PayloadUUID</key>
			<string>A1B2C3D4-E5F6-7890-ABCD-EF1234567890</string>
			<key>PayloadVersion</key>
			<integer>1</integer>
		</dict>
	</array>
	<key>PayloadDescription</key>
	<string>Installs the WavelogGate Root CA for HTTPS/WSS on localhost</string>
	<key>PayloadDisplayName</key>
	<string>WavelogGate</string>
	<key>PayloadIdentifier</key>
	<string>org.wavelog.waveloggate</string>
	<key>PayloadRemovalDisallowed</key>
	<false/>
	<key>PayloadType</key>
	<string>Configuration</string>
	<key>PayloadUUID</key>
	<string>B2C3D4E5-F6A7-8901-BCDE-F12345678901</string>
	<key>PayloadVersion</key>
	<integer>1</integer>
</dict>
</plist>`, certB64)

	profilePath := filepath.Join(filepath.Dir(caCertPath), "waveloggate-ca.mobileconfig")
	if err := os.WriteFile(profilePath, []byte(profile), 0o644); err != nil {
		return InstallResult{Success: false, Message: "Profile could not be written: " + err.Error()}
	}

	if out, err := exec.Command("/usr/bin/open", profilePath).CombinedOutput(); err != nil {
		return InstallResult{
			Success: false,
			Message: "Profile could not be opened: " + strings.TrimSpace(string(out)),
			Command: `open "` + profilePath + `"`,
		}
	}

	return InstallResult{
		Success: true,
		Message: `Profile installer opened. Please install in System Settings — search for "Profiles" and install the pending profile, then restart your browser.`,
	}
}
