import { Download, WrapText } from "lucide-react";
import { useMemo, useState } from "react";
import { cn } from "../lib/cn";
import { base64ToBytes, formatBytes } from "../lib/format";
import { countNodes, formatJson, JsonTooLargeError, parseJson, type JsonNode } from "../lib/json";
import type { Body } from "../types/api";
import { CodeView } from "./CodeView";
import { PairsTable } from "./HeadersTable";
import { HEX_PREVIEW_BYTES, HexView } from "./HexView";
import { JsonTree } from "./JsonTree";
import { EmptyState, Notice } from "./ui/feedback";
import { CopyButton } from "./ui/misc";
import { Tabs } from "./ui/tabs";

/** Raw text beyond this is cut from the on-screen view (download has it all). */
export const DISPLAY_LIMIT = 512 * 1024;
/** Bodies larger than this are not parsed into a tree. */
const TREE_CHAR_LIMIT = 1024 * 1024;
const TREE_NODE_LIMIT = 20_000;

type Mode = "tree" | "pretty" | "raw" | "fields" | "parts" | "hex";

interface Parsed {
  node: JsonNode | null;
  nodes: number;
  problem?: string;
}

function parseForView(text: string | null): Parsed {
  if (text === null) return { node: null, nodes: 0 };
  if (text.length > TREE_CHAR_LIMIT) {
    return {
      node: null,
      nodes: 0,
      problem: `The body is larger than ${formatBytes(TREE_CHAR_LIMIT)}, so it is shown as raw text.`,
    };
  }
  try {
    const node = parseJson(text, 250_000);
    return { node, nodes: countNodes(node) };
  } catch (e) {
    if (e instanceof JsonTooLargeError)
      return { node: null, nodes: 0, problem: "The document has too many values to format." };
    return { node: null, nodes: 0, problem: (e as Error).message };
  }
}

interface BodyViewProps {
  body: Body;
  downloadHref?: string;
  /** Name used in messages, for example "request body". */
  noun?: string;
}

