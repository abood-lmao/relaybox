// Package server wires the services together and runs the HTTP server.
package server

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/abood-lmao/relaybox/internal/api"
	"github.com/abood-lmao/relaybox/internal/auth"
	"github.com/abood-lmao/relaybox/internal/capture"
	"github.com/abood-lmao/relaybox/internal/config"
	"github.com/abood-lmao/relaybox/internal/events"
	"github.com/abood-lmao/relaybox/internal/ratelimit"
	"github.com/abood-lmao/relaybox/internal/replay"
	"github.com/abood-lmao/relaybox/internal/retention"
	"github.com/abood-lmao/relaybox/internal/settings"
	"github.com/abood-lmao/relaybox/internal/storage"
	"github.com/abood-lmao/relaybox/internal/version"
	"github.com/abood-lmao/relaybox/internal/webui"
)

// Limits and timeouts. Documented in docs/configuration.md.
const (
	maxEventClients   = 32
	eventHistory      = 256
	rateLimiterKeys   = 10000
	readHeaderTimeout = 10 * time.Second
	readTimeout       = 60 * time.Second
	writeTimeout      = 75 * time.Second
	idleTimeout       = 120 * time.Second
	maxHeaderBytes    = 256 << 10
	shutdownTimeout   = 10 * time.Second
)

// App is a fully wired Relaybox instance.
type App struct {
	cfg       config.Config
	log       *slog.Logger
	db        *storage.DB
	broker    *events.Broker
	retention *retention.Runner
	auth      *auth.Authenticator
	api       *api.API
	capture   *capture.Service
	ui        *webui.Handler
	closeOnce sync.Once
}

// Open opens the database and builds every service. ui holds the built
// dashboard; nil serves a page explaining how to build it.
func Open(ctx context.Context, cfg config.Config, log *slog.Logger, ui fs.FS) (*App, error) {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	db, err := storage.Open(ctx, cfg.DatabasePath)
	if err != nil {
		return nil, err
	}
	set, warnings, err := settings.New(ctx, db, settings.DefaultsFromConfig(cfg), cfg.MaxBodyBytes)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("load settings: %w", err)
	}
	for _, w := range warnings {
		log.Warn("stored setting ignored", "detail", w)
	}
	uiHandler, err := webui.NewHandler(ui)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("load dashboard files: %w", err)
	}

	broker := events.New(maxEventClients, eventHistory)
	ret := retention.New(db, set, broker, log)
	var secrets []string
	if cfg.DashboardToken != "" {
		secrets = append(secrets, cfg.DashboardToken)
	}
	capSvc := capture.New(capture.Options{
		DB:           db,
		Settings:     set,
		Events:       broker,
		Limiter:      ratelimit.New(cfg.RateLimitRPS, cfg.RateLimitBurst, rateLimiterKeys),
		Logger:       log,
		Secrets:      secrets,
		StripCookies: []string{auth.CookieName},
		OnStored:     ret.Nudge,
	})
	guard := replay.NewGuard(cfg.ReplayAllowedHosts)
	rep := replay.NewService(replay.ServiceOptions{
		DB:         db,
		Capture:    capSvc,
		Guard:      guard,
		Sender:     replay.NewSender(guard, "relaybox/"+version.Version, replay.DefaultTimeout),
		URLEnabled: cfg.URLReplayEnabled(),
		Logger:     log,
	})
	authn := auth.New(cfg.DashboardToken)
	a := &App{
		cfg: cfg, log: log, db: db, broker: broker, retention: ret, auth: authn, capture: capSvc, ui: uiHandler,
		api: api.New(api.Deps{
			Config: cfg, DB: db, Settings: set, Capture: capSvc, Replay: rep,
			Retention: ret, Events: broker, Auth: authn, Logger: log,
		}),
	}
	return a, nil
}

// Close releases the database. It is safe to call more than once.
func (a *App) Close() error {
	var err error
	a.closeOnce.Do(func() {
		a.broker.Close()
		err = a.db.Close()
	})
	return err
}

// Serve handles connections on ln until ctx is cancelled, then shuts down
// gracefully: new connections are refused, live event streams are closed,
// and in-flight requests get up to ten seconds to finish.
func (a *App) Serve(ctx context.Context, ln net.Listener) error {
	srv := &http.Server{
		Handler:           a.Handler(),
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
		MaxHeaderBytes:    maxHeaderBytes,
		ErrorLog:          slog.NewLogLogger(a.log.Handler(), slog.LevelDebug),
	}
	srv.RegisterOnShutdown(a.broker.Close)

	retCtx, stopRetention := context.WithCancel(context.WithoutCancel(ctx))
	retDone := make(chan struct{})
	go func() {
		defer close(retDone)
		a.retention.Run(retCtx)
	}()
	defer func() {
		stopRetention()
		<-retDone
	}()

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()

	select {
	case err := <-serveErr:
		return err
	case <-ctx.Done():
	}
	a.log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		a.log.Warn("some requests did not finish before shutdown", "err", err)
		_ = srv.Close()
	}
	if err := <-serveErr; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	a.log.Info("stopped")
	return nil
}

// Run opens the app, listens on the configured address, and serves until
// ctx is cancelled.
func Run(ctx context.Context, cfg config.Config, log *slog.Logger) error {
	app, err := Open(ctx, cfg, log, webui.Dist())
	if err != nil {
		return err
	}
	defer app.Close()

	ln, err := net.Listen("tcp", cfg.Addr())
	if err != nil {
		if errors.Is(err, syscall.EADDRINUSE) || strings.Contains(err.Error(), "address already in use") ||
			strings.Contains(err.Error(), "Only one usage of each socket address") {
			return fmt.Errorf("cannot listen on %s: the port is already in use. Stop the other process or choose another port with --port or %s", cfg.Addr(), config.EnvPort)
		}
		return fmt.Errorf("cannot listen on %s: %w", cfg.Addr(), err)
	}
	app.logStartup()
	return app.Serve(ctx, ln)
}

func (a *App) logStartup() {
	dbPath, err := filepath.Abs(a.cfg.DatabasePath)
	if err != nil {
		dbPath = a.cfg.DatabasePath
	}
	a.log.Info("relaybox started",
		"version", version.Version,
		"listen", a.cfg.Addr(),
		"dashboard", a.cfg.PublicBaseURL,
		"database", dbPath,
		"remote_dashboard", a.cfg.AllowRemoteDashboard,
		"url_replay", a.cfg.URLReplayEnabled(),
	)
	if !a.ui.Built() {
		a.log.Warn("this binary was built without the dashboard; run make build to include it")
	}
	if len(a.cfg.ReplayAllowedHosts) > 0 {
		a.log.Warn("replay may reach these private hosts", "hosts", strings.Join(a.cfg.ReplayAllowedHosts, ","))
	}
}
