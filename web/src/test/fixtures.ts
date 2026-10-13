import type { Body, Endpoint, Meta, RequestDetail, RequestSummary, Stats } from "../types/api";

export const meta: Meta = {
  name: "Relaybox",
  version: "0.1.0-test",
  commit: "unknown",
  build_date: "unknown",
  public_base_url: "http://127.0.0.1:8787",
  hook_url_prefix: "http://127.0.0.1:8787/hooks/",
  auth_required: false,
  remote_dashboard: false,
  url_replay_enabled: true,
  allowed_methods: ["GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"],
  mock_defaults: {
    status: 200,
    content_type: "application/json",
    headers: {},
    body: '{"received":true}',
    delay_ms: 0,
  },
  limits: {
    mock_body_bytes: 65536,
    mock_headers: 32,
    mock_delay_ms: 10000,
    endpoint_name_length: 80,
    min_body_bytes: 1024,
    max_body_bytes_ceiling: 1048576,
    default_max_body_bytes: 1048576,
    replay_body_bytes: 10485760,
    replay_response_bytes: 1048576,
  },
};

export function endpoint(over: Partial<Endpoint> = {}): Endpoint {
  const id = over.id ?? 1;
  return {
    id,
    name: `Endpoint ${id}`,
    enabled: true,
    url: `http://127.0.0.1:8787/hooks/pub${id}`,
    public_id: `pub${id}`,
    mock_response: { ...meta.mock_defaults },
    max_body_bytes: null,
    effective_max_body_bytes: 1048576,
    request_count: 0,
    last_request_at: null,
    created_at: "2026-10-10T10:00:00Z",
    updated_at: "2026-10-10T10:00:00Z",
    ...over,
  };
}

export function summary(over: Partial<RequestSummary> = {}): RequestSummary {
  return {
    id: 1,
    endpoint: { id: 1, name: "Endpoint 1" },
    source: "capture",
    replay_of: null,
    method: "POST",
    path: "/events",
    query_string: "",
    status: 200,
    duration_ms: 3,
    body_size: 17,
    body_truncated: false,
    content_type: "application/json",
    received_at: "2026-10-10T10:05:00Z",
    redacted: false,
    ...over,
  };
}

export function body(over: Partial<Body> = {}): Body {
  const data = over.data ?? '{"event":"test"}';
  return {
    encoding: "utf8",
    data,
    size: data.length,
    stored_bytes: data.length,
    truncated: false,
    kind: "json",
    content_type: "application/json",
    ...over,
  };
}

export function detail(over: Partial<RequestDetail> = {}): RequestDetail {
  const s = summary(over);
  return {
    ...s,
    url: `http://127.0.0.1:8787/hooks/pub1${s.path}`,
    query: [],
    headers: [
      ["Content-Type", "application/json"],
      ["User-Agent", "curl/8.0"],
    ],
    body: body(),
    content_length: 16,
    remote_addr: "127.0.0.1",
    response: {
      status: 200,
      status_text: "OK",
      headers: [["Content-Type", "application/json"]],
      body: body({ data: '{"received":true}' }),
    },
    redactions: [],
    replay_children: [],
    ...over,
  };
}

export function stats(over: Partial<Stats> = {}): Stats {
  return {
    total_requests: 0,
    last_24h: 0,
    endpoints: 0,
    status_classes: { "2xx": 0, "3xx": 0, "4xx": 0, "5xx": 0 },
    hourly: Array.from({ length: 24 }, (_, i) => ({
      start: new Date(Date.UTC(2026, 9, 10, i)).toISOString(),
      count: 0,
    })),
    top_endpoints: [],
    recent: [],
    ...over,
  };
}
