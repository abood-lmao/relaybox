import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Download, GitCompareArrows, Repeat2, Terminal, Trash2 } from "lucide-react";
import { useState, type ReactNode } from "react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router";
import { BodyView } from "../components/BodyView";
import { PairsTable } from "../components/HeadersTable";
import { MethodTag, SourceTag, StatusCode, StatusDot, WithRedactions } from "../components/http";
import { PageHeader } from "../components/layout/PageHeader";
import { useDocumentTitle } from "../hooks/useDocumentTitle";
import { useToast } from "../components/toast";
import { Button, LinkButton } from "../components/ui/button";
import { buttonClass } from "../components/ui/buttonClass";
import { ConfirmDialog } from "../components/ui/dialog";
import { EmptyState, ErrorState, LoadingBlock, Notice } from "../components/ui/feedback";
import { CopyButton, Facts, Panel } from "../components/ui/misc";
import { TabPanel, Tabs } from "../components/ui/tabs";
import { WireView } from "../components/WireView";
import { copyText } from "../hooks/useCopy";
import { ApiError, api, errorMessage } from "../lib/api";
import { requestToCurl } from "../lib/curl";
import { displayQuery } from "../lib/http";
import {
  formatBytes,
  formatDateTime,
  formatDuration,
  formatTimestamp,
  pairsToText,
  plural,
} from "../lib/format";
import { useRequest } from "../lib/queries";
import type { RequestDetail } from "../types/api";

type Tab = "overview" | "headers" | "query" | "body" | "response" | "metadata";
const TABS: Tab[] = ["overview", "headers", "query", "body", "response", "metadata"];

export function RequestDetailPage() {
  const id = Number(useParams().id);
  const request = useRequest(id);
  useDocumentTitle(request.data ? `${request.data.method} ${request.data.path || "/"}` : `Request ${id}`);

  if (!Number.isInteger(id) || id <= 0) {
    return <ErrorState error={new Error("This is not a valid request address.")} title="Request not found" />;
  }
  if (request.isPending) return <LoadingBlock />;
  if (request.isError) {
    const missing = request.error instanceof ApiError && request.error.status === 404;
    return (
      <EmptyState
        title={missing ? "Request not found" : "The request could not be loaded"}
        action={<LinkButton to="/requests">Back to requests</LinkButton>}
      >
        {missing
          ? "It may have been deleted, or removed by the retention policy."
          : errorMessage(request.error)}
      </EmptyState>
    );
  }
  return <RequestView key={request.data.id} req={request.data} />;
}

function RequestView({ req }: { req: RequestDetail }) {
  const [params, setParams] = useSearchParams();
  const tabParam = params.get("tab") as Tab | null;
  const tab: Tab = tabParam && TABS.includes(tabParam) ? tabParam : "overview";
  const setTab = (t: Tab) => {
    const next = new URLSearchParams(params);
    if (t === "overview") next.delete("tab");
    else next.set("tab", t);
    setParams(next, { replace: true });
  };

  return (
    <div>
      <PageHeader
        back={{ to: `/requests?endpoint_id=${req.endpoint.id}`, label: `Requests to ${req.endpoint.name}` }}
        title={
          <span className="flex items-baseline gap-2.5">
            <MethodTag method={req.method} className="text-base" />
            <span className="font-mono text-base font-medium break-all">
              {req.path || "/"}
              {req.query_string && (
                <span className="text-faint">
                  ?<WithRedactions text={displayQuery(req.query_string)} />
                </span>
              )}
            </span>
          </span>
        }
        meta={
          <>
            <span className="inline-flex items-center gap-1.5">
              <StatusDot status={req.status} />
              <StatusCode status={req.status} withText />
            </span>
            <time dateTime={req.received_at} title={formatDateTime(req.received_at)}>
              {formatTimestamp(req.received_at)}
            </time>
            <Link to={`/endpoints/${req.endpoint.id}`} className="hover:text-fg hover:underline">
              {req.endpoint.name}
            </Link>
            <span>{formatDuration(req.duration_ms)}</span>
            <span>{formatBytes(req.body_size)}</span>
            <SourceTag source={req.source} />
            {req.replay_of && (
              <Link to={`/requests/${req.replay_of}`} className="hover:text-fg hover:underline">
                Replay of #{req.replay_of}
              </Link>
            )}
          </>
        }
        actions={<Actions req={req} />}
      />

      <Tabs
        label="Request sections"
        value={tab}
        onChange={setTab}
        tabs={[
          { key: "overview", label: "Overview" },
          { key: "headers", label: "Headers", count: req.headers.length },
          { key: "query", label: "Query", count: req.query.length },
          { key: "body", label: "Body" },
          { key: "response", label: "Response" },
          { key: "metadata", label: "Metadata" },
        ]}
      />

      <TabPanel id={tab} className="mt-4">
        {tab === "overview" && <OverviewTab req={req} onTab={setTab} />}
        {tab === "headers" && (
          <Panel
            title="Request headers"
            description="Names are shown in canonical form, sorted alphabetically."
            actions={
              req.headers.length > 0 && (
                <CopyButton text={pairsToText(req.headers)} label="Copy all" showLabel />
              )
            }
          >
            <PairsTable pairs={req.headers} empty="No headers" filterable />
          </Panel>
        )}
        {tab === "query" && (
          <Panel
            title="Query parameters"
            actions={
              req.query_string && <CopyButton text={req.query_string} label="Copy query string" showLabel />
            }
          >
            <PairsTable pairs={req.query} nameLabel="Parameter" empty="No query parameters" />
          </Panel>
        )}
        {tab === "body" && (
          <Panel>
            <BodyView body={req.body} downloadHref={api.bodyDownloadURL(req.id)} noun="request body" />
          </Panel>
        )}
        {tab === "response" && <ResponseTab req={req} />}
        {tab === "metadata" && <MetadataTab req={req} />}
      </TabPanel>
    </div>
  );
}

