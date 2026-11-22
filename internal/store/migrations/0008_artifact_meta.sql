-- 0008: artifact pins, tags and titles (P4.6). tags and titles are keyed by
-- (kind, ref) like pins, so other item kinds (and Home, P6.1) can reuse
-- them; for artifacts kind is 'artifact' and ref is the file path. They are
-- not pruned with the artifacts index, so a file that comes back keeps them.

CREATE TABLE tags (
    kind TEXT NOT NULL,
    ref  TEXT NOT NULL,
    tag  TEXT NOT NULL,
    PRIMARY KEY (kind, ref, tag)
);
CREATE INDEX tags_by_tag ON tags (kind, tag);

CREATE TABLE titles (
    kind  TEXT NOT NULL,
    ref   TEXT NOT NULL,
    title TEXT NOT NULL,
    PRIMARY KEY (kind, ref)
);

-- A pinned copy in .forge/artifacts kept in sync with its original in a
-- session worktree until detached (worktree removed, session archived, or
-- by hand); after that the copy is the final version.
CREATE TABLE artifact_links (
    copy_path     TEXT    PRIMARY KEY,
    source_path   TEXT    NOT NULL,
    session_id    INTEGER,                    -- sessions.id, not a foreign key: the session may go
    session_name  TEXT    NOT NULL DEFAULT '',
    synced_at     INTEGER,                    -- unix millis, last copy
    detached_at   INTEGER,                    -- unix millis; NULL while syncing
    detach_reason TEXT
);
