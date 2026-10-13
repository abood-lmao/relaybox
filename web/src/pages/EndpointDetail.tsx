import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Download, List, Pencil, RefreshCw, Trash2, Upload } from "lucide-react";
import { useRef, useState, type FormEvent } from "react";
import { Link, useLocation, useNavigate, useParams } from "react-router";
import { CurlExamples } from "../components/endpoint/CurlExamples";
import { MockResponseEditor } from "../components/endpoint/MockResponseEditor";
import { PageHeader } from "../components/layout/PageHeader";
import { useDocumentTitle } from "../hooks/useDocumentTitle";
import { RequestTable } from "../components/RequestTable";
import { useToast } from "../components/toast";
import { Button, LinkButton } from "../components/ui/button";
import { ConfirmDialog, Dialog } from "../components/ui/dialog";
import { EmptyState, ErrorState, LoadingBlock, Notice } from "../components/ui/feedback";
import { Checkbox, Field, Input, Select, Switch } from "../components/ui/form";
import { Badge, CopyButton, Panel } from "../components/ui/misc";
import { ApiError, api, errorMessage } from "../lib/api";
import { formatBytes, formatDateTime, formatNumber, plural } from "../lib/format";
import { useEndpoint, useMeta, useRequests } from "../lib/queries";
import type { Endpoint, Meta } from "../types/api";

export function EndpointDetailPage() {
  const params = useParams();
  const id = Number(params.id);
  const endpoint = useEndpoint(id);
  const meta = useMeta();
  useDocumentTitle(endpoint.data?.name ?? "Endpoint");

  if (!Number.isInteger(id) || id <= 0) {
    return (
      <ErrorState error={new Error("This is not a valid endpoint address.")} title="Endpoint not found" />
    );
  }
  if (endpoint.isPending || meta.isPending) return <LoadingBlock />;
  if (endpoint.isError) {
    const missing = endpoint.error instanceof ApiError && endpoint.error.status === 404;
    return (
      <EmptyState
        title={missing ? "Endpoint not found" : "The endpoint could not be loaded"}
        action={<LinkButton to="/endpoints">Back to endpoints</LinkButton>}
      >
        {missing ? "It may have been deleted." : errorMessage(endpoint.error)}
      </EmptyState>
    );
  }
  if (meta.isError) return <ErrorState error={meta.error} onRetry={() => meta.refetch()} />;
  return <EndpointView endpoint={endpoint.data} meta={meta.data} />;
}

