-- Initial schema. Timestamps are Unix milliseconds (UTC). Durations are
-- microseconds. JSON columns hold UTF-8 JSON text.

CREATE TABLE endpoints (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    public_id         TEXT    NOT NULL UNIQUE,
    name              TEXT    NOT NULL,
    enabled           INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    mock_status       INTEGER NOT NULL DEFAULT 200,
    mock_content_type TEXT    NOT NULL DEFAULT 'application/json',
    mock_headers      TEXT    NOT NULL DEFAULT '{}',
    mock_body         TEXT    NOT NULL DEFAULT '',
    mock_delay_ms     INTEGER NOT NULL DEFAULT 0,
    max_body_bytes    INTEGER,
    created_at        INTEGER NOT NULL,
    updated_at        INTEGER NOT NULL
);

CREATE UNIQUE INDEX idx_endpoints_name ON endpoints (name COLLATE NOCASE);

CREATE TABLE captured_requests (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    endpoint_id      INTEGER NOT NULL REFERENCES endpoints (id) ON DELETE CASCADE,
    source           TEXT    NOT NULL DEFAULT 'capture' CHECK (source IN ('capture', 'import', 'replay')),
    replay_of        INTEGER REFERENCES captured_requests (id) ON DELETE SET NULL,
    method           TEXT    NOT NULL,
    path             TEXT    NOT NULL,
    raw_query        TEXT    NOT NULL DEFAULT '',
    query            TEXT    NOT NULL DEFAULT '[]',
    headers          TEXT    NOT NULL DEFAULT '{}',
    body             BLOB,
    body_size        INTEGER NOT NULL DEFAULT 0,
    body_truncated   INTEGER NOT NULL DEFAULT 0 CHECK (body_truncated IN (0, 1)),
    content_type     TEXT    NOT NULL DEFAULT '',
    content_length   INTEGER,
    remote_addr      TEXT    NOT NULL DEFAULT '',
    response_status  INTEGER NOT NULL,
    response_headers TEXT    NOT NULL DEFAULT '{}',
    response_body    BLOB,
    duration_us      INTEGER NOT NULL DEFAULT 0,
    received_at      INTEGER NOT NULL,
    redactions       TEXT    NOT NULL DEFAULT '[]'
);

CREATE INDEX idx_requests_received ON captured_requests (received_at, id);
CREATE INDEX idx_requests_endpoint_received ON captured_requests (endpoint_id, received_at, id);
CREATE INDEX idx_requests_status ON captured_requests (response_status);
CREATE INDEX idx_requests_method ON captured_requests (method);
CREATE INDEX idx_requests_replay_of ON captured_requests (replay_of);

CREATE TABLE replay_attempts (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    request_id         INTEGER NOT NULL REFERENCES captured_requests (id) ON DELETE CASCADE,
    target             TEXT    NOT NULL CHECK (target IN ('url', 'endpoint')),
    destination        TEXT    NOT NULL,
    destination_host   TEXT    NOT NULL,
    target_endpoint_id INTEGER REFERENCES endpoints (id) ON DELETE SET NULL,
    result_request_id  INTEGER REFERENCES captured_requests (id) ON DELETE SET NULL,
    method             TEXT    NOT NULL,
    response_status    INTEGER,
    response_size      INTEGER,
    duration_us        INTEGER NOT NULL DEFAULT 0,
    outcome            TEXT    NOT NULL CHECK (outcome IN ('success', 'failure')),
    summary            TEXT    NOT NULL,
    created_at         INTEGER NOT NULL
);

CREATE INDEX idx_replays_request ON replay_attempts (request_id, created_at);
CREATE INDEX idx_replays_result ON replay_attempts (result_request_id);
CREATE INDEX idx_replays_target_endpoint ON replay_attempts (target_endpoint_id);

CREATE TABLE settings (
    key        TEXT PRIMARY KEY,
    value      TEXT    NOT NULL,
    updated_at INTEGER NOT NULL
);
