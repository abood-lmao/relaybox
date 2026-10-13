import { useMutation, useQueryClient } from "@tanstack/react-query";
import { ArrowRight, Check, Inbox } from "lucide-react";
import { useState, type FormEvent, type ReactNode } from "react";
import { Link } from "react-router";
import { CurlExamples } from "../components/endpoint/CurlExamples";
import { PageHeader } from "../components/layout/PageHeader";
import { useDocumentTitle } from "../hooks/useDocumentTitle";
import { RequestTable } from "../components/RequestTable";
import { useArrivalBaseline } from "../hooks/useArrivalBaseline";
import { Button } from "../components/ui/button";
import { EmptyState, ErrorState, LoadingBlock } from "../components/ui/feedback";
import { Input } from "../components/ui/form";
import { Badge, CopyButton, Panel } from "../components/ui/misc";
import { useLive } from "../hooks/live";
import { ApiError, api, errorMessage } from "../lib/api";
import { cn } from "../lib/cn";
import { formatNumber, formatRelative, plural } from "../lib/format";
import { useEndpoints, useStats } from "../lib/queries";
import type { Endpoint, Stats } from "../types/api";

export function OverviewPage() {
  useDocumentTitle("Overview");
  const stats = useStats();
  const endpoints = useEndpoints();
  const baseline = useArrivalBaseline(stats.data?.recent, "overview", stats.isSuccess);

  if (stats.isPending || endpoints.isPending) return <LoadingBlock />;
  if (stats.isError || endpoints.isError) {
    return (
      <ErrorState
        error={stats.error ?? endpoints.error}
        onRetry={() => {
          void stats.refetch();
          void endpoints.refetch();
        }}
      />
    );
  }
  const s = stats.data;
  const showOnboarding = endpoints.data.length === 0 || s.total_requests === 0;

  return (
    <div className="space-y-4">
      <PageHeader title="Overview" description="Activity across every endpoint on this Relaybox instance." />
      {showOnboarding && <Onboarding endpoints={endpoints.data} />}
      <StatRow stats={s} />
      <div className="grid gap-4 lg:grid-cols-3">
        <Panel title="Requests, last 24 hours" className="lg:col-span-2" bodyClassName="px-4 pt-4 pb-3">
          <HourlyChart hourly={s.hourly} />
        </Panel>
        <Panel title="Response status codes" description="All stored requests" bodyClassName="p-4">
          <StatusDistribution classes={s.status_classes} />
        </Panel>
      </div>
      <div className="grid gap-4 lg:grid-cols-3">
        <Panel
          title="Recent requests"
          className="lg:col-span-2"
          actions={
            s.recent.length > 0 && (
              <Link
                to="/requests"
                className="inline-flex items-center gap-1 text-xs font-medium text-accent-fg hover:underline"
              >
                View all <ArrowRight className="size-3" aria-hidden />
              </Link>
            )
          }
        >
          {s.recent.length > 0 ? (
            <RequestTable items={s.recent} compact newerThan={baseline} />
          ) : (
            <EmptyState
              icon={<Inbox className="size-6" aria-hidden />}
              title="No requests yet"
              className="py-10"
            >
              Requests show up here the moment they arrive.
            </EmptyState>
          )}
        </Panel>
        <Panel title="Most active endpoints" description="By requests in the last 24 hours">
          <TopEndpoints stats={s} />
        </Panel>
      </div>
    </div>
  );
}

function StatRow({ stats }: { stats: Stats }) {
  const errors = (stats.status_classes["4xx"] ?? 0) + (stats.status_classes["5xx"] ?? 0);
  const share = stats.total_requests > 0 ? Math.round((errors / stats.total_requests) * 1000) / 10 : 0;
  const items = [
    { label: "Stored requests", value: formatNumber(stats.total_requests) },
    { label: "Last 24 hours", value: formatNumber(stats.last_24h) },
    { label: "Endpoints", value: formatNumber(stats.endpoints) },
    {
      label: "4xx and 5xx responses",
      value: formatNumber(errors),
      note: stats.total_requests > 0 ? `${share}% of stored` : undefined,
    },
  ];
  return (
    <dl className="grid grid-cols-2 gap-px overflow-hidden rounded-lg border border-line bg-line lg:grid-cols-4">
      {items.map((it) => (
        <div key={it.label} className="bg-surface px-4 py-3">
          <dt className="text-xs text-muted">{it.label}</dt>
          <dd className="mt-1 flex items-baseline gap-2">
            <span className="font-mono text-xl font-medium tracking-tight">{it.value}</span>
            {it.note && <span className="text-xs text-faint">{it.note}</span>}
          </dd>
        </div>
      ))}
    </dl>
  );
}