function EndpointView({ endpoint: ep, meta }: { endpoint: Endpoint; meta: Meta }) {
  const qc = useQueryClient();
  const toast = useToast();
  const location = useLocation();
  const justCreated = (location.state as { created?: boolean } | null)?.created === true;
  const [renaming, setRenaming] = useState(false);

  const refresh = () => {
    void qc.invalidateQueries({ queryKey: ["endpoints"] });
    void qc.invalidateQueries({ queryKey: ["stats"] });
  };
  const update = useMutation({
    mutationFn: (input: Parameters<typeof api.updateEndpoint>[1]) => api.updateEndpoint(ep.id, input),
    onSuccess: (updated) => {
      qc.setQueryData(["endpoints", ep.id], updated);
      refresh();
    },
  });
  const toggle = useMutation({
    mutationFn: (enabled: boolean) => api.updateEndpoint(ep.id, { enabled }),
    onSuccess: (updated) => {
      qc.setQueryData(["endpoints", ep.id], updated);
      refresh();
      toast.success(updated.enabled ? "Endpoint enabled." : "Endpoint disabled. It now answers 503.");
    },
    onError: (err) => toast.error(errorMessage(err)),
  });

  return (
    <div>
      <PageHeader
        back={{ to: "/endpoints", label: "Endpoints" }}
        title={
          <span className="inline-flex items-center gap-2">
            {ep.name}
            <button
              type="button"
              onClick={() => setRenaming(true)}
              className="rounded p-1 text-faint hover:bg-surface-2 hover:text-fg"
              aria-label="Rename endpoint"
              title="Rename"
            >
              <Pencil className="size-3.5" aria-hidden />
            </button>
          </span>
        }
        meta={
          <>
            {!ep.enabled && <Badge tone="warn">disabled</Badge>}
            <span>Created {formatDateTime(ep.created_at)}</span>
            <Link to={`/requests?endpoint_id=${ep.id}`} className="hover:text-fg hover:underline">
              {plural(ep.request_count, "request")}
            </Link>
          </>
        }
        actions={
          <>
            <Switch
              checked={ep.enabled}
              onChange={(v) => toggle.mutate(v)}
              disabled={toggle.isPending}
              label="Accept requests"
              showLabel
            />
            <LinkButton
              to={`/requests?endpoint_id=${ep.id}`}
              icon={<List className="size-3.5" aria-hidden />}
            >
              View requests
            </LinkButton>
          </>
        }
      />

      {justCreated && (
        <Notice className="mb-4" title="Endpoint created">
          Send a request to the URL below. It appears under Requests as soon as it arrives.
        </Notice>
      )}

      <div className="grid gap-4 xl:grid-cols-[minmax(0,1fr)_380px]">
        <div className="min-w-0 space-y-4">
          <UrlPanel endpoint={ep} meta={meta} />
          <Panel
            title="Mock response"
            description="What this endpoint answers to every request it captures."
            bodyClassName="p-4"
          >
            <MockResponseEditor
              value={ep.mock_response}
              meta={meta}
              saving={update.isPending && update.variables?.mock_response !== undefined}
              error={update.variables?.mock_response !== undefined ? update.error : undefined}
              onSave={(mock) =>
                update.mutate(
                  { mock_response: mock },
                  { onSuccess: () => toast.success("Mock response saved.") },
                )
              }
            />
          </Panel>
          <RecentRequests endpoint={ep} />
        </div>
        <div className="min-w-0 space-y-4">
          <BodyLimitPanel endpoint={ep} meta={meta} />
          <FixturesPanel endpoint={ep} />
          <DangerPanel endpoint={ep} />
        </div>
      </div>

      <RenameDialog open={renaming} endpoint={ep} onClose={() => setRenaming(false)} />
    </div>
  );
}

function UrlPanel({ endpoint: ep, meta }: { endpoint: Endpoint; meta: Meta }) {
  const qc = useQueryClient();
  const toast = useToast();
  const [confirm, setConfirm] = useState(false);
  const rotate = useMutation({
    mutationFn: () => api.rotateEndpointURL(ep.id),
    onSuccess: (updated) => {
      qc.setQueryData(["endpoints", ep.id], updated);
      void qc.invalidateQueries({ queryKey: ["endpoints"] });
      setConfirm(false);
      toast.success("The endpoint has a new URL. The old one no longer works.");
    },
    onError: (err) => toast.error(errorMessage(err)),
  });
  return (
    <Panel
      title="Endpoint URL"
      actions={
        <Button
          size="sm"
          variant="ghost"
          icon={<RefreshCw className="size-3.5" aria-hidden />}
          onClick={() => setConfirm(true)}
        >
          New URL
        </Button>
      }
      bodyClassName="space-y-4 p-4"
    >
      <div className="flex items-center gap-2 rounded-md border border-line-strong bg-surface-2/50 py-1 pr-1 pl-3">
        <code className="min-w-0 flex-1 font-mono text-[13px] break-all">{ep.url}</code>
        <CopyButton text={ep.url} label="Copy URL" showLabel />
      </div>
      <ul className="space-y-1 text-xs text-muted">
        <li>
          Accepts {meta.allowed_methods.join(", ")}. Sub-paths such as{" "}
          <code className="font-mono">/orders/created</code> are captured too.
        </li>
        <li>Anyone who has this URL can send requests to it. Get a new URL if it leaks.</li>
        {!ep.enabled && (
          <li className="text-warn">The endpoint is disabled and answers 503 without storing anything.</li>
        )}
      </ul>
      <CurlExamples url={ep.url} />
      <ConfirmDialog
        open={confirm}
        title="Replace the endpoint URL?"
        confirmLabel="Replace URL"
        busy={rotate.isPending}
        onConfirm={() => rotate.mutate()}
        onCancel={() => setConfirm(false)}
      >
        Senders using the current URL will get 404 responses. Requests already captured are kept.
      </ConfirmDialog>
    </Panel>
  );
}

