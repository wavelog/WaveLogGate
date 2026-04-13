package config

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Profile holds per-profile configuration.
type Profile struct {
	WavelogURL         string  `json:"wavelog_url"`
	WavelogKey         string  `json:"wavelog_key"`
	WavelogID          string  `json:"wavelog_id"`
	WavelogRadioname   string  `json:"wavelog_radioname"`
	WavelogPmode       bool    `json:"wavelog_pmode"`
	FlrigHost          string  `json:"flrig_host"`
	FlrigPort          string  `json:"flrig_port"`
	FlrigEna           bool    `json:"flrig_ena"`
	HamlibHost         string  `json:"hamlib_host"`
	HamlibPort         string  `json:"hamlib_port"`
	HamlibEna          bool    `json:"hamlib_ena"`
	IgnorePwr          bool    `json:"ignore_pwr"`
	RotatorEnabled     bool    `json:"rotator_enabled"`
	RotatorHost        string  `json:"rotator_host"`
	RotatorPort        string  `json:"rotator_port"`
	RotatorThresholdAz float64 `json:"rotator_threshold_az"`
	RotatorThresholdEl float64 `json:"rotator_threshold_el"`
	RotatorParkAz      float64 `json:"rotator_park_az"`
	RotatorParkEl      float64 `json:"rotator_park_el"`

	// Managed rigctld settings (WaveLogGate launches/manages rigctld).
	HamlibManaged   bool   `json:"hamlib_managed"`
	HamlibModel     int    `json:"hamlib_model"`
	HamlibDevice    string `json:"hamlib_device"`
	HamlibBaud      int    `json:"hamlib_baud"`
	HamlibParity    string `json:"hamlib_parity"`
	HamlibStopBits  int    `json:"hamlib_stop_bits"`
	HamlibHandshake string `json:"hamlib_handshake"`
}

// Config is the root configuration object.
type Config struct {
	Version        int       `json:"version"`
	Profile        int       `json:"profile"`
	ProfileNames   []string  `json:"profileNames"`
	UDPEnabled     bool      `json:"udp_enabled"`
	UDPPort        int       `json:"udp_port"`
	MinimapEnabled bool      `json:"minimap_enabled"`
	Profiles       []Profile `json:"profiles"`
}

func defaultProfile() Profile {
	return Profile{
		WavelogURL:         "",
		WavelogKey:         "",
		WavelogID:          "0",
		WavelogRadioname:   "WLGate",
		WavelogPmode:       true,
		FlrigHost:          "127.0.0.1",
		FlrigPort:          "12345",
		FlrigEna:           false,
		HamlibHost:         "127.0.0.1",
		HamlibPort:         "4532",
		HamlibEna:          false,
		IgnorePwr:          false,
		RotatorHost:        "",
		RotatorPort:        "4533",
		RotatorThresholdAz: 2,
		RotatorThresholdEl: 2,
		RotatorParkAz:      0,
		RotatorParkEl:      0,
	}
}

func Default() Config {
	return Config{
		Version:        5,
		Profile:        0,
		ProfileNames:   []string{"Profile 1", "Profile 2"},
		UDPEnabled:     true,
		UDPPort:        2333,
		MinimapEnabled: false,
		Profiles:       []Profile{defaultProfile(), defaultProfile()},
	}
}

func configPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "WavelogGate", "config.json"), nil
}

func Load() (Config, error) {
	path, err := configPath()
	if err != nil {
		return Default(), err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			cfg := Default()
			_ = Save(cfg)
			return cfg, nil
		}
		return Default(), err
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Default(), err
	}

	cfg = migrate(cfg)
	return cfg, nil
}

