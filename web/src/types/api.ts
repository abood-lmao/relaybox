// Types for the Relaybox REST API (see api/openapi.yaml).

export type Pair = [string, string];

export interface MockResponse {
  status: number;
  content_type: string;
  headers: Record<string, string>;
  body: string;
  delay_ms: number;
}

export interface Endpoint {
  id: number;
  name: string;
  enabled: boolean;
  url: string;
  public_id: string;
  mock_response: MockResponse;
  max_body_bytes: number | null;
  effective_max_body_bytes: number;
  request_count: number;
  last_request_at: string | null;
  created_at: string;
  updated_at: string;
}

export interface EndpointRef {
  id: number;
  name: string;
}

export type Source = "capture" | "import" | "replay";

export interface RequestSummary {
  id: number;
  endpoint: EndpointRef;
  source: Source;
  replay_of: number | null;
  method: string;
  path: string;
  query_string: string;
  status: number;
  duration_ms: number;
  body_size: number;
  body_truncated: boolean;
  content_type: string;
  received_at: string;
  redacted: boolean;
}

export type BodyKind = "empty" | "json" | "text" | "form" | "multipart" | "binary";

export interface BodyPart {
  name: string;
  filename?: string;
  content_type?: string;
  size: number;
  text?: string;
  headers: Pair[];
}

export interface Body {
  encoding: "utf8" | "base64";
  data: string;
  size: number;
  stored_bytes: number;
  truncated: boolean;
  kind: BodyKind;
  content_type: string;
  parse_error?: string;
  form?: Pair[];
  parts?: BodyPart[];
}

export interface ResponseInfo {
  status: number;
  status_text: string;
  headers: Pair[];
  body: Body;
}

export interface Redaction {
  location: "header" | "query" | "body";
  name: string;
}

export interface ReplayChild {
  id: number;
  endpoint: EndpointRef;
  status: number;
  received_at: string;
}

export interface RequestDetail extends RequestSummary {
  url: string;
  query: Pair[];
  headers: Pair[];
  body: Body;
  content_length: number | null;
  remote_addr: string;
  response: ResponseInfo;
  redactions: Redaction[];
  replay_children: ReplayChild[];
}

export interface Page<T> {
  items: T[];
  total: number;
  limit: number;
  offset: number;
}

export interface TopEndpoint {
  id: number;
  name: string;
  enabled: boolean;
  last_24h: number;
  total: number;
  last_request_at: string | null;
}

export interface Stats {
  total_requests: number;
  last_24h: number;
  endpoints: number;
  status_classes: Record<string, number>;
  hourly: { start: string; count: number }[];
  top_endpoints: TopEndpoint[];
  recent: RequestSummary[];
}

export interface Meta {
  name: string;
  version: string;
  commit: string;
  build_date: string;
  public_base_url: string;
  hook_url_prefix: string;
  auth_required: boolean;
  remote_dashboard: boolean;
  url_replay_enabled: boolean;
  allowed_methods: string[];
  mock_defaults: MockResponse;
  limits: {
    mock_body_bytes: number;
    mock_headers: number;
    mock_delay_ms: number;
    endpoint_name_length: number;
    min_body_bytes: number;
    max_body_bytes_ceiling: number;
    default_max_body_bytes: number;
    replay_body_bytes: number;
    replay_response_bytes: number;
  };
}

export interface HeaderOption {
  name: string;
  value: string;
  selected: boolean;
  redacted: boolean;
  blocked?: string;
}

export interface ReplayDraft {
  request: RequestSummary;
  method: string;
  headers: HeaderOption[];
  body: Body;
  url_replay_enabled: boolean;
  allowed_hosts: string[];
  max_body_bytes: number;
}

export interface ReplayAttempt {
  id: number;
  request_id: number;
  target: "url" | "endpoint";
  destination: string;
  destination_host: string;
  target_endpoint_id: number | null;
  result_request_id: number | null;
  method: string;
  response_status: number | null;
  response_size: number | null;
  duration_ms: number;
  outcome: "success" | "failure";
  summary: string;
  created_at: string;
}

export interface ReplayInput {
  target: "url" | "endpoint";
  url?: string;
  endpoint_id?: number;
  method?: string;
  headers?: Pair[];
  body?: { encoding: "utf8" | "base64"; data: string };
  confirmed?: boolean;
}

export interface ReplayResult {
  attempt: ReplayAttempt;
  response: ResponseInfo | null;
  created_request_id: number | null;
}

export interface Difference {
  path: string;
  kind: "added" | "removed" | "changed";
  left?: string;
  right?: string;
}

export interface DiffLine {
  op: "equal" | "insert" | "delete";
  text: string;
  left?: number;
  right?: number;
}

export interface CompareBody {
  mode: "json" | "text" | "binary" | "empty";
  equal: boolean;
  left_error?: string;
  right_error?: string;
  differences: Difference[] | null;
  lines: DiffLine[] | null;
  truncated: boolean;
  approximate: boolean;
  incomplete_data: boolean;
  note?: string;
}

export interface Comparison {
  part: "request" | "response";
  left: RequestSummary;
  right: RequestSummary;
  body: CompareBody;
  headers: Difference[];
}

export interface RuntimeSettings {
  retention_days: number;
  max_stored_requests: number;
  default_max_body_bytes: number;
  redact_headers: string[];
  redact_fields: string[];
}

export type SettingKey = keyof RuntimeSettings;

export interface ServerInfo {
  version: string;
  commit: string;
  build_date: string;
  started_at: string;
  listen_address: string;
  public_base_url: string;
  database_path: string;
  database_size_bytes: number;
  schema_version: number;
  log_level: string;
  log_format: string;
  remote_dashboard: boolean;
  auth_required: boolean;
  url_replay_enabled: boolean;
  replay_allowed_hosts: string[];
  rate_limit_rps: number;
  rate_limit_burst: number;
  max_body_bytes_limit: number;
  replay_timeout_ms: number;
  replay_response_bytes: number;
}

export interface Settings {
  runtime: RuntimeSettings;
  defaults: RuntimeSettings;
  overridden: SettingKey[];
  limits: {
    min_body_bytes: number;
    max_body_bytes_ceiling: number;
    max_retention_days: number;
    max_stored_requests_limit: number;
  };
  server: ServerInfo;
}

export type SettingsPatch = Partial<RuntimeSettings> & { reset?: SettingKey[] };

export interface Session {
  auth_required: boolean;
  authenticated: boolean;
}

export interface ImportResult {
  imported: number;
  endpoint_id: number;
  request_ids: number[];
}

export interface CleanupResult {
  expired: number;
  trimmed: number;
}

export interface RequestFilters {
  endpoint_id?: string;
  method?: string;
  status?: string;
  source?: string;
  from?: string;
  to?: string;
  q?: string;
  sort?: string;
  order?: string;
  limit?: string;
  offset?: string;
}
