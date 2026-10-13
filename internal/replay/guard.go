// Package replay sends captured requests to destinations chosen by the user,
// with protections against server-side request forgery (SSRF).
//
// Destinations are checked three times: the URL is validated before any
// network activity, every address a hostname resolves to is checked before
// dialing, and the socket address is checked again right before connecting.
// The resolved address is dialed directly, so a second DNS answer (DNS
// rebinding) can never redirect the connection.
package replay

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"syscall"

	"github.com/abood-lmao/relaybox/internal/config"
)

// BlockedError reports a destination that is not allowed.
type BlockedError struct {
	Reason string
}

func (e *BlockedError) Error() string { return e.Reason }

// IsBlocked reports whether err is (or wraps) a BlockedError.
func IsBlocked(err error) bool {
	var b *BlockedError
	return errors.As(err, &b)
}

// reservedPrefixes are ranges that are not reachable public unicast
// destinations, in addition to those the netip predicates cover.
var reservedPrefixes = mustPrefixes(
	"0.0.0.0/8",       // "this network"
	"100.64.0.0/10",   // carrier-grade NAT
	"192.0.0.0/24",    // IETF protocol assignments
	"192.0.2.0/24",    // documentation
	"192.88.99.0/24",  // deprecated 6to4 relay anycast
	"198.18.0.0/15",   // benchmarking
	"198.51.100.0/24", // documentation
	"203.0.113.0/24",  // documentation
	"240.0.0.0/4",     // reserved, includes 255.255.255.255
	"::/96",           // deprecated IPv4-compatible addresses
	"64:ff9b::/96",    // NAT64, embeds IPv4 addresses
	"64:ff9b:1::/48",  // local-use NAT64
	"100::/64",        // discard prefix
	"2001::/23",       // IETF protocol assignments, includes Teredo
	"2001:db8::/32",   // documentation
	"2002::/16",       // 6to4, embeds IPv4 addresses
	"3fff::/20",       // documentation
	"5f00::/16",       // segment routing
	"fc00::/7",        // unique local addresses
	"fec0::/10",       // deprecated site-local addresses
)

func mustPrefixes(ss ...string) []netip.Prefix {
	out := make([]netip.Prefix, len(ss))
	for i, s := range ss {
		out[i] = netip.MustParsePrefix(s)
	}
	return out
}

// IsPublic reports whether addr is a public unicast address.
func IsPublic(addr netip.Addr) bool {
	addr = addr.Unmap()
	if !addr.IsValid() || addr.Zone() != "" || addr.IsUnspecified() || addr.IsLoopback() || addr.IsPrivate() ||
		addr.IsLinkLocalUnicast() || addr.IsMulticast() || !addr.IsGlobalUnicast() {
		return false
	}
	for _, p := range reservedPrefixes {
		if p.Contains(addr) {
			return false
		}
	}
	return true
}

// blockedHostnames are names that only make sense inside private networks.
// This check is a convenience that gives a clear error early; the address
// checks at dial time are what actually enforce the policy.
var blockedHostnames = []string{"localhost", "localhost.localdomain", "metadata.google.internal", "metadata"}
var blockedSuffixes = []string{".localhost", ".local", ".internal", ".home.arpa", ".lan", ".intranet", ".corp", ".in-addr.arpa", ".ip6.arpa"}

// Guard decides which destinations replay may reach.
type Guard struct {
	allowed map[string]bool // exact host:port entries that may be private
	lookup  func(ctx context.Context, host string) ([]netip.Addr, error)
}

// NewGuard returns a guard. allowedHosts are host:port entries (already
// normalized by config.ParseReplayAllowedHosts) that may resolve to
// private addresses; cloud metadata addresses stay blocked regardless.
func NewGuard(allowedHosts []string) *Guard {
	g := &Guard{allowed: make(map[string]bool), lookup: systemLookup}
	for _, h := range allowedHosts {
		g.allowed[h] = true
	}
	return g
}

func systemLookup(ctx context.Context, host string) ([]netip.Addr, error) {
	return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
}

const maxURLLength = 2048