func Save(cfg Config) error {
	path, err := configPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// migrate ensures the config matches version 4 schema.
func migrate(cfg Config) Config {
	// Ensure at least 2 profiles exist.
	for len(cfg.Profiles) < 2 {
		cfg.Profiles = append(cfg.Profiles, defaultProfile())
	}
	// Ensure profileNames match profile count.
	for len(cfg.ProfileNames) < len(cfg.Profiles) {
		cfg.ProfileNames = append(cfg.ProfileNames, defaultProfileName(len(cfg.ProfileNames)))
	}
	// Version upgrades.
	if cfg.Version < 3 {
		cfg.Version = 3
		if cfg.UDPPort == 0 {
			cfg.UDPPort = 2333
		}
		cfg.UDPEnabled = true
	}
	if cfg.Version < 4 {
		cfg.Version = 4
		// MinimapEnabled defaults to false — already zero value.
	}
	if cfg.Version < 5 {
		cfg.Version = 5
		// New hamlib managed fields default to zero values (disabled).
	}
	return cfg
}

func defaultProfileName(idx int) string {
	return fmt.Sprintf("Profile %d", idx+1)
}

// ActiveProfile returns the currently active profile.
func (c *Config) ActiveProfile() Profile {
	if c.Profile >= 0 && c.Profile < len(c.Profiles) {
		return c.Profiles[c.Profile]
	}
	return defaultProfile()
}

// ValidateURL checks if a URL string has valid format for Wavelog API.
// This is a fast, non-blocking validation that only checks format, not connectivity.
// Returns (isValid, warningMessage) - warningMessage is empty if valid.
func ValidateURL(urlStr string) (bool, string) {
	// Check 1: Empty URL (most common issue)
	if strings.TrimSpace(urlStr) == "" {
		return false, "Wavelog URL is empty - please configure your Wavelog server URL"
	}

	// Check 2: Missing protocol
	if !strings.HasPrefix(urlStr, "http://") && !strings.HasPrefix(urlStr, "https://") {
		return false, "URL must start with http:// or https://"
	}

	// Check 3: Invalid URL format
	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return false, fmt.Sprintf("Invalid URL format: %v", err)
	}

	// Check 4: Missing host
	if parsedURL.Host == "" {
		return false, "URL is missing host/domain (e.g., http://wavelog.example.com)"
	}

	// Check 5: Localhost warning (info only, not an error)
	if parsedURL.Host == "localhost" || strings.HasPrefix(parsedURL.Host, "127.0.0.1") {
		return true, "Using localhost - make sure Wavelog server is running"
	}

	return true, ""
}

// ValidateSerialPort checks if a serial port device string has valid format.
// This is a fast, non-blocking validation that only checks format, not connectivity.
// Returns (isValid, warningMessage) - warningMessage is empty if valid.
func ValidateSerialPort(device string) (bool, string) {
	// Empty check (most common issue)
	trimmed := strings.TrimSpace(device)
	if trimmed == "" {
		return false, "Serial port (device) must not be empty"
	}

	// Windows-specific COM port validation
	upperDevice := strings.ToUpper(trimmed)
	if strings.HasPrefix(upperDevice, "COM") {
		// Extract number part
		numStr := strings.TrimPrefix(upperDevice, "COM")

		// Check if there's something after COM
		if numStr == "" {
			return false, "COM port number is missing (e.g., COM1, COM2)"
		}

		// Must be numeric
		num, err := strconv.Atoi(numStr)
		if err != nil {
			return false, fmt.Sprintf("COM port number must be numeric, got: %s", numStr)
		}

		// Range check (Windows supports COM1-COM256)
		if num < 1 || num > 256 {
			return false, fmt.Sprintf("COM port number must be 1-256, got: COM%d", num)
		}

		// Valid COM port format - note: we don't check if the port actually exists
		// to avoid blocking operations. The actual rigctld will report if the port doesn't exist.
		return true, ""
	}

	// Non-Windows: basic validation for device paths
	// Linux: /dev/ttyUSB0, /dev/ttyACM0, /dev/ttyS0, etc.
	// macOS: /dev/cu.usbserial*, /dev/tty.usbserial*, etc.
	if strings.HasPrefix(trimmed, "/dev/") {
		// Basic check: device path should have at least one more character after /dev/
		if len(trimmed) > 5 {
			return true, ""
		}
		return false, "Device path is too short (e.g., /dev/ttyUSB0)"
	}

	// Unknown format but not empty - might be valid on some systems
	return true, ""
}
