# Security model

Relaybox receives requests from the internet and shows them to a developer. Everything it captures is treated as untrusted, and the dashboard is protected separately from the capture endpoints. This page explains what is protected, how, and what you are responsible for.

To report a vulnerability, see [SECURITY.md](../SECURITY.md).

## Two surfaces

| Surface | Paths | Who can reach it |
| --- | --- | --- |
| Capture endpoints | `/hooks/{public_id}/...` | Anyone who knows an endpoint URL and can reach the server. |
| Dashboard and API | `/`, `/api/v1/...` | This machine only, unless remote access is configured. |

The root handler dispatches `/hooks/` before any other routing, so the dashboard's protections and the capture path never interfere with each other.

## Captured data is sensitive

Webhooks often carry personal information, such as names, email addresses, postal addresses, and payment details, and secrets, such as API keys, tokens, and signatures. Relaybox stores what it receives, except the values its redaction rules match. Treat the database, fixture exports, and screenshots accordingly, and delete what you no longer need: single requests, a selection, an endpoint's requests, or everything (Settings), or let retention remove old requests automatically.

## Endpoint URLs are bearer capabilities

An endpoint URL contains a random 26-character identifier with 128 bits of randomness. Anyone who has the URL can send requests to it, exactly as with any webhook receiver. Treat endpoint URLs like passwords:

- share them only with the services that need to call them
- use **New URL** on the endpoint page if one leaks; the old URL stops working at once
- disable an endpoint you are not using; it then answers `503` and stores nothing

Endpoint identifiers are shortened to four characters in logs.

## Dashboard access

### Local mode (default)

The server listens on `127.0.0.1`. Even if you change `RELAYBOX_HOST` to listen on all interfaces (to receive webhooks from other machines), the dashboard and API only answer requests that meet all three conditions:

1. **The client address is loopback.** Requests from other machines get `403`.
2. **The `Host` header is a loopback name** (`localhost`, `127.0.0.1`, `[::1]`). This stops DNS rebinding: a malicious web page that points its own domain at `127.0.0.1` still sends its own host name, and is refused.
3. **No proxy headers** such as `Forwarded`, `X-Forwarded-For`, or `CF-Connecting-IP`. A tunnel or reverse proxy on the same machine connects from loopback, but the real client is somewhere else, so these requests are refused.

### Remote mode

Set `RELAYBOX_ALLOW_REMOTE_DASHBOARD=true` and `RELAYBOX_DASHBOARD_TOKEN` (at least 24 characters). The server refuses to start in remote mode without a token. Then:

- Every API route except the health checks and `/api/v1/auth/*` requires the token, as `Authorization: Bearer <token>` or as a session cookie.
- Signing in sets a `relaybox_session` cookie: signed with HMAC, `HttpOnly`, `SameSite=Strict`, `Secure` on HTTPS, valid for 7 days.
- Tokens are compared in constant time, and login attempts are limited to about 10 per minute per client address.
- Replay to URLs is disabled unless `RELAYBOX_ALLOW_REMOTE_REPLAY=true`, so a leaked token does not turn the server into a request relay.
- `RELAYBOX_REPLAY_ALLOWED_HOSTS` is rejected at startup.
- If the token appears anywhere in a captured request (headers, query string, or body), it is replaced with `[REDACTED]`, and the session cookie is removed, so neither ends up in the database.

Put a TLS-terminating reverse proxy in front of a remotely accessible Relaybox. The token travels with every request.

### Cross-site request forgery

State-changing API requests from browsers go through Go's `http.CrossOriginProtection`, which rejects cross-origin requests based on `Sec-Fetch-Site` and `Origin`. Request bodies must be JSON, which plain HTML forms cannot send. Session cookies are `SameSite=Strict`.

## Captured content is untrusted

Headers, bodies, paths, query strings, fixture files, and mock response bodies can all contain hostile content.

