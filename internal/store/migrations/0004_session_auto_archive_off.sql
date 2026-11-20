-- 0004: auto_archive_off (P1.10). Set when an archived session is restored,
-- so the next PR sync doesn't move it straight back to the archive although
-- its PR is still merged or closed.

ALTER TABLE sessions ADD COLUMN auto_archive_off INTEGER NOT NULL DEFAULT 0;
