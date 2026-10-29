package test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"app/internal/realtime"
)

func TestDispatcher_SystemAction(t *testing.T) {
	registry := realtime.NewActionRegistry()
	realtime.RegisterSystemActions(registry)
	dispatcher := realtime.NewDispatcher(registry)

	handler, exists := registry.Lookup("system:ping")
	if !exists {
		t.Fatalf("expected system:ping to exist")
	}

	res, err := handler(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("unexpected error executing system:ping: %v", err)
	}

	m, ok := res.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map result, got: %T", res)
	}
	if _, ok := m["server_time"]; !ok {
		t.Errorf("expected server_time key in result")
	}

	// Test system:echo
	echoHandler, exists := registry.Lookup("system:echo")
	if !exists {
		t.Fatalf("expected system:echo to exist")
	}
	echoRes, err := echoHandler(context.Background(), nil, json.RawMessage(`{"hello":"world"}`))
	if err != nil {
		t.Fatalf("unexpected error executing system:echo: %v", err)
	}
	echoMap, ok := echoRes.(map[string]interface{})
	if !ok || echoMap["echo"] != "ok" {
		t.Errorf("unexpected echo result: %v", echoRes)
	}

	// Test system:info
	infoHandler, exists := registry.Lookup("system:info")
	if !exists {
		t.Fatalf("expected system:info to exist")
	}
	infoRes, err := infoHandler(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("unexpected error executing system:info: %v", err)
	}
	infoMap, ok := infoRes.(map[string]interface{})
	if !ok || infoMap["os"] == "" || infoMap["go_version"] == "" {
		t.Errorf("unexpected info result: %v", infoRes)
	}

	_ = dispatcher
}

func TestDispatcher_FallbackHandler(t *testing.T) {
	registry := realtime.NewActionRegistry()
	dispatcher := realtime.NewDispatcher(registry)

	var interceptedAction string
	dispatcher.SetFallback(func(ctx context.Context, action string, payload json.RawMessage) (interface{}, error) {
		interceptedAction = action
		if action == "engine:fail" {
			return nil, errors.New("simulated error")
		}
		return map[string]string{"status": "handled by fallback"}, nil
	})

	client := realtime.NewClient("test_client", nil, nil)

	// Test success via fallback
	dispatcher.Execute(context.Background(), client, realtime.ActionMessage{
		ID:     "msg_fallback_1",
		Action: "engine:echo",
	})

	if interceptedAction != "engine:echo" {
		t.Errorf("expected intercepted action engine:echo, got: %s", interceptedAction)
	}
}

func TestDispatcher_PanicRecovery(t *testing.T) {
	registry := realtime.NewActionRegistry()
	registry.Register("panic:test", func(ctx context.Context, client *realtime.Client, payload json.RawMessage) (interface{}, error) {
		panic("forced action crash")
	})

	dispatcher := realtime.NewDispatcher(registry)
	client := realtime.NewClient("test_client", nil, nil)

	dispatcher.Execute(context.Background(), client, realtime.ActionMessage{
		ID:     "msg_panic_1",
		Action: "panic:test",
	})
}
