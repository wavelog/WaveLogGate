package radio

import (
	"bufio"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"waveloggate/internal/debug"
)

// RigStatus holds the current radio state.
type RigStatus struct {
	FreqA float64
	FreqB float64
	Mode  string
	ModeB string
	Power float64
	Split bool
	PTT   bool
}

// RadioClient is the interface for radio backends.
type RadioClient interface {
	GetStatus() (RigStatus, error)
	SetFreqMode(hz int64, mode string) error
	SetTxFreq(hz int64) error
	GetModes() ([]string, error)
}

// ─── FLRig ────────────────────────────────────────────────────────────────────

// FLRigClient implements RadioClient via HTTP/XML-RPC against FLRig.
type FLRigClient struct {
	baseURL    string
	httpClient *http.Client
}

func NewFLRig(host, port string) *FLRigClient {
	hostPart, pathPart, _ := strings.Cut(host, "/")
	if pathPart == "" {
		pathPart = "/"
	} else if !strings.HasPrefix(pathPart, "/") {
		pathPart = "/" + pathPart
	}
	return &FLRigClient{
		baseURL: fmt.Sprintf("http://%s:%s%s", hostPart, port, pathPart),
		httpClient: &http.Client{
			Timeout: 3 * time.Second,
		},
	}
}

func (c *FLRigClient) xmlrpcCall(method string, args ...interface{}) (string, error) {
	var params strings.Builder
	if len(args) > 0 {
		params.WriteString("<params>")
		for _, a := range args {
			switch v := a.(type) {
			case float64:
				params.WriteString(fmt.Sprintf("<param><value><double>%v</double></value></param>", v))
			case string:
				params.WriteString(fmt.Sprintf("<param><value>%s</value></param>", v))
			}
		}
		params.WriteString("</params>")
	}

	body := fmt.Sprintf(`<?xml version="1.0"?><methodCall><methodName>%s</methodName>%s</methodCall>`,
		method, params.String())

	resp, err := c.httpClient.Post(c.baseURL, "text/xml", strings.NewReader(body))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	return extractXMLRPCValue(data), nil
}

// extractXMLRPCValue extracts the first value from an XML-RPC response.
func extractXMLRPCValue(data []byte) string {
	dec := xml.NewDecoder(bytes.NewReader(data))
	inValue := false
	var text strings.Builder

	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "value" {
				inValue = true
			}
		case xml.EndElement:
			if t.Name.Local == "value" {
				inValue = false
			}
		case xml.CharData:
			if inValue {
				text.Write(t)
			}
		}
	}
	return strings.TrimSpace(text.String())
}

func (c *FLRigClient) GetStatus() (RigStatus, error) {
	var s RigStatus
	var err error

	vfoStr, err := c.xmlrpcCall("rig.get_vfo")
	if err != nil {
		return s, err
	}
	s.FreqA, _ = strconv.ParseFloat(vfoStr, 64)

	s.Mode, err = c.xmlrpcCall("rig.get_mode")
	if err != nil {
		return s, err
	}

	pttStr, _ := c.xmlrpcCall("rig.get_ptt")
	s.PTT = pttStr == "1" || pttStr == "T" || strings.ToLower(pttStr) == "true"

	pwrStr, _ := c.xmlrpcCall("rig.get_power")
	s.Power, _ = strconv.ParseFloat(pwrStr, 64)

	splitStr, _ := c.xmlrpcCall("rig.get_split")
	s.Split = splitStr == "1"

	vfoBStr, _ := c.xmlrpcCall("rig.get_vfoB")
	s.FreqB, _ = strconv.ParseFloat(vfoBStr, 64)

	s.ModeB, _ = c.xmlrpcCall("rig.get_modeB")

	return s, nil
}

func (c *FLRigClient) SetFreqMode(hz int64, mode string) error {
	if mode != "" {
		if _, err := c.xmlrpcCall("rig.set_modeA", mode); err != nil {
			return err
		}
	}
	_, err := c.xmlrpcCall("main.set_frequency", float64(hz))
	return err
}

func (c *FLRigClient) SetTxFreq(hz int64) error {
	_, err := c.xmlrpcCall("rig.set_vfoB", float64(hz))
	return err
}

