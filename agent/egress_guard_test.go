package agent

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestIsBlockedEgressIP(t *testing.T) {
	for _, tc := range []struct {
		ip      string
		blocked bool
	}{
		{"127.0.0.1", true},
		{"::1", true},
		{"10.1.2.3", true},
		{"172.20.0.5", true},
		{"192.168.1.1", true},
		{"169.254.169.254", true},
		{"100.64.0.1", true},
		{"0.0.0.0", true},
		{"fd00::1", true},
		{"8.8.8.8", false},
		{"158.69.208.1", false},
		{"2606:4700::1111", false},
	} {
		if got := isBlockedEgressIP(net.ParseIP(tc.ip)); got != tc.blocked {
			t.Errorf("%s blocked=%v, want %v", tc.ip, got, tc.blocked)
		}
	}
}

func TestGuardedClientBlocksLoopback(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_, _ = w.Write([]byte(`ok`))
	}))
	defer srv.Close()

	c := NewGuardedHTTPClient(5 * time.Second)
	_, err := c.Get(srv.URL)
	if !errors.Is(err, ErrBlockedEgress) {
		t.Fatalf("loopback err = %v, want ErrBlockedEgress", err)
	}
	if hits != 0 {
		t.Fatalf("internal server reached %d times", hits)
	}

	// With the test escape hatch the same client reaches the server.
	allowPrivateEgress = true
	defer func() { allowPrivateEgress = false }()
	resp, err := NewGuardedHTTPClient(5 * time.Second).Get(srv.URL)
	if err != nil || hits != 1 {
		t.Fatalf("allowed call err=%v hits=%d", err, hits)
	}
	_ = resp.Body.Close()
}
