//go:build windows

package hamlib

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"waveloggate/internal/debug"
)

// listWindowsCOMPorts reads COM ports from the Windows device map registry key.
// This avoids opening any serial ports (which can block indefinitely on
// Bluetooth/USB virtual COM ports).
func listWindowsCOMPorts() []string {
	debug.Log("[HAMLIB] GetSerialPorts called - attempting registry query")

	// Method 1: Try registry query with increased timeout (2 seconds)
	// This is more reliable on slower systems or with antivirus scanning
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	regCmd := exec.CommandContext(ctx, "reg", "query", `HKLM\HARDWARE\DEVICEMAP\SERIALCOMM`)
	regCmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	out, err := regCmd.Output()
	if err != nil {
		debug.Log("[HAMLIB] Registry query failed: %v", err)
		// Provide more specific error message
		if ctx.Err() == context.DeadlineExceeded {
			debug.Log("[HAMLIB] Registry query timed out - system may be slow or have antivirus scanning")
		}
		// Try PowerShell fallback method
		return listWindowsCOMPortsPowerShell()
	}

	debug.Log("[HAMLIB] Registry query succeeded, parsing results")

	// Each value line looks like:
	//   \Device\Serial0    REG_SZ    COM1
	var ports []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if !strings.Contains(line, "REG_SZ") {
			continue
		}
		parts := strings.Fields(line)
		// Last field is the COM port name.
		if len(parts) > 0 {
			last := strings.TrimRight(parts[len(parts)-1], "\r")
			if strings.HasPrefix(strings.ToUpper(last), "COM") {
				ports = append(ports, last)
			}
		}
	}

	if len(ports) > 0 {
		debug.Log("[HAMLIB] Found %d COM ports via registry: %v", len(ports), ports)
		return ports
	}

	debug.Log("[HAMLIB] Registry query returned no ports, trying PowerShell fallback")
	return listWindowsCOMPortsPowerShell()
}

// listWindowsCOMPortsPowerShell uses PowerShell as a fallback method to enumerate COM ports.
// This can work when registry access is restricted.
func listWindowsCOMPortsPowerShell() []string {
	debug.Log("[HAMLIB] Trying PowerShell COM port enumeration")

	// PowerShell command to get COM ports using WMI
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "powershell",
		"-NoProfile", "-NonInteractive",
		"-Command", "[System.IO.Ports.SerialPort]::GetPortNames()")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}

	output, err := cmd.Output()
	if err != nil {
		debug.Log("[HAMLIB] PowerShell COM port query failed: %v", err)
		// Return empty list - no COM ports or no access
		return []string{}
	}

	var ports []string
	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" && strings.HasPrefix(line, "COM") {
			// PowerShell returns lines like "COM1" directly
			ports = append(ports, line)
		}
	}

	if len(ports) > 0 {
		debug.Log("[HAMLIB] Found %d COM ports via PowerShell: %v", len(ports), ports)
		return ports
	}

	debug.Log("[HAMLIB] Both registry and PowerShell methods failed or returned no ports")
	return []string{}
}

// CanDownload reports whether automatic rigctld download is supported on this platform.
func CanDownload() bool { return true }

const githubReleasesURL = "https://api.github.com/repos/Hamlib/Hamlib/releases/latest"

