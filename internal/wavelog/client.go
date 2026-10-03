package wavelog

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
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

// IsV2Key reports whether the given key targets the v2 API.
func IsV2Key(key string) bool {
	return strings.HasPrefix(key, tokenPrefixV2)
}

// RequiredScopes lists the v2 scopes WavelogGate needs for full operation:
// station list, QSO upload and radio status.
var RequiredScopes = []string{"station:read", "qso:write", "radio:write"}

// TokenInfo is the metadata returned by GET /api/v2/token ("whoami").
type TokenInfo struct {
	Name       string                           `json:"name"`
	Owner      string                           `json:"owner"`
	Scopes     []string                         `json:"scopes"`
	Extensions map[string]RadioObservationOffer `json:"extensions,omitempty"`
}

const RadioObservationProtocol = "shackcq.radio-observation.v1"

// RadioObservationOffer is explicit server opt-in for correlated successful
// poll reports. A v2 token by itself never enables this extension.
type RadioObservationOffer struct {
	Version          int    `json:"version"`
	ReportIntervalMS int    `json:"report_interval_ms"`
	Acknowledgement  string `json:"acknowledgement"`
	MaxClockSkewMS   int    `json:"max_clock_skew_ms"`
}

// RadioObservationCapability returns the negotiated extension only when the
// server advertised the exact protocol and bounded parameters.
func (c *Client) RadioObservationCapability() (*RadioObservationOffer, error) {
	cfg := c.cfg.Load()
	if cfg == nil || !IsV2Key(cfg.WavelogKey) {
		return nil, nil
	}
	info, err := c.GetTokenInfo()
	if err != nil {
		return nil, err
	}
	offer, ok := info.Extensions[RadioObservationProtocol]
	if !ok {
		return nil, nil
	}
	if offer.Version != 1 || offer.Acknowledgement != "echo" || offer.ReportIntervalMS < 1000 || offer.ReportIntervalMS > 30000 || offer.MaxClockSkewMS < 1000 || offer.MaxClockSkewMS > 600000 {
		return nil, fmt.Errorf("unsupported radio observation capability")
	}
	return &offer, nil
}

// MissingScopes returns the RequiredScopes the token does not carry.
func (t *TokenInfo) MissingScopes() []string {
	granted := make(map[string]bool, len(t.Scopes))
	for _, s := range t.Scopes {
		granted[s] = true
	}
	var missing []string
	for _, s := range RequiredScopes {
		if !granted[s] {
			missing = append(missing, s)
		}
	}
	return missing
}

// GetTokenInfo fetches token metadata from the v2 API. The endpoint needs no scope,
// so it works even for a token that is missing everything else — which makes it the
// right probe for the connectivity test.
func (c *Client) GetTokenInfo() (*TokenInfo, error) {
	cfg := c.cfg.Load()
	endpoint := baseURL(cfg) + "/api/v2/token"

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
	debug.Log("[WL] token info HTTP %d: %s", resp.StatusCode, string(data))

	if strings.Contains(string(data), "<html") || strings.Contains(string(data), "<!DOCTYPE") {
		return nil, fmt.Errorf("wrong URL")
	}

	var vr v2Response
	if err := json.Unmarshal(data, &vr); err != nil {
		return nil, fmt.Errorf("invalid response")
	}
	if vr.Error != nil {
		return nil, fmt.Errorf("%s", vr.reason())
	}

	var info TokenInfo
	if err := json.Unmarshal(vr.Data, &info); err != nil {
		return nil, fmt.Errorf("invalid response")
	}
	return &info, nil
}

// RadioData holds the data sent to Wavelog's /api/radio endpoint.
type RadioData struct {
	Frequency              int64
	Mode                   string
	Power                  float64
	FrequencyRx            int64
	ModeRx                 string
	Split                  bool
	PropMode               string
	SatName                string
	SatMode                string
	SourceSessionID        string
	SourceSessionStartedAt time.Time
	SourceSequence         uint64
	SourceObservedAt       time.Time
	SourceProtocol         string
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
	if IsV2Key(cfg.WavelogKey) {
		req.Header.Set("Authorization", "Bearer "+cfg.WavelogKey)
	}
	return req, nil
}

// SendQSO posts an ADIF string to Wavelog. dryRun asks the server to validate only.
// v1 uses POST /api/qso (+/true), v2 uses POST /api/v2/qso with a dryrun flag; both
// submit ADIF so the local ADIF pipeline stays unchanged.
func (c *Client) SendQSO(adifStr string, dryRun bool) (*QSOResult, error) {
	cfg := c.cfg.Load()
	v2 := IsV2Key(cfg.WavelogKey)

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
	Radio                  string  `json:"radio"`
	Key                    string  `json:"key,omitempty"`
	Frequency              int64   `json:"frequency"`
	Mode                   string  `json:"mode"`
	Power                  float64 `json:"power,omitempty"`
	FrequencyRx            int64   `json:"frequency_rx,omitempty"`
	ModeRx                 string  `json:"mode_rx,omitempty"`
	PropMode               string  `json:"prop_mode,omitempty"`
	SatName                string  `json:"sat_name,omitempty"`
	SatMode                string  `json:"sat_mode,omitempty"`
	SourceSessionID        string  `json:"source_session_id,omitempty"`
	SourceSessionStartedAt string  `json:"source_session_started_at,omitempty"`
	SourceSequence         uint64  `json:"source_sequence,omitempty"`
	SourceObservedAt       string  `json:"source_observed_at,omitempty"`
	SourceProtocol         string  `json:"source_protocol,omitempty"`
}

