# Configuration

Relaybox is configured with environment variables, a few command-line flags, and runtime settings that you change in the dashboard.

- **Environment variables** (`RELAYBOX_*`) are read at startup. Changing them requires a restart.
- **Flags** on `relaybox serve` override the matching variables.
- **Runtime settings** (retention, stored request limit, default body limit, redaction lists) are saved in the database and apply immediately. The environment variables for those settings only provide the defaults.

Invalid values stop the server with a message that names every problem. Unknown `RELAYBOX_*` variables are reported as warnings, so a typo does not go unnoticed.

The binary does not read `.env` files. `.env` is only used by Docker Compose.

## Environment variables

### Network

| Variable | Default | Description |
| --- | --- | --- |
| `RELAYBOX_HOST` | `127.0.0.1` | Address to listen on. Use `0.0.0.0` to accept webhooks from other machines. The dashboard stays local unless remote access is enabled (see below). |
| `RELAYBOX_PORT` | `8787` | Port, 1 to 65535. |
| `RELAYBOX_PUBLIC_BASE_URL` | `http://<host>:<port>` | Base URL used to build endpoint URLs in the dashboard, for example the address of a tunnel or reverse proxy. Must be `http` or `https`, with no credentials, query, or fragment. When the host is `0.0.0.0` the default uses `127.0.0.1`. |

### Storage and retention

| Variable | Default | Description |
| --- | --- | --- |
| `RELAYBOX_DATABASE_PATH` | `./data/relaybox.db` | SQLite database file. The directory is created if needed. |
| `RELAYBOX_MAX_BODY_BYTES` | `1048576` (1 MiB) | Largest request body stored per request, from 1024 to 67108864 (64 MiB). This is also the highest value the dashboard allows for the default and per-endpoint limits. Larger bodies are stored truncated and the sender receives `413`. |
| `RELAYBOX_RETENTION_DAYS` | `7` | Default number of days to keep requests, 0 to 3650. 0 keeps them forever. |
| `RELAYBOX_MAX_STORED_REQUESTS` | `10000` | Default limit on stored requests, 0 to 10000000. The oldest requests are removed first. 0 means no limit. |

### Logging

