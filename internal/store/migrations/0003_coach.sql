-- Phase 3: habits, sleep and proactive messages. See docs/architecture/coach-engine.md.

CREATE TABLE habits (
    id           TEXT PRIMARY KEY,
    name         TEXT    NOT NULL,
    why          TEXT    NOT NULL DEFAULT '',   -- the owner's reason, in their words
    anchor       TEXT    NOT NULL DEFAULT '',   -- "After I finish lunch"
    behavior     TEXT    NOT NULL,              -- "Walk for 10 minutes"
    tiny_version TEXT    NOT NULL,              -- the worst-day version
    cadence      TEXT    NOT NULL DEFAULT 'daily',  -- daily | weekdays | days:mon,wed | times:3
    window_start TEXT    NOT NULL DEFAULT '12:00',  -- local HH:MM; the prompt time
    window_end   TEXT    NOT NULL DEFAULT '15:00',  -- local HH:MM; the prompt stops being useful
    state        TEXT    NOT NULL DEFAULT 'active'
                 CHECK (state IN ('active', 'paused', 'retired')),
    created_at   INTEGER NOT NULL
);

-- One row per habit per local day. A day with no row is "not logged".
CREATE TABLE habit_logs (
    habit_id TEXT    NOT NULL REFERENCES habits (id) ON DELETE CASCADE,
    day      TEXT    NOT NULL,                  -- local date, YYYY-MM-DD
    status   TEXT    NOT NULL CHECK (status IN ('done', 'tiny', 'skipped')),
    note     TEXT    NOT NULL DEFAULT '',
    ts       INTEGER NOT NULL,
    PRIMARY KEY (habit_id, day)
);

-- Raw sleep signals. 'activity' rows are the owner's messages and button
-- presses; they are all the chat-gap inference has to go on.
CREATE TABLE sleep_events (
    id     INTEGER PRIMARY KEY AUTOINCREMENT,
    ts     INTEGER NOT NULL,
    kind   TEXT    NOT NULL CHECK (kind IN ('bedtime', 'wake', 'activity', 'import')),
    source TEXT    NOT NULL DEFAULT 'chat'
);
CREATE INDEX sleep_events_ts ON sleep_events (ts);

-- One row per night, keyed by the local date the owner woke up.
CREATE TABLE sleep_nights (
    day            TEXT PRIMARY KEY,            -- local date of waking, YYYY-MM-DD
    onset          INTEGER NOT NULL,
    wake           INTEGER NOT NULL,
    confidence     TEXT    NOT NULL CHECK (confidence IN ('explicit', 'confirmed', 'inferred', 'prior')),
    user_confirmed INTEGER NOT NULL DEFAULT 0
);

-- Every proactive message, and whether the owner reacted to it.
CREATE TABLE notifications (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    ts           INTEGER NOT NULL,
    kind         TEXT    NOT NULL,              -- morning, habit:<id>, evening, weekly, winddown, backoff
    variant      TEXT    NOT NULL DEFAULT '',
    responded_at INTEGER                        -- first owner activity within the response window
);
CREATE INDEX notifications_ts ON notifications (ts);
