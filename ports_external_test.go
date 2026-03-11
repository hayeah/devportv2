package devport_test

import (
	"fmt"
	"net"
	"sync"
	"testing"
)

const (
	externalTestPortRangeStart = 43200
	externalTestPortRangeEnd   = 45999
)

var (
	externalTestPortMu   sync.Mutex
	externalNextTestPort = externalTestPortRangeStart
)

func reserveTCPPortRange(t *testing.T, extra int) (int, int, int) {
	t.Helper()

	width := extra + 2
	for {
		start := nextExternalTestPortBlock(t, width)
		listeners, err := reserveExternalPortBlock(start, width)
		if err != nil {
			continue
		}
		for _, listener := range listeners {
			_ = listener.Close()
		}
		return start, start, start + 1
	}
}

func nextExternalTestPortBlock(t *testing.T, width int) int {
	t.Helper()

	externalTestPortMu.Lock()
	defer externalTestPortMu.Unlock()

	if externalNextTestPort+width-1 > externalTestPortRangeEnd {
		t.Fatalf("exhausted external test port range for width %d", width)
	}
	start := externalNextTestPort
	externalNextTestPort += width + 1
	return start
}

func reserveExternalPortBlock(start, width int) ([]net.Listener, error) {
	listeners := make([]net.Listener, 0, width)
	for port := start; port < start+width; port++ {
		listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			for _, existing := range listeners {
				_ = existing.Close()
			}
			return nil, err
		}
		listeners = append(listeners, listener)
	}
	return listeners, nil
}
