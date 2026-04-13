package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"waveloggate/internal/cert"
	"waveloggate/internal/config"
	"waveloggate/internal/debug"
	"waveloggate/internal/hamlib"
	"waveloggate/internal/qsy"
	"waveloggate/internal/radio"
	"waveloggate/internal/rotator"
	"waveloggate/internal/startmenu"
	"waveloggate/internal/udp"
	"waveloggate/internal/wavelog"
	"waveloggate/internal/ws"
)

var appVersion = "vdev"

// App is the Wails application backend.
type App struct {
	ctx       context.Context
	cfg       config.Config
	certPaths cert.Paths
	udpSrv    *udp.Server
	wsHub     *ws.Hub
	qsySrv    *qsy.Server
	poller    *radio.Poller
	wlClient  *wavelog.Client
	rotator   *rotator.Client
	hamlibMgr *hamlib.Manager

	// hamlibStartMu serialises stop+start sequences so that rapid profile
	// switches (SaveConfig, SwitchProfile) cannot interleave and leave a
	// stale process running.
	hamlibStartMu sync.Mutex
}

// NewApp creates a new App.
func NewApp() *App {
	return &App{}
}

// startup is called by Wails when the application starts.
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx

	go startmenu.EnsureShortcut("WaveLogGate") //nolint:errcheck

	cfg, err := config.Load()
	if err != nil {
		cfg = config.Default()
	}
	a.cfg = cfg

	profile := cfg.ActiveProfile()

	// Validate Wavelog URL configuration (fast, non-blocking check).
	a.emitURLWarning(profile.WavelogURL)

	// Wavelog client.
	a.wlClient = wavelog.New(&profile, appVersion)

	// WebSocket hub.
	a.wsHub = ws.NewHub()
	go func() {
		if err := a.wsHub.ListenAndServe(":54322"); err != nil {
			a.emitStatus("WebSocket error: " + err.Error())
		}
	}()

	// Rotator client.
	rot := rotator.New(cfg.ActiveProfile())
	rot.OnPosition = func(az, el float64) {
		wailsruntime.EventsEmit(a.ctx, "rotator:position", map[string]interface{}{
			"az": az, "el": el,
		})
	}
	rot.OnStatus = func(connected bool) {
		wailsruntime.EventsEmit(a.ctx, "rotator:status", connected)
		if connected {
			a.emitStatus("") // clear any previous rotator error
		}
	}
	rot.OnBearing = func(typ string, az, el float64) {
		wailsruntime.EventsEmit(a.ctx, "rotator:bearing", map[string]interface{}{
			"type": typ, "az": az, "el": el,
		})
	}
	rot.OnMoving = func(moving bool) {
		wailsruntime.EventsEmit(a.ctx, "rotator:moving", moving)
	}
	rot.OnError = func(msg string) {
		a.emitStatus(msg)
	}
	rot.Start()
	a.rotator = rot

	// Hamlib process manager.
	a.hamlibMgr = hamlib.New(func(running bool, message string) {
		wailsruntime.EventsEmit(a.ctx, "hamlib:status", map[string]interface{}{
			"running": running,
			"message": message,
		})
	})
	a.startManagedHamlib(profile)

	a.wsHub.OnMessage = func(data []byte) {
		debug.Log("[WS] received: %s", data)

		var msg map[string]json.RawMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			debug.Log("[WS] unmarshal error: %v", err)
			return
		}
		var msgType string
		if err := json.Unmarshal(msg["type"], &msgType); err != nil {
			debug.Log("[WS] missing/invalid 'type' field: %v", err)
			return
		}
		debug.Log("[WS] type=%s", msgType)

		switch msgType {
		case "lookup_result":
			var payload map[string]json.RawMessage
			if err := json.Unmarshal(msg["payload"], &payload); err != nil {
				debug.Log("[WS] lookup_result: bad 'payload': %v", err)
				return
			}
			az, err := parseRawFloat(payload["azimuth"])
			if err != nil {
				debug.Log("[WS] lookup_result: bad 'azimuth': %v | payload keys: %v", err, mapKeys(payload))
				return
			}
			debug.Log("[WS] lookup_result: az=%.1f → HandleWSCommand", az)
			a.rotator.HandleWSCommand(az, 0, "hf")
		case "satellite_position":
			var payload map[string]json.RawMessage
			if err := json.Unmarshal(msg["data"], &payload); err != nil {
				debug.Log("[WS] satellite_position: bad 'data': %v", err)
				return
			}
			az, err1 := parseRawFloat(payload["azimuth"])
			el, err2 := parseRawFloat(payload["elevation"])
			if err1 != nil || err2 != nil {
				debug.Log("[WS] satellite_position: bad az/el: %v %v | payload keys: %v", err1, err2, mapKeys(payload))
				return
			}
			debug.Log("[WS] satellite_position: az=%.1f el=%.1f → HandleWSCommand", az, el)
			a.rotator.HandleWSCommand(az, el, "sat")
		default:
			debug.Log("[WS] unhandled type=%s", msgType)
		}
	}

	// Radio poller.
	a.poller = radio.NewPoller(&profile, a.wlClient, func(status radio.RigStatus) {
		// Emit to frontend.
		wailsruntime.EventsEmit(a.ctx, "radio:status", map[string]interface{}{
			"freqMHz":   status.FreqA / 1_000_000,
			"mode":      status.Mode,
			"split":     status.Split,
			"freqTxMHz": status.FreqB / 1_000_000,
			"modeTx":    status.ModeB,
		})

		// Broadcast to WebSocket clients.
		msg := ws.RadioStatusMsg{
			Type:      "radio_status",
			Frequency: int64(math.Round(status.FreqA)),
			Mode:      status.Mode,
			Power:     status.Power,
			Radio:     profile.WavelogRadioname,
		}
		if status.Split {
			msg.Frequency = int64(math.Round(status.FreqB)) // TX
			msg.Mode = status.ModeB
			msg.FrequencyRx = int64(math.Round(status.FreqA)) // RX
			msg.ModeRx = status.Mode
		}
		a.wsHub.BroadcastStatus(msg)
	})
	a.poller.Start(ctx)

	// TLS certificate.
	certPaths, newlyGenerated, certErr := cert.Setup()
	if certErr != nil {
		a.emitStatus("TLS cert error: " + certErr.Error())
	}
	a.certPaths = certPaths

	// QSY server — polyglot HTTP+HTTPS on :54321.
	a.qsySrv = qsy.New(func(hz int64, mode string) error {
		return a.poller.SetFreqMode(hz, mode)
	})
	go func() {
		if certPaths.Cert != "" && certPaths.Key != "" {
			if err := a.qsySrv.ListenAndServePolyglot(":54321", certPaths.Cert, certPaths.Key); err != nil {
				a.emitStatus("QSY server error: " + err.Error())
			}
		} else {
			if err := a.qsySrv.ListenAndServe(":54321"); err != nil {
				a.emitStatus("QSY server error: " + err.Error())
			}
		}
	}()

	// WSS on :54323.
	go func() {
		if certPaths.Cert != "" && certPaths.Key != "" {
			if err := a.wsHub.ListenAndServeTLS(":54323", certPaths.Cert, certPaths.Key); err != nil {
				a.emitStatus("WSS server error: " + err.Error())
			}
		}
	}()

	// Notify frontend if certificate installation is needed.
	if certErr == nil && (newlyGenerated || !cert.IsCertInstalled(certPaths.CACert)) {
		wailsruntime.EventsEmit(a.ctx, "cert:install_needed", cert.GetInfo(certPaths))
	}

	// UDP server.
	if cfg.UDPEnabled {
		a.udpSrv = udp.New(
			cfg.UDPPort,
			a.wlClient,
			func(result *wavelog.QSOResult) {
				wailsruntime.EventsEmit(a.ctx, "qso:result", result)
			},
			func(msg string) {
				a.emitStatus(msg)
			},
		)
		if err := a.udpSrv.Start(); err != nil {
			a.emitStatus("UDP error: " + err.Error())
		}
	}
}

