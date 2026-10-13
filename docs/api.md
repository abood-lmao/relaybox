# API

The dashboard is built entirely on a REST API, and everything it does is available to scripts. The complete reference, with every field and response, is the OpenAPI 3.1 document:

- in the repository: [`api/openapi.yaml`](../api/openapi.yaml)
- on a running server: `http://127.0.0.1:8787/api/v1/openapi.yaml`

This page covers the conventions and the most common calls. Examples assume the default local address.

## Access

| Mode | How to call the API |
| --- | --- |
| Local (default) | From the same machine, at `127.0.0.1`, `localhost`, or `[::1]`. No credentials. Requests with proxy headers such as `X-Forwarded-For` are refused. |
| Token configured | Add `Authorization: Bearer <RELAYBOX_DASHBOARD_TOKEN>`, or sign in with `POST /api/v1/auth/login` and send the `relaybox_session` cookie. |

Health checks (`/healthz`, `/readyz`) and capture endpoints (`/hooks/...`) need no credentials.

State-changing requests sent by a browser from another origin are rejected (`403 cross_origin_rejected`). Command-line tools such as curl are not affected.

## Conventions

- Request and response bodies are JSON (`Content-Type: application/json`).
- Timestamps are RFC 3339 strings in UTC.
- Header lists are arrays of `[name, value]` pairs, so repeated headers and their order are preserved.
- Bodies are objects with `encoding` (`utf8` or `base64`), `data`, `size` (bytes received), `stored_bytes`, `truncated`, `kind` (`json`, `form`, `multipart`, `text`, `binary`, or `empty`), and `content_type`.

### Errors

Every error has the same shape:

```json
{
  "error": {
    "code": "validation_failed",
    "message": "Some fields are invalid.",
    "fields": { "name": "Another endpoint already uses this name." }
  }
}
```

`fields` appears on `422` validation errors. Invalid fixture imports return `422` with a `details` list instead. Error messages are written for people and never include stack traces or internal paths.

## Endpoints

```sh
# Create
curl -s -X POST http://127.0.0.1:8787/api/v1/endpoints \
  -H 'Content-Type: application/json' \
  -d '{"name": "Payments staging"}'
```

The response includes `id`, `public_id`, and `url`, the address to give to the sending service.

| Method and path | Purpose |
| --- | --- |
| `GET /api/v1/endpoints` | List endpoints with request counts. |
| `POST /api/v1/endpoints` | Create. Body: `name` (required, at most 80 characters, unique ignoring case), optional `enabled`, `mock_response`, `max_body_bytes`. |
| `GET /api/v1/endpoints/{id}` | Get one. |
| `PATCH /api/v1/endpoints/{id}` | Change any of `name`, `enabled`, `mock_response`, `max_body_bytes` (`null` uses the default). |
| `DELETE /api/v1/endpoints/{id}` | Delete the endpoint and its requests. Returns `deleted_requests`. |
| `POST /api/v1/endpoints/{id}/rotate-url` | Issue a new public identifier. The old URL stops working immediately. |
| `POST /api/v1/mock-preview` | Show the response a mock configuration would produce, without saving it. Body: `mock_response`, optional `method`. |

A mock response:

```json
{
  "status": 202,
  "content_type": "application/json",
  "headers": { "X-Request-Source": "relaybox" },
  "body": "{\"accepted\": true}",
  "delay_ms": 120
}
```

Limits: status 200 to 599, body up to 64 KiB, up to 32 headers, delay up to 10000 ms. Headers that Relaybox controls cannot be set, including `Set-Cookie`, `Content-Security-Policy`, `Strict-Transport-Security`, `Content-Length`, `Content-Type` (use `content_type`), and anything starting with `X-Relaybox-`. When `content_type` is JSON, the body must be valid JSON.

## Capture

Send anything to an endpoint URL:

```sh
curl -X POST http://127.0.0.1:8787/hooks/<public_id>/orders/created?attempt=1 \
  -H 'Content-Type: application/json' \
  -d '{"order_id": "ord_123"}'
```

