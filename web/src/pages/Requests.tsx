import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Download, GitCompareArrows, Inbox, Plus, Trash2, X } from "lucide-react";
import { useId, useMemo, useState, type ReactNode } from "react";
import { useNavigate, useSearchParams } from "react-router";
import { PageHeader } from "../components/layout/PageHeader";
import { useDocumentTitle } from "../hooks/useDocumentTitle";
import { RequestTable } from "../components/RequestTable";
import { useArrivalBaseline } from "../hooks/useArrivalBaseline";
import { useToast } from "../components/toast";
import { Button, LinkButton } from "../components/ui/button";
import { ConfirmDialog } from "../components/ui/dialog";
import { EmptyState, ErrorState, LoadingBlock } from "../components/ui/feedback";
import { Input, Select } from "../components/ui/form";
import { Pagination } from "../components/ui/Pagination";
import { Panel } from "../components/ui/misc";
import { api, errorMessage } from "../lib/api";
import { formatNumber, plural } from "../lib/format";
import { METHODS } from "../lib/http";
import { useEndpoints, useRequests, useStats } from "../lib/queries";
import type { Endpoint, RequestFilters } from "../types/api";

const SORTS: Record<string, { label: string; sort: string; order: string }> = {
  newest: { label: "Newest first", sort: "received_at", order: "desc" },
  oldest: { label: "Oldest first", sort: "received_at", order: "asc" },
  slowest: { label: "Slowest first", sort: "duration", order: "desc" },
  largest: { label: "Largest first", sort: "size", order: "desc" },
  status: { label: "Status, high to low", sort: "status", order: "desc" },
};

const FILTER_KEYS = ["endpoint_id", "method", "status", "source", "from", "to", "q"] as const;

/** Converts a datetime-local value to RFC 3339 for the API. */
function toRFC3339(local: string): string | undefined {
  if (!local) return undefined;
  const d = new Date(local);
  return Number.isNaN(d.getTime()) ? undefined : d.toISOString();
}

export function RequestsPage() {
  useDocumentTitle("Requests");
  const [params, setParams] = useSearchParams();
  const endpoints = useEndpoints();

  const get = (k: string) => params.get(k) ?? "";
  const sortKey = SORTS[get("sort")] ? get("sort") : "newest";
  const limit = [25, 50, 100, 200].includes(Number(get("limit"))) ? Number(get("limit")) : 50;
  const offset = Math.max(0, Number(get("offset")) || 0);

  const filters: RequestFilters = {
    endpoint_id: get("endpoint_id") || undefined,
    method: get("method") || undefined,
    status: get("status") || undefined,
    source: get("source") || undefined,
    from: toRFC3339(get("from")),
    to: toRFC3339(get("to")),
    q: get("q") || undefined,
    sort: SORTS[sortKey].sort,
    order: SORTS[sortKey].order,
    limit: String(limit),
    offset: String(offset),
  };
  const filterKey = JSON.stringify(filters);
  const selectionKey = JSON.stringify({
    ...filters,
    limit: undefined,
    offset: undefined,
    sort: undefined,
    order: undefined,
  });
  const active = FILTER_KEYS.filter((k) => get(k) !== "");

  const update = (patch: Record<string, string | undefined>, resetOffset = true) => {
    const next = new URLSearchParams(params);
    for (const [k, v] of Object.entries(patch)) {
      if (v) next.set(k, v);
      else next.delete(k);
    }
    if (resetOffset) next.delete("offset");
    setParams(next);
  };
  const clearFilters = () => {
    const next = new URLSearchParams(params);
    FILTER_KEYS.forEach((k) => next.delete(k));
    next.delete("offset");
    setParams(next);
  };

  const endpointList = endpoints.data ?? [];
  const currentEndpoint = endpointList.find((e) => String(e.id) === get("endpoint_id"));

  return (
    <div>
      <PageHeader
        title={currentEndpoint ? `Requests to ${currentEndpoint.name}` : "Requests"}
        description="Every request captured by your endpoints, newest first. Click a row to inspect it."
        actions={<DeleteAllButton endpoint={currentEndpoint} />}
      />

      <div className="mb-3 flex flex-wrap items-end gap-2" role="group" aria-label="Filters">
        <FilterSelect
          label="Endpoint"
          value={get("endpoint_id")}
          onChange={(v) => update({ endpoint_id: v })}
        >
          <option value="">All endpoints</option>
          {endpointList.map((e) => (
            <option key={e.id} value={e.id}>
              {e.name}
            </option>
          ))}
        </FilterSelect>
        <FilterSelect label="Method" value={get("method")} onChange={(v) => update({ method: v })}>
          <option value="">Any method</option>
          {METHODS.map((m) => (
            <option key={m} value={m}>
              {m}
            </option>
          ))}
        </FilterSelect>
        <FilterSelect label="Status" value={get("status")} onChange={(v) => update({ status: v })}>
          <option value="">Any status</option>
          <option value="2xx">2xx success</option>
          <option value="3xx">3xx redirect</option>
          <option value="4xx">4xx client error</option>
          <option value="5xx">5xx server error</option>
        </FilterSelect>
        <FilterSelect label="Source" value={get("source")} onChange={(v) => update({ source: v })}>
          <option value="">Any source</option>
          <option value="capture">Captured</option>
          <option value="replay">Replayed</option>
          <option value="import">Imported</option>
        </FilterSelect>
        <label className="flex flex-col gap-1 text-xs text-muted">
          From
          <Input
            type="datetime-local"
            value={get("from")}
            onChange={(e) => update({ from: e.target.value })}
            className="w-44"
          />
        </label>
        <label className="flex flex-col gap-1 text-xs text-muted">
          To
          <Input
            type="datetime-local"
            value={get("to")}
            onChange={(e) => update({ to: e.target.value })}
            className="w-44"
          />
        </label>
        <FilterSelect
          label="Sort"
          value={sortKey}
          onChange={(v) => update({ sort: v === "newest" ? undefined : v })}
        >
          {Object.entries(SORTS).map(([k, s]) => (
            <option key={k} value={k}>
              {s.label}
            </option>
          ))}
        </FilterSelect>
        {active.length > 0 && (
          <Button variant="ghost" icon={<X className="size-3.5" aria-hidden />} onClick={clearFilters}>
            Clear filters
          </Button>
        )}
      </div>
      {get("q") && (
        <p className="mb-3 flex items-center gap-2 text-sm text-muted">
          Showing requests whose path, query, headers, or body contain
          <span className="inline-flex items-center gap-1 rounded bg-accent-soft px-1.5 py-0.5 font-mono text-xs text-accent-fg">
            {get("q")}
            <button
              type="button"
              onClick={() => update({ q: undefined })}
              aria-label="Clear search"
              className="hover:text-fg"
            >
              <X className="size-3" aria-hidden />
            </button>
          </span>
        </p>
      )}

      <Results
        key={selectionKey}
        viewKey={filterKey}
        highlightNew={sortKey === "newest" && offset === 0}
        filters={filters}
        hasFilters={active.length > 0}
        endpoints={endpointList}
        onClearFilters={clearFilters}
        onPage={(o, l) =>
          update({ offset: o ? String(o) : undefined, limit: l === 50 ? undefined : String(l) }, false)
        }
      />
    </div>
  );
}

