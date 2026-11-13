package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"app/internal/apperr"
	"app/internal/contracts"
	"app/internal/progress"
)

const testToken = "secret-token"

type echoPayload struct {
	Text string `json:"text"`
}

func (p echoPayload) Validate() error {
	if p.Text == "" {
		return errors.New("text is required")
	}
	return nil
}

type testModule struct{ started atomic.Int32 }

func (*testModule) Name() string { return "test" }

func (m *testModule) Register(r *Registry) {
	Handle(r, "t:echo", func(_ context.Context, _ *Request, p echoPayload) (echoPayload, error) { return p, nil })
	Handle(r, "t:slow", func(ctx context.Context, req *Request, _ contracts.Empty) (contracts.Empty, error) {
		m.started.Add(1)
		progress.Report(ctx, progress.Update{Percent: 50, Message: "half"})
		<-ctx.Done()
		return contracts.Empty{}, ctx.Err()
	})
	Handle(r, "t:panic", func(context.Context, *Request, contracts.Empty) (contracts.Empty, error) { panic("boom") })
	Handle(r, "t:fail", func(context.Context, *Request, contracts.Empty) (contracts.Empty, error) {
		return contracts.Empty{}, errors.New("db password=hunter2 leaked?")
	})
	Handle(r, "t:notfound", func(context.Context, *Request, contracts.Empty) (contracts.Empty, error) {
		return contracts.Empty{}, apperr.NotFound("no such thing")
	})
	Handle(r, "t:validation", func(context.Context, *Request, contracts.Empty) (contracts.Empty, error) {
		return contracts.Empty{}, apperr.Validation("invalid input parameter")
	})
	Handle(r, "t:heartbeat", func(ctx context.Context, req *Request, _ contracts.Empty) (contracts.Empty, error) {
		for i := 1; i <= 3; i++ {
			time.Sleep(30 * time.Millisecond)
			if ctx.Err() != nil {
				return contracts.Empty{}, ctx.Err()
			}
			progress.Report(ctx, progress.Update{Percent: float64(i * 30), Message: "progressing"})
		}
		return contracts.Empty{}, nil
	})
	Handle(r, contracts.ActionBinariesDownload, func(ctx context.Context, req *Request, _ contracts.Empty) (contracts.Empty, error) {
		return contracts.Empty{}, nil
	})
}

var testActions = []string{
	"t:echo", "t:slow", "t:panic", "t:fail", "t:notfound",
	"t:validation", "t:heartbeat", contracts.ActionBinariesDownload,
}

func newTestHub(t *testing.T, mutate func(*Options)) (*Hub, *httptest.Server, *testModule) {
	t.Helper()
	o := Options{
		AuthToken:            testToken,
		MaxConcurrentActions: 4,
		ActionTimeout:        2 * time.Second,
		LongActionTimeout:    4 * time.Second,
		KeepAliveInterval:    10 * time.Millisecond,
		QueueTimeout:         200 * time.Millisecond,
		ClientBufferSize:     16,
		MaxMessageBytes:      4096,
		PingInterval:         time.Minute,
		WriteTimeout:         time.Second,
		DeclaredActions:      testActions,
		DeclaredEvents:       []string{"t:happened"},
		Logger:               slog.New(slog.DiscardHandler),
	}
	if mutate != nil {
		mutate(&o)
	}
	h, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	mod := &testModule{}
	if err := h.Mount(mod); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = h.Close(ctx)
		srv.Close()
	})
	return h, srv, mod
}

type client struct {
	t    *testing.T
	conn *websocket.Conn
}

func dial(t *testing.T, srv *httptest.Server, protocols ...string) (*client, *http.Response, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	url := "ws" + strings.TrimPrefix(srv.URL, "http")
	conn, resp, err := websocket.Dial(ctx, url, &websocket.DialOptions{Subprotocols: protocols})
	if err != nil {
		return nil, resp, err
	}
	t.Cleanup(func() { _ = conn.CloseNow() })
	return &client{t: t, conn: conn}, resp, nil
}