function Actions({ req }: { req: RequestDetail }) {
  const qc = useQueryClient();
  const toast = useToast();
  const navigate = useNavigate();
  const [confirm, setConfirm] = useState(false);
  const remove = useMutation({
    mutationFn: () => api.deleteRequest(req.id),
    onSuccess: () => {
      setConfirm(false);
      qc.removeQueries({ queryKey: ["request", req.id] });
      void qc.invalidateQueries({ queryKey: ["requests"] });
      void qc.invalidateQueries({ queryKey: ["stats"] });
      void qc.invalidateQueries({ queryKey: ["endpoints"] });
      toast.success("Request deleted.");
      navigate(`/requests?endpoint_id=${req.endpoint.id}`);
    },
    onError: (err) => toast.error(errorMessage(err)),
  });

  const copyCurl = async () => {
    const curl = requestToCurl(req);
    if (!(await copyText(curl.command))) {
      toast.error("Copying failed.");
      return;
    }
    const notes: string[] = [];
    if (curl.omittedHeaders.length)
      notes.push(`redacted headers were left out (${curl.omittedHeaders.join(", ")})`);
    if (curl.bodyOmitted) notes.push("the body was left out because it is binary or truncated");
    toast.success(notes.length ? `curl command copied, but ${notes.join(" and ")}.` : "curl command copied.");
  };

  return (
    <>
      <LinkButton
        to={`/requests/${req.id}/replay`}
        variant="primary"
        icon={<Repeat2 className="size-3.5" aria-hidden />}
      >
        Replay
      </LinkButton>
      <LinkButton to={`/compare?left=${req.id}`} icon={<GitCompareArrows className="size-3.5" aria-hidden />}>
        Compare
      </LinkButton>
      <Button icon={<Terminal className="size-3.5" aria-hidden />} onClick={copyCurl}>
        Copy as curl
      </Button>
      <a href={api.requestExportURL(req.id)} download className={buttonClass()}>
        <Download className="size-3.5" aria-hidden />
        Export
      </a>
      <Button
        variant="ghost"
        icon={<Trash2 className="size-3.5" aria-hidden />}
        onClick={() => setConfirm(true)}
        aria-label="Delete request"
      >
        Delete
      </Button>
      <ConfirmDialog
        open={confirm}
        title="Delete this request?"
        confirmLabel="Delete request"
        danger
        busy={remove.isPending}
        onConfirm={() => remove.mutate()}
        onCancel={() => setConfirm(false)}
      >
        Request #{req.id} and its replay history are removed. This cannot be undone.
      </ConfirmDialog>
    </>
  );
}

