// Package config loads and validates Relaybox configuration from environment
// variables. Invalid values are reported as errors; nothing is silently ignored.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/abood-lmao/relaybox/internal/ids"
	"github.com/abood-lmao/relaybox/internal/logging"
)

// Defaults and limits. Documented in docs/configuration.md.
const (
	DefaultHost              = "127.0.0.1"
	DefaultPort              = 8787
	DefaultDatabasePath      = "./data/relaybox.db"
	DefaultMaxBodyBytes      = 1 << 20
	MinBodyBytes             = 1 << 10
	MaxBodyBytesCeiling      = 64 << 20
	DefaultRetentionDays     = 7
	MaxRetentionDays         = 3650
	DefaultMaxStoredRequests = 10000
	MaxStoredRequestsLimit   = 10_000_000
	DefaultRateLimitRPS      = 50
	DefaultRateLimitBurst    = 100
	MinTokenLength           = 24
)

// Environment variable names.
const (
	EnvHost                 = "RELAYBOX_HOST"
	EnvPort                 = "RELAYBOX_PORT"
	EnvDatabasePath         = "RELAYBOX_DATABASE_PATH"
	EnvPublicBaseURL        = "RELAYBOX_PUBLIC_BASE_URL"
	EnvMaxBodyBytes         = "RELAYBOX_MAX_BODY_BYTES"
	EnvRetentionDays        = "RELAYBOX_RETENTION_DAYS"
	EnvMaxStoredRequests    = "RELAYBOX_MAX_STORED_REQUESTS"
	EnvLogLevel             = "RELAYBOX_LOG_LEVEL"
	EnvLogFormat            = "RELAYBOX_LOG_FORMAT"
	EnvAllowRemoteDashboard = "RELAYBOX_ALLOW_REMOTE_DASHBOARD"
	EnvDashboardToken       = "RELAYBOX_DASHBOARD_TOKEN"
	EnvAllowRemoteReplay    = "RELAYBOX_ALLOW_REMOTE_REPLAY"
	EnvReplayAllowedHosts   = "RELAYBOX_REPLAY_ALLOWED_HOSTS"
	EnvRateLimitRPS         = "RELAYBOX_RATE_LIMIT_RPS"
	EnvRateLimitBurst       = "RELAYBOX_RATE_LIMIT_BURST"
)

var knownVars = map[string]bool{
	EnvHost: true, EnvPort: true, EnvDatabasePath: true, EnvPublicBaseURL: true,
	EnvMaxBodyBytes: true, EnvRetentionDays: true, EnvMaxStoredRequests: true,
	EnvLogLevel: true, EnvLogFormat: true, EnvAllowRemoteDashboard: true,
	EnvDashboardToken: true, EnvAllowRemoteReplay: true, EnvReplayAllowedHosts: true,
	EnvRateLimitRPS: true, EnvRateLimitBurst: true,
}

// Config is the validated process configuration. Fields that can change at
// runtime (retention, redaction, default body limit) live in the settings
// service; the values here only seed their defaults.
type Config struct {
	Host                 string
	Port                 int
	DatabasePath         string
	PublicBaseURL        string
	MaxBodyBytes         int64
	RetentionDays        int
	MaxStoredRequests    int
	LogLevel             slog.Level
	LogFormat            string
	AllowRemoteDashboard bool
	DashboardToken       string
	AllowRemoteReplay    bool
	ReplayAllowedHosts   []string
	RateLimitRPS         float64
	RateLimitBurst       int
}

// Addr returns the host:port the server listens on.
func (c Config) Addr() string {
	return net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
}

// LoopbackBind reports whether the server only listens on a loopback address.
func (c Config) LoopbackBind() bool {
	return isLoopbackHost(c.Host)
}

// AuthRequired reports whether dashboard and API requests need the token.
func (c Config) AuthRequired() bool {
	return c.DashboardToken != ""
}

// URLReplayEnabled reports whether requests may be replayed to URLs. Local
// dashboards always allow it; a remotely accessible dashboard needs
// RELAYBOX_ALLOW_REMOTE_REPLAY=true, so a leaked token cannot turn the
// server into a request relay by default.
func (c Config) URLReplayEnabled() bool {
	return !c.AllowRemoteDashboard || c.AllowRemoteReplay
}

// FromEnviron converts os.Environ() style entries into a map.
func FromEnviron(environ []string) map[string]string {
	env := make(map[string]string)
	for _, kv := range environ {
		k, v, ok := strings.Cut(kv, "=")
		if ok && strings.HasPrefix(k, "RELAYBOX_") {
			env[k] = v
		}
	}
	return env
}

