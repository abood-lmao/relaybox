package api

import (
	"net/http"
	"path/filepath"
	"time"

	"github.com/abood-lmao/relaybox/internal/capture"
	"github.com/abood-lmao/relaybox/internal/config"
	"github.com/abood-lmao/relaybox/internal/mockresp"
	"github.com/abood-lmao/relaybox/internal/replay"
	"github.com/abood-lmao/relaybox/internal/settings"
	"github.com/abood-lmao/relaybox/internal/storage"
	"github.com/abood-lmao/relaybox/internal/version"
)

var startedAt = time.Now()

// getMeta returns what the dashboard needs at startup.
func (a *API) getMeta(w http.ResponseWriter, _ *http.Request) error {
	writeJSON(w, http.StatusOK, map[string]any{
		"name":               "Relaybox",
		"version":            version.Version,
		"commit":             version.Commit,
		"build_date":         version.Date,
		"public_base_url":    a.cfg.PublicBaseURL,
		"hook_url_prefix":    a.cfg.PublicBaseURL + capture.PathPrefix,
		"auth_required":      a.auth.Enabled(),
		"remote_dashboard":   a.cfg.AllowRemoteDashboard,
		"url_replay_enabled": a.replay.URLEnabled(),
		"allowed_methods":    mockresp.AllowedMethods,
		"mock_defaults":      mockresp.Default(),
		"limits": map[string]any{
			"mock_body_bytes":        mockresp.MaxBodyBytes,
			"mock_headers":           mockresp.MaxHeaders,
			"mock_delay_ms":          mockresp.MaxDelayMS,
			"endpoint_name_length":   maxEndpointNameLen,
			"min_body_bytes":         config.MinBodyBytes,
			"max_body_bytes_ceiling": a.settings.Ceiling(),
			"default_max_body_bytes": a.settings.Current().DefaultMaxBodyBytes,
			"replay_body_bytes":      replay.MaxBodyBytes,
			"replay_response_bytes":  replay.MaxResponseBodyBytes,
		},
	})
	return nil
}

func (a *API) getStats(w http.ResponseWriter, r *http.Request) error {
	s, err := a.db.Stats(r.Context(), a.now())
	if err != nil {
		return err
	}
	recent, _, err := a.db.ListRequests(r.Context(), storage.RequestFilter{Limit: 8})
	if err != nil {
		return err
	}
	hourly := make([]map[string]any, 0, len(s.Hourly))
	for _, h := range s.Hourly {
		hourly = append(hourly, map[string]any{"start": h.Start, "count": h.Count})
	}
	top := make([]map[string]any, 0, len(s.TopEndpoints))
	for _, e := range s.TopEndpoints {
		top = append(top, map[string]any{
			"id": e.ID, "name": e.Name, "enabled": e.Enabled,
			"last_24h": e.Last24h, "total": e.Total, "last_request_at": e.LastSeen,
		})
	}
	recentViews := make([]RequestSummaryView, 0, len(recent))
	for _, rq := range recent {
		recentViews = append(recentViews, summaryView(rq))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"total_requests": s.TotalRequests,
		"last_24h":       s.Last24h,
		"endpoints":      s.Endpoints,
		"status_classes": s.StatusClasses,
		"hourly":         hourly,
		"top_endpoints":  top,
		"recent":         recentViews,
	})
	return nil
}

type settingsView struct {
	Runtime    settings.Values `json:"runtime"`
	Defaults   settings.Values `json:"defaults"`
	Overridden []string        `json:"overridden"`
	Limits     map[string]any  `json:"limits"`
	Server     map[string]any  `json:"server"`
}

func (a *API) settingsView(r *http.Request) settingsView {
	cur := a.settings.Current()
	overridden := cur.Overridden
	if overridden == nil {
		overridden = []string{}
	}
	dbPath, err := filepath.Abs(a.db.Path())
	if err != nil {
		dbPath = a.db.Path()
	}
	schema, _ := a.db.SchemaVersion(r.Context())
	allowed := a.cfg.ReplayAllowedHosts
	if allowed == nil {
		allowed = []string{}
	}
	return settingsView{
		Runtime:    cur.Values,
		Defaults:   a.settings.Defaults(),
		Overridden: overridden,
		Limits: map[string]any{
			"min_body_bytes":            config.MinBodyBytes,
			"max_body_bytes_ceiling":    a.settings.Ceiling(),
			"max_retention_days":        config.MaxRetentionDays,
			"max_stored_requests_limit": config.MaxStoredRequestsLimit,
		},
		Server: map[string]any{
			"version":               version.Version,
			"commit":                version.Commit,
			"build_date":            version.Date,
			"started_at":            startedAt.UTC(),
			"listen_address":        a.cfg.Addr(),
			"public_base_url":       a.cfg.PublicBaseURL,
			"database_path":         dbPath,
			"database_size_bytes":   a.db.SizeBytes(),
			"schema_version":        schema,
			"log_level":             a.cfg.LogLevel.String(),
			"log_format":            a.cfg.LogFormat,
			"remote_dashboard":      a.cfg.AllowRemoteDashboard,
			"auth_required":         a.auth.Enabled(),
			"url_replay_enabled":    a.replay.URLEnabled(),
			"replay_allowed_hosts":  allowed,
			"rate_limit_rps":        a.cfg.RateLimitRPS,
			"rate_limit_burst":      a.cfg.RateLimitBurst,
			"max_body_bytes_limit":  a.cfg.MaxBodyBytes,
			"replay_timeout_ms":     replay.DefaultTimeout.Milliseconds(),
			"replay_response_bytes": replay.MaxResponseBodyBytes,
		},
	}
}

func (a *API) getSettings(w http.ResponseWriter, r *http.Request) error {
	writeJSON(w, http.StatusOK, a.settingsView(r))
	return nil
}

func (a *API) updateSettings(w http.ResponseWriter, r *http.Request) error {
	var p settings.Patch
	if err := decodeJSON(w, r, &p, maxJSONBody); err != nil {
		return err
	}
	if _, err := a.settings.Update(r.Context(), p); err != nil {
		return err
	}
	a.log.Info("settings updated")
	a.publish(EventSettingsUpdated, map[string]any{})
	if a.retention != nil {
		a.retention.Nudge()
	}
	writeJSON(w, http.StatusOK, a.settingsView(r))
	return nil
}

func (a *API) runCleanup(w http.ResponseWriter, r *http.Request) error {
	res, err := a.retention.RunOnce(r.Context())
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, res)
	return nil
}
