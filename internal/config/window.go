package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// WindowState holds the last known window position.
// It lives in its own file so it never races with the main config.
type WindowState struct {
	X int `json:"x"`
	Y int `json:"y"`
}

func windowPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "WavelogGate", "window.json"), nil
}

// LoadWindow returns the saved window position; ok is false if none exists.
func LoadWindow() (ws WindowState, ok bool) {
	path, err := windowPath()
	if err != nil {
		return ws, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ws, false
	}
	if json.Unmarshal(data, &ws) != nil {
		return WindowState{}, false
	}
	return ws, true
}

// SaveWindow persists the window position.
func SaveWindow(ws WindowState) error {
	path, err := windowPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.Marshal(ws)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
