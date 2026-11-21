-- 0006: repos forge remembers (zero-config projects). A repo is recorded
-- when forge starts in it, runs a session in it or switches to it, so the
-- project list grows beyond the scanned folders; the Plane project picked
-- for it lives here too (plane_decided = 1 with plane_project_id NULL means
-- "no Plane project").

CREATE TABLE projects (
    repo             TEXT    PRIMARY KEY COLLATE NOCASE, -- owner/name
    path             TEXT,                               -- last known checkout
    last_used_at     INTEGER,                            -- unix millis
    plane_project_id TEXT,
    plane_decided    INTEGER NOT NULL DEFAULT 0
);