func (c *FLRigClient) GetModes() ([]string, error) {
	body := `<?xml version="1.0"?><methodCall><methodName>rig.get_modes</methodName></methodCall>`
	resp, err := c.httpClient.Post(c.baseURL, "text/xml", strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	debug.Log("[GetModes] raw response: %s", string(data))
	modes := extractXMLRPCArray(data)
	debug.Log("[GetModes] parsed modes: %v", modes)
	return modes, nil
}

// extractXMLRPCArray extracts a string array from an XML-RPC response.
// Handles both <value><string>X</string></value> and bare <value>X</value>.
func extractXMLRPCArray(data []byte) []string {
	dec := xml.NewDecoder(bytes.NewReader(data))
	inValue := false
	hasStringChild := false
	var result []string
	var cur strings.Builder

	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "value" {
				inValue = true
				hasStringChild = false
				cur.Reset()
			} else if t.Name.Local == "string" && inValue {
				hasStringChild = true
				cur.Reset()
			}
		case xml.EndElement:
			if t.Name.Local == "string" && inValue {
				if s := strings.TrimSpace(cur.String()); s != "" {
					result = append(result, s)
				}
			} else if t.Name.Local == "value" && inValue {
				if !hasStringChild {
					if s := strings.TrimSpace(cur.String()); s != "" {
						result = append(result, s)
					}
				}
				inValue = false
			}
		case xml.CharData:
			if inValue {
				cur.Write(t)
			}
		}
	}
	return result
}

// ─── Hamlib ───────────────────────────────────────────────────────────────────

// HamlibClient implements RadioClient via TCP against rigctld.
type HamlibClient struct {
	host string
	port string

	// Sub-band freq/mode cache for IC-9700 SAT mode.
	// Reading Sub freq/band requires a VFO swap which toggles the radio display.
	// We limit this to once every subFreqTTL to reduce display flicker.
	subFreq    float64
	subMode    string
	subFreqAt  time.Time
	subFreqTTL time.Duration

	// Power reporting. rigctld's RFPOWER level is normalised 0.0–1.0, so it
	// needs converting to watts before wavelog can use it.
	readPower bool    // false when the profile has "Ignore Power" set
	maxPower  float64 // watts at level 1.0; 0 = ask hamlib via power2mW
}

func NewHamlib(host, port string, readPower bool, maxPower float64) *HamlibClient {
	return &HamlibClient{
		host:       host,
		port:       port,
		subFreqTTL: 5 * time.Second,
		readPower:  readPower,
		maxPower:   maxPower,
	}
}

func (c *HamlibClient) sendCmd(cmd string) (string, error) {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(c.host, c.port), 3*time.Second)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(3 * time.Second)) //nolint:errcheck

	if _, err := fmt.Fprint(conn, cmd); err != nil {
		return "", err
	}

	reader := bufio.NewReader(conn)
	line, readErr := reader.ReadString('\n')
	line = strings.TrimSpace(line)

	if strings.HasPrefix(line, "RPRT") {
		return "", fmt.Errorf("hamlib error: %s", line)
	}
	if readErr != nil && line == "" {
		return "", readErr
	}
	return line, nil
}

// rigCmd is a command together with the number of response lines rigctld sends
// back for it. The count is not uniform: a set answers with a single "RPRT 0",
// while a get answers with its values and no status line at all — `f` returns
// one line, but `m` returns mode then passband and `s` returns the flag then
// the split VFO. sendCmds has to know the count up front, or the replies drift
// out of step with the commands for the rest of the connection.
type rigCmd struct {
	cmd   string
	lines int
}

// sendCmds sends multiple commands over a single TCP connection and returns the
// trimmed response lines for each. Use this for VFO-swap sequences that must
// not interleave with other commands.
func (c *HamlibClient) sendCmds(cmds ...rigCmd) ([][]string, error) {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(c.host, c.port), 3*time.Second)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(3 * time.Second)) //nolint:errcheck

	reader := bufio.NewReader(conn)
	responses := make([][]string, 0, len(cmds))
	for _, rc := range cmds {
		if _, err := fmt.Fprint(conn, rc.cmd); err != nil {
			return responses, err
		}
		lines := make([]string, 0, rc.lines)
		for i := 0; i < rc.lines; i++ {
			line, readErr := reader.ReadString('\n')
			line = strings.TrimSpace(line)
			lines = append(lines, line)
			// A get that fails answers with one RPRT line in place of its
			// values, so a multi-line command can come back short. Stop here
			// rather than consuming the next command's reply.
			if strings.HasPrefix(line, "RPRT") || readErr != nil {
				break
			}
		}
		responses = append(responses, lines)
	}
	return responses, nil
}