function RecentRequests({ endpoint: ep }: { endpoint: Endpoint }) {
  const requests = useRequests({ endpoint_id: String(ep.id), limit: "10" });
  return (
    <Panel
      title="Recent requests"
      actions={
        ep.request_count > 0 && (
          <Link
            to={`/requests?endpoint_id=${ep.id}`}
            className="text-xs font-medium text-accent-fg hover:underline"
          >
            View all {formatNumber(ep.request_count)}
          </Link>
        )
      }
    >
      {requests.isPending ? (
        <LoadingBlock />
      ) : requests.isError ? (
        <ErrorState error={requests.error} onRetry={() => requests.refetch()} />
      ) : requests.data.items.length === 0 ? (
        <EmptyState title="No requests yet" className="py-10">
          Requests sent to this endpoint show up here.
        </EmptyState>
      ) : (
        <RequestTable items={requests.data.items} showEndpoint={false} compact />
      )}
    </Panel>
  );
}

const units = { KiB: 1024, MiB: 1024 * 1024 } as const;
type Unit = keyof typeof units;

function splitBytes(n: number): { amount: string; unit: Unit } {
  if (n % units.MiB === 0) return { amount: String(n / units.MiB), unit: "MiB" };
  return { amount: String(Math.round((n / units.KiB) * 100) / 100), unit: "KiB" };
}

function BodyLimitPanel({ endpoint: ep, meta }: { endpoint: Endpoint; meta: Meta }) {
  const qc = useQueryClient();
  const toast = useToast();
  const initial = splitBytes(ep.max_body_bytes ?? meta.limits.default_max_body_bytes);
  const [custom, setCustom] = useState(ep.max_body_bytes !== null);
  const [amount, setAmount] = useState(initial.amount);
  const [unit, setUnit] = useState<Unit>(initial.unit);
  const save = useMutation({
    mutationFn: (value: number | null) => api.updateEndpoint(ep.id, { max_body_bytes: value }),
    onSuccess: (updated) => {
      qc.setQueryData(["endpoints", ep.id], updated);
      void qc.invalidateQueries({ queryKey: ["endpoints"] });
      toast.success("Body limit saved.");
    },
  });
  const bytes = Math.round(Number(amount) * units[unit]);
  const fieldError =
    save.error instanceof ApiError
      ? (save.error.fields.max_body_bytes ?? save.error.message)
      : save.error
        ? errorMessage(save.error)
        : undefined;
  const submit = (e: FormEvent) => {
    e.preventDefault();
    save.mutate(custom ? bytes : null);
  };
  const unchanged = custom ? ep.max_body_bytes === bytes : ep.max_body_bytes === null;

  return (
    <Panel title="Body size limit" bodyClassName="p-4">
      <form onSubmit={submit} className="space-y-3">
        <p className="text-xs text-muted">
          Bodies larger than the limit are stored truncated and marked as such. Currently{" "}
          <span className="font-medium text-fg">{formatBytes(ep.effective_max_body_bytes)}</span>.
        </p>
        <label className="flex items-center gap-2 text-sm">
          <input
            type="radio"
            name="limit"
            checked={!custom}
            onChange={() => setCustom(false)}
            className="accent-accent"
          />
          Use the default ({formatBytes(meta.limits.default_max_body_bytes)})
        </label>
        <label className="flex items-center gap-2 text-sm">
          <input
            type="radio"
            name="limit"
            checked={custom}
            onChange={() => setCustom(true)}
            className="accent-accent"
          />
          Custom limit
        </label>
        {custom && (
          <div className="flex gap-2 pl-6">
            <Input
              aria-label="Limit"
              inputMode="decimal"
              value={amount}
              onChange={(e) => setAmount(e.target.value.replace(/[^\d.]/g, ""))}
              className="w-28 font-mono"
              aria-invalid={fieldError ? true : undefined}
            />
            <Select
              aria-label="Unit"
              value={unit}
              onChange={(e) => setUnit(e.target.value as Unit)}
              className="w-24"
            >
              <option value="KiB">KiB</option>
              <option value="MiB">MiB</option>
            </Select>
          </div>
        )}
        {fieldError && <p className="text-xs text-err">{fieldError}</p>}
        <p className="text-xs text-faint">
          Allowed range {formatBytes(meta.limits.min_body_bytes)} to{" "}
          {formatBytes(meta.limits.max_body_bytes_ceiling)}.
        </p>
        <Button
          type="submit"
          size="sm"
          loading={save.isPending}
          disabled={unchanged || (custom && !(bytes > 0))}
        >
          Save limit
        </Button>
      </form>
    </Panel>
  );
}