- **The dashboard never renders captured content as HTML.** It is always shown as text through React, which escapes it. There is no use of `dangerouslySetInnerHTML`. HTML payloads show as source code, and binary bodies as a hex dump.
- **Strict Content Security Policy** on every dashboard response: `default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; font-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'`. The dashboard HTML has no inline scripts or styles, so no exceptions are needed. Even if an escaping bug slipped through, injected scripts would not run.
- **Other headers:** `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, `Referrer-Policy: no-referrer`, `Cross-Origin-Opener-Policy: same-origin`, `Cross-Origin-Resource-Policy: same-origin`, and a restrictive `Permissions-Policy`.
- **Raw body downloads** are served as attachments with `Content-Security-Policy: sandbox; default-src 'none'` and `nosniff`, so opening one cannot run script on the dashboard origin.
- **Mock responses** share the dashboard's origin, so they get the same sandbox policy, and headers that would affect the origin cannot be configured: `Set-Cookie`, `Set-Cookie2`, `Clear-Site-Data`, `Content-Security-Policy` (and its report-only variant), `Strict-Transport-Security`, `Alt-Svc`, `Service-Worker-Allowed`, and `X-Content-Type-Options`. Framing and transport headers (`Content-Length`, `Content-Encoding`, `Transfer-Encoding`, `Connection`, and others) and the `X-Relaybox-` prefix are reserved too.

## Redaction

Configured headers and fields are replaced with `[REDACTED]` before a request is stored. The originals are never written to the database, the logs, exports, or the live event stream.

- Header names are matched ignoring case. For `Authorization` the scheme is kept (`Bearer [REDACTED]`), and cookie names are kept.
- Field names are matched in query strings, JSON objects at any depth, URL-encoded forms, and multipart form fields, ignoring case, `-`, `_`, `.`, and spaces. `api_key`, `apiKey`, and `API-KEY` are the same entry.
- JSON is rewritten in place: only matched values change, and the rest of the document keeps its exact bytes.
- Each request records which values were redacted, and the dashboard marks them with a hatched `[REDACTED]` label.
- Fixture exports apply the current rules again, so tightening the rules also protects older requests when you share them.

Redaction is a safety net, not a guarantee. A secret in an unexpected place, such as free text in a body, is not detected. Review data before sharing exports or screenshots. The default lists are in [configuration.md](configuration.md#default-redaction-lists).

## Replay and SSRF

Replay never happens automatically. A user picks a request, reviews what will be sent, and confirms. The API requires `"confirmed": true` for URL replays.

Replaying into a Relaybox endpoint never touches the network.

Replaying to a URL goes through these checks:

1. **URL validation before any network activity.** Only `http` and `https`. URLs with credentials are rejected, so they are never stored in the replay history. Host names such as `localhost`, `*.local`, `*.internal`, and `metadata.google.internal` are refused early with a clear message.
2. **Address checks after resolution.** Every address a host name resolves to must be a public unicast address. Loopback, private (RFC 1918 and unique local IPv6), link-local, carrier-grade NAT, documentation, benchmarking, multicast, and reserved ranges are blocked, as are IPv6 forms that embed IPv4 addresses (NAT64, 6to4, Teredo).
3. **No second lookup.** The connection is made to an address that passed the check, never by resolving the name again, so DNS rebinding cannot redirect it. The socket address is checked once more right before connecting.
4. **Cloud metadata addresses are always blocked**, even when allowlisted: `169.254.169.254`, `169.254.170.2`, `100.100.100.200`, and `fd00:ec2::254`.
5. **No redirects are followed**, no proxy from the environment is used, and TLS certificates are always verified.
6. **Bounded:** 30-second timeout, response bodies read up to 1 MiB, request bodies up to 10 MiB, and at most four replays at a time.

Hop-by-hop and framing headers (`Host`, `Content-Length`, `Transfer-Encoding`, `Connection`, and similar) are never forwarded. Redacted headers are unselected by default, because their real values were never stored.

### Replaying to local services during development

`RELAYBOX_REPLAY_ALLOWED_HOSTS=localhost:3000` lets replay reach the listed `host:port` pairs even though they are loopback or private. It is off by default, applies only to the exact entries listed, never unlocks metadata addresses, and is rejected while the remote dashboard is enabled. You can also replay into a Relaybox endpoint, which needs no network access at all.

## Data storage

- Requests are stored in a single SQLite file, by default `./data/relaybox.db`. The directory is created with mode `0700`. The container image stores it in the `/data` volume and runs as an unprivileged user.
- Retention (7 days and 10,000 requests by default) limits how much accumulates. Deleting an endpoint deletes its requests and replay history.
- Bulk deletion always requires an explicit scope (`all`, one endpoint, or a list of IDs). Filters never widen or narrow a delete.
- Relaybox does not encrypt the database. Protect the file like any other local data, and use disk encryption on machines that hold sensitive payloads.
- Logs contain no bodies, header values, or query strings.
- Relaybox sends no telemetry and makes no outbound connections except the replays you send.

## Input limits

| Limit | Value |
| --- | --- |
| Request body stored | 1 MiB by default, configurable up to 64 MiB |
| Captures per endpoint | 50 per second, burst 100, by default |
| Search text | 200 characters |
| Page size | 200 requests |
| Mock response body | 64 KiB, 32 headers, 10 s delay |
| Fixture file | 32 MiB, 1000 requests |
| Bulk delete by ID | 1000 IDs |
| Live update connections | limited; extra connections get `503` |

All database queries are parameterized. Sorting and filtering use fixed allowlists, never user-supplied SQL.

## Your responsibilities

- Keep the default local mode unless you need remote access, and use a long random token and TLS when you do.
- Treat endpoint URLs as secrets and rotate leaked ones.
- Review the redaction lists for the services you test, and add their secret-bearing headers and fields.
- Do not point production webhooks at a Relaybox instance unless you are prepared to store their payloads.
- Keep Relaybox updated.
