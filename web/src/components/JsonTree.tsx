import { ChevronRight } from "lucide-react";
import { memo, useState } from "react";
import { cn } from "../lib/cn";
import { REDACTED } from "../lib/http";
import type { JsonNode } from "../lib/json";

const AUTO_EXPAND_DEPTH = 3;
const FULLY_OPEN_NODES = 400;
const CHILD_PAGE = 200;

function summary(node: JsonNode): string {
  if (node.type === "object") return node.entries.length === 1 ? "1 key" : `${node.entries.length} keys`;
  if (node.type === "array") return node.items.length === 1 ? "1 item" : `${node.items.length} items`;
  return "";
}

function Scalar({ node }: { node: JsonNode }) {
  switch (node.type) {
    case "string":
      if (node.value === REDACTED) {
        return (
          <span className="redacted" title="Removed before storage">
            {REDACTED}
          </span>
        );
      }
      return <span className="break-all text-code-string">{JSON.stringify(node.value)}</span>;
    case "number":
      return <span className="text-code-number">{node.raw}</span>;
    case "boolean":
      return <span className="text-code-keyword">{String(node.value)}</span>;
    case "null":
      return <span className="text-code-keyword">null</span>;
    default:
      return null;
  }
}

interface RowProps {
  name: string | number | null;
  node: JsonNode;
  depth: number;
  last: boolean;
  openDepth: number;
}

const Row = memo(function Row({ name, node, depth, last, openDepth }: RowProps) {
  const container = node.type === "object" || node.type === "array";
  const [open, setOpen] = useState(depth < openDepth);
  const [shown, setShown] = useState(CHILD_PAGE);
  const comma = last ? null : <span className="text-code-punct">,</span>;
  const label =
    name === null ? null : typeof name === "number" ? (
      <span className="text-faint">{name}: </span>
    ) : (
      <>
        <span className="text-code-key">{JSON.stringify(name)}</span>
        <span className="text-code-punct">: </span>
      </>
    );

  if (!container) {
    return (
      <div className="pl-4">
        {label}
        <Scalar node={node} />
        {comma}
      </div>
    );
  }

  const children: [string | number, JsonNode][] =
    node.type === "object" ? node.entries : node.items.map((v, i) => [i, v] as [number, JsonNode]);
  const [openCh, closeCh] = node.type === "object" ? ["{", "}"] : ["[", "]"];

  if (children.length === 0) {
    return (
      <div className="pl-4">
        {label}
        <span className="text-code-punct">
          {openCh}
          {closeCh}
        </span>
        {comma}
      </div>
    );
  }

  return (
    <div>
      <div className="flex">
        <button
          type="button"
          onClick={() => setOpen((o) => !o)}
          aria-expanded={open}
          aria-label={open ? "Collapse" : "Expand"}
          className="flex size-4 shrink-0 items-center justify-center self-center rounded text-faint hover:text-fg"
        >
          <ChevronRight className={cn("size-3 transition-transform", open && "rotate-90")} aria-hidden />
        </button>
        <div className="min-w-0">
          {label}
          <span className="text-code-punct">{openCh}</span>
          {!open && (
            <>
              <button
                type="button"
                onClick={() => setOpen(true)}
                className="mx-1 rounded bg-surface-2 px-1 text-[11px] text-muted hover:text-fg"
              >
                {summary(node)}
              </button>
              <span className="text-code-punct">{closeCh}</span>
              {comma}
            </>
          )}
        </div>
      </div>
      {open && (
        <>
          <div className="ml-[7px] border-l border-line pl-2">
            {children.slice(0, shown).map(([k, v], i) => (
              <Row
                key={i}
                name={k}
                node={v}
                depth={depth + 1}
                last={i === children.length - 1}
                openDepth={openDepth}
              />
            ))}
            {children.length > shown && (
              <button
                type="button"
                onClick={() => setShown((s) => s + CHILD_PAGE * 5)}
                className="my-0.5 ml-4 rounded px-1 text-xs text-accent-fg hover:underline"
              >
                Show {Math.min(children.length - shown, CHILD_PAGE * 5)} more of {children.length - shown}{" "}
                remaining
              </button>
            )}
          </div>
          <div className="pl-4">
            <span className="text-code-punct">{closeCh}</span>
            {comma}
          </div>
        </>
      )}
    </div>
  );
});

/** Collapsible JSON tree. Small documents open fully; large ones open to a few levels. */
export function JsonTree({ root, nodes }: { root: JsonNode; nodes: number }) {
  return (
    <div className="font-mono text-[12.5px] leading-[1.6]">
      <Row
        name={null}
        node={root}
        depth={0}
        last
        openDepth={nodes <= FULLY_OPEN_NODES ? Infinity : AUTO_EXPAND_DEPTH}
      />
    </div>
  );
}
