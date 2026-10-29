package test

import (
	"sync"
	"testing"

	"app/internal/realtime"
)

func TestClient_SendOnClosedClientNoPanic(t *testing.T) {
	client := realtime.NewClient("test-client", nil, nil)

	// Close client
	client.Close()

	// Sending after close should silently drop and NEVER panic
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			client.Send([]byte(`{"test":true}`))
			_ = client.SendJSON(map[string]string{"msg": "hello"})
		}()
	}
	wg.Wait()
}
