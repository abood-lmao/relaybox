# Changelog

All notable changes to this project are documented here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project uses [Semantic Versioning](https://semver.org/).

## [Unreleased]

First version, not yet released.

### Added

- Capture endpoints at `/hooks/{id}` for GET, HEAD, POST, PUT, PATCH, DELETE, and OPTIONS, including any sub-path and query string.
- Configurable mock responses per endpoint: status, content type, headers, body, and delay, with a preview. Reserved headers such as `Set-Cookie` and `Content-Security-Policy` cannot be set.
- Per-endpoint body size limits. Larger bodies are stored truncated and the sender gets `413`.
- Redaction of sensitive headers, query parameters, and JSON and form fields before storage, with lists that can be edited in the dashboard.
- Request list with server-side pagination, filters for endpoint, method, status, source, and date, sorting, and full-text search over paths, headers, and bodies.
- Request detail view with headers, query, body (JSON tree, formatted, raw, form fields, multipart parts, hex dump), the response that was sent, and metadata.
- Replay to an endpoint or to a URL, with editable method, headers, and body, a confirmation step, SSRF protection, and replay history.
- Comparison of two requests or two responses, with JSON differences by path and line differences for text.
- Fixture export and import (JSON), from the dashboard, the API, and `relaybox export`.
- Live updates through Server-Sent Events.
- Overview page with counts, a 24-hour chart, status distribution, and most active endpoints.
- Runtime settings for retention, stored request limit, default body limit, and redaction lists, plus automatic cleanup.
- Optional remote dashboard mode with token sign-in, CSRF protection, and replay restrictions.
- Command-line tools: `serve`, `cleanup`, `export`, `doctor`, and `version`.
- REST API described by an OpenAPI 3.1 document at `/api/v1/openapi.yaml`.
- Single binary with the dashboard embedded, a Dockerfile, and a Compose file.
- Documentation for configuration, the API, the architecture, the security model, and development, plus sample request scripts and fixture files in `examples/`.

[Unreleased]: https://github.com/abood-lmao/relaybox/commits/main
