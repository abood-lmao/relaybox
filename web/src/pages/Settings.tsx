import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Download, Eraser, RotateCcw, Trash2 } from "lucide-react";
import { useState, type FormEvent, type ReactNode } from "react";
import { PageHeader } from "../components/layout/PageHeader";
import { useDocumentTitle } from "../hooks/useDocumentTitle";
import { useToast } from "../components/toast";
import { Button } from "../components/ui/button";
import { ConfirmDialog } from "../components/ui/dialog";
import { ErrorState, LoadingBlock } from "../components/ui/feedback";
import { Checkbox, Field, Input, Select, Textarea } from "../components/ui/form";
import { Badge, CopyButton, Facts, Panel } from "../components/ui/misc";
import { ApiError, api, errorMessage } from "../lib/api";
import { formatBytes, formatDateTime, formatNumber, plural } from "../lib/format";
import { useEndpoints, useSettings, useStats } from "../lib/queries";
import type { SettingKey, Settings, SettingsPatch } from "../types/api";

export function SettingsPage() {
  useDocumentTitle("Settings");
  const settings = useSettings();
  if (settings.isPending) return <LoadingBlock />;
  if (settings.isError) return <ErrorState error={settings.error} onRetry={() => settings.refetch()} />;
  const s = settings.data;
  return (
    <div className="max-w-5xl">
      <PageHeader
        title="Settings"
        description="Runtime settings apply immediately and are stored in the database. Server settings come from environment variables and need a restart."
      />
      <div className="space-y-4">
        <RetentionSection s={s} />
        <LimitsSection s={s} />
        <RedactionSection s={s} />
        <ReplaySection s={s} />
        <ServerSection s={s} />
        <DataSection />
      </div>
    </div>
  );
}

function Applies({ restart }: { restart?: boolean }) {
  return restart ? <Badge tone="warn">Restart required</Badge> : <Badge tone="ok">Applies immediately</Badge>;
}

function useSaveSettings(onSaved?: () => void) {
  const qc = useQueryClient();
  const toast = useToast();
  return useMutation({
    mutationFn: (patch: SettingsPatch) => api.updateSettings(patch),
    onSuccess: (next) => {
      qc.setQueryData(["settings"], next);
      void qc.invalidateQueries({ queryKey: ["meta"] });
      void qc.invalidateQueries({ queryKey: ["endpoints"] });
      toast.success("Settings saved.");
      onSaved?.();
    },
  });
}

function fieldError(err: unknown, key: string): string | undefined {
  if (err instanceof ApiError) return err.fields[key];
  return undefined;
}

function generalError(err: unknown): string | undefined {
  if (!err) return undefined;
  if (err instanceof ApiError && err.status === 422) return undefined;
  return errorMessage(err);
}

function DefaultNote({ s, k, format }: { s: Settings; k: SettingKey; format: (v: never) => string }) {
  const save = useSaveSettings();
  const overridden = s.overridden.includes(k);
  return (
    <span className="flex items-center gap-2">
      <span>Default {format(s.defaults[k] as never)}</span>
      {overridden && (
        <button
          type="button"
          onClick={() => save.mutate({ reset: [k] })}
          className="inline-flex items-center gap-1 text-accent-fg hover:underline"
          disabled={save.isPending}
        >
          <RotateCcw className="size-3" aria-hidden />
          Reset
        </button>
      )}
    </span>
  );
}

/** Keeps a form draft in sync with the saved value it was created from. */
function useDraft<T>(saved: T, toDraft: (v: T) => string) {
  const [draft, setDraft] = useState(() => toDraft(saved));
  const [base, setBase] = useState(saved);
  if (saved !== base) {
    setBase(saved);
    setDraft(toDraft(saved));
  }
  return [draft, setDraft] as const;
}

const days = (n: number) => (n === 0 ? "keep forever" : plural(n, "day"));
const count = (n: number) => (n === 0 ? "no limit" : formatNumber(n));