// shutdown is called by Wails when the application closes.
func (a *App) shutdown(ctx context.Context) {
	if a.wsHub != nil {
		a.wsHub.Shutdown(ctx)
	}
	if a.qsySrv != nil {
		a.qsySrv.Shutdown(ctx)
	}
	if a.hamlibMgr != nil {
		a.hamlibMgr.Stop()
	}
	if a.udpSrv != nil {
		a.udpSrv.Stop()
	}
	if a.poller != nil {
		a.poller.Stop()
	}
	if a.rotator != nil {
		a.rotator.Stop()
	}
}

func (a *App) emitStatus(msg string) {
	if a.ctx != nil {
		wailsruntime.EventsEmit(a.ctx, "status:message", msg)
	}
}

func (a *App) emitURLWarning(url string) {
	valid, warning := config.ValidateURL(url)
	switch {
	case !valid:
		a.emitStatus("Configuration Error: " + warning)
	case warning != "":
		a.emitStatus("Notice: " + warning)
	default:
		a.emitStatus("")
	}
}

// ─── Frontend-exposed methods ──────────────────────────────────────────────────

// GetConfig returns the current configuration.
func (a *App) GetConfig() config.Config {
	return a.cfg
}

// SaveConfig saves the configuration and returns the updated config.
func (a *App) SaveConfig(cfg config.Config) config.Config {
	a.cfg = cfg
	_ = config.Save(cfg)

	// Update subsystems with new profile.
	profile := cfg.ActiveProfile()

	// Validate Wavelog URL when config is saved.
	a.emitURLWarning(profile.WavelogURL)

	a.wlClient.UpdateProfile(&profile)
	a.poller.UpdateConfig(&profile)
	a.rotator.UpdateProfile(profile)
	a.startManagedHamlib(profile)

	wailsruntime.EventsEmit(a.ctx, "rotator:enabled", profile.RotatorEnabled)
	wailsruntime.EventsEmit(a.ctx, "radio:enabled", profile.FlrigEna || profile.HamlibEna)

	return a.cfg
}

