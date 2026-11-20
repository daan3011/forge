-- 0002: allow status 'archived' (a killed session whose worktree was removed;
-- uuid, branch and links are kept so it can be resumed later).
--
-- SQLite cannot alter a CHECK constraint, so sessions is rebuilt. Migrations
-- run in a transaction with foreign keys on, where DROP TABLE sessions would
-- cascade-delete session_events; session_events is therefore set aside and
-- restored around the rebuild.

CREATE TABLE session_events_backup AS SELECT * FROM session_events;
DROP TABLE session_events;

CREATE TABLE sessions_new (
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
                        CHECK (status IN ('starting', 'working', 'waiting', 'idle', 'error', 'dead', 'archived')),
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
INSERT INTO sessions_new SELECT * FROM sessions;
DROP TABLE sessions;
ALTER TABLE sessions_new RENAME TO sessions;
CREATE INDEX sessions_project_archived ON sessions (project, archived_at);
CREATE INDEX sessions_status ON sessions (status);

CREATE TABLE session_events (
    id                INTEGER PRIMARY KEY,
    session_id        INTEGER REFERENCES sessions (id) ON DELETE CASCADE,
    claude_session_id TEXT    NOT NULL,
    event             TEXT    NOT NULL,
    tool_name         TEXT,
    payload           TEXT,
    created_at        INTEGER NOT NULL
);
INSERT INTO session_events SELECT * FROM session_events_backup;
DROP TABLE session_events_backup;
CREATE INDEX session_events_session ON session_events (session_id, created_at);
CREATE INDEX session_events_claude ON session_events (claude_session_id, created_at);
