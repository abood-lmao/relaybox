import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Plus, Webhook } from "lucide-react";
import { Link, useNavigate, useSearchParams } from "react-router";
import { CreateEndpointDialog } from "../components/endpoint/CreateEndpointDialog";
import { PageHeader } from "../components/layout/PageHeader";
import { useDocumentTitle } from "../hooks/useDocumentTitle";
import { useToast } from "../components/toast";
import { Button } from "../components/ui/button";
import { EmptyState, ErrorState, LoadingBlock } from "../components/ui/feedback";
import { Switch } from "../components/ui/form";
import { CopyButton, Panel } from "../components/ui/misc";
import { StatusCode } from "../components/http";
import { api, errorMessage } from "../lib/api";
import { formatBytes, formatNumber, formatRelative } from "../lib/format";
import { useEndpoints } from "../lib/queries";
import type { Endpoint } from "../types/api";

export function EndpointsPage() {
  useDocumentTitle("Endpoints");
  const endpoints = useEndpoints();
  const [params, setParams] = useSearchParams();
  const navigate = useNavigate();
  const creating = params.get("new") === "1";
  const setCreating = (open: boolean) => {
    const next = new URLSearchParams(params);
    if (open) next.set("new", "1");
    else next.delete("new");
    setParams(next, { replace: true });
  };

  return (
    <div>
      <PageHeader
        title="Endpoints"
        description="Each endpoint has its own URL. Anyone who has the URL can send requests to it."
        actions={
          <Button
            variant="primary"
            icon={<Plus className="size-3.5" aria-hidden />}
            onClick={() => setCreating(true)}
          >
            New endpoint
          </Button>
        }
      />
      {endpoints.isPending ? (
        <LoadingBlock />
      ) : endpoints.isError ? (
        <ErrorState error={endpoints.error} onRetry={() => endpoints.refetch()} />
      ) : endpoints.data.length === 0 ? (
        <Panel>
          <EmptyState
            icon={<Webhook className="size-6" aria-hidden />}
            title="No endpoints yet"
            action={
              <Button
                variant="primary"
                icon={<Plus className="size-3.5" aria-hidden />}
                onClick={() => setCreating(true)}
              >
                Create your first endpoint
              </Button>
            }
          >
            Create an endpoint to get a URL, then point a webhook sender at it.
          </EmptyState>
        </Panel>
      ) : (
        <Panel>
          <EndpointList endpoints={endpoints.data} />
        </Panel>
      )}
      <CreateEndpointDialog
        open={creating}
        onClose={() => setCreating(false)}
        onCreated={(ep) => navigate(`/endpoints/${ep.id}`, { state: { created: true } })}
      />
    </div>
  );
}

function EndpointList({ endpoints }: { endpoints: Endpoint[] }) {
  const qc = useQueryClient();
  const toast = useToast();
  const toggle = useMutation({
    mutationFn: ({ id, enabled }: { id: number; enabled: boolean }) => api.updateEndpoint(id, { enabled }),
    onSuccess: (ep) => {
      void qc.invalidateQueries({ queryKey: ["endpoints"] });
      toast.success(`${ep.name} is ${ep.enabled ? "enabled" : "disabled"}.`);
    },
    onError: (err) => toast.error(errorMessage(err)),
  });

  return (
    <div className="overflow-x-auto">
      <table className="w-full min-w-[720px] text-left text-sm">
        <thead className="border-b border-line text-xs text-muted">
          <tr>
            <th scope="col" className="px-4 py-2 font-medium">
              Name
            </th>
            <th scope="col" className="px-3 py-2 font-medium">
              URL
            </th>
            <th scope="col" className="px-3 py-2 font-medium">
              Responds
            </th>
            <th scope="col" className="px-3 py-2 text-right font-medium">
              Requests
            </th>
            <th scope="col" className="px-3 py-2 font-medium">
              Last request
            </th>
            <th scope="col" className="px-4 py-2 font-medium">
              Enabled
            </th>
          </tr>
        </thead>
        <tbody>
          {endpoints.map((e) => (
            <tr key={e.id} className="border-b border-line last:border-0 hover:bg-surface-2/60">
              <td className="px-4 py-2.5">
                <Link to={`/endpoints/${e.id}`} className="font-medium hover:text-accent-fg hover:underline">
                  {e.name}
                </Link>
                <p className="text-xs text-faint">Body limit {formatBytes(e.effective_max_body_bytes)}</p>
              </td>
              <td className="max-w-80 px-3 py-2.5">
                <div className="flex items-center gap-1">
                  <code className="truncate font-mono text-xs text-muted" title={e.url}>
                    {e.url}
                  </code>
                  <CopyButton text={e.url} label={`Copy URL for ${e.name}`} />
                </div>
              </td>
              <td className="px-3 py-2.5 whitespace-nowrap">
                <StatusCode status={e.mock_response.status} />
                {e.mock_response.delay_ms > 0 && (
                  <span className="ml-1.5 text-xs text-muted">
                    after {formatNumber(e.mock_response.delay_ms)} ms
                  </span>
                )}
              </td>
              <td className="px-3 py-2.5 text-right">
                <Link
                  to={`/requests?endpoint_id=${e.id}`}
                  className="font-mono text-xs hover:text-accent-fg hover:underline"
                >
                  {formatNumber(e.request_count)}
                </Link>
              </td>
              <td className="px-3 py-2.5 text-xs whitespace-nowrap text-muted">
                {e.last_request_at ? formatRelative(e.last_request_at) : "Never"}
              </td>
              <td className="px-4 py-2.5">
                <Switch
                  checked={e.enabled}
                  label={`Accept requests for ${e.name}`}
                  disabled={toggle.isPending && toggle.variables?.id === e.id}
                  onChange={(enabled) => toggle.mutate({ id: e.id, enabled })}
                />
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