// TestResult is the result of a Wavelog connectivity test.
type TestResult struct {
	Success bool   `json:"success"`
	Reason  string `json:"reason"`
}

const demoADIF = `<call:5>DJ7NT <gridsquare:4>JO30 <mode:3>FT8 <rst_sent:3>-15 <rst_rcvd:2>33 <qso_date:8>20240110 <time_on:6>051855 <qso_date_off:8>20240110 <time_off:6>051855 <band:3>40m <freq:8>7.155783 <station_callsign:5>TE1ST <my_gridsquare:6>JO30OO <eor>`

// TestWavelog tests Wavelog connectivity with a demo ADIF record.
func (a *App) TestWavelog(profile config.Profile) TestResult {
	client := wavelog.New(&profile, appVersion)
	result, err := client.SendQSO(demoADIF, true)
	if err != nil {
		return TestResult{Success: false, Reason: err.Error()}
	}
	return TestResult{Success: result.Success, Reason: result.Reason}
}

// GetStations fetches station profiles from Wavelog.
func (a *App) GetStations(url, key string) []wavelog.Station {
	profile := config.Profile{
		WavelogURL: url,
		WavelogKey: key,
	}
	client := wavelog.New(&profile, appVersion)
	stations, err := client.GetStations()
	if err != nil {
		return []wavelog.Station{}
	}
	return stations
}

// CreateProfile adds a new profile with the given name.
func (a *App) CreateProfile(name string) (int, error) {
	cfg := a.cfg
	cfg.Profiles = append(cfg.Profiles, config.Default().Profiles[0])
	cfg.ProfileNames = append(cfg.ProfileNames, name)
	a.cfg = cfg
	_ = config.Save(cfg)
	return len(cfg.Profiles) - 1, nil
}

// DeleteProfile removes a profile by index. Minimum 2 profiles must remain.
func (a *App) DeleteProfile(index int) error {
	cfg := a.cfg
	if len(cfg.Profiles) <= 2 {
		return fmt.Errorf("cannot delete: minimum 2 profiles required")
	}
	if index == cfg.Profile {
		return fmt.Errorf("cannot delete the active profile")
	}
	cfg.Profiles = append(cfg.Profiles[:index], cfg.Profiles[index+1:]...)
	cfg.ProfileNames = append(cfg.ProfileNames[:index], cfg.ProfileNames[index+1:]...)
	if cfg.Profile >= len(cfg.Profiles) {
		cfg.Profile = len(cfg.Profiles) - 1
	}
	a.cfg = cfg
	_ = config.Save(a.cfg)
	return nil
}

