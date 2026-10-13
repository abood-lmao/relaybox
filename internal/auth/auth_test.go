package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const token = "0123456789abcdefghijklmnop"

func TestDisabledAuthenticator(t *testing.T) {
	a := New("")
	if a.Enabled() || a.CheckToken("") || a.ValidSession("x.y") {
		t.Fatal("disabled authenticator must not accept credentials")
	}
	if !a.Authenticated(httptest.NewRequest("GET", "/", nil)) {
		t.Fatal("requests pass when auth is disabled")
	}
}

func TestTokenCheck(t *testing.T) {
	a := New(token)
	if !a.CheckToken(token) || a.CheckToken(token+"x") || a.CheckToken("") || a.CheckToken(strings.ToUpper(token)) {
		t.Fatal("token comparison is wrong")
	}
}

func TestSessionLifecycle(t *testing.T) {
	a := New(token)
	now := time.Now()
	a.now = func() time.Time { return now }
	value, expires := a.NewSession()
	if !a.ValidSession(value) || !expires.After(now) {
		t.Fatal("fresh session should be valid")
	}
	now = expires.Add(time.Second)
	if a.ValidSession(value) {
		t.Fatal("expired session accepted")
	}
}

func TestSessionsAreBoundToTheToken(t *testing.T) {
	value, _ := New(token).NewSession()
	if New("another-token-0123456789xyz").ValidSession(value) {
		t.Fatal("session signed with a different token accepted")
	}
}

func TestTamperedSessionsAreRejected(t *testing.T) {
	a := New(token)
	value, _ := a.NewSession()
	payload, sig, _ := strings.Cut(value, ".")
	forged := []string{
		"",
		"garbage",
		payload,
		payload + ".",
		"." + sig,
		payload + "." + sig[:len(sig)-2] + "AA",
		strings.Replace(payload, payload[:2], "__", 1) + "." + sig,
	}
	for _, f := range forged {
		if a.ValidSession(f) {
			t.Errorf("accepted forged session %q", f)
		}
	}
}

func TestAuthenticatedRequest(t *testing.T) {
	a := New(token)
	value, _ := a.NewSession()

	r := httptest.NewRequest("GET", "/api/v1/endpoints", nil)
	if a.Authenticated(r) {
		t.Fatal("anonymous request accepted")
	}
	r.AddCookie(&http.Cookie{Name: CookieName, Value: value})
	if !a.Authenticated(r) {
		t.Fatal("session cookie rejected")
	}

	r = httptest.NewRequest("GET", "/api/v1/endpoints", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	if !a.Authenticated(r) {
		t.Fatal("bearer token rejected")
	}
	r.Header.Set("Authorization", "Basic "+token)
	if a.Authenticated(r) {
		t.Fatal("non-bearer scheme accepted")
	}
}

func TestSessionCookieAttributes(t *testing.T) {
	a := New(token)
	w := httptest.NewRecorder()
	a.SetSessionCookie(w, httptest.NewRequest("POST", "/", nil))
	c := w.Result().Cookies()[0]
	if !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.Path != "/" || c.Secure {
		t.Fatalf("cookie %+v", c)
	}
	r := httptest.NewRequest("POST", "/", nil)
	r.Header.Set("X-Forwarded-Proto", "https")
	w = httptest.NewRecorder()
	a.SetSessionCookie(w, r)
	if !w.Result().Cookies()[0].Secure {
		t.Fatal("cookie should be Secure behind HTTPS")
	}
}
