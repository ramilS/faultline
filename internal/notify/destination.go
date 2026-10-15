package notify

import (
	"errors"
	"net"
	"net/http"
	"net/netip"
	"syscall"
	"time"
)

// ErrDestinationNotAllowed reports a webhook that resolves to an address
// Faultline refuses to call: a request from the server reaches what no outside
// caller can (the host itself, the cloud metadata service, the private
// network), so an owner-supplied URL must not be a way in.
var ErrDestinationNotAllowed = errors.New("destination address is not allowed")

const dialTimeout = 5 * time.Second

// refusedDestinationError names the address the dial guard refused. It is what
// the channel shows, so it must carry the address, never the URL.
type refusedDestinationError struct {
	address string
}

func (e *refusedDestinationError) Error() string {
	return ErrDestinationNotAllowed.Error() + ": " + e.address
}

func (e *refusedDestinationError) Unwrap() error { return ErrDestinationNotAllowed }

// carrierGradeNAT (RFC 6598) is not covered by netip's IsPrivate, but some
// clouds put internal services there — Alibaba Cloud's metadata endpoint is
// 100.100.100.200.
var carrierGradeNAT = netip.MustParsePrefix("100.64.0.0/10")

// allowedAddress decides whether a webhook may connect to address. Loopback,
// link-local (which holds the 169.254.169.254 metadata service), unspecified
// and multicast addresses are never allowed; private ranges only when the
// operator opted in with FAULTLINE_WEBHOOK_ALLOW_PRIVATE.
func allowedAddress(address netip.Addr, allowPrivate bool) bool {
	address = address.Unmap()
	if address.IsLoopback() || address.IsLinkLocalUnicast() || address.IsLinkLocalMulticast() ||
		address.IsInterfaceLocalMulticast() || address.IsMulticast() || address.IsUnspecified() {
		return false
	}
	if address.IsPrivate() || carrierGradeNAT.Contains(address) {
		return allowPrivate
	}
	return true
}

// guardedWebhookClient checks the address at connect time, after DNS
// resolution: checking the hostname when the channel is saved would let a
// name that later re-resolves to an internal address (DNS rebinding) through.
// Its transport sets no Proxy, so it dials directly and ignores HTTPS_PROXY:
// through a proxy the check would only ever see the proxy's address.
func guardedWebhookClient(allowPrivate bool) *http.Client {
	dialer := &net.Dialer{
		Timeout: dialTimeout,
		Control: func(_, hostPort string, _ syscall.RawConn) error {
			addrPort, err := netip.ParseAddrPort(hostPort)
			if err != nil {
				return &refusedDestinationError{address: hostPort}
			}
			if !allowedAddress(addrPort.Addr(), allowPrivate) {
				return &refusedDestinationError{address: addrPort.Addr().String()}
			}
			return nil
		},
	}
	return newClient(&http.Transport{
		DialContext:         dialer.DialContext,
		ForceAttemptHTTP2:   true,
		TLSHandshakeTimeout: dialTimeout,
		MaxIdleConns:        10,
		IdleConnTimeout:     90 * time.Second,
	})
}

// newClient times out after sendTimeout and does not follow redirects: an
// endpoint that moved should be reconfigured, not followed to wherever it now
// points — for a webhook that could be an internal address the dial guard
// would then have to catch.
func newClient(transport http.RoundTripper) *http.Client {
	return &http.Client{
		Timeout:   sendTimeout,
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}
