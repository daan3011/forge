# forge — Progress

Feature IDs match docs/SPEC.md. Tick a box when its feature is done.

## Phase 0 — Foundations

- [x] P0.1 Project scaffold
- [x] P0.2 Config
  - **Zero-config projects** (later change): projects no longer need config. `internal/projects` scans `project_roots` one level deep for git checkouts with a GitHub remote. Without `project_roots`, it scans the parent of the repo forge starts in plus `~/Documents/Projects`, `~/code`, `~/src` and `~/dev`. The remote is read from `.git/config` without running git; linked worktrees are followed. Repos forge was started in, ran a session in or switched to are remembered in the new `projects` table (migration 0006, `store.Projects`), so the list grows. `projects.Merge` combines the detected checkouts, remembered repos and config entries, which are now overrides matched by repo; only `repo` is required. Names default to the repo name, or `owner/name` when two repos share one. A remembered repo whose checkout is gone keeps its repo without a path. `cmd/forge` runs it for the TUI, `forge new` and `forge plane ls`. `TestMain` keeps the CLI tests away from the real home folder and working directory.
- [x] P0.3 Store
- [x] P0.4 App shell & theme
- [x] P0.5 Keymap & help
- [x] P0.6 Command palette
- [x] P0.7 Project switcher
  - **Zero-config projects** (later change): the switcher lists the detected projects, most recently used first, each with `repo · ~/path` or `repo · no checkout`. Switching records the project as used (`app.WithProjectStore`), as does starting a session (`sessions.Config.Recent`). Starting forge inside a repo opens that project; `forge new` without `--project` uses it too, as does `forge plane ls` when it's linked. The Plane tab links projects to Plane: a repo is matched to the Plane project whose identifier or name equals the repo name, ignoring case, dashes, underscores and dots (`projects.PlaneMatch`; exactly one match counts). Otherwise the active project gets a one-time picker, "Which Plane project goes with owner/name?", that includes "None". Links are saved in the database and sent to the root model as `tabs.PlaneLinkedMsg`; the palette command "Plane: link project…" changes a link. Checkout and session errors now say to clone the repo under a project root instead of editing the config.
- [x] P0.8 Background refresh — tabs load cache-first via `tabkit.LoadCached`/`FetchAndCache`; `internal/refresh` ticks per source. GitHub rate-limit backoff (`github.RateLimitMsg`) is wired but only takes effect once the P2.1 client emits it via `github.Observe`.
  - **Minimum interval** (added with P3.3): `refresh.New` clamps any source interval below 5s (`refresh.MinInterval` = `config.MinRefreshInterval`) to 5s, and `Interval` clamps backoff overrides too. Config validation already rejects shorter intervals, but a config built in code skipped it, and a zero interval refetched in a tight loop (~180 list calls a second in the app tests). Clamped intervals become `Scheduler.Warnings` / `app.Model.Warnings`: `forge` prints them as `warning: …` on stderr before the TUI starts, and the app shows them as toasts. `TestZeroRefreshIntervalDoesNotSpin` covers it, and the app's Plane tests now set intervals explicitly (`planeConfig()`).
- [x] P0.9 Forge animations — `internal/ui/anim`: one shared `Engine` clock (12 fps, ticks only while something visible animates; never with `animations: off` or `FORGE_NO_ANIM`), Splash, HammerFrame (+ `anim.Hammer` bubbles spinner), HeatBar, Burst, Flame, EmptyForge, harmonica `Spring` (split divider, palette slide-in). `ui.SuccessMsg` gives a success toast with a spark burst; nothing emits it yet. `Flame` is ready for P1.4 statuses. Hidden `forge demo anim [--level]` shows everything.

## Phase 1 — Agent sessions

- [x] P1.1 tmux driver — `internal/sessions/tmux`: `Driver` interface + `Exec` (every call passes `-L forge -f <conf>`; see the isolation follow-up below). Sanitised names are the stable ids, exact-match targets (`=sess:=win`), argv-only (no shell). Each session gets a hidden `_forge` placeholder window; server sets `remain-on-exit` so exited agents read as dead and keep their output. `Attach` switches client when on the forge socket, else returns an `*exec.Cmd` (TMUX stripped, not killed by ctx) for `tea.ExecProcess`. `tmuxtest.Fake` + shared `RunConformance`; real-server tests via `go test -tags tmux ./internal/sessions/tmux/...`. Hidden `forge demo tmux` exercises attach/detach.
  - [x] P1.1 follow-up: isolated tmux server. forge's server uses only its own config: an embedded, fully commented default (`internal/sessions/tmux/forge.tmux.conf.tmpl`, header `# forge-tmux-config v1`, colours from `ui.TmuxColors`), written by `tmux.EnsureConfig` to `$XDG_CONFIG_HOME/forge/tmux.conf` on first run and never overwritten. It has no prefix, prefix-less `M-d` / `M-[` / `M-w`, and an ember status line. `Exec.base()` always emits `-L forge -f <conf>` (`/dev/null` without `WithConfig`), so `~/.tmux.conf` never loads. `Driver.Attach` returns an `Attachment`:
    - `AttachSwitched` inside forge's server
    - `AttachExec` in a plain terminal, or inside an outer tmux older than 3.2 (TMUX stripped)
    - `AttachPopup` inside your own tmux: `display-popup -E -w 90% -h 90%` running `env -u TMUX tmux -L forge -f <conf> attach …`, shell-quoted via the new `internal/shell`. It blocks until the popup closes (checked on tmux 3.8), and warns if the outer tmux binds `M-d` in its root table.
    
    `Driver` gains `ListAll` and `KillServer`. New `forge tmux restart [--yes]` and `forge tmux config [--reset]` (reset keeps `tmux.conf.bak.<timestamp>`), also Sessions-tab palette commands ("tmux: …"). `forge demo tmux` handles all three attach modes and refreshes after detach / popup close. The Sessions tab's own Enter-to-attach comes with P1.5. Real-server test checks that `~/.tmux.conf` is ignored, `prefix` is `None`, and `#{config_files}` is forge's file.