function FilterSelect({
  label,
  value,
  onChange,
  children,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  children: ReactNode;
}) {
  const id = useId();
  return (
    <div className="flex flex-col gap-1 text-xs text-muted">
      <label htmlFor={id}>{label}</label>
      <Select id={id} value={value} onChange={(e) => onChange(e.target.value)} className="w-auto min-w-32">
        {children}
      </Select>
    </div>
  );
}

interface ResultsProps {
  viewKey: string;
  highlightNew: boolean;
  filters: RequestFilters;
  hasFilters: boolean;
  endpoints: Endpoint[];
  onClearFilters: () => void;
  onPage: (offset: number, limit: number) => void;
}

function Results({
  viewKey,
  highlightNew,
  filters,
  hasFilters,
  endpoints,
  onClearFilters,
  onPage,
}: ResultsProps) {
  const requests = useRequests(filters);
  const baseline = useArrivalBaseline(
    requests.data?.items,
    viewKey,
    requests.isSuccess && !requests.isPlaceholderData,
  );
  const navigate = useNavigate();
  const qc = useQueryClient();
  const toast = useToast();
  const [selected, setSelected] = useState<Set<number>>(new Set());
  const [confirmDelete, setConfirmDelete] = useState(false);

  const items = useMemo(() => requests.data?.items ?? [], [requests.data]);
  const toggle = (id: number) =>
    setSelected((s) => {
      const next = new Set(s);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  const toggleAll = (checked: boolean) =>
    setSelected((s) => {
      const next = new Set(s);
      for (const r of items) {
        if (checked) next.add(r.id);
        else next.delete(r.id);
      }
      return next;
    });

  const ids = [...selected];
  const remove = useMutation({
    mutationFn: () => api.deleteRequests({ scope: "ids", ids }),
    onSuccess: (res) => {
      setSelected(new Set());
      setConfirmDelete(false);
      void qc.invalidateQueries();
      toast.success(`Deleted ${plural(res.deleted, "request")}.`);
    },
    onError: (err) => toast.error(errorMessage(err)),
  });
  const exportSelected = useMutation({
    mutationFn: () => api.exportFixture({ request_ids: ids, include_bodies: true }),
    onError: (err) => toast.error(errorMessage(err)),
  });

  if (requests.isPending)
    return (
      <Panel>
        <LoadingBlock />
      </Panel>
    );
  if (requests.isError) {
    return (
      <Panel>
        <ErrorState error={requests.error} onRetry={() => requests.refetch()} />
      </Panel>
    );
  }
  const page = requests.data;

  if (page.total === 0) {
    return (
      <Panel>
        {hasFilters ? (
          <EmptyState
            title="No requests match these filters"
            action={<Button onClick={onClearFilters}>Clear filters</Button>}
          >
            Try a wider date range or a different status.
          </EmptyState>
        ) : endpoints.length === 0 ? (
          <EmptyState
            icon={<Inbox className="size-6" aria-hidden />}
            title="No requests yet"
            action={
              <LinkButton
                to="/endpoints?new=1"
                variant="primary"
                icon={<Plus className="size-3.5" aria-hidden />}
              >
                Create an endpoint
              </LinkButton>
            }
          >
            Create an endpoint, then send a request to its URL.
          </EmptyState>
        ) : (
          <EmptyState icon={<Inbox className="size-6" aria-hidden />} title="No requests yet">
            Send a request to one of your endpoint URLs. It appears here as soon as it arrives.
          </EmptyState>
        )}
      </Panel>
    );
  }

  return (
    <Panel>
      {selected.size > 0 && (
        <div className="flex flex-wrap items-center gap-2 border-b border-line bg-accent-soft/50 px-4 py-2 text-sm">
          <span className="font-medium">{formatNumber(selected.size)} selected</span>
          <Button
            size="sm"
            icon={<GitCompareArrows className="size-3.5" aria-hidden />}
            disabled={selected.size !== 2}
            title={selected.size !== 2 ? "Select exactly two requests to compare" : undefined}
            onClick={() => {
              const [a, b] = ids.sort((x, y) => x - y);
              navigate(`/compare?left=${a}&right=${b}`);
            }}
          >
            Compare
          </Button>
          <Button
            size="sm"
            icon={<Download className="size-3.5" aria-hidden />}
            loading={exportSelected.isPending}
            onClick={() => exportSelected.mutate()}
          >
            Export
          </Button>
          <Button
            size="sm"
            variant="danger"
            icon={<Trash2 className="size-3.5" aria-hidden />}
            onClick={() => setConfirmDelete(true)}
          >
            Delete
          </Button>
          <Button size="sm" variant="ghost" onClick={() => setSelected(new Set())}>
            Clear selection
          </Button>
        </div>
      )}
      <div className={requests.isPlaceholderData ? "opacity-60 transition-opacity" : undefined}>
        <RequestTable
          items={items}
          selected={selected}
          onToggle={toggle}
          onToggleAll={toggleAll}
          newerThan={highlightNew ? baseline : undefined}
        />
      </div>
      <div className="border-t border-line">
        <Pagination total={page.total} limit={page.limit} offset={page.offset} onChange={onPage} />
      </div>
      <ConfirmDialog
        open={confirmDelete}
        title={`Delete ${plural(selected.size, "request")}?`}
        confirmLabel="Delete"
        danger
        busy={remove.isPending}
        onConfirm={() => remove.mutate()}
        onCancel={() => setConfirmDelete(false)}
      >
        Only the selected requests are deleted. This cannot be undone.
      </ConfirmDialog>
    </Panel>
  );
}

function DeleteAllButton({ endpoint }: { endpoint?: Endpoint }) {
  const qc = useQueryClient();
  const toast = useToast();
  const stats = useStats();
  const [open, setOpen] = useState(false);
  const count = endpoint ? endpoint.request_count : (stats.data?.total_requests ?? 0);
  const remove = useMutation({
    mutationFn: () =>
      endpoint
        ? api.deleteRequests({ scope: "endpoint", endpoint_id: endpoint.id })
        : api.deleteRequests({ scope: "all" }),
    onSuccess: (res) => {
      setOpen(false);
      void qc.invalidateQueries();
      toast.success(`Deleted ${plural(res.deleted, "request")}.`);
    },
    onError: (err) => toast.error(errorMessage(err)),
  });
  return (
    <>
      <Button
        icon={<Trash2 className="size-3.5" aria-hidden />}
        disabled={count === 0}
        onClick={() => setOpen(true)}
      >
        {endpoint ? "Clear endpoint" : "Delete all"}
      </Button>
      <ConfirmDialog
        open={open}
        title={endpoint ? `Delete all requests for ${endpoint.name}?` : "Delete every stored request?"}
        confirmLabel={`Delete ${plural(count, "request")}`}
        danger
        busy={remove.isPending}
        onConfirm={() => remove.mutate()}
        onCancel={() => setOpen(false)}
      >
        {endpoint
          ? `This deletes ${plural(count, "request")} captured by ${endpoint.name}. Other endpoints are not affected.`
          : `This deletes ${plural(count, "request")} across all endpoints. Endpoints and their URLs are kept.`}{" "}
        Method, status, date, and search filters do not narrow this action. This cannot be undone.
      </ConfirmDialog>
    </>
  );
}