const hourFormat = new Intl.DateTimeFormat(undefined, { hour: "2-digit", minute: "2-digit" });

function HourlyChart({ hourly }: { hourly: Stats["hourly"] }) {
  const max = Math.max(1, ...hourly.map((h) => h.count));
  const total = hourly.reduce((n, h) => n + h.count, 0);
  const busiest = hourly.reduce((a, b) => (b.count > a.count ? b : a), hourly[0]);
  const summary =
    total === 0
      ? "No requests in the last 24 hours."
      : `${plural(total, "request")} in the last 24 hours. Busiest hour started at ${hourFormat.format(new Date(busiest.start))} with ${plural(busiest.count, "request")}.`;
  return (
    <figure>
      <div role="img" aria-label={summary} className="flex h-36 items-end gap-[3px]">
        {hourly.map((h) => {
          const label = `${hourFormat.format(new Date(h.start))}: ${plural(h.count, "request")}`;
          return (
            <div key={h.start} className="group relative flex h-full flex-1 items-end" title={label}>
              <div
                className={cn(
                  "w-full rounded-t-[2px] transition-colors",
                  h.count > 0 ? "bg-accent/70 group-hover:bg-accent" : "bg-line",
                )}
                style={{ height: h.count > 0 ? `${Math.max(4, (h.count / max) * 100)}%` : "2px" }}
              />
            </div>
          );
        })}
      </div>
      <div className="mt-2 flex justify-between font-mono text-[11px] text-faint" aria-hidden>
        {hourly
          .filter((_, i) => i % 6 === 0 || i === hourly.length - 1)
          .map((h, i, arr) => (
            <span key={h.start}>{i === arr.length - 1 ? "now" : hourFormat.format(new Date(h.start))}</span>
          ))}
      </div>
      <figcaption className="sr-only">{summary}</figcaption>
    </figure>
  );
}

const classes = [
  { key: "2xx", label: "Success", color: "bg-ok" },
  { key: "3xx", label: "Redirect", color: "bg-redirect" },
  { key: "4xx", label: "Client error", color: "bg-warn" },
  { key: "5xx", label: "Server error", color: "bg-err" },
];

function StatusDistribution({ classes: counts }: { classes: Record<string, number> }) {
  const total = classes.reduce((n, c) => n + (counts[c.key] ?? 0), 0);
  if (total === 0) return <p className="py-6 text-center text-sm text-muted">No responses recorded yet.</p>;
  return (
    <div>
      <div className="flex h-2.5 overflow-hidden rounded-full bg-surface-2" aria-hidden>
        {classes.map((c) =>
          counts[c.key] ? (
            <div key={c.key} className={c.color} style={{ width: `${(counts[c.key] / total) * 100}%` }} />
          ) : null,
        )}
      </div>
      <ul className="mt-4 space-y-2 text-sm">
        {classes.map((c) => {
          const n = counts[c.key] ?? 0;
          return (
            <li key={c.key} className="flex items-center gap-2">
              <span className={cn("size-2 rounded-full", c.color)} aria-hidden />
              <span className="font-mono text-xs">{c.key}</span>
              <span className="text-muted">{c.label}</span>
              <span className="ml-auto font-mono text-xs">{formatNumber(n)}</span>
              <span className="w-12 text-right font-mono text-xs text-faint">
                {Math.round((n / total) * 100)}%
              </span>
            </li>
          );
        })}
      </ul>
    </div>
  );
}

