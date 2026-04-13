package hamlib

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"waveloggate/internal/config"
	"waveloggate/internal/debug"
)

// State represents the current lifecycle state of the managed rigctld process.
type State int

const (
	StateStopped  State = iota
	StateStarting       // process launched, waiting for TCP readiness
	StateRunning        // process is up and port is bound
	StateError          // process exited unexpectedly or failed to start
)

// Manager manages the lifecycle of a rigctld child process.
type Manager struct {
	mu           sync.Mutex
	cmd          *exec.Cmd
	processDone  chan struct{} // closed by the sole cmd.Wait() goroutine; nil when no process
	stderrCloser io.Closer     // stderr pipe; closing it unblocks the scanner goroutine
	state        State
	lastMsg      string
	cancelMon    context.CancelFunc
	cfg          config.Profile

	// OnStatus is called on every state transition.
	// running=true means rigctld is accepting connections.
	OnStatus func(running bool, message string)
}

// New creates a new Manager. onStatus is called on every state change (may be nil).
func New(onStatus func(running bool, message string)) *Manager {
	return &Manager{OnStatus: onStatus}
}

// IsRunning returns true if rigctld is currently accepting connections.
func (m *Manager) IsRunning() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state == StateRunning
}

// StatusString returns a human-readable status suitable for the frontend.
func (m *Manager) StatusString() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch m.state {
	case StateStopped:
		return "Stopped"
	case StateStarting:
		return "Starting…"
	case StateRunning:
		return "Running"
	case StateError:
		if m.lastMsg != "" {
			return "Error: " + m.lastMsg
		}
		return "Error"
	default:
		return "Unknown"
	}
}

// Validate checks that the profile has all fields required for a managed launch.
func Validate(cfg config.Profile) error {
	if cfg.HamlibModel <= 0 {
		return fmt.Errorf("no radio model selected")
	}
	if strings.TrimSpace(cfg.HamlibDevice) == "" {
		return fmt.Errorf("serial port (device) must not be empty")
	}
	if cfg.HamlibBaud <= 0 {
		return fmt.Errorf("baud rate must be greater than 0")
	}
	if p, err := strconv.Atoi(cfg.HamlibPort); err != nil || p < 1 || p > 65535 {
		return fmt.Errorf("invalid rigctld port %q", cfg.HamlibPort)
	}
	return nil
}

// Start validates the profile, finds rigctld, and launches it.
// The function returns immediately; readiness and process monitoring happen
// in a background goroutine. State changes are reported via OnStatus.
//
// IMPORTANT: no blocking OS operations are performed while m.mu is held.
// On Windows, cmd.Start() can be delayed by Defender/SmartScreen; holding
// the mutex during that delay would block GetHamlibStatus() and freeze the UI.
func (m *Manager) Start(cfg config.Profile) error {
	// Validate before touching any state.
	if err := Validate(cfg); err != nil {
		return err
	}

	// Cancel the existing monitor and grab the old process — all under lock,
	// but no blocking work happens here.
	m.mu.Lock()
	if m.cancelMon != nil {
		m.cancelMon()
		m.cancelMon = nil
	}
	oldCmd := m.cmd
	m.cmd = nil
	oldProcessDone := m.processDone
	m.processDone = nil
	oldStderrCloser := m.stderrCloser
	m.stderrCloser = nil
	m.cfg = cfg
	m.mu.Unlock()

	// Close old stderr pipe so its scanner goroutine exits promptly.
	if oldStderrCloser != nil {
		oldStderrCloser.Close()
	}

	// Terminate the old process OUTSIDE the lock (Wait can block up to 5 s).
	stopCmd(oldCmd, oldProcessDone)

	// Find the rigctld binary (filesystem stat — no lock needed).
	rigctldPath, err := RigctldPath()
	if err != nil {
		m.mu.Lock()
		m.setState(StateError, err.Error())
		m.mu.Unlock()
		return err
	}

	args := buildArgs(cfg)
	debug.Log("[HAMLIB] launching: %s %s", rigctldPath, strings.Join(args, " "))

	cmd := exec.Command(rigctldPath, args...)

	// Collect stderr lines for diagnostics.
	stderr, err := cmd.StderrPipe()
	if err != nil {
		m.mu.Lock()
		m.setState(StateError, err.Error())
		m.mu.Unlock()
		return fmt.Errorf("cannot attach stderr: %w", err)
	}

	// Prevent the child from inheriting the parent's console / WebView2 message
	// pump on Windows (see cmd_windows.go).
	setCmdAttrs(cmd)

	// cmd.Start() can be slow on Windows (Defender/SmartScreen) — keep mutex free.
	if err := cmd.Start(); err != nil {
		m.mu.Lock()
		m.setState(StateError, err.Error())
		m.mu.Unlock()
		return fmt.Errorf("cannot start rigctld: %w", err)
	}

	monCtx, cancel := context.WithCancel(context.Background())

	// processDone is closed by the sole cmd.Wait() goroutine below.
	// All other code (stopCmd, monitor) only reads this channel — they never
	// call cmd.Wait() themselves, which prevents the double-Wait race.
	processDone := make(chan struct{})
	go func() {
		cmd.Wait()
		close(processDone)
	}()

	// Re-acquire lock only to update stored state.
	m.mu.Lock()
	m.cmd = cmd
	m.processDone = processDone
	m.stderrCloser = stderr
	m.cancelMon = cancel
	m.setState(StateStarting, "Starting…")
	m.mu.Unlock()

	// Capture stderr in a separate goroutine.
	stderrLines := make(chan string, 64)
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			line := scanner.Text()
			debug.Log("[HAMLIB stderr] %s", line)
			select {
			case stderrLines <- line:
			default:
			}
		}
		close(stderrLines)
	}()

	// Monitor goroutine: wait for readiness then watch for process exit.
	go m.monitor(monCtx, cmd, cfg, stderrLines, processDone)
	return nil
}

