-- Phase 1 schema. See docs/architecture/data-model.md.
-- Timestamps are unix milliseconds in UTC.

CREATE TABLE profile (
    key        TEXT PRIMARY KEY,
    value      TEXT    NOT NULL,          -- JSON
    updated_at INTEGER NOT NULL
);

CREATE TABLE messages (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    ts         INTEGER NOT NULL,
    role       TEXT    NOT NULL CHECK (role IN ('user', 'assistant', 'system', 'tool')),
    content    TEXT    NOT NULL,
    origin     TEXT    NOT NULL DEFAULT 'user',
    tool_calls TEXT,                      -- JSON, assistant tool requests
    tokens     INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX messages_ts ON messages (ts);

CREATE TABLE jobs (
    id          TEXT PRIMARY KEY,
    owner       TEXT    NOT NULL,         -- system, coach, plugin:<id>
    name        TEXT    NOT NULL,         -- handler name
    payload     TEXT    NOT NULL DEFAULT '{}',
    rule        TEXT    NOT NULL DEFAULT '',   -- empty means one-off
    zone        TEXT    NOT NULL DEFAULT 'UTC',
    next_run_at INTEGER NOT NULL,
    misfire     TEXT    NOT NULL DEFAULT 'run_once',
    state       TEXT    NOT NULL DEFAULT 'scheduled'
                CHECK (state IN ('scheduled', 'running', 'done', 'paused', 'failed')),
    last_run_at INTEGER,
    run_count   INTEGER NOT NULL DEFAULT 0,
    last_error  TEXT    NOT NULL DEFAULT '',
    created_at  INTEGER NOT NULL
);
CREATE INDEX jobs_due ON jobs (state, next_run_at);

CREATE TABLE job_runs (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    job_id      TEXT    NOT NULL REFERENCES jobs (id) ON DELETE CASCADE,
    started_at  INTEGER NOT NULL,
    finished_at INTEGER,
    status      TEXT    NOT NULL,
    error       TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX job_runs_job ON job_runs (job_id, started_at);

CREATE TABLE llm_usage (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    ts                INTEGER NOT NULL,
    slot              TEXT    NOT NULL,   -- general, transcription, tts
    model             TEXT    NOT NULL,
    prompt_tokens     INTEGER NOT NULL DEFAULT 0,
    completion_tokens INTEGER NOT NULL DEFAULT 0,
    cost_usd          REAL    NOT NULL DEFAULT 0,
    generation_id     TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX llm_usage_ts ON llm_usage (ts);

CREATE TABLE audit_log (
    id        INTEGER PRIMARY KEY AUTOINCREMENT,
    ts        INTEGER NOT NULL,
    actor     TEXT    NOT NULL,           -- owner, system, plugin:<id>
    action    TEXT    NOT NULL,
    risk      TEXT    NOT NULL DEFAULT 'read',
    confirmed INTEGER NOT NULL DEFAULT 0,
    status    TEXT    NOT NULL,
    summary   TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX audit_log_ts ON audit_log (ts);
