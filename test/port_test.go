package test

import (
	"testing"

	"app/internal/netutil"
)

func TestFindAvailablePort(t *testing.T) {
	port, err := netutil.FindAvailablePort("127.0.0.1")
	if err != nil {
		t.Fatalf("expected no error finding available port, got: %v", err)
	}
	if port <= 0 || port > 65535 {
		t.Fatalf("expected valid port between 1 and 65535, got: %d", port)
	}
}

func TestFindAvailablePorts_MultipleDistinct(t *testing.T) {
	count := 3
	ports, err := netutil.FindAvailablePorts("127.0.0.1", count)
	if err != nil {
		t.Fatalf("expected no error finding ports, got: %v", err)
	}
	if len(ports) != count {
		t.Fatalf("expected %d ports, got %d", count, len(ports))
	}

	seen := make(map[int]bool)
	for _, p := range ports {
		if p <= 0 || p > 65535 {
			t.Errorf("invalid port value: %d", p)
		}
		if seen[p] {
			t.Errorf("duplicate port found: %d", p)
		}
		seen[p] = true
	}
}
