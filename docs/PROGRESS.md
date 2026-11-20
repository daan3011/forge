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
  - **Limits**: the picker lists the current project's sessions (as the Sessions tab does); switch to "All projects" to reach others. Archived sessions can't be resumed (their worktree is removed by design).
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
- [ ] P1.8 Waiting notifications
- [ ] P1.9 Session ↔ issue/PR links
  - Note (from P1.7): StartSessionFrom opens the form with `sessionstab.NewRequest{Refs: {plane_issue|github_pr: ref}}`, so the template skips asking for it.
- [ ] P1.10 Auto-archive

## Phase 2 — GitHub

- [ ] P2.1 PR list & sections
  - TODO (from P1.7): register a `templates.ContextProvider` for `github_pr` (fills `{pr_title}`, `{pr_body}`) through `app.WithTemplates` / `deps.providers`.
- [ ] P2.2 PR detail pane
- [ ] P2.3 PR quick actions
- [ ] P2.4 Review queue & diff
- [ ] P2.5 Stale PR markers
- [ ] P2.6 Pipelines list
- [ ] P2.7 Log tail & rerun
- [ ] P2.8 "Fix this" from CI

## Phase 3 — Plane

- [ ] P3.1 Plane client
  - TODO (from P1.7): register a `templates.ContextProvider` for `plane_issue` (fills `{issue_title}`, `{issue_body}`) through `app.WithTemplates` / `deps.providers`.
- [ ] P3.2 Issue list view
- [ ] P3.3 Kanban board
- [ ] P3.4 Create & edit
- [ ] P3.5 Quick capture
- [ ] P3.6 Cycle summary
- [ ] P3.7 Branch naming

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
- [ ] P6.2 Scratchpad & daily note
- [ ] P6.3 Custom commands
- [ ] P6.4 Polish & packaging
  - [ ] TODO: `forge doctor` reports the tmux version and which config file the forge server uses (`#{config_files}`), and warns when the user's tmux.conf version (`tmux.ParseConfigVersion`) is older than the embedded one (`tmux.ConfigVersion`).
