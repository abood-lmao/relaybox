package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/abood-lmao/relaybox/internal/auth"
	"github.com/abood-lmao/relaybox/internal/config"
)

func remoteMode(c *config.Config) {
	c.Host = "0.0.0.0"
	c.AllowRemoteDashboard = true
	c.DashboardToken = testToken
}

func TestLoginFlow(t *testing.T) {
	h := newHarness(t, remoteMode)
	w := h.do("GET", "/api/v1/auth/session", nil)
	expectStatus(t, w, 200)
	if s := decode[map[string]bool](t, w); !s["auth_required"] || s["authenticated"] {
		t.Fatalf("session = %v", s)
	}

	expectError(t, h.do("POST", "/api/v1/auth/login", map[string]string{"token": "wrong"}), 401, "invalid_token")
	expectError(t, h.do("POST", "/api/v1/auth/login", map[string]string{"token": testToken[:len(testToken)-1]}), 401, "invalid_token")

	w = h.do("POST", "/api/v1/auth/login", map[string]string{"token": testToken})
	expectStatus(t, w, 200)
	var cookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == auth.CookieName {
			cookie = c
		}
	}
	if cookie == nil || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/" {
		t.Fatalf("cookie = %+v", cookie)
	}
	if strings.Contains(cookie.Value, testToken) {
		t.Fatal("the session cookie contains the token")
	}

	w = h.do("GET", "/api/v1/auth/session", nil, "Cookie", auth.CookieName+"="+cookie.Value)
	if s := decode[map[string]bool](t, w); !s["authenticated"] {
		t.Fatalf("cookie session = %v", s)
	}
	w = h.do("GET", "/api/v1/auth/session", nil, "Authorization", "Bearer "+testToken)
	if s := decode[map[string]bool](t, w); !s["authenticated"] {
		t.Fatalf("bearer session = %v", s)
	}
	w = h.do("GET", "/api/v1/auth/session", nil, "Cookie", auth.CookieName+"="+cookie.Value+"x")
	if s := decode[map[string]bool](t, w); s["authenticated"] {
		t.Fatal("tampered cookie accepted")
	}

	w = h.do("POST", "/api/v1/auth/logout", nil)
	expectStatus(t, w, 204)
	cleared := false
	for _, c := range w.Result().Cookies() {
		if c.Name == auth.CookieName && c.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Errorf("logout cookies = %v", w.Result().Cookies())
	}
}

func TestLoginIsRateLimited(t *testing.T) {
	h := newHarness(t, remoteMode)
	for i := range 10 {
		if w := h.do("POST", "/api/v1/auth/login", map[string]string{"token": "guess"}); w.Code != 401 {
			t.Fatalf("attempt %d = %d", i+1, w.Code)
		}
	}
	w := h.do("POST", "/api/v1/auth/login", map[string]string{"token": testToken})
	expectError(t, w, 429, "too_many_attempts")
	if w.Header().Get("Retry-After") == "" {
		t.Error("missing Retry-After")
	}
}

func TestLoginWithoutAuth(t *testing.T) {
	h := newHarness(t)
	expectError(t, h.do("POST", "/api/v1/auth/login", map[string]string{"token": "x"}), 400, "auth_disabled")
	w := h.do("GET", "/api/v1/auth/session", nil)
	if s := decode[map[string]bool](t, w); s["auth_required"] {
		t.Fatalf("session = %v", s)
	}
}
