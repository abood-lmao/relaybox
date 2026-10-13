# Contributing to Relaybox

Thanks for your interest. Relaybox is a small, self-hosted tool, and contributions that keep it simple, safe, and well tested are very welcome: bug reports, fixes, documentation, tests, and focused features.

Before starting on a larger change, open an issue to discuss it. The non-goals in the README (multi-tenant SaaS features, billing, external databases, queues) are deliberate.

## Setting up

You need:

- Go 1.26 or newer (the `toolchain` line in `go.mod` downloads the exact version if needed)
- Node.js 22.12 or newer, with npm
- Git
- Optionally `make`. On Windows, `scripts/dev.ps1` and the commands below do the same things.

```sh
git clone https://github.com/abood-lmao/relaybox.git
cd relaybox
make deps        # go mod download, npm ci in web/
make dev         # API on :8787, dashboard with hot reload on :5173
```

Open <http://localhost:5173>. Vite proxies `/api` and `/hooks` to the Go server.

Without make:

```sh
go run ./cmd/relaybox serve           # terminal 1
cd web && npm ci && npm run dev       # terminal 2
```

On Windows, `.\scripts\dev.ps1` starts both.

## Project structure

| Path | Contents |
| --- | --- |
| `cmd/relaybox/` | Entry point of the binary |
| `internal/` | The Go server: capture, redaction, storage, API, replay, and supporting packages |
| `web/` | The dashboard (React, TypeScript, Vite, Tailwind CSS) |
| `api/` | OpenAPI document and the fixture JSON Schema |
| `docs/` | User and developer documentation |
| `examples/` | Sample requests and fixture files |
| `scripts/` | Helper scripts |

[docs/development.md](docs/development.md) describes each package, the build, and the test setup in more detail.

## Checks to run before a pull request

| Check | With make | Without make |
| --- | --- | --- |
| Go tests | `make test-go` | `go test ./...` |
| Frontend tests | `make test-web` | `cd web && npm test` |
| Go format and vet | `make lint-go` | `gofmt -l cmd internal api` (no output expected), `go vet ./...` |
| Frontend types, lint, format | `make lint-web` | `cd web && npm run typecheck && npm run lint && npm run format:check` |
| Full build | `make build` | `cd web && npm run build`, then `go build -o bin/relaybox ./cmd/relaybox` |

CI runs the same commands, plus `go test -race`, `go mod tidy -diff`, a smoke test of the built binary, and a container build.

## Guidelines

- **Keep the security model intact.** Captured requests are untrusted input. They are never rendered as HTML, never executed, and never sent anywhere without an explicit user action. Changes to capture, redaction, replay, authentication, or response headers need tests and a note in the pull request.
- **Write tests for behavior changes.** Go tests sit next to the code (`*_test.go`). Frontend tests use Vitest and Testing Library (`*.test.ts(x)`), with `fetch` mocked through `web/src/test/utils.tsx`.
- **Do not disable or skip failing tests** to get a green build. Fix the cause, or explain in the pull request why the test was wrong.
- **Keep dependencies few.** The Go server uses the standard library plus a pure-Go SQLite driver. Discuss new dependencies in an issue first.
- **Update the docs** when you change behavior, configuration, or the API. The OpenAPI document lives in `api/openapi.yaml`.
- **Match the surrounding style.** `gofmt` for Go, Prettier for the frontend. User-facing text is plain and specific: say what happened and what to do next.
- **Never commit secrets or real payloads.** Use made-up data in tests, fixtures, and screenshots.

## Commits and pull requests

- Keep pull requests focused. Several small ones are easier to review than one large one.
- Write commit messages in the imperative mood ("Add header filter to request list").
- Fill in the pull request template, including how you tested the change.
- Add a line to the `Unreleased` section of [CHANGELOG.md](CHANGELOG.md) for user-visible changes.

## Code review

- Every pull request is reviewed by a maintainer before it is merged, and CI must pass.
- Reviewers look at whether the change is tested, keeps the security model intact, handles errors with clear messages, and updates the docs and `api/openapi.yaml` where needed.
- Changes to capture, redaction, replay, authentication, or response headers get a closer look and may take longer.
- Respond to feedback with new commits rather than force-pushing, so reviewers can see what changed. Maintainers may squash commits when merging.
- Disagreement is fine. Keep it about the code, and assume good intent.

## Reporting bugs and security problems

Use the issue templates for bugs and feature requests. Report security problems privately as described in [SECURITY.md](SECURITY.md), not in a public issue.

## Code of conduct

Everyone taking part is expected to follow the [code of conduct](CODE_OF_CONDUCT.md).

## License

By contributing, you agree that your contributions are licensed under the [MIT License](LICENSE).
