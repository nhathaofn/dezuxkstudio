package desktop

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"app/internal/apperr"
	"app/internal/config"
	"app/internal/contracts"
	"app/internal/secrets"
)

// Wails binds every exported method of Bridge; they must match the contract exactly.
func TestBridgeMatchesContract(t *testing.T) {
	bt := reflect.TypeFor[*Bridge]()
	declared := map[string]contracts.BridgeMethod{}
	for _, m := range contracts.Bridge() {
		declared[m.Name] = m
	}
	if bt.NumMethod() != len(declared) {
		t.Fatalf("Bridge exposes %d methods, contract declares %d", bt.NumMethod(), len(declared))
	}
	errType := reflect.TypeFor[error]()
	for i := 0; i < bt.NumMethod(); i++ {
		m := bt.Method(i)
		spec, ok := declared[m.Name]
		if !ok {
			t.Errorf("method %s is not declared in contracts.Bridge()", m.Name)
			continue
		}
		in := m.Type.NumIn() - 1 // receiver
		if in != len(spec.Params) {
			t.Errorf("%s: %d params, contract %d", m.Name, in, len(spec.Params))
			continue
		}
		for p := range spec.Params {
			if m.Type.In(p+1) != spec.Params[p] {
				t.Errorf("%s: param %d is %s, contract %s", m.Name, p, m.Type.In(p+1), spec.Params[p])
			}
		}
		var results []reflect.Type
		for o := 0; o < m.Type.NumOut(); o++ {
			if m.Type.Out(o) != errType {
				results = append(results, m.Type.Out(o))
			}
		}
		switch {
		case spec.Result == nil && len(results) != 0,
			spec.Result != nil && (len(results) != 1 || results[0] != spec.Result):
			t.Errorf("%s: results %v, contract %v", m.Name, results, spec.Result)
		}
	}
}

func TestBridgeLifecycle(t *testing.T) {
	cfg := config.Default(config.ModeDesktop)
	cfg.Desktop.ReadyTimeout = 50 * time.Millisecond

	b := newBridge(cfg, secrets.NewMemory())
	if _, err := b.GetConnectionInfo(); !apperr.IsCode(err, apperr.CodeUnavailable) {
		t.Fatalf("before ready: %v", err)
	}

	b.succeed(connectionInfo(cfg, "ws://127.0.0.1:1/ws", "tok"))
	info, err := b.GetConnectionInfo()
	if err != nil || info.Token != "tok" || info.ActionTimeoutMs != cfg.Realtime.ActionTimeout.Milliseconds() || info.LongActionTimeoutMs != cfg.Realtime.LongActionTimeout.Milliseconds() {
		t.Fatalf("info=%+v err=%v", info, err)
	}
	if err := b.SetRemoteToken("x"); !apperr.IsCode(err, apperr.CodeBadRequest) {
		t.Fatalf("desktop mode must refuse remote token: %v", err)
	}

	failed := newBridge(cfg, secrets.NewMemory())
	failed.fail(errors.New("db locked"))
	if _, err := failed.GetConnectionInfo(); err == nil {
		t.Fatal("fatal error not reported")
	}
}

func TestClientModeReadsTokenFromCredentialStore(t *testing.T) {
	cfg := config.Default(config.ModeClient)
	cfg.Client.RemoteURL = "wss://core.example.com/ws"
	store := secrets.NewMemory()
	b := newBridge(cfg, store)
	b.succeed(connectionInfo(cfg, cfg.Client.RemoteURL, ""))

	if err := b.SetRemoteToken("remote-secret"); err != nil {
		t.Fatal(err)
	}
	info, err := b.GetConnectionInfo()
	if err != nil || info.Token != "remote-secret" {
		t.Fatalf("info=%+v err=%v", info, err)
	}
}

func TestContentSecurityPolicy(t *testing.T) {
	desktop := contentSecurityPolicy(config.Default(config.ModeDesktop))
	if !strings.Contains(desktop, "ws://127.0.0.1:*") || !strings.Contains(desktop, "http://127.0.0.1:*") || strings.Contains(desktop, "example.com") {
		t.Errorf("desktop CSP: %s", desktop)
	}
	if !strings.Contains(desktop, "object-src 'none'") || !strings.Contains(desktop, "img-src 'self' data: blob:") || !strings.Contains(desktop, "media-src 'self' data: blob:") {
		t.Errorf("hardening directives missing: %s", desktop)
	}

	for _, tc := range []struct {
		remote string
		want   []string
	}{
		{"wss://core.example.com:8443/ws", []string{"wss://core.example.com:8443", "https://core.example.com:8443"}},
		{"https://core.example.com:8443/ws", []string{"wss://core.example.com:8443", "https://core.example.com:8443"}},
		{"ws://core.local:9000/ws", []string{"ws://core.local:9000", "http://core.local:9000"}},
		{"http://core.local:9000", []string{"ws://core.local:9000", "http://core.local:9000"}},
	} {
		cfg := config.Default(config.ModeClient)
		cfg.Client.RemoteURL = tc.remote
		client := contentSecurityPolicy(cfg)
		for _, w := range tc.want {
			if !strings.Contains(client, w) {
				t.Errorf("remote=%s: expected %q in CSP %s", tc.remote, w, client)
			}
		}
		if strings.Contains(client, "ws://127.0.0.1") {
			t.Errorf("remote=%s: unexpected loopback in client CSP %s", tc.remote, client)
		}
	}
}

// A slow start answers "still starting" (retryable) through the real Wails error formatter
// shape; a real startup failure is never marked retryable.
func TestBridgeSlowStartIsRetryable(t *testing.T) {
	cfg := config.Default(config.ModeDesktop)
	cfg.Desktop.ReadyTimeout = 30 * time.Millisecond
	b := newBridge(cfg, secrets.NewMemory())

	_, err := b.GetConnectionInfo()
	body := contracts.NewErrorBody(err)
	if body.Code != apperr.CodeUnavailable || body.Details[DetailStarting] != true {
		t.Fatalf("slow start must be retryable, got %+v", body)
	}
	go func() {
		time.Sleep(40 * time.Millisecond) // core comes up after the first polling window
		b.succeed(connectionInfo(cfg, "ws://127.0.0.1:1/ws", "tok"))
	}()
	var info contracts.ConnectionInfo
	for i := 0; i < 10; i++ { // what the frontend does
		if info, err = b.GetConnectionInfo(); err == nil {
			break
		}
	}
	if err != nil || info.Token != "tok" {
		t.Fatalf("core never became reachable: %v", err)
	}

	failed := newBridge(cfg, secrets.NewMemory())
	failed.fail(apperr.Unavailable("db locked"))
	_, err = failed.GetConnectionInfo()
	if fb := contracts.NewErrorBody(err); fb.Details[DetailStarting] == true {
		t.Fatalf("a real startup failure must not be retried: %+v", fb)
	}
}
