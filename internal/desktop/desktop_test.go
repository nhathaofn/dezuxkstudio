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
	if err != nil || info.Token != "tok" || info.ActionTimeoutMs != cfg.Realtime.ActionTimeout.Milliseconds() {
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
	if !strings.Contains(desktop, "ws://127.0.0.1:*") || strings.Contains(desktop, "example.com") {
		t.Errorf("desktop CSP: %s", desktop)
	}
	if !strings.Contains(desktop, "object-src 'none'") {
		t.Errorf("hardening directives missing: %s", desktop)
	}

	cfg := config.Default(config.ModeClient)
	cfg.Client.RemoteURL = "wss://core.example.com:8443/ws"
	client := contentSecurityPolicy(cfg)
	if !strings.Contains(client, "wss://core.example.com:8443") || strings.Contains(client, "ws://127.0.0.1") {
		t.Errorf("client CSP: %s", client)
	}
}