// UpdateRadioStatus posts radio status to Wavelog's radio endpoint.
// Frequencies are Hz in both API versions, so only path and auth differ.
func (c *Client) UpdateRadioStatus(data RadioData) error {
	_, err := c.updateRadioStatus(context.Background(), data, false, c.cfg.Load())
	return err
}

// UpdateRadioStatusForProfile preserves the profile binding captured with a
// poll even if the active UI profile changes before delivery completes.
func (c *Client) UpdateRadioStatusForProfile(data RadioData, cfg *config.Profile) error {
	_, err := c.updateRadioStatus(context.Background(), data, false, cfg)
	return err
}

// UpdateRadioObservation sends one negotiated observation and requires the
// credential-bound server to echo the exact identity and canonical payload hash.
func (c *Client) UpdateRadioObservation(ctx context.Context, data RadioData) error {
	_, err := c.updateRadioStatus(ctx, data, true, c.cfg.Load())
	return err
}

// UpdateRadioObservationForProfile keeps the credential and station used for
// a poll immutable while its asynchronous delivery is in flight.
func (c *Client) UpdateRadioObservationForProfile(ctx context.Context, data RadioData, cfg *config.Profile) error {
	_, err := c.updateRadioStatus(ctx, data, true, cfg)
	return err
}

func (c *Client) updateRadioStatus(ctx context.Context, data RadioData, requireAck bool, cfg *config.Profile) (string, error) {
	endpoint := baseURL(cfg) + "/api/radio"
	if IsV2Key(cfg.WavelogKey) {
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
	if IsV2Key(key) {
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
	// Correlation fields are emitted only after explicit capability negotiation.
	if requireAck && data.SourceProtocol == RadioObservationProtocol && IsV2Key(cfg.WavelogKey) {
		p.SourceProtocol = data.SourceProtocol
		p.SourceSessionID = data.SourceSessionID
		p.SourceSessionStartedAt = formatObservationTime(data.SourceSessionStartedAt)
		p.SourceSequence = data.SourceSequence
		if !data.SourceObservedAt.IsZero() {
			p.SourceObservedAt = formatObservationTime(data.SourceObservedAt)
		}
	} else if requireAck {
		return "", fmt.Errorf("radio observation capability not negotiated")
	}
	if data.Power > 0 {
		p.Power = data.Power
	}

	body, err := json.Marshal(p)
	if err != nil {
		return "", err
	}

	req, err := c.newRequest("POST", endpoint, body, cfg)
	if err != nil {
		return "", err
	}
	req = req.WithContext(ctx)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("radio status HTTP %d", resp.StatusCode)
	}
	if !requireAck {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return "", nil
	}
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return "", err
	}
	var envelope struct {
		Data struct {
			Observation struct {
				Protocol         string `json:"protocol"`
				SessionID        string `json:"session_id"`
				SessionStartedAt string `json:"session_started_at"`
				Sequence         uint64 `json:"sequence"`
				ObservedAt       string `json:"observed_at"`
				PayloadHash      string `json:"payload_hash"`
			} `json:"observation"`
		} `json:"data"`
	}
	if err := json.Unmarshal(responseBody, &envelope); err != nil {
		return "", fmt.Errorf("invalid radio observation acknowledgement")
	}
	ack := envelope.Data.Observation
	expectedHash := radioObservationPayloadHash(p)
	if ack.Protocol != p.SourceProtocol || ack.SessionID != p.SourceSessionID || ack.SessionStartedAt != p.SourceSessionStartedAt ||
		ack.Sequence != p.SourceSequence || ack.ObservedAt != p.SourceObservedAt || ack.PayloadHash != expectedHash {
		return "", fmt.Errorf("radio observation acknowledgement mismatch")
	}
	return expectedHash, nil
}

func formatObservationTime(value time.Time) string {
	return value.UTC().Truncate(time.Millisecond).Format("2006-01-02T15:04:05.000Z")
}

func radioObservationPayloadHash(p radioPayload) string {
	canonical := struct {
		Frequency        int64   `json:"frequency"`
		Mode             string  `json:"mode"`
		FrequencyRx      *int64  `json:"frequency_rx"`
		ModeRx           *string `json:"mode_rx"`
		Radio            string  `json:"radio"`
		SessionID        string  `json:"session_id"`
		SessionStartedAt string  `json:"session_started_at"`
		Sequence         uint64  `json:"sequence"`
		ObservedAt       string  `json:"observed_at"`
	}{
		Frequency: p.Frequency, Mode: p.Mode, Radio: p.Radio, SessionID: p.SourceSessionID,
		SessionStartedAt: p.SourceSessionStartedAt, Sequence: p.SourceSequence, ObservedAt: p.SourceObservedAt,
	}
	if p.FrequencyRx != 0 {
		canonical.FrequencyRx = &p.FrequencyRx
	}
	if p.ModeRx != "" {
		canonical.ModeRx = &p.ModeRx
	}
	body, _ := json.Marshal(canonical)
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
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
	v2 := IsV2Key(cfg.WavelogKey)

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
