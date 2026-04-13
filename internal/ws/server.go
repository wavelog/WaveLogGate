package ws

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// RadioStatusMsg is the message broadcast to WebSocket clients.
type RadioStatusMsg struct {
	Type        string  `json:"type"`
	Frequency   int64   `json:"frequency"`
	Mode        string  `json:"mode"`
	Power       float64 `json:"power,omitempty"`
	Radio       string  `json:"radio"`
	Timestamp   int64   `json:"timestamp"`
	FrequencyRx int64   `json:"frequency_rx,omitempty"`
	ModeRx      string  `json:"mode_rx,omitempty"`
}

type client struct {
	conn      *websocket.Conn
	send      chan []byte
	closeOnce sync.Once
}

// close shuts down a client exactly once — prevents double-close panics.
func (c *client) close() {
	c.closeOnce.Do(func() {
		close(c.send)
		c.conn.Close()
	})
}

// Hub manages WebSocket client connections and broadcasting.
type Hub struct {
	mu        sync.RWMutex
	clients   map[*client]struct{}
	current   []byte // last status for welcome messages
	OnMessage func(data []byte)

	srvMu   sync.Mutex
	servers []*http.Server
}

// NewHub creates a new Hub.
func NewHub() *Hub {
	return &Hub{
		clients: make(map[*client]struct{}),
	}
}

func (h *Hub) broadcast(msg []byte) {
	h.mu.Lock()
	h.current = msg
	for c := range h.clients {
		select {
		case c.send <- msg:
		default:
			delete(h.clients, c)
			c.close()
		}
	}
	h.mu.Unlock()
}

// BroadcastStatus serializes and broadcasts a RadioStatusMsg.
func (h *Hub) BroadcastStatus(msg RadioStatusMsg) {
	msg.Timestamp = time.Now().UnixMilli()
	data, err := json.Marshal(msg)
	if err != nil {
		return
	}
	h.broadcast(data)
}

func (h *Hub) add(c *client) {
	h.mu.Lock()
	h.clients[c] = struct{}{}
	current := h.current
	h.mu.Unlock()

	// Send welcome.
	welcome, _ := json.Marshal(map[string]string{
		"type":    "welcome",
		"message": "Connected to WavelogGate WebSocket server",
	})
	c.send <- welcome

	// Send current status if available.
	if current != nil {
		c.send <- current
	}
}

func (h *Hub) remove(c *client) {
	h.mu.Lock()
	delete(h.clients, c)
	h.mu.Unlock()
	c.close()
}

// ServeHTTP handles WebSocket upgrade and client lifecycle.
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	c := &client{
		conn: conn,
		send: make(chan []byte, 32),
	}
	h.add(c)

	// Writer goroutine: drains send channel, exits when channel is closed.
	go func() {
		for msg := range c.send {
			if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				break
			}
		}
		h.remove(c)
	}()

	// Reader: forwards incoming messages; on disconnect removes client.
	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			break
		}
		if h.OnMessage != nil {
			h.OnMessage(msg)
		}
	}
	h.remove(c)
}

// Shutdown gracefully stops all HTTP servers started by this Hub.
func (h *Hub) Shutdown(ctx context.Context) {
	h.srvMu.Lock()
	srvs := h.servers
	h.servers = nil
	h.srvMu.Unlock()
	for _, srv := range srvs {
		_ = srv.Shutdown(ctx)
	}
}

func (h *Hub) addServer(srv *http.Server) {
	h.srvMu.Lock()
	h.servers = append(h.servers, srv)
	h.srvMu.Unlock()
}

// ListenAndServe starts the WebSocket server on the given address.
func (h *Hub) ListenAndServe(addr string) error {
	mux := http.NewServeMux()
	mux.Handle("/", h)
	srv := &http.Server{Addr: addr, Handler: mux}
	h.addServer(srv)
	err := srv.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

// ListenAndServeTLS starts a secure WebSocket (WSS) server on the given address.
func (h *Hub) ListenAndServeTLS(addr, certFile, keyFile string) error {
	mux := http.NewServeMux()
	mux.Handle("/", h)
	srv := &http.Server{Addr: addr, Handler: mux}
	h.addServer(srv)
	err := srv.ListenAndServeTLS(certFile, keyFile)
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}
