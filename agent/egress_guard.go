package agent

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"syscall"
	"time"
)

// ErrQueryToolsDisabled is returned for tools of type "query". They used to run
// the tool's Endpoint as raw SQL against the SHARED database connection — no
// organization scoping, and parameters interpolated as text — so any org user
// could read every tenant's tables (users, credentials, …) through the bot
// tool editor's "test" button or through the LLM itself (QA-EXP-ROLES).
// Tenant-authored SQL cannot be made safe on a multi-tenant database; use an
// "api"/"webhook" tool against the data API instead.
var ErrQueryToolsDisabled = errors.New("las herramientas de tipo consulta SQL están deshabilitadas por seguridad; usa una herramienta de tipo API")

// ErrBlockedEgress is returned when a tool targets a loopback, private,
// link-local (cloud metadata) or otherwise internal address.
var ErrBlockedEgress = errors.New("destino no permitido: la herramienta no puede llamar direcciones internas o privadas")

// allowPrivateEgress lets tests point tools at an httptest server on
// 127.0.0.1. Never set in production code paths.
var allowPrivateEgress = false

// isBlockedEgressIP reports whether ip is an address a tenant-configured tool
// must never reach: the host itself, the private network the platform runs in
// (database, WhatsApp service, internal APIs) or the cloud metadata endpoint.
func isBlockedEgressIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() {
		return true
	}
	// Carrier-grade NAT 100.64.0.0/10 (used by some internal overlays).
	if v4 := ip.To4(); v4 != nil && v4[0] == 100 && v4[1]&0xC0 == 64 {
		return true
	}
	return false
}

// guardedDialControl runs after DNS resolution, on the exact address being
// dialed, so DNS rebinding and redirects to internal hosts are covered too.
func guardedDialControl(_, address string, _ syscall.RawConn) error {
	if allowPrivateEgress {
		return nil
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrBlockedEgress, address)
	}
	if isBlockedEgressIP(net.ParseIP(host)) {
		return fmt.Errorf("%w: %s", ErrBlockedEgress, host)
	}
	return nil
}

// newGuardedHTTPClient returns an http.Client for tenant-configured outbound
// calls: every connection (including redirects) is checked by
// guardedDialControl, and proxies from the environment are ignored so the
// check sees the real destination.
func newGuardedHTTPClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second, Control: guardedDialControl}
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dialer.DialContext(ctx, network, addr)
		},
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: timeout,
		MaxIdleConns:          10,
		IdleConnTimeout:       30 * time.Second,
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("demasiadas redirecciones")
			}
			return nil
		},
	}
}
