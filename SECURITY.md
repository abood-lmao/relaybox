# Security policy

## Supported versions

Relaybox has not had a stable release yet. Until 1.0, security fixes go into the latest release and the `main` branch only.

## Reporting a vulnerability

Please do not open a public issue for security problems.

Report it privately to the maintainer, [@abood-lmao](https://github.com/abood-lmao), through GitHub private vulnerability reporting:

<https://github.com/abood-lmao/relaybox/security/advisories/new>

That form goes only to the repository owner. It stays off the public issue list.

Please include:

- the Relaybox version (`relaybox version`) and how you run it
- the configuration that matters (remote dashboard on or off, allowed replay hosts), without real tokens
- steps to reproduce, ideally a curl command or a small fixture file
- what an attacker could achieve

We aim to acknowledge reports within a few days, keep you informed while a fix is prepared, and credit you in the release notes unless you prefer otherwise.

## Keeping secrets out of public issues

Bug reports often include captured requests, configuration, or logs, and captured webhooks can contain personal information and secrets. Before posting anything in a public issue or discussion:

- Replace dashboard tokens, API keys, signatures, cookies, and passwords with made-up values.
- Do not post real endpoint URLs. Anyone who has one can send requests to it.
- Remove personal data such as names, email addresses, phone numbers, and payment details from payloads and screenshots.
- Share fixtures exported without bodies (`relaybox export --no-bodies`), or read the file first. Redaction only covers the headers and fields it is configured for.

If you post a secret by mistake, revoke or rotate it where it was issued (use **New URL** for an endpoint URL). Editing the comment is not enough, because earlier versions stay visible in its edit history.

## Security model

[docs/security.md](docs/security.md) describes the security model in detail. In short:

- **Endpoint URLs are bearer capabilities.** Anyone who has an endpoint URL can send requests to it. This is how webhooks work and is not a vulnerability. Rotate the URL in the dashboard if it leaks.
- **The dashboard and API are local by default.** They accept only loopback clients that use a loopback host name and come without proxy headers. Remote access needs `RELAYBOX_ALLOW_REMOTE_DASHBOARD=true` and a token of at least 24 characters.
- **Captured content is untrusted.** It is shown as text, never rendered as HTML or run as script, under a strict Content Security Policy.
- **Sensitive values are redacted before storage.** Configured headers and fields are replaced with `[REDACTED]` before a request is written to the database.
- **Replay only happens on request.** Replaying to a URL needs explicit confirmation, blocks private, loopback, link-local, and cloud metadata addresses, follows no redirects, and verifies TLS.

## In scope

Examples of problems we want to hear about:

- reaching the dashboard or API from another machine, or from a web page, without the configured token
- script execution or HTML injection from captured requests, fixtures, or mock responses
- secrets reaching the database, logs, exports, or event streams despite matching a redaction rule
- replay reaching private, loopback, or metadata addresses that are not explicitly allowed
- a mock response setting headers that Relaybox reserves (for example `Set-Cookie` or `Content-Security-Policy`)
- deleting data outside the scope a user selected

## Out of scope

- requests sent by someone who has an endpoint URL (see above)
- attacks that need control of the machine running Relaybox or of its database file
- denial of service through very high request volume when the rate limit was raised or disabled
- running with `RELAYBOX_REPLAY_ALLOWED_HOSTS` set, which deliberately allows replay to the listed private services
