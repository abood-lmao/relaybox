import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { CircleCheck, CircleX, Send } from "lucide-react";
import { useState } from "react";
import { Link, useParams } from "react-router";
import { BodyView } from "../components/BodyView";
import { PairsTable } from "../components/HeadersTable";
import { MethodTag, StatusCode } from "../components/http";
import { PageHeader } from "../components/layout/PageHeader";
import { useDocumentTitle } from "../hooks/useDocumentTitle";
import { Button, LinkButton } from "../components/ui/button";
import { Dialog } from "../components/ui/dialog";
import { EmptyState, ErrorState, LoadingBlock, Notice } from "../components/ui/feedback";
import { Checkbox, Field, Input, Select, Textarea } from "../components/ui/form";
import { Badge, Panel } from "../components/ui/misc";
import { ApiError, api, errorMessage } from "../lib/api";
import { cn } from "../lib/cn";
import { formatBytes, formatDateTime, formatDuration, formatTimestamp, plural } from "../lib/format";
import { isRedacted } from "../lib/http";
import { useEndpoints, useMeta, useReplayHistory } from "../lib/queries";
import type { Endpoint, Meta, ReplayAttempt, ReplayDraft, ReplayInput, ReplayResult } from "../types/api";

export function ReplayPage() {
  const id = Number(useParams().id);
  useDocumentTitle(`Replay request ${id}`);
  const draft = useQuery({
    queryKey: ["request", id, "replay-draft"],
    queryFn: () => api.replayDraft(id),
    enabled: id > 0,
    staleTime: Infinity,
  });
  const endpoints = useEndpoints();
  const meta = useMeta();

  if (draft.isPending || endpoints.isPending || meta.isPending) return <LoadingBlock />;
  if (draft.isError) {
    const missing = draft.error instanceof ApiError && draft.error.status === 404;
    return (
      <EmptyState
        title={missing ? "Request not found" : "The request could not be loaded"}
        action={<LinkButton to="/requests">Back to requests</LinkButton>}
      >
        {missing ? "It may have been deleted." : errorMessage(draft.error)}
      </EmptyState>
    );
  }
  if (endpoints.isError || meta.isError) {
    return <ErrorState error={endpoints.error ?? meta.error} onRetry={() => void endpoints.refetch()} />;
  }

  return (
    <div>
      <PageHeader
        back={{ to: `/requests/${id}`, label: `Request #${id}` }}
        title="Replay request"
        description="Send a captured request again, to one of your endpoints or to a URL. Nothing is sent until you confirm."
      />
      <div className="grid gap-4 xl:grid-cols-[minmax(0,1fr)_360px]">
        <ReplayForm draft={draft.data} endpoints={endpoints.data} meta={meta.data} />
        <History requestId={id} endpoints={endpoints.data} />
      </div>
    </div>
  );
}

interface HeaderRow {
  name: string;
  value: string;
  selected: boolean;
  redacted: boolean;
  blocked?: string;
}

