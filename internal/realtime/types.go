package realtime

import "encoding/json"

// ActionMessage is sent from client to server to invoke a command.
type ActionMessage struct {
	ID      string          `json:"id"`
	Action  string          `json:"action"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// ResponseMessage is sent from server back to client in response to an ActionMessage.
type ResponseMessage struct {
	ID      string      `json:"id"`
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
}

// EventMessage is pushed from server to client(s) asynchronously.
type EventMessage struct {
	Event     string      `json:"event"`
	RefID     string      `json:"ref_id,omitempty"`
	Data      interface{} `json:"data"`
	Timestamp int64       `json:"timestamp"`
}

// SystemPingMessage is used for latency measurement.
type SystemPingMessage struct {
	Type      string `json:"type"`
	Timestamp int64  `json:"timestamp"`
}