// isSatMode returns true if the rig reports SAT mode active (IC-9700 style).
// Returns false for rigs that do not support SAT mode (e.g. IC-7300 returns
// a hamlib error for the unknown function).
func (c *HamlibClient) isSatMode() bool {
	resp, err := c.sendCmd("u SATMODE\n")
	active := err == nil && strings.TrimSpace(resp) == "1"
	debug.Log("[hamlib] SAT mode probe: resp=%q err=%v active=%v", resp, err, active)
	return active
}

// readSubVFO returns the Sub-band (uplink/TX) frequency and mode for IC-9700
// SAT mode.
//
// hamlib's get_split_freq (`i`) returns 0 for IC-9700 in SAT mode, so the only
// working method is a VFO swap: V Sub → f → m → V Main. This physically toggles
// the displayed VFO on the radio, so we cache the result and only refresh every
// subFreqTTL (5 s) to limit display flicker to once per 5 seconds instead of
// once per second.
//
// The mode is read inside the same swap because it is only reachable there, and
// because both of its consumers need the uplink one specifically: power2mW
// answers from per-band, per-mode tables, and wavelog logs the TX mode.
func (c *HamlibClient) readSubVFO() (float64, string, error) {
	if time.Since(c.subFreqAt) < c.subFreqTTL && c.subFreqAt != (time.Time{}) {
		debug.Log("[hamlib] SAT sub-band (cached): %.0f Hz %s", c.subFreq, c.subMode)
		return c.subFreq, c.subMode, nil
	}

	// V Sub  → RPRT 0
	// f      → <frequency>
	// m      → <mode> then <passband>
	// V Main → RPRT 0
	resps, err := c.sendCmds(
		rigCmd{"V Sub\n", 1},
		rigCmd{"f\n", 1},
		rigCmd{"m\n", 2},
		rigCmd{"V Main\n", 1},
	)
	if err != nil {
		return 0, "", err
	}
	if len(resps) < 3 || len(resps[0]) == 0 || len(resps[1]) == 0 {
		return 0, "", fmt.Errorf("incomplete response from sub VFO read")
	}
	if strings.HasPrefix(resps[0][0], "RPRT") && resps[0][0] != "RPRT 0" {
		return 0, "", fmt.Errorf("VFO switch to Sub rejected: %s", resps[0][0])
	}
	freq, err := strconv.ParseFloat(resps[1][0], 64)
	if err != nil {
		return 0, "", fmt.Errorf("bad frequency from sub VFO: %q", resps[1][0])
	}

	// The mode is a bonus, not a precondition: losing it costs the TX mode, not
	// the frequency, so a rig that refuses `m` still reports a usable sub band.
	mode := ""
	if len(resps[2]) > 0 && !strings.HasPrefix(resps[2][0], "RPRT") {
		mode = resps[2][0]
	}

	c.subFreq, c.subMode, c.subFreqAt = freq, mode, time.Now()
	debug.Log("[hamlib] SAT sub-band (fresh): %.0f Hz %s", freq, mode)
	return freq, mode, nil
}

// setSubVFOFreq sets the Sub-band frequency by temporarily switching the
// active VFO to Sub and back to Main within a single connection.
// Used for IC-9700 SAT mode where Sub = uplink (TX).
func (c *HamlibClient) setSubVFOFreq(hz int64) error {
	// V Sub       → RPRT 0
	// F <hz>      → RPRT 0
	// V Main      → RPRT 0
	resps, err := c.sendCmds(
		rigCmd{"V Sub\n", 1},
		rigCmd{fmt.Sprintf("F %d\n", hz), 1},
		rigCmd{"V Main\n", 1},
	)
	if err != nil {
		return err
	}
	for _, r := range resps {
		if len(r) > 0 && strings.HasPrefix(r[0], "RPRT") && r[0] != "RPRT 0" {
			return fmt.Errorf("hamlib error setting sub VFO freq: %s", r[0])
		}
	}
	return nil
}

