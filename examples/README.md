# Examples

Sample requests and fixture files for trying out Relaybox. All data in this directory is made up.

## Send sample webhooks

Create an endpoint in the dashboard and copy its URL, or create one from the command line:

```sh
curl -s -X POST http://127.0.0.1:8787/api/v1/endpoints \
  -H 'Content-Type: application/json' \
  -d '{"name": "Samples"}'
```

The response contains `url`. Then send the samples:

```sh
./examples/curl/send-samples.sh http://127.0.0.1:8787/hooks/<public_id>
```

On Windows:

```powershell
.\examples\curl\send-samples.ps1 http://127.0.0.1:8787/hooks/<public_id>
```

Both scripts send the same eight requests: a JSON payment event, a push event with signature headers, a URL-encoded form, a multipart upload, XML, plain text, a GET with a query string, and a DELETE. Several of them contain values that the default redaction rules replace before storage, such as `client_secret`, `password`, `token`, `api_key`, `Authorization`, and `X-Api-Key`. Open a request in the dashboard to see the `[REDACTED]` marks.

The payloads are in [`curl/payloads`](curl/payloads), if you want to send them by hand:

```sh
curl -X POST "http://127.0.0.1:8787/hooks/<public_id>/payments" \
  -H 'Content-Type: application/json' \
  --data-binary @examples/curl/payloads/payment-succeeded.json
```

## Import fixtures

[`fixtures`](fixtures) has ready-made fixture files:

| File | Contents |
| --- | --- |
| `payment-events.json` | Four payment events, including a failed delivery and its retry (compare them to see what changed) and a value that was already redacted. |
| `push-events.json` | A ping and a push event from a source code host, with signature headers. |
| `mixed-content.json` | A form, a multipart upload, a binary PNG (stored as base64), XML, and a GET with a query string. |

Import one from the endpoint page (**Import fixture**), or with the API. The endpoint ID is the number in the endpoint page's address:

```sh
curl -s -X POST "http://127.0.0.1:8787/api/v1/fixtures/import?endpoint_id=1" \
  -H 'Content-Type: application/json' \
  --data-binary @examples/fixtures/payment-events.json
```

Imported requests are stored with the source `import` and the time of import. Nothing in a fixture is executed or sent anywhere.

## Export fixtures

```sh
# The 100 most recent requests of endpoint 1
curl -s -X POST http://127.0.0.1:8787/api/v1/fixtures/export \
  -H 'Content-Type: application/json' \
  -d '{"endpoint_id": 1, "limit": 100, "include_bodies": true}' \
  -o relaybox-fixture.json

# The same without a running server
relaybox export --endpoint 1 --out relaybox-fixture.json
```

Exports apply the current redaction rules again, but review a file before sharing it: a secret in an unexpected place is not detected.

## Query captured requests

```sh
# The ten most recent POST requests that mention ord_1042
curl -s 'http://127.0.0.1:8787/api/v1/requests?method=POST&q=ord_1042&limit=10'

# Follow new requests as they arrive
curl -N http://127.0.0.1:8787/api/v1/events
```

See [docs/api.md](../docs/api.md) for the full API.
