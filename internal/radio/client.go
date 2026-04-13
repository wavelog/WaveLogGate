package radio

import (
	"bufio"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
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
	return &FLRigClient{
		baseURL: fmt.Sprintf("http://%s:%s/", host, port),
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

	// Sub-band freq cache for IC-9700 SAT mode.
	// Reading Sub freq requires a VFO swap which toggles the radio display.
	// We limit this to once every subFreqTTL to reduce display flicker.
	subFreq    float64
	subFreqAt  time.Time
	subFreqTTL time.Duration
}

func NewHamlib(host, port string) *HamlibClient {
	return &HamlibClient{
		host:       host,
		port:       port,
		subFreqTTL: 5 * time.Second,
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
	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(line)

	if strings.HasPrefix(line, "RPRT") {
		return "", fmt.Errorf("hamlib error: %s", line)
	}
	return line, nil
}

// sendCmds sends multiple commands over a single TCP connection and returns
// one trimmed response line per command. Use this for VFO-swap sequences
// that must not interleave with other commands.
func (c *HamlibClient) sendCmds(cmds ...string) ([]string, error) {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(c.host, c.port), 3*time.Second)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(3 * time.Second)) //nolint:errcheck

	reader := bufio.NewReader(conn)
	responses := make([]string, 0, len(cmds))
	for _, cmd := range cmds {
		if _, err := fmt.Fprint(conn, cmd); err != nil {
			return responses, err
		}
		line, _ := reader.ReadString('\n')
		responses = append(responses, strings.TrimSpace(line))
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

// readSubVFOFreq returns the Sub-band (uplink/TX) frequency for IC-9700 SAT mode.
//
// hamlib's get_split_freq (`i`) returns 0 for IC-9700 in SAT mode, so the only
// working method is a VFO swap: V Sub → f → V Main. This physically toggles the
// displayed VFO on the radio, so we cache the result and only refresh every
// subFreqTTL (5 s) to limit display flicker to once per 5 seconds instead of
// once per second.
func (c *HamlibClient) readSubVFOFreq() (float64, error) {
	if time.Since(c.subFreqAt) < c.subFreqTTL && c.subFreqAt != (time.Time{}) {
		debug.Log("[hamlib] SAT sub-band freq (cached): %.0f Hz", c.subFreq)
		return c.subFreq, nil
	}

	// V Sub  → RPRT 0
	// f      → <frequency>
	// V Main → RPRT 0
	resps, err := c.sendCmds("V Sub\n", "f\n", "V Main\n")
	if err != nil {
		return 0, err
	}
	if len(resps) < 2 {
		return 0, fmt.Errorf("incomplete response from sub VFO read")
	}
	if strings.HasPrefix(resps[0], "RPRT") && resps[0] != "RPRT 0" {
		return 0, fmt.Errorf("VFO switch to Sub rejected: %s", resps[0])
	}
	freq, err := strconv.ParseFloat(resps[1], 64)
	if err != nil {
		return 0, fmt.Errorf("bad frequency from sub VFO: %q", resps[1])
	}

	c.subFreq = freq
	c.subFreqAt = time.Now()
	debug.Log("[hamlib] SAT sub-band freq (fresh): %.0f Hz", freq)
	return freq, nil
}

// setSubVFOFreq sets the Sub-band frequency by temporarily switching the
// active VFO to Sub and back to Main within a single connection.
// Used for IC-9700 SAT mode where Sub = uplink (TX).
func (c *HamlibClient) setSubVFOFreq(hz int64) error {
	// V Sub       → RPRT 0
	// F <hz>      → RPRT 0
	// V Main      → RPRT 0
	resps, err := c.sendCmds("V Sub\n", fmt.Sprintf("F %d\n", hz), "V Main\n")
	if err != nil {
		return err
	}
	for _, r := range resps {
		if strings.HasPrefix(r, "RPRT") && r != "RPRT 0" {
			return fmt.Errorf("hamlib error setting sub VFO freq: %s", r)
		}
	}
	return nil
}

func (c *HamlibClient) GetStatus() (RigStatus, error) {
	var s RigStatus

	freqStr, err := c.sendCmd("f\n")
	if err != nil {
		return s, err
	}
	s.FreqA, _ = strconv.ParseFloat(freqStr, 64)

	modeStr, _ := c.sendCmd("m\n")
	s.Mode = strings.TrimSpace(modeStr)

	// IC-9700 SAT mode: Main band = downlink (RX), Sub band = uplink (TX).
	// Hamlib's standard split commands (s / i / I) do not work correctly in
	// this mode because SAT mode is a separate rig function, not VFO-A/B split.
	// Probe for SAT mode first; fall back to normal split detection otherwise.
	if c.isSatMode() {
		s.Split = true
		subFreq, err := c.readSubVFOFreq()
		if err == nil {
			s.FreqB = subFreq
		} else {
			debug.Log("[hamlib] SAT sub-band read failed: %v", err)
		}
		// ModeB: reading Sub mode requires another VFO swap + two-line response
		// parsing; skip for now — wavelog handles empty ModeB gracefully.
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

	return s, nil
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
		if line != "" && !strings.HasPrefix(line, "RPRT") {
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