function FixturesPanel({ endpoint: ep }: { endpoint: Endpoint }) {
  const qc = useQueryClient();
  const toast = useToast();
  const fileRef = useRef<HTMLInputElement>(null);
  const [limit, setLimit] = useState("100");
  const [includeBodies, setIncludeBodies] = useState(true);
  const [importErrors, setImportErrors] = useState<string[] | null>(null);

  const exportMutation = useMutation({
    mutationFn: () =>
      api.exportFixture({ endpoint_id: ep.id, limit: Number(limit) || 100, include_bodies: includeBodies }),
    onError: (err) => toast.error(errorMessage(err)),
  });
  const importMutation = useMutation({
    mutationFn: async (file: File) => api.importFixture(ep.id, await file.text()),
    onSuccess: (res) => {
      setImportErrors(null);
      void qc.invalidateQueries({ queryKey: ["requests"] });
      void qc.invalidateQueries({ queryKey: ["endpoints"] });
      void qc.invalidateQueries({ queryKey: ["stats"] });
      toast.success(`Imported ${plural(res.imported, "request")} into ${ep.name}.`);
    },
    onError: (err) => {
      if (err instanceof ApiError && err.details.length > 0) setImportErrors(err.details);
      else setImportErrors([errorMessage(err)]);
    },
  });

  return (
    <Panel
      title="Fixtures"
      description="Save requests to a JSON file, or load them back."
      bodyClassName="space-y-5 p-4"
    >
      <div className="space-y-2">
        <h3 className="text-xs font-medium">Export</h3>
        <div className="flex items-center gap-2">
          <Input
            aria-label="Number of most recent requests to export"
            inputMode="numeric"
            value={limit}
            onChange={(e) => setLimit(e.target.value.replace(/\D/g, "").slice(0, 4))}
            className="w-20 font-mono"
          />
          <span className="text-xs text-muted">most recent requests (up to 1,000)</span>
        </div>
        <label className="flex items-center gap-2 text-sm">
          <Checkbox checked={includeBodies} onChange={(e) => setIncludeBodies(e.target.checked)} />
          Include bodies
        </label>
        <Button
          size="sm"
          icon={<Download className="size-3.5" aria-hidden />}
          loading={exportMutation.isPending}
          disabled={ep.request_count === 0}
          onClick={() => exportMutation.mutate()}
        >
          Export fixture
        </Button>
      </div>
      <div className="space-y-2 border-t border-line pt-4">
        <h3 className="text-xs font-medium">Import</h3>
        <p className="text-xs text-muted">
          Requests are stored in this endpoint as imported. Nothing is sent anywhere, and the current
          redaction rules and body limit apply.
        </p>
        <input
          ref={fileRef}
          type="file"
          accept="application/json,.json"
          className="sr-only"
          aria-label="Fixture file"
          onChange={(e) => {
            const file = e.target.files?.[0];
            if (file) importMutation.mutate(file);
            e.target.value = "";
          }}
        />
        <Button
          size="sm"
          icon={<Upload className="size-3.5" aria-hidden />}
          loading={importMutation.isPending}
          onClick={() => fileRef.current?.click()}
        >
          Import fixture
        </Button>
        {importErrors && (
          <Notice tone="error" title="The fixture was not imported">
            <ul className="list-disc space-y-0.5 pl-4">
              {importErrors.slice(0, 8).map((d, i) => (
                <li key={i}>{d}</li>
              ))}
              {importErrors.length > 8 && <li>{importErrors.length - 8} more problems</li>}
            </ul>
          </Notice>
        )}
      </div>
    </Panel>
  );
}