// Load builds a Config from environment values. It returns warnings for
// suspicious but valid input, and a joined error listing every invalid value.
func Load(env map[string]string) (Config, []string, error) {
	var (
		errs     []error
		warnings []string
	)
	get := func(key string) string { return strings.TrimSpace(env[key]) }
	fail := func(key, format string, args ...any) {
		errs = append(errs, fmt.Errorf("%s: %s", key, fmt.Sprintf(format, args...)))
	}

	unknown := make([]string, 0)
	for k := range env {
		if strings.HasPrefix(k, "RELAYBOX_") && !knownVars[k] {
			unknown = append(unknown, k)
		}
	}
	sort.Strings(unknown)
	for _, k := range unknown {
		warnings = append(warnings, fmt.Sprintf("ignoring unknown environment variable %s", k))
	}

	cfg := Config{
		Host:              DefaultHost,
		Port:              DefaultPort,
		DatabasePath:      DefaultDatabasePath,
		MaxBodyBytes:      DefaultMaxBodyBytes,
		RetentionDays:     DefaultRetentionDays,
		MaxStoredRequests: DefaultMaxStoredRequests,
		LogLevel:          slog.LevelInfo,
		LogFormat:         "text",
		RateLimitRPS:      DefaultRateLimitRPS,
		RateLimitBurst:    DefaultRateLimitBurst,
	}

	if v := get(EnvHost); v != "" {
		if !validHost(v) {
			fail(EnvHost, "%q is not an IP address or hostname", v)
		} else {
			cfg.Host = strings.Trim(v, "[]")
		}
	}
	if v := get(EnvPort); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 65535 {
			fail(EnvPort, "%q must be a number between 1 and 65535", v)
		} else {
			cfg.Port = n
		}
	}
	if v, ok := env[EnvDatabasePath]; ok {
		if strings.TrimSpace(v) == "" {
			fail(EnvDatabasePath, "must not be empty")
		} else {
			cfg.DatabasePath = strings.TrimSpace(v)
		}
	}
	if v := get(EnvMaxBodyBytes); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < MinBodyBytes || n > MaxBodyBytesCeiling {
			fail(EnvMaxBodyBytes, "%q must be a byte count between %d and %d", v, MinBodyBytes, MaxBodyBytesCeiling)
		} else {
			cfg.MaxBodyBytes = n
		}
	}
	if v := get(EnvRetentionDays); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 || n > MaxRetentionDays {
			fail(EnvRetentionDays, "%q must be between 0 (keep forever) and %d", v, MaxRetentionDays)
		} else {
			cfg.RetentionDays = n
		}
	}
	if v := get(EnvMaxStoredRequests); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 || n > MaxStoredRequestsLimit {
			fail(EnvMaxStoredRequests, "%q must be between 0 (no limit) and %d", v, MaxStoredRequestsLimit)
		} else {
			cfg.MaxStoredRequests = n
		}
	}
	if v := get(EnvLogLevel); v != "" {
		lvl, err := logging.ParseLevel(v)
		if err != nil {
			fail(EnvLogLevel, "%v", err)
		} else {
			cfg.LogLevel = lvl
		}
	}
	if v := get(EnvLogFormat); v != "" {
		switch strings.ToLower(v) {
		case "text", "json":
			cfg.LogFormat = strings.ToLower(v)
		default:
			fail(EnvLogFormat, "%q must be text or json", v)
		}
	}

	parseBool := func(key string, dst *bool) {
		v := get(key)
		if v == "" {
			return
		}
		b, err := ParseBool(v)
		if err != nil {
			fail(key, "%v", err)
			return
		}
		*dst = b
	}
	parseBool(EnvAllowRemoteDashboard, &cfg.AllowRemoteDashboard)
	parseBool(EnvAllowRemoteReplay, &cfg.AllowRemoteReplay)

	if v := get(EnvRateLimitRPS); v != "" {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil || f < 0 || f > 100000 {
			fail(EnvRateLimitRPS, "%q must be a number between 0 (disabled) and 100000", v)
		} else {
			cfg.RateLimitRPS = f
		}
	}
	if v := get(EnvRateLimitBurst); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 1000000 {
			fail(EnvRateLimitBurst, "%q must be a number between 1 and 1000000", v)
		} else {
			cfg.RateLimitBurst = n
		}
	}

	if v := get(EnvReplayAllowedHosts); v != "" {
		hosts, err := ParseReplayAllowedHosts(v)
		if err != nil {
			fail(EnvReplayAllowedHosts, "%v", err)
		} else {
			cfg.ReplayAllowedHosts = hosts
		}
	}

	token := get(EnvDashboardToken)
	switch {
	case token != "" && len(token) < MinTokenLength:
		fail(EnvDashboardToken, "must be at least %d characters long", MinTokenLength)
	case strings.ContainsAny(token, " \t\r\n"):
		fail(EnvDashboardToken, "must not contain whitespace")
	default:
		cfg.DashboardToken = token
	}
	if cfg.AllowRemoteDashboard && token == "" {
		fail(EnvDashboardToken, "is required when %s=true. Example of a random value you can use: %s",
			EnvAllowRemoteDashboard, ids.NewToken())
	}

	base := get(EnvPublicBaseURL)
	if base == "" {
		cfg.PublicBaseURL = defaultBaseURL(cfg.Host, cfg.Port)
	} else {
		normalized, err := NormalizeBaseURL(base)
		if err != nil {
			fail(EnvPublicBaseURL, "%v", err)
		} else {
			cfg.PublicBaseURL = normalized
		}
	}

	if !cfg.LoopbackBind() && !cfg.AllowRemoteDashboard {
		warnings = append(warnings, fmt.Sprintf(
			"listening on %s: webhook endpoints are reachable from the network, but the dashboard and API only accept loopback clients (set %s=true to change this)",
			cfg.Host, EnvAllowRemoteDashboard))
	}
	if cfg.AllowRemoteDashboard && len(cfg.ReplayAllowedHosts) > 0 {
		fail(EnvReplayAllowedHosts, "is a development option for replaying to local services and cannot be used while %s=true",
			EnvAllowRemoteDashboard)
	}
	if cfg.AllowRemoteReplay && !cfg.AllowRemoteDashboard {
		warnings = append(warnings, fmt.Sprintf(
			"%s has no effect unless %s=true: replay to public destinations is always available to local dashboard users",
			EnvAllowRemoteReplay, EnvAllowRemoteDashboard))
	}

	if len(errs) > 0 {
		return Config{}, warnings, errors.Join(errs...)
	}
	return cfg, warnings, nil
}