function OverviewTab({ req, onTab }: { req: RequestDetail; onTab: (t: Tab) => void }) {
  const contentType = req.headers.find(([k]) => k.toLowerCase() === "content-type")?.[1];
  return (
    <div className="grid gap-4 xl:grid-cols-[minmax(0,1fr)_minmax(0,1.4fr)]">
      <div className="min-w-0 space-y-4">
        <Panel title="Summary" bodyClassName="p-4">
          <Facts
            items={[
              [
                "Endpoint",
                <Link key="ep" to={`/endpoints/${req.endpoint.id}`} className="hover:underline">
                  {req.endpoint.name}
                </Link>,
              ],
              ["Received", formatDateTime(req.received_at)],
              ["Response", <StatusCode key="st" status={req.status} withText />],
              ["Handled in", formatDuration(req.duration_ms)],
              [
                "Content type",
                contentType ? (
                  <span className="font-mono text-xs">{contentType}</span>
                ) : (
                  <span className="text-faint">none</span>
                ),
              ],
              [
                "Body",
                <span key="body">
                  {req.body.kind === "empty" ? "empty" : `${formatBytes(req.body_size)}, ${req.body.kind}`}
                  {req.body_truncated && <span className="ml-1.5 text-warn">truncated</span>}
                </span>,
              ],
              [
                "Sender",
                <span key="ip" className="font-mono text-xs">
                  {req.remote_addr || "unknown"}
                </span>,
              ],
            ]}
          />
        </Panel>
        {req.redactions.length > 0 && (
          <Notice title={`${plural(req.redactions.length, "value")} redacted before storage`}>
            <p className="mb-1.5">
              These values were replaced with <span className="redacted">[REDACTED]</span> when the request
              arrived. The originals were never stored.
            </p>
            <ul className="flex flex-wrap gap-1.5">
              {req.redactions.map((r, i) => (
                <li
                  key={i}
                  className="rounded border border-line bg-surface px-1.5 py-0.5 font-mono text-xs text-fg"
                >
                  <span className="text-faint">{r.location}:</span> {r.name}
                </li>
              ))}
            </ul>
          </Notice>
        )}
        {req.replay_children.length > 0 && (
          <Panel title="Replays recorded as requests">
            <ul>
              {req.replay_children.map((c) => (
                <li key={c.id} className="border-b border-line last:border-0">
                  <Link
                    to={`/requests/${c.id}`}
                    className="flex items-center gap-3 px-4 py-2 text-sm hover:bg-surface-2/60"
                  >
                    <span className="font-mono text-xs text-muted">#{c.id}</span>
                    <span className="flex-1 truncate">{c.endpoint.name}</span>
                    <StatusCode status={c.status} />
                    <span className="text-xs text-muted">{formatTimestamp(c.received_at)}</span>
                  </Link>
                </li>
              ))}
            </ul>
          </Panel>
        )}
      </div>
      <Panel
        title="As received"
        description="Request line, headers, and the start of the body."
        actions={
          <button
            type="button"
            onClick={() => onTab("body")}
            className="text-xs font-medium text-accent-fg hover:underline"
          >
            Open body
          </button>
        }
        bodyClassName="p-4"
      >
        <WireView request={req} />
      </Panel>
    </div>
  );
}

function ResponseTab({ req }: { req: RequestDetail }) {
  const r = req.response;
  return (
    <div className="space-y-4">
      <Panel
        title={
          <span className="flex items-center gap-2">
            Response sent to the caller <StatusCode status={r.status} withText />
          </span>
        }
        description="Relaybox answered with this endpoint's mock response."
        actions={
          r.headers.length > 0 && <CopyButton text={pairsToText(r.headers)} label="Copy headers" showLabel />
        }
      >
        <PairsTable pairs={r.headers} empty="No response headers" />
      </Panel>
      <Panel>
        <BodyView body={r.body} downloadHref={api.bodyDownloadURL(req.id, "response")} noun="response body" />
      </Panel>
    </div>
  );
}

function MetadataTab({ req }: { req: RequestDetail }) {
  return (
    <Panel bodyClassName="p-4">
      <Facts
        items={[
          [
            "Request ID",
            <span key="id" className="font-mono text-xs">
              {req.id}
            </span>,
          ],
          [
            "Endpoint",
            <span key="ep">
              {req.endpoint.name} <span className="font-mono text-xs text-faint">(ID {req.endpoint.id})</span>
            </span>,
          ],
          [
            "Source",
            req.source === "capture"
              ? "Captured"
              : req.source === "replay"
                ? "Created by a replay"
                : "Imported from a fixture",
          ],
          ...(req.replay_of
            ? ([
                [
                  "Replay of",
                  <Link key="ro" to={`/requests/${req.replay_of}`} className="hover:underline">
                    Request #{req.replay_of}
                  </Link>,
                ],
              ] as [string, ReactNode][])
            : []),
          [
            "Received at",
            <span key="ra" className="font-mono text-xs">
              {req.received_at}
            </span>,
          ],
          [
            "URL",
            <span key="url" className="flex items-start gap-1">
              <span className="font-mono text-xs break-all">{req.url}</span>
              <CopyButton text={req.url} label="Copy URL" />
            </span>,
          ],
          [
            "Sender address",
            <span key="ip" className="font-mono text-xs">
              {req.remote_addr || "unknown"}
            </span>,
          ],
          [
            "Content-Length header",
            req.content_length === null ? (
              <span className="text-faint">not sent</span>
            ) : (
              <span className="font-mono text-xs">{req.content_length}</span>
            ),
          ],
          ["Body size", `${formatBytes(req.body_size)} (${req.body_size.toLocaleString()} bytes)`],
          [
            "Stored bytes",
            `${req.body.stored_bytes.toLocaleString()} bytes${req.body_truncated ? ", truncated" : ""}`,
          ],
          ["Handled in", formatDuration(req.duration_ms)],
          [
            "Redactions",
            req.redactions.length === 0 ? (
              <span className="text-faint">none</span>
            ) : (
              <ul key="red" className="space-y-0.5">
                {req.redactions.map((r, i) => (
                  <li key={i} className="font-mono text-xs">
                    <span className="text-faint">{r.location}:</span> {r.name}
                  </li>
                ))}
              </ul>
            ),
          ],
        ]}
      />
    </Panel>
  );
}
