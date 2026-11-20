-- 0003: record who owns a session's branch and worktree (P1.9).
--
-- branch_created: forge created the branch (agent/<name>, or a new custom
-- branch). Only such branches are ever offered for deletion; a pre-existing
-- local branch or a PR head branch is never deleted. Every session before
-- this migration ran on a forge-made agent/ branch, hence the default.
--
-- worktree_external: the session runs in a worktree forge did not create
-- (e.g. the main checkout, picked when the branch was checked out there).
-- forge never removes such a worktree.

ALTER TABLE sessions ADD COLUMN branch_created INTEGER NOT NULL DEFAULT 1;
ALTER TABLE sessions ADD COLUMN worktree_external INTEGER NOT NULL DEFAULT 0;
