package rotator

import (
	"bufio"
	"fmt"
	"math"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"waveloggate/internal/config"
	"waveloggate/internal/debug"
)

const (
	busyWatchdog    = 5 * time.Second
	pollInterval    = 1 * time.Second
	pollSuppression = 3 * time.Second
	connTimeout     = 3 * time.Second
	wsRateLimit     = 150 * time.Millisecond
)

type FollowMode string

const (
	FollowOff FollowMode = "off"
	FollowHF  FollowMode = "hf"
	FollowSAT FollowMode = "sat"
)

// Position holds azimuth and elevation angles.
type Position struct {
	Az, El float64
}

type wsCmd struct {
	az, el float64
	typ    string
}

// Client is a single-goroutine rotctld TCP client with a follow state machine.
type Client struct {
	mu         sync.Mutex
	cfg        config.Profile
	followMode FollowMode

	conn       net.Conn
	connTarget string // "host:port" last connected to
	buf        string

	currentCmd  string // "set" | "get" | ""
	busyTimer   *time.Timer
	pendingSet  *Position // latest P command not yet sent
	pollPending bool
	lastPTime   time.Time
	stopping    bool      // S sent, waiting for RPRT
	stopAfter   *Position // P to issue after S's RPRT
	lastMoving  bool

	lastCmdPos Position
	currentPos Position

	pendingPark bool // when true, pendingSet is a park command — bypass threshold

	lastWsCmd time.Time
	pendingWs *wsCmd
	wsTimer   *time.Timer

	OnPosition func(az, el float64)             // → Wails event rotator:position
	OnStatus   func(connected bool)             // → Wails event rotator:status
	OnBearing  func(typ string, az, el float64) // → Wails event rotator:bearing
	OnMoving   func(moving bool)                // → Wails event rotator:moving
	OnError    func(msg string)                 // → Wails event status:message

	cmdCh  chan struct{}
	stopCh chan struct{}
}

// New creates a new Client with the given profile.
func New(cfg config.Profile) *Client {
	return &Client{
		cfg:        cfg,
		followMode: FollowOff,
		cmdCh:      make(chan struct{}, 1),
		stopCh:     make(chan struct{}),
	}
}

// Start launches the background goroutine.
func (c *Client) Start() {
	go c.run()
}

// Stop shuts down the client.
func (c *Client) Stop() {
	close(c.stopCh)
}

// UpdateProfile updates the configuration. Triggers a reconnect if host/port changed.
func (c *Client) UpdateProfile(cfg config.Profile) {
	c.mu.Lock()
	c.cfg = cfg
	c.mu.Unlock()
	c.signal()
}

// SetFollow sets the follow mode.
func (c *Client) SetFollow(mode FollowMode) {
	c.mu.Lock()
	c.followMode = mode
	if mode == FollowOff {
		c.pendingSet = nil
		c.pendingWs = nil
		c.stopAfter = nil
		c.stopping = false
		if c.wsTimer != nil {
			c.wsTimer.Stop()
			c.wsTimer = nil
		}
		if c.conn != nil && c.currentCmd == "" {
			fmt.Fprintf(c.conn, "S\n")
		}
		c.notifyMoving()
	}
	c.mu.Unlock()
	c.signal()
}

// GotoPosition switches follow to Off and queues a direct move to the given az/el,
// bypassing the movement threshold (same as Park).
func (c *Client) GotoPosition(az, el float64) {
	az = math.Mod(az, 360)
	if az < 0 {
		az += 360
	}
	if el < 0 {
		el = 0
	}
	if el > 90 {
		el = 90
	}
	c.mu.Lock()
	c.followMode = FollowOff
	c.pendingSet = &Position{Az: az, El: el}
	c.pendingPark = true
	c.pendingWs = nil
	if c.wsTimer != nil {
		c.wsTimer.Stop()
		c.wsTimer = nil
	}
	c.notifyMoving()
	c.mu.Unlock()
	c.signal()
}

// Park sets follow to off and queues a move to the park position, bypassing threshold.
func (c *Client) Park() {
	c.mu.Lock()
	c.followMode = FollowOff
	c.pendingSet = &Position{Az: c.cfg.RotatorParkAz, El: c.cfg.RotatorParkEl}
	c.pendingPark = true
	c.notifyMoving()
	c.mu.Unlock()
	c.signal()
}

