package httpclient

import (
	"net/http"
	"testing"
	"time"

	"app/internal/config"
)

func TestNewUsesConfigAndWithTimeoutSharesThePool(t *testing.T) {
	cfg := config.Default(config.ModeDesktop).Network
	cfg.IdleConnTimeout = 42 * time.Second
	base := New(cfg)
	tr := base.Transport.(*http.Transport)
	if tr.IdleConnTimeout != 42*time.Second || tr.MaxIdleConns != cfg.MaxIdleConns || tr.Proxy == nil {
		t.Fatalf("transport not built from config: %+v", tr)
	}
	if base.Timeout != 0 {
		t.Fatal("the shared client must not cap streaming downloads")
	}
	u := WithTimeout(base, 10*time.Second)
	if u.Timeout != 10*time.Second || u.Transport != base.Transport {
		t.Fatal("WithTimeout must set a timeout and reuse the same transport")
	}
	if base.Timeout != 0 {
		t.Fatal("WithTimeout must not modify the base client")
	}
}
