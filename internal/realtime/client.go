package realtime

import (
	"encoding/json"
	"sync"

	"github.com/coder/websocket"
)

// Client represents a connected WebSocket session.
type Client struct {
	ID        string
	hub       *Hub
	conn      *websocket.Conn
	sendChan  chan []byte
	mu        sync.RWMutex
	closed    bool
	closeOnce sync.Once
}

// NewClient initializes a client representation.
func NewClient(id string, hub *Hub, conn *websocket.Conn) *Client {
	return &Client{
		ID:       id,
		hub:      hub,
		conn:     conn,
		sendChan: make(chan []byte, 256),
	}
}

// Send queues raw bytes to the outgoing channel if the client is active.
func (c *Client) Send(data []byte) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.closed {
		return
	}

	select {
	case c.sendChan <- data:
	default:
		// Drop message if buffer is saturated to avoid blocking the hub
	}
}

// SendJSON marshals and queues a JSON payload safely.
func (c *Client) SendJSON(v interface{}) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.Send(data)
	return nil
}

// Close terminates client channels and underlying connection safely without panics.
func (c *Client) Close() {
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.closed = true
		close(c.sendChan)
		c.mu.Unlock()

		if c.conn != nil {
			_ = c.conn.Close(websocket.StatusNormalClosure, "client closed")
		}
	})
}