// Download fetches the latest Hamlib Windows x64 ZIP from GitHub Releases,
// extracts rigctld.exe, and places it in the managed hamlib directory.
// Progress (0–100) is reported on progressCh.
func Download(ctx context.Context, progressCh chan<- int) error {
	dir, err := hamlibDir()
	if err != nil {
		return fmt.Errorf("cannot determine hamlib dir: %w", err)
	}
	debug.Log("[HAMLIB] download dir: %s", dir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("cannot create hamlib dir %s: %w", dir, err)
	}

	// Step 1: fetch latest release metadata.
	reportProgress(progressCh, 5)
	client := &http.Client{Timeout: 30 * time.Second}

	debug.Log("[HAMLIB] fetching release info from %s", githubReleasesURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, githubReleasesURL, nil)
	if err != nil {
		return fmt.Errorf("cannot build request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("cannot fetch release info from %s: %w", githubReleasesURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GitHub API returned HTTP %d for %s", resp.StatusCode, githubReleasesURL)
	}

	var release struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
			Size               int64  `json:"size"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return fmt.Errorf("cannot decode release JSON: %w", err)
	}
	debug.Log("[HAMLIB] latest release: %s (%d assets)", release.TagName, len(release.Assets))

	// Step 2: find Windows x64 ZIP asset.
	reportProgress(progressCh, 10)
	var assetURL string
	var assetSize int64
	for _, a := range release.Assets {
		debug.Log("[HAMLIB] asset: %s", a.Name)
		name := strings.ToLower(a.Name)
		if strings.Contains(name, "w64") && strings.HasSuffix(name, ".zip") {
			assetURL = a.BrowserDownloadURL
			assetSize = a.Size
			break
		}
	}
	if assetURL == "" {
		// Log all asset names to help diagnose naming changes.
		var names []string
		for _, a := range release.Assets {
			names = append(names, a.Name)
		}
		return fmt.Errorf("no Windows x64 ZIP found in Hamlib release %s — assets: %s",
			release.TagName, strings.Join(names, ", "))
	}
	debug.Log("[HAMLIB] downloading asset: %s (%d bytes)", assetURL, assetSize)

	// Step 3: download ZIP to a temp file.
	tmpFile, err := os.CreateTemp("", "hamlib-*.zip")
	if err != nil {
		return fmt.Errorf("cannot create temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	req2, err := http.NewRequestWithContext(ctx, http.MethodGet, assetURL, nil)
	if err != nil {
		return fmt.Errorf("cannot build download request: %w", err)
	}
	resp2, err := client.Do(req2)
	if err != nil {
		return fmt.Errorf("download failed for %s: %w", assetURL, err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned HTTP %d for %s", resp2.StatusCode, assetURL)
	}

	// Download with progress reporting.
	var downloaded int64
	buf := make([]byte, 32*1024)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := resp2.Body.Read(buf)
		if n > 0 {
			if _, werr := tmpFile.Write(buf[:n]); werr != nil {
				return fmt.Errorf("write error (disk full?): %w", werr)
			}
			downloaded += int64(n)
			if assetSize > 0 {
				pct := int(10 + 80*downloaded/assetSize)
				reportProgress(progressCh, pct)
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("download error: %w", err)
		}
	}
	tmpFile.Close()
	reportProgress(progressCh, 90)

	// Step 4: extract rigctld.exe from the ZIP.
	destPath := filepath.Join(dir, "rigctld.exe")
	debug.Log("[HAMLIB] extracting rigctld.exe to %s", destPath)
	if err := extractRigctld(tmpFile.Name(), destPath); err != nil {
		return err
	}
	reportProgress(progressCh, 95)

	// Step 5: verify the binary works.
	debug.Log("[HAMLIB] verifying rigctld.exe --version")
	verCmd := exec.Command(destPath, "--version")
	verCmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	out, err := verCmd.Output()
	if err != nil {
		return fmt.Errorf("extracted rigctld.exe failed version check: %w", err)
	}
	debug.Log("[HAMLIB] version output: %s", strings.TrimSpace(string(out)))

	// Save version string.
	versionLine := strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0]
	_ = os.WriteFile(filepath.Join(dir, "version.txt"), []byte(release.TagName+" ("+versionLine+")"), 0644)

	reportProgress(progressCh, 100)
	return nil
}

func extractRigctld(zipPath, destPath string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("cannot open ZIP %s: %w", zipPath, err)
	}
	defer r.Close()

	destDir := filepath.Dir(destPath)
	foundRigctld := false

	for _, f := range r.File {
		baseName := filepath.Base(f.Name)

		// Extract rigctld.exe and all DLL files
		if strings.EqualFold(baseName, "rigctld.exe") || strings.EqualFold(filepath.Ext(baseName), ".dll") {
			destPath := filepath.Join(destDir, baseName)

			rc, err := f.Open()
			if err != nil {
				return fmt.Errorf("cannot read %s from ZIP: %w", baseName, err)
			}

			// Write to temp file first
			tmp := destPath + ".tmp"
			out, err := os.Create(tmp)
			if err != nil {
				rc.Close()
				return fmt.Errorf("cannot create %s: %w", tmp, err)
			}

			if _, err := io.Copy(out, rc); err != nil {
				out.Close()
				rc.Close()
				os.Remove(tmp)
				return fmt.Errorf("cannot write %s: %w", baseName, err)
			}
			out.Close()
			rc.Close()

			// Rename temp file to final destination
			if err := os.Rename(tmp, destPath); err != nil {
				os.Remove(tmp)
				return fmt.Errorf("cannot install %s to %s: %w", baseName, destPath, err)
			}

			if strings.EqualFold(baseName, "rigctld.exe") {
				foundRigctld = true
			} else {
				debug.Log("[HAMLIB] Extracted DLL: %s", baseName)
			}
		}
	}

	if !foundRigctld {
		return fmt.Errorf("rigctld.exe not found inside ZIP (checked %d entries)", len(r.File))
	}

	return nil
}

func reportProgress(ch chan<- int, pct int) {
	if ch == nil {
		return
	}
	select {
	case ch <- pct:
	default:
	}
}