func mustDial(t *testing.T, srv *httptest.Server) *client {
	c, _, err := dial(t, srv, contracts.ProtocolVersion, contracts.AuthSubprotocolPrefix+testToken)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func (c *client) send(v any) {
	c.t.Helper()
	data, _ := json.Marshal(v)
	if err := c.conn.Write(context.Background(), websocket.MessageText, data); err != nil {
		c.t.Fatal(err)
	}
}

func (c *client) action(id, name string, payload any) {
	c.t.Helper()
	raw, _ := json.Marshal(payload)
	c.send(contracts.ClientMessage{Type: contracts.MessageAction, ID: id, Action: name, Payload: raw})
}

type frame struct {
	Type    string               `json:"type"`
	ID      string               `json:"id"`
	Success bool                 `json:"success"`
	Data    json.RawMessage      `json:"data"`
	Error   *contracts.ErrorBody `json:"error"`
	Event   string               `json:"event"`
	RefID   string               `json:"ref_id"`
}

func (c *client) read() frame {
	c.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, data, err := c.conn.Read(ctx)
	if err != nil {
		c.t.Fatal(err)
	}
	var f frame
	if err := json.Unmarshal(data, &f); err != nil {
		c.t.Fatal(err)
	}
	return f
}

// response reads frames until the response for id arrives, collecting events.
func (c *client) response(id string) (frame, []frame) {
	c.t.Helper()
	var events []frame
	for {
		f := c.read()
		if f.Type == string(contracts.MessageResponse) && f.ID == id {
			return f, events
		}
		events = append(events, f)
	}
}

func TestAuthentication(t *testing.T) {
	_, srv, _ := newTestHub(t, nil)

	if _, resp, err := dial(t, srv, contracts.ProtocolVersion); err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("missing token: err=%v", err)
	}
	if _, _, err := dial(t, srv, contracts.ProtocolVersion, contracts.AuthSubprotocolPrefix+"wrong"); err == nil {
		t.Fatal("wrong token accepted")
	}
	c, _, err := dial(t, srv, contracts.ProtocolVersion, contracts.AuthSubprotocolPrefix+testToken)
	if err != nil {
		t.Fatal(err)
	}
	if c.conn.Subprotocol() != contracts.ProtocolVersion {
		t.Fatalf("negotiated %q", c.conn.Subprotocol())
	}
}

func TestBearerAndProtocolVersion(t *testing.T) {
	_, srv, _ := newTestHub(t, nil)
	url := "ws" + strings.TrimPrefix(srv.URL, "http")
	ctx := context.Background()

	h := http.Header{"Authorization": {"Bearer " + testToken}}
	conn, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: h, Subprotocols: []string{contracts.ProtocolVersion}})
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.CloseNow()

	// Without the protocol version the server closes the connection.
	conn, _, err = websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: h})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	if _, _, err := conn.Read(ctx); websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
		t.Fatalf("expected policy violation, got %v", err)
	}
}

// Every supported version (the current one and, during a rollout, the previous) negotiates as itself;
// the newest offered by the client wins.
func TestSupportedProtocolVersionsNegotiate(t *testing.T) {
	_, srv, _ := newTestHub(t, nil)
	versions := contracts.SupportedProtocolVersions()
	if len(versions) == 0 || versions[0] != contracts.ProtocolVersion {
		t.Fatalf("current version must come first: %v", versions)
	}
	for _, v := range versions {
		c, _, err := dial(t, srv, v, contracts.AuthSubprotocolPrefix+testToken)
		if err != nil {
			t.Fatalf("%s: %v", v, err)
		}
		if got := c.conn.Subprotocol(); got != v {
			t.Fatalf("offered %q, negotiated %q", v, got)
		}
	}
}

func TestAnonymousRequiresOptIn(t *testing.T) {
	if _, err := New(Options{Logger: slog.New(slog.DiscardHandler)}); err == nil {
		t.Fatal("hub without token or AllowAnonymous accepted")
	}
}

