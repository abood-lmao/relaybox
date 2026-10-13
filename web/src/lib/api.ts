import type {
  CleanupResult,
  Comparison,
  Endpoint,
  ImportResult,
  Meta,
  MockResponse,
  Page,
  ReplayAttempt,
  ReplayDraft,
  ReplayInput,
  ReplayResult,
  RequestDetail,
  RequestFilters,
  RequestSummary,
  ResponseInfo,
  Session,
  Settings,
  SettingsPatch,
  Stats,
} from "../types/api";

/** An error returned by the API, or a network failure (status 0). */
export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly fields: Record<string, string>;
  readonly details: string[];

  constructor(
    status: number,
    code: string,
    message: string,
    fields: Record<string, string> = {},
    details: string[] = [],
  ) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
    this.fields = fields;
    this.details = details;
  }
}

export function errorMessage(err: unknown): string {
  if (err instanceof ApiError) return err.message;
  if (err instanceof Error) return err.message;
  return "Something went wrong.";
}

interface ErrorBody {
  error?: { code?: string; message?: string; fields?: Record<string, string>; details?: string[] };
}

async function readError(res: Response): Promise<ApiError> {
  let body: ErrorBody | null = null;
  try {
    body = (await res.json()) as ErrorBody;
  } catch {
    // Not JSON; fall through to a generic message.
  }
  const e = body?.error;
  return new ApiError(
    res.status,
    e?.code ?? "http_error",
    e?.message ?? `The server answered ${res.status} ${res.statusText}.`.trim(),
    e?.fields ?? {},
    e?.details ?? [],
  );
}

async function send(method: string, path: string, body?: unknown): Promise<Response> {
  const headers: Record<string, string> = { Accept: "application/json" };
  let payload: string | undefined;
  if (body !== undefined) {
    headers["Content-Type"] = "application/json";
    payload = typeof body === "string" ? body : JSON.stringify(body);
  }
  let res: Response;
  try {
    res = await fetch(path, { method, headers, body: payload, credentials: "same-origin" });
  } catch {
    throw new ApiError(0, "network_error", "Relaybox did not respond. Check that the server is running.");
  }
  if (!res.ok) throw await readError(res);
  return res;
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await send(method, path, body);
  if (res.status === 204) return undefined as T;
  return (await res.json()) as T;
}

function query(params: Record<string, string | number | boolean | undefined>): string {
  const q = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) {
    if (v !== undefined && v !== "") q.set(k, String(v));
  }
  const s = q.toString();
  return s ? `?${s}` : "";
}

function filenameFrom(res: Response, fallback: string): string {
  const cd = res.headers.get("Content-Disposition") ?? "";
  const match = /filename="([^"]+)"/.exec(cd);
  return match?.[1] ?? fallback;
}

/** Saves a blob through a temporary link. */
export function saveBlob(blob: Blob, filename: string) {
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = filename;
  document.body.appendChild(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

export interface EndpointInput {
  name?: string;
  enabled?: boolean;
  mock_response?: MockResponse;
  max_body_bytes?: number | null;
}

export const api = {
  session: () => request<Session>("GET", "/api/v1/auth/session"),
  login: (token: string) => request<Session>("POST", "/api/v1/auth/login", { token }),
  logout: () => request<void>("POST", "/api/v1/auth/logout"),

  meta: () => request<Meta>("GET", "/api/v1/meta"),
  stats: () => request<Stats>("GET", "/api/v1/stats"),

  listEndpoints: () => request<{ items: Endpoint[] }>("GET", "/api/v1/endpoints").then((r) => r.items),
  getEndpoint: (id: number) => request<Endpoint>("GET", `/api/v1/endpoints/${id}`),
  createEndpoint: (input: EndpointInput) => request<Endpoint>("POST", "/api/v1/endpoints", input),
  updateEndpoint: (id: number, input: EndpointInput) =>
    request<Endpoint>("PATCH", `/api/v1/endpoints/${id}`, input),
  deleteEndpoint: (id: number) => request<{ deleted_requests: number }>("DELETE", `/api/v1/endpoints/${id}`),
  rotateEndpointURL: (id: number) => request<Endpoint>("POST", `/api/v1/endpoints/${id}/rotate-url`),
  previewMock: (mock: MockResponse, method = "POST") =>
    request<ResponseInfo>("POST", "/api/v1/mock-preview", { mock_response: mock, method }),

  listRequests: (filters: RequestFilters) =>
    request<Page<RequestSummary>>("GET", `/api/v1/requests${query({ ...filters })}`),
  getRequest: (id: number) => request<RequestDetail>("GET", `/api/v1/requests/${id}`),
  deleteRequest: (id: number) => request<void>("DELETE", `/api/v1/requests/${id}`),
  deleteRequests: (
    scope: { scope: "all" } | { scope: "endpoint"; endpoint_id: number } | { scope: "ids"; ids: number[] },
  ) => {
    const params: Record<string, string> = { scope: scope.scope };
    if (scope.scope === "endpoint") params.endpoint_id = String(scope.endpoint_id);
    if (scope.scope === "ids") params.ids = scope.ids.join(",");
    return request<{ deleted: number }>("DELETE", `/api/v1/requests${query(params)}`);
  },
  compare: (leftId: number, rightId: number, part: "request" | "response") =>
    request<Comparison>("POST", "/api/v1/requests/compare", { left_id: leftId, right_id: rightId, part }),

  replayDraft: (id: number) => request<ReplayDraft>("GET", `/api/v1/requests/${id}/replay`),
  replay: (id: number, input: ReplayInput) =>
    request<ReplayResult>("POST", `/api/v1/requests/${id}/replay`, input),
  replayHistory: (id: number) =>
    request<{ items: ReplayAttempt[] }>("GET", `/api/v1/requests/${id}/replays`).then((r) => r.items),

  bodyDownloadURL: (id: number, part: "request" | "response" = "request") =>
    `/api/v1/requests/${id}/body${query({ part })}`,
  requestExportURL: (id: number, includeBody = true) =>
    `/api/v1/requests/${id}/export${includeBody ? "" : "?include_body=false"}`,
  exportFixture: async (input: {
    request_ids?: number[];
    endpoint_id?: number;
    limit?: number;
    include_bodies?: boolean;
  }) => {
    const res = await send("POST", "/api/v1/fixtures/export", input);
    saveBlob(await res.blob(), filenameFrom(res, "relaybox-fixture.json"));
  },
  importFixture: (endpointId: number, fileText: string) =>
    request<ImportResult>("POST", `/api/v1/fixtures/import${query({ endpoint_id: endpointId })}`, fileText),

  settings: () => request<Settings>("GET", "/api/v1/settings"),
  updateSettings: (patch: SettingsPatch) => request<Settings>("PATCH", "/api/v1/settings", patch),
  cleanup: () => request<CleanupResult>("POST", "/api/v1/maintenance/cleanup"),
};
