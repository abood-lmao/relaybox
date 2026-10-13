// Package auth implements token authentication for remotely accessible
// dashboards. Browsers exchange the token for a signed session cookie; API
// clients can send the token as a Bearer credential instead.
//
// Sessions are stateless: the cookie carries an expiry and an HMAC derived
// from the token. Changing RELAYBOX_DASHBOARD_TOKEN invalidates every
// session.
package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"net/http"
	"strings"
	"time"
)

// CookieName is the session cookie. The capture handler strips it from
// recorded requests.
const CookieName = "relaybox_session"

// SessionTTL is how long a login lasts.
const SessionTTL = 7 * 24 * time.Hour

// Authenticator checks tokens and sessions. The zero value (no token)
// authenticates nothing and reports Enabled() == false.
type Authenticator struct {
	tokenHash [32]byte
	key       []byte
	enabled   bool
	now       func() time.Time
}

// New returns an authenticator for token. An empty token disables auth.
func New(token string) *Authenticator {
	a := &Authenticator{now: time.Now}
	if token == "" {
		return a
	}
	a.enabled = true
	a.tokenHash = sha256.Sum256([]byte(token))
	mac := hmac.New(sha256.New, []byte("relaybox session key v1"))
	mac.Write([]byte(token))
	a.key = mac.Sum(nil)
	return a
}

// Enabled reports whether a token is configured.
func (a *Authenticator) Enabled() bool { return a.enabled }

// CheckToken compares candidate with the configured token in constant time.
func (a *Authenticator) CheckToken(candidate string) bool {
	if !a.enabled {
		return false
	}
	h := sha256.Sum256([]byte(candidate))
	return subtle.ConstantTimeCompare(h[:], a.tokenHash[:]) == 1
}

// NewSession returns a signed session value and its expiry.
func (a *Authenticator) NewSession() (string, time.Time) {
	expires := a.now().Add(SessionTTL)
	payload := make([]byte, 24)
	binary.BigEndian.PutUint64(payload[:8], uint64(expires.Unix()))
	_, _ = rand.Read(payload[8:])
	return enc(payload) + "." + enc(a.sign(payload)), expires
}

// ValidSession reports whether value is an unexpired session signed with the
// current token.
func (a *Authenticator) ValidSession(value string) bool {
	if !a.enabled {
		return false
	}
	p, s, ok := strings.Cut(value, ".")
	if !ok {
		return false
	}
	payload, err1 := base64.RawURLEncoding.DecodeString(p)
	sig, err2 := base64.RawURLEncoding.DecodeString(s)
	if err1 != nil || err2 != nil || len(payload) != 24 {
		return false
	}
	if !hmac.Equal(sig, a.sign(payload)) {
		return false
	}
	expires := time.Unix(int64(binary.BigEndian.Uint64(payload[:8])), 0)
	return a.now().Before(expires)
}

func (a *Authenticator) sign(payload []byte) []byte {
	mac := hmac.New(sha256.New, a.key)
	mac.Write(payload)
	return mac.Sum(nil)
}

func enc(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// Authenticated reports whether r carries a valid session cookie or Bearer
// token. It always returns true when auth is disabled.
func (a *Authenticator) Authenticated(r *http.Request) bool {
	if !a.enabled {
		return true
	}
	if c, err := r.Cookie(CookieName); err == nil && a.ValidSession(c.Value) {
		return true
	}
	if scheme, cred, ok := strings.Cut(r.Header.Get("Authorization"), " "); ok && strings.EqualFold(scheme, "Bearer") {
		return a.CheckToken(strings.TrimSpace(cred))
	}
	return false
}

// SetSessionCookie starts a browser session.
func (a *Authenticator) SetSessionCookie(w http.ResponseWriter, r *http.Request) {
	value, expires := a.NewSession()
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    value,
		Path:     "/",
		Expires:  expires,
		MaxAge:   int(SessionTTL.Seconds()),
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteStrictMode,
	})
}

// ClearSessionCookie ends a browser session.
func ClearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteStrictMode,
	})
}

// isHTTPS reports whether the browser reached us over HTTPS, directly or
// through a TLS-terminating proxy.
func isHTTPS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}