// RenameProfile renames a profile by index.
func (a *App) RenameProfile(index int, name string) error {
	if index < 0 || index >= len(a.cfg.ProfileNames) {
		return fmt.Errorf("invalid profile index")
	}
	a.cfg.ProfileNames[index] = name
	_ = config.Save(a.cfg)
	return nil
}

// SwitchProfile switches to the profile at the given index.
func (a *App) SwitchProfile(index int) error {
	if index < 0 || index >= len(a.cfg.Profiles) {
		return fmt.Errorf("invalid profile index")
	}
	a.cfg.Profile = index
	_ = config.Save(a.cfg)

	profile := a.cfg.ActiveProfile()
	a.wlClient.UpdateProfile(&profile)
	a.poller.UpdateConfig(&profile)
	a.rotator.UpdateProfile(profile)
	a.startManagedHamlib(profile)

	wailsruntime.EventsEmit(a.ctx, "profile:switched", map[string]interface{}{
		"rotatorEnabled": profile.RotatorEnabled,
		"radioEnabled":   profile.FlrigEna || profile.HamlibEna,
	})
	return nil
}

// UDPStatus holds current UDP server status.
type UDPStatus struct {
	Enabled        bool `json:"enabled"`
	Port           int  `json:"port"`
	Running        bool `json:"running"`
	MinimapEnabled bool `json:"minimapEnabled"`
}

// GetUDPStatus returns the current UDP server status.
func (a *App) GetUDPStatus() UDPStatus {
	return UDPStatus{
		Enabled:        a.cfg.UDPEnabled,
		Port:           a.cfg.UDPPort,
		Running:        a.udpSrv != nil,
		MinimapEnabled: a.cfg.MinimapEnabled,
	}
}

// RotatorStatus holds the current rotator state for the frontend.
type RotatorStatus struct {
	Connected  bool    `json:"connected"`
	Az         float64 `json:"az"`
	El         float64 `json:"el"`
	FollowMode string  `json:"followMode"`
}

// GetRotatorStatus returns the current rotator status.
func (a *App) GetRotatorStatus() RotatorStatus {
	if a.rotator == nil {
		return RotatorStatus{}
	}
	pos := a.rotator.CurrentPosition()
	return RotatorStatus{
		Connected:  a.rotator.IsConnected(),
		Az:         pos.Az,
		El:         pos.El,
		FollowMode: string(a.rotator.GetFollowMode()),
	}
}

// RotatorSetFollow sets the rotator follow mode ("off", "hf", "sat").
func (a *App) RotatorSetFollow(mode string) {
	debug.Log("[ROT] RotatorSetFollow: mode=%s", mode)
	if a.rotator != nil {
		a.rotator.SetFollow(rotator.FollowMode(mode))
	}
}

// RotatorGoto points the rotator to the given azimuth/elevation.
// Automatically disables follow mode. Returns an error if the rotator is not connected.
func (a *App) RotatorGoto(az, el float64) error {
	connected := a.rotator != nil && a.rotator.IsConnected()
	debug.Log("[APP] RotatorGoto called: az=%.1f el=%.1f connected=%v", az, el, connected)
	if !connected {
		return fmt.Errorf("rotator not connected")
	}
	a.rotator.GotoPosition(az, el)
	wailsruntime.EventsEmit(a.ctx, "rotator:followmode", "off")
	return nil
}

// RotatorPark parks the rotator.
func (a *App) RotatorPark() {
	if a.rotator != nil {
		p := a.cfg.ActiveProfile()
		a.rotator.Park()
		wailsruntime.EventsEmit(a.ctx, "rotator:goto", map[string]interface{}{
			"az": p.RotatorParkAz, "el": p.RotatorParkEl,
		})
	}
}