function DangerPanel({ endpoint: ep }: { endpoint: Endpoint }) {
  const qc = useQueryClient();
  const toast = useToast();
  const navigate = useNavigate();
  const [action, setAction] = useState<"clear" | "delete" | null>(null);
  const clear = useMutation({
    mutationFn: () => api.deleteRequests({ scope: "endpoint", endpoint_id: ep.id }),
    onSuccess: (res) => {
      setAction(null);
      void qc.invalidateQueries();
      toast.success(`Deleted ${plural(res.deleted, "request")}.`);
    },
    onError: (err) => toast.error(errorMessage(err)),
  });
  const remove = useMutation({
    mutationFn: () => api.deleteEndpoint(ep.id),
    onSuccess: (res) => {
      setAction(null);
      qc.removeQueries({ queryKey: ["endpoints", ep.id] });
      void qc.invalidateQueries();
      toast.success(`Deleted ${ep.name} and ${plural(res.deleted_requests, "request")}.`);
      navigate("/endpoints");
    },
    onError: (err) => toast.error(errorMessage(err)),
  });

  return (
    <Panel title="Delete" bodyClassName="space-y-3 p-4">
      <div className="flex items-center justify-between gap-3">
        <p className="text-xs text-muted">Remove every captured request, keep the endpoint and its URL.</p>
        <Button size="sm" disabled={ep.request_count === 0} onClick={() => setAction("clear")}>
          Clear requests
        </Button>
      </div>
      <div className="flex items-center justify-between gap-3 border-t border-line pt-3">
        <p className="text-xs text-muted">Delete the endpoint, its URL, and its requests.</p>
        <Button
          size="sm"
          variant="danger"
          icon={<Trash2 className="size-3.5" aria-hidden />}
          onClick={() => setAction("delete")}
        >
          Delete
        </Button>
      </div>
      <ConfirmDialog
        open={action === "clear"}
        title={`Delete all requests for ${ep.name}?`}
        confirmLabel={`Delete ${plural(ep.request_count, "request")}`}
        danger
        busy={clear.isPending}
        onConfirm={() => clear.mutate()}
        onCancel={() => setAction(null)}
      >
        This deletes {plural(ep.request_count, "captured request")} from this endpoint only. Other endpoints
        are not affected. This cannot be undone.
      </ConfirmDialog>
      <ConfirmDialog
        open={action === "delete"}
        title={`Delete ${ep.name}?`}
        confirmLabel="Delete endpoint"
        danger
        busy={remove.isPending}
        onConfirm={() => remove.mutate()}
        onCancel={() => setAction(null)}
      >
        The URL stops working immediately and {plural(ep.request_count, "captured request")} are deleted with
        it. This cannot be undone.
      </ConfirmDialog>
    </Panel>
  );
}

function RenameDialog({
  open,
  endpoint: ep,
  onClose,
}: {
  open: boolean;
  endpoint: Endpoint;
  onClose: () => void;
}) {
  const qc = useQueryClient();
  const toast = useToast();
  const [name, setName] = useState(ep.name);
  const [wasOpen, setWasOpen] = useState(open);
  if (open !== wasOpen) {
    setWasOpen(open);
    if (open) setName(ep.name);
  }
  const rename = useMutation({
    mutationFn: (n: string) => api.updateEndpoint(ep.id, { name: n }),
    onSuccess: (updated) => {
      qc.setQueryData(["endpoints", ep.id], updated);
      void qc.invalidateQueries({ queryKey: ["endpoints"] });
      void qc.invalidateQueries({ queryKey: ["requests"] });
      toast.success("Endpoint renamed.");
      onClose();
    },
  });
  const error =
    rename.error instanceof ApiError
      ? (rename.error.fields.name ?? rename.error.message)
      : rename.error
        ? errorMessage(rename.error)
        : undefined;
  const close = () => {
    rename.reset();
    onClose();
  };
  return (
    <Dialog open={open} onClose={close} title="Rename endpoint">
      <form
        onSubmit={(e) => {
          e.preventDefault();
          if (name.trim()) rename.mutate(name.trim());
        }}
        className="space-y-4"
      >
        <Field label="Name" error={error}>
          {(p) => (
            <Input {...p} autoFocus maxLength={80} value={name} onChange={(e) => setName(e.target.value)} />
          )}
        </Field>
        <div className="flex justify-end gap-2">
          <Button onClick={close}>Cancel</Button>
          <Button
            type="submit"
            variant="primary"
            loading={rename.isPending}
            disabled={!name.trim() || name.trim() === ep.name}
          >
            Save
          </Button>
        </div>
      </form>
    </Dialog>
  );
}