// ParseBool accepts common spellings of true and false.
func ParseBool(s string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "on":
		return true, nil
	case "0", "false", "no", "off":
		return false, nil
	}
	return false, fmt.Errorf("%q is not a boolean (use true or false)", s)
}

// NormalizeBaseURL validates a public base URL and strips any trailing slash.
func NormalizeBaseURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("%q is not a valid URL", raw)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("%q must start with http:// or https://", raw)
	}
	if u.Host == "" {
		return "", fmt.Errorf("%q is missing a host", raw)
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("%q must not contain credentials, a query string, or a fragment", raw)
	}
	return strings.TrimRight(u.String(), "/"), nil
}

// ParseReplayAllowedHosts parses a comma-separated list of host:port entries
// that replay may reach even though they resolve to private addresses.
func ParseReplayAllowedHosts(raw string) ([]string, error) {
	var out []string
	seen := make(map[string]bool)
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		host, port, err := net.SplitHostPort(part)
		if err != nil {
			return nil, fmt.Errorf("%q must be in host:port form, for example localhost:3000", part)
		}
		p, err := strconv.Atoi(port)
		if err != nil || p < 1 || p > 65535 {
			return nil, fmt.Errorf("%q has an invalid port", part)
		}
		host = strings.ToLower(host)
		if addr, err := netip.ParseAddr(host); err == nil {
			addr = addr.Unmap()
			if IsMetadataAddr(addr) || addr.IsUnspecified() || addr.IsMulticast() {
				return nil, fmt.Errorf("%q cannot be allowed for replay", part)
			}
			host = addr.String()
		} else if !validHostname(host) {
			return nil, fmt.Errorf("%q has an invalid hostname", part)
		}
		entry := net.JoinHostPort(host, strconv.Itoa(p))
		if !seen[entry] {
			seen[entry] = true
			out = append(out, entry)
		}
	}
	return out, nil
}

// IsMetadataAddr reports whether addr is a well-known cloud metadata service
// address. These are never reachable through replay, even when allowlisted.
func IsMetadataAddr(addr netip.Addr) bool {
	for _, m := range metadataAddrs {
		if addr == m {
			return true
		}
	}
	return false
}

var metadataAddrs = []netip.Addr{
	netip.MustParseAddr("169.254.169.254"), // AWS, GCP, Azure, DigitalOcean, and others
	netip.MustParseAddr("169.254.170.2"),   // AWS ECS task metadata
	netip.MustParseAddr("100.100.100.200"), // Alibaba Cloud
	netip.MustParseAddr("fd00:ec2::254"),   // AWS IPv6
}

func defaultBaseURL(host string, port int) string {
	h := host
	if addr, err := netip.ParseAddr(host); err == nil && addr.IsUnspecified() {
		h = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(h, strconv.Itoa(port))
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	addr, err := netip.ParseAddr(strings.Trim(host, "[]"))
	return err == nil && addr.Unmap().IsLoopback()
}

func validHost(h string) bool {
	h = strings.Trim(h, "[]")
	if _, err := netip.ParseAddr(h); err == nil {
		return true
	}
	return validHostname(strings.ToLower(h))
}

func validHostname(h string) bool {
	if h == "" || len(h) > 253 {
		return false
	}
	for _, label := range strings.Split(h, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
				return false
			}
		}
	}
	return true
}
