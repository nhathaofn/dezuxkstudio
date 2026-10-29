// Package test holds end-to-end tests that run the real core (storage, services,
// realtime hub, HTTP server) and talk to it like the frontend does.
// Unit tests live next to the code they test.
package test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"app/internal/config"
	"app/internal/contracts"
	"app/internal/core"
	"app/internal/paths"
	"app/internal/security"
)

type harness struct {
	t     *testing.T
	core  *core.Core
	token string
	cfg   *config.Config
}

func startCore(t *testing.T, mutate func(*config.Config)) *harness {
	t.Helper()
	cfg := config.Default(config.ModeDesktop)
	if mutate != nil {
		mutate(cfg)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	p, err := paths.Resolve("e2e", paths.Overrides{DataDir: dir + "/data", LogDir: dir + "/logs"})
	if err != nil {
		t.Fatal(err)
	}
	p.Config = dir + "/config"
	if err := p.Ensure(); err != nil {
		t.Fatal(err)
	}
	token, _ := security.NewToken()
	c, err := core.New(context.Background(), core.Options{
		Config:        cfg,
		Paths:         p,
		Logger:        slog.New(slog.DiscardHandler),
		AuthToken:     token,
		OpenDirectory: func(string) error { return nil },
		HTTPClient:    http.DefaultClient,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := c.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})
	return &harness{t: t, core: c, token: token, cfg: cfg}
}

func (h *harness) httpURL(path string) string {
	return "http" + strings.TrimPrefix(strings.TrimSuffix(h.core.WSURL(), "/ws"), "ws") + path
}

type wsClient struct {
	t    *testing.T
	conn *websocket.Conn
	seq  int
}

func (h *harness) dial() *wsClient {
	h.t.Helper()
	conn, _, err := websocket.Dial(context.Background(), h.core.WSURL(), &websocket.DialOptions{
		Subprotocols: []string{contracts.ProtocolVersion, contracts.AuthSubprotocolPrefix + h.token},
	})
	if err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(func() { _ = conn.CloseNow() })
	return &wsClient{t: h.t, conn: conn}
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

// call sends an action and returns its response plus any events received meanwhile.
func (c *wsClient) call(action string, payload any) (frame, []frame) {
	c.t.Helper()
	c.seq++
	id := action + "#" + string(rune('a'+c.seq))
	raw, _ := json.Marshal(payload)
	msg, _ := json.Marshal(contracts.ClientMessage{Type: contracts.MessageAction, ID: id, Action: action, Payload: raw})
	if err := c.conn.Write(context.Background(), websocket.MessageText, msg); err != nil {
		c.t.Fatal(err)
	}
	var events []frame
	for {
		f := c.next()
		if f.Type == string(contracts.MessageResponse) && f.ID == id {
			return f, events
		}
		events = append(events, f)
	}
}

func (c *wsClient) next() frame {
	c.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
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

func TestEveryDeclaredActionIsServed(t *testing.T) {
	h := startCore(t, nil)
	c := h.dial()
	for _, spec := range contracts.Actions() {
		resp, _ := c.call(spec.Name, map[string]any{"__probe": true})
		if resp.Error != nil && resp.Error.Code == "ERR_UNKNOWN_ACTION" {
			t.Errorf("%s is declared but not served", spec.Name)
		}
	}
}

func TestPreferencesRoundTripAndBroadcast(t *testing.T) {
	h := startCore(t, nil)
	a, b := h.dial(), h.dial()

	resp, _ := a.call(contracts.ActionPreferencesGet, contracts.Empty{})
	var prefs contracts.Preferences
	if err := json.Unmarshal(resp.Data, &prefs); err != nil || prefs.Theme != "light" {
		t.Fatalf("defaults: %s %v", resp.Data, err)
	}

	resp, _ = a.call(contracts.ActionPreferencesUpdate, map[string]string{"theme": "dark", "language": "vi"})
	if !resp.Success {
		t.Fatalf("update: %+v", resp.Error)
	}
	evt := b.next()
	if evt.Event != contracts.EventPreferencesUpdated || !strings.Contains(string(evt.Data), `"dark"`) {
		t.Fatalf("broadcast: %+v", evt)
	}

	resp, _ = a.call(contracts.ActionPreferencesUpdate, map[string]string{"theme": "neon"})
	if resp.Error == nil || resp.Error.Code != "ERR_VALIDATION" {
		t.Fatalf("invalid theme accepted: %+v", resp)
	}
	resp, _ = a.call(contracts.ActionPreferencesUpdate, map[string]string{})
	if resp.Error == nil || resp.Error.Code != "ERR_VALIDATION" {
		t.Fatalf("empty patch accepted: %+v", resp)
	}
}

func TestDiagnosticsExportReportsProgress(t *testing.T) {
	h := startCore(t, nil)
	c := h.dial()
	resp, events := c.call(contracts.ActionDiagnosticsExport, contracts.Empty{})
	if !resp.Success {
		t.Fatalf("export: %+v", resp.Error)
	}
	progress := 0
	for _, e := range events {
		if e.Event == contracts.EventActionProgress && e.RefID == resp.ID {
			progress++
		}
	}
	if progress == 0 {
		t.Fatal("no progress events")
	}
}

func TestOpenFolderValidatesTarget(t *testing.T) {
	h := startCore(t, nil)
	c := h.dial()
	if resp, _ := c.call(contracts.ActionSystemOpenFolder, map[string]string{"target": "logs"}); !resp.Success {
		t.Fatalf("logs: %+v", resp.Error)
	}
	if resp, _ := c.call(contracts.ActionSystemOpenFolder, map[string]string{"target": "../../etc"}); resp.Error == nil || resp.Error.Code != "ERR_VALIDATION" {
		t.Fatalf("path target accepted: %+v", resp)
	}
}

func TestHTTPEndpoints(t *testing.T) {
	h := startCore(t, nil)

	res, err := http.Get(h.httpURL("/health"))
	if err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("health: %v %v", res, err)
	}
	res.Body.Close()

	res, err = http.Get(h.httpURL("/runtime-config.json"))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if strings.Contains(string(body), h.token) || !strings.Contains(string(body), "ws_url") {
		t.Fatalf("runtime config leaked token or missing url: %s", body)
	}

	res, err = http.Post(h.httpURL("/api/events/broadcast"), "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("broadcast must be disabled by default, got %d", res.StatusCode)
	}

	if _, _, err := websocket.Dial(context.Background(), h.core.WSURL(), &websocket.DialOptions{
		Subprotocols: []string{contracts.ProtocolVersion},
	}); err == nil {
		t.Fatal("WebSocket without token accepted")
	}
}

func TestBroadcastEndpointWhenEnabled(t *testing.T) {
	h := startCore(t, func(c *config.Config) { c.Server.EnableBroadcast = true })
	ws := h.dial()

	post := func(token, contentType, body string) int {
		req, _ := http.NewRequest(http.MethodPost, h.httpURL("/api/events/broadcast"), strings.NewReader(body))
		req.Header.Set("Content-Type", contentType)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}

	if code := post("", "application/json", `{"event":"preferences:updated","data":{}}`); code != http.StatusUnauthorized {
		t.Errorf("no token: %d", code)
	}
	if code := post(h.token, "text/plain", `{"event":"preferences:updated","data":{}}`); code != http.StatusUnsupportedMediaType {
		t.Errorf("text/plain: %d", code)
	}
	if code := post(h.token, "application/json", `{"event":"made:up","data":{}}`); code != http.StatusBadRequest {
		t.Errorf("undeclared event: %d", code)
	}
	if code := post(h.token, "application/json", `{"event":"preferences:updated","data":{"theme":"light","language":"en"}}`); code != http.StatusAccepted {
		t.Fatalf("valid broadcast: %d", code)
	}
	if evt := ws.next(); evt.Event != contracts.EventPreferencesUpdated {
		t.Fatalf("event not delivered: %+v", evt)
	}
}