function RetentionSection({ s }: { s: Settings }) {
  const toast = useToast();
  const qc = useQueryClient();
  const [retention, setRetention] = useDraft(s.runtime.retention_days, String);
  const [maxStored, setMaxStored] = useDraft(s.runtime.max_stored_requests, String);
  const save = useSaveSettings();
  const cleanup = useMutation({
    mutationFn: api.cleanup,
    onSuccess: (res) => {
      void qc.invalidateQueries();
      toast.success(
        res.expired + res.trimmed === 0
          ? "Nothing to clean up."
          : `Removed ${plural(res.expired, "expired request")} and ${plural(res.trimmed, "request")} over the limit.`,
      );
    },
    onError: (err) => toast.error(errorMessage(err)),
  });
  const submit = (e: FormEvent) => {
    e.preventDefault();
    save.mutate({ retention_days: Number(retention), max_stored_requests: Number(maxStored) });
  };
  const dirty =
    Number(retention) !== s.runtime.retention_days || Number(maxStored) !== s.runtime.max_stored_requests;

  return (
    <Panel title="Retention" actions={<Applies />} bodyClassName="p-4">
      <form onSubmit={submit} className="space-y-4">
        <p className="text-sm text-muted">
          Old requests are removed automatically in the background. Changes take effect on the next cleanup
          pass, or right away with Run cleanup now.
        </p>
        <div className="grid gap-4 sm:grid-cols-2">
          <Field
            label="Keep requests for (days)"
            error={fieldError(save.error, "retention_days")}
            hint={<DefaultNote s={s} k="retention_days" format={days} />}
          >
            {(p) => (
              <Input
                {...p}
                inputMode="numeric"
                value={retention}
                onChange={(e) => setRetention(e.target.value.replace(/\D/g, "").slice(0, 5))}
                className="font-mono"
              />
            )}
          </Field>
          <Field
            label="Maximum stored requests"
            error={fieldError(save.error, "max_stored_requests")}
            hint={<DefaultNote s={s} k="max_stored_requests" format={count} />}
          >
            {(p) => (
              <Input
                {...p}
                inputMode="numeric"
                value={maxStored}
                onChange={(e) => setMaxStored(e.target.value.replace(/\D/g, "").slice(0, 9))}
                className="font-mono"
              />
            )}
          </Field>
        </div>
        <p className="text-xs text-faint">
          Use 0 to keep requests forever or to store any number. Retention is capped at{" "}
          {formatNumber(s.limits.max_retention_days)} days and the request limit at{" "}
          {formatNumber(s.limits.max_stored_requests_limit)}.
        </p>
        {generalError(save.error) && <p className="text-sm text-err">{generalError(save.error)}</p>}
        <div className="flex flex-wrap gap-2">
          <Button
            type="submit"
            variant="primary"
            loading={save.isPending}
            disabled={!dirty || retention === "" || maxStored === ""}
          >
            Save
          </Button>
          <Button
            icon={<Eraser className="size-3.5" aria-hidden />}
            loading={cleanup.isPending}
            onClick={() => cleanup.mutate()}
          >
            Run cleanup now
          </Button>
        </div>
      </form>
    </Panel>
  );
}

const KiB = 1024;
const MiB = 1024 * 1024;

