# Architecture

Relaybox is one Go process with an embedded React dashboard and a SQLite database. There are no other services: no queue, no cache, no external database.

```text
webhook sender ─▶ /hooks/{id}/... ─▶ capture ─▶ redact ─▶ SQLite ─▶ mock response to the sender
                                                            │
                                                            ├─▶ events (SSE) ─▶ dashboard refreshes
                                                            │
browser ─▶ local-only or token check ─▶ /api/v1/... ◀───────┘
                                     ├─▶ replay ─▶ SSRF guard ─▶ destination URL
                                     └─▶ /  (embedded dashboard files)
```

## Request flow

### Capturing a webhook

1. The root handler (`internal/server`) sends every path under `/hooks/` to the capture service before any other routing. Capture endpoints are public by design.
2. `internal/capture` looks up the endpoint by its public identifier, checks that it is enabled, and applies the per-endpoint rate limit.
3. The body is read up to the endpoint's limit. A larger body is stored truncated, and the sender gets `413`.
4. `internal/redact` replaces sensitive headers, query parameters, and JSON, form, and multipart fields with `[REDACTED]`. The original values never reach the database.
5. The request is written to SQLite. Only after the write succeeds does the sender get the endpoint's mock response, after the configured delay. If the write fails, the sender gets `503` instead, so a sender never sees success for a request that was not recorded.
6. A `request.created` event with the request ID and a few fields (never headers or bodies) is published to connected dashboards.

### The dashboard

The dashboard is a single-page React app served from files embedded in the binary (`internal/webui`). It talks only to the REST API under `/api/v1`, and listens to `/api/v1/events` (Server-Sent Events) to refresh lists when something changes. Events carry IDs, not payloads, and are not a durable log: after a reconnect the dashboard refetches what it shows.

### Replay

`internal/replay` rebuilds a request from storage, applies the user's edits, and sends it either:

- **into an endpoint**, by running it through the capture pipeline directly (no network involved), or
- **to a URL**, through an HTTP client whose dialer checks every resolved address and connects only to addresses that passed the check. See [security.md](security.md#replay-and-ssrf).

Each attempt is recorded in `replay_attempts`, with the outcome, status, duration, and a one-line summary. Destination credentials are never stored: URLs with a user name or password are rejected.

## Packages

| Package | Responsibility |
| --- | --- |
| `cmd/relaybox` | Entry point. Sets up signal handling and calls `internal/cli`. |
| `internal/cli` | Commands: `serve`, `cleanup`, `export`, `doctor`, `version`, and the hidden `healthcheck` used by the container image. |
| `internal/config` | Reads and validates `RELAYBOX_*` variables. Reports every invalid value at once. |
| `internal/server` | Wires the components together. Owns the root handler, the local-only guard, the dashboard security headers, request logging, and panic recovery. |
| `internal/capture` | Receives `/hooks/` requests, enforces body limits, stores requests, and writes mock responses. Also ingests replays and fixture imports. |
| `internal/mockresp` | The mock response model: validation, reserved headers, and response building. |
| `internal/redact` | Redaction policy for headers, query strings, JSON, form, and multipart bodies. JSON is rewritten in place so untouched bytes stay identical. |
| `internal/storage` | SQLite access, migrations, queries, and search. |
| `internal/settings` | Runtime settings stored in the database, with defaults from the environment. |
| `internal/retention` | Deletes expired requests and enforces the stored request limit, every 10 minutes and shortly after new requests arrive. |
| `internal/events` | In-memory publish and subscribe broker behind the SSE stream, with a short backlog for reconnects. |
| `internal/api` | REST handlers, JSON views, error responses, login, and the OpenAPI document. |
| `internal/auth` | Token comparison and signed session cookies for remote-dashboard mode. |
| `internal/replay` | SSRF guard, HTTP sender, and the replay service with history and a concurrency limit. |
| `internal/compare` | Compares two requests or responses: JSON by path, text by line, binary by content. |
| `internal/fixture` | The fixture file format: building sanitized exports and validating imports. |
| `internal/ratelimit` | Token-bucket rate limiter keyed by endpoint or client address. |
| `internal/ids` | Public endpoint identifiers and random tokens. |
| `internal/validate` | Shared validation helpers for header names and values. |
| `internal/logging` | `log/slog` setup. |
| `internal/version` | Build information set with `-ldflags`. |
| `internal/webui` | Serves the embedded dashboard build. |
| `api` | `openapi.yaml` and `fixture.schema.json`, embedded and served by the API. |
| `web` | The React dashboard (Vite, TypeScript, Tailwind CSS, TanStack Query). |

## Storage

SQLite through `modernc.org/sqlite`, a pure-Go driver, so the binary builds with `CGO_ENABLED=0` and runs without system libraries.

- **Write and read pools.** Writes go through a pool with a single connection and `BEGIN IMMEDIATE` transactions. Reads use a separate read-only pool of four connections. With WAL mode, a slow search never blocks a capture.
- **Migrations** are embedded SQL files in `internal/storage/migrations`, applied in order at startup and recorded in `schema_migrations`.
- **Timestamps** are Unix milliseconds in UTC. Durations are microseconds.

Tables:

| Table | Contents |
| --- | --- |
| `endpoints` | Name, public identifier, enabled flag, mock response, optional body limit. Names are unique ignoring case. |
| `captured_requests` | Method, path, query, headers, body (as stored, after redaction), size and truncation flag, the response that was sent, timing, source (`capture`, `replay`, or `import`), the list of redactions, and a link to the original for replays. Deleting an endpoint deletes its requests. |
| `replay_attempts` | One row per replay, linked to the original request. Deleted with it. |
| `settings` | Runtime settings as JSON values. |

Search (`q`) uses parameterized `LIKE` matching over the path, query, headers, content type, and body text. It is case-insensitive for ASCII letters and fine for the data volumes Relaybox is built for (tens of thousands of requests).

## Dashboard

- React 19 with React Router, TanStack Query for data fetching and caching, Tailwind CSS 4, and Lucide icons.
- No inline scripts or styles in the HTML, so the strict Content Security Policy needs no exceptions.
- Captured content is always rendered as React text nodes. JSON is parsed by a small parser in `web/src/lib/json.ts` that keeps numbers exactly as sent (so `9007199254740993` is not rounded) and keeps duplicate keys.
- The build output goes to `internal/webui/static/dist` and is embedded with `go:embed`. A binary built without it still serves the API and capture endpoints, and shows a page explaining how to build the dashboard.

## Design decisions

- **One process, one file.** A single binary and a single database file keep installation, backup, and removal trivial.
- **Persist before responding.** A sender gets the mock response only once the request is safely stored.
- **Redact before storing.** Secrets that match the rules never reach disk, exports, or event streams.
- **Local by default.** The dashboard is only reachable from the same machine unless remote access is configured with a token.
- **Explicit replay.** Nothing is ever forwarded automatically. Replays to URLs need a confirmation and pass through the SSRF guard.
