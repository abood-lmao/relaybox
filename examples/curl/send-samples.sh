#!/usr/bin/env bash
# Sends a set of sample webhooks to a Relaybox endpoint: JSON, form, multipart,
# XML, plain text, a GET with a query string, and a DELETE. All data is made up,
# and some of it is meant to be redacted.
#
# Usage: ./send-samples.sh http://127.0.0.1:8787/hooks/<public_id>
set -euo pipefail

if [ $# -ne 1 ]; then
  cat >&2 <<'EOF'
Usage: send-samples.sh <endpoint URL>

Create an endpoint in the dashboard and copy its URL, or create one with:
  curl -s -X POST http://127.0.0.1:8787/api/v1/endpoints \
    -H 'Content-Type: application/json' -d '{"name": "Samples"}'
EOF
  exit 2
fi

url="${1%/}"
cd "$(dirname "$0")/payloads"

send() {
  local label="$1"
  shift
  printf '%-32s' "$label"
  curl -sS -o /dev/null -w '%{http_code}\n' "$@"
}

send "JSON payment event" -X POST "$url/payments?attempt=1" \
  -H 'Content-Type: application/json' \
  -H 'User-Agent: ExamplePayments/2.1' \
  -H 'Webhook-Id: evt_example_0001' \
  -H 'Webhook-Signature: t=1760090000,v1=3f1c8a0d9b7e6c5a4f3e2d1c0b9a8f7e6d5c4b3a2f1e0d9c8b7a6f5e4d3c2b1a' \
  --data-binary @payment-succeeded.json

send "Push event" -X POST "$url/git" \
  -H 'Content-Type: application/json' \
  -H 'User-Agent: ExampleHookshot/1.0' \
  -H 'X-GitHub-Event: push' \
  -H 'X-GitHub-Delivery: 72d3162e-cc78-11e3-81ab-4c9367dc0958' \
  -H 'X-Hub-Signature-256: sha256=7d38cdd689735b008b3c702edd92eea23791c5f6a3c4c1f2b5d8a9e0f1b2c3d4' \
  --data-binary @push.json

send "Form status callback" -X POST "$url/sms/status" \
  -H 'Content-Type: application/x-www-form-urlencoded' \
  --data-binary @status-callback.txt

send "Multipart upload" -X POST "$url/uploads" \
  -F 'order_id=ord_1042' \
  -F 'password=example-password' \
  -F 'receipt=@receipt.txt;type=text/plain'

send "XML notification" -X POST "$url/invoices" \
  -H 'Content-Type: application/xml' \
  --data-binary @invoice.xml

send "Plain text with a bearer token" -X PUT "$url/orders/ord_1042/status" \
  -H 'Content-Type: text/plain' \
  -H 'Authorization: Bearer example-token-abc123' \
  --data-binary 'shipped'

send "GET with an API key" "$url/ping?event=ping&api_key=example-key-123&page=2"

send "DELETE without a body" -X DELETE "$url/subscriptions/sub_example_9" \
  -H 'X-Api-Key: example-key-456'

echo "Done. Open the dashboard to inspect the requests."
