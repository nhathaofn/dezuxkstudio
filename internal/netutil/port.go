package netutil

import (
	"fmt"
	"net"
)

// FindAvailablePort finds an open TCP port on the specified host.
func FindAvailablePort(host string) (int, error) {
	ports, err := FindAvailablePorts(host, 1)
	if err != nil {
		return 0, err
	}
	return ports[0], nil
}

// FindAvailablePorts finds multiple distinct open TCP ports simultaneously to prevent collisions.
func FindAvailablePorts(host string, count int) ([]int, error) {
	if host == "" {
		host = "127.0.0.1"
	}
	if count <= 0 {
		return nil, nil
	}

	listeners := make([]net.Listener, 0, count)
	ports := make([]int, 0, count)

	defer func() {
		for _, l := range listeners {
			_ = l.Close()
		}
	}()

	for i := 0; i < count; i++ {
		listener, err := net.Listen("tcp", fmt.Sprintf("%s:0", host))
		if err != nil {
			return nil, fmt.Errorf("failed to probe available port on %s: %w", host, err)
		}
		listeners = append(listeners, listener)

		addr, ok := listener.Addr().(*net.TCPAddr)
		if !ok {
			return nil, fmt.Errorf("failed to cast TCPAddr on %s", host)
		}
		ports = append(ports, addr.Port)
	}

	return ports, nil
}
