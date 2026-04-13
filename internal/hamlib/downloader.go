package hamlib

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// hamlibDir returns the path to the managed hamlib directory
// (~/.config/WavelogGate/hamlib/).
func hamlibDir() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "WavelogGate", "hamlib"), nil
}

// rigctldName returns the rigctld executable name for the current platform.
func rigctldName() string {
	if runtime.GOOS == "windows" {
		return "rigctld.exe"
	}
	return "rigctld"
}

// commonPlatformPaths returns common installation paths for rigctld on the current platform.
// This helps find installations that may not be in the GUI application's PATH (e.g., Homebrew on macOS).
func commonPlatformPaths(executableName string) []string {
	switch runtime.GOOS {
	case "darwin":
		// macOS: Homebrew installation paths for both Intel and Apple Silicon
		return []string{
			"/opt/homebrew/bin/" + executableName, // Apple Silicon
			"/usr/local/bin/" + executableName,    // Intel Macs
		}
	case "linux":
		// Linux: common installation paths
		return []string{
			"/usr/bin/" + executableName,
			"/usr/local/bin/" + executableName,
		}
	default:
		return nil
	}
}

// RigctldPath returns the path to a usable rigctld binary.
// Search order:
//  1. ~/.config/WavelogGate/hamlib/rigctld[.exe]  (previously downloaded)
//  2. Common platform-specific installation paths (Homebrew on macOS, etc.)
//  3. rigctld in system PATH
//
// Returns an error with diagnostics if not found.
func RigctldPath() (string, error) {
	name := rigctldName()

	// 1. Previously downloaded copy.
	dir, err := hamlibDir()
	if err == nil {
		managed := filepath.Join(dir, name)
		if info, err := os.Stat(managed); err == nil {
			if info.Mode()&0o111 != 0 || runtime.GOOS == "windows" {
				return managed, nil
			}
			return "", fmt.Errorf("found %s but it is not executable (permissions: %s)", managed, info.Mode())
		}
	}

	// 2. Common platform-specific installation paths.
	for _, path := range commonPlatformPaths(name) {
		if info, err := os.Stat(path); err == nil {
			if info.Mode()&0o111 != 0 || runtime.GOOS == "windows" {
				return path, nil
			}
		}
	}

	// 3. System PATH.
	if path, err := exec.LookPath(name); err == nil {
		return path, nil
	}

	// Not found — build a helpful error message.
	searchedPaths := []string{}
	if dir != "" {
		searchedPaths = append(searchedPaths, filepath.Join(dir, name))
	}
	// Add common platform paths to the searched paths for the error message
	searchedPaths = append(searchedPaths, commonPlatformPaths(name)...)
	if pathEnv := os.Getenv("PATH"); pathEnv != "" {
		for _, p := range filepath.SplitList(pathEnv) {
			searchedPaths = append(searchedPaths, filepath.Join(p, name))
		}
	}

	return "", fmt.Errorf("%s not found. Searched:\n%s\n\n%s",
		name,
		strings.Join(searchedPaths[:min(len(searchedPaths), 10)], "\n"),
		InstallGuide(),
	)
}

// InstalledVersion returns a version string for the installed rigctld binary.
// For the managed (downloaded) binary it reads the cached version.txt written
// at download time. For system-installed binaries (Homebrew, apt, etc.) it
// falls back to running "rigctld --version" which is fast and safe.
func InstalledVersion() string {
	// Prefer the cached version written during managed download.
	dir, err := hamlibDir()
	if err == nil {
		data, err := os.ReadFile(filepath.Join(dir, "version.txt"))
		if err == nil {
			return strings.TrimSpace(string(data))
		}
	}

	// Fall back to asking the system-installed binary.
	path, err := exec.LookPath(rigctldName())
	if err != nil {
		return ""
	}
	out, err := exec.Command(path, "--version").Output()
	if err != nil {
		return ""
	}
	return strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0]
}

// InstallGuide returns platform-appropriate installation instructions.
func InstallGuide() string {
	switch runtime.GOOS {
	case "windows":
		return "Click the Download button to automatically install rigctld."
	case "darwin":
		return "Install Hamlib via Homebrew:\n\n  brew install hamlib\n\nThen click 'Detect' to find the installation."
	default: // linux and others
		return detectLinuxInstallGuide()
	}
}

func detectLinuxInstallGuide() string {
	// Detect distro from /etc/os-release.
	if data, err := os.ReadFile("/etc/os-release"); err == nil {
		content := strings.ToLower(string(data))
		if strings.Contains(content, "debian") || strings.Contains(content, "ubuntu") {
			return "Install Hamlib via apt:\n\n  sudo apt install hamlib-utils\n\nThen click 'Detect' to find the installation."
		}
		if strings.Contains(content, "fedora") || strings.Contains(content, "rhel") || strings.Contains(content, "centos") {
			return "Install Hamlib via dnf:\n\n  sudo dnf install hamlib\n\nThen click 'Detect' to find the installation."
		}
		if strings.Contains(content, "arch") || strings.Contains(content, "manjaro") {
			return "Install Hamlib via pacman:\n\n  sudo pacman -S hamlib\n\nThen click 'Detect' to find the installation."
		}
	}
	return "Install Hamlib using your distribution's package manager (e.g. apt install hamlib-utils, dnf install hamlib, pacman -S hamlib).\n\nThen click 'Detect' to find the installation."
}

// ListSerialPorts returns available serial ports on the current platform.
// Falls back to an empty slice if enumeration fails or is unsupported.
func ListSerialPorts() []string {
	switch runtime.GOOS {
	case "darwin":
		return listPortsGlob([]string{"/dev/tty.usbserial*", "/dev/tty.usbmodem*", "/dev/cu.usbserial*", "/dev/cu.usbmodem*"})
	case "linux":
		ports := listPortsGlob([]string{"/dev/ttyUSB*", "/dev/ttyACM*"})
		// Include ttyS0-ttyS3 if they exist.
		for i := 0; i < 4; i++ {
			p := fmt.Sprintf("/dev/ttyS%d", i)
			if _, err := os.Stat(p); err == nil {
				ports = append(ports, p)
			}
		}
		return ports
	case "windows":
		return listWindowsCOMPorts()
	default:
		return nil
	}
}

func listPortsGlob(patterns []string) []string {
	var ports []string
	seen := map[string]bool{}
	for _, pattern := range patterns {
		matches, _ := filepath.Glob(pattern)
		for _, m := range matches {
			if !seen[m] {
				seen[m] = true
				ports = append(ports, m)
			}
		}
	}
	return ports
}
