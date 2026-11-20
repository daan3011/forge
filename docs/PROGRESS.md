# forge — Progress

Feature IDs match docs/SPEC.md. Tick a box when its feature is done.

## Phase 0 — Foundations

- [x] P0.1 Project scaffold
- [x] P0.2 Config
- [x] P0.3 Store
- [x] P0.4 App shell & theme
- [x] P0.5 Keymap & help
- [x] P0.6 Command palette
- [x] P0.7 Project switcher
- [x] P0.8 Background refresh — tabs load cache-first via `tabkit.LoadCached`/`FetchAndCache`; `internal/refresh` ticks per source. GitHub rate-limit backoff (`github.RateLimitMsg`) is wired but only takes effect once the P2.1 client emits it via `github.Observe`.
- [x] P0.9 Forge animations — `internal/ui/anim`: one shared `Engine` clock (12 fps, ticks only while something visible animates; never with `animations: off` or `FORGE_NO_ANIM`), Splash, HammerFrame (+ `anim.Hammer` bubbles spinner), HeatBar, Burst, Flame, EmptyForge, harmonica `Spring` (split divider, palette slide-in). `ui.SuccessMsg` gives a success toast with a spark burst; nothing emits it yet. `Flame` is ready for P1.4 statuses. Hidden `forge demo anim [--level]` shows everything.

## Phase 1 — Agent sessions

- [x] P1.1 tmux driver — `internal/sessions/tmux`: `Driver` interface + `Exec` (every call passes `-L forge -f <conf>`; see the isolation follow-up below). Sanitised names are the stable ids, exact-match targets (`=sess:=win`), argv-only (no shell). Each session gets a hidden `_forge` placeholder window; server sets `remain-on-exit` so exited agents read as dead and keep their output. `Attach` switches client when on the forge socket, else returns an `*exec.Cmd` (TMUX stripped, not killed by ctx) for `tea.ExecProcess`. `tmuxtest.Fake` + shared `RunConformance`; real-server tests via `go test -tags tmux ./internal/sessions/tmux/...`. Hidden `forge demo tmux` exercises attach/detach.
  - [x] P1.1 follow-up: isolated tmux server. forge's server uses only its own config: an embedded, fully commented default (`internal/sessions/tmux/forge.tmux.conf.tmpl`, header `# forge-tmux-config v1`, colours from `ui.TmuxColors`), written by `tmux.EnsureConfig` to `$XDG_CONFIG_HOME/forge/tmux.conf` on first run and never overwritten. It has no prefix, prefix-less `M-d` / `M-[` / `M-w`, and an ember status line. `Exec.base()` always emits `-L forge -f <conf>` (`/dev/null` without `WithConfig`), so `~/.tmux.conf` never loads. `Driver.Attach` returns an `Attachment`:
    - `AttachSwitched` inside forge's server
    - `AttachExec` in a plain terminal, or inside an outer tmux older than 3.2 (TMUX stripped)
    - `AttachPopup` inside your own tmux: `display-popup -E -w 90% -h 90%` running `env -u TMUX tmux -L forge -f <conf> attach …`, shell-quoted via the new `internal/shell`. It blocks until the popup closes (checked on tmux 3.8), and warns if the outer tmux binds `M-d` in its root table.
    
    `Driver` gains `ListAll` and `KillServer`. New `forge tmux restart [--yes]` and `forge tmux config [--reset]` (reset keeps `tmux.conf.bak.<timestamp>`), also Sessions-tab palette commands ("tmux: …"). `forge demo tmux` handles all three attach modes and refreshes after detach / popup close. The Sessions tab's own Enter-to-attach comes with P1.5. Real-server test checks that `~/.tmux.conf` is ignored, `prefix` is `None`, and `#{config_files}` is forge's file.
