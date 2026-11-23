-- 0010: forge mcp (P6.5).
--
-- proposed_issues holds issues agents filed through forge mcp. They wait on
-- Home until the user accepts (creates them in Plane), edits or drops them.
-- With mcp.auto_create_issues they are created at once and kept here as
-- accepted, for attribution.
CREATE TABLE proposed_issues (
    id               INTEGER PRIMARY KEY,
    session_id       INTEGER REFERENCES sessions (id) ON DELETE SET NULL,
    session_name     TEXT    NOT NULL,
    project          TEXT    NOT NULL,
    plane_project_id TEXT,
    title            TEXT    NOT NULL,
    description      TEXT,
    priority         TEXT,
    labels           TEXT,
    status           TEXT    NOT NULL DEFAULT 'proposed'
                             CHECK (status IN ('proposed', 'accepted', 'dropped')),
    plane_issue_id   TEXT,
    plane_key        TEXT,
    created_at       INTEGER NOT NULL,
    updated_at       INTEGER NOT NULL
);
CREATE INDEX proposed_issues_status ON proposed_issues (status, project);

-- Written by the agent through forge mcp, never by Update: the latest
-- progress line and a summary the archive keeps.
ALTER TABLE sessions ADD COLUMN progress TEXT;
ALTER TABLE sessions ADD COLUMN progress_at INTEGER;
ALTER TABLE sessions ADD COLUMN agent_summary TEXT;

-- links gains the kinds forge mcp writes: proposals, docs and artifacts.
CREATE TABLE links_new (
    id         INTEGER PRIMARY KEY,
    from_kind  TEXT    NOT NULL CHECK (from_kind IN ('session', 'plane_issue', 'github_pr', 'github_run', 'proposed_issue', 'doc', 'artifact')),
    from_id    TEXT    NOT NULL,
    to_kind    TEXT    NOT NULL CHECK (to_kind IN ('session', 'plane_issue', 'github_pr', 'github_run', 'proposed_issue', 'doc', 'artifact')),
    to_id      TEXT    NOT NULL,
    url        TEXT,
    title      TEXT,
    state      TEXT,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    UNIQUE (from_kind, from_id, to_kind, to_id)
);

INSERT INTO links_new (id, from_kind, from_id, to_kind, to_id, url, title, state, created_at, updated_at)
    SELECT id, from_kind, from_id, to_kind, to_id, url, title, state, created_at, updated_at FROM links;

DROP TABLE links;
ALTER TABLE links_new RENAME TO links;
CREATE INDEX links_to ON links (to_kind, to_id);