// Stop terminates the managed rigctld process if it is running.
// The lock is released before waiting for process exit to avoid blocking callers.
func (m *Manager) Stop() {
	m.mu.Lock()
	if m.cancelMon != nil {
		m.cancelMon()
		m.cancelMon = nil
	}
	cmd := m.cmd
	m.cmd = nil
	processDone := m.processDone
	m.processDone = nil
	stderrCloser := m.stderrCloser
	m.stderrCloser = nil
	wasRunning := m.state != StateStopped
	m.state = StateStopped
	m.lastMsg = ""
	m.mu.Unlock()

	if wasRunning {
		m.notify(false, "")
	}

	// Close stderr pipe so the scanner goroutine exits promptly.
	if stderrCloser != nil {
		stderrCloser.Close()
	}

	// Terminate and wait OUTSIDE the lock (can block up to 5 s).
	stopCmd(cmd, processDone)
}

// stopCmd sends a termination signal to cmd and waits for the process to exit
// via processDone (which is closed by the sole cmd.Wait() goroutine started in
// Start). It never calls cmd.Wait() itself to prevent the double-Wait race.
// Safe to call with nil arguments.
func stopCmd(cmd *exec.Cmd, processDone <-chan struct{}) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = terminateProcess(cmd.Process)
	if processDone == nil {
		return
	}
	select {
	case <-processDone:
		// Process exited cleanly.
	case <-time.After(5 * time.Second):
		// Serial ports can take longer to release on Windows with USB drivers;
		// force-kill and give the Wait goroutine a moment to observe the exit.
		_ = cmd.Process.Kill()
		select {
		case <-processDone:
		case <-time.After(2 * time.Second):
		}
	}
}

// monitor waits for TCP readiness then watches for process exit.
// processDone is closed by the sole cmd.Wait() goroutine started in Start();
// monitor never calls cmd.Wait() itself.
func (m *Manager) monitor(ctx context.Context, cmd *exec.Cmd, cfg config.Profile, stderrLines <-chan string, processDone <-chan struct{}) {
	addr := net.JoinHostPort(cfg.HamlibHost, cfg.HamlibPort)
	if cfg.HamlibHost == "" {
		addr = net.JoinHostPort("127.0.0.1", cfg.HamlibPort)
	}

	// Wait for rigctld to start listening (15-second window for Windows).
	// Windows can have slower startup due to Defender/SmartScreen checks.
	ready := waitForPort(ctx, addr, 15*time.Second)
	if !ready {
		// If the context was cancelled this is an intentional Stop/Restart —
		// don't overwrite the StateStopped set by Stop().
		select {
		case <-ctx.Done():
			_ = cmd.Process.Kill()
			return
		default:
		}

		// Collect any error lines already buffered.
		var stderrBuf []string
	drainLoop:
		for {
			select {
			case line, ok := <-stderrLines:
				if !ok {
					break drainLoop
				}
				stderrBuf = append(stderrBuf, line)
			default:
				break drainLoop
			}
		}

		// Build detailed error message
		msg := "rigctld did not start in time — check your serial port and baud rate"
		if len(stderrBuf) > 0 {
			last := stderrBuf[len(stderrBuf)-1]
			interpreted := interpretStderr(last, cfg)
			if interpreted != "rigctld: "+last {
				msg = interpreted
			} else {
				// Include raw stderr in the message for debugging
				msg = fmt.Sprintf("rigctld error: %s (model: %d, port: %s, baud: %d)", last, cfg.HamlibModel, cfg.HamlibDevice, cfg.HamlibBaud)
			}
		} else {
			// No stderr output - process might have exited silently
			msg = fmt.Sprintf("rigctld started but exited without error output (model: %d, port: %s, baud: %d) - check if rigctld is installed and the radio is connected", cfg.HamlibModel, cfg.HamlibDevice, cfg.HamlibBaud)
		}

		m.mu.Lock()
		m.setState(StateError, msg)
		m.mu.Unlock()
		_ = cmd.Process.Kill()
		return
	}

	// Guard against a Stop() that raced with waitForPort completing.
	m.mu.Lock()
	select {
	case <-ctx.Done():
		m.mu.Unlock()
		return
	default:
	}
	m.setState(StateRunning, "Running")
	m.mu.Unlock()

	// Watch for stderr lines and process exit. processDone is closed by the
	// sole cmd.Wait() goroutine — no Wait call here.
	var lastStderr string
	for {
		select {
		case <-ctx.Done():
			return
		case line, ok := <-stderrLines:
			if ok {
				lastStderr = line
			}
		case <-processDone:
			// Check ctx.Done() under the lock so we cannot race with Stop()
			// setting m.state = StateStopped: if Stop() cancelled the context
			// we must not overwrite StateStopped with StateError.
			m.mu.Lock()
			select {
			case <-ctx.Done():
				// Expected stop — don't report error.
				m.mu.Unlock()
				return
			default:
			}
			msg := "rigctld exited unexpectedly"
			if lastStderr != "" {
				msg = interpretStderr(lastStderr, cfg)
			}
			m.state = StateError
			m.lastMsg = msg
			m.notify(false, msg)
			m.mu.Unlock()
			return
		}
	}
}