function LimitsSection({ s }: { s: Settings }) {
  const toBest = (n: number) =>
    n % MiB === 0 ? `${n / MiB}:MiB` : `${Math.round((n / KiB) * 100) / 100}:KiB`;
  const [value, setValue] = useDraft(s.runtime.default_max_body_bytes, toBest);
  const [amount, unit] = value.split(":");
  const bytes = Math.round(Number(amount) * (unit === "MiB" ? MiB : KiB));
  const save = useSaveSettings();
  return (
    <Panel title="Payload limits" actions={<Applies />} bodyClassName="p-4">
      <form
        onSubmit={(e) => {
          e.preventDefault();
          save.mutate({ default_max_body_bytes: bytes });
        }}
        className="space-y-4"
      >
        <Field
          label="Default body limit"
          error={fieldError(save.error, "default_max_body_bytes")}
          hint={<DefaultNote s={s} k="default_max_body_bytes" format={(n: number) => formatBytes(n)} />}
        >
          {(p) => (
            <div className="flex gap-2">
              <Input
                {...p}
                inputMode="decimal"
                value={amount}
                onChange={(e) => setValue(`${e.target.value.replace(/[^\d.]/g, "")}:${unit}`)}
                className="w-32 font-mono"
              />
              <Select
                aria-label="Unit"
                value={unit}
                onChange={(e) => setValue(`${amount}:${e.target.value}`)}
                className="w-24"
              >
                <option value="KiB">KiB</option>
                <option value="MiB">MiB</option>
              </Select>
            </div>
          )}
        </Field>
        <p className="text-sm text-muted">
          Applies to endpoints without their own limit. Bodies above the limit are stored truncated and
          marked. The largest allowed value is {formatBytes(s.limits.max_body_bytes_ceiling)}, set by
          RELAYBOX_MAX_BODY_BYTES (restart required to change).
        </p>
        {generalError(save.error) && <p className="text-sm text-err">{generalError(save.error)}</p>}
        <Button
          type="submit"
          variant="primary"
          loading={save.isPending}
          disabled={!(bytes > 0) || bytes === s.runtime.default_max_body_bytes}
        >
          Save
        </Button>
      </form>
    </Panel>
  );
}

const lines = (list: string[]) => list.join("\n");
const parseLines = (text: string) =>
  text
    .split(/[\n,]/)
    .map((l) => l.trim())
    .filter(Boolean);

function RedactionSection({ s }: { s: Settings }) {
  const [headers, setHeaders] = useDraft(s.runtime.redact_headers, lines);
  const [fields, setFields] = useDraft(s.runtime.redact_fields, lines);
  const save = useSaveSettings();
  const dirty =
    lines(parseLines(headers)) !== lines(s.runtime.redact_headers) ||
    lines(parseLines(fields)) !== lines(s.runtime.redact_fields);
  const overridden = s.overridden.includes("redact_headers") || s.overridden.includes("redact_fields");
  return (
    <Panel title="Redaction" actions={<Applies />} bodyClassName="p-4">
      <form
        onSubmit={(e) => {
          e.preventDefault();
          save.mutate({ redact_headers: parseLines(headers), redact_fields: parseLines(fields) });
        }}
        className="space-y-4"
      >
        <p className="text-sm text-muted">
          Matching values are replaced with <span className="redacted">[REDACTED]</span> before a request is
          stored, so the originals never reach the database. Changes apply to new requests; stored requests
          keep the redaction they were captured with.
        </p>
        <div className="grid gap-4 md:grid-cols-2">
          <Field
            label="Header names"
            error={fieldError(save.error, "redact_headers")}
            hint={`One per line. Matched ignoring case. ${s.runtime.redact_headers.length} active.`}
          >
            {(p) => (
              <Textarea
                {...p}
                rows={9}
                spellCheck={false}
                value={headers}
                onChange={(e) => setHeaders(e.target.value)}
                className="font-mono text-xs"
              />
            )}
          </Field>
          <Field
            label="Field names"
            error={fieldError(save.error, "redact_fields")}
            hint="One per line. Checked in JSON bodies, form fields, and query parameters, ignoring case, dashes, underscores, dots, and spaces."
          >
            {(p) => (
              <Textarea
                {...p}
                rows={9}
                spellCheck={false}
                value={fields}
                onChange={(e) => setFields(e.target.value)}
                className="font-mono text-xs"
              />
            )}
          </Field>
        </div>
        {generalError(save.error) && <p className="text-sm text-err">{generalError(save.error)}</p>}
        <div className="flex flex-wrap gap-2">
          <Button type="submit" variant="primary" loading={save.isPending} disabled={!dirty}>
            Save
          </Button>
          {overridden && (
            <Button
              variant="ghost"
              icon={<RotateCcw className="size-3.5" aria-hidden />}
              onClick={() => save.mutate({ reset: ["redact_headers", "redact_fields"] })}
            >
              Restore default lists
            </Button>
          )}
        </div>
      </form>
    </Panel>
  );
}