func TestTypedHandlerValidationAndErrors(t *testing.T) {
	_, srv, _ := newTestHub(t, nil)
	c := mustDial(t, srv)

	c.action("1", "t:echo", echoPayload{Text: "hi"})
	if f, _ := c.response("1"); !f.Success || !strings.Contains(string(f.Data), "hi") {
		t.Fatalf("echo: %+v", f)
	}

	cases := []struct {
		id, action string
		payload    any
		code       apperr.Code
	}{
		{"2", "t:echo", map[string]any{"text": "x", "extra": 1}, apperr.CodeValidation},
		{"3", "t:echo", map[string]any{}, apperr.CodeValidation},
		{"4", "t:nope", nil, apperr.CodeUnknownAction},
		{"5", "t:panic", nil, apperr.CodeInternal},
		{"6", "t:notfound", nil, apperr.CodeNotFound},
	}
	for _, tc := range cases {
		c.action(tc.id, tc.action, tc.payload)
		f, _ := c.response(tc.id)
		if f.Success || f.Error == nil || f.Error.Code != tc.code {
			t.Errorf("%s: %+v", tc.action, f.Error)
		}
	}

	c.action("7", "t:fail", nil)
	f, _ := c.response("7")
	if f.Error == nil || f.Error.Code != apperr.CodeInternal || strings.Contains(f.Error.Message, "hunter2") {
		t.Fatalf("internal error leaked details: %+v", f.Error)
	}
}

func TestMalformedAndDuplicateMessages(t *testing.T) {
	_, srv, _ := newTestHub(t, nil)
	c := mustDial(t, srv)

	_ = c.conn.Write(context.Background(), websocket.MessageText, []byte("{not json"))
	if f := c.read(); f.Error == nil || f.Error.Code != apperr.CodeValidation {
		t.Fatalf("malformed: %+v", f)
	}
	c.send(contracts.ClientMessage{Type: "bogus", ID: "x"})
	if f := c.read(); f.Error == nil || f.Error.Code != apperr.CodeBadRequest {
		t.Fatalf("bogus type: %+v", f)
	}

	c.action("dup", "t:slow", nil)
	c.action("dup", "t:slow", nil)
	if f := c.read(); f.Type == "event" {
		f = c.read()
		if f.Error == nil || f.Error.Code != apperr.CodeBadRequest {
			t.Fatalf("duplicate id: %+v", f)
		}
	} else if f.Error == nil || f.Error.Code != apperr.CodeBadRequest {
		t.Fatalf("duplicate id: %+v", f)
	}
}

func TestCancelAndProgress(t *testing.T) {
	_, srv, _ := newTestHub(t, nil)
	c := mustDial(t, srv)

	c.action("job", "t:slow", nil)
	progress := c.read()
	if progress.Event != contracts.EventActionProgress || progress.RefID != "job" {
		t.Fatalf("expected progress event, got %+v", progress)
	}
	c.send(contracts.ClientMessage{Type: contracts.MessageCancel, ID: "job"})
	f, _ := c.response("job")
	if f.Error == nil || f.Error.Code != apperr.CodeCancelled {
		t.Fatalf("cancel: %+v", f)
	}
}

func TestActionTimeout(t *testing.T) {
	_, srv, _ := newTestHub(t, func(o *Options) { o.ActionTimeout = 100 * time.Millisecond })
	c := mustDial(t, srv)
	c.action("1", "t:slow", nil)
	f, _ := c.response("1")
	if f.Error == nil || f.Error.Code != apperr.CodeTimeout {
		t.Fatalf("timeout: %+v", f)
	}
}

func TestConcurrencyLimit(t *testing.T) {
	_, srv, mod := newTestHub(t, func(o *Options) { o.MaxConcurrentActions = 1 })
	c := mustDial(t, srv)
	c.action("a", "t:slow", nil)
	for mod.started.Load() == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	c.action("b", "t:slow", nil)
	f, _ := c.response("b")
	if f.Error == nil || f.Error.Code != apperr.CodeConcurrencyLimit {
		t.Fatalf("limit: %+v", f)
	}
}

