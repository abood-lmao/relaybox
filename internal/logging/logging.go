// Package logging configures the structured logger used across Relaybox.
package logging

import (
	"fmt"
	"io"
	"log/slog"
	"strings"
)

// ParseLevel converts a level name (debug, info, warn, error) to a slog.Level.
func ParseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug, nil
	case "", "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	}
	return slog.LevelInfo, fmt.Errorf("unknown log level %q (use debug, info, warn, or error)", s)
}

// New returns a logger writing to w in the given format ("text" or "json").
func New(w io.Writer, level slog.Level, format string) *slog.Logger {
	opts := &slog.HandlerOptions{Level: level}
	if format == "json" {
		return slog.New(slog.NewJSONHandler(w, opts))
	}
	return slog.New(slog.NewTextHandler(w, opts))
}

// HookPath shortens a /hooks/{id}/... path for logs. Endpoint identifiers are
// bearer capabilities, so logs only keep a short prefix.
func HookPath(path string) string {
	const prefix = "/hooks/"
	if !strings.HasPrefix(path, prefix) {
		return path
	}
	rest := path[len(prefix):]
	id, tail, _ := strings.Cut(rest, "/")
	if len(id) > 4 {
		id = id[:4] + "..."
	}
	if tail != "" {
		return prefix + id + "/" + tail
	}
	return prefix + id
}
