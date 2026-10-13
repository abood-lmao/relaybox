import { FileCode2 } from "lucide-react";
import type { ReactNode } from "react";
import { CodeView } from "../components/CodeView";
import { PageHeader } from "../components/layout/PageHeader";
import { useDocumentTitle } from "../hooks/useDocumentTitle";
import { Kbd, Panel } from "../components/ui/misc";
import { useMeta } from "../lib/queries";

function Section({ id, title, children }: { id: string; title: string; children: ReactNode }) {
  return (
    <section id={id} className="scroll-mt-20">
      <Panel
        title={title}
        bodyClassName="space-y-3 p-4 text-sm leading-relaxed [&_li]:ml-4 [&_ul]:list-disc [&_ul]:space-y-1"
      >
        {children}
      </Panel>
    </section>
  );
}

function C({ children }: { children: ReactNode }) {
  return <code className="rounded bg-surface-2 px-1 py-px font-mono text-[12.5px]">{children}</code>;
}

const sections = [
  ["start", "Quick start"],
  ["endpoints", "Endpoints and URLs"],
  ["responses", "Mock responses"],
  ["inspect", "Inspecting requests"],
  ["redaction", "Redaction"],
  ["replay", "Replay"],
  ["compare", "Compare"],
  ["fixtures", "Fixtures"],
  ["search", "Search and filters"],
  ["shortcuts", "Keyboard shortcuts"],
  ["api", "REST API"],
  ["config", "Configuration"],
  ["security", "Security model"],
] as const;

const envVars: [string, string, string][] = [
  [
    "RELAYBOX_HOST",
    "127.0.0.1",
    "Address to listen on. Other addresses expose webhook URLs to the network; the dashboard still only accepts this machine unless remote access is enabled.",
  ],
  ["RELAYBOX_PORT", "8787", "Port to listen on."],
  [
    "RELAYBOX_DATABASE_PATH",
    "./data/relaybox.db",
    "SQLite database file. The directory is created if needed.",
  ],
  [
    "RELAYBOX_PUBLIC_BASE_URL",
    "http://HOST:PORT",
    "Base used to build endpoint URLs, for example a tunnel or reverse proxy address.",
  ],
  [
    "RELAYBOX_MAX_BODY_BYTES",
    "1048576",
    "Largest body limit anyone can configure (1 KiB to 64 MiB). Also the default body limit.",
  ],
  [
    "RELAYBOX_RETENTION_DAYS",
    "7",
    "Default retention. 0 keeps requests forever. Can be changed in Settings.",
  ],
  [
    "RELAYBOX_MAX_STORED_REQUESTS",
    "10000",
    "Default cap on stored requests. 0 means no cap. Can be changed in Settings.",
  ],
  ["RELAYBOX_LOG_LEVEL", "info", "debug, info, warn, or error."],
  ["RELAYBOX_LOG_FORMAT", "text", "text or json."],
  ["RELAYBOX_RATE_LIMIT_RPS", "50", "Requests per second accepted per endpoint. 0 disables the limit."],
  ["RELAYBOX_RATE_LIMIT_BURST", "100", "Burst size for the rate limit."],
  [
    "RELAYBOX_ALLOW_REMOTE_DASHBOARD",
    "false",
    "Allow the dashboard and API from other machines. Requires a dashboard token.",
  ],
  [
    "RELAYBOX_DASHBOARD_TOKEN",
    "(none)",
    "Access token, at least 24 characters. When set, the dashboard and API require it.",
  ],
  ["RELAYBOX_ALLOW_REMOTE_REPLAY", "false", "Allow replay to URLs while the remote dashboard is enabled."],
  [
    "RELAYBOX_REPLAY_ALLOWED_HOSTS",
    "(none)",
    "Development option: comma-separated host:port pairs on private networks that replay may reach. Rejected when the remote dashboard is enabled.",
  ],
];

