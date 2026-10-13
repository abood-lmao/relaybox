package replay

import (
	"context"
	"net/netip"
	"strings"
	"testing"
)

func TestIsPublic(t *testing.T) {
	public := []string{"8.8.8.8", "1.1.1.1", "93.184.216.34", "2606:4700:4700::1111", "2a00:1450:4001:80b::200e"}
	private := []string{
		"127.0.0.1", "127.255.255.254", "10.0.0.1", "172.16.5.4", "192.168.1.1", "169.254.169.254",
		"169.254.1.1", "100.64.0.1", "0.0.0.0", "0.1.2.3", "192.0.2.10", "198.18.0.1", "203.0.113.5",
		"224.0.0.1", "255.255.255.255", "240.0.0.1",
		"::1", "::", "fe80::1", "fc00::1", "fd12:3456::1", "ff02::1", "fec0::1", "2001:db8::1",
		"::ffff:127.0.0.1", "::ffff:10.0.0.1", "::127.0.0.1", "64:ff9b::a00:1", "2002:a00:1::1", "2001::1",
	}
	for _, s := range public {
		if !IsPublic(netip.MustParseAddr(s)) {
			t.Errorf("%s should be public", s)
		}
	}
	for _, s := range private {
		if IsPublic(netip.MustParseAddr(s)) {
			t.Errorf("%s should not be public", s)
		}
	}
}

func TestValidateURLRejectsUnsafeDestinations(t *testing.T) {
	g := NewGuard(nil)
	cases := map[string]string{
		"":                                         "Enter",
		"ftp://example.com/":                       "http",
		"file:///etc/passwd":                       "http",
		"gopher://example.com":                     "http",
		"javascript:alert(1)":                      "http",
		"http://":                                  "host",
		"https://user:pass@example.com/":           "credentials",
		"http://127.0.0.1/":                        "loopback",
		"http://127.0.0.1:8787/hooks/x":            "loopback",
		"http://[::1]/":                            "loopback",
		"http://[::ffff:127.0.0.1]/":               "loopback",
		"http://10.1.2.3/":                         "private",
		"http://192.168.0.10:8080/":                "private",
		"http://[fd00::1]/":                        "private",
		"http://[fe80::1%25eth0]/":                 "zone",
		"http://169.254.169.254/latest/meta-data/": "metadata",
		"http://[fd00:ec2::254]/":                  "metadata",
		"http://0.0.0.0/":                          "unicast",
		"http://localhost/":                        "local",
		"http://api.localhost/":                    "local",
		"http://printer.local/":                    "local",
		"http://metadata.google.internal/":         "local",
		"http://127.1/":                            "not valid",
		"http://2130706433/":                       "not valid",
		"http://0x7f000001/":                       "not valid",
		"http://exa mple.com/":                     "parsed",
		"http://example.com:0/":                    "port",
		"http://example.com:70000/":                "port",
		"http://ex_ample.com/":                     "not valid",
		"https://" + strings.Repeat("a", 2100):     "longer",
	}
	for raw, want := range cases {
		_, err := g.ValidateURL(raw)
		if err == nil {
			t.Errorf("%q: expected rejection", raw)
			continue
		}
		if !IsBlocked(err) || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(want)) {
			t.Errorf("%q: error %q does not mention %q", raw, err, want)
		}
	}
}

func TestValidateURLNormalizes(t *testing.T) {
	g := NewGuard(nil)
	cases := map[string]string{
		"HTTPS://Example.COM./hook?x=1#frag": "https://example.com/hook?x=1",
		"http://example.com:80/a":            "http://example.com/a",
		"https://example.com:8443/a":         "https://example.com:8443/a",
		"http://8.8.8.8/":                    "http://8.8.8.8/",
		"http://[2606:4700:4700::1111]:81/":  "http://[2606:4700:4700::1111]:81/",
	}
	for raw, want := range cases {
		u, err := g.ValidateURL(raw)
		if err != nil {
			t.Errorf("%q: %v", raw, err)
			continue
		}
		if u.String() != want {
			t.Errorf("%q normalized to %q, want %q", raw, u.String(), want)
		}
	}
}

func TestAllowlistIsExactHostAndPort(t *testing.T) {
	g := NewGuard([]string{"localhost:3000", "127.0.0.1:9000"})
	for _, ok := range []string{"http://localhost:3000/webhook", "http://LOCALHOST:3000/", "http://127.0.0.1:9000/"} {
		if _, err := g.ValidateURL(ok); err != nil {
			t.Errorf("%q should be allowed: %v", ok, err)
		}
	}
	for _, bad := range []string{"http://localhost:3001/", "http://localhost/", "http://127.0.0.1:9001/", "http://127.0.0.2:9000/"} {
		if _, err := g.ValidateURL(bad); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
}

func TestDialRejectsWhenAnyResolvedAddressIsPrivate(t *testing.T) {
	g := NewGuard(nil)
	cases := map[string][]netip.Addr{
		"all-private.example": {netip.MustParseAddr("10.0.0.5")},
		"mixed.example":       {netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("127.0.0.1")},
		"mapped.example":      {netip.MustParseAddr("::ffff:192.168.1.1")},
		"v6-ula.example":      {netip.MustParseAddr("fd00::1")},
		"metadata.example":    {netip.MustParseAddr("169.254.169.254")},
	}
	for host, addrs := range cases {
		addrs := addrs
		g.lookup = func(context.Context, string) ([]netip.Addr, error) { return addrs, nil }
		_, err := g.DialContext(context.Background(), "tcp", host+":443")
		if !IsBlocked(err) {
			t.Errorf("%s: expected block, got %v", host, err)
		}
	}
}

func TestMetadataStaysBlockedForAllowlistedHosts(t *testing.T) {
	g := NewGuard([]string{"evil.example:80"})
	g.lookup = func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("169.254.169.254")}, nil
	}
	if _, err := g.DialContext(context.Background(), "tcp", "evil.example:80"); !IsBlocked(err) {
		t.Fatalf("metadata address must stay blocked: %v", err)
	}
}

func TestSocketLevelCheckBlocksPrivateAddresses(t *testing.T) {
	g := NewGuard(nil)
	ctl := g.control(false)
	for _, addr := range []string{"127.0.0.1:80", "[::1]:443", "10.0.0.1:80", "[::ffff:192.168.0.1]:80", "169.254.169.254:80"} {
		if err := ctl("tcp", addr, nil); !IsBlocked(err) {
			t.Errorf("%s: control allowed the connection", addr)
		}
	}
	if err := ctl("tcp", "8.8.8.8:443", nil); err != nil {
		t.Errorf("public address blocked: %v", err)
	}
	if err := g.control(true)("tcp", "127.0.0.1:3000", nil); err != nil {
		t.Errorf("allowlisted private address blocked: %v", err)
	}
	if err := g.control(true)("tcp", "169.254.169.254:80", nil); !IsBlocked(err) {
		t.Error("metadata must be blocked even when private addresses are allowed")
	}
}