function TopEndpoints({ stats }: { stats: Stats }) {
  if (stats.top_endpoints.length === 0) {
    return <p className="px-4 py-10 text-center text-sm text-muted">No endpoint activity yet.</p>;
  }
  return (
    <ul>
      {stats.top_endpoints.map((e) => (
        <li key={e.id} className="border-b border-line last:border-0">
          <Link
            to={`/endpoints/${e.id}`}
            className="flex items-center gap-3 px-4 py-2.5 hover:bg-surface-2/70"
          >
            <div className="min-w-0 flex-1">
              <div className="flex items-center gap-2">
                <span className="truncate font-medium">{e.name}</span>
                {!e.enabled && <Badge>disabled</Badge>}
              </div>
              <p className="text-xs text-muted">
                {e.last_request_at ? `Last request ${formatRelative(e.last_request_at)}` : "No requests yet"}{" "}
                · {formatNumber(e.total)} total
              </p>
            </div>
            <span className="font-mono text-sm">{formatNumber(e.last_24h)}</span>
          </Link>
        </li>
      ))}
    </ul>
  );
}

function Onboarding({ endpoints }: { endpoints: Endpoint[] }) {
  const qc = useQueryClient();
  const { status } = useLive();
  const [name, setName] = useState("");
  const create = useMutation({
    mutationFn: (n: string) => api.createEndpoint({ name: n }),
    onSuccess: () => {
      setName("");
      void qc.invalidateQueries({ queryKey: ["endpoints"] });
      void qc.invalidateQueries({ queryKey: ["stats"] });
    },
  });
  const first = endpoints[0];
  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (name.trim()) create.mutate(name.trim());
  };
  const createError =
    create.error instanceof ApiError
      ? (create.error.fields.name ?? create.error.message)
      : create.error
        ? errorMessage(create.error)
        : null;

  return (
    <Panel title="Get started" description="Three steps to your first captured request.">
      <ol className="divide-y divide-line">
        <Step n={1} done={!!first} title="Create an endpoint">
          {first ? (
            <p className="text-sm text-muted">
              <Link to={`/endpoints/${first.id}`} className="font-medium text-fg hover:underline">
                {first.name}
              </Link>{" "}
              is ready.
            </p>
          ) : (
            <form onSubmit={submit} className="flex max-w-md flex-col gap-1.5">
              <div className="flex gap-2">
                <Input
                  aria-label="Endpoint name"
                  placeholder="My first endpoint"
                  maxLength={80}
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  aria-invalid={createError ? true : undefined}
                />
                <Button type="submit" variant="primary" loading={create.isPending} disabled={!name.trim()}>
                  Create
                </Button>
              </div>
              {createError && <p className="text-xs text-err">{createError}</p>}
            </form>
          )}
        </Step>
        <Step n={2} done={false} disabled={!first} title="Send it a request">
          {first ? (
            <div className="space-y-2">
              <div className="flex max-w-2xl items-center gap-2 rounded-md border border-line bg-surface-2/50 py-1 pr-1 pl-3">
                <code className="min-w-0 flex-1 truncate font-mono text-[12.5px]">{first.url}</code>
                <CopyButton text={first.url} label="Copy URL" showLabel />
              </div>
              <div className="max-w-2xl">
                <CurlExamples url={first.url} />
              </div>
            </div>
          ) : (
            <p className="text-sm text-muted">
              Point any webhook sender, script, or curl at the endpoint URL.
            </p>
          )}
        </Step>
        <Step n={3} done={false} disabled={!first} title="Inspect it here">
          <p className="flex items-center gap-2 text-sm text-muted">
            {first && status === "live" && (
              <span className="inline-block size-2 animate-pulse rounded-full bg-ok" aria-hidden />
            )}
            {first
              ? status === "live"
                ? "Waiting for the first request. It will appear on this page as soon as it arrives."
                : "Waiting for the first request. This page refreshes every few seconds."
              : "Headers, body, query, and the response it received, side by side."}
          </p>
        </Step>
      </ol>
    </Panel>
  );
}

function Step({
  n,
  done,
  disabled,
  title,
  children,
}: {
  n: number;
  done: boolean;
  disabled?: boolean;
  title: string;
  children: ReactNode;
}) {
  return (
    <li className={cn("flex gap-4 px-4 py-4", disabled && "opacity-60")}>
      <span
        className={cn(
          "flex size-6 shrink-0 items-center justify-center rounded-full border font-mono text-xs",
          done ? "border-ok bg-ok text-white" : "border-line-strong text-muted",
        )}
        aria-hidden
      >
        {done ? <Check className="size-3.5" /> : n}
      </span>
      <div className="min-w-0 flex-1 space-y-2">
        <h3 className="text-sm font-medium">
          {title}
          {done && <span className="sr-only"> (done)</span>}
        </h3>
        {children}
      </div>
    </li>
  );
}