| Variable | Default | Description |
| --- | --- | --- |
| `RELAYBOX_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, or `error`. At `debug`, every HTTP request is logged (method, path, status, size, duration). |
| `RELAYBOX_LOG_FORMAT` | `text` | `text` or `json`. |

Logs never contain request bodies, header values, or query strings, and endpoint identifiers are shortened to their first four characters.

### Capture rate limit

| Variable | Default | Description |
| --- | --- | --- |
| `RELAYBOX_RATE_LIMIT_RPS` | `50` | Requests per second accepted per endpoint, 0 to 100000. 0 disables the limit. Extra requests get `429` with `Retry-After` and are not stored. |
| `RELAYBOX_RATE_LIMIT_BURST` | `100` | Short bursts allowed above the rate, 1 to 1000000. |

### Remote access and replay

| Variable | Default | Description |
| --- | --- | --- |
| `RELAYBOX_ALLOW_REMOTE_DASHBOARD` | `false` | Let the dashboard and API accept connections from other machines. Requires `RELAYBOX_DASHBOARD_TOKEN`. |
| `RELAYBOX_DASHBOARD_TOKEN` | not set | Access token for the dashboard and API, at least 24 characters, no whitespace. When set, the dashboard asks for it even on a local connection. Generate one with `openssl rand -base64 32`. |
| `RELAYBOX_ALLOW_REMOTE_REPLAY` | `false` | Allow replay to URLs while the remote dashboard is on. Without it, a remotely accessible dashboard can only replay into Relaybox endpoints. Has no effect in local mode, where URL replay is always available. |
| `RELAYBOX_REPLAY_ALLOWED_HOSTS` | not set | **Development only.** Comma-separated `host:port` entries that replay may reach even though they resolve to private or loopback addresses, for example `localhost:3000,127.0.0.1:8080`. Cloud metadata addresses are refused even when listed. Rejected at startup while `RELAYBOX_ALLOW_REMOTE_DASHBOARD=true`. |

Booleans accept `true`, `false`, `1`, `0`, `yes`, `no`, `on`, and `off`.

## Command-line flags

`relaybox serve`:

| Flag | Variable |
| --- | --- |
| `--host ADDRESS` | `RELAYBOX_HOST` |
| `--port NUMBER` | `RELAYBOX_PORT` |
| `--database PATH` | `RELAYBOX_DATABASE_PATH` |
| `--public-url URL` | `RELAYBOX_PUBLIC_BASE_URL` |

Other commands:

| Command | Purpose | Flags |
| --- | --- | --- |
| `relaybox cleanup` | Apply the retention policy now. A running server does this every 10 minutes. | `--dry-run`, `--database PATH` |
| `relaybox export` | Write recent requests to a fixture file. | `--endpoint NAME\|ID`, `--limit N` (default 100, at most 1000), `--out FILE`, `--no-bodies`, `--database PATH` |
| `relaybox doctor` | Check the configuration, the database, the dashboard files, and the port, and summarize the security settings. | `--host`, `--port`, `--database` |
| `relaybox version` | Print the version, commit, build date, and Go version. | |
| `relaybox help [command]` | Show help. | |

Exit codes: `0` success, `1` failure, `2` invalid usage or configuration.

## Runtime settings

Open **Settings** in the dashboard, or use `GET` and `PATCH /api/v1/settings`.

| Setting | Seeded from | Notes |
| --- | --- | --- |
| Retention days | `RELAYBOX_RETENTION_DAYS` | Applied by the cleanup that runs every 10 minutes, or right away with **Run cleanup now**. |
| Maximum stored requests | `RELAYBOX_MAX_STORED_REQUESTS` | Also enforced shortly after new requests arrive. |
| Default body limit | `RELAYBOX_MAX_BODY_BYTES` | Used by endpoints without their own limit. Cannot exceed `RELAYBOX_MAX_BODY_BYTES`. |
| Redacted header names | built-in list | Matched ignoring case. |
| Redacted field names | built-in list | Matched in query parameters, JSON keys, and form fields, ignoring case, `-`, `_`, `.`, and spaces. |

A saved value overrides the environment default until you reset it. Each setting has a **Reset** link, and the redaction lists have **Restore default lists**.

### Default redaction lists

Headers: `Authorization`, `Proxy-Authorization`, `Cookie`, `Set-Cookie`, `X-Api-Key`, `Api-Key`, `X-Auth-Token`, `X-Access-Token`, `X-Amz-Security-Token`, `X-Goog-Api-Key`, `X-Gitlab-Token`, `X-Hook-Secret`, `X-Webhook-Secret`, `X-Telegram-Bot-Api-Secret-Token`.

For `Authorization` and `Proxy-Authorization` the scheme is kept (`Bearer [REDACTED]`), and for cookies the cookie names are kept.

Fields: `password`, `passwd`, `secret`, `client_secret`, `api_key`, `access_token`, `refresh_token`, `id_token`, `token`, `private_key`, `authorization`, `card_number`, `cvc`, `cvv`.

Signature headers such as `Stripe-Signature` or `X-Hub-Signature-256` are not redacted by default: they are not secrets on their own, and you often need them to debug signature checks. Add them to the list if you prefer.

## Examples

Capture webhooks from other machines while keeping the dashboard local:

```sh
RELAYBOX_HOST=0.0.0.0 relaybox serve
```

Use a tunnel address in endpoint URLs:

```sh
relaybox serve --public-url https://example-tunnel.example.com
```

Requests that arrive through a tunnel are captured normally. The dashboard itself refuses requests that carry proxy headers in local mode, so open it at `http://127.0.0.1:8787`, not through the tunnel.

Run on a server with a remotely accessible dashboard, behind a TLS reverse proxy:

```sh
RELAYBOX_HOST=127.0.0.1 \
RELAYBOX_ALLOW_REMOTE_DASHBOARD=true \
RELAYBOX_DASHBOARD_TOKEN="$(openssl rand -base64 32)" \
RELAYBOX_PUBLIC_BASE_URL=https://relaybox.example.com \
relaybox serve
```

Replay into an app running on your machine during development:

```sh
RELAYBOX_REPLAY_ALLOWED_HOSTS=localhost:3000 relaybox serve
```

## Docker

The image sets `RELAYBOX_HOST=0.0.0.0`, `RELAYBOX_PORT=8787`, and `RELAYBOX_DATABASE_PATH=/data/relaybox.db`, and stores data in the `/data` volume.

Inside a container, connections from your browser arrive from the Docker network rather than from loopback, so local mode would refuse them. `docker-compose.yml` therefore runs in remote-dashboard mode with a token, and publishes the port on `127.0.0.1` only. Because of that, `RELAYBOX_REPLAY_ALLOWED_HOSTS` cannot be used with Compose, and URL replay needs `RELAYBOX_ALLOW_REMOTE_REPLAY=true` in `.env`.
