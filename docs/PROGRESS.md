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
- [ ] P1.4 Live status via hooks
- [ ] P1.5 Attach & resume
- [ ] P1.6 Transcript viewer
- [ ] P1.7 Session templates
- [ ] P1.8 Waiting notifications
- [ ] P1.9 Session ↔ issue/PR links
- [ ] P1.10 Auto-archive

## Phase 2 — GitHub

- [ ] P2.1 PR list & sections
- [ ] P2.2 PR detail pane
- [ ] P2.3 PR quick actions
- [ ] P2.4 Review queue & diff
- [ ] P2.5 Stale PR markers
- [ ] P2.6 Pipelines list
- [ ] P2.7 Log tail & rerun
- [ ] P2.8 "Fix this" from CI

## Phase 3 — Plane

- [ ] P3.1 Plane client
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