function ReplayForm({ draft, endpoints, meta }: { draft: ReplayDraft; endpoints: Endpoint[]; meta: Meta }) {
  const qc = useQueryClient();
  const req = draft.request;
  const [method, setMethod] = useState(draft.method);
  const [headers, setHeaders] = useState<HeaderRow[]>(() => draft.headers.map((h) => ({ ...h })));
  const textBody = draft.body.encoding === "utf8";
  const [body, setBody] = useState(textBody ? draft.body.data : "");
  const [editBody, setEditBody] = useState(false);
  const [sendTruncated, setSendTruncated] = useState(false);
  const [target, setTarget] = useState<"endpoint" | "url">(draft.url_replay_enabled ? "url" : "endpoint");
  const [url, setUrl] = useState("");
  const [endpointId, setEndpointId] = useState(
    String(endpoints.find((e) => e.id !== req.endpoint.id)?.id ?? endpoints[0]?.id ?? ""),
  );
  const [reviewing, setReviewing] = useState(false);
  const [result, setResult] = useState<ReplayResult | null>(null);

  const bodyEdited = textBody && body !== draft.body.data;
  const truncated = draft.body.truncated;
  const sendExplicitBody = bodyEdited || (truncated && sendTruncated);

  const buildInput = (): ReplayInput => {
    const input: ReplayInput = {
      target,
      method,
      headers: headers.filter((h) => h.selected && !h.blocked).map((h) => [h.name, h.value]),
      confirmed: target === "url",
    };
    if (target === "url") input.url = url.trim();
    else input.endpoint_id = Number(endpointId);
    if (sendExplicitBody) {
      input.body = textBody
        ? { encoding: "utf8", data: body }
        : { encoding: "base64", data: draft.body.data };
    }
    return input;
  };

  const send = useMutation({
    mutationFn: () => api.replay(req.id, buildInput()),
    onSuccess: (res) => {
      setReviewing(false);
      setResult(res);
      void qc.invalidateQueries({ queryKey: ["request", req.id, "replays"] });
      if (res.created_request_id) {
        void qc.invalidateQueries({ queryKey: ["requests"] });
        void qc.invalidateQueries({ queryKey: ["stats"] });
        void qc.invalidateQueries({ queryKey: ["endpoints"] });
      }
    },
    onError: () => setReviewing(false),
  });

  const fieldErrors = send.error instanceof ApiError ? send.error.fields : {};
  const generalError =
    send.error && !(send.error instanceof ApiError && send.error.status === 422)
      ? errorMessage(send.error)
      : null;
  const selectedHeaders = headers.filter((h) => h.selected && !h.blocked);
  const placeholderHeaders = selectedHeaders.filter((h) => isRedacted(h.value));
  const targetEndpoint = endpoints.find((e) => String(e.id) === endpointId);
  const destinationHost = (() => {
    try {
      return new URL(url.trim()).host;
    } catch {
      return "";
    }
  })();
  const canReview =
    (target === "url" ? draft.url_replay_enabled && /^https?:\/\/.+/i.test(url.trim()) : !!targetEndpoint) &&
    (!truncated || sendExplicitBody);

  return (
    <div className="min-w-0 space-y-4">
      <Panel
        title="1. Request"
        description={`Based on request #${req.id}, received ${formatDateTime(req.received_at)}.`}
        bodyClassName="space-y-4 p-4"
      >
        <div className="flex flex-wrap items-end gap-3">
          <Field label="Method" error={fieldErrors.method} className="w-36">
            {(p) => (
              <Select {...p} value={method} onChange={(e) => setMethod(e.target.value)} className="font-mono">
                {meta.allowed_methods.map((m) => (
                  <option key={m}>{m}</option>
                ))}
              </Select>
            )}
          </Field>
          <p className="pb-2 font-mono text-xs break-all text-muted">
            original: <MethodTag method={req.method} /> {req.path || "/"}
            {req.query_string && `?${req.query_string}`}
          </p>
        </div>

        <fieldset>
          <legend className="mb-1.5 text-xs font-medium">
            Headers{" "}
            <span className="font-normal text-muted">
              ({selectedHeaders.length} of {headers.length} selected)
            </span>
          </legend>
          <div className="overflow-hidden rounded-md border border-line">
            {headers.length === 0 && (
              <p className="px-3 py-3 text-sm text-muted">The request had no headers.</p>
            )}
            {headers.map((h, i) => (
              <div
                key={`${h.name}-${i}`}
                className={cn(
                  "flex items-center gap-2 border-b border-line px-3 py-1.5 last:border-0",
                  h.blocked && "bg-surface-2/50",
                )}
              >
                <Checkbox
                  checked={h.selected && !h.blocked}
                  disabled={!!h.blocked}
                  onChange={(e) =>
                    setHeaders((hs) => hs.map((x, j) => (j === i ? { ...x, selected: e.target.checked } : x)))
                  }
                  aria-label={`Forward ${h.name}`}
                />
                <span className="w-44 shrink-0 truncate font-mono text-xs text-muted" title={h.name}>
                  {h.name}
                </span>
                {h.blocked ? (
                  <span className="text-xs text-faint">Not forwarded: {h.blocked}</span>
                ) : (
                  <Input
                    aria-label={`Value for ${h.name}`}
                    value={h.value}
                    onChange={(e) =>
                      setHeaders((hs) =>
                        hs.map((x, j) => (j === i ? { ...x, value: e.target.value, selected: true } : x)),
                      )
                    }
                    className={cn("h-7 flex-1 font-mono text-xs", isRedacted(h.value) && "redacted")}
                  />
                )}
              </div>
            ))}
          </div>
          {fieldErrors.headers && <p className="mt-1 text-xs text-err">{fieldErrors.headers}</p>}
          {headers.some((h) => h.redacted) && (
            <p className="mt-1.5 text-xs text-muted">
              Redacted headers are unselected because their real values were never stored. Type a value to
              send one.
            </p>
          )}
          {placeholderHeaders.length > 0 && (
            <p className="mt-1 text-xs text-warn">
              {placeholderHeaders.map((h) => h.name).join(", ")} will be sent with the literal text
              [REDACTED].
            </p>
          )}
        </fieldset>

        <div>
          <div className="mb-1.5 flex items-center justify-between">
            <span className="text-xs font-medium">Body</span>
            {textBody && draft.body.kind !== "empty" && !editBody && (
              <Button size="sm" variant="ghost" onClick={() => setEditBody(true)}>
                Edit body
              </Button>
            )}
          </div>
          {truncated && (
            <Notice tone="warn" title="The stored body is incomplete" className="mb-2">
              Only {formatBytes(draft.body.stored_bytes)} of {formatBytes(draft.body.size)} were stored.{" "}
              {textBody
                ? "Edit the body, or confirm that the stored part should be sent."
                : "Confirm that the stored part should be sent."}
              <label className="mt-1.5 flex items-center gap-2 text-fg">
                <Checkbox checked={sendTruncated} onChange={(e) => setSendTruncated(e.target.checked)} />
                Send the stored part anyway
              </label>
            </Notice>
          )}
          {editBody && textBody ? (
            <Field label="Body to send" error={fieldErrors.body}>
              {(p) => (
                <Textarea
                  {...p}
                  rows={10}
                  spellCheck={false}
                  value={body}
                  onChange={(e) => setBody(e.target.value)}
                  className="font-mono text-[12.5px]"
                />
              )}
            </Field>
          ) : (
            <div className="rounded-md border border-line">
              <BodyView body={draft.body} noun="body" />
            </div>
          )}
          {bodyEdited && (
            <p className="mt-1 text-xs text-muted">The edited body will be sent instead of the stored one.</p>
          )}
        </div>
      </Panel>

      <Panel title="2. Destination" bodyClassName="space-y-3 p-4">
        <div role="radiogroup" aria-label="Destination type" className="grid gap-2 sm:grid-cols-2">
          <TargetOption
            checked={target === "endpoint"}
            onSelect={() => setTarget("endpoint")}
            title="A Relaybox endpoint"
            text="Recorded as a new request, linked to the original. Nothing leaves this machine."
          />
          <TargetOption
            checked={target === "url"}
            onSelect={() => setTarget("url")}
            disabled={!draft.url_replay_enabled}
            title="A URL"
            text={
              draft.url_replay_enabled
                ? "Sent to an external server, for example your app's webhook handler."
                : "Disabled on this server."
            }
          />
        </div>
        {target === "endpoint" ? (
          <Field label="Endpoint" error={fieldErrors.endpoint_id}>
            {(p) => (
              <Select {...p} value={endpointId} onChange={(e) => setEndpointId(e.target.value)}>
                {endpoints.length === 0 && <option value="">No endpoints</option>}
                {endpoints.map((e) => (
                  <option key={e.id} value={e.id}>
                    {e.name}
                    {e.enabled ? "" : " (disabled)"}
                    {e.id === req.endpoint.id ? " (original)" : ""}
                  </option>
                ))}
              </Select>
            )}
          </Field>
        ) : (
          <Field
            label="Destination URL"
            error={fieldErrors.url}
            hint={
              draft.allowed_hosts.length > 0
                ? `Private and loopback addresses are blocked, except ${draft.allowed_hosts.join(", ")}. Redirects are not followed.`
                : "Private, loopback, and cloud metadata addresses are blocked. Redirects are not followed."
            }
          >
            {(p) => (
              <Input
                {...p}
                type="url"
                placeholder="https://example.com/webhooks/test"
                value={url}
                onChange={(e) => setUrl(e.target.value)}
                className="font-mono"
              />
            )}
          </Field>
        )}
        {!draft.url_replay_enabled && (
          <Notice>
            Replay to URLs is off while the dashboard accepts remote connections. An operator can enable it
            with RELAYBOX_ALLOW_REMOTE_REPLAY=true.
          </Notice>
        )}
      </Panel>

      {generalError && (
        <Notice tone="error" title="The replay was not sent">
          {generalError}
        </Notice>
      )}

      <div className="flex items-center gap-3">
        <Button
          variant="primary"
          icon={<Send className="size-3.5" aria-hidden />}
          disabled={!canReview}
          onClick={() => setReviewing(true)}
        >
          Review and send
        </Button>
        {!canReview && (
          <span className="text-xs text-muted">
            {target === "url" && !/^https?:\/\/.+/i.test(url.trim())
              ? "Enter an http or https URL."
              : truncated && !sendExplicitBody
                ? "Decide how to handle the incomplete body."
                : "Choose a destination."}
          </span>
        )}
      </div>

      {result && <ResultPanel result={result} />}

      <Dialog
        open={reviewing}
        onClose={() => setReviewing(false)}
        title={
          target === "url"
            ? `Send to ${destinationHost}?`
            : `Replay into ${targetEndpoint?.name ?? "endpoint"}?`
        }
        footer={
          <>
            <Button onClick={() => setReviewing(false)}>Cancel</Button>
            <Button
              variant="primary"
              loading={send.isPending}
              icon={<Send className="size-3.5" aria-hidden />}
              onClick={() => send.mutate()}
            >
              {target === "url" ? "Send request" : "Replay"}
            </Button>
          </>
        }
      >
        <div className="space-y-3 text-sm">
          {target === "url" && (
            <Notice tone="warn">
              This sends a real HTTP request to{" "}
              <span className="font-mono break-all text-fg">{url.trim()}</span>. The receiving server may act
              on it.
            </Notice>
          )}
          <dl className="grid grid-cols-[100px_1fr] gap-x-4 gap-y-1.5">
            <dt className="text-muted">Method</dt>
            <dd>
              <MethodTag method={method} />
            </dd>
            <dt className="text-muted">Destination</dt>
            <dd className="font-mono text-xs break-all">
              {target === "url" ? url.trim() : targetEndpoint?.url}
            </dd>
            <dt className="text-muted">Headers</dt>
            <dd className="text-xs">
              {selectedHeaders.length === 0 ? "none" : selectedHeaders.map((h) => h.name).join(", ")}
            </dd>
            <dt className="text-muted">Body</dt>
            <dd className="text-xs">
              {sendExplicitBody
                ? bodyEdited
                  ? `edited, ${formatBytes(new TextEncoder().encode(body).length)}`
                  : `stored part, ${formatBytes(draft.body.stored_bytes)}`
                : draft.body.kind === "empty"
                  ? "empty"
                  : `as captured, ${formatBytes(draft.body.stored_bytes)}`}
            </dd>
          </dl>
        </div>
      </Dialog>
    </div>
  );
}

