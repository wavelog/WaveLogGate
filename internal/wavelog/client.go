package wavelog

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"waveloggate/internal/adif"
	"waveloggate/internal/config"
	"waveloggate/internal/debug"
)

// QSOResult holds the result of a QSO submission.
type QSOResult struct {
	Success bool   `json:"success"`
	Call    string `json:"call"`
	Band    string `json:"band"`
	Mode    string `json:"mode"`
	RstSent string `json:"rstSent"`
	RstRcvd string `json:"rstRcvd"`
	TimeOn  string `json:"timeOn"`
	Reason  string `json:"reason"`
}

// Reason values classifying SendQSO outcomes. Single source of truth — callers
// that gate retry/buffering on a reason must use IsTransient, not string literals.
const (
	ReasonInternet    = "internet problem"
	ReasonTimeout     = "timeout"
	ReasonServerError = "server error"
	ReasonRateLimited = "rate limited"
)

// IsTransient reports whether a SendQSO reason is worth retrying later.
func IsTransient(reason string) bool {
	return reason == ReasonInternet || reason == ReasonTimeout ||
		reason == ReasonServerError || reason == ReasonRateLimited
}

// tokenPrefixV2 marks API tokens issued by Wavelog's v2 API. v1 keys never carry it,
// so the prefix alone decides which API a profile talks to — no extra setting needed.
const tokenPrefixV2 = "wl2_"

// useV2 reports whether the configured key targets the v2 API.
func useV2(key string) bool {
	return strings.HasPrefix(key, tokenPrefixV2)
}

// RadioData holds the data sent to Wavelog's /api/radio endpoint.
type RadioData struct {
	Frequency   int64
	Mode        string
	Power       float64
	FrequencyRx int64
	ModeRx      string
	Split       bool
	PropMode    string
	SatName     string
	SatMode     string
}

// Station represents a Wavelog station profile.
type Station struct {
	Name     string `json:"station_profile_name"`
	Callsign string `json:"station_callsign"`
	ID       string `json:"station_id"`
}

// Client communicates with the Wavelog API.
// cfg is an atomic pointer: UpdateProfile swaps it from the UI goroutine while
// UDP handlers and the retry-queue goroutine read it concurrently.
type Client struct {
	cfg        atomic.Pointer[config.Profile]
	httpClient *http.Client
	userAgent  string
}

// New creates a new Wavelog client.
func New(cfg *config.Profile, appVersion string) *Client {
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
	}
	c := &Client{
		httpClient: &http.Client{
			Timeout:   5 * time.Second,
			Transport: transport,
		},
		userAgent: "WavelogGate/" + appVersion,
	}
	c.cfg.Store(cfg)
	return c
}

// UpdateProfile updates the config profile used by the client.
func (c *Client) UpdateProfile(cfg *config.Profile) {
	c.cfg.Store(cfg)
}

type qsoPayload struct {
	Key              string `json:"key"`
	StationProfileID string `json:"station_profile_id"`
	Type             string `json:"type"`
	String           string `json:"string"`
}

type qsoPayloadV2 struct {
	StationProfileID string `json:"station_profile_id"`
	ImportType       string `json:"import_type"`
	Adif             string `json:"adif"`
	DryRun           bool   `json:"dryrun,omitempty"`
}