func TestPublishDeclaredEventsOnly(t *testing.T) {
	h, srv, _ := newTestHub(t, nil)
	c := mustDial(t, srv)
	for h.ClientCount() == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	h.Publish("t:undeclared", 1)
	h.Publish("t:happened", map[string]int{"n": 1})
	if f := c.read(); f.Event != "t:happened" {
		t.Fatalf("event: %+v", f)
	}
}

func TestCloseDisconnectsAndCancels(t *testing.T) {
	h, srv, mod := newTestHub(t, nil)
	c := mustDial(t, srv)
	c.action("job", "t:slow", nil)
	for mod.started.Load() == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := h.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if h.ClientCount() != 0 {
		t.Fatal("sessions remain after Close")
	}
	// Frames queued before Close (progress, cancellation response) may still arrive;
	// after them the connection must be closed.
	readCtx, readCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer readCancel()
	for i := 0; ; i++ {
		if _, _, err := c.conn.Read(readCtx); err != nil {
			if readCtx.Err() != nil {
				t.Fatal("connection still open after Close")
			}
			return
		}
		if i > 10 {
			t.Fatal("connection still delivering frames after Close")
		}
	}
}

func TestRegistryRules(t *testing.T) {
	h, err := New(Options{
		AllowAnonymous: true, MaxConcurrentActions: 1, ActionTimeout: time.Second, LongActionTimeout: time.Second, KeepAliveInterval: 10 * time.Millisecond, QueueTimeout: time.Second,
		ClientBufferSize: 1, MaxMessageBytes: 1024, PingInterval: time.Second, WriteTimeout: time.Second,
		DeclaredActions: []string{"a:one", "a:two"}, Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	noop := func(context.Context, *Request, contracts.Empty) (contracts.Empty, error) {
		return contracts.Empty{}, nil
	}

	undeclared := moduleFunc(func(r *Registry) { Handle(r, "a:other", noop) })
	if err := h.Mount(undeclared); err == nil {
		t.Fatal("undeclared action accepted")
	}
	dup := moduleFunc(func(r *Registry) { Handle(r, "a:one", noop); Handle(r, "a:one", noop) })
	if err := h.Mount(dup); err == nil {
		t.Fatal("duplicate action accepted")
	}
	if err := h.VerifyComplete(); err == nil || !strings.Contains(err.Error(), "a:two") {
		t.Fatalf("missing handler not reported: %v", err)
	}
}

type moduleFunc func(r *Registry)

func (moduleFunc) Name() string           { return "func" }
func (m moduleFunc) Register(r *Registry) { m(r) }

func TestValidationSurfacesCleanly(t *testing.T) {
	_, srv, _ := newTestHub(t, nil)
	c := mustDial(t, srv)
	c.action("1", "t:validation", nil)
	f, _ := c.response("1")
	if f.Error == nil {
		t.Fatalf("expected error, got nil response")
	}
	if f.Error.Code != apperr.CodeValidation {
		t.Fatalf("expected CodeValidation (%s), got: %s", apperr.CodeValidation, f.Error.Code)
	}
	if f.Error.Message != "invalid input parameter" {
		t.Fatalf("expected exact message 'invalid input parameter', got: %q", f.Error.Message)
	}
}

func TestHeartbeatExtendsDeadline(t *testing.T) {
	// Set ActionTimeout to 60ms.
	// t:heartbeat takes 3 * 30ms = ~90ms, emitting progress every 30ms.
	// Without heartbeat reset, it would exceed the 60ms deadline and fail.
	_, srv, _ := newTestHub(t, func(o *Options) {
		o.ActionTimeout = 60 * time.Millisecond
	})
	c := mustDial(t, srv)
	c.action("hb1", "t:heartbeat", nil)
	f, _ := c.response("hb1")
	if f.Error != nil {
		t.Fatalf("expected heartbeat action to succeed via deadline extensions, failed with: %+v", f.Error)
	}
}

func TestTieredActionTimeout(t *testing.T) {
	h, _, _ := newTestHub(t, func(o *Options) {
		o.ActionTimeout = 50 * time.Millisecond
		o.LongActionTimeout = 300 * time.Millisecond
	})
	if d := h.actionTimeout("t:slow"); d != 50*time.Millisecond {
		t.Fatalf("expected 50ms for standard action, got %v", d)
	}
	if d := h.actionTimeout(contracts.ActionBinariesDownload); d != 300*time.Millisecond {
		t.Fatalf("expected 300ms for binaries download, got %v", d)
	}
	if d := h.actionTimeout(contracts.ActionWorkflowRunGraph); d != 300*time.Millisecond {
		t.Fatalf("expected 300ms for workflow run graph, got %v", d)
	}
	if d := h.actionTimeout(contracts.ActionWorkflowRunNode); d != 300*time.Millisecond {
		t.Fatalf("expected 300ms for workflow run node, got %v", d)
	}
	if d := h.actionTimeout(contracts.ActionDiagnosticsExport); d != 300*time.Millisecond {
		t.Fatalf("expected 300ms for diagnostics export, got %v", d)
	}
}

// Every contracts.LongActions entry gets the long idle timeout; waits on bounded external work
// (agent sign-in/connect/probe) stay on the short one and keep themselves alive instead.
func TestEveryLongActionUsesLongTimeout(t *testing.T) {
	h, _, _ := newTestHub(t, func(o *Options) {
		o.ActionTimeout = 50 * time.Millisecond
		o.LongActionTimeout = 300 * time.Millisecond
	})
	for _, a := range contracts.LongActions() {
		if d := h.actionTimeout(a); d != 300*time.Millisecond {
			t.Errorf("%s: timeout %v, want long timeout", a, d)
		}
	}
	if !contracts.IsLongAction(contracts.ActionAgentResolveApproval) {
		t.Error("agent:resolve_approval runs a workflow and must be a long action")
	}
	for _, a := range []string{contracts.ActionAgentLogin, contracts.ActionAgentConnect, contracts.ActionSystemPing} {
		if contracts.IsLongAction(a) {
			t.Errorf("%s must rely on keep-alive, not the long timeout", a)
		}
	}
}

// End to end, like the binaries download: an action that waits silently (agent sign-in in the
// browser) keeps itself alive with progress.KeepAlive and outlives a much shorter idle timeout;
// the client sees heartbeat frames; the same wait without keep-alive still times out.
func TestKeepAliveOutlivesIdleTimeout(t *testing.T) {
	h, srv, _ := newTestHub(t, func(o *Options) {
		o.ActionTimeout = 60 * time.Millisecond
		o.KeepAliveInterval = 15 * time.Millisecond
		o.DeclaredActions = append(append([]string{}, testActions...), contracts.ActionAgentLogin, "t:silent")
	})
	wait := func(ctx context.Context) error {
		select {
		case <-time.After(300 * time.Millisecond):
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if err := h.Mount(moduleFunc(func(r *Registry) {
		Handle(r, contracts.ActionAgentLogin, func(ctx context.Context, _ *Request, _ contracts.Empty) (contracts.Empty, error) {
			defer progress.KeepAlive(ctx)()
			return contracts.Empty{}, wait(ctx)
		})
		Handle(r, "t:silent", func(ctx context.Context, _ *Request, _ contracts.Empty) (contracts.Empty, error) {
			return contracts.Empty{}, wait(ctx)
		})
	})); err != nil {
		t.Fatal(err)
	}
	c := mustDial(t, srv)
	c.action("login", contracts.ActionAgentLogin, nil)
	f, events := c.response("login")
	if f.Error != nil {
		t.Fatalf("kept-alive action timed out: %+v", f.Error)
	}
	beats := 0
	for _, e := range events {
		if e.Event == contracts.EventActionProgress && e.RefID == "login" && strings.Contains(string(e.Data), `"heartbeat":true`) {
			beats++
		}
	}
	if beats == 0 {
		t.Fatal("client received no heartbeat frames; its own timer would expire")
	}

	c.action("silent", "t:silent", nil)
	f, _ = c.response("silent")
	if f.Error == nil || f.Error.Code != apperr.CodeTimeout {
		t.Fatalf("silent action without keep-alive should time out, got %+v", f.Error)
	}
}
