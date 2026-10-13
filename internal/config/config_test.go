package config

import (
	"log/slog"
	"strings"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	cfg, warnings, err := Load(map[string]string{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if cfg.Host != "127.0.0.1" || cfg.Port != 8787 {
		t.Errorf("listen address = %s, want 127.0.0.1:8787", cfg.Addr())
	}
	if cfg.PublicBaseURL != "http://127.0.0.1:8787" {
		t.Errorf("PublicBaseURL = %q", cfg.PublicBaseURL)
	}
	if cfg.MaxBodyBytes != 1<<20 || cfg.RetentionDays != 7 {
		t.Errorf("limits = %d bytes / %d days", cfg.MaxBodyBytes, cfg.RetentionDays)
	}
	if !cfg.LoopbackBind() || cfg.AllowRemoteDashboard || cfg.AuthRequired() {
		t.Errorf("defaults must be local-only without auth: %+v", cfg)
	}
	if cfg.LogLevel != slog.LevelInfo || cfg.LogFormat != "text" {
		t.Errorf("logging = %v/%s", cfg.LogLevel, cfg.LogFormat)
	}
}

func TestLoadOverrides(t *testing.T) {
	cfg, _, err := Load(map[string]string{
		EnvHost:               "::1",
		EnvPort:               "9000",
		EnvDatabasePath:       "/tmp/x.db",
		EnvPublicBaseURL:      "https://hooks.example.com/",
		EnvMaxBodyBytes:       "2048",
		EnvRetentionDays:      "0",
		EnvLogLevel:           "debug",
		EnvLogFormat:          "JSON",
		EnvReplayAllowedHosts: "localhost:3000, 127.0.0.1:8080,localhost:3000",
		EnvRateLimitRPS:       "0",
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Addr() != "[::1]:9000" {
		t.Errorf("Addr = %s", cfg.Addr())
	}
	if cfg.PublicBaseURL != "https://hooks.example.com" {
		t.Errorf("PublicBaseURL = %q (trailing slash should be removed)", cfg.PublicBaseURL)
	}
	if cfg.MaxBodyBytes != 2048 || cfg.RetentionDays != 0 || cfg.RateLimitRPS != 0 {
		t.Errorf("numeric overrides not applied: %+v", cfg)
	}
	if cfg.LogLevel != slog.LevelDebug || cfg.LogFormat != "json" {
		t.Errorf("logging = %v/%s", cfg.LogLevel, cfg.LogFormat)
	}
	want := []string{"localhost:3000", "127.0.0.1:8080"}
	if strings.Join(cfg.ReplayAllowedHosts, ",") != strings.Join(want, ",") {
		t.Errorf("ReplayAllowedHosts = %v, want %v", cfg.ReplayAllowedHosts, want)
	}
}

func TestLoadRejectsInvalidValues(t *testing.T) {
	cases := map[string]map[string]string{
		"port text":         {EnvPort: "http"},
		"port range":        {EnvPort: "70000"},
		"bool":              {EnvAllowRemoteDashboard: "maybe"},
		"body too small":    {EnvMaxBodyBytes: "10"},
		"body too large":    {EnvMaxBodyBytes: "999999999999"},
		"retention":         {EnvRetentionDays: "-1"},
		"log level":         {EnvLogLevel: "loud"},
		"log format":        {EnvLogFormat: "xml"},
		"base url scheme":   {EnvPublicBaseURL: "ftp://example.com"},
		"base url query":    {EnvPublicBaseURL: "https://example.com/?a=b"},
		"base url userinfo": {EnvPublicBaseURL: "https://user:pass@example.com"},
		"host":              {EnvHost: "not a host!"},
		"empty db path":     {EnvDatabasePath: "  "},
		"short token":       {EnvDashboardToken: "short"},
		"token whitespace":  {EnvDashboardToken: "abcdefghijkl mnopqrstuvwxyz"},
		"allowed host port": {EnvReplayAllowedHosts: "localhost"},
		"metadata allowed":  {EnvReplayAllowedHosts: "169.254.169.254:80"},
		"rate":              {EnvRateLimitRPS: "-5"},
		"burst":             {EnvRateLimitBurst: "0"},
	}
	for name, env := range cases {
		t.Run(name, func(t *testing.T) {
			if _, _, err := Load(env); err == nil {
				t.Fatalf("Load(%v) succeeded, want error", env)
			}
		})
	}
}

func TestLoadReportsAllErrors(t *testing.T) {
	_, _, err := Load(map[string]string{EnvPort: "x", EnvLogLevel: "y"})
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	if !strings.Contains(msg, EnvPort) || !strings.Contains(msg, EnvLogLevel) {
		t.Fatalf("error should mention both variables: %v", msg)
	}
}

func TestRemoteDashboardRequiresToken(t *testing.T) {
	_, _, err := Load(map[string]string{EnvAllowRemoteDashboard: "true"})
	if err == nil || !strings.Contains(err.Error(), EnvDashboardToken) {
		t.Fatalf("expected token error, got %v", err)
	}

	cfg, _, err := Load(map[string]string{
		EnvAllowRemoteDashboard: "true",
		EnvDashboardToken:       strings.Repeat("a", 32),
		EnvHost:                 "0.0.0.0",
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.AuthRequired() || cfg.LoopbackBind() {
		t.Fatalf("unexpected config: %+v", cfg)
	}
	if cfg.PublicBaseURL != "http://127.0.0.1:8787" {
		t.Errorf("unspecified bind address should display 127.0.0.1, got %q", cfg.PublicBaseURL)
	}
	if cfg.URLReplayEnabled() {
		t.Error("URL replay must be off for remote dashboards unless explicitly allowed")
	}
}

func TestReplaySafetyRules(t *testing.T) {
	token := strings.Repeat("a", 32)

	_, _, err := Load(map[string]string{
		EnvAllowRemoteDashboard: "true",
		EnvDashboardToken:       token,
		EnvReplayAllowedHosts:   "localhost:3000",
	})
	if err == nil || !strings.Contains(err.Error(), EnvReplayAllowedHosts) {
		t.Fatalf("local replay hosts must be rejected with a remote dashboard, got %v", err)
	}

	cfg, _, err := Load(map[string]string{
		EnvAllowRemoteDashboard: "true",
		EnvDashboardToken:       token,
		EnvAllowRemoteReplay:    "true",
	})
	if err != nil || !cfg.URLReplayEnabled() {
		t.Fatalf("explicit opt-in should enable URL replay: %v", err)
	}

	cfg, warnings, err := Load(map[string]string{EnvAllowRemoteReplay: "true"})
	if err != nil || !cfg.URLReplayEnabled() || len(warnings) == 0 {
		t.Fatalf("local mode: enabled=%v warnings=%v err=%v", cfg.URLReplayEnabled(), warnings, err)
	}
}

func TestNonLoopbackBindWarns(t *testing.T) {
	_, warnings, err := Load(map[string]string{EnvHost: "0.0.0.0"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(warnings) == 0 {
		t.Fatal("expected a warning about network exposure")
	}
}

func TestUnknownVariablesWarn(t *testing.T) {
	_, warnings, err := Load(map[string]string{"RELAYBOX_PROT": "1"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "RELAYBOX_PROT") {
		t.Fatalf("warnings = %v", warnings)
	}
}

func TestParseBool(t *testing.T) {
	for _, s := range []string{"1", "true", "TRUE", "yes", "on"} {
		if b, err := ParseBool(s); err != nil || !b {
			t.Errorf("ParseBool(%q) = %v, %v", s, b, err)
		}
	}
	for _, s := range []string{"0", "false", "No", "off"} {
		if b, err := ParseBool(s); err != nil || b {
			t.Errorf("ParseBool(%q) = %v, %v", s, b, err)
		}
	}
	if _, err := ParseBool("2"); err == nil {
		t.Error("ParseBool(2) should fail")
	}
}

func TestFromEnvironKeepsOnlyRelayboxVariables(t *testing.T) {
	env := FromEnviron([]string{"PATH=/bin", "RELAYBOX_PORT=1234", "RELAYBOX_HOST=a=b"})
	if len(env) != 2 || env["RELAYBOX_PORT"] != "1234" || env["RELAYBOX_HOST"] != "a=b" {
		t.Fatalf("FromEnviron = %v", env)
	}
}