// HandleWSCommand handles an incoming bearing update from WS (rate-limited).
// Always fires OnBearing immediately for live display.
func (c *Client) HandleWSCommand(az, el float64, typ string) {
	// Always update bearing display.
	c.mu.Lock()
	onBearing := c.OnBearing
	followMode := c.followMode
	currentAz := c.currentPos.Az
	c.mu.Unlock()

	debug.Log("[ROT] WS bearing: type=%s demanded_az=%.1f el=%.1f current_az=%.1f follow=%s",
		typ, az, el, currentAz, followMode)

	if onBearing != nil {
		onBearing(typ, az, el)
	}

	// Only queue rotator move if follow mode matches.
	c.mu.Lock()
	if (typ == "hf" && followMode != FollowHF) || (typ == "sat" && followMode != FollowSAT) {
		debug.Log("[ROT] WS bearing dropped: follow=%s does not match type=%s — no move queued", followMode, typ)
		c.mu.Unlock()
		return
	}

	now := time.Now()
	if now.Sub(c.lastWsCmd) >= wsRateLimit {
		c.lastWsCmd = now
		c.pendingSet = &Position{Az: az, El: el}
		if c.wsTimer != nil {
			c.wsTimer.Stop()
			c.wsTimer = nil
		}
		debug.Log("[ROT] WS bearing queued immediately: az=%.1f el=%.1f", az, el)
		c.notifyMoving()
		c.mu.Unlock()
		c.signal()
		return
	}

	// Rate-limit: schedule deferred send.
	c.pendingWs = &wsCmd{az: az, el: el, typ: typ}
	remaining := wsRateLimit - now.Sub(c.lastWsCmd)
	debug.Log("[ROT] WS bearing rate-limited, deferred by %v: az=%.1f el=%.1f", remaining, az, el)
	if c.wsTimer == nil {
		c.wsTimer = time.AfterFunc(remaining, func() {
			c.mu.Lock()
			pw := c.pendingWs
			c.pendingWs = nil
			c.wsTimer = nil
			if pw != nil {
				fm := c.followMode
				if (pw.typ == "hf" && fm == FollowHF) || (pw.typ == "sat" && fm == FollowSAT) {
					debug.Log("[ROT] WS deferred bearing applied: az=%.1f el=%.1f follow=%s", pw.az, pw.el, fm)
					c.lastWsCmd = time.Now()
					c.pendingSet = &Position{Az: pw.az, El: pw.el}
					c.notifyMoving()
				} else {
					debug.Log("[ROT] WS deferred bearing dropped: follow=%s no longer matches type=%s", fm, pw.typ)
				}
			}
			c.mu.Unlock()
			c.signal()
		})
	}
	c.mu.Unlock()
}

// GetFollowMode returns the current follow mode.
func (c *Client) GetFollowMode() FollowMode {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.followMode
}

// CurrentPosition returns the last known position.
func (c *Client) CurrentPosition() Position {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.currentPos
}

// IsConnected returns true if currently connected.
func (c *Client) IsConnected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn != nil
}

// signal wakes up the run loop without blocking.
func (c *Client) signal() {
	select {
	case c.cmdCh <- struct{}{}:
	default:
	}
}

// run is the single goroutine that owns the TCP socket.
func (c *Client) run() {
	tick := time.NewTicker(pollInterval)
	defer tick.Stop()
	for {
		select {
		case <-tick.C:
			c.mu.Lock()
			c.pollPending = true
			c.mu.Unlock()
			c.ensureConnected()
			c.processQueue()
		case <-c.cmdCh:
			c.ensureConnected()
			c.processQueue()
		case <-c.stopCh:
			c.mu.Lock()
			c.closeSocket()
			c.mu.Unlock()
			return
		}
	}
}