export function DocsPage() {
  useDocumentTitle("Documentation");
  const meta = useMeta();
  const prefix = meta.data?.hook_url_prefix ?? "http://127.0.0.1:8787/hooks/";
  const methods = meta.data?.allowed_methods.join(", ") ?? "GET, HEAD, POST, PUT, PATCH, DELETE, OPTIONS";
  const example = `${prefix}YOUR-ENDPOINT-ID`;

  return (
    <div>
      <PageHeader
        title="Documentation"
        description="How Relaybox captures, stores, and replays webhook requests."
      />
      <div className="grid gap-6 lg:grid-cols-[200px_minmax(0,1fr)]">
        <nav aria-label="On this page" className="lg:sticky lg:top-20 lg:self-start">
          <ul className="flex flex-wrap gap-x-3 gap-y-1 text-sm lg:flex-col">
            {sections.map(([id, title]) => (
              <li key={id}>
                <a href={`#${id}`} className="text-muted hover:text-fg">
                  {title}
                </a>
              </li>
            ))}
          </ul>
        </nav>
        <div className="min-w-0 max-w-3xl space-y-4">
          <Section id="start" title="Quick start">
            <ol className="ml-4 list-decimal space-y-1">
              <li>Create an endpoint on the Endpoints page. It gets a unique URL.</li>
              <li>Send a request to that URL from a webhook provider, your own code, or curl.</li>
              <li>
                Open the request under Requests to see its headers, query, body, and the response it got.
              </li>
            </ol>
            <div className="rounded-md border border-line bg-surface-2/50 p-3">
              <CodeView
                text={`curl -X POST ${example} \\\n  -H 'Content-Type: application/json' \\\n  -d '{"event":"test"}'`}
              />
            </div>
          </Section>

          <Section id="endpoints" title="Endpoints and URLs">
            <p>
              Endpoint URLs look like <C>{example}</C>. The ID is random and hard to guess, but it is not a
              password: anyone who has the URL can send requests to it. Treat it like a secret, and use New
              URL on the endpoint page if it leaks. The old URL stops working immediately.
            </p>
            <ul>
              <li>
                Accepted methods: <C>{methods}</C>. Others get <C>405</C>.
              </li>
              <li>
                Sub-paths are captured too. A request to <C>{`${example}/orders/created?x=1`}</C> is stored
                with the path <C>/orders/created</C>.
              </li>
              <li>
                Disabled endpoints answer <C>503</C> and store nothing. Unknown IDs answer <C>404</C>.
              </li>
              <li>
                Each endpoint accepts a limited request rate. Requests above it get <C>429</C> with a{" "}
                <C>Retry-After</C> header and are not stored.
              </li>
              <li>
                Bodies larger than the endpoint's body limit are stored truncated and marked as such. The
                sender still gets the mock response.
              </li>
              <li>
                If a request cannot be stored, the sender gets <C>503</C> instead of a success response.
              </li>
            </ul>
          </Section>

          <Section id="responses" title="Mock responses">
            <p>
              Every endpoint answers with a configurable response: a status code from 200 to 599, a content
              type, up to 32 extra headers, a body of up to 64 KiB, and an optional delay of up to 10 seconds.
              JSON bodies are validated when you save. Use Preview to see exactly what a sender would receive.
            </p>
            <p>
              Headers that control transport or would affect the dashboard are reserved, for example{" "}
              <C>Set-Cookie</C>, <C>Content-Security-Policy</C>, <C>Strict-Transport-Security</C>, and
              anything starting with <C>X-Relaybox-</C>. Responses to <C>HEAD</C> requests have no body, and
              statuses such as <C>204</C> cannot have one.
            </p>
          </Section>

          <Section id="inspect" title="Inspecting requests">
            <ul>
              <li>
                Overview shows a summary and the request as received: request line, headers, and the start of
                the body.
              </li>
              <li>
                JSON bodies get a collapsible tree and a formatted view. Numbers and duplicate keys are shown
                exactly as sent.
              </li>
              <li>Form and multipart bodies are split into fields and parts.</li>
              <li>
                Binary bodies are never rendered; you get a hex dump of the first 4 KiB and a download link.
              </li>
              <li>
                Captured HTML and scripts are always displayed as text. Downloads are served as attachments,
                never inline.
              </li>
              <li>
                Copy as curl rebuilds the request. Redacted headers are left out because their values were
                never stored.
              </li>
            </ul>
          </Section>

          <Section id="redaction" title="Redaction">
            <p>
              Before a request is stored, values of sensitive headers (such as <C>Authorization</C> and{" "}
              <C>Cookie</C>) and sensitive fields in JSON bodies, form bodies, and query strings (such as{" "}
              <C>password</C> and <C>client_secret</C>) are replaced with{" "}
              <span className="redacted">[REDACTED]</span>. The originals are not kept anywhere, so they
              cannot be shown, exported, or replayed later.
            </p>
            <p>
              Field names match ignoring case, dashes, underscores, dots, and spaces, so <C>clientSecret</C>{" "}
              and <C>client-secret</C> both match <C>client_secret</C>. Edit both lists in Settings. Changes
              apply to new requests only.
            </p>
          </Section>

          <Section id="replay" title="Replay">
            <p>
              Replay sends a stored request again. Nothing is replayed automatically, and every replay is
              recorded in the request's history.
            </p>
            <ul>
              <li>
                <span className="font-medium">Into an endpoint:</span> the request is recorded as a new
                request in the chosen endpoint, linked to the original. Nothing leaves this machine.
              </li>
              <li>
                <span className="font-medium">To a URL:</span> the request is sent over the network after you
                confirm the destination. You can change the method, choose and edit headers, and edit the body
                first.
              </li>
            </ul>
            <p>Replay to URLs is protected against server-side request forgery:</p>
            <ul>
              <li>Only http and https URLs are accepted.</li>
              <li>
                The host name is resolved once, every address is checked, and the connection goes to a checked
                address.
              </li>
              <li>
                Loopback, private, link-local, and other special addresses are refused. Cloud metadata
                addresses are always refused. For local development you can allow specific <C>host:port</C>{" "}
                pairs with <C>RELAYBOX_REPLAY_ALLOWED_HOSTS</C>.
              </li>
              <li>
                Redirects are not followed, proxy settings are ignored, and TLS certificates are always
                verified.
              </li>
              <li>
                Each replay times out after 30 seconds, and only the first 1 MiB of the response is kept.
              </li>
              <li>
                Framing headers such as <C>Host</C> and <C>Content-Length</C> are never copied; they are set
                for the new request.
              </li>
            </ul>
            <p>
              If a stored body was truncated, replaying it would send incomplete data, so you must edit the
              body or confirm that the stored part should be sent.
            </p>
          </Section>

          <Section id="compare" title="Compare">
            <p>
              Compare puts two requests (or their responses) side by side. JSON bodies are compared
              structurally and differences are listed by path, such as <C>$.data.items[2].price</C>. Other
              text is compared line by line. Binary bodies are only checked for equality. Header differences
              are listed too.
            </p>
          </Section>

          <Section id="fixtures" title="Fixtures">
            <p>
              A fixture is a JSON file containing captured requests. Export one from an endpoint, from
              selected requests, or from a single request. Import a fixture into any endpoint to load the
              requests back as imported requests.
            </p>
            <ul>
              <li>
                Imports are validated first. If anything is wrong, nothing is imported and the problems are
                listed.
              </li>
              <li>
                Imported requests are never sent anywhere, and the target endpoint's settings are used, not
                the file's.
              </li>
              <li>The current redaction rules and body limit apply to imported requests.</li>
              <li>
                The format is described by <C>api/fixture.schema.json</C> in the repository, a JSON Schema
                document.
              </li>
            </ul>
          </Section>

          <Section id="search" title="Search and filters">
            <p>
              The search box matches text in the path, query string, headers, content type, and body, ignoring
              case for ASCII letters. Combine it with the endpoint, method, status, source, and date filters
              on the Requests page. Filters live in the page address, so you can bookmark or share a filtered
              view.
            </p>
          </Section>

          <Section id="shortcuts" title="Keyboard shortcuts">
            <ul>
              <li>
                <Kbd>/</Kbd> focuses the search box.
              </li>
              <li>
                <Kbd>Esc</Kbd> closes dialogs and leaves the search box.
              </li>
              <li>
                <Kbd>←</Kbd> and <Kbd>→</Kbd> move between tabs when a tab is focused.
              </li>
            </ul>
          </Section>

          <Section id="api" title="REST API">
            <p>
              Everything in the dashboard uses a JSON API under <C>/api/v1</C>. Errors have the shape{" "}
              <C>{'{"error":{"code":"…","message":"…"}}'}</C>. When a dashboard token is configured, send it
              as <C>Authorization: Bearer TOKEN</C>.
            </p>
            <a
              href="/api/v1/openapi.yaml"
              download
              className="inline-flex items-center gap-1.5 text-sm font-medium text-accent-fg hover:underline"
            >
              <FileCode2 className="size-4" aria-hidden />
              Download the OpenAPI document
            </a>
          </Section>

          <Section id="config" title="Configuration">
            <p>
              Server settings come from environment variables and are read at startup. <C>relaybox serve</C>{" "}
              also accepts <C>--host</C>, <C>--port</C>, <C>--database</C>, and <C>--public-url</C>, which
              take precedence. Invalid values stop the server with a message instead of being ignored.
            </p>
            <div className="-mx-4 overflow-x-auto">
              <table className="w-full min-w-[640px] text-left text-sm">
                <thead className="border-y border-line text-xs text-muted">
                  <tr>
                    <th scope="col" className="px-4 py-2 font-medium">
                      Variable
                    </th>
                    <th scope="col" className="px-3 py-2 font-medium">
                      Default
                    </th>
                    <th scope="col" className="px-4 py-2 font-medium">
                      Meaning
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {envVars.map(([name, def, text]) => (
                    <tr key={name} className="border-b border-line align-top last:border-0">
                      <td className="px-4 py-2 font-mono text-xs">{name}</td>
                      <td className="px-3 py-2 font-mono text-xs whitespace-nowrap text-muted">{def}</td>
                      <td className="px-4 py-2 text-xs text-muted">{text}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            <p>
              Run <C>relaybox doctor</C> to check the configuration, database, and port before starting.
            </p>
          </Section>

          <Section id="security" title="Security model">
            <ul>
              <li>
                Relaybox listens on 127.0.0.1 by default. Webhook URLs, the dashboard, and the API are only
                reachable from this machine.
              </li>
              <li>
                The dashboard and API reject requests that are not from this machine, that use an unexpected
                host name (which blocks DNS rebinding), or that arrive through a proxy or tunnel. Webhook URLs
                and health checks are not restricted, so a tunnel can forward webhooks without exposing the
                dashboard.
              </li>
              <li>
                Remote dashboard access needs <C>RELAYBOX_ALLOW_REMOTE_DASHBOARD=true</C> and a dashboard
                token. Use HTTPS in front of Relaybox when you do this.
              </li>
              <li>
                State-changing API calls from other websites are rejected (cross-origin request protection).
              </li>
              <li>
                The dashboard sends a strict Content Security Policy, and captured content is never executed.
              </li>
            </ul>
          </Section>
        </div>
      </div>
    </div>
  );
}