- [x] P1.2 Worktree manager — `internal/sessions/worktree`: `Manager` (`Create` / `List` / `Remove` / `DeleteBranch`) over a fakeable `Git` interface (`Exec` = git binary, argv only, never prompts). Worktrees live at `<worktree_root>/<repo>/<session>` on `agent/<session>`, created `--no-track` from `origin/<base>` after a fetch (offline falls back to the local base, reported as `FetchErr`). Existing branch → `ErrBranchExists` (retry with `ReuseBranch`); per-project `worktree_copy` globs copied in (never overwriting tracked files, rolled back on failure). `Reconcile` maps worktrees to sessions → matched / orphans / missing. `Remove` refuses dirty worktrees (`*DirtyError`) unless forced and reports `BranchMerged` (ancestry only; squash merges not detected). Tests run against real temp repos with an isolated git config.
- [x] P1.3 Start named session — `n` (or `forge new`, flags or the same Huh form) asks name / project / base / template / first prompt. `internal/sessions.Service` creates the worktree, writes the prompt to `<data>/forge/sessions/<name>/prompt.md` and opens a tmux window in the worktree running `sh -c 'exec claude --session-id <uuid> -- "$(cat <file>)"'` (`internal/sessions/claude`; paths shell-quoted, the prompt never in a command line) with `FORGE_SESSION=<name>`, then saves the row; any failure rolls back what was done. Sessions tab: name / status / activity / project / branch / link, detail pane with a capture-pane preview every 2s while visible (only for live sessions; `tabs.VisibilityAware`). List refresh marks sessions with a gone/exited window `dead` until P1.4 hooks take over. `x`: kill → `dead` (row kept for P1.5 resume), or kill + remove worktree → `archived` (migration 0002; branch, uuid and links kept; dirty worktrees ask before forcing). `a` toggles the archived view, `D` deletes an archived row. `tmux.Driver.PasteText` (bracketed paste via a named buffer) is used by P2.8 (`Service.SendPrompt`) and in place for P4.3. Templates field appears once P1.7 provides templates. Real-tmux end-to-end test: `go test -tags tmux ./internal/sessions/`.
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
    
    What's left is asked for in the form, one field per variable (multi-line for `*_body` / `*_log`). The registry starts empty: P2.1 and P3.1 register providers. Until then, the issue/PR title and body are typed in, and that manual fallback stays for sessions with no ticket.
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
- [x] P2.5 Stale PR markers
  - **Config**: `github.stale_after_days` (default 5, must be ≥ 0; 0 turns markers and the filter off).
  - **Markers**: a PR whose `updatedAt` is at least the threshold old is stale: its ◷ (updated) age turns amber (`PRStale`, ember mixed with molten) and a dim ◷ (`PRStaleIcon`) sits on the row's second line. Twice the threshold turns the age red (`PRVeryStale`, error colour). The created column is unchanged.
    - A column's `Render` only sees its own cell, so the level rides in the updated cell after a `\x1f` separator (`updatedCell` / `splitUpdatedCell` in `rows.go`). It can't be typed, so `/` is unaffected.
  - **Filter**: `pr_stale_only` S (scope `tab:prs`) toggles "stale only" for every section, on top of the repo filter: counts, rows and the status detail follow, the search box shows "◷ stale only", and an emptied section says "no stale PRs here · S shows all". With the markers off, S toasts instead. `Select` (palette jump) drops the filter when the target PR isn't stale; changing project resets it.
  - **Palette**: "PRs: show stale only" / "PRs: show all (not just stale)" (`pr_stale_only`).
  - **Tests**: `staleLevel` boundaries, cell encoding, `renderUpdated`; tab tests for S, counts, empty hint, palette command, `Select`, markers off; config default and validation; app teatest `TestPRStaleFilter` (`2` → `S` → only #98). The app's fake PRs now use clock-relative `updatedAt` so they stay fresh / stale over time.
  - **Limits**: staleness is GitHub's `updatedAt`, which bot comments, CI status pushes and label changes also bump, so "stale" means "nothing touched it", not "no human activity". Ages are computed when rows are laid out (each fetch / filter change), not ticked live.
- [x] P2.6 Pipelines list — the Pipelines tab lists GitHub Actions runs for the active project's repo, or every configured repo under "all projects". Code in `internal/tabs/pipelines` (`pipelines.go` list, filters and refresh; `rows.go` columns; `detail.go` jobs pane); client in `internal/github/runs.go`.
  - **Client** (`github.RunsClient`, `tabkit.Deps.Runs`, `app.WithRuns`, the same `githubPulls` client in `cmd/forge`):
    - `ListRuns(repo, RunFilter{Branch, Status, Actor})` is REST `GET …/actions/runs?per_page=30`, with branch / status / actor filtered by GitHub.
    - `RunJobs(repo, id)` is `GET …/runs/{id}/jobs?per_page=100` (latest attempt), with steps.
    - `Viewer()` is `GET user`, fetched once and remembered, for "mine".
    - REST status / conclusion map to the `Check*` states through the existing `checkRunState`, so runs, jobs and steps share the checks' icons. `Run.Duration` runs from `run_started_at` to `updated_at`, or to now while running. Queued is zero.
    - Parsed against live responses from this repo: states, titles, durations and steps all match.
  - **List**: a bordered filter box (`⌕ acme/app · branch: x · workflow: ci · status: failed · actor: @me`). Columns: status icon · (repo, only with several repos) · workflow · branch · commit (display title, first line) · event · actor · duration · started.
    - Duration and started are stored as timestamps and drawn against now, so a running run's time keeps moving between fetches.
    - Runs from every repo are merged newest first. Each repo is fetched and cached separately in kv_cache, keyed by project, repo and the GitHub-side filters. Loading is cache-first.
    - One error toast per refresh. The empty state explains itself: no repo, no client, `gh auth login`, the fetch error, or "no runs match · x clears filters".
  - **Filters**:
    - `f` opens a huh form: branch (free text with suggestions from loaded runs), workflow (filtered client-side), status (in progress / queued / failed / succeeded / cancelled / completed), and mine.
    - `m` toggles mine: actor = the gh login. Without a login it toasts and turns itself off.
    - `x` clears every filter and a PR's repo override. `/` text-filters the rows.
    - Changing project resets the filters.
  - **Detail pane**:
    - Header: repo · workflow #n (attempt), the title on a band, a state pill (`Theme.CheckPill`), branch @ sha7, event, actor, age and duration.
    - Then each job (icon, name, duration) with its numbered steps indented below. The pane scrolls with J / K.
    - Jobs are fetched once the cursor rests for 150ms, refetched whenever the run's updatedAt moves ("updating…" shows meanwhile), and kept in memory (32 runs). A failed fetch toasts for the selected run.
  - **Live progress**:
    - While the tab is visible and a run on screen is queued or in progress, it refreshes every 15s (`activeInterval`). The selected run's jobs are refetched too.
    - The timer is the tab's own (`tabs.VisibilityAware`), with a generation counter so timers never stack. It stops when the tab is hidden or every run has finished.
    - It is skipped when `github.refresh_interval` is already 15s or less. Normal GitHub ticks still apply.
    - Running durations (rows, jobs, steps) advance every second through the app's clock re-render.
  - **From a PR**: `p` (`pr_runs`, palette "PR: show pipeline runs") sends `tabs.ShowRunsMsg{Repo, Branch}`. The app switches to the `tabs.RunsShower` tab, which shows that branch's runs with no other filters. A repo outside the active project is listed on its own (`repo: x`) until the filters are cleared.
  - **Keys** (scope `tab:pipelines`): `pipeline_filters` f, `pipeline_mine` m, `pipeline_clear_filters` x, `pipeline_detail_down` J, `pipeline_detail_up` K, `pipeline_open` o (run in browser). Also `pr_runs` p (scope `tab:prs`).
  - **Palette**: Filter runs, "Pipelines: filters… / show only mine (show everyone's) / clear filters / scroll detail down / up", "Run: open in browser", widen / narrow list. Runs are palette entities ("ci #42 title", repo · branch · state).
  - **Shared**: `ui.ShortDuration` / `ui.StateDuration` (moved from the PRs detail pane, which now uses them), `Theme.CheckPill`.
  - **Tests**:
    - github: `ParseRuns` / `ParseJobs` fixtures (running, queued, failure, cancelled, skipped steps, null actor, multi-line commit), durations, query params, errors, `Viewer` remembered.
    - tab: rows and order, merged repos and the repo column, detail jobs / steps and the memory cache, durations ticking without fetches, fast refresh (armed once, refetches jobs, stops when done or hidden, off with a short base interval), mine (with and without a login), the filter form → GitHub filter + cache key, client-side workflow, `ShowRuns` with and without an override, cache-first + one toast, empty states, a stale fetch after a project change, jobs errors, status detail, entities / select, palette commands, text filter.
    - prs: `p` and its palette command send `ShowRunsMsg`.
    - app: done-when teatest `TestPipelineLiveProgress` (`2` → `p` → the running run with steps → it finishes → refresh → "succeeded" and the finished job).
  - **Limits**: 30 runs per repo and the latest attempt's jobs only. The 15s loop doesn't follow the app's rate-limit backoff: one list call per repo per tick, only while visible and something runs. Workflow names come from loaded runs. For P2.7, `Job.ID` / `Job.URL` are there for the log tail.
- [x] P2.7 Log tail & rerun — `l` on a run opens a full-screen log viewer on its failed jobs, collapsed to the error context. `t` / `ctrl+r` / `X` rerun the failed jobs, rerun the whole run, or cancel it. Code: `internal/cilog` (pure parsing), `internal/tabs/pipelines/logview.go` (viewer) and `actions.go` (reruns / cancel); client in `internal/github/runs.go`.
  - **Client** (`github.RunsClient` grew four methods):
    - `JobLogs(repo, jobID)` is `GET …/actions/jobs/{id}/logs`. GitHub 302s to a signed URL; the HTTP client follows it, and go-gh only sends the token to github.com. The download is capped at 32 MB (`ErrLogTooLarge`). A 404 / 410 becomes `ErrLogsUnavailable` ("expired, or the job hasn't finished").
    - `RerunFailedJobs` / `RerunRun` / `CancelRun` are `POST …/runs/{id}/rerun-failed-jobs | rerun | cancel`.
    - Checked live against a failed run of this repo: the log downloads and the `--- FAIL` / `##[error]` context is what the viewer opens on.
  - **Parsing** (`internal/cilog`, no I/O, table-tested against `testdata/job.log`):
    - `Clean` strips the BOM, GitHub timestamps, ANSI and `\r` (keeping a progress bar's last frame) and expands tabs. It turns `##[group]X` into `▸ X` and drops `##[endgroup]`.
    - `IsErrorLine`: `##[error]`, `FAIL`, `failed`, `exit code`, or `error` / `panic` as whole words, so "prone to errors" and `-Werror` don't match.
    - `Extract` keeps ±5 lines around each match and merges touching windows. `Collapse` puts `⋯ N lines hidden ⋯` between hunks; with no matches it shows the last 40 lines.
  - **Viewer** (modal, scope `log`; takes over the tab like the PR diff viewer):
    - The jobs are the run's failed ones. On a green run they are its finished jobs, with a note. A run with no finished jobs toasts. Jobs that aren't loaded yet are fetched first.
    - Title `repo · workflow #n › job (k/N failed)`. The rule shows the mode (`errors · N hunks` / `full log · N lines` / `no error lines found · showing the tail`), the search state and the position. Line numbers are the full log's.
    - `e` toggles error context and the full log, keeping the top line. `/` searches as you type (case-insensitive) and `n` / `N` cycle matches. A query found only in hidden lines switches to the full log on enter. `]` / `[` switch failed jobs, `o` opens the job, esc clears the search and then closes.
    - Logs are downloaded and parsed off the update loop. The last 8 are kept in memory, so switching jobs back is instant. Only visible lines are rendered, so big logs scroll cheaply.
  - **Error summary for P2.8**: each loaded *failed* job's collapsed context is saved in kv_cache as a `cilog.Summary` (repo, run id / number / attempt, workflow, branch, sha, job id / name / URL, lines, tail flag). Kind is `ci_errors` and the key is `cilog.SummaryKey(repo, runID, jobID)` = `ci_errors:<repo>#<run>:<job>`. Lines are capped at 200 / 16 KB, with no TTL. Job ids change on a rerun, so attempts don't overwrite each other.
  - **Reruns / cancel**: these act on the viewer's run, else the selected one.
    - `t` (no confirm) only works when the run finished as failure / cancelled / timed_out / startup_failure; otherwise it toasts.
    - `ctrl+r` (completed runs) and `X` (active runs) ask with a huh confirm first.
    - A second press while the call is in flight toasts. On success a toast shows and the runs refresh 3s later; the 15s fast refresh takes over once the new attempt shows as running.
  - **Keys**: `pipeline_logs` l, `pipeline_rerun_failed` t, `pipeline_rerun` ctrl+r, `pipeline_cancel` X (scope `tab:pipelines`). The viewer (scope `log`) has `log_up`/`log_down`/`log_page_*`/`log_top`/`log_bottom`, `log_toggle_full` e, `log_search` /, `log_next_match` n, `log_prev_match` N, `log_next_job` ], `log_prev_job` [, `log_rerun_failed` t/R, `log_rerun` ctrl+r, `log_cancel` X, `log_open` o and `log_close` esc/q. `R` stays global refresh-all; the viewer is modal, so `R` is free there. All of them are palette commands (the viewer's while it is open) and are documented in `default.yaml`.
- [x] P2.8 "Fix this" from CI — `F` on a failed run (Pipelines), on the job shown in the log viewer, or on a PR with failing checks (PRs) hands the failure to an agent. `alt+f` does the same but opens the prompt for editing first. Code: `internal/cifix` (gathering the failure, template variables, fix branch names), `internal/tabs/pipelines/fix.go`, `internal/tabs/prs/fix.go` and `internal/tabs/sessions/fix.go`.
  - **What's handed over**:
    - Pipelines list: the run's failed jobs (at most 3).
    - Log viewer: the job on screen.
    - PRs: the head commit's failing checks (at most 3). Actions jobs get their logs. Other checks (commit statuses, third-party apps) are listed by name and URL only.
    - For each job, the P2.7 summary is read from kv_cache. On a miss, the log is downloaded, collapsed and saved, so a later `F` or `l` doesn't download it again. An unavailable log is noted in the prompt and never fails the whole.
    - A green run, or a PR without failing checks, gives an info toast.
    - While the failure is gathered, the tab is busy (hammer in the status bar), and a second `F` toasts.
  - **Template**: fix-ci gained `{ci_workflow}`, `{ci_job}`, `{ci_run}` and `{commit_message}` next to `{failing_log}`. `failing_log` holds one `── workflow › job ──` block per job, capped at 16 KB. A templates folder created before P2.8 keeps its old fix-ci.md (`EnsureDefaults` never overwrites); add the new variables by hand, or delete the folder to get the new defaults.
  - **Where it goes** (Sessions tab, `tabs.Source.Fix` = `tabs.FixRequest{Reuse, Immediate, StartCommit, Confirm}`):
    1. **Live linked session**: the newest session linked to the PR that isn't dead or archived gets the rendered prompt through `Service.SendPrompt`. That is `tmux.PasteText` (bracketed paste, then Enter) after a `WindowAlive` check. The row is selected and a toast says "sent the failure to <name>". With `alt+f`, the prompt is shown for editing first ("Send to <name>"). If the send fails, the flow goes on to step 2.
       A session that is `working` (mid-turn) isn't pasted into straight away. A dialog asks "<name> is busy" with three answers:
       - "Send now (queued)": Claude Code takes it after the current turn. With `alt+f`, the prompt is edited first.
       - "Start a new fix session (own branch off <head>)": git checks a branch out only once, so the new session branches from the PR head (`Base` = head) and is still linked to the PR.
       - Cancel.
       Waiting, idle, starting and error sessions get the prompt as before.
    2. **New session** on the PR's head branch, from fix-ci, linked to the PR. `F` starts it right away, with a spark toast "fix session pr-12 started". `alt+f` opens the pre-filled new-session form. A missing variable or a deleted template also falls back to the form, and a branch checked out elsewhere gets the existing P1.9 dialog.
    3. **No open PR** for the run's branch (a push to main, a scheduled run, or a failed PR lookup): a confirm, default yes: "No open PR for <branch>. Start a fix session on a new branch fix/<workflow-slug>-<run-id> from <sha7>?". The branch starts at the failing commit: `StartOpts.StartCommit` → `worktree.CreateOpts.StartCommit`, fetched from origin by sha when missing locally. The session is linked to the run (new link kind `github_run`, migration 0005 rebuilds `links` for the CHECK), and P1.9's PR sync attaches a PR opened from that branch later. The detail pane shows a `run:` line, and `o` falls back to the run.
  - **GitHub data**:
    - `Run.CommitMessage` holds the full head commit message.
    - The PR detail query also fetches the head commit's `oid` / `message` and, for check runs, `databaseId` (the job id) and `workflowRun.databaseId` (the run id), as `Check.RunID` / `JobID`.
    - Checks cached before this have no ids. `Check.CheckIDs` then falls back to parsing `/actions/runs/<run>/job/<job>` from the URL.
    - The new fields were checked against the live API.
  - **Wiring**: `tabkit.Deps.Pulls` / `app.WithPulls` (the same `githubPulls` client) lets Pipelines find the PR of a run's branch; the PRs tab uses `Deps.Runs` for logs.
  - **Keys**: `pipeline_fix` F / `pipeline_fix_edit` alt+f (`tab:pipelines`), `log_fix` / `log_fix_edit` (`log`), `pr_fix` / `pr_fix_edit` (`tab:prs`). Documented in `default.yaml`.
  - **Palette**: "CI: fix this" and "CI: fix this (edit prompt)…" (`pipeline_fix*`, and they act on the viewer's job while it is open), "CI: fix this (failing checks)" (`pr_fix*`).
  - **Tests**:
    - `cifix` tables: summarise with cache hit / miss + save / unavailable / not Actions, vars, cap, fix branch.
    - github fixture with ids and the head commit; `ParseCheckIDs`.
    - Migration 0005: rows survive, `github_run` accepted.
    - Worktree from a local commit, a remote-only commit, an unknown one, a non-sha, and a reused fix branch.
    - `SendPrompt` against fake tmux, plus a real-tmux case in `-tags tmux`.
    - Pipelines / PRs: sources, saved summaries, the log viewer's job, no PR, guards, busy, palette.
    - Sessions: immediate start, reuse of the live session, dead → new, send failure → new, alt+f form, edit-then-send, missing var, unknown template, confirm / cancel, `run:` line.
    - App done-when `TestFixThisFromRedCheck`: real service, git worktrees and store, fake tmux. `3` → `F` → the session on `retry` with the failure in prompt.md, linked to `daan/forge#12` → `3` → `F` → pasted into the same session, using the saved summary.
  - **Not verified live**: the full key flow was only run under teatest, not by hand against a real red run. Try `F` on a failed run of this repo.
  - **Limits**:
    - Fork PRs: the head branch isn't fetched, the same as `s`.
    - A PR check's run attempt / number aren't known, so its summaries are keyed by run and job id only (as P2.7's are).
    - Only the latest attempt's jobs are used for a run.

## Phase 3 — Plane

- [x] P3.1 Plane client — typed client for Plane's **v2** REST API in `internal/plane`, plus the debug command `forge plane ls`.
  - **Client**: `plane.New(cfg.Plane, key, opts...)`. The base URL comes from config, so self-hosted instances with v2 work too. The key comes from `ResolveAPIKey` (`api_key`, else `$PLANE_API_KEY`) and is sent as `X-API-Key`. Every path ends in `/` as v2 requires.
    - Methods: `Me`, `Projects` / `Project`, `States`, `Labels`, `Cycles`, `CurrentCycle`, `ProjectMembers` / `WorkspaceMembers`, `WorkItems` / `WorkItem` / `WorkItemByIdentifier("FORGE-12")`, `CreateWorkItem`, `UpdateWorkItem`, `Comments` and `CreateComment`.
    - Narrow interfaces (`ProjectReader`, `WorkItemLister`, `WorkItemWriter`, `Commenter`) plus the composite `plane.API` give later tabs something to fake.
    - `WorkItemFilter` covers state / state group / priority / assignee / label (`__in` when there are several values), cycle, search, `updated_at__gte`, archived, `order_by`, `expand` and `fields`. `WorkItemUpdate` uses pointer fields, so PATCH only sends what changed.
    - Lists page with `paginate=cursor&per_page=200` until `has_more` is false, stop at `Limit`, and give up after 50 pages. `order_by` priority / state_group / sort_order switch to offset paging, which v2 requires.
    - Errors are `*plane.APIError`, decoded from problem+json (`code`, `detail`, field errors). Helpers: `IsNotFound`, `IsAuthError`, `IsRateLimited`. If `Me` gets a 404, it returns `ErrNoV2`.
    - There is no "current cycle" filter in v2, so `Cycle.IsCurrent` and `CurrentCycle` go by dates. A date-only end date counts through that whole day.
    - `HTMLToText` turns description and comment HTML into plain text.
  - **Rate limits**: a 429 is retried up to 3 times. The wait is `Retry-After` (seconds or an HTTP date, capped at 2 min). Without it, the wait runs until `X-RateLimit-Reset`, or backs off 1s/2s/4s (all clamped to 1–30s). Waits stop when the context is cancelled. `OnResponse` sees every response.
  - **Cache**: every list is written through to kv_cache (kind `plane`, project = Plane project id, TTL = `plane.refresh_interval`) under `plane:<workspace>:<resource>:<project>:<fnv(query|limit)>`. `CachedWorkItems` / `CachedProjects` read a list back (items, fetched-at, stale) so P3.2 can render first and refresh after (P0.8). Gets, writes and comments aren't cached, and a failed refresh never touches the cache.
  - **Template provider** (the P1.7 TODO): `main` registers `plane_issue` → `Client.IssueContext`, which fills `{issue_title}` and `{issue_body}` (as plain text) from a key like `FORGE-12`. Config and key load on first use, so a missing Plane setup only fails templates that use an issue, and those fall back to the form.
  - **`forge plane ls [project]`**: the project is found like the Plane tab finds it (later change, with P3.3), via `projects.ResolvePlane`:
    1. a forge project by name; without an argument, the repo you're in, else the only project with a saved link;
    2. its saved link (picked on the tab, or `plane_project_id` in config), including a saved "none";
    3. else the automatic match (`projects.PlaneMatch`), which the CLI then saves like the tab does (`linked owner/repo to Plane project FORGE` on stderr);
    4. else, for a typed name only, a Plane project identifier, name or UUID. This also lets you name a Plane project for a repo that chose "none".

    Steps 2–3 are `projects.PlaneLinkOf`, which the tab's `autoLink` also uses. The Plane project list is fetched only when a step needs it. Without a match the error says why (no match, "none" chosen, several linked, unknown name) and lists the workspace's Plane projects (`name one of: Forge (FORGE), …`, no archived ones). Tested per step in `internal/projects` and end to end in `cmd/forge` (link saved, second run without a project list). Checked live: `forge plane ls` in this repo linked it to FORGE and listed FORGE-1. Flags: `--state-group a,b`, `--mine`, `--search`, `--limit` (50, 0 = all), `--json`. Output is a table: `ID STATE PRIO TITLE` (with `expand=state`).
  - **Tests**: hand-written v2 fixtures in `internal/plane/testdata`, with no real workspace data. Table-driven `httptest` tests check every method's method, path, query, key header, body and decoding. Also covered: cursor paging and limit, the offset fallback, 429 retries (`Retry-After`, `X-RateLimit-Reset`, backoff, giving up, cancellation), problem+json, 401 / 404 / `ErrNoV2` and write-through cache. There are table tests for filter encoding, rate-limit parsing, dates, `IsCurrent` and `HTMLToText`. `cmd/forge` tests run `plane ls` end to end against an httptest server and check the kv_cache row.
  - **Limits**: v2 only, with no v1 fallback. An instance without `/api/v2` fails with `ErrNoV2`. The v2 rate-limit number isn't documented, so forge only reacts to 429s and doesn't pace itself. Paths and shapes come from the docs (developers.plane.so, v2) and haven't been checked against a live workspace yet.
- [x] P3.2 Issue list view — the Plane tab (`internal/tabs/plane`) replaces its placeholder: work items as a table with a detail pane.
  - **Wiring**: `main` builds the client with `planeClient(cfg.Plane, st.Cache())` and passes it with `app.WithPlane(api, err)` to `tabkit.Deps.Plane`. If Plane isn't set up (no base URL, workspace or key), the error goes to `Deps.PlaneErr`. Startup still works, and the tab shows "Plane isn't set up: …".
  - **Projects**: the list follows the active project's `plane_project_id`. With all projects, every configured Plane project is listed, merged by updated time. The `f` form can show another configured project instead. That override, plus the label and cycle filters (they belong to one project), is dropped when the project changes.
  - **Table**: Key · Title · State (coloured by state group, `Theme.StateGroupStyle`) · P (`Theme.PriorityIcon`: ‼ ▲ ■ ▽ ·) · Labels · Assignee (`daan +1`) · Updated (age). Sorted by most recently updated; a hidden id column keeps the selection across refreshes.
  - **Filters**: everything except `/` is sent to Plane. The default is assignee = me (from `Me`) and open states (triage, backlog, unstarted, started). `m` toggles me / anyone, `x` clears to anyone in every state, and `/` filters the loaded rows. The `f` Huh form has project, assignee (me, anyone or a member), states (multi-select), and label and cycle (current one marked). Label and cycle only appear when one project is shown and its details have loaded. A box above the table shows the active filters. If `Me` fails with anything but an auth or no-v2 error, the tab falls back to anyone with a toast.
  - **Loading**: lists ask Plane with `expand=state,labels,assignees`, `order_by=-updated_at` and at most 1000 items per project (the status bar says when that cap is hit). The tab reads `CachedWorkItems` first, then fetches; a cached copy that arrives after the fetch is ignored. Project details (`Project`, `States`, `Labels`, `Cycles`, `ProjectMembers`) load once and again after 10 min. Errors toast once per refresh: a 401 reads "API key rejected", a missing v2 "this Plane instance has no /api/v2". Background refresh uses the `plane` source, only while the tab is visible.
  - **Detail pane**: the header comes from the row at once: key · project, the title on a band, state, priority, assignees, label chips, cycle, due date and ages. After a 150 ms rest it fetches `WorkItem` (for the description) and `Comments` together, plus `sessions.IssueLinks`. Below the header it shows Description, Linked (sessions with status, and PRs as `#7 title [state]`) and Comments (oldest first, author from project members). Markdown is rendered with Glamour off the update loop (`ui.RenderMarkdown`, moved from the PRs tab). Details are kept in memory per id + updated_at (64 items). `J` / `K` scroll.
  - **HTML**: `plane.HTMLToMarkdown` turns Plane's tiptap HTML into Markdown: headings, emphasis, inline code, fenced code with its language, links, images, nested and task lists, quotes and rules. Unknown tags such as mentions are dropped and their text kept.
  - **Links (the P1.9 TODO)**: after each fetch the tab calls `sessions.SetIssueStates`, only for keys whose state name changed. That runs `Links.SetState(IssueRef(key), state)`, so session panes show current issue states. `sessions.IssueLinks(key)` returns the sessions linked to an issue plus PRs linked to those sessions or to the issue itself, each once, skipping deleted sessions.
  - **Keys / palette**: `f` filters, `m` mine, `x` clear, `J`/`K` scroll the detail, `o` opens in Plane (`IssueURL`), `y` copies the URL (OSC 52 fallback). Each also has a palette command (`plane_*`). Issues are palette entities (kind "Issue", searchable by key, title and labels); picking one clears `/` if it hides the item.
  - **Tests**: table tests for `HTMLToMarkdown`. Tab tests with a fake `plane.API` cover the default filter, cache-first after a failed fetch, a late cache copy being ignored, mine/clear, `/`, the `f` form (label, then a project switch dropping it), the cycle filter, all projects, a project without Plane, not set up, the auth error, the anyone fallback, the detail pane (Glamour, comment order and authors, linked sessions and PRs, memory cache), quiet stale detail errors, entities/select, state sync, open/copy and palette ids. Sessions tests cover `IssueLinks` and `SetIssueStates`. The app teatest `TestBrowsePlaneIssues` is the done-when: `4` → my open issues and description → `m` → `x` → ctrl+k `FORGE-3` → its detail.
  - **Not verified live**: there is no Plane workspace / key configured on this machine, so the tab has only run against fakes. Try it with `workspace_slug` and `$PLANE_API_KEY` set.
  - **Limits**: one assignee and one label at a time in the form (Plane accepts several). Label and cycle filters need a single project. No kanban yet (P3.3) and no create / edit / comment (P3.4).
- [x] P3.3 Kanban board — `b` switches the Plane tab between the list and a full-width board (`internal/tabs/plane/board.go`).
  - **Columns**: one per state, ordered by group (triage, backlog, unstarted, started, completed, cancelled), then by `sequence`. With several projects, states with the same group and name share a column. Headers show the count, plus `↑n` / `↓n` when cards are scrolled out of view. Cards show the key, priority icon, assignee initials (`DA BO +1`) and the title wrapped to two lines. In a column, cards sort by priority, then by most recently updated.
  - **Fetching**: the board ignores the list's state filter. Assignee, label, cycle and project filters still apply. Each project has two lists: open groups, and completed + cancelled updated since the start of the day `plane.board_done_days` ago (default 14; at least 1). Rounding to the day keeps the kv_cache key stable all day. The tab keeps a pool of both views' lists, so `b` shows data at once and then refreshes. Only the visible view fetches.
  - **Keys**: `h`/`l` (or ←/→) move between columns and `j`/`k`/`g`/`G` between cards. Each column remembers its card. The cursor stays on the card it was on through refreshes, including the moment when a card is in neither list. If the card is hidden (filtered out, or moved away), the cursor moves to the nearest column with cards, and comes back once the card shows again (e.g. after `esc` clears the filter).
  - **Text filter**: `/` filters board cards too, with the list's matching (`ui.RowMatches` on the same row cells, shared with `ui.Table`). It is shared between the views and shows under the filter box. Counts are of the matching cards. Picking a hidden issue from the palette clears it. `b` keeps the selection both ways when the other view has the card.
  - **Moving**: `H`/`L` move the card to the previous / next state of its own project. The board changes at once and the cursor follows. Then `UpdateWorkItem` PATCHes `state_id`. Moves of one card go out one at a time: if the card moved again while a PATCH was in flight, the next PATCH sends the latest target. On failure the card goes back to the last state Plane accepted, with a toast (`FORGE-3: couldn't move to Done: …`). A refresh that lands mid-move keeps the target. On success the new state goes to the links table (`SetIssueStates`). If states haven't loaded yet, it toasts "states still loading". The palette's move commands also work from the list.
  - **Collapse / scroll**: a collapsed column is a 3-wide strip (count, then the name downwards). Cancelled columns start collapsed. `z` collapses or expands the cursor's column. `Z` collapses every empty column, or expands them all when they are all collapsed. The cursor can sit on a strip (drawn in the accent), but no card is selected there, so `j`/`k`/`H`/`L`/`enter` do nothing. A column opens when the wanted card lands in it (moved there with `H`/`L`, or picked from the palette). If every column fits, they share the width (24–40 cells). If not, as many ~28-wide columns as fit fill the width and scroll sideways, with `‹ n` / `n ›` for hidden columns.
  - **Card detail**: `enter` opens the same detail as the list's pane, as an overlay (lipgloss compositor), and `esc` closes it. `J`/`K`, `o`, `y`, `H`/`L` and `b` work on it. P3.4's `e`/`c`/`s` work there too. Details are only fetched while the overlay is open.
  - **Keys / palette**: `plane_board`, `plane_col_left`, `plane_col_right`, `plane_move_prev`, `plane_move_next`, `plane_collapse_column` (`z`), `plane_collapse_empty` (`Z`), `plane_open_card`. Board-only commands switch to the board first. The overlay closes with `Input.Cancel` (esc), because a tab binding for esc would clash with the main scope. `TestRemapChangesBehaviourAndHelp` now remaps help to `W` (`H` is taken), and the keymap's help-range test uses `U`–`Y` (`Z` is taken).
  - **Tests**: board tests with the fake API cover columns and fetch filters (with the cutoff), merging across projects, navigation, horizontal scroll at 60 columns, `z` / `Z` (including a strip under the cursor and moving into a collapsed column), the `/` filter (same rows as the list, palette select clears it, `esc`, the wanted card kept while hidden), move / rollback / double move with the second failing / refresh mid-move / missing states, the selection kept by `b`, the overlay (detail, copy, move, esc), palette commands, and table tests for title wrapping and initials. Config tests cover `board_done_days`. The app teatest `TestPlaneBoardMove` is the done-when: `4` → `b` → `L` (PATCH recorded, cursor in Shipped) → `H` → `b`.
  - **Not verified live**: still no Plane workspace on this machine, so only fakes. Try `b`, then `L`, then check the item in Plane's web UI.
  - **Limits**: no reordering within a column (no `sort_order` in our types). Moving a card into a closed state it isn't allowed in (e.g. a Plane workflow rule) shows Plane's error and rolls back.
- [x] P3.4 Create & edit — `n` new, `e` edit, `c` comment and `s` session in the Plane tab (`internal/tabs/plane/edit.go`).
  - **Forms**: `n` and `e` open a two-page Huh form (through `openDialog`, so esc cancels and the tab captures input).
    - Page 1: title (required, at most 255 characters), state (ordered like the board; create defaults to the project's `is_default` state), priority (default none), labels (multi-select), assignee (unassigned, me, then members) and cycle (none, then cycles with "(current)").
    - Page 2: the description, a Huh text field where ctrl+e opens `$EDITOR` on a `.md` file.
    - With several projects listed, `n` asks for the project first.
    - The form opens once the project's details are loaded (`loadMeta`) and, for edit, once the full item has arrived (`WorkItem`, for `description_html`). Until then a "loading…" toast shows.
  - **Saving**:
    - Create sends `CreateWorkItem` with ids. Its response has no identifier, so the key is built from the project identifier and `sequence_id`.
    - The new item goes straight into the pooled lists whose filters match it, is selected (list or board) and toasts "created FORGE-5". If the filters hide it, an info toast says so. Then the tab refreshes.
    - Edit PATCHes only the fields that changed. If nothing changed, it toasts "no changes" and sends nothing.
    - Clearing the cycle sends `cycle_id: null` (`WorkItemUpdate.MarshalJSON`).
    - Changing the assignee replaces every assignee with the one picked; leaving it unchanged keeps them all.
  - **Markdown ↔ HTML**: `plane.MarkdownToHTML` uses goldmark (now a direct dependency) with GFM and raw HTML allowed. Task lists are rewritten to tiptap's `data-type="taskList"` / `data-checked` shape.
  - **Round-trip safety**:
    - `description_html` is only sent when the edited Markdown differs from what the form was opened with, so a description you don't touch is never rewritten.
    - `plane.HTMLToEditableMarkdown` keeps mentions (`mention-component`) and images (`image-component`, `img`) verbatim as raw HTML, so they survive an edit. A literal `<` in text is escaped.
    - It also lists what Markdown can't represent: mentions, images, tables, embeds, underline, highlights, sub/superscript and unknown tags. When that list isn't empty, the form shows a warning on both pages.
  - **Comments**: `c` opens a Markdown text field (ctrl+e for `$EDITOR`); a blank comment is refused. Submitting calls `CreateComment` and toasts "commented on FORGE-1". The item's cached details are dropped, so the new comment shows after a reload while the old version stays on screen until then.
  - **Sessions**: `s` calls `tabs.StartSessionFrom` with `KindPlaneIssue`, the key, URL, title, state name and the repo of the forge project linked to the Plane project. The Sessions tab picks the `implement-issue` template. Since P3.7 it also passes `plane.BranchFor(item)` as the branch.
  - **Keys / palette**:
    - Keys: `plane_new` (`n`), `plane_edit` (`e`), `plane_comment` (`c`), `plane_session` (`s`). They work in the list, on the board and in the card overlay (one shared `itemAction`).
    - Each has a palette command. They appear in short and full help, and in `default.yaml`.
    - New `Theme.FormWarn` style for the warning.
  - **Tests**:
    - Table tests: `MarkdownToHTML`, `HTMLToEditableMarkdown` (lossy kinds; mention and image survive HTML→MD→HTML; tables flagged), round-trip stability for plain content, and `WorkItemUpdate` JSON (null cycle).
    - Tab tests:
      - create in list and board (payload, toast, selection)
      - a blank title refused
      - created but hidden by filters
      - the project picker with several projects
      - create failure
      - an edit that sends only the title (no description)
      - an edit of priority, cycle and description
      - no changes
      - a lossy description: warning shown and not sent when unchanged; mention and image kept when edited
      - comment (a blank one refused, HTML, toast, detail reload)
      - session source
      - keys in the board overlay
    - The harness skips cursor-blink commands, so form tests don't wait on real timers.
    - App teatest `TestPlaneCreateIssue`: `4` → `m` → `n` → title → submit → "created FORGE-3", cursor on it.
  - **Not verified live**: still no Plane workspace on this machine. Worth checking in Plane's web UI: task lists render as checklists, a cleared cycle is accepted as `null`, and an edited description with a mention keeps the mention.
  - **Limits**:
    - One assignee in the form.
    - Tables, embeds and similar content are flattened to text if you edit a description that has them (you are warned first).
    - The description field's height is set when the form opens.
- [x] P3.5 Quick capture — `ctrl+n` from any tab, `forge capture "title #proj !high @me +label"` from any shell, and `forge capture` alone for the tmux `Alt+c` popup.
  - **Tokens** (`internal/capture`, no UI): `#project` (Plane identifier or name, or a linked forge project's name), `!priority` (urgent / high / medium / low / none, prefixes like `!h`), `@person` (`@me`, display name, first name, full name or email local part; several allowed) and `+label` (several; case, spaces and `_` fold to `-`). A token is a word starting with its sigil. A sigil inside a word (`C#`) or alone stays in the title, and `\#1` keeps `#1`. Last `#`/`!` wins.
    - Without `#project`: the active forge project's Plane link, else the only linked project (`capture.DefaultProject`). The CLI uses the repo you're in, else the last active project. `--project` / `-p` adds a `#token` when the line has none.
    - Unknown tokens are blocked with the closest match: `no label "frontnd" — did you mean frontend?`. The match is a prefix, then a substring, then an edit distance (transpositions count once; within 1 for values up to 3 characters, else within 2 or a third of the length). In the line, `tab` applies it. The CLI exits 1 with the same message.
    - The state is left out, so Plane uses the project's default.
  - **Catalog**: projects, you, and per-project labels and members. Loaded concurrently, only as needed (`capture.Need`): a plain title with a default project fetches nothing before the create. `ResolveLoading` / the pending-submit loop load what a `NotLoadedError` names and retry, at most 3 rounds.
    - Cached first: new `plane.CacheReader` (`CachedMe`, `CachedLabels`, `CachedProjectMembers`, plus the existing `CachedProjects`) is an optional interface, so fakes don't need it. `Me` now writes through to the cache.
    - Completion hides Plane's intake bot (display name `<uuid>-intake`; the API has no bot flag).
  - **Line** (`ui.CaptureInput`, shared by the overlay and the CLI): a one-line editor (←/→, home/end, ctrl+a/e/b/f, ctrl+w, ctrl+u, delete). Tokens are highlighted. A row shows the candidates for the token under the cursor; `tab` / `shift+tab` cycle through them in place. A message row shows errors, "did you mean" with "(tab to fix)", or a hammer spinner while loading or creating.
  - **TUI**: `ctrl+n` (`capture`) opens it over any tab, not while a tab takes text input, and `ctrl+n` again closes it. The title names the default project.
    - `enter` closes the overlay at once and creates in the background. On success it toasts "created FORGE-7" (with sparks) and the Plane tab inserts and selects the item (`(*planetab.Tab).Created`). On failure an error toast appears and the next `ctrl+n` reopens the text.
    - `shift+enter` / `alt+enter` switches to the Plane tab and opens the P3.4 create form prefilled (`NewIssue`, `IssueDraft`). Tokens that don't resolve are left out there.
    - Palette command "Quick capture issue…".
  - **CLI**: `forge capture "…"` prints `created FORGE-7  <url>`. When the create response has no key, it uses the project's identifier (from the catalog, else one `Project` call).
    - With no arguments: an inline Bubble Tea program (no alt screen) with the same line. It loads the catalog from the cache, then fresh. `enter` creates, shows the result for 600ms and exits; esc exits and leaves nothing on screen.
    - `shift+enter` exits the program and runs the full create form standalone (`planetab.RunCreateForm`; the form builder no longer needs the tab: `issueForm(formCtx, …)`).
  - **tmux**: forge's tmux.conf is now v2 with `bind -n M-c display-popup -E -w 76 -h 8 "forge capture"` and `M-c capture` in the status line. Existing files aren't rewritten. Instead `app.WithNotice` shows "forge's tmux config has updates (Alt+c capture)…" once per config version, remembered in the store under `notice:tmux-config-v2`. The SPEC tmux section has the `~/.tmux.conf` line for capturing from your own tmux.
  - **Fix on the way**: Plane's labels endpoint returns 400 for cursor paging ("This ordering can't be used with cursor pagination"). `Labels` now asks for `order_by=sort_order`, which `listAll` already offset-pages. This also fixes the Plane tab's label loading on such instances.
  - **Keys**: `capture` (global) and `capture_submit`, `capture_full_form`, `capture_complete`, `capture_complete_prev`, `capture_left` / `right` / `home` / `end`, `capture_delete_word`, `capture_delete_forward` in a modal `capture` scope, all in `default.yaml`. `validateTitle` moved to `capture.ValidateTitle`. The `stubCmd` helper is gone (capture was the last stub).
  - **Tests**: table tests for `Parse`, `Resolve` (every token kind, defaults, aliases, unknowns with and without suggestions, empty / long titles, not loaded), `Complete`, `Candidates`, `ResolveLoading` (loads only what's needed), `DefaultProject` and `Merge`. Widget tests cover editing, the action keys, tab cycling, fix, busy and width. App tests cover the teatest flow (`ctrl+n` on PRs → tokens → "created FORGE-1"; then `shift+enter` → prefilled form), an unknown label (hint, tab fix, create), completion, the key rules and the notice shown once. CLI tests run against an httptest Plane (only linked project, every token, `#anv`, `-p`, unknown label / project, no title). There are also a tmux config v2 test and a labels paging test.
  - **Verified live** (read-only, nothing created): `forge capture` in a 100×12 tmux pane against the real workspace. Project, person and label completion work, as do the unknown-project hint, tab to fix and esc. The ctrl+n overlay renders over the PRs tab. Creating an item, and the 3-second Alt+c round trip, weren't run, to avoid making real issues.
  - **Limits**: one `#project` (the last wins). Labels can't be created from the line. The overlay's catalog is refreshed after 5 minutes, on the next `ctrl+n`.
- [x] P3.6 Cycle summary: a line under the Plane tab's filter box (list and board) with the current cycle's name, days left, a heat bar with % done and counts: done / in progress / blocked / todo.
  - **Current cycle**: the first cycle in the project's cached metadata where `IsCurrent(now)` holds. The line only shows when exactly one project is listed. With no current cycle there is no line, and the table keeps its row.
  - **Counts**: a separate fetch of every item in the cycle (`cycle_id` only, no order, so it gets its own cache entry), whatever the tab's filters are. The default "mine, open" list doesn't skew it. It loads the cache first, then fetches, on each `Refresh` and when the metadata lands.
    - The list is kept in `Tab.cyc` (a `projectItems`). `editItem`, `insertItem` and `reapplyMoves` cover it (`allLists`), so board moves, edits and creates update the counts without a refetch.
    - Buckets (`countCycle`): completed → done, cancelled → left out of the total (a muted "N cancelled"). Otherwise blocked if blocked, else started → in progress, else todo (triage / backlog / unstarted / unknown). Progress = done / (total − cancelled).
    - "Ends today" / "1 day left" / "N days left": a date-only end date is its own day, a date-time end date counts by its day in local time.
  - **Blocked**: new `plane.blocked_labels` (default `[blocked]`) and `plane.blocked_states` (default `[Blocked]`), matched by name, case-insensitive (`config.Plane.IsBlocked`). A completed item with a blocked label still counts as done.
  - **Width**: the line drops the key hint, then the cancelled count and half the bar, then switches to short labels (`✓1 ▸1 ⊘1 ○1`).
  - **Key**: `plane_cycle` (`C`) switches between the current cycle only (the existing cycle filter, shown as "cycle: Sprint 12" in the filter box) and every cycle. It works in the list and on the board. Palette: "Plane: current cycle only" / "Plane: every cycle". Without a current cycle it shows "no current cycle" (for several projects, also how to pick one).
  - New theme styles `CycleName`, `CycleBlocked`.
  - **Tests**:
    - Table tests: `countCycle` (each bucket, blocked by label / state / case, done over blocked, cancelled excluded, empty without NaN, unknown state), `daysLeft`, and `IsBlocked` + YAML override.
    - Tab tests: header counts with the default filter, full / short variants by width, no line without a current cycle or with several projects (and the table height), `C` on, then off on the board, palette titles, and a board move updating the counts without a refetch.
  - **Verified live** (read-only): the real FORGE project has no running cycle, so no line shows and `C` toasts "no current cycle". The header against a real cycle wasn't checked live, because creating a cycle would be a write.
  - **Limits**:
    - No clicking: forge has no mouse mode, so it's `C` / palette only.
    - One project at a time.
    - Counts cover the first 1000 items of the cycle.
    - An item edited out of the cycle keeps counting until the refresh that follows the edit.
- [x] P3.7 Branch naming: sessions started from a Plane item work on a `proj-123-short-title` branch. PRs whose branch or title names an item key are linked to the item, and show in both detail panes.
  - **`plane.BranchFor(item)`**: the lower-cased key plus a slug of the title. ASCII only: common Latin accents are folded by a small table (é→e, ß→ss), combining marks and other scripts are dropped, and every other run becomes one `-`. At most 50 bytes, cut at a word boundary where it can be. An empty title gives just the key.
  - **Sessions**: the Plane tab's `s` passes `BranchFor(item)` as `tabs.Source.Branch`, which replaces `agent/<name>`. Creating, reusing or taking ownership of the worktree works as in P1.9: if the branch already exists, it is reused. `forge new --issue` still uses `agent/<name>` unless `--branch` is given.
  - **Detection** (`plane.FindKey`): only keys of the workspace's Plane project identifiers count, ignoring case and not inside a longer word. That way `utf-8`, `p3-7` and `myproj-3` don't match. The branch is checked first, then the title.
  - **Linking**: after each successful PR section fetch, the PRs tab queues that section's PRs for a background run (`prs/backlink.go`). One run happens at a time, so two sections that land together never both treat a link as new.
    - The identifiers come from the cached Plane projects, else `Projects()`, and are kept on the tab after that.
    - The new `sessions.Service.LinkIssuePRs` stores an issue → PR link with the PR's URL, title and state. It returns only the links that didn't exist before. A PR whose title or state changed is updated, and an unchanged one isn't written at all.
    - Errors (for example a bad Plane key) are toasted once per run of forge.
  - **Detail panes**:
    - Plane: `IssueLinks` already returned PRs linked directly to the item, so they show under "Linked" with their state, and no change was needed.
    - PRs: a new **Issues** section under Sessions, from `sessions.Service.PRIssues`. Each line is `◆ PROJ-123`. It is loaded together with the linked sessions, and again for the selected PR after a run adds links.
  - **Comment** (optional): `plane.comment_on_pr_link` (default `false`). When a run first links an **open or draft** PR, forge posts `<p>Pull request <a href=…>owner/repo#12</a>: title</p>` on the item. Merged and closed PRs never get a comment, so turning the option on doesn't flood old items.
  - **Tests**:
    - Table tests: `BranchFor` (punctuation, accents, combining marks, other scripts, empty or junk title, word-boundary cut, hard cut) and `FindKey` (branch, title, branch before title, a path prefix, the longest identifier winning, unknown identifiers, inside a word, none known).
    - Service: `LinkIssuePRs` (new, unchanged without a rewrite, state update not reported as new). `IssueLinks` then shows the PR on the item, and `PRIssues` shows the item on the PR.
    - PRs tab:
      - links from the branch and the title, with meta, and the detail shows `◆ PROJ-12`
      - identifiers fetched once
      - comments only on open and draft PRs, and only once across refreshes
      - an error toasted once
      - nothing linked without a Plane client
    - Plane tab: `s` sets `Branch`.
  - **Limits**:
    - Only PRs in the configured PR sections are scanned, and only while the PRs tab refreshes (it doesn't refresh while hidden). A PR opened from an issue session is still shown on the item through the session (`SyncPRs`, on GitHub ticks) before then.
    - If the comment fails, it isn't retried, because the link already exists.
    - PR-side issue lines show only the key: the item's title isn't stored on the link.

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