function TargetOption({
  checked,
  onSelect,
  disabled,
  title,
  text,
}: {
  checked: boolean;
  onSelect: () => void;
  disabled?: boolean;
  title: string;
  text: string;
}) {
  return (
    <button
      type="button"
      role="radio"
      aria-checked={checked}
      disabled={disabled}
      onClick={onSelect}
      className={cn(
        "rounded-md border px-3 py-2.5 text-left transition-colors disabled:cursor-not-allowed disabled:opacity-50",
        checked ? "border-accent bg-accent-soft/60" : "border-line hover:border-line-strong",
      )}
    >
      <span className="flex items-center gap-2 text-sm font-medium">
        <span
          aria-hidden
          className={cn(
            "size-3 rounded-full border",
            checked ? "border-[4px] border-accent" : "border-line-strong",
          )}
        />
        {title}
      </span>
      <span className="mt-0.5 block pl-5 text-xs text-muted">{text}</span>
    </button>
  );
}

function ResultPanel({ result }: { result: ReplayResult }) {
  const a = result.attempt;
  const ok = a.outcome === "success";
  return (
    <Panel
      title={
        <span className="flex items-center gap-2">
          {ok ? (
            <CircleCheck className="size-4 text-ok" aria-hidden />
          ) : (
            <CircleX className="size-4 text-err" aria-hidden />
          )}
          {ok ? "Replay delivered" : "Replay failed"}
        </span>
      }
      description={a.summary}
      actions={
        result.created_request_id && (
          <LinkButton to={`/requests/${result.created_request_id}`} size="sm">
            Open request #{result.created_request_id}
          </LinkButton>
        )
      }
    >
      <div className="flex flex-wrap gap-x-6 gap-y-1 border-b border-line px-4 py-2.5 text-xs text-muted">
        <span>
          Destination <span className="font-mono text-fg">{a.destination_host || a.destination}</span>
        </span>
        {a.response_status !== null && (
          <span>
            Status <StatusCode status={a.response_status} withText />
          </span>
        )}
        <span>Took {formatDuration(a.duration_ms)}</span>
      </div>
      {result.response ? (
        <>
          <PairsTable pairs={result.response.headers} empty="No response headers" />
          <div className="border-t border-line">
            <BodyView body={result.response.body} noun="response body" />
          </div>
        </>
      ) : (
        <p className="px-4 py-4 text-sm text-muted">No response was received.</p>
      )}
    </Panel>
  );
}

