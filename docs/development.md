# Development

This page covers working on Relaybox itself. For contribution guidelines, see [CONTRIBUTING.md](../CONTRIBUTING.md).

## Repository layout

| Path | Contents |
| --- | --- |
| `cmd/relaybox/` | The `main` package. It only calls `internal/cli`. |
| `internal/cli/` | Commands (`serve`, `cleanup`, `export`, `doctor`, `version`), flags, exit codes. |
| `internal/config/` | Environment variables, validation, and defaults. |
| `internal/server/` | Wiring, the root handler, access control, security headers, request logging, graceful shutdown. |
| `internal/api/` | REST handlers under `/api/v1`, error responses, the SSE stream. |
| `internal/capture/` | The capture pipeline for `/hooks/`, imports, and replays into endpoints. |
| `internal/redact/` | Header, query, JSON, form, and multipart redaction. |
| `internal/replay/` | Replay service, destination guard, and HTTP sender. |
| `internal/storage/` | SQLite access, migrations (`migrations/*.sql`), queries. |
| `internal/settings/` | Runtime settings stored in the database. |
| `internal/retention/` | Periodic cleanup. |
| `internal/events/` | In-memory event broker for live updates. |
| `internal/fixture/` | Fixture file format: parsing, validation, and export. |
| `internal/compare/` | Request comparison used by the compare page. |
| `internal/mockresp/` | Mock response validation and rendering. |
| `internal/auth/`, `internal/ratelimit/`, `internal/ids/`, `internal/validate/`, `internal/logging/`, `internal/version/` | Small supporting packages. |
| `internal/webui/` | Embeds and serves the built dashboard from `static/dist`. |
| `api/` | `openapi.yaml` and `fixture.schema.json`. |
| `web/` | The dashboard: React, TypeScript, Vite, Tailwind CSS. |
| `docs/`, `examples/`, `scripts/` | Documentation, sample requests and fixtures, helper scripts. |

## Prerequisites

- Go 1.26 or newer. If your Go is older but at least 1.21, the `toolchain` line in `go.mod` makes it download the right version automatically.
- Node.js 22.12 or newer, with npm.
- Optional: `make` (Linux, macOS, or Git Bash and WSL on Windows) and Docker.

Relaybox uses a pure-Go SQLite driver, so no C compiler is needed. Builds set `CGO_ENABLED=0`.

## First-time setup

```sh
make deps
```

or without make:

```sh
go mod download
cd web && npm ci
```

## Running in development

```sh
make dev
```

This starts the Go server on <http://127.0.0.1:8787> and the Vite dev server on <http://localhost:5173>. Open the Vite address: the dashboard reloads as you edit, and Vite proxies `/api`, `/hooks`, `/healthz`, and `/readyz` to the Go server. Set `RELAYBOX_DEV_BACKEND` to proxy to a server on another address.

On Windows without make:

```powershell
.\scripts\dev.ps1
```

Or run the two parts in separate terminals:

```sh
go run ./cmd/relaybox serve
cd web && npm run dev
```

Go changes need a restart of the server. Development data goes to `./data/relaybox.db`, which is ignored by git. Use `--database` or `RELAYBOX_DATABASE_PATH` to keep separate databases.

To fill a fresh database with sample requests, create an endpoint and run one of the scripts in [`examples/curl`](../examples/curl).

## Building

```sh
make build
```

This builds the dashboard into `internal/webui/static/dist`, then compiles `bin/relaybox` with the dashboard embedded. Without make:

```sh
cd web && npm run build && cd ..
go build -trimpath -o bin/relaybox ./cmd/relaybox
```

On Windows, name the output `bin/relaybox.exe`.

The version shown by `relaybox version` and in the dashboard is set at build time:

```sh
go build -trimpath -ldflags "-s -w \
  -X github.com/abood-lmao/relaybox/internal/version.Version=v0.1.0 \
  -X github.com/abood-lmao/relaybox/internal/version.Commit=$(git rev-parse --short=12 HEAD) \
  -X github.com/abood-lmao/relaybox/internal/version.Date=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  -o bin/relaybox ./cmd/relaybox
```

If you build the binary without building the dashboard first, it still works, but `/` shows a page explaining how to build the dashboard.

## Tests and checks

These are the commands CI runs.

| Check | Command |
| --- | --- |
| Go formatting | `gofmt -l cmd internal api` (no output expected) |
| Tidy modules | `go mod tidy -diff` (no output expected) |
| Go vet | `go vet ./...` |
| Go tests | `go test ./...` (CI adds `-race -count=1`) |
| Dashboard types | `cd web && npm run typecheck` |
| Dashboard lint | `cd web && npm run lint` |
| Dashboard formatting | `cd web && npm run format:check` |
| Dashboard tests | `cd web && npm test` |
| Dashboard build | `cd web && npm run build` |

`make test` and `make lint` run all of them except `go mod tidy -diff`. The race detector needs cgo and a C compiler, so it is not part of `make test`; run `CGO_ENABLED=1 go test -race ./...` if you have one.

CI also builds the binary, starts it, sends a webhook, checks that a password in the payload was redacted, and builds and probes the container image.

### Writing tests

- **Go:** tests sit next to the code. Storage tests use a temporary SQLite file. API tests go through the real handler with `httptest`, including access control. Replay tests use a local test server with an explicit allowlist entry, so no external network is needed.
- **Dashboard:** Vitest with Testing Library and jsdom. `web/src/test/utils.tsx` provides `mockApi`, which replaces `fetch` with a table of routes and records calls, and `renderApp`, which renders the whole app at a given URL. `web/src/test/fixtures.ts` builds API objects with sensible defaults.

## Changing the database schema

Migrations are SQL files in `internal/storage/migrations`, embedded in the binary. At startup, each one not yet recorded in `schema_migrations` runs in its own transaction, in order of its numeric prefix. A database created by a newer version is refused instead of being modified. To change the schema:

1. Add a new file with the next number, for example `0002_add_labels.sql`. Never edit a migration that has been released.
2. Update the queries in `internal/storage` and add tests.
3. Start the server against a copy of an existing database to check the upgrade.

## Changing the API

1. Update the handler in `internal/api` and its tests.
2. Update `api/openapi.yaml` and, for user-visible changes, [api.md](api.md).
3. Update the types in `web/src/types` and the client in `web/src/lib/api.ts`.

## Screenshots

The images in `docs/screenshots` were captured from a local build with sample data, at a 1440 by 960 viewport. When the interface changes noticeably, capture new ones the same way and use only made-up data.

## Releasing

1. Update `CHANGELOG.md`: move the `Unreleased` entries under the new version and date.
2. Make sure CI passes on `main`.
3. Tag the release: `git tag -a v0.1.0 -m "v0.1.0"` and push the tag.
4. Build binaries with `make build VERSION=v0.1.0` for each target platform (set `GOOS` and `GOARCH`), and the image with `make docker VERSION=v0.1.0`.

There is no automated release workflow yet.
