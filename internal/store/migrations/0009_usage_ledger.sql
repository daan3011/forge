-- 0009: usage ledger (P5.4). Every assistant message with a usage block, from
-- every transcript under ~/.claude/projects, keyed by message id (or request
-- id) so a message copied into a resumed session's file counts once: the
-- first file ingested keeps it. The daily rollups in usage are rebuilt from
-- this ledger, so it also keeps history after Claude Code deletes old
-- transcripts.

CREATE TABLE usage_messages (
    msg_key            TEXT    PRIMARY KEY,
    file               TEXT    NOT NULL,
    claude_session_id  TEXT    NOT NULL,
    cwd                TEXT    NOT NULL DEFAULT '',
    model              TEXT    NOT NULL DEFAULT '',
    day                TEXT    NOT NULL, -- local date, YYYY-MM-DD
    ts                 INTEGER NOT NULL, -- unix millis
    input_tokens       INTEGER NOT NULL DEFAULT 0,
    output_tokens      INTEGER NOT NULL DEFAULT 0,
    cache_read_tokens  INTEGER NOT NULL DEFAULT 0,
    cache_write_tokens INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX usage_messages_day ON usage_messages (day);
CREATE INDEX usage_messages_session ON usage_messages (claude_session_id);
CREATE INDEX usage_messages_file ON usage_messages (file);

-- How far each transcript file has been read. head hashes the first bytes
-- so a replaced file is noticed and read again.
CREATE TABLE usage_files (
    path       TEXT    PRIMARY KEY,
    size       INTEGER NOT NULL,
    offset     INTEGER NOT NULL,
    head       INTEGER NOT NULL,
    head_len   INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