// v2Response is the common v2 envelope: success carries data, failure carries error.
type v2Response struct {
	Data  json.RawMessage `json:"data"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// reason renders a v2 error for display, preferring the human-readable message.
func (r v2Response) reason() string {
	if r.Error == nil {
		return "invalid response"
	}
	if r.Error.Message != "" {
		return r.Error.Message
	}
	return r.Error.Code
}

type apiResponse struct {
	Status   string   `json:"status"`
	Reason   string   `json:"reason"`
	Messages []string `json:"messages"`
	Call     string   `json:"call"`
	Band     string   `json:"band"`
	Mode     string   `json:"mode"`
	RstSent  string   `json:"rst_sent"`
	RstRcvd  string   `json:"rst_rcvd"`
	TimeOn   string   `json:"time_on"`
}

// redactKey masks all but the last 4 characters of an API key.
func redactKey(key string) string {
	if len(key) <= 4 {
		return "****"
	}
	return strings.Repeat("*", len(key)-4) + key[len(key)-4:]
}

// baseURL returns the configured Wavelog URL without a trailing slash. The user is
// expected to include /index.php themselves (see README).
func baseURL(cfg *config.Profile) string {
	return strings.TrimRight(cfg.WavelogURL, "/")
}

// newRequest builds a request with the common headers. For v2 keys the token goes into
// the Authorization header; v1 carries it in the body or path instead.
func (c *Client) newRequest(method, endpoint string, body []byte, cfg *config.Profile) (*http.Request, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, endpoint, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if useV2(cfg.WavelogKey) {
		req.Header.Set("Authorization", "Bearer "+cfg.WavelogKey)
	}
	return req, nil
}

// SendQSO posts an ADIF string to Wavelog. dryRun asks the server to validate only.
// v1 uses POST /api/qso (+/true), v2 uses POST /api/v2/qso with a dryrun flag; both
// submit ADIF so the local ADIF pipeline stays unchanged.
func (c *Client) SendQSO(adifStr string, dryRun bool) (*QSOResult, error) {
	cfg := c.cfg.Load()
	v2 := useV2(cfg.WavelogKey)

	var (
		endpoint string
		payload  any
	)
	if v2 {
		endpoint = baseURL(cfg) + "/api/v2/qso"
		payload = qsoPayloadV2{
			StationProfileID: cfg.WavelogID,
			ImportType:       "adif",
			Adif:             adifStr,
			DryRun:           dryRun,
		}
	} else {
		endpoint = baseURL(cfg) + "/api/qso"
		if dryRun {
			endpoint += "/true"
		}
		payload = qsoPayload{
			Key:              cfg.WavelogKey,
			StationProfileID: cfg.WavelogID,
			Type:             "adif",
			String:           adifStr,
		}
	}
	debug.Log("[WL] endpoint: %s  dryRun=%v  v2=%v", endpoint, dryRun, v2)

	// Extract QSO details from ADIF for response (since API doesn't return them for ADIF type)
	qsoInfo := adif.Parse(adifStr)

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	debug.Log("[WL] payload: %s", strings.Replace(string(body), cfg.WavelogKey, redactKey(cfg.WavelogKey), -1))

	req, err := c.newRequest("POST", endpoint, body, cfg)
	if err != nil {
		return nil, fmt.Errorf(ReasonInternet)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		debug.Log("[WL] HTTP error: %v", err)
		if strings.Contains(err.Error(), "timeout") || strings.Contains(err.Error(), "deadline") {
			return &QSOResult{Success: false, Reason: ReasonTimeout}, nil
		}
		return &QSOResult{Success: false, Reason: ReasonInternet}, nil
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	bodyStr := string(data)
	debug.Log("[WL] HTTP %d  response: %s", resp.StatusCode, bodyStr)

	// 5xx server errors are transient — retry later via the queue.
	if resp.StatusCode >= 500 {
		debug.Log("[WL] server error %d — treating as transient", resp.StatusCode)
		return &QSOResult{Success: false, Reason: ReasonServerError}, nil
	}

	// v2 rate limits with 429 — also worth retrying.
	if resp.StatusCode == http.StatusTooManyRequests {
		debug.Log("[WL] rate limited — treating as transient")
		return &QSOResult{Success: false, Reason: ReasonRateLimited}, nil
	}

	// Detect HTML response (wrong URL).
	if strings.Contains(bodyStr, "<html") || strings.Contains(bodyStr, "<!DOCTYPE") {
		debug.Log("[WL] response is HTML — wrong URL")
		return &QSOResult{Success: false, Reason: "wrong URL"}, nil
	}

	// success builds the result from the locally parsed ADIF — neither API echoes the
	// QSO fields back for ADIF submissions.
	success := func() *QSOResult {
		debug.Log("[WL] QSO created: call=%s band=%s mode=%s", qsoInfo["CALL"], qsoInfo["BAND"], qsoInfo["MODE"])
		return &QSOResult{
			Success: true,
			Call:    qsoInfo["CALL"],
			Band:    qsoInfo["BAND"],
			Mode:    qsoInfo["MODE"],
			RstSent: qsoInfo["RST_SENT"],
			RstRcvd: qsoInfo["RST_RCVD"],
			TimeOn:  qsoInfo["TIME_ON"],
		}
	}

	if v2 {
		var vr v2Response
		if err := json.Unmarshal(data, &vr); err != nil {
			debug.Log("[WL] JSON unmarshal failed: %v  raw: %s", err, bodyStr)
			return &QSOResult{Success: false, Reason: "invalid response"}, nil
		}
		if resp.StatusCode < 300 && vr.Error == nil && len(vr.Data) > 0 {
			return success(), nil
		}
		reason := vr.reason()
		debug.Log("[WL] QSO rejected: HTTP %d reason=%s", resp.StatusCode, reason)
		return &QSOResult{Success: false, Reason: reason}, nil
	}

	var ar apiResponse
	if err := json.Unmarshal(data, &ar); err != nil {
		debug.Log("[WL] JSON unmarshal failed: %v  raw: %s", err, bodyStr)
		return &QSOResult{Success: false, Reason: "invalid response"}, nil
	}

	if ar.Status == "created" {
		return success(), nil
	}

	reason := ar.Reason
	if reason == "" {
		reason = ar.Status
	}
	debug.Log("[WL] QSO rejected: status=%s reason=%s", ar.Status, reason)
	return &QSOResult{Success: false, Reason: reason}, nil
}

type radioPayload struct {
	Radio       string  `json:"radio"`
	Key         string  `json:"key,omitempty"`
	Frequency   int64   `json:"frequency"`
	Mode        string  `json:"mode"`
	Power       float64 `json:"power,omitempty"`
	FrequencyRx int64   `json:"frequency_rx,omitempty"`
	ModeRx      string  `json:"mode_rx,omitempty"`
	PropMode    string  `json:"prop_mode,omitempty"`
	SatName     string  `json:"sat_name,omitempty"`
	SatMode     string  `json:"sat_mode,omitempty"`
}

// UpdateRadioStatus posts radio status to Wavelog's radio endpoint.
// Frequencies are Hz in both API versions, so only path and auth differ.
func (c *Client) UpdateRadioStatus(data RadioData) error {
	cfg := c.cfg.Load()
	endpoint := baseURL(cfg) + "/api/radio"
	if useV2(cfg.WavelogKey) {
		endpoint = baseURL(cfg) + "/api/v2/radio"
	}

	freq := data.Frequency
	freqRx := data.FrequencyRx
	mode := data.Mode
	modeRx := data.ModeRx

	// If split, swap TX/RX.
	if data.Split {
		freq, freqRx = freqRx, freq
		mode, modeRx = modeRx, mode
	}

	// v2 authenticates via header, so the body must not carry the key.
	key := cfg.WavelogKey
	if useV2(key) {
		key = ""
	}

	p := radioPayload{
		Radio:       cfg.WavelogRadioname,
		Key:         key,
		Frequency:   freq,
		Mode:        mode,
		FrequencyRx: freqRx,
		ModeRx:      modeRx,
		PropMode:    data.PropMode,
		SatName:     data.SatName,
		SatMode:     data.SatMode,
	}
	if data.Power > 0 {
		p.Power = data.Power
	}

	body, err := json.Marshal(p)
	if err != nil {
		return err
	}

	req, err := c.newRequest("POST", endpoint, body, cfg)
	if err != nil {
		return err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// stationV2 is the v2 shape of a station profile. It is mapped onto Station so the
// frontend keeps consuming the v1 field names.
type stationV2 struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Callsign string `json:"callsign"`
}

// GetStations fetches the station profile list from Wavelog.
// v1 takes the key in the path, v2 uses GET /api/v2/station with a Bearer header.
func (c *Client) GetStations() ([]Station, error) {
	cfg := c.cfg.Load()
	v2 := useV2(cfg.WavelogKey)

	endpoint := baseURL(cfg) + "/api/station_info/" + cfg.WavelogKey
	if v2 {
		endpoint = baseURL(cfg) + "/api/v2/station"
	}

	req, err := c.newRequest("GET", endpoint, nil, cfg)
	if err != nil {
		return nil, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if v2 {
		var vr v2Response
		if err := json.Unmarshal(data, &vr); err != nil {
			return nil, fmt.Errorf("invalid response: %w", err)
		}
		if vr.Error != nil {
			return nil, fmt.Errorf("%s", vr.reason())
		}
		var v2Stations []stationV2
		if err := json.Unmarshal(vr.Data, &v2Stations); err != nil {
			return nil, fmt.Errorf("invalid response: %w", err)
		}
		stations := make([]Station, 0, len(v2Stations))
		for _, s := range v2Stations {
			stations = append(stations, Station{
				Name:     s.Name,
				Callsign: s.Callsign,
				ID:       strconv.Itoa(s.ID),
			})
		}
		return stations, nil
	}

	var stations []Station
	if err := json.Unmarshal(data, &stations); err != nil {
		return nil, fmt.Errorf("invalid response: %w", err)
	}
	return stations, nil
}
