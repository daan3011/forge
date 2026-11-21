-- 0005: allow link kind 'github_run' (P2.8): a "fix this" session started
-- for a failed workflow run whose branch has no PR is linked to that run.
--
-- SQLite cannot alter a CHECK constraint, so links is rebuilt. Nothing
-- references links, so the plain copy / drop / rename is safe.

CREATE TABLE links_new (
    id         INTEGER PRIMARY KEY,
    from_kind  TEXT    NOT NULL CHECK (from_kind IN ('session', 'plane_issue', 'github_pr', 'github_run')),
    from_id    TEXT    NOT NULL,
    to_kind    TEXT    NOT NULL CHECK (to_kind IN ('session', 'plane_issue', 'github_pr', 'github_run')),
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
