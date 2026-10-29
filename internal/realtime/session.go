package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/coder/websocket"

	"app/internal/apperr"
	"app/internal/contracts"
	"app/internal/safego"
)

// session is one connected WebSocket client.
type session struct {
	id     string
	hub    *Hub
	conn   *websocket.Conn
	ctx    context.Context
	cancel context.CancelFunc

	sendMu sync.RWMutex
	send   chan []byte
	closed bool

	inflightMu sync.Mutex
	inflight   map[string]context.CancelFunc
}

func newSession(id string, h *Hub, conn *websocket.Conn) *session {
	ctx, cancel := context.WithCancel(h.ctx)
	return &session{
		id:       id,
		hub:      h,
		conn:     conn,
		ctx:      ctx,
		cancel:   cancel,
		send:     make(chan []byte, h.opts.ClientBufferSize),
		inflight: map[string]context.CancelFunc{},
	}
}

// run blocks until the connection ends, then releases every resource.
func (s *session) run() {
	s.conn.SetReadLimit(s.hub.opts.MaxMessageBytes)
	done := make(chan struct{})
	safego.Go("realtime.writeLoop", func() {
		defer close(done)
		s.writeLoop()
	})
	s.readLoop()
	s.close(websocket.StatusNormalClosure, "")
	<-done
}

func (s *session) readLoop() {
	for {
		_, data, err := s.conn.Read(s.ctx)
		if err != nil {
			return
		}
		var msg contracts.ClientMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			s.respondError("", apperr.Validation("malformed message"))
			continue
		}
		switch msg.Type {
		case contracts.MessageAction:
			s.handleAction(msg)
		case contracts.MessageCancel:
			s.cancelInflight(msg.ID)
		default:
			s.respondError(msg.ID, apperr.New(apperr.CodeBadRequest, "unknown message type"))
		}
	}
}

func (s *session) writeLoop() {
	ticker := time.NewTicker(s.hub.opts.PingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case msg, ok := <-s.send:
			if !ok {
				return
			}
			if err := s.write(msg); err != nil {
				s.cancel()
				return
			}
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(s.ctx, s.hub.opts.WriteTimeout)
			err := s.conn.Ping(ctx)
			cancel()
			if err != nil {
				s.cancel()
				return
			}
		}
	}
}

func (s *session) write(msg []byte) error {
	ctx, cancel := context.WithTimeout(s.ctx, s.hub.opts.WriteTimeout)
	defer cancel()
	return s.conn.Write(ctx, websocket.MessageText, msg)
}

// enqueue queues a frame. A client that cannot keep up is disconnected instead of
// silently losing responses.
func (s *session) enqueue(v any) {
	data, err := json.Marshal(v)
	if err != nil {
		s.hub.log.Error("realtime: marshal outbound message", "client_id", s.id, "error", err)
		return
	}
	s.sendMu.RLock()
	defer s.sendMu.RUnlock()
	if s.closed {
		return
	}
	select {
	case s.send <- data:
	default:
		s.hub.log.Warn("realtime: client too slow, disconnecting", "client_id", s.id)
		s.cancel()
	}
}

func (s *session) respond(id string, data any) {
	s.enqueue(contracts.ResponseMessage{Type: contracts.MessageResponse, ID: id, Success: true, Data: data})
}

func (s *session) respondError(id string, err error) {
	s.enqueue(contracts.ResponseMessage{Type: contracts.MessageResponse, ID: id, Error: contracts.NewErrorBody(err)})
}

func (s *session) emit(event, refID string, data any) {
	s.enqueue(contracts.EventMessage{
		Type: contracts.MessageEvent, Event: event, RefID: refID, Data: data, Timestamp: time.Now().UnixMilli(),
	})
}

// handleAction acquires a concurrency slot (applying back-pressure to this client),
// then executes the handler in its own goroutine.
func (s *session) handleAction(msg contracts.ClientMessage) {
	if msg.ID == "" {
		s.respondError("", apperr.New(apperr.CodeBadRequest, "action id is required"))
		return
	}
	handler, ok := s.hub.registry.Lookup(msg.Action)
	if !ok {
		s.respondError(msg.ID, apperr.New(apperr.CodeUnknownAction, "unknown action: "+msg.Action))
		return
	}

	if !s.hub.acquire(s.ctx) {
		s.respondError(msg.ID, apperr.New(apperr.CodeConcurrencyLimit, "server busy, try again"))
		return
	}

	ctx, cancel := context.WithTimeout(s.ctx, s.hub.opts.ActionTimeout)
	if !s.trackInflight(msg.ID, cancel) {
		cancel()
		s.hub.release()
		s.respondError(msg.ID, apperr.New(apperr.CodeBadRequest, "duplicate action id"))
		return
	}

	req := &Request{
		ID:       msg.ID,
		Action:   msg.Action,
		ClientID: s.id,
		progress: func(p contracts.ActionProgress) { s.emit(contracts.EventActionProgress, msg.ID, p) },
	}

	// The session still holds a WaitGroup slot here, so Add cannot race with Hub.Close's Wait.
	s.hub.wg.Add(1)
	safego.Go("realtime.action", func() {
		defer s.hub.wg.Done()
		defer s.hub.release()
		defer s.untrackInflight(msg.ID)
		defer cancel()

		result, err := s.hub.execute(ctx, req, handler, msg.Payload)
		if err != nil {
			s.respondError(msg.ID, err)
			return
		}
		s.respond(msg.ID, result)
	})
}

func (s *session) trackInflight(id string, cancel context.CancelFunc) bool {
	s.inflightMu.Lock()
	defer s.inflightMu.Unlock()
	if _, dup := s.inflight[id]; dup {
		return false
	}
	s.inflight[id] = cancel
	return true
}

func (s *session) untrackInflight(id string) {
	s.inflightMu.Lock()
	delete(s.inflight, id)
	s.inflightMu.Unlock()
}

func (s *session) cancelInflight(id string) {
	s.inflightMu.Lock()
	cancel, ok := s.inflight[id]
	s.inflightMu.Unlock()
	if ok {
		cancel()
	}
}

func (s *session) close(code websocket.StatusCode, reason string) {
	s.sendMu.Lock()
	if s.closed {
		s.sendMu.Unlock()
		return
	}
	s.closed = true
	close(s.send)
	s.sendMu.Unlock()

	s.cancel()
	if err := s.conn.Close(code, reason); err != nil && !errors.Is(err, context.Canceled) {
		s.hub.log.Debug("realtime: close connection", "client_id", s.id, "error", err)
	}
}
