-- Phase 2: plugin storage, pending confirmations and long-term memory.

CREATE TABLE plugin_kv (
    plugin     TEXT    NOT NULL,
    key        TEXT    NOT NULL,
    value      TEXT    NOT NULL,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (plugin, key)
);

-- Risky tool calls waiting for the owner's decision. Arguments are stored so
-- the confirmation shows exactly what will run.
CREATE TABLE pending_actions (
    id         TEXT PRIMARY KEY,
    ts         INTEGER NOT NULL,
    tool       TEXT    NOT NULL,
    args       TEXT    NOT NULL,
    summary    TEXT    NOT NULL,
    expires_at INTEGER NOT NULL,
    state      TEXT    NOT NULL DEFAULT 'pending'
               CHECK (state IN ('pending', 'approved', 'cancelled', 'expired'))
);
CREATE INDEX pending_actions_state ON pending_actions (state, expires_at);

-- Long-term memory. origin records where a fact came from; only 'user' and
-- 'kest' entries may influence behavior, anything derived from tools or the
-- web stays quarantined. See docs/architecture/security.md.
CREATE TABLE memories (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    ts            INTEGER NOT NULL,
    kind          TEXT    NOT NULL CHECK (kind IN ('fact', 'summary')),
    content       TEXT    NOT NULL,
    origin        TEXT    NOT NULL,
    confidence    REAL    NOT NULL DEFAULT 1,
    source_msg_id INTEGER
);
CREATE INDEX memories_kind_ts ON memories (kind, ts);
