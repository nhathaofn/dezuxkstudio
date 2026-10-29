package realtime

import (
	"encoding/json"
	"time"
)

// Broadcaster sends event messages to individual or all connected clients.
type Broadcaster struct {
	hub *Hub
}

// NewBroadcaster creates a broadcaster instance.
func NewBroadcaster(hub *Hub) *Broadcaster {
	return &Broadcaster{hub: hub}
}

// Emit sends an event to a single specific client.
func (b *Broadcaster) Emit(client *Client, event string, refID string, data interface{}) {
	evt := EventMessage{
		Event:     event,
		RefID:     refID,
		Data:      data,
		Timestamp: time.Now().UnixMilli(),
	}
	_ = client.SendJSON(evt)
}

// Broadcast sends an event to all connected clients.
func (b *Broadcaster) Broadcast(event string, refID string, data interface{}) {
	evt := EventMessage{
		Event:     event,
		RefID:     refID,
		Data:      data,
		Timestamp: time.Now().UnixMilli(),
	}

	payload, err := json.Marshal(evt)
	if err != nil {
		return
	}

	clients := b.hub.GetClients()
	for _, client := range clients {
		client.Send(payload)
	}
}