// readPowerWatts returns the current TX power in watts, or 0 if it cannot be
// determined.
//
// rigctld reports RFPOWER as a normalised 0.0–1.0 level, so it has to be scaled
// to watts. An explicitly configured max power wins — that is the only way to be
// right when an amplifier or transverter sits between the rig and the antenna.
// Otherwise we ask hamlib itself via power2mW, which uses the backend's own power
// tables and so stays correct on rigs whose maximum differs per band (IC-9700:
// 100 W / 75 W / 10 W).
//
// freqHz and mode should describe the TX VFO, since power2mW is band-dependent.
//
// A failure is never remembered. A rig that cannot answer is simply asked again
// next poll, exactly as the SAT mode probe is on every rig that lacks SAT mode:
// one command a second is the same price the poller already pays there, and it
// is the wrong trade for a state flag that would make a momentary RPRT — a
// timeout on a CI-V bus shared with WSJT-X, say — silence power for good.
func (c *HamlibClient) readPowerWatts(freqHz float64, mode string) float64 {
	if !c.readPower {
		return 0
	}

	resp, err := c.sendCmd("l RFPOWER\n")
	if err != nil {
		debug.Log("[hamlib] RFPOWER read failed: %v", err)
		return 0
	}
	level, err := strconv.ParseFloat(strings.TrimSpace(resp), 64)
	if err != nil {
		debug.Log("[hamlib] RFPOWER unparseable: %q", resp)
		return 0
	}

	level = math.Max(0, math.Min(1, level))
	if level == 0 {
		return 0
	}

	if c.maxPower > 0 {
		watts := roundWatts(level * c.maxPower)
		debug.Log("[hamlib] power: level=%.6f max=%gW → %gW (configured max)", level, c.maxPower, watts)
		return watts
	}

	// power2mW is a long-form rigctl command: \power2mW <level> <freq> <mode>
	//
	// It is refused by a backend with no power tables, and equally by a good
	// backend asked about a receive-only frequency (an IC-7300 hears
	// 0.03–74 MHz but transmits on the ham bands only). Both are answered by
	// setting Max Power, which skips this call entirely.
	mwResp, err := c.sendCmd(fmt.Sprintf("\\power2mW %.6f %.0f %s\n", level, freqHz, mode))
	if err != nil {
		debug.Log("[hamlib] power2mW failed at %.0f Hz; set Max Power to report power here: %v", freqHz, err)
		return 0
	}
	mw, err := strconv.ParseFloat(strings.TrimSpace(mwResp), 64)
	if err != nil || mw <= 0 {
		debug.Log("[hamlib] power2mW returned %q at %.0f Hz; set Max Power to report power here", mwResp, freqHz)
		return 0
	}

	watts := roundWatts(mw / 1000)
	debug.Log("[hamlib] power: level=%.6f freq=%.0f mode=%s → %gW (power2mW)", level, freqHz, mode, watts)
	return watts
}

// roundWatts rounds power to the precision the rig actually knows.
//
// RFPOWER comes back as a level quantised by the radio's own scale, not by
// anything meaningful: an IC-7300 set to 52% reports 133/255 = 0.521569, which
// would otherwise be logged as 52.2 W. At 10 W and up that fraction is an
// artifact, so round to whole watts. Below it the fraction is real — QRP
// operators log 0.5 W — so keep one decimal.
//
// Rounding also collapses adjacent steps of the rig's scale, so small drift no
// longer counts as a change and pushes an update to wavelog every poll.
func roundWatts(w float64) float64 {
	if w >= 10 {
		return math.Round(w)
	}
	return math.Round(w*10) / 10
}

func (c *HamlibClient) GetStatus() (RigStatus, error) {
	var s RigStatus

	freqStr, err := c.sendCmd("f\n")
	if err != nil {
		return s, err
	}
	s.FreqA, _ = strconv.ParseFloat(freqStr, 64)

	modeStr, err := c.sendCmd("m\n")
	if err != nil {
		return s, err
	}
	s.Mode = strings.TrimSpace(modeStr)

	// IC-9700 SAT mode: Main band = downlink (RX), Sub band = uplink (TX).
	// Hamlib's standard split commands (s / i / I) do not work correctly in
	// this mode because SAT mode is a separate rig function, not VFO-A/B split.
	// Probe for SAT mode first; fall back to normal split detection otherwise.
	if c.isSatMode() {
		s.Split = true
		subFreq, subMode, err := c.readSubVFO()
		if err == nil {
			s.FreqB = subFreq
			s.ModeB = subMode
		} else {
			debug.Log("[hamlib] SAT sub-band read failed: %v", err)
		}
	} else {
		// Normal hamlib split (IC-7300 style VFO-A/B or IC-9700 simplex/split).
		splitStr, _ := c.sendCmd("s\n")
		s.Split = strings.TrimSpace(splitStr) == "1"
		if s.Split {
			freqBStr, _ := c.sendCmd("i\n")
			s.FreqB, _ = strconv.ParseFloat(strings.TrimSpace(freqBStr), 64)
			modeBStr, _ := c.sendCmd("x\n")
			s.ModeB = strings.TrimSpace(modeBStr)
		}
	}

	if freq, mode, ok := txForPower(s); ok {
		s.Power = c.readPowerWatts(freq, mode)
	}

	return s, nil
}