// SaveAdvanced saves global (non-profile) settings.
func (a *App) SaveAdvanced(udpEnabled bool, udpPort int, minimapEnabled bool) error {
	a.cfg.UDPEnabled = udpEnabled
	a.cfg.UDPPort = udpPort
	a.cfg.MinimapEnabled = minimapEnabled
	_ = config.Save(a.cfg)

	wailsruntime.EventsEmit(a.ctx, "advanced:changed", map[string]interface{}{
		"minimapEnabled": minimapEnabled,
	})

	// Restart UDP server if needed.
	if a.udpSrv != nil {
		a.udpSrv.Stop()
		a.udpSrv = nil
	}
	if udpEnabled {
		a.udpSrv = udp.New(
			udpPort,
			a.wlClient,
			func(result *wavelog.QSOResult) {
				wailsruntime.EventsEmit(a.ctx, "qso:result", result)
			},
			func(msg string) {
				a.emitStatus(msg)
			},
		)
		if err := a.udpSrv.Start(); err != nil {
			return err
		}
	}
	return nil
}

// GetCertInfo returns the current certificate state.
func (a *App) GetCertInfo() cert.Info {
	return cert.GetInfo(a.certPaths)
}

// IsCertInstalled reports whether the Root CA is trusted by the OS.
func (a *App) IsCertInstalled() bool {
	return cert.IsCertInstalled(a.certPaths.CACert)
}

// InstallCert installs the Root CA into the system trust store.
func (a *App) InstallCert() cert.InstallResult {
	return cert.Install(a.certPaths.CACert)
}

// ─── Hamlib management ─────────────────────────────────────────────────────────

// HamlibStatus is returned by GetHamlibStatus.
type HamlibStatus struct {
	Installed    bool   `json:"installed"`
	Version      string `json:"version"`
	Running      bool   `json:"running"`
	StatusMsg    string `json:"statusMsg"`
	InstallGuide string `json:"installGuide"`
	CanDownload  bool   `json:"canDownload"`
}

// DownloadResult is returned by DownloadHamlib.
type DownloadResult struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// GetHamlibStatus returns the current hamlib installation and process status.
func (a *App) GetHamlibStatus() HamlibStatus {
	_, err := hamlib.RigctldPath()
	installed := err == nil
	return HamlibStatus{
		Installed:    installed,
		Version:      hamlib.InstalledVersion(),
		Running:      a.hamlibMgr != nil && a.hamlibMgr.IsRunning(),
		StatusMsg:    a.hamlibStatusMsg(),
		InstallGuide: hamlib.InstallGuide(),
		CanDownload:  hamlib.CanDownload(),
	}
}

func (a *App) hamlibStatusMsg() string {
	if a.hamlibMgr == nil {
		return ""
	}
	return a.hamlibMgr.StatusString()
}

// DownloadHamlib triggers the hamlib download (Windows) or returns install guide (others).
func (a *App) DownloadHamlib() DownloadResult {
	progressCh := make(chan int, 16)
	go func() {
		for pct := range progressCh {
			wailsruntime.EventsEmit(a.ctx, "hamlib:download_progress", map[string]interface{}{
				"percent": pct,
			})
		}
	}()

	ctx := a.ctx
	err := hamlib.Download(ctx, progressCh)
	close(progressCh)
	if err != nil {
		return DownloadResult{Success: false, Message: err.Error()}
	}
	return DownloadResult{Success: true, Message: "rigctld installed successfully"}
}

// SearchRadioModels returns hamlib radio models matching the query string.
// An empty query string returns all available models (resets any previous search).
func (a *App) SearchRadioModels(q string) []hamlib.RadioModel {
	return hamlib.SearchModels(q)
}

// RefreshRadioModels refreshes the cached radio model list from the installed rigctld.
// Useful after installing/updating hamlib or to reset the model enumeration.
func (a *App) RefreshRadioModels() int {
	hamlib.InvalidateModelCache()
	models := hamlib.SearchModels("")
	return len(models)
}

