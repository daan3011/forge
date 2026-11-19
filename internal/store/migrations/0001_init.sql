-- 0001: initial schema. All timestamps are unix milliseconds.

-- Agent sessions (P1.3-P1.10).
CREATE TABLE sessions (
    id                  INTEGER PRIMARY KEY,
    name                TEXT    NOT NULL UNIQUE,
    claude_session_id   TEXT    NOT NULL UNIQUE,
    project             TEXT    NOT NULL,
    repo                TEXT,
    base_branch         TEXT,
    branch              TEXT,
    worktree            TEXT,
    tmux_session        TEXT,
    tmux_window         TEXT,
    template            TEXT,
    status              TEXT    NOT NULL DEFAULT 'starting'
                        CHECK (status IN ('starting', 'working', 'waiting', 'idle', 'error', 'dead')),
    status_changed_at   INTEGER,
    last_activity_at    INTEGER,
    transcript_path     TEXT,
    transcript_offset   INTEGER NOT NULL DEFAULT 0,
    waiting_notified_at INTEGER,
    notes               TEXT,
    summary             TEXT,
    archived_at         INTEGER,
    created_at          INTEGER NOT NULL,
    updated_at          INTEGER NOT NULL
);
CREATE INDEX sessions_project_archived ON sessions (project, archived_at);
CREATE INDEX sessions_status ON sessions (status);

-- Raw Claude Code hook events (P1.4). session_id is NULL when the hook
-- arrived for a session forge does not know (yet).
CREATE TABLE session_events (
    id                INTEGER PRIMARY KEY,
    session_id        INTEGER REFERENCES sessions (id) ON DELETE CASCADE,
    claude_session_id TEXT    NOT NULL,
    event             TEXT    NOT NULL,
    tool_name         TEXT,
    payload           TEXT,
    created_at        INTEGER NOT NULL
);
CREATE INDEX session_events_session ON session_events (session_id, created_at);
CREATE INDEX session_events_claude ON session_events (claude_session_id, created_at);

-- Edges between sessions, Plane issues and GitHub PRs (P1.9, P3.7).
-- url/title/state describe the "to" end.
CREATE TABLE links (
    id         INTEGER PRIMARY KEY,
    from_kind  TEXT    NOT NULL CHECK (from_kind IN ('session', 'plane_issue', 'github_pr')),
    from_id    TEXT    NOT NULL,
    to_kind    TEXT    NOT NULL CHECK (to_kind IN ('session', 'plane_issue', 'github_pr')),
    to_id      TEXT    NOT NULL,
    url        TEXT,
    title      TEXT,
    state      TEXT,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    UNIQUE (from_kind, from_id, to_kind, to_id)
);
CREATE INDEX links_to ON links (to_kind, to_id);

-- Pinned things (artifacts first, P4.6; shown on Home, P6.1).
CREATE TABLE pins (
    kind       TEXT    NOT NULL,
    ref        TEXT    NOT NULL,
    project    TEXT    NOT NULL DEFAULT '',
    title      TEXT,
    position   INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    PRIMARY KEY (kind, ref)
);

-- Generic cache for API responses, palette recents, indexes (P0.6, P0.8, P5.3).
-- ttl is in milliseconds; 0 means the entry never goes stale.
CREATE TABLE kv_cache (
    key        TEXT    PRIMARY KEY,
    kind       TEXT    NOT NULL,
    project    TEXT    NOT NULL DEFAULT '',
    value      BLOB    NOT NULL,
    fetched_at INTEGER NOT NULL,
    ttl        INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX kv_cache_kind_project ON kv_cache (kind, project);
CREATE INDEX kv_cache_fetched_at ON kv_cache (fetched_at);

-- Daily token usage rollups from transcripts (P5.4).
CREATE TABLE usage (
    day                TEXT    NOT NULL,
    project            TEXT    NOT NULL,
    claude_session_id  TEXT    NOT NULL DEFAULT '',
    model              TEXT    NOT NULL,
    input_tokens       INTEGER NOT NULL DEFAULT 0,
    output_tokens      INTEGER NOT NULL DEFAULT 0,
    cache_read_tokens  INTEGER NOT NULL DEFAULT 0,
    cache_write_tokens INTEGER NOT NULL DEFAULT 0,
    messages           INTEGER NOT NULL DEFAULT 0,
    updated_at         INTEGER NOT NULL,
    PRIMARY KEY (day, project, claude_session_id, model)
);
CREATE INDEX usage_session ON usage (claude_session_id);