// setState sets state+message and calls notify. Must be called with m.mu held.
func (m *Manager) setState(s State, msg string) {
	m.state = s
	m.lastMsg = msg
	m.notify(s == StateRunning, msg)
}

// notify calls OnStatus without holding mu (to avoid deadlock if the callback
// tries to call back into the manager). Caller must hold mu when calling.
func (m *Manager) notify(running bool, msg string) {
	fn := m.OnStatus
	if fn != nil {
		go fn(running, msg)
	}
}

// buildArgs constructs the rigctld argument list from a profile.
func buildArgs(cfg config.Profile) []string {
	args := []string{
		"-m", strconv.Itoa(cfg.HamlibModel),
		"-r", cfg.HamlibDevice,
		"-s", strconv.Itoa(cfg.HamlibBaud),
		"-t", cfg.HamlibPort,
	}

	// Bind address (if non-default).
	if cfg.HamlibHost != "" && cfg.HamlibHost != "0.0.0.0" {
		args = append(args, "-T", cfg.HamlibHost)
	}

	// Parity.
	if cfg.HamlibParity != "" && cfg.HamlibParity != "none" {
		args = append(args, "-P", cfg.HamlibParity)
	}

	// Stop bits.
	if cfg.HamlibStopBits > 0 {
		args = append(args, "-S", strconv.Itoa(cfg.HamlibStopBits))
	}

	// Handshake (mapped to set-conf).
	switch cfg.HamlibHandshake {
	case "rtscts":
		args = append(args, "--set-conf=rts_state=ON,cts_state=ON")
	case "xonxoff":
		args = append(args, "--set-conf=xon_xoff=1")
	}

	return args
}

// waitForPort tries to TCP-connect to addr until it succeeds or the deadline
// is reached. Returns true if the port became available before the deadline.
func waitForPort(ctx context.Context, addr string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return false
		}
		conn, err := net.DialTimeout("tcp", addr, 300*time.Millisecond)
		if err == nil {
			conn.Close()
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(200 * time.Millisecond):
		}
	}
	return false
}

// interpretStderr maps common rigctld error messages to user-friendly strings.
func interpretStderr(line string, cfg config.Profile) string {
	lower := strings.ToLower(line)
	switch {
	case strings.Contains(lower, "no such device") || strings.Contains(lower, "no such file"):
		return fmt.Sprintf("Serial port %q not found — check the device name", cfg.HamlibDevice)
	case strings.Contains(lower, "permission denied"):
		return fmt.Sprintf("Permission denied on %q — try: sudo chmod a+rw %s", cfg.HamlibDevice, cfg.HamlibDevice)
	case strings.Contains(lower, "address already in use") || strings.Contains(lower, "bind"):
		return fmt.Sprintf("Port %s is already in use — another rigctld may be running", cfg.HamlibPort)
	case strings.Contains(lower, "rig not found") || strings.Contains(lower, "no rig found"):
		return fmt.Sprintf("Hamlib model %d not recognised by rigctld — check your radio model selection", cfg.HamlibModel)
	default:
		return "rigctld: " + line
	}
}