// ensureConnected dials rotctld if not already connected.
// Must be called from the run goroutine (not holding mu).
func (c *Client) ensureConnected() {
	c.mu.Lock()
	host := c.cfg.RotatorHost
	port := c.cfg.RotatorPort
	target := host + ":" + port
	conn := c.conn
	connTarget := c.connTarget
	c.mu.Unlock()

	if host == "" || !c.cfg.RotatorEnabled {
		// No host configured or rotator disabled — disconnect if currently connected.
		c.mu.Lock()
		wasConnected := c.conn != nil
		if wasConnected {
			c.closeSocket()
		}
		onStatus := c.OnStatus
		c.mu.Unlock()
		if wasConnected && onStatus != nil {
			go onStatus(false)
		}
		return
	}

	if conn != nil && connTarget == target {
		return // already connected to correct target
	}

	// Target changed or not connected — close old socket.
	c.mu.Lock()
	c.closeSocket()
	c.mu.Unlock()

	nc, err := net.DialTimeout("tcp", target, connTimeout)
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		if c.OnStatus != nil {
			go c.OnStatus(false)
		}
		if c.OnError != nil {
			go c.OnError("Rotator: " + err.Error())
		}
		return
	}

	c.conn = nc
	c.connTarget = target
	c.buf = ""
	c.currentCmd = ""
	c.pendingSet = nil
	c.pollPending = true // poll immediately on connect
	c.stopping = false
	c.stopAfter = nil
	if c.busyTimer != nil {
		c.busyTimer.Stop()
		c.busyTimer = nil
	}

	if c.OnStatus != nil {
		go c.OnStatus(true)
	}

	// Start reader goroutine.
	go c.readLoop(nc)
}

// readLoop reads from the TCP socket and feeds data into onData.
// Exits when the connection is closed.
func (c *Client) readLoop(conn net.Conn) {
	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		line := scanner.Text()
		c.mu.Lock()
		// Only process if this connection is still current.
		if c.conn != conn {
			c.mu.Unlock()
			return
		}
		c.onLine(line)
		c.mu.Unlock()
	}
	// Connection closed.
	c.mu.Lock()
	if c.conn == conn {
		c.closeSocket()
		if c.OnStatus != nil {
			go c.OnStatus(false)
		}
	}
	c.mu.Unlock()
	c.signal()
}

// onLine processes one line received from rotctld. Must be called with mu held.
func (c *Client) onLine(line string) {
	debug.Log("[ROT] rotctld → %q (cmd=%s)", line, c.currentCmd)
	if c.currentCmd == "" {
		// No command pending — drop unsolicited/stale lines to avoid buffer pollution.
		return
	}
	c.buf += line + "\n"

	switch c.currentCmd {
	case "set":
		// Wait for RPRT N
		if strings.HasPrefix(line, "RPRT ") {
			code := strings.TrimSpace(strings.TrimPrefix(line, "RPRT "))
			wasStop := c.stopping
			c.clearBusy()
			if wasStop {
				debug.Log("[ROT] RPRT %s for S received", code)
				c.stopping = false
				if c.stopAfter != nil {
					pos := c.stopAfter
					c.stopAfter = nil
					c.sendP(pos)
				}
			} else {
				debug.Log("[ROT] RPRT %s for P received", code)
			}
			c.notifyMoving()
			c.pollPending = false
			c.buf = ""
			c.signal()
		}

	case "get":
		// Accumulate lines; parse when we have ≥2 floats or RPRT arrives.
		// Handles both bare ("123.0") and labelled ("Azimuth: 123.0") formats.
		lines := strings.Split(strings.TrimSpace(c.buf), "\n")
		var nums []float64
		gotRPRT := false
		for _, l := range lines {
			l = strings.TrimSpace(l)
			if l == "" {
				continue
			}
			if strings.HasPrefix(l, "RPRT") {
				gotRPRT = true
				continue
			}
			// Take the last whitespace-separated field — works for both
			// bare "123.0" and labelled "Azimuth: 123.0".
			fields := strings.Fields(l)
			if len(fields) > 0 {
				if v, err := strconv.ParseFloat(fields[len(fields)-1], 64); err == nil {
					nums = append(nums, v)
				}
			}
		}
		debug.Log("[ROT] p response raw lines: %q → parsed nums: %v gotRPRT=%v", lines, nums, gotRPRT)
		if len(nums) >= 2 {
			c.currentPos = Position{Az: nums[0], El: nums[1]}
			if c.OnPosition != nil {
				az, el := c.currentPos.Az, c.currentPos.El
				go c.OnPosition(az, el)
			}
			c.clearBusy()
			c.buf = ""
			c.signal()
		} else if gotRPRT {
			// RPRT arrived but couldn't parse ≥2 floats — rotctld may not support p
			debug.Log("[ROT] p RPRT received but insufficient data: %v", nums)
			c.clearBusy()
			c.buf = ""
			c.signal()
		}
	}
}

