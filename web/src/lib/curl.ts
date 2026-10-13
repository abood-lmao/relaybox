import type { Pair, RequestDetail } from "../types/api";
import { isRedacted } from "./http";

/** Quotes a value for POSIX shells. */
export function shellQuote(value: string): string {
  if (/^[A-Za-z0-9_\-./:=@%+,]+$/.test(value)) return value;
  return `'${value.replace(/'/g, `'"'"'`)}'`;
}

const skipHeaders = new Set([
  "host",
  "content-length",
  "connection",
  "transfer-encoding",
  "accept-encoding",
  "user-agent",
  "x-forwarded-for",
  "x-forwarded-host",
  "x-forwarded-proto",
  "x-real-ip",
  "forwarded",
]);

/** Example requests shown for an endpoint URL. */
export function endpointExamples(url: string): { label: string; command: string }[] {
  return [
    {
      label: "JSON",
      command: [
        `curl -X POST ${shellQuote(url)} \\`,
        `  -H 'Content-Type: application/json' \\`,
        `  -d '{"event":"test","message":"hello"}'`,
      ].join("\n"),
    },
    {
      label: "Form",
      command: [`curl -X POST ${shellQuote(url + "/form")} \\`, `  -d 'name=Ada' -d 'plan=pro'`].join("\n"),
    },
    {
      label: "GET with query",
      command: `curl ${shellQuote(url + "?source=docs&attempt=1")}`,
    },
  ];
}

export interface CurlResult {
  command: string;
  omittedHeaders: string[];
  bodyOmitted: boolean;
}

/**
 * Builds a curl command that resends a captured request to its endpoint.
 * Redacted headers are left out because their real values were not stored.
 */
export function requestToCurl(req: RequestDetail): CurlResult {
  const parts = [`curl -X ${req.method} ${shellQuote(req.url)}`];
  const omitted: string[] = [];
  for (const [name, value] of req.headers as Pair[]) {
    if (skipHeaders.has(name.toLowerCase())) continue;
    if (isRedacted(value)) {
      omitted.push(name);
      continue;
    }
    parts.push(`-H ${shellQuote(`${name}: ${value}`)}`);
  }
  let bodyOmitted = false;
  if (req.body.kind !== "empty") {
    if (req.body.encoding === "utf8" && !req.body.truncated) {
      parts.push(`--data-binary ${shellQuote(req.body.data)}`);
    } else {
      bodyOmitted = true;
    }
  }
  return { command: parts.join(" \\\n  "), omittedHeaders: omitted, bodyOmitted };
}
