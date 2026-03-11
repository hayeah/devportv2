package devport

import (
	"fmt"
	"net"
	"sync"
	"testing"
)

const (
	internalTestPortRangeStart = 46200
	internalTestPortRangeEnd   = 48199
)

var (
	internalTestPortMu   sync.Mutex
	internalNextTestPort = internalTestPortRangeStart
)

func reserveIntegrationTCPPortRange(t *testing.T, extra int) (int, int, int) {
	t.Helper()

	width := extra + 2
	for {
		start := nextInternalTestPortBlock(t, width)
		listeners, err := reserveContiguousPortBlock(start, width)
		if err != nil {
			continue
		}
		for _, listener := range listeners {
			_ = listener.Close()
		}
		return start, start, start + 1
	}
}

func nextInternalTestPortBlock(t *testing.T, width int) int {
	t.Helper()

	internalTestPortMu.Lock()
	defer internalTestPortMu.Unlock()

	if internalNextTestPort+width-1 > internalTestPortRangeEnd {
		t.Fatalf("exhausted internal test port range for width %d", width)
	}
	start := internalNextTestPort
	internalNextTestPort += width + 1
	return start
}

func reserveContiguousPortBlock(start, width int) ([]net.Listener, error) {
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
