-- 0011: settings (P6.6).
--
-- Durable facts about this install, such as setup_completed and the last
-- result of each setup step. Unlike kv_cache, the cache controls (P5.3)
-- never clear this table.
CREATE TABLE settings (
    key        TEXT    PRIMARY KEY,
    value      TEXT    NOT NULL,
    updated_at INTEGER NOT NULL
);