// processQueue decides what command to send next. Must be called from run goroutine.
func (c *Client) processQueue() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn == nil || c.currentCmd != "" {
		return
	}

	if c.pendingSet != nil {
		pos := c.pendingSet
		isPark := c.pendingPark
		c.pendingSet = nil
		c.pendingPark = false

		// Check threshold (skip for explicit park commands).
		if !isPark {
			diffAz := math.Abs(pos.Az - c.lastCmdPos.Az)
			if diffAz > 180 {
				diffAz = 360 - diffAz
			}
			diffEl := math.Abs(pos.El - c.lastCmdPos.El)
			debug.Log("[ROT] threshold check: demanded_az=%.1f last_cmd_az=%.1f diffAz=%.1f (threshAz=%.1f) diffEl=%.1f (threshEl=%.1f)",
				pos.Az, c.lastCmdPos.Az, diffAz, c.cfg.RotatorThresholdAz, diffEl, c.cfg.RotatorThresholdEl)
			if diffAz < c.cfg.RotatorThresholdAz && diffEl < c.cfg.RotatorThresholdEl {
				debug.Log("[ROT] threshold not reached — P suppressed")
				return
			}
		}

		// Always stop first, then issue P after rotctld confirms with RPRT.
		if c.stopping {
			// S already sent and unconfirmed — just update the target.
			debug.Log("[ROT] S pending, updating target to az=%.1f el=%.1f", pos.Az, pos.El)
			c.stopAfter = pos
			return
		}

		debug.Log("[ROT] → sending S (before P %.1f %.1f)", pos.Az, pos.El)
		fmt.Fprintf(c.conn, "S\n")
		c.currentCmd = "set"
		c.stopping = true
		c.stopAfter = pos
		c.armBusy()
		c.notifyMoving()
		return
	}

	if c.pollPending && time.Since(c.lastPTime) > pollSuppression {
		c.pollPending = false
		fmt.Fprintf(c.conn, "p\n")
		c.currentCmd = "get"
		c.armBusy()
	}
}

// sendP sends a P az el command. Must be called with mu held.
func (c *Client) sendP(pos *Position) {
	if c.conn == nil {
		return
	}
	debug.Log("[ROT] → sending P %.1f %.1f to rotctld (current_az=%.1f follow=%s)",
		pos.Az, pos.El, c.currentPos.Az, c.followMode)
	fmt.Fprintf(c.conn, "P %.1f %.1f\n", pos.Az, pos.El)
	c.lastCmdPos = *pos
	c.lastPTime = time.Now()
	c.currentCmd = "set"
	c.armBusy()
}

// notifyMoving fires OnMoving if the moving state has changed. Must be called with mu held.
func (c *Client) notifyMoving() {
	moving := c.pendingSet != nil || c.stopping || c.currentCmd == "set"
	if moving == c.lastMoving {
		return
	}
	c.lastMoving = moving
	if c.OnMoving != nil {
		cb := c.OnMoving
		go cb(moving)
	}
}

// armBusy arms the busy watchdog timer. Must be called with mu held.
func (c *Client) armBusy() {
	if c.busyTimer != nil {
		c.busyTimer.Stop()
	}
	c.busyTimer = time.AfterFunc(busyWatchdog, func() {
		c.mu.Lock()
		c.currentCmd = ""
		c.buf = ""
		if c.stopping {
			// S timed out — rescue target so next processQueue retries
			c.stopping = false
			if c.stopAfter != nil {
				c.pendingSet = c.stopAfter
				c.stopAfter = nil
				c.pendingPark = true // bypass threshold
			}
		}
		c.notifyMoving()
		c.mu.Unlock()
		c.signal()
	})
}

// clearBusy stops the watchdog and clears currentCmd. Must be called with mu held.
func (c *Client) clearBusy() {
	if c.busyTimer != nil {
		c.busyTimer.Stop()
		c.busyTimer = nil
	}
	c.currentCmd = ""
}

// closeSocket closes the TCP connection. Must be called with mu held.
func (c *Client) closeSocket() {
	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}
	c.connTarget = ""
	c.currentCmd = ""
	c.buf = ""
	if c.busyTimer != nil {
		c.busyTimer.Stop()
		c.busyTimer = nil
	}
}
