package plugin

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"syscall"
	"time"
)

// maxRedirects bounds redirect chains followed by plugin HTTP clients.
const maxRedirects = 5

// HTTPOptions configures NewHTTPClient.
type HTTPOptions struct {
	// AllowHosts lists hostnames the client may contact. "*" allows any host
	// that resolves to a public address.
	AllowHosts []string
	// Timeout bounds a whole request. Defaults to 20 seconds.
	Timeout time.Duration
	// AllowPrivate disables the private-address guard. Tests use it to reach
	// httptest servers on loopback; production code must not set it.
	AllowPrivate bool
}

// ErrBlocked is returned when a request is refused by policy.
var ErrBlocked = errors.New("request blocked by plugin network policy")

// NewHTTPClient returns a client that only talks to allowed hosts over http or
// https, and never to private, loopback or link-local addresses. The address
// check runs on the connection actually made, after DNS resolution, so a name
// that resolves to an internal address is refused too (SSRF and DNS rebinding).
// Every redirect is re-checked against the allowlist.
func NewHTTPClient(o HTTPOptions) *http.Client {
	if o.Timeout == 0 {
		o.Timeout = 20 * time.Second
	}
	allowed := make(map[string]bool, len(o.AllowHosts))
	for _, h := range o.AllowHosts {
		allowed[strings.ToLower(strings.TrimSpace(h))] = true
	}

	dialer := &net.Dialer{Timeout: 10 * time.Second}
	if !o.AllowPrivate {
		dialer.Control = func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			addr, err := netip.ParseAddr(host)
			if err != nil {
				return err
			}
			if isPrivate(addr) {
				return fmt.Errorf("%w: address %s is not public", ErrBlocked, addr)
			}
			return nil
		}
	}

	check := func(req *http.Request) error {
		if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
			return fmt.Errorf("%w: scheme %q", ErrBlocked, req.URL.Scheme)
		}
		host := strings.ToLower(req.URL.Hostname())
		if !allowed["*"] && !allowed[host] {
			return fmt.Errorf("%w: host %q is not in the plugin's network capability", ErrBlocked, host)
		}
		return nil
	}

	return &http.Client{
		Timeout: o.Timeout,
		Transport: &guardTransport{
			check: check,
			next: &http.Transport{
				DialContext:           dialer.DialContext,
				TLSHandshakeTimeout:   10 * time.Second,
				ResponseHeaderTimeout: o.Timeout,
				MaxIdleConns:          10,
				IdleConnTimeout:       30 * time.Second,
				// Proxies would bypass the dial-time address check.
				Proxy: nil,
			},
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return fmt.Errorf("%w: more than %d redirects", ErrBlocked, maxRedirects)
			}
			return check(req)
		},
	}
}

type guardTransport struct {
	check func(*http.Request) error
	next  http.RoundTripper
}

func (g *guardTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := g.check(req); err != nil {
		return nil, err
	}
	return g.next.RoundTrip(req)
}

var privatePrefixes = mustPrefixes(
	"0.0.0.0/8",      // "this" network
	"10.0.0.0/8",     // private
	"100.64.0.0/10",  // carrier-grade NAT
	"127.0.0.0/8",    // loopback
	"169.254.0.0/16", // link-local, including cloud metadata services
	"172.16.0.0/12",  // private
	"192.0.0.0/24",   // IETF protocol assignments
	"192.168.0.0/16", // private
	"198.18.0.0/15",  // benchmarking
	"224.0.0.0/4",    // multicast
	"240.0.0.0/4",    // reserved
	"::/128",         // unspecified
	"::1/128",        // loopback
	"fc00::/7",       // unique local
	"fe80::/10",      // link-local
	"ff00::/8",       // multicast
)

func mustPrefixes(cidrs ...string) []netip.Prefix {
	out := make([]netip.Prefix, len(cidrs))
	for i, c := range cidrs {
		out[i] = netip.MustParsePrefix(c)
	}
	return out
}

// isPrivate reports whether addr is not a public unicast address.
func isPrivate(addr netip.Addr) bool {
	addr = addr.Unmap() // treat ::ffff:10.0.0.1 as 10.0.0.1
	for _, p := range privatePrefixes {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}