function OnOff({ on, children }: { on: boolean; children?: ReactNode }) {
  return (
    <span className="inline-flex items-center gap-2">
      <Badge tone={on ? "ok" : "neutral"}>{on ? "on" : "off"}</Badge>
      {children && <span className="text-xs text-muted">{children}</span>}
    </span>
  );
}

function ReplaySection({ s }: { s: Settings }) {
  const srv = s.server;
  return (
    <Panel title="Replay security" actions={<Applies restart />} bodyClassName="space-y-4 p-4">
      <Facts
        items={[
          [
            "Replay to URLs",
            <OnOff key="u" on={srv.url_replay_enabled}>
              {srv.url_replay_enabled
                ? "Public addresses only, after confirmation"
                : "RELAYBOX_ALLOW_REMOTE_REPLAY"}
            </OnOff>,
          ],
          [
            "Allowed private hosts",
            srv.replay_allowed_hosts.length === 0 ? (
              <span className="text-faint">none (RELAYBOX_REPLAY_ALLOWED_HOSTS)</span>
            ) : (
              <span className="font-mono text-xs">{srv.replay_allowed_hosts.join(", ")}</span>
            ),
          ],
          ["Timeout", `${formatNumber(srv.replay_timeout_ms / 1000)} s`],
          ["Response size cap", formatBytes(srv.replay_response_bytes)],
        ]}
      />
      <ul className="list-disc space-y-1 pl-5 text-xs text-muted">
        <li>
          Loopback, private, and link-local addresses are refused unless listed in
          RELAYBOX_REPLAY_ALLOWED_HOSTS. Cloud metadata addresses are always refused.
        </li>
        <li>
          The host name is resolved once and the connection goes to the checked address, so DNS changes cannot
          redirect it.
        </li>
        <li>Redirects are not followed, proxies are not used, and TLS certificates are always verified.</li>
        <li>Replays into a Relaybox endpoint never leave this machine and are always available.</li>
      </ul>
    </Panel>
  );
}

function ServerSection({ s }: { s: Settings }) {
  const srv = s.server;
  return (
    <Panel title="Server" actions={<Applies restart />} bodyClassName="p-4">
      <Facts
        items={[
          [
            "Version",
            <span key="v" className="font-mono text-xs">
              {srv.version}
              {srv.commit !== "unknown" && ` (commit ${srv.commit.slice(0, 12)})`}
              {srv.build_date !== "unknown" && `, built ${srv.build_date}`}
            </span>,
          ],
          ["Running since", formatDateTime(srv.started_at)],
          [
            "Listening on",
            <span key="l" className="font-mono text-xs">
              {srv.listen_address}
            </span>,
          ],
          [
            "Public base URL",
            <span key="p" className="font-mono text-xs">
              {srv.public_base_url}
            </span>,
          ],
          [
            "Database",
            <span key="d" className="flex items-start gap-1">
              <span className="font-mono text-xs break-all">{srv.database_path}</span>
              <CopyButton text={srv.database_path} label="Copy database path" />
            </span>,
          ],
          ["Database size", `${formatBytes(srv.database_size_bytes)}, schema version ${srv.schema_version}`],
          [
            "Remote dashboard",
            <OnOff key="r" on={srv.remote_dashboard}>
              {srv.remote_dashboard
                ? srv.auth_required
                  ? "Token required"
                  : ""
                : "Only reachable from this machine"}
            </OnOff>,
          ],
          [
            "Rate limit per endpoint",
            `${formatNumber(srv.rate_limit_rps)} requests per second, bursts of ${formatNumber(srv.rate_limit_burst)}`,
          ],
          [
            "Logging",
            <span key="lg" className="font-mono text-xs">
              {srv.log_level}, {srv.log_format}
            </span>,
          ],
        ]}
      />
      <p className="mt-4 text-xs text-faint">
        Change these with environment variables or command-line flags, then restart. See Documentation for the
        full list.
      </p>
    </Panel>
  );
}

