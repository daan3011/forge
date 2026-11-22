-- 0007: the artifact gallery index (P4.5). One row per file found by the
-- watch globs; rows are pruned when their file is gone from a scanned root.
-- created_at is the file's mtime when first seen (there is no portable
-- birth time) and is kept on later scans.

CREATE TABLE artifacts (
    path        TEXT    PRIMARY KEY,
    project     TEXT    NOT NULL DEFAULT '',
    session_id  INTEGER,                      -- sessions.id when in its worktree
    type        TEXT    NOT NULL,             -- html | svg | png | ... | file
    size        INTEGER NOT NULL,
    created_at  INTEGER NOT NULL,             -- unix millis
    modified_at INTEGER NOT NULL,             -- unix millis
    seen_at     INTEGER NOT NULL              -- unix millis, last scan that found it
);
CREATE INDEX artifacts_project ON artifacts (project, modified_at DESC);
