import { formatBytes } from "../lib/format";
import type { RequestDetail } from "../types/api";
import { WithRedactions } from "./http";

const BODY_PREVIEW = 4096;

function requestTarget(url: string): string {
  try {
    const u = new URL(url);
    return u.pathname + u.search;
  } catch {
    return url;
  }
}

/**
 * The request as it reached Relaybox: request line, headers, and the
 * start of the body. Header names are in canonical form and sorted, as
 * the server stores them.
 */
export function WireView({ request }: { request: RequestDetail }) {
  const body = request.body;
  let bodyText: string | null = null;
  let bodyNote: string | null = null;
  if (body.kind !== "empty") {
    if (body.encoding === "utf8") {
      bodyText = body.data.length > BODY_PREVIEW ? body.data.slice(0, BODY_PREVIEW) : body.data;
      if (body.data.length > BODY_PREVIEW)
        bodyNote = `${formatBytes(body.size - BODY_PREVIEW)} more in the Body tab`;
    } else {
      bodyNote = `binary body, ${formatBytes(body.size)}; see the Body tab`;
    }
  }
  return (
    <pre
      tabIndex={0}
      aria-label="Raw request"
      className="scroll-thin overflow-x-auto font-mono text-[12.5px] leading-[1.6] break-all whitespace-pre-wrap"
    >
      <span className="font-medium">
        <span className="text-accent-fg">{request.method}</span> {requestTarget(request.url)}
      </span>
      {"\n"}
      {request.headers.map(([k, v], i) => (
        <span key={i}>
          <span className="text-muted">{k}:</span> <WithRedactions text={v} />
          {"\n"}
        </span>
      ))}
      {(bodyText !== null || bodyNote) && "\n"}
      {bodyText !== null && <WithRedactions text={bodyText} />}
      {bodyNote && (
        <>
          {bodyText !== null && "\n"}
          <span className="text-faint italic">[{bodyNote}]</span>
        </>
      )}
    </pre>
  );
}