export function BodyView({ body, downloadHref, noun = "body" }: BodyViewProps) {
  const text = body.encoding === "utf8" ? body.data : null;
  const parsed = useMemo(() => (body.kind === "json" ? parseForView(text) : null), [body.kind, text]);
  const bytes = useMemo(() => (body.encoding === "base64" ? base64ToBytes(body.data) : null), [body]);
  const treeOK = !!parsed?.node && parsed.nodes <= TREE_NODE_LIMIT;
  const pretty = useMemo(() => (parsed?.node ? formatJson(parsed.node) : null), [parsed]);

  const modes: { key: Mode; label: string }[] = [];
  if (body.kind === "json") {
    if (treeOK) modes.push({ key: "tree", label: "Tree" });
    if (pretty !== null) modes.push({ key: "pretty", label: "Formatted" });
  }
  if (body.kind === "form" && body.form) modes.push({ key: "fields", label: "Fields" });
  if (body.kind === "multipart" && body.parts) modes.push({ key: "parts", label: "Parts" });
  if (text !== null) modes.push({ key: "raw", label: "Raw" });
  if (bytes) modes.push({ key: "hex", label: "Hex" });

  const [mode, setMode] = useState<Mode | null>(null);
  const [wrap, setWrap] = useState(true);
  const active = mode && modes.some((m) => m.key === mode) ? mode : modes[0]?.key;

  if (body.kind === "empty") {
    return <EmptyState title={`No ${noun}`} className="py-8" />;
  }

  const shownText = active === "pretty" ? (pretty ?? "") : (text ?? "");
  const cut = (active === "raw" || active === "pretty") && shownText.length > DISPLAY_LIMIT;
  const visibleText = cut ? shownText.slice(0, DISPLAY_LIMIT) : shownText;
  const copyable = (active === "raw" || active === "pretty" || active === "tree") && text !== null;

  return (
    <div>
      <div className="flex flex-wrap items-center justify-between gap-2 border-b border-line px-4 py-2">
        <div className="flex min-w-0 items-center gap-3">
          {modes.length > 1 && (
            <Tabs size="sm" label="Body view" tabs={modes} value={active ?? "raw"} onChange={setMode} />
          )}
          <span className="truncate font-mono text-xs text-muted">
            {body.content_type || "no content type"} · {formatBytes(body.size)}
          </span>
        </div>
        <div className="flex items-center gap-1">
          {(active === "raw" || active === "pretty") && (
            <button
              type="button"
              onClick={() => setWrap((w) => !w)}
              aria-pressed={wrap}
              title="Wrap long lines"
              className={cn(
                "inline-flex h-7 items-center gap-1.5 rounded-md px-2 text-xs font-medium transition-colors",
                wrap ? "bg-surface-2 text-fg" : "text-muted hover:text-fg",
              )}
            >
              <WrapText className="size-3.5" aria-hidden />
              Wrap
            </button>
          )}
          {copyable && (
            <CopyButton
              text={active === "pretty" ? shownText : (text ?? "")}
              label={active === "pretty" ? "Copy formatted" : "Copy body"}
              showLabel
            />
          )}
          {downloadHref && (
            <a
              href={downloadHref}
              download
              className="inline-flex h-7 items-center gap-1.5 rounded-md px-2 text-xs font-medium text-muted transition-colors hover:bg-surface-2 hover:text-fg"
            >
              <Download className="size-3.5" aria-hidden />
              Download
            </a>
          )}
        </div>
      </div>

      <div className="space-y-2 px-4 pt-3 empty:hidden">
        {body.truncated && (
          <Notice tone="warn" title="Body truncated">
            Only the first {formatBytes(body.stored_bytes)} of {formatBytes(body.size)} were stored, because
            the endpoint's body limit was reached. Formatting and replay use the stored part only.
          </Notice>
        )}
        {body.parse_error && (
          <Notice tone="warn" title="Could not parse the body">
            {body.parse_error}
          </Notice>
        )}
        {body.kind === "json" && parsed?.problem && !body.parse_error && <Notice>{parsed.problem}</Notice>}
        {body.kind === "json" && parsed?.node && !treeOK && (
          <Notice>
            The document has {parsed.nodes.toLocaleString()} values, more than the tree view shows. Use the
            formatted view instead.
          </Notice>
        )}
        {cut && (
          <Notice>
            Showing the first {formatBytes(DISPLAY_LIMIT)} of {formatBytes(shownText.length)}.
            {downloadHref && " Download the body to see all of it."}
          </Notice>
        )}
        {active === "hex" && bytes && (
          <Notice>
            Binary content is never rendered. Showing a hex dump of the first{" "}
            {formatBytes(Math.min(bytes.length, HEX_PREVIEW_BYTES))}
            {bytes.length > HEX_PREVIEW_BYTES ? ` of ${formatBytes(bytes.length)}` : ""}.
          </Notice>
        )}
      </div>

      <div className="px-4 py-3">
        {active === "tree" && parsed?.node && <JsonTree root={parsed.node} nodes={parsed.nodes} />}
        {(active === "raw" || active === "pretty") && (
          <CodeView text={visibleText} json={body.kind === "json"} wrap={wrap} label={noun} />
        )}
        {active === "hex" && bytes && <HexView bytes={bytes} />}
        {active === "fields" && body.form && (
          <div className="-mx-4 -my-3">
            <PairsTable pairs={body.form} nameLabel="Field" empty="No fields" filterable />
          </div>
        )}
        {active === "parts" && body.parts && <Parts parts={body.parts} />}
      </div>
    </div>
  );
}

function Parts({ parts }: { parts: NonNullable<Body["parts"]> }) {
  if (parts.length === 0) return <EmptyState title="No parts" className="py-6" />;
  return (
    <ol className="space-y-3">
      {parts.map((p, i) => (
        <li key={i} className="rounded-md border border-line">
          <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1 border-b border-line bg-surface-2/60 px-3 py-1.5">
            <span className="font-mono text-xs font-medium">{p.name || "(unnamed)"}</span>
            {p.filename && <span className="font-mono text-xs text-muted">file: {p.filename}</span>}
            {p.content_type && <span className="font-mono text-xs text-muted">{p.content_type}</span>}
            <span className="ml-auto text-xs text-faint">{formatBytes(p.size)}</span>
          </div>
          <div className="px-3 py-2">
            {p.text !== undefined ? (
              <CodeView text={p.text} json={p.content_type?.includes("json")} />
            ) : (
              <p className="text-xs text-muted">
                Binary content ({formatBytes(p.size)}). Download the body to inspect it.
              </p>
            )}
          </div>
        </li>
      ))}
    </ol>
  );
}