- Methods: GET, HEAD, POST, PUT, PATCH, DELETE, OPTIONS. Others get `405`.
- Any sub-path and query string after the identifier is recorded.
- Answers: the endpoint's mock response; `413` when the body exceeds the limit (the request is still stored, truncated); `429` with `Retry-After` when the rate limit is exceeded; `503` when the endpoint is disabled or the request could not be stored; `404` for unknown identifiers.

## Requests

```sh
# The 20 most recent failed POSTs to endpoint 3 that mention "ord_123"
curl -s 'http://127.0.0.1:8787/api/v1/requests?endpoint_id=3&method=POST&status=5xx&q=ord_123&limit=20'
```

`GET /api/v1/requests` returns `{ "items": [...], "total": N, "limit": L, "offset": O }`. Filters:

| Parameter | Values |
| --- | --- |
| `endpoint_id` | Endpoint ID. |
| `method` | One or more methods, comma-separated or repeated. |
| `status` | Codes (`404`) or classes (`4xx`), comma-separated or repeated. |
| `source` | `capture`, `replay`, or `import`. |
| `from`, `to` | RFC 3339 timestamps or `YYYY-MM-DD` dates. A date in `to` covers the whole day. |
| `q` | Text searched in the path, query, headers, content type, and body. At most 200 characters. |
| `sort` | `received_at` (default), `duration`, `size`, `status`, or `method`. |
| `order` | `desc` (default) or `asc`. |
| `limit`, `offset` | Page size 1 to 200 (default 50), and offset. |

| Method and path | Purpose |
| --- | --- |
| `GET /api/v1/requests/{id}` | Full request: headers, query, body, the response that was sent, redactions, and replays made from it. |
| `GET /api/v1/requests/{id}/body?part=request\|response` | Download the raw body, served as an attachment with `Content-Security-Policy: sandbox`. |
| `DELETE /api/v1/requests/{id}` | Delete one request. |
| `DELETE /api/v1/requests?scope=...` | Bulk delete. `scope` is required: `all`, `endpoint` (with `endpoint_id`), or `ids` (with `ids=1,2,3`, at most 1000). Filters do not apply here, so the scope is always explicit. |
| `POST /api/v1/requests/compare` | Compare two requests. Body: `left_id`, `right_id`, `part` (`request` or `response`). |

## Replay

```sh
# See what would be sent
curl -s http://127.0.0.1:8787/api/v1/requests/42/replay

# Send it to a URL (confirmed must be true)
curl -s -X POST http://127.0.0.1:8787/api/v1/requests/42/replay \
  -H 'Content-Type: application/json' \
  -d '{"target": "url", "url": "https://example.com/webhooks", "confirmed": true}'

# Replay into another endpoint (stays inside Relaybox)
curl -s -X POST http://127.0.0.1:8787/api/v1/requests/42/replay \
  -H 'Content-Type: application/json' \
  -d '{"target": "endpoint", "endpoint_id": 2}'
```

Optional fields: `method`, `headers` (pairs; replaces the stored headers), and `body` (`{ "encoding": "utf8", "data": "..." }`, at most 10 MiB). Without `headers`, Relaybox sends the headers that the replay preview (`GET .../replay`) selects by default: everything except framing and hop-by-hop headers such as `Host` and `Content-Length`, headers that were redacted, and headers added by proxies such as `X-Forwarded-For`. Without `body`, the stored body is sent.

The response has the recorded `attempt`, the destination's `response` (status, headers, and up to 1 MiB of body), and `created_request_id` for replays into an endpoint.

| Status and code | Meaning |
| --- | --- |
| `409 confirmation_required` | A URL replay without `"confirmed": true`. |
| `409 body_truncated` | The stored body is incomplete. Send an explicit `body`. |
| `409 endpoint_disabled` | The target endpoint is disabled. |
| `403 replay_disabled` | URL replay is off because the remote dashboard is on without `RELAYBOX_ALLOW_REMOTE_REPLAY=true`. |
| `422 validation_failed` | The URL is invalid or names a blocked address or host. `fields.url` explains why. |
| `429 busy` | Four replays are already running. |