// RadioSetFreq tunes the radio to the given frequency in Hz, keeping the current mode.
func (a *App) RadioSetFreq(hz int64) error {
	if a.poller == nil {
		return fmt.Errorf("radio not connected")
	}
	return a.poller.SetFreqMode(hz, "")
}

// RadioSetTxFreq sets the TX (VFO B) frequency in Hz for split operation.
func (a *App) RadioSetTxFreq(hz int64) error {
	if a.poller == nil {
		return fmt.Errorf("radio not connected")
	}
	return a.poller.SetTxFreq(hz)
}

// GetSerialPorts returns available serial ports on the current platform.
func (a *App) GetSerialPorts() []string {
	return hamlib.ListSerialPorts()
}

// StartHamlib starts (or restarts) the managed rigctld process for the active profile.
// Runs asynchronously (like startManagedHamlib) to avoid blocking the Wails RPC thread
// and to serialise with any concurrent stop+start sequence via hamlibStartMu.
func (a *App) StartHamlib() error {
	if a.hamlibMgr == nil {
		return fmt.Errorf("hamlib manager not initialised")
	}
	profile := a.cfg.ActiveProfile()
	go func() {
		a.hamlibStartMu.Lock()
		defer a.hamlibStartMu.Unlock()
		a.hamlibMgr.Stop()
		if err := a.hamlibMgr.Start(profile); err != nil {
			wailsruntime.EventsEmit(a.ctx, "hamlib:status", map[string]interface{}{
				"running": false,
				"message": err.Error(),
			})
		}
	}()
	return nil
}

// StopHamlib stops the managed rigctld process.
// Runs asynchronously to serialise with any in-flight startManagedHamlib goroutine
// via hamlibStartMu, preventing a concurrent start sequence from undoing the stop.
func (a *App) StopHamlib() {
	if a.hamlibMgr == nil {
		return
	}
	go func() {
		a.hamlibStartMu.Lock()
		defer a.hamlibStartMu.Unlock()
		a.hamlibMgr.Stop()
	}()
}

// startManagedHamlib stops any running instance and starts a new one if the
// profile has HamlibManaged=true and HamlibEna=true.
// The entire stop+start sequence runs in a goroutine so that callers on the
// Wails RPC thread (SaveConfig, SwitchProfile) are never blocked by the
// up-to-5-second process-exit wait.
func (a *App) startManagedHamlib(profile config.Profile) {
	if a.hamlibMgr == nil {
		return
	}
	go func() {
		// Serialise concurrent calls: a rapid SaveConfig → SwitchProfile
		// sequence must not let the second Stop() kill the process the first
		// Start() just launched.
		a.hamlibStartMu.Lock()
		defer a.hamlibStartMu.Unlock()

		// Stop waits for the old process to exit (up to 5 s on Windows) so
		// the serial port is released before the new instance tries to open it.
		a.hamlibMgr.Stop()

		if !profile.HamlibManaged || !profile.HamlibEna {
			return
		}

		// Validate serial port before attempting to start rigctld.
		if valid, warning := config.ValidateSerialPort(profile.HamlibDevice); !valid {
			wailsruntime.EventsEmit(a.ctx, "hamlib:status", map[string]interface{}{
				"running": false,
				"message": "Configuration Error: " + warning,
			})
			return
		}

		if err := a.hamlibMgr.Start(profile); err != nil {
			wailsruntime.EventsEmit(a.ctx, "hamlib:status", map[string]interface{}{
				"running": false,
				"message": err.Error(),
			})
		}
	}()
}

// mapKeys returns the keys of a map for debug logging.
func mapKeys(m map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// parseRawFloat parses a json.RawMessage that is either a JSON string or JSON number
// into a float64. Handles both "270" (string) and 270 (number) forms.
func parseRawFloat(raw json.RawMessage) (float64, error) {
	if len(raw) == 0 {
		return 0, fmt.Errorf("empty value")
	}
	// JSON string: starts with '"'
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return 0, err
		}
		return strconv.ParseFloat(strings.TrimSpace(s), 64)
	}
	// JSON number (or anything else)
	var f float64
	if err := json.Unmarshal(raw, &f); err != nil {
		return 0, err
	}
	return f, nil
}
