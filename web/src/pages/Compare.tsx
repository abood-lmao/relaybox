import { useQuery } from "@tanstack/react-query";
import { ArrowLeftRight, CircleCheck, GitCompareArrows } from "lucide-react";
import { useState } from "react";
import { Link, useSearchParams } from "react-router";
import { MethodTag, StatusCode, WithRedactions } from "../components/http";
import { PageHeader } from "../components/layout/PageHeader";
import { useDocumentTitle } from "../hooks/useDocumentTitle";
import { IconButton } from "../components/ui/button";
import { EmptyState, ErrorState, LoadingBlock, Notice } from "../components/ui/feedback";
import { Input, Select } from "../components/ui/form";
import { Badge, Panel } from "../components/ui/misc";
import { Tabs } from "../components/ui/tabs";
import { api } from "../lib/api";
import { cn } from "../lib/cn";
import { formatTimestamp, plural } from "../lib/format";
import { useRequests } from "../lib/queries";
import type { CompareBody, Difference, DiffLine, RequestSummary } from "../types/api";

type Part = "request" | "response";

export function ComparePage() {
  useDocumentTitle("Compare");
  const [params, setParams] = useSearchParams();
  const left = Number(params.get("left")) || 0;
  const right = Number(params.get("right")) || 0;
  const part: Part = params.get("part") === "response" ? "response" : "request";
  const recent = useRequests({ limit: "50" });

  const set = (patch: Record<string, string | number | undefined>) => {
    const next = new URLSearchParams(params);
    for (const [k, v] of Object.entries(patch)) {
      if (v) next.set(k, String(v));
      else next.delete(k);
    }
    setParams(next, { replace: true });
  };

  const ready = left > 0 && right > 0 && left !== right;
  const comparison = useQuery({
    queryKey: ["compare", left, right, part],
    queryFn: () => api.compare(left, right, part),
    enabled: ready,
  });

  const options = recent.data?.items ?? [];

  return (
    <div>
      <PageHeader
        title="Compare"
        description="Put two captured requests side by side to see what changed in their headers and bodies."
      />
      <Panel bodyClassName="flex flex-wrap items-end gap-3 p-4">
        <RequestPicker label="Left" value={left} options={options} onChange={(id) => set({ left: id })} />
        <IconButton
          label="Swap left and right"
          onClick={() => set({ left: right || undefined, right: left || undefined })}
          disabled={!left && !right}
          className="mb-0.5"
        >
          <ArrowLeftRight className="size-4" aria-hidden />
        </IconButton>
        <RequestPicker label="Right" value={right} options={options} onChange={(id) => set({ right: id })} />
        <div className="flex flex-col gap-1 text-xs text-muted">
          Compare
          <Tabs<Part>
            size="sm"
            label="Part to compare"
            className="h-8 items-center rounded-md border border-line px-1"
            tabs={[
              { key: "request", label: "Request" },
              { key: "response", label: "Response" },
            ]}
            value={part}
            onChange={(p) => set({ part: p === "request" ? undefined : p })}
          />
        </div>
      </Panel>

      <div className="mt-4">
        {!ready ? (
          <Panel>
            <EmptyState
              icon={<GitCompareArrows className="size-6" aria-hidden />}
              title="Choose two different requests"
            >
              Pick them above, or select two rows on the Requests page and press Compare.
            </EmptyState>
          </Panel>
        ) : comparison.isPending ? (
          <LoadingBlock label="Comparing" />
        ) : comparison.isError ? (
          <Panel>
            <ErrorState
              error={comparison.error}
              title="The requests could not be compared"
              onRetry={() => comparison.refetch()}
            />
          </Panel>
        ) : (
          <div className="space-y-4">
            <div className="grid gap-4 md:grid-cols-2">
              <SideCard side="Left" req={comparison.data.left} />
              <SideCard side="Right" req={comparison.data.right} />
            </div>
            <HeaderDiff diffs={comparison.data.headers} part={part} />
            <BodyDiff body={comparison.data.body} part={part} />
          </div>
        )}
      </div>
    </div>
  );
}

