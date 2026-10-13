package api

import (
	"math"
	"net"
	"net/http"
	"strconv"

	apidocs "github.com/abood-lmao/relaybox/api"
	"github.com/abood-lmao/relaybox/internal/auth"
)

func (a *API) session(w http.ResponseWriter, r *http.Request) error {
	writeJSON(w, http.StatusOK, map[string]bool{
		"auth_required": a.auth.Enabled(),
		"authenticated": a.auth.Authenticated(r),
	})
	return nil
}

func (a *API) loginHandler(w http.ResponseWriter, r *http.Request) error {
	if !a.auth.Enabled() {
		return badRequest("auth_disabled", "This server does not require a login.")
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if ok, wait := a.login.Allow(host); !ok {
		w.Header().Set("Retry-After", strconv.Itoa(max(1, int(math.Ceil(wait.Seconds())))))
		return &Error{Status: http.StatusTooManyRequests, Code: "too_many_attempts",
			Message: "Too many login attempts. Wait a minute and try again."}
	}
	var in struct {
		Token string `json:"token"`
	}
	if err := decodeJSON(w, r, &in, 4096); err != nil {
		return err
	}
	if !a.auth.CheckToken(in.Token) {
		a.log.Warn("failed dashboard login", "remote", host)
		return &Error{Status: http.StatusUnauthorized, Code: "invalid_token", Message: "That token is not correct."}
	}
	a.auth.SetSessionCookie(w, r)
	writeJSON(w, http.StatusOK, map[string]bool{"auth_required": true, "authenticated": true})
	return nil
}

func (a *API) logout(w http.ResponseWriter, r *http.Request) error {
	auth.ClearSessionCookie(w, r)
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (a *API) openAPI(w http.ResponseWriter, _ *http.Request) error {
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(apidocs.OpenAPI)
	return nil
}