function DataSection() {
  const qc = useQueryClient();
  const toast = useToast();
  const endpoints = useEndpoints();
  const stats = useStats();
  const [endpointId, setEndpointId] = useState("");
  const [limit, setLimit] = useState("1000");
  const [includeBodies, setIncludeBodies] = useState(true);
  const [confirm, setConfirm] = useState(false);
  const list = endpoints.data ?? [];
  const chosen = list.find((e) => String(e.id) === endpointId) ?? list[0];
  const total = stats.data?.total_requests ?? 0;

  const exportMutation = useMutation({
    mutationFn: () =>
      api.exportFixture({
        endpoint_id: chosen!.id,
        limit: Number(limit) || 1000,
        include_bodies: includeBodies,
      }),
    onError: (err) => toast.error(errorMessage(err)),
  });
  const wipe = useMutation({
    mutationFn: () => api.deleteRequests({ scope: "all" }),
    onSuccess: (res) => {
      setConfirm(false);
      void qc.invalidateQueries();
      toast.success(`Deleted ${plural(res.deleted, "request")}.`);
    },
    onError: (err) => toast.error(errorMessage(err)),
  });

  return (
    <Panel title="Export and delete" bodyClassName="grid gap-6 p-4 md:grid-cols-2">
      <div className="space-y-3">
        <h3 className="text-sm font-medium">Export a fixture</h3>
        <p className="text-xs text-muted">
          Downloads requests as a JSON fixture you can import into another Relaybox. Redacted values stay
          redacted, and the dashboard token is never included.
        </p>
        <div className="flex flex-wrap gap-2">
          <Select
            aria-label="Endpoint to export"
            value={chosen ? String(chosen.id) : ""}
            onChange={(e) => setEndpointId(e.target.value)}
            className="w-auto min-w-40"
          >
            {list.length === 0 && <option value="">No endpoints</option>}
            {list.map((e) => (
              <option key={e.id} value={e.id}>
                {e.name} ({formatNumber(e.request_count)})
              </option>
            ))}
          </Select>
          <Input
            aria-label="Most recent requests to include"
            inputMode="numeric"
            value={limit}
            onChange={(e) => setLimit(e.target.value.replace(/\D/g, "").slice(0, 4))}
            className="w-20 font-mono"
          />
        </div>
        <label className="flex items-center gap-2 text-sm">
          <Checkbox checked={includeBodies} onChange={(e) => setIncludeBodies(e.target.checked)} />
          Include bodies
        </label>
        <Button
          size="sm"
          icon={<Download className="size-3.5" aria-hidden />}
          disabled={!chosen || chosen.request_count === 0}
          loading={exportMutation.isPending}
          onClick={() => exportMutation.mutate()}
        >
          Export
        </Button>
      </div>
      <div className="space-y-3 md:border-l md:border-line md:pl-6">
        <h3 className="text-sm font-medium">Delete all requests</h3>
        <p className="text-xs text-muted">
          Removes every stored request and replay record. Endpoints, their URLs, and settings are kept.
        </p>
        <Button
          size="sm"
          variant="danger"
          icon={<Trash2 className="size-3.5" aria-hidden />}
          disabled={total === 0}
          onClick={() => setConfirm(true)}
        >
          Delete {plural(total, "request")}
        </Button>
      </div>
      <ConfirmDialog
        open={confirm}
        title="Delete every stored request?"
        confirmLabel={`Delete ${plural(total, "request")}`}
        danger
        busy={wipe.isPending}
        onConfirm={() => wipe.mutate()}
        onCancel={() => setConfirm(false)}
      >
        All {plural(total, "request")} across every endpoint are deleted. This cannot be undone.
      </ConfirmDialog>
    </Panel>
  );
}