function RequestPicker({
  label,
  value,
  options,
  onChange,
}: {
  label: string;
  value: number;
  options: RequestSummary[];
  onChange: (id: number | undefined) => void;
}) {
  const [typed, setTyped] = useState("");
  const known = options.some((o) => o.id === value);
  return (
    <div className="flex min-w-0 flex-1 basis-72 flex-col gap-1 text-xs text-muted">
      <label htmlFor={`pick-${label}`}>{label}</label>
      <div className="flex gap-2">
        <Select
          id={`pick-${label}`}
          value={value || ""}
          onChange={(e) => onChange(Number(e.target.value) || undefined)}
          className="min-w-0 flex-1 font-mono text-xs"
        >
          <option value="">Choose a recent request</option>
          {value > 0 && !known && <option value={value}>#{value}</option>}
          {options.map((o) => (
            <option key={o.id} value={o.id}>
              #{o.id} {o.method} {o.path || "/"} · {o.endpoint.name} · {formatTimestamp(o.received_at)}
            </option>
          ))}
        </Select>
        <form
          onSubmit={(e) => {
            e.preventDefault();
            const n = Number(typed);
            if (Number.isInteger(n) && n > 0) {
              onChange(n);
              setTyped("");
            }
          }}
        >
          <Input
            aria-label={`${label} request ID`}
            placeholder="ID"
            inputMode="numeric"
            value={typed}
            onChange={(e) => setTyped(e.target.value.replace(/\D/g, ""))}
            className="w-20 font-mono text-xs"
          />
        </form>
      </div>
    </div>
  );
}

function SideCard({ side, req }: { side: string; req: RequestSummary }) {
  return (
    <div className="rounded-lg border border-line bg-surface px-4 py-3">
      <div className="flex items-center justify-between text-xs text-muted">
        <span>{side}</span>
        <Link to={`/requests/${req.id}`} className="font-mono hover:text-fg hover:underline">
          #{req.id}
        </Link>
      </div>
      <p className="mt-1 flex items-baseline gap-2 font-mono text-sm break-all">
        <MethodTag method={req.method} />
        {req.path || "/"}
      </p>
      <p className="mt-1 flex flex-wrap gap-x-3 text-xs text-muted">
        <span>{req.endpoint.name}</span>
        <span>{formatTimestamp(req.received_at)}</span>
        <StatusCode status={req.status} />
      </p>
    </div>
  );
}

const kindLabel: Record<Difference["kind"], { text: string; tone: "ok" | "err" | "warn" }> = {
  added: { text: "added", tone: "ok" },
  removed: { text: "removed", tone: "err" },
  changed: { text: "changed", tone: "warn" },
};

function DiffTable({ diffs, pathLabel }: { diffs: Difference[]; pathLabel: string }) {
  return (
    <div className="overflow-x-auto">
      <table className="w-full min-w-[640px] table-fixed text-left text-sm">
        <thead className="border-b border-line text-xs text-muted">
          <tr>
            <th scope="col" className="w-[28%] px-4 py-2 font-medium">
              {pathLabel}
            </th>
            <th scope="col" className="w-24 px-3 py-2 font-medium">
              Change
            </th>
            <th scope="col" className="px-3 py-2 font-medium">
              Left
            </th>
            <th scope="col" className="px-4 py-2 font-medium">
              Right
            </th>
          </tr>
        </thead>
        <tbody>
          {diffs.map((d, i) => (
            <tr key={i} className="border-b border-line align-top last:border-0">
              <td className="px-4 py-2 font-mono text-xs break-all">{d.path}</td>
              <td className="px-3 py-2">
                <Badge tone={kindLabel[d.kind].tone}>{kindLabel[d.kind].text}</Badge>
              </td>
              <td className="px-3 py-2 font-mono text-xs break-all whitespace-pre-wrap text-muted">
                {d.left !== undefined ? (
                  <WithRedactions text={d.left} />
                ) : (
                  <span className="text-faint">absent</span>
                )}
              </td>
              <td className="px-4 py-2 font-mono text-xs break-all whitespace-pre-wrap">
                {d.right !== undefined ? (
                  <WithRedactions text={d.right} />
                ) : (
                  <span className="text-faint">absent</span>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function Identical({ text }: { text: string }) {
  return (
    <p className="flex items-center gap-2 px-4 py-4 text-sm text-muted">
      <CircleCheck className="size-4 text-ok" aria-hidden />
      {text}
    </p>
  );
}

function HeaderDiff({ diffs, part }: { diffs: Difference[]; part: Part }) {
  return (
    <Panel
      title={`${part === "request" ? "Request" : "Response"} headers`}
      description={diffs.length > 0 ? plural(diffs.length, "difference") : undefined}
    >
      {diffs.length === 0 ? (
        <Identical text="The headers are identical." />
      ) : (
        <DiffTable diffs={diffs} pathLabel="Header" />
      )}
    </Panel>
  );
}

function BodyDiff({ body, part }: { body: CompareBody; part: Part }) {
  const notes: string[] = [];
  if (body.note) notes.push(body.note);
  if (body.incomplete_data)
    notes.push(
      "At least one body was truncated when it was captured, so the comparison covers only the stored part.",
    );
  if (body.truncated)
    notes.push("There are more differences than can be shown. Only the first ones are listed.");
  if (body.approximate)
    notes.push("The bodies differ a lot, so the line diff is approximate rather than minimal.");

  const diffs = body.differences ?? [];
  const lines = body.lines ?? [];
  const description =
    body.mode === "json"
      ? `Compared as JSON${diffs.length ? `, ${plural(diffs.length, "difference")}` : ""}`
      : `Compared as ${body.mode}`;

  return (
    <Panel title={`${part === "request" ? "Request" : "Response"} body`} description={description}>
      {(notes.length > 0 || body.left_error || body.right_error) && (
        <div className="space-y-2 px-4 pt-3">
          {notes.map((n, i) => (
            <Notice key={i}>{n}</Notice>
          ))}
          {body.left_error && (
            <Notice tone="warn" title="Left body">
              {body.left_error}
            </Notice>
          )}
          {body.right_error && (
            <Notice tone="warn" title="Right body">
              {body.right_error}
            </Notice>
          )}
        </div>
      )}
      {body.mode === "empty" && <Identical text="Both bodies are empty." />}
      {body.mode === "binary" &&
        (body.equal ? (
          <Identical text="The binary bodies are identical." />
        ) : (
          <p className="px-4 py-4 text-sm">The binary bodies differ.</p>
        ))}
      {body.mode === "json" &&
        (diffs.length === 0 && body.equal ? (
          <Identical text="The JSON documents are equivalent." />
        ) : (
          <div className="mt-3 border-t border-line">
            <DiffTable diffs={diffs} pathLabel="Path" />
          </div>
        ))}
      {body.mode === "text" &&
        (body.equal ? <Identical text="The bodies are identical." /> : <LineDiff lines={lines} />)}
    </Panel>
  );
}

const CONTEXT = 3;

type Chunk = { kind: "lines"; lines: DiffLine[] } | { kind: "gap"; lines: DiffLine[] };

function chunkLines(lines: DiffLine[]): Chunk[] {
  const keep = new Array<boolean>(lines.length).fill(false);
  lines.forEach((l, i) => {
    if (l.op !== "equal") {
      for (let j = Math.max(0, i - CONTEXT); j <= Math.min(lines.length - 1, i + CONTEXT); j++)
        keep[j] = true;
    }
  });
  const chunks: Chunk[] = [];
  let i = 0;
  while (i < lines.length) {
    const k = keep[i];
    const start = i;
    while (i < lines.length && keep[i] === k) i++;
    const slice = lines.slice(start, i);
    chunks.push(k ? { kind: "lines", lines: slice } : { kind: "gap", lines: slice });
  }
  return chunks;
}

function LineDiff({ lines }: { lines: DiffLine[] }) {
  const [expanded, setExpanded] = useState<Set<number>>(new Set());
  const chunks = chunkLines(lines);
  return (
    <div className="mt-3 overflow-x-auto border-t border-line font-mono text-[12.5px] leading-[1.6]">
      <table className="w-full">
        <tbody>
          {chunks.map((c, ci) =>
            c.kind === "gap" && !expanded.has(ci) ? (
              <tr key={ci}>
                <td colSpan={4} className="bg-surface-2/60 px-4 py-1 text-xs">
                  <button
                    type="button"
                    onClick={() => setExpanded((s) => new Set(s).add(ci))}
                    className="text-accent-fg hover:underline"
                  >
                    Show {plural(c.lines.length, "unchanged line")}
                  </button>
                </td>
              </tr>
            ) : (
              c.lines.map((l, li) => (
                <tr
                  key={`${ci}-${li}`}
                  className={cn(l.op === "insert" && "bg-ok/10", l.op === "delete" && "bg-err/10")}
                >
                  <td className="w-12 px-2 text-right text-faint select-none">{l.left ?? ""}</td>
                  <td className="w-12 px-2 text-right text-faint select-none">{l.right ?? ""}</td>
                  <td
                    className={cn(
                      "w-5 text-center select-none",
                      l.op === "insert" && "text-ok",
                      l.op === "delete" && "text-err",
                    )}
                    aria-label={l.op === "insert" ? "added" : l.op === "delete" ? "removed" : undefined}
                  >
                    {l.op === "insert" ? "+" : l.op === "delete" ? "-" : ""}
                  </td>
                  <td className="pr-4 break-all whitespace-pre-wrap">{l.text || " "}</td>
                </tr>
              ))
            ),
          )}
        </tbody>
      </table>
    </div>
  );
}
