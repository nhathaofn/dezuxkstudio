// Package contracts is the single source of truth for everything crossing the
// Go ↔ frontend boundary: wire protocol, action/event names, payload DTOs and
// Wails bridge types. `go run ./cmd/contractgen` turns it into
// frontend/src/contracts/generated.ts; CI fails if the two drift apart.
//
// Rules:
//   - Names: actions are "<domain>:<verb>[_<noun>]", events are "<domain>:<past_tense>".
//   - Every action/event must be listed in Actions()/Events(); the registry refuses
//     handlers for undeclared actions and startup fails if a declared action has no handler.
//   - DTOs only use JSON-friendly types; optional fields use pointers + omitempty.
package contracts

import (
	"encoding/json"

	"app/internal/apperr"
)

// ProtocolVersion is negotiated as a WebSocket subprotocol.
const ProtocolVersion = "dezuxk.v1"

// AuthSubprotocolPrefix carries the access token as a second subprotocol ("auth.<token>"),
// keeping it out of URLs and logs.
const AuthSubprotocolPrefix = "auth."

// MessageType discriminates every frame on the wire.
type MessageType string

const (
	MessageAction   MessageType = "action"   // client → server
	MessageCancel   MessageType = "cancel"   // client → server
	MessageResponse MessageType = "response" // server → client
	MessageEvent    MessageType = "event"    // server → client
)

// MessageTypes lists every message type (enum export).
func MessageTypes() []string {
	return []string{string(MessageAction), string(MessageCancel), string(MessageResponse), string(MessageEvent)}
}

// ClientMessage is any frame sent by the client.
type ClientMessage struct {
	Type    MessageType     `json:"type"`
	ID      string          `json:"id"`
	Action  string          `json:"action,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// ErrorBody is the user-safe error returned to clients.
type ErrorBody struct {
	Code    apperr.Code    `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

// ResponseMessage answers exactly one action, correlated by ID.
type ResponseMessage struct {
	Type    MessageType `json:"type"`
	ID      string      `json:"id"`
	Success bool        `json:"success"`
	Data    any         `json:"data,omitempty"`
	Error   *ErrorBody  `json:"error,omitempty"`
}

// EventMessage is pushed by the server. RefID links it to an action request, if any.
type EventMessage struct {
	Type      MessageType `json:"type"`
	Event     string      `json:"event"`
	RefID     string      `json:"ref_id,omitempty"`
	Data      any         `json:"data"`
	Timestamp int64       `json:"timestamp"`
}

// NewErrorBody converts any error into the wire format.
func NewErrorBody(err error) *ErrorBody {
	ae := apperr.From(err)
	return &ErrorBody{Code: ae.Code, Message: ae.Message, Details: ae.Details}
}