- [x] P1.2 Worktree manager — `internal/sessions/worktree`: `Manager` (`Create` / `List` / `Remove` / `DeleteBranch`) over a fakeable `Git` interface (`Exec` = git binary, argv only, never prompts). Worktrees live at `<worktree_root>/<repo>/<session>` on `agent/<session>`, created `--no-track` from `origin/<base>` after a fetch (offline falls back to the local base, reported as `FetchErr`). Existing branch → `ErrBranchExists` (retry with `ReuseBranch`); per-project `worktree_copy` globs copied in (never overwriting tracked files, rolled back on failure). `Reconcile` maps worktrees to sessions → matched / orphans / missing. `Remove` refuses dirty worktrees (`*DirtyError`) unless forced and reports `BranchMerged` (ancestry only; squash merges not detected). Tests run against real temp repos with an isolated git config.
- [x] P1.3 Start named session — `n` (or `forge new`, flags or the same Huh form) asks name / project / base / template / first prompt. `internal/sessions.Service` creates the worktree, writes the prompt to `<data>/forge/sessions/<name>/prompt.md` and opens a tmux window in the worktree running `sh -c 'exec claude --session-id <uuid> -- "$(cat <file>)"'` (`internal/sessions/claude`; paths shell-quoted, the prompt never in a command line) with `FORGE_SESSION=<name>`, then saves the row; any failure rolls back what was done. Sessions tab: name / status / activity / project / branch / link, detail pane with a capture-pane preview every 2s while visible (only for live sessions; `tabs.VisibilityAware`). List refresh marks sessions with a gone/exited window `dead` until P1.4 hooks take over. `x`: kill → `dead` (row kept for P1.5 resume), or kill + remove worktree → `archived` (migration 0002; branch, uuid and links kept; dirty worktrees ask before forcing). `a` toggles the archived view, `D` deletes an archived row. `tmux.Driver.PasteText` (bracketed paste via a named buffer) is in place for P1.9/P2.8/P4.3 but unused. Templates field appears once P1.7 provides templates. Real-tmux end-to-end test: `go test -tags tmux ./internal/sessions/`.
- [x] P1.4 Live status via hooks — `forge hook <event>` (`cmd/forge/hook.go`) reads the hook JSON on stdin, records it in `session_events`, applies the status machine and pings the TUI. It always exits 0, prints nothing on stdout (SessionStart/UserPromptSubmit stdout would become Claude context), gives up after 2 s, and logs problems to `<data>/forge/hook.log` (rotated at 1 MiB). Logic lives in `internal/sessions/hooks`:
  - **Status machine** (`Next`): SessionStart → idle (not on compact); UserPromptSubmit / PreToolUse / PostToolUse → working; Notification `permission_prompt` / `elicitation_*` / `agent_needs_input` → waiting (`idle_prompt` comes after Stop, so it leaves the session idle); Stop → idle; StopFailure (turn ended on an API error) → error; SessionEnd → dead, except reason `clear` / `resume`. Archived sessions never change. A gone or exited tmux window → dead is still the list sweep.
  - **8 hooks** are registered with `timeout: 5` in each new worktree's `.claude/settings.local.json`: the six from the spec plus PostToolUse (PreToolUse fires *before* the permission prompt, so it's the first signal after you approve) and StopFailure. `hooks.Install` merges into an existing file (e.g. one copied by `worktree_copy`) and replaces earlier forge entries. `Worktrees.EnsureExcluded` git-ignores the file through the repo's shared `info/exclude` unless it's already ignored. Global settings are never touched. A real worktree session was confirmed to load its own `settings.local.json`.
  - **Finding the session**: by Claude session id. After `/clear` or `/resume`, Claude starts a new id: that SessionStart finds the session by `$FORGE_SESSION` and adopts the new id (`Sessions.SetClaudeID`). Other unknown ids, such as a nested `claude -p` that inherits the environment, are recorded unattributed and change nothing.
  - **Live socket** (`internal/sessions/live`): one JSON line per ping at `$XDG_DATA_HOME/forge/forge.sock` (mode 0600). A stale socket file is reclaimed. A second forge gets `ErrRunning`, prints a note and falls back to periodic refresh. Sends give up after 250 ms; no listener is not an error.
  - **Sessions tab**: a ping updates the row in memory at once (waiting sorts first), then reloads from the db, coalescing pings that land during a refresh. The Status column and the detail title show `anim.Flame` (flickering flame working, amber pulse waiting, anvil idle, ╳ error, ash dead). It animates only while a visible agent's flame moves, and respects `animations`. Tables gained an optional `ui.Column.Render`.
  - **capture-pane fallback**: while the selected session is waiting on a permission prompt and its pane shows no prompt for 2 captures in a row, `Service.ClearWaiting` moves it back to working (conditional `SetStatusIf`, so it never overwrites a hook). Detection is `claude.PermissionPromptVisible`, the one place that knows the prompt's text, tested against real captured panes in `internal/sessions/claude/testdata/`.
  - **Measured** end to end with a real Claude Code 2.1.329 session in an isolated forge: hook → TUI row in 50–90 ms for idle / working / waiting / idle / dead, plus `/clear` (stays alive, id adopted). Claude itself sends `permission_prompt` about 6 s after the prompt appears, so "waiting" shows about 6 s late; that delay is Claude's, not forge's.
  - **Limits**: sessions started before P1.4 have no hooks until they are resumed (P1.5 reinstalls them on every resume). The hook path is the absolute path of the forge binary that started the session, so a `go run` binary won't survive.
- [x] P1.5 Attach & resume — `enter` on a session attaches; `b` resumes it in the background; ctrl+g (palette: "Go to session…") opens a session picker matched by name, linked issue/PR (`Row.Links`, full id and `#n`) and branch, and picking attaches. Session entities in the ctrl+k palette match links too (`tabs.Entity.Search`). The flow lives in `internal/tabs/sessions/attach.go` and `internal/sessions/resume.go`:
  - **Inspect, ask, then Resume**: `Service.Inspect` reports `ResumePlan{Alive, WorktreeMissing, HasTranscript, PromptFile}` so the tab can ask first. A missing worktree → "Recreate it from agent/<name>?" (`Worktrees.Create` with `ReuseBranch`; if the branch is gone too it starts again from the base, with a note). No transcript but a saved first prompt → "Send its first prompt again?" (default yes). Esc cancels cleanly.
  - **Resume** re-reads the row, leaves a live agent alone, (re)installs hooks + `EnsureExcluded`, kills the dead pane (remain-on-exit) and opens a new window in the worktree with `FORGE_SESSION`, running `claude --resume <uuid>` when `claude.FindTranscript` finds `~/.claude/projects/*/<uuid>.jsonl`. Otherwise it runs `claude --session-id <uuid>` again (Claude writes no transcript until the first message). Such a fresh start is watched for up to 2 s (`StartupCheck`). If claude exits with "Session ID … is already in use" (`claude.UUIDInUse`), the session gets a new uuid (`SetClaudeID`; links are keyed by row id, so they stay). The row's tmux target is updated if it moved (`Sessions.SetWindow`), status → starting. If a later step fails, the new window is closed.
  - **Attach** (`Service.Attach`) uses the stored target: forge's own client switches in place, a popup of the user's tmux runs in the background, otherwise `tea.ExecProcess`. On detach (or popup close) the list and that session's preview refresh at once. A second enter while one is in flight is ignored; the tab shows busy meanwhile.
  - **Keys**: tab keys now have their own scope (`tab:sessions`). They may not clash with global/list keys or each other, but different tabs may reuse a key (`b` is free for the Plane board toggle in P3.3). `R` stays refresh-all; ctrl+r stays free for P2.7.
  - **Tests**: service table tests (fake tmux gained `OnStart` to simulate a refused uuid), tab flows incl. dialogs under teatest, app picker tests, and `TestResumeAfterRebootEndToEnd` (`-tags tmux`). That test kills the real tmux server, resumes with `--resume <uuid>` in the worktree, then deletes the worktree and recreates it from the branch.
  - **Limits**: the picker lists the current project's sessions (as the Sessions tab does); switch to "All projects" to reach others. Archived sessions can't be resumed until they are restored with `A` (P1.10); a removed worktree is then recreated from the branch.