// txForPower returns the frequency and mode to ask power2mW about — always the
// TX VFO, because power tables are per band and per mode.
//
// When split is on but VFO B is unknown there is no answer worth giving. On the
// rigs split matters for, the RX VFO is a different band entirely — an IC-9700
// in SAT mode listens on 70 cm while transmitting on 2 m — so falling back to it
// would report another band's power (75 W tables for a 100 W band) and would
// count failures against a frequency we are not transmitting on.
func txForPower(s RigStatus) (freq float64, mode string, ok bool) {
	if !s.Split {
		return s.FreqA, s.Mode, s.FreqA > 0
	}
	if s.FreqB <= 0 {
		return 0, "", false
	}
	// ModeB is empty only if the rig refused the mode read; the RX mode is then
	// the better guess, since uplink and downlink modes match in practice.
	mode = s.ModeB
	if mode == "" {
		mode = s.Mode
	}
	return s.FreqB, mode, true
}

func (c *HamlibClient) SetFreqMode(hz int64, mode string) error {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(c.host, c.port), 3*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(3 * time.Second)) //nolint:errcheck

	if _, err := fmt.Fprintf(conn, "F %d\n", hz); err != nil {
		return err
	}
	if mode != "" {
		if _, err := fmt.Fprintf(conn, "M %s -1\n", mode); err != nil {
			return err
		}
	}
	return nil
}

func (c *HamlibClient) SetTxFreq(hz int64) error {
	// IC-9700 in SAT mode rejects the standard split-freq command (I).
	// Use a VFO swap to the Sub band instead.
	if c.isSatMode() {
		return c.setSubVFOFreq(hz)
	}
	_, err := c.sendCmd(fmt.Sprintf("I %d\n", hz))
	return err
}

func (c *HamlibClient) GetModes() ([]string, error) {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(c.host, c.port), 3*time.Second)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(3 * time.Second)) //nolint:errcheck

	if _, err := fmt.Fprint(conn, "M ? 0\n"); err != nil {
		return nil, err
	}

	reader := bufio.NewReader(conn)
	var modes []string
	for {
		line, err := reader.ReadString('\n')
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "RPRT") {
			break
		}
		if line != "" {
			parts := strings.Fields(line)
			modes = append(modes, parts...)
		}
		if err != nil {
			break
		}
	}
	return modes, nil
}

// ─── Mode selection ───────────────────────────────────────────────────────────

// fallbackModes defines fallback chains for mode matching.
var fallbackModes = map[string][]string{
	"CW":   {"CW-U", "CW-R", "CWL", "CWR", "CW-L"},
	"RTTY": {"RTTY-R", "RTTYR", "RTTY-U", "RTTY-L"},
}

// GetClosestMode finds the best match for a desired mode from available modes.
func GetClosestMode(desired string, available []string) string {
	upper := strings.ToUpper(desired)

	// Exact match.
	for _, m := range available {
		if strings.ToUpper(m) == upper {
			debug.Log("[mode] exact match: %q", m)
			return m
		}
	}

	// Fallback chain.
	if fallbacks, ok := fallbackModes[upper]; ok {
		for _, fb := range fallbacks {
			for _, m := range available {
				if strings.ToUpper(m) == fb {
					debug.Log("[mode] fallback match: %q -> %q", desired, m)
					return m
				}
			}
		}
	}

	// Prefix match.
	for _, m := range available {
		if strings.HasPrefix(strings.ToUpper(m), upper) {
			debug.Log("[mode] prefix match: %q -> %q", desired, m)
			return m
		}
	}

	debug.Log("[mode] no match found for %q in %v", desired, available)
	return ""
}

// SelectMode determines the target mode for a QSY operation.
func SelectMode(requestedMode string, freqHz int64, available []string) string {
	if requestedMode != "" {
		if m := GetClosestMode(strings.ToUpper(requestedMode), available); m != "" {
			return m
		}
		return strings.ToUpper(requestedMode)
	}
	if freqHz < 7_999_000 {
		return "LSB"
	}
	return "USB"
}