function History({ requestId, endpoints }: { requestId: number; endpoints: Endpoint[] }) {
  const history = useReplayHistory(requestId);
  return (
    <Panel title="Replay history" description="Earlier replays of this request.">
      {history.isPending ? (
        <LoadingBlock />
      ) : history.isError ? (
        <ErrorState error={history.error} onRetry={() => history.refetch()} />
      ) : history.data.length === 0 ? (
        <p className="px-4 py-8 text-center text-sm text-muted">This request has not been replayed yet.</p>
      ) : (
        <ul>
          {history.data.map((a) => (
            <HistoryItem key={a.id} attempt={a} endpoints={endpoints} />
          ))}
        </ul>
      )}
      {history.data && history.data.length > 0 && (
        <p className="border-t border-line px-4 py-2 text-xs text-faint">
          {plural(history.data.length, "attempt")}
        </p>
      )}
    </Panel>
  );
}

function HistoryItem({ attempt: a, endpoints }: { attempt: ReplayAttempt; endpoints: Endpoint[] }) {
  const ep = a.target_endpoint_id ? endpoints.find((e) => e.id === a.target_endpoint_id) : undefined;
  const destination =
    a.target === "endpoint" ? (ep?.name ?? "deleted endpoint") : a.destination_host || a.destination;
  return (
    <li className="border-b border-line px-4 py-2.5 last:border-0">
      <div className="flex items-center gap-2 text-sm">
        <Badge tone={a.outcome === "success" ? "ok" : "err"}>
          {a.outcome === "success" ? "delivered" : "failed"}
        </Badge>
        <MethodTag method={a.method} />
        <span className="min-w-0 flex-1 truncate font-mono text-xs" title={a.destination}>
          {destination}
        </span>
        {a.response_status !== null && <StatusCode status={a.response_status} />}
      </div>
      <p className="mt-1 text-xs text-muted">{a.summary}</p>
      <p className="mt-0.5 flex gap-3 text-xs text-faint">
        <time dateTime={a.created_at} title={formatDateTime(a.created_at)}>
          {formatTimestamp(a.created_at)}
        </time>
        <span>{formatDuration(a.duration_ms)}</span>
        {a.result_request_id && (
          <Link to={`/requests/${a.result_request_id}`} className="text-accent-fg hover:underline">
            request #{a.result_request_id}
          </Link>
        )}
      </p>
    </li>
  );
}