- [x] P1.6 Transcript viewer — `t` on a session (palette: "Open session transcript") opens a full-screen viewer, so you can read what an agent did without attaching. The parser lives in `internal/sessions/transcript`; the viewer is in `internal/tabs/sessions/transcript.go`.
  - **Format**: checked against real Claude Code 2.1.x files. Only `user`, `assistant` and `system` lines are read; other types and malformed lines are skipped.
    - One API message is written as one line per content block, all with the same `message.id`, and every line repeats `message.usage`. So text blocks are merged, and usage is counted once per id.
    - User lines are either a prompt (a string) or `tool_result` blocks, which are paired with their `tool_use` by id.
    - `/command` wrappers and compactions become one-line notices. `thinking` blocks, `isMeta` lines and subagent (`isSidechain`) lines are skipped.
  - **Locating**: `transcript.Locate` uses the session's `transcript_path` from the hooks if that file exists. Otherwise it falls back to `claude.FindTranscript` (`~/.claude/projects/*/<uuid>.jsonl`). On each load it uses the session's current row, so a new path (e.g. after `/clear`) is picked up.
  - **Incremental parsing**: `transcript.Cache` remembers per file the byte offset and parser state.
    - Loading again only parses the bytes appended since. A half-written last line waits for the next load. A truncated or replaced file (different inode) starts over.
    - On a 28 MB transcript the first parse takes 230 ms and re-opening takes 54 µs.
    - The open viewer re-reads every 2 s while its tab is visible.
  - **Viewer**:
    - Summary header: messages, tool calls, files touched by Edit/Write/MultiEdit/NotebookEdit (relative to the worktree, first 4 then `+N`), last activity, and tokens (`in · out · cache` from usage, or `~chars/4` when the file has none).
    - Prompts are shown as text; assistant replies are rendered with Glamour v2 in forge colours (`Theme.GlamourStyle`). Glamour runs in a `tea.Cmd`, cached per entry and width, with plain text shown until it's ready.
    - Each tool call is one line `▸ ⚒ Bash  Run tests ✓/✗/…`. `enter` expands it to show its input (`$ cmd`, an Edit's -/+ lines, Write content, otherwise indented JSON) and its result, each capped at 40 lines. `e` toggles all.
    - `j/k` move entry by entry; `ctrl+d/u`, `g/G` jump; `f` follows new entries (on when opened); `esc`/`q` closes.
    - The viewer keys have their own modal scope (`transcript`), so `enter` inside the viewer never reaches Attach. Toggle, expand-all, follow and close are palette commands while the viewer is open.
  - **Tests**: table-driven parser tests on a fixture (split blocks, usage dedup, unknown/malformed lines, result pairing, wrappers, chars/4), cache tests (partial line, append, truncate, replace) and `Locate` tests. There are teatest flows for open → expand → expand all → close → enter attaches, live refresh, missing transcript, and the palette commands. `go test ./internal/sessions/transcript -run TestRealFiles -real ~/.claude/projects` parses every local transcript (63 files here, no errors).
  - **Limits**: subagent transcripts are not shown. The offset cache lives in memory per forge process; `transcript_offset` is left for the persistent cache in P5.3. A resize re-renders the markdown on the next 2 s poll (plain text until then).
- [x] P1.7 Session templates — markdown files in `~/.config/forge/templates/` (`$XDG_CONFIG_HOME` respected). The logic is in `internal/templates`; the form steps are in `internal/tabs/sessions/templateflow.go`.
  - **Format**: optional YAML front matter with `name` (default: file name), `description` and `variables`. Each variable is a bare name or `{name, description, default}`. The body is the prompt. `{name}` placeholders use lower-case letters, digits and `_`; `\{` gives a literal brace. Any `{var}` in the body counts, declared or not; declaring one only adds a description and a default.
  - **Defaults**: implement-issue, fix-ci, review-pr, refactor, write-tests and explore are embedded. `EnsureDefaults` writes them only when the folder doesn't exist yet, so edits are kept and deleted defaults stay deleted. Broken files are skipped with an error toast (a warning in `forge new`).
  - **Variables** are resolved by `templates.Resolve`:
    - built-ins: `{repo}`, `{branch}` (`agent/<name>`), `{docs}` (`Project.DocsPath()`), and `{issue}` / `{pr}` from the ref
    - then a `ContextProvider` per ref, from a `Registry` keyed by link kind (`plane_issue`, `github_pr`)
    - then front-matter defaults
    - explicit values (form fields, `--var`) always win
    
    What's left is asked for in the form, one field per variable (multi-line for `*_body` / `*_log`). The registry is **empty for now**: P2.1 and P3.1 register providers. Until then, the issue/PR title and body are typed in, and that manual fallback stays for sessions with no ticket.
  - **Form** (`n`):
    - The template picker sits in the main form, with a live preview beside it. The preview is rendered in the tab's `View`, not as a huh Note, because `Note.DescriptionFunc` runs in a command and races the input fields. It shows the description and the body, with built-ins filled in and the rest left as `{var}`.
    - With a template chosen, the prompt field is hidden. The steps after it are chained dialogs:
      1. Issue / PR refs, only those the template uses and the request doesn't already carry.
      2. Resolve, in a `tea.Cmd`. A provider error shows as an info toast and its variables fall back to fields.
      3. The missing variables.
      4. The rendered prompt, editable.
    - Esc at any step cancels. `sessions.default_template` is pre-selected.
    - `NewRequest{Opts, Refs, Vars}` is the entry point P1.9 / P2.8 call with a ref or a failing log. `StartOpts` is unchanged.
  - **`forge new`**: new flags `--template`, `--issue`, `--pr` and `--var name=value` (repeatable). Without a terminal, missing variables are an error that names them. An explicit `--prompt` wins over the template, which is then only recorded. An unknown template name is an error.
  - **Palette**: "Edit templates" (global) creates the folder if needed, then opens it in `$VISUAL` / `$EDITOR` via `tea.ExecProcess`.
  - **Tests**:
    - table tests for parse / render / load / defaults / resolve
    - teatest done-when (`TestImplementIssueFillsPromptFromProvider`): a fake `plane_issue` provider, implement-issue + PROJ-123, and the prompt holds its title and body
    - a manual-variables flow and cancel
    - CLI flag tests and the palette command
- [x] P1.8 Waiting notifications — desktop notifications through `internal/notify` (`gen2brain/beeep`, falling back to `osascript` on macOS / `notify-send` elsewhere; `notifytest.Recorder` for tests). The rules are in `internal/sessions/alert`, which both `forge hook` and the TUI use.
  - **Debounce**: `store.Sessions.ClaimWaitingNotification` is a compare-and-set on the existing `waiting_notified_at` column (`status = 'waiting' AND waiting_notified_at IS NULL`). Whoever wins the claim notifies, so one waiting spell gives exactly one notification even when the hook and the TUI race. `SetStatus` already clears the claim on a status change, so the next spell notifies again. No migration was needed.
  - **TUI path**: every sessions refresh tick (which runs even when the Sessions tab is hidden), plus every hook ping, runs `Checker.CheckWaiting` in a `tea.Cmd`. It covers every project's unarchived waiting sessions and notifies "<name> is waiting for you" once a session has waited `sessions.notify_after_minutes` (0 = never). The same check counts the waiting sessions for the status bar badge `● N waiting`, shown on every tab (`ui.StatusInfo.Waiting`, `Theme.StatusWait`). The Sessions tab forwards each ping to the root model as `tabs.SessionPingMsg`. While the terminal isn't focused (`View.ReportFocus`, `tea.FocusMsg` / `tea.BlurMsg`; assumed focused until told otherwise), Stop gives "<name> finished" and StopFailure gives "<name> hit an error", controlled by the new `sessions.notify_on_finish` / `notify_on_error` (default on).
  - **Hook path**: `live.Sender.Send` now reports whether a TUI received the ping. When none did (nothing listening, or the send failed), `forge hook` notifies itself. Waiting notifies at once, debounced by the same claim, because no process is left to wait N minutes. Errors and finished turns follow the toggles. The hook reads the config without creating it; a missing file means the defaults.
  - **Watched windows**: both paths skip a session whose tmux window is the active window of an attached session on the forge socket (`tmux.Driver.WindowWatched`, `list-windows -F '#{window_name}\t#{window_active}\t#{session_attached}'`). A watched waiting session isn't claimed, so it notifies once you look away.
  - **Palette**: "Notifications: send test notification".
  - **Tests**:
    - done-when `TestWaitingNotifiesExactlyOnce` (alert) and `TestAlertsWaitingExactlyOnce` (app, through the refresh tick)
    - store claim table test
    - hook table tests: no TUI, TUI running, watched, toggles off, `notify_after_minutes: 0`, a failed send
    - focus tests and a teatest that the waiting count shows on another tab
    - fallback argv and escaping tests
    - fake and real-tmux conformance for `WindowWatched`
    
    Checked by hand with the built binary and an isolated database: three Notification events gave one claim and one macOS notification.
  - **Limits**: the hook path ignores `notify_after_minutes`. If the TUI quits while a session waits but before N minutes pass, that spell isn't notified. Focus reports need terminal support; inside tmux they also need `focus-events on`. Without them forge counts as focused and doesn't notify about errors or finished turns while it runs.
- [x] P1.9 Session ↔ issue/PR links — sessions know the Plane issue and GitHub PRs they are for. The links table and `store.Links` existed since 0001 (generic edges with cached url / title / state), so no new table.
  - **Start from a source**: `tabs.StartSessionFrom(tabs.Source{Kind, ID, Number, URL, Title, State, Repo, Base, Branch, Template, Vars})` returns a `tabs.StartSessionMsg`. The root model switches to the `tabs.SessionStarter` tab (Sessions), which opens the new-session form pre-filled:
    - name: the lower-cased issue key (`proj-123`) or `pr-<n>`, with `-2`, `-3`… if listed
    - project: from `Repo`; base, branch and vars as given
    - template: `implement-issue` / `review-pr` when present, else the default
    - `{issue_title}` / `{pr_title}` from the source's title, so that much is known before P2.1 / P3.1 providers exist
    
    The session is linked to the source on start (`StartOpts.Links`, written by `Service.Start`, rolled back with the row). Issues and PRs typed into the template's refs form, or given to `forge new --issue/--pr`, are linked too (`sessionstab.WithRefLinks`: `#12`, `12`, `owner/repo#12` or a PR URL; issue URLs come from `plane.base_url` + `workspace_slug`).
  - **Branch override**: `StartOpts.Branch` / `worktree.CreateOpts.Branch`, the new form's Branch field and `forge new --branch`. Empty keeps `agent/<name>`. A custom branch is used as found:
    1. an existing local branch is checked out
    2. a branch only on origin (a PR head) gets a local tracking branch (`WorktreeAddTracking`)
    3. otherwise a new branch is made from the base
    
    `Resume` recreates a missing worktree on the stored branch the same way.
  - **Branch checked out elsewhere**: git refuses to check out a branch twice. `worktree.BranchCheckedOutError{Branch, Path}` becomes `sessions.BranchInUseError{Branch, Path, Session}`. The tab then offers "Go to session <name>" when it's a forge session's worktree. Otherwise (e.g. the main checkout) it offers "Start the session in <path>": `StartOpts.Worktree`, recorded as `worktree_external`. forge never removes or recreates that worktree; "remove worktree" then only archives.
  - **Branch ownership** (migration 0003): `branch_created` (default 1: every earlier session ran on a forge-made `agent/` branch) and `worktree_external`. The kill dialog offers "Kill, remove worktree and delete branch" only when `sessions.CanDeleteBranch` holds, i.e. forge made the branch in its own worktree. `KillOpts.DeleteBranch` is refused with `ErrNotOwnBranch` otherwise, before anything is killed. A reused local branch or a PR head branch is never deleted, not even by Start's rollback. `Worktrees.DeleteBranch` now takes the branch name.
  - **PR auto-link**: `Service.SyncPRs(project)` asks a `github.PullFinder` for each active session's PRs by head branch and links up to 5 newest. It also refreshes title and state (open / draft / merged / closed); sessions on their base branch are skipped.
    - `github.REST` is the PullFinder: go-gh v2.12.2 (reuses `gh auth`), `GET repos/{repo}/pulls?head=owner:branch&state=all`. Every response goes through `OnResponse`, which the TUI wires to `github.Observe`, so the rate-limit backoff now really runs.
    - The Sessions tab syncs once at start and on every GitHub tick. A new optional `tabs.SourceRefresher` lets the root model call `RefreshSource(src)` instead of `Refresh`. A failed sync shows one info toast per run.
  - **Detail pane**: `issue:` and `pr:` lines with key / `#n`, title and state (coloured by `Theme.LinkState`), `(+N more)`. Open PRs come before merged / closed ones (`sessions.LinksOf`), and the list's Link column also prefers an open PR.
  - **Open keys**: `o` opens the linked PR, or the issue when there is no PR; `O` opens the issue. More than one link of that kind gives a picker. Both are also palette commands and appear in help. URLs go through the new `internal/browser` (`open` / `xdg-open`, argv only, http(s) URLs only; `browser.Fake` for tests).
  - **For other tabs**: `Service.LinkedSessions(ctx, kind, id)` (on `sessions.API`) returns the sessions linked to an issue key or `owner/repo#n`.
  - **Tests**:
    - worktree cases against real repos: new branch from base, existing local, remote-only PR head (tracking set), checked out in the main checkout and in another worktree, invalid name
    - service tables: links saved on start, custom-branch ownership, rollback keeps a foreign branch, BranchInUse (main checkout / another session / run in the external worktree), delete-only-own-branches, SyncPRs with a fake finder
    - GitHub parser and httptest-style transport tests; migration 0003 defaults
    - teatest flows incl. the "done when" (`TestStartFromIssueThenPR`): start from an issue → detail shows the issue → GitHub tick → detail shows its PR → `o` / `O` open them
    - also: picker, branch-in-use dialogs, kill options, and app routing of `StartSessionFrom` / `RefreshSource`
  - **Limits**: PRs from forks aren't matched (head filter is `owner:branch` of the repo itself). Issue states only change when P3.1 calls `Links.SetState`; until then the detail pane shows the state the source had when the session started.
- [x] P1.10 Auto-archive — sessions whose PRs are all merged or closed move to the archive with a summary; notes and summary are searchable from the palette. Service side in `internal/sessions/archive.go`, tab side in `internal/tabs/sessions/archive.go`.
  - **Two archive states**: `archived_at` set = in the archive. `status = archived` (0002) additionally means the worktree was removed. An archived session that kept its worktree keeps its last status.
  - **Auto-archive**: the PR sync Cmd (GitHub ticks, plus once at start) runs `Service.AutoArchive(project)` right after `SyncPRs`, since link states only change there. A session is archived when:
    - it has ≥1 PR link and every PR is `merged` / `closed` (`sessions.PRsFinished`)
    - it isn't `working` / `waiting`; busy agents are picked up on a later tick
    - it wasn't restored from the archive: migration 0004 adds `auto_archive_off`, set by `Unarchive`
    
    A toast says "<name> archived — PR #12 merged" (or "N sessions archived (PRs done): …").
  - **Summary** (`Service.Summarize`): extracted, no LLM. It has one line per link (`PR owner/repo#12 merged — title`, `issue PROJ-1 …`), then `transcript.Brief`:
    - messages · tool calls · files touched (relative to the worktree, first 5 then `+N`)
    - the last 3 assistant replies, one line each, ≤240 runes
    
    A missing transcript reads "no transcript". `Kill{RemoveWorktree}` writes a summary too, so every archived session has one. Notes are never touched. `ArchiveRemoved` now keeps an earlier `archived_at`. `transcript.ShortFiles` moved out of the viewer.
  - **Cleanup**: new config `sessions.archive_cleanup: ask | always | never` (default `ask`), through the existing kill flow:
    - `always` removes worktree + tmux window, never forced; a dirty worktree still gets the "remove anyway?" dialog
    - `ask` queues one dialog per session: remove / remove + delete branch (only `CanDeleteBranch`) / keep. Questions open one at a time, only when no other dialog, transcript or filter is up. One pushed aside by another dialog goes back in the queue (`dialog.displaced`).
    - `never` leaves them
  - **Keys** (also palette commands and help):
    - `A` (new `session_archive`) archives the selected session, with the same cleanup; in the archived view it restores it (`Service.Unarchive`: a removed worktree comes back `dead`, so enter / `b` recreate it via P1.5)
    - `x` in the archived view removes a kept worktree + window
    - `D` still needs the worktree gone, and says so
    - enter / `b` on an archived session point at `A`
  - **Lists & palette**: every refresh loads both lists, so `a` switches at once. Archived sessions are `tabs.Entity{Kind: "Archived session"}` (`sessionstab.EntityArchivedSession`), searchable by links, branch, summary and notes. They're offered from either view; picking one opens the archived view on it. The ctrl+g picker stays active-only (`EntitySession`).
  - **Detail pane**: archived rows show `archived: <date> · PR #12 merged [· worktree kept]` and a `── summary ──` / `── notes ──` section in place of the output preview (no capture-pane polling).
  - **Tests**:
    - store: migration 0004 default, `Unarchive` (flag, archived → dead), `ArchiveRemoved` keeps `archived_at`
    - `Brief` / `ShortFiles` tables
    - service: `AutoArchive` table (merged, closed, mixed, draft, no PR, issue only, working, waiting, restored); done-when `TestMergedPRArchivesWithSummary` (fake `PullFinder` open → merged, summary from the transcript fixture); archive / cleanup / restore round trip; Kill writes a summary
    - config enum
    - tab: done-when `TestMergedPRArchivesSession` (teatest: GitHub tick → toast → two queued cleanup questions → archived view shows the summary); cleanup modes; busy sessions wait; manual archive + restore; `x` in the archived view; palette entities + select
    
    Real-tmux suite (`-tags tmux`) still passes.
  - **Limits**: auto-archive runs only while forge runs, on GitHub ticks. A session started from an already-merged PR is archived on the next tick (restore it to keep it). There's no notes editor yet; notes are kept and searched when present.

## Phase 2 — GitHub

- [x] P2.1 PR list & sections — the PRs tab shows one GitHub search per configured section, laid out after `docs/design/gh-dash.jpeg` in forge colours. The tab is in `internal/tabs/prs`, the client in `internal/github`.
  - **Client**: `github.REST` became `github.Client`, forge's only GitHub client (`githubPulls` in `cmd/forge`). It makes REST (P1.9 links) and GraphQL clients on first use over one observing transport, so PR searches report the rate limit through `OnResponse` / `Observe` too.
    - `SearchPRs(ctx, query, limit)` is one GraphQL `search(type: ISSUE)` per section, fetching repo, number, title, author, isDraft, state, reviewDecision, the head commit's `statusCheckRollup` state, additions / deletions, created / updated, head / base ref, url and mergeable, plus `viewer { login }` and `issueCount`.
    - `BuildQuery` adds `is:pr` when missing, `repo:<owner/name>` for the active project (server-side, so counts are right) and `sort:updated-desc` unless the query sorts itself.
    - `SectionLimit` = 50. `IsAuthError` recognises a missing or refused `gh` login.
    - The query was checked against the live API.
  - **Template provider** (the P1.7 TODO): `Client.PRContext` resolves `{pr_title}` / `{pr_body}` for `owner/repo#n` or a PR URL. `main` registers it for `github_pr` (`defaultProviders`). A bare `#12` can't be resolved (the provider gets no repo) and falls back to the form.
  - **Sections**: the default config is now My PRs (`is:open author:@me`), Review requested (`is:open review-requested:@me`) and Involved (`is:open involves:@me`); existing config files keep their own sections.
    - Each section is cached in kv_cache under `tab:prs:<project>:<slug>:<fnv(query)>`, so editing a query never shows stale rows.
    - Every refresh loads the cache first, then fetches all sections, so every count fills. A refresh with several failing sections gives one toast. Results for a project you've left are dropped. GitHub ticks only refresh while the tab is visible.
  - **Layout**:
    - section bar (`ui.SectionBar`: `⌕ │ My PRs (3) │ Review requested (0) │ Involved (2)`, compacting to counts when narrow)
    - bordered search box with the query as sent (plus `· repo: x` when filtered)
    - two-line rows: state icon · repo `· #n` over the title · review · CI · `+1.2k -34` · updated · created (`ui.ShortAge`: 5m / 3h / 2d / 1w / 4mo / 1y)
    - The list starts at 65% (`SplitPane.SetRatio`). The right pane is a summary until P2.2: repo · #n, title, state, base ← head, author and ages, review / checks / conflicts lines, URL.
    - Empty states explain themselves: no client, `gh auth login`, loading, no PRs from the filtered repo.
  - **Shared UI**:
    - `ui.Table.SetRowHeight(n)` (cells split on `\n`; a `Render` returns n lines; scrolling and paging count rows)
    - `ui.Column.Hidden` (never drawn, still matched by `/`; PRs keep their id and `@author head base` there)
    - `StatusInfo.Detail` via `tabs.StatusDetailer`: PRs shows `@login · PR 3/12 (fetched 12 of 57)`. It is dropped after the hints.
    - Theme slot `patina` (green: approvals, passing checks, additions), `PRIcon` / `ReviewIcon` / `CIIcon` and the PR styles.
  - **Filtering**:
    - `/` filters the section's rows by repo, title, author or branch.
    - `f` opens a huh picker (`All repos` + every repo in the loaded sections or configured projects). The choice narrows every section client-side and updates the counts.
    - A project switch clears the repo filter and re-queries with `repo:`.
  - **Keys** (scope `tab:prs`, configurable): `pr_next_section` l/→, `pr_prev_section` h/←, `pr_repo_filter` f.
  - **Palette**: Filter PRs, next / previous section, filter by repo…, show all repos (while filtered), one command per section, widen / narrow. PR entities (`#n title`, `repo · @author`, searchable by head branch) jump to the first section holding the PR, dropping filters that hide it.
  - **Tests**:
    - github: `ParseSearch` fixture (draft, ghost author, no checks, non-PR nodes), `BuildQuery`, `SearchPRs` / `PRContext` over a fake transport (variables, errors, `OnResponse`), `ParsePRRef`, `IsAuthError`
    - ui: two-line rows / hidden columns / paging, `SectionBar` compacting and width, status detail dropping, `ShortAge`, icons, `patina` override
    - tab: sections and counts, cursor and section status detail, `h` / `l` wrapping; done-when `TestRepoFilter` (`f` → acme/bubbles narrows every section with counts, esc keeps it, All repos restores); text filter on hidden branch text; cache-first with a failing fetch (one toast); auth error; no client; active project (query, cache key, stale fetch dropped); entities / select; palette commands
    - app: the PRs flows now run against the real tab with a fake `PRSearcher` (`WithGitHub`)
    
    Checked by hand in an isolated forge against the real API: sections, counts, rows, detail, status bar and the repo filter.
  - **Limits**: at most 50 PRs per section (the status bar shows `of N`). Ages are computed when rows are built, i.e. on each refresh. "Start session from PR" comes with the P2.3 quick actions (`tabs.StartSessionFrom(tabs.Source{Kind: github_pr, ID, Number, Branch: HeadRef, …})`). There is no app version in the tab bar yet (P6.4).
- [x] P2.2 PR detail pane — the PRs tab's right pane is now a real detail view after `docs/design/gh-dash.jpeg`: header, then Overview / Checks / Activity. Code in `internal/tabs/prs/detail.go` (state, loading) and `detailview.go` (view); client in `internal/github/detail.go`.
  - **Client**: `Client.PRDetail(ctx, repo, n)` is one GraphQL query (body, labels, review requests, latest reviews, merge state, the head commit's check runs and status contexts, last 30 comments and reviews), parsed by `ParsePRDetail`.
    - Checks are normalised (`github.Check*`: fail / running / pending / pass / skipped) and listed failing first; `Check.Duration`.
    - Reviewers merge latest reviews with requests (a re-requested reviewer is pending again); empty "commented" reviews that only carry inline replies and drafts are left out of Activity, which is newest first.
    - `github.PRClient` (= `PRSearcher` + `PRDetailer`) is now the type of `tabkit.Deps.GitHub` / `app.WithGitHub`.
    - The query was checked against the live API.
  - **Loading** ("no lag"):
    - The header and status cards come from the list row, so they follow the cursor immediately.
    - Details are fetched once the cursor rests for 150ms (`detailDebounce`, a generation counter drops older ticks); only that PR is fetched.
    - Cached by PR + updatedAt: in memory (`id@updatedAt`, 64 entries) and in kv_cache under `pr-detail:owner/repo#n` with the list's updatedAt it was fetched for, so a restart shows details without a call. A new updatedAt refetches while the old details show, marked "updating…".
    - Glamour renders the body and comment bodies in a command, per pane width; plain wrapped text shows until it lands.
    - Errors: one toast for the selected PR (stale results are dropped silently); the pane shows the error; revisiting retries.
  - **Overview**: status card (review: approved with N approving reviews / changes requested by … / review required; checks summary; merge: conflicts / clean / behind / blocked / unstable, merged, closed), labels in their own colours (`Theme.LabelChip`), reviewers with state icons, linked forge sessions (P1.9 `LinkedSessions(github_pr, id)`, loaded on each rest, newest first, with status and age), description.
  - **Checks**: icon, name · workflow, duration (`2m14s`, `running 1m03s`, `queued`, `skipped`). **Activity**: author, review state or "commented", age, rendered body.
  - **Keys** (scope `tab:prs`, configurable): `pr_detail_next` ], `pr_detail_prev` [, `pr_detail_down` J, `pr_detail_up` K (half a pane). A new PR starts scrolled to the top.
  - **Palette**: next / previous detail tab, show overview / checks / activity, scroll detail down / up.
  - **Theme**: `DetailBand`, `DetailTab(Active)`, `DetailRule`, `StatusCard`, `CardTitle`, `StatePill`, `LabelChip`, `CheckIcon`, `ReviewStateIcon`.
  - **Tests**:
    - github: `ParsePRDetail` fixture (check runs + status context, ordering, durations, ghost, team and re-requested reviewers, drafts and empty reviews skipped), edge cases, `checkRunState`, `PRDetail` over a fake transport.
    - tab: done-when `TestDetailNoLag` (pane follows `j` with no fetch; four moves → one fetch, for the resting PR; going back is served from memory), updatedAt invalidation, kv_cache across a restart, errors and stale results, tabs and palette commands, scrolling, linked sessions, eviction, durations.
  - **Limits**: 30 comments / reviews, 100 checks, 20 labels / reviewers; inline review threads aren't shown. After a resize the Markdown re-renders on the next message (plain text meanwhile). The list keeps its 65% start (`<` / `>` resize).
- [x] P2.3 PR quick actions — actions on the selected PR, each a key (scope `tab:prs`, configurable) and a palette command (`PR: …`). Code in `internal/tabs/prs/actions.go`; client in `internal/github/actions.go`.
  - **Keys**: `pr_open` o (browser), `pr_copy_url` y, `pr_checkout` c, `pr_update_branch` u, `pr_merge` m, `pr_auto_merge` M, `pr_toggle_draft` d, `pr_session` s.
  - **Client** (`github.PRActor`, now part of `PRClient`): `PR` (one row, same fields as the search via the shared `prFields`), `UpdateBranch` and `Merge` (REST `PUT …/update-branch`, `…/merge` with `merge_method`), `EnableAutoMerge` and `SetDraft` (GraphQL mutations; the node id is looked up first). `PRDetail.MergeMethods` comes from the repo's allowed merge methods; `PR.CrossRepo` from `isCrossRepository`.
  - **Readable errors**: `github.ActionError(verb, n, err)` / `Reason` → "merge #12 failed: Pull Request is not mergeable": go-gh's `HTTP 405 … (url)` noise is dropped, GraphQL messages joined, 401 → `gh auth login`, 403 → "no permission: …", timeouts named.
  - **Merge**: huh dialog: method (only the repo's allowed ones when the detail is loaded; the last used first; skipped when there is one) and a confirm defaulting to Cancel, with a note when checks fail / run, there are conflicts or changes were requested. Drafts ask to be marked ready first. **Auto-merge**: method pick only.
  - **Checkout** (`internal/checkout`, over `worktree.Exec` + `worktree.Worktrees`): already checked out somewhere → says where; project checkout clean → fetch and `git checkout` (a tracking branch when only on origin); dirty → a worktree `pr-<n>` under the sessions' worktree root (so `s` later offers to reuse it). Fork PRs fetch `refs/pull/<n>/head` into `pr-<n>` (fast-forward only). The project is the configured one with a path for the PR's repo.
  - **Copy**: `internal/clipboard` (pbcopy / wl-copy / xclip / xsel); without a tool, OSC 52 (`tea.SetClipboard`).
  - **Session**: `tabs.StartSessionFrom` with the PR's link, head branch and base (the P1.9 form).
  - **Running / refresh**: u / m / M / d / c run in a command (2 min timeout); one action per PR at a time. `tabs.Busier` → the status bar shows the hammer and "merging #12…" (`StatusInfo.Activity`). Done → success or error toast. After a GitHub action the PR is re-fetched (`PR`) and its row patched in every section; the patch outranks older search results (search lags) until a search returns it at least as new. A new updatedAt refetches the detail pane by itself. Merged / closed PRs refuse u / m / M / d with a toast.
  - **Wiring**: `tabkit.Deps.Checkout` / `.Clipboard`, `app.WithCheckout` / `WithClipboard`, built in `cmd/forge`.
  - **Tests**: github actions over a fake transport (paths, bodies, mutations + node id, `Reason` cases), checkout on temp repos (clean, existing local, dirty → worktree, already checked out, fork clean / dirty, errors), clipboard tool choice, status bar activity, tab tests for every key and palette command (row patch vs stale / newer search, busy + double action, guards, merge dialog with allowed methods / decline / confirm / esc, auto-merge, checkout messages, session source), and a teatest merge flow in `internal/app`.
  - **Not verified live**: the queries (`PR`, merge methods) were checked against the live API; the mutations and the merge / update calls were only tested against fakes. Try them on a throwaway PR.
  - **Limits**: auto-merge can only be turned on, not off; `s` on a fork PR starts from its base (the fork branch isn't fetched); the kv_cache keeps the pre-action copy until the next search.
- [x] P2.4 Review queue & diff — `v` on a PR (palette: "PR: review diff") opens a full-screen diff viewer; `a` approves, `x` requests changes, `C` comments. Code in `internal/tabs/prs/diffview.go`; client in `internal/github/review.go`; `internal/delta` wraps delta.
  - **Client** (`github.PRReviewer`, now part of `PRClient`):
    - `Diff` is REST `GET …/pulls/{n}` with `Accept: application/vnd.github.v3.diff`, from a third lazy go-gh client on the same observing transport, so the rate limit is still reported. It is capped at 20 MiB. GitHub's 406 for huge diffs → `ErrDiffTooLarge`, toasted as "open it in the browser (o)".
    - `SubmitReview` is `POST …/pulls/{n}/reviews` with `{event, body}`; approve may have no body. `restPut` became `restDo(method, …)`.
    - `ParseDiff` splits a git diff into `DiffFile`s: paths, status (modified / added / deleted / renamed / binary), +/- counts and text.
    - The diff fetch was checked against the live API: 27 files on #21, matching GitHub. The review POST was only tested against fakes.
  - **Viewer**: it takes over the tab below the section bar and captures input, like the transcript.
    - File list on the left (30%, at least 24 cols, hidden under 60 cols): status letter, path shortened from the left, `+n -m`, with the file in view highlighted.
    - All files sit in one viewport, a blank line apart, with each file's first line recorded. `n` / `p` jump between files; scrolling updates the current file.
    - Header: `#n title · base ← head · N files +a -d · file i/N`.
    - Diffs are fetched in a command and cached in memory per PR + updatedAt (8 entries). Not in kv_cache, because they're big.
  - **Colours**: rendered in a command for the diff pane's width, and again after a resize; raw text shows until the render lands.
    - With delta on the PATH: each file goes through `delta --paging=never --width=<pane> --dark|--light`, 4 at a time. Per file, so file offsets stay exact. Passing `--dark` / `--light` also stops delta querying the terminal, which would race Bubble Tea for input. The user's git-config delta settings still apply.
    - A file delta fails on gets Lip Gloss colours, with one toast per tab.
    - Without delta: Lip Gloss (`DiffAdd` / `DiffDel` plus the new theme slots `DiffHunk`, `DiffMeta`, `DiffFileHeader`, `DiffFile(Active)`). A file band replaces the `diff --git` / index / ---/+++ lines; tabs become 4 spaces and lines are cut to the width.
  - **Reviews**: a huh dialog with a body (8 lines; ctrl+e opens `$VISUAL` / `$EDITOR` through huh's external editor) and a confirm that defaults to submit.
    - The body is required for request changes and comment.
    - It runs through `runAction`: busy hammer, spark toast ("approved #12"), and the row re-fetched and patched (review decision), which also refreshes the detail pane. The viewer stays open.
    - Guards: merged / closed PRs, and approve / request changes on your own PR (author = the searches' viewer). Comments are allowed.
  - **Keys**: `pr_diff` v (scope `tab:prs`). Modal scope `diff` (only these keys are live while the viewer is open, like the transcript): `diff_up/down` k/j, `diff_page_up/down` ctrl+u/ctrl+d (also space), `diff_top/bottom` g/G, `diff_next_file` n, `diff_prev_file` p, `diff_approve` a, `diff_request_changes` x, `diff_comment` C, `diff_open` o, `diff_close` esc/q.
  - **Palette**: "PR: review diff", plus "PR: approve… / request changes… / comment…" (`diff_*` ids). These also work on the selected PR from the list. While the viewer is open: "Diff: next file / previous file / close".
  - **Wiring**: `tabkit.Deps.Delta`, `app.WithDelta`, `delta.Exec{Dark: true}` in `cmd/forge`.
  - **Tests**:
    - github: `ParseDiff` fixture (modified, added, deleted, rename, binary, mode-only, paths with spaces, a `+-- ` line in a hunk), `Diff` (Accept header; JSON clients keep theirs; 406 / 404), `SubmitReview` bodies and validation, own-PR error text.
    - delta: argv, stdin through a stand-in script, stderr in errors, missing binary.
    - tab: open / n / p / scroll / close / cache, modal keys, delta and resize re-render, delta failure fallback, errors, stale results, approve, request changes needing a body, esc, guards, palette, colouriser, `shortenLeft`.
    - app: teatest done-when `TestPRReviewFlow` (`2` → `v` → `n` → `a` → enter → enter → "approved #101").
  - **Limits**: no inline (line) comments, only whole-PR reviews. No pending-review drafts. Re-opening after a review refetches the diff (updatedAt changed). delta isn't installed on the dev machine, so the delta path is only tested with fakes and a stand-in script; `brew install git-delta` to try it.
- [ ] P2.5 Stale PR markers
- [ ] P2.6 Pipelines list
- [ ] P2.7 Log tail & rerun
- [ ] P2.8 "Fix this" from CI
  - Note (from P1.9): find a linked session with `Service.LinkedSessions(github_pr, "owner/repo#n")`; otherwise `StartSessionFrom` with `Branch` = the PR's head and `Vars{"failing_log": …}`.

## Phase 3 — Plane

- [ ] P3.1 Plane client
  - TODO (from P1.7): register a `templates.ContextProvider` for `plane_issue` (fills `{issue_title}`, `{issue_body}`) through `app.WithTemplates` / `deps.providers`.
  - TODO (from P1.9): on refresh, `Links.SetState(store.IssueRef(key), state)` so session detail panes show current issue states.
- [ ] P3.2 Issue list view
- [ ] P3.3 Kanban board
- [ ] P3.4 Create & edit
- [ ] P3.5 Quick capture
- [ ] P3.6 Cycle summary
- [ ] P3.7 Branch naming
  - Note (from P1.9): pass `BranchFor(item)` as `tabs.Source.Branch`; the worktree side (create / reuse / ownership) is done.

## Phase 4 — Docs & design artifacts

- [ ] P4.1 Docs browser
- [ ] P4.2 Docs search
- [ ] P4.3 Send docs to agent
- [ ] P4.4 Decision log
- [ ] P4.5 Artifact gallery
- [ ] P4.6 Pin & tag artifacts
- [ ] P4.7 Inline previews

## Phase 5 — Context & cache

- [ ] P5.1 CLAUDE.md manager
- [ ] P5.2 Context packs
- [ ] P5.3 Cache controls
- [ ] P5.4 Usage view

## Phase 6 — Workflow glue

- [ ] P6.1 Home screen
  - Note (from P2.4): the "Review requested" badge = the PR count of the section whose query has `review-requested:@me`. The PRs tab caches it in kv_cache under `prs.sectionKey(project, section)` (a `github.SearchResult`), so Home can read the count without a call.
- [ ] P6.2 Scratchpad & daily note
  - Note (from P1.10): finished sessions = `archived_at` in the day with their `summary` (first line: the PR and its state).
- [ ] P6.3 Custom commands
- [ ] P6.4 Polish & packaging
  - [ ] TODO: `forge doctor` reports the tmux version and which config file the forge server uses (`#{config_files}`), and warns when the user's tmux.conf version (`tmux.ParseConfigVersion`) is older than the embedded one (`tmux.ConfigVersion`).