// ValidateURL checks a destination URL without touching the network and
// returns it normalized.
func (g *Guard) ValidateURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, &BlockedError{"Enter a destination URL."}
	}
	if len(raw) > maxURLLength {
		return nil, &BlockedError{fmt.Sprintf("The URL is longer than %d characters.", maxURLLength)}
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, &BlockedError{"The URL could not be parsed."}
	}
	u.Scheme = strings.ToLower(u.Scheme)
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, &BlockedError{"Only http:// and https:// destinations are supported."}
	}
	if u.Opaque != "" || u.Host == "" {
		return nil, &BlockedError{"The URL needs a host, for example https://example.com/webhook."}
	}
	if u.User != nil {
		return nil, &BlockedError{"Remove the credentials from the URL. Send them in a header instead, so they are not stored with the replay history."}
	}
	u.Fragment = ""
	u.RawFragment = ""

	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	port := u.Port()
	if port == "" {
		port = map[string]string{"http": "80", "https": "443"}[u.Scheme]
	}
	if p, err := strconv.Atoi(port); err != nil || p < 1 || p > 65535 {
		return nil, &BlockedError{"The URL has an invalid port."}
	}

	if addr, err := netip.ParseAddr(host); err == nil {
		if addr.Zone() != "" {
			return nil, &BlockedError{"IPv6 zone identifiers are not supported."}
		}
		addr = addr.Unmap()
		key := net.JoinHostPort(addr.String(), port)
		if reason := g.addrProblem(addr, g.allowed[key]); reason != "" {
			return nil, &BlockedError{fmt.Sprintf("%s is %s.", addr, reason)}
		}
		host = addr.String()
	} else {
		if !validHostname(host) {
			return nil, &BlockedError{"The host name is not valid. Use letters, digits, and hyphens (punycode for international names)."}
		}
		if !g.allowed[net.JoinHostPort(host, port)] && isInternalName(host) {
			return nil, &BlockedError{fmt.Sprintf("%s is a local or internal host name. Replay to local services is only possible for hosts listed in %s.", host, config.EnvReplayAllowedHosts)}
		}
	}

	if u.Port() == "" || (u.Scheme == "http" && port == "80") || (u.Scheme == "https" && port == "443") {
		u.Host = hostForURL(host)
	} else {
		u.Host = net.JoinHostPort(host, port)
	}
	return u, nil
}

func hostForURL(host string) string {
	if strings.Contains(host, ":") {
		return "[" + host + "]"
	}
	return host
}

// addrProblem returns why addr may not be dialed, or "" if it may.
func (g *Guard) addrProblem(addr netip.Addr, allowPrivate bool) string {
	addr = addr.Unmap()
	switch {
	case config.IsMetadataAddr(addr):
		return "a cloud metadata address, which is always blocked"
	case !addr.IsValid() || addr.IsUnspecified() || addr.IsMulticast():
		return "not a unicast address"
	case IsPublic(addr):
		return ""
	case allowPrivate:
		return ""
	case addr.IsLoopback():
		return fmt.Sprintf("a loopback address (to replay to local services, add the host and port to %s)", config.EnvReplayAllowedHosts)
	default:
		return "a private or reserved address"
	}
}

func isInternalName(host string) bool {
	for _, h := range blockedHostnames {
		if host == h {
			return true
		}
	}
	for _, s := range blockedSuffixes {
		if strings.HasSuffix(host, s) {
			return true
		}
	}
	return false
}

// validHostname accepts LDH host names whose last label is not numeric, so
// inputs like "127.1" or "2130706433" that some resolvers treat as IPv4
// addresses are rejected.
func validHostname(h string) bool {
	if h == "" || len(h) > 253 {
		return false
	}
	labels := strings.Split(h, ".")
	for _, label := range labels {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
				return false
			}
		}
	}
	last := labels[len(labels)-1]
	return strings.Trim(last, "0123456789") != "" && !strings.HasPrefix(last, "0x")
}

// DialContext resolves addr, checks every resolved address, and connects to
// a checked address. It never resolves the name a second time.
func (g *Guard) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	host = strings.ToLower(host)
	var ips []netip.Addr
	if ip, err := netip.ParseAddr(host); err == nil {
		ips = []netip.Addr{ip.Unmap()}
	} else {
		resolved, err := g.lookup(ctx, host)
		if err != nil {
			return nil, err
		}
		for _, ip := range resolved {
			ips = append(ips, ip.Unmap())
		}
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("%s did not resolve to any address", host)
	}

	allowPrivate := g.allowed[net.JoinHostPort(host, port)]
	for _, ip := range ips {
		if reason := g.addrProblem(ip, allowPrivate); reason != "" {
			return nil, &BlockedError{fmt.Sprintf("%s resolves to %s, %s.", host, ip, reason)}
		}
	}

	d := net.Dialer{Timeout: dialTimeout, Control: g.control(allowPrivate)}
	var firstErr error
	for _, ip := range ips {
		conn, err := d.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
		if firstErr == nil {
			firstErr = err
		}
		if IsBlocked(err) || ctx.Err() != nil {
			break
		}
	}
	return nil, firstErr
}

// control re-checks the exact address the socket is about to connect to.
func (g *Guard) control(allowPrivate bool) func(network, address string, _ syscall.RawConn) error {
	return func(network, address string, _ syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return &BlockedError{"invalid socket address"}
		}
		ip, err := netip.ParseAddr(host)
		if err != nil {
			return &BlockedError{"invalid socket address"}
		}
		if reason := g.addrProblem(ip, allowPrivate); reason != "" {
			return &BlockedError{fmt.Sprintf("Connection to %s blocked: %s.", ip, reason)}
		}
		return nil
	}
}