When a host name only turns out to resolve to a blocked address at connection time, the call succeeds but the attempt is recorded as failed, and `attempt.summary` gives the reason.

`GET /api/v1/requests/{id}/replays` lists earlier attempts.

## Fixtures

A fixture is a JSON file with captured requests that can be imported into any endpoint. The format is described by [`api/fixture.schema.json`](../api/fixture.schema.json).

```json
{
  "format": "relaybox.fixture",
  "version": 1,
  "exported_at": "2026-10-10T12:00:00Z",
  "requests": [
    {
      "method": "POST",
      "path": "/events",
      "query": "attempt=1",
      "headers": [["Content-Type", "application/json"]],
      "body": { "encoding": "utf8", "data": "{\"type\":\"invoice.paid\"}" }
    }
  ]
}
```

| Method and path | Purpose |
| --- | --- |
| `POST /api/v1/fixtures/export` | Body: `endpoint_id` or `request_ids`, optional `limit` (at most 1000) and `include_bodies`. Returns the file as an attachment. |
| `GET /api/v1/requests/{id}/export` | Export one request. `?include_body=false` leaves the body out. |
| `POST /api/v1/fixtures/import?endpoint_id=N` | Import a fixture file (the request body) into an endpoint. Returns `imported` and `request_ids`. |

Exports apply the current redaction rules again and leave out endpoint URLs, internal IDs, and sender addresses. Imports are validated strictly (unknown fields, methods, header syntax, body encoding, sizes) and stored as requests with `source` set to `import`. Nothing in a fixture is executed or sent anywhere, and the endpoint's current redaction rules and body limit apply. Imported requests get the time of import; `received_at` and `endpoint` in the file are informational.

The same export is available offline with `relaybox export`.

## Settings and maintenance

| Method and path | Purpose |
| --- | --- |
| `GET /api/v1/settings` | Runtime settings, their defaults, which ones are overridden, limits, and read-only server information. |
| `PATCH /api/v1/settings` | Change `retention_days`, `max_stored_requests`, `default_max_body_bytes`, `redact_headers`, or `redact_fields`. `reset` takes a list of keys to return to their defaults. |
| `POST /api/v1/maintenance/cleanup` | Apply the retention policy now. Returns `expired` and `trimmed` counts. |
| `GET /api/v1/stats` | Counts, hourly volume for the last 24 hours, status classes, most active endpoints, and recent requests. |
| `GET /api/v1/meta` | Version, limits, and feature flags for the dashboard. |

## Live updates

`GET /api/v1/events` is a Server-Sent Events stream. Event types:

| Event | Data |
| --- | --- |
| `request.created` | `id`, `endpoint_id`, `method`, `status`, `source`, `received_at` |
| `requests.imported` | `endpoint_id`, `count` |
| `requests.deleted` | `count`, and `ids` or `reason` (`retention`, `endpoint_deleted`) when known |
| `endpoints.changed` | `id`, `action` (`created`, `updated`, or `deleted`) |
| `settings.updated` | empty object |
| `resync` | sent after a reconnect when events were missed; refetch everything |

Events never contain headers or bodies, and they are not stored: a client that was disconnected for long gets `resync` and should reload its data from the API. A comment line is sent every 25 seconds to keep proxies from closing the connection.

```sh
curl -N http://127.0.0.1:8787/api/v1/events
```

## Authentication routes

| Method and path | Purpose |
| --- | --- |
| `GET /api/v1/auth/session` | `auth_required` and `authenticated`. |
| `POST /api/v1/auth/login` | Body: `token`. Sets an `HttpOnly`, `SameSite=Strict` session cookie valid for 7 days. At most 10 attempts per minute per client address. |
| `POST /api/v1/auth/logout` | Clears the cookie. |
