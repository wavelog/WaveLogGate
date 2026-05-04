package udp

import (
	"fmt"
	"net"
	"strings"

	"waveloggate/internal/adif"
	"waveloggate/internal/debug"
	"waveloggate/internal/wavelog"
)

// maxConcurrentHandlers limits how many datagrams are processed in parallel.
const maxConcurrentHandlers = 16

// Server is the UDP listener for WSJT-X / FLDigi packets.
type Server struct {
	port     int
	wlClient *wavelog.Client
	onResult func(result *wavelog.QSOResult)
	onStatus func(msg string)
	conn     *net.UDPConn
	sem      chan struct{}
}

// New creates a new UDP server.
func New(port int, wlClient *wavelog.Client, onResult func(result *wavelog.QSOResult), onStatus func(msg string)) *Server {
	return &Server{
		port:     port,
		wlClient: wlClient,
		onResult: onResult,
		onStatus: onStatus,
		sem:      make(chan struct{}, maxConcurrentHandlers),
	}
}

// Start binds the UDP socket and begins receiving datagrams.
func (s *Server) Start() error {
	addr := &net.UDPAddr{
		IP:   net.IPv4zero,
		Port: s.port,
	}
	conn, err := net.ListenUDP("udp4", addr)
	if err != nil {
		if strings.Contains(err.Error(), "address already in use") {
			return fmt.Errorf("Port %d already in use. Stop the other application and restart.", s.port)
		}
		return err
	}
	s.conn = conn

	if s.onStatus != nil {
		s.onStatus(fmt.Sprintf("Waiting for QSO / Listening on UDP %d", s.port))
	}

	go s.readLoop()
	return nil
}

// Stop closes the UDP connection.
func (s *Server) Stop() {
	if s.conn != nil {
		s.conn.Close()
	}
}

func (s *Server) readLoop() {
	buf := make([]byte, 65536)
	for {
		n, _, err := s.conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		data := string(buf[:n])
		s.sem <- struct{}{}
		go func() {
			defer func() { <-s.sem }()
			s.handleDatagram(data)
		}()
	}
}

func (s *Server) handleDatagram(data string) {
	debug.Log("[UDP] raw datagram received (%d bytes)", len(data))

	var fields map[string]string
	var err error

	if strings.Contains(data, "xml") {
		debug.Log("[UDP] detected format: XML (FLDigi/N1MM)")
		fields, err = adif.ParseXML(data)
		if err != nil {
			debug.Log("[UDP] XML parse failed: %v", err)
			if s.onStatus != nil {
				s.onStatus("Received broken XML: " + err.Error())
			}
			return
		}
	} else {
		debug.Log("[UDP] detected format: ADIF (WSJT-X)")
		normalized := adif.NormalizeTXPwr(data)
		normalized = adif.NormalizeKIndex(normalized)
		fields = adif.Parse(normalized)
	}

	if len(fields) == 0 {
		debug.Log("[UDP] no ADIF fields parsed — dropping datagram")
		if s.onStatus != nil {
			s.onStatus("No ADIF detected. WSJT-X: Use ONLY Secondary UDP-Server")
		}
		return
	}

	debug.Log("[UDP] parsed %d fields: %v", len(fields), fields)

	// Enrich band if missing.
	if _, ok := fields["BAND"]; !ok {
		if freqStr, ok := fields["FREQ"]; ok {
			var mhz float64
			fmt.Sscanf(freqStr, "%f", &mhz)
			if band := adif.FreqToBand(mhz); band != "" {
				fields["BAND"] = band
				debug.Log("[UDP] band enriched from freq %s MHz -> %s", freqStr, band)
			}
		}
	}

	adifStr := adif.MapToADIF(fields)
	debug.Log("[UDP] final ADIF: %s", adifStr)

	if s.wlClient == nil {
		return
	}

	result, err := s.wlClient.SendQSO(adifStr, false)
	if err != nil {
		debug.Log("[UDP] SendQSO error: %v", err)
		result = &wavelog.QSOResult{Success: false, Reason: err.Error()}
	}

	debug.Log("[UDP] QSO result: success=%v call=%s band=%s mode=%s reason=%s",
		result.Success, result.Call, result.Band, result.Mode, result.Reason)

	if s.onResult != nil {
		s.onResult(result)
	}
}
