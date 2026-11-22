# forge — Spec & Build Prompts

Nov 20, 2026 · @Daan

## Overview

**forge** is one Go TUI that puts your PRs, CI, Plane board, Claude Code sessions, docs and design artifacts behind a single keyboard-driven interface. Its home screen answers one question: *what needs me right now?*

### Principles

- **Keyboard first.** Every action reachable from the command palette (`ctrl+k`); vim-style movement everywhere.
- **Never block the UI.** All network and disk work runs as `tea.Cmd`s; results arrive as messages. Cached data shows instantly, then refreshes.
- **Local state in one place.** SQLite for sessions, pins, links and cache; YAML for config; plain markdown for docs.
- **Agents are first-class.** Every Claude session has a name, a worktree, a tmux window, a live status and links to its issue and PR.
- **Feels like a forge.** Small, quick animations (hammer strikes, sparks, glowing embers) show work happening. They never slow anything down and can be turned off.
- **Don't fight existing tools.** Reuse `gh` auth, `tmux`, `git worktree`, `$EDITOR`, `delta`, and the `claude` CLI rather than reimplementing them.
- **Build in vertical slices.** Each phase ships something usable on its own.

### Look & feel

| Element | Design |
| --- | --- |
| Palette | Charcoal background, steel-grey borders, ember orange accent, molten yellow highlights, cool blue for links |
| Splash | ASCII anvil; hammer strikes twice, sparks fly, "forge" wordmark glows from dark red to white-hot (under 1s, any key skips) |
| Spinner | Hammer-on-anvil frames for loading; tiny spark burst when a fetch finishes |
| Progress | "Heat bar" that shifts dark red → orange → yellow → white as it fills |
| Agent status | working = flickering flame, waiting = pulsing amber ember, idle = cold grey anvil, error = cracked red, dead = ash |
| Success | Spark burst on merge, approve, issue created, session started |
| Empty states | Cold forge with a faint ember: "nothing on the anvil" |
| Transitions | Spring-based pane resize and overlay slide-in via `harmonica` |

### Stack

| Concern | Choice |
| --- | --- |
| Language | Go 1.23+ |
| TUI | Bubble Tea, Bubbles (list, table, viewport, textinput, spinner), Lip Gloss |
| Animation | `charmbracelet/harmonica` (springs) + custom frame-based components |
| Markdown rendering | Glamour |
| Forms | Huh |
| Fuzzy matching | `sahilm/fuzzy` |
| Storage | SQLite via `modernc.org/sqlite` (no cgo) |
| Config | YAML (`gopkg.in/yaml.v3`) at `~/.config/forge/config.yaml` |
| GitHub | `cli/go-gh/v2` (reuses your `gh auth` token; GraphQL + REST) |
| Plane | REST API with `X-API-Key`, base URL configurable for self-hosted |
| Agent hosting | tmux on a dedicated socket (`tmux -L forge`) |
| Agent isolation | `git worktree`, one per session |
| File watching | `fsnotify` |
| Notifications | `gen2brain/beeep` (falls back to `notify-send` / `osascript`) |
| CLI entry | `spf13/cobra`: `forge`, `forge hook`, `forge capture`, `forge new` |

### File layout on disk

| Path | Holds |
| --- | --- |
| `~/.config/forge/config.yaml` | Repos, Plane workspace, keybinds, sections, custom commands |
| `~/.local/share/forge/forge.db` | Sessions, events, links, pins, cache, usage |
| `~/.local/share/forge/worktrees/<repo>/<session>` | Agent worktrees |
| `<repo>/.forge/docs/` | Project docs and decision log (committed to git) |
| `~/.local/share/forge/notes/YYYY-MM-DD.md` | Scratchpad / daily notes |

### Code layout

```text
cmd/forge/           main + cobra commands (root, hook, capture, new)
internal/app/        root model, routing between tabs, global keys, palette
internal/ui/         shared components: table, split pane, status bar, theme, keymap
internal/ui/anim/    forge animations: splash, spinners, heat bar, sparks, status flames
internal/tabs/       home, prs, pipelines, plane, sessions, docs, artifacts, context
internal/github/     go-gh client, queries, caching
internal/plane/      Plane REST client
internal/sessions/   tmux driver, worktree manager, hook ingest, transcript parser
internal/store/      SQLite schema, migrations, repositories
internal/config/     config load/validate/defaults
internal/notify/     desktop notifications
```

### Architecture

&#91;embedded content: forge architecture · 7 parts\]

forge talks to GitHub and Plane directly and drives agents through tmux. Agents report back on their own: Claude Code hooks call `forge hook`, which writes to the database and pings the TUI.

## Feature spec

There are 49 features in seven phases. IDs match the build prompts below. Phase 0 is the shell everything else plugs into, and Phase 1 (agent sessions) is the core of the tool.

### Phase 0 — Foundations

| ID | Feature | Behaviour |
| --- | --- | --- |
| P0.1 | Project scaffold | Go module, cobra commands, Makefile, golangci-lint, CI. |
| P0.2 | Config | YAML config with defaults, validation and a `forge config edit` command. Projects need no config: they are detected, and config entries only override them. |
| P0.3 | Store | SQLite with versioned migrations; repositories per domain. |
| P0.4 | App shell & theme | Tab bar, split list/detail layout, status bar, forge palette (see Look & feel). |
| P0.5 | Keymap & help | Central keymap, vim movement, `?` help overlay generated from bindings. |
| P0.6 | Command palette | `ctrl+k` fuzzy search over actions and entities (PRs, issues, sessions, docs). |
| P0.7 | Project switcher | `ctrl+p` picks the active project from the detected repos, most recently used first; it filters every tab (repo + Plane project + docs). |
| P0.8 | Background refresh | Per-source refresh intervals, cache-first loading, "updated 7s ago" and rate-limit display. |
| P0.9 | Forge animations | Splash, hammer spinner, heat bar, spark bursts, flame status icons, cold-forge empty states; `animations: full / reduced / off`. |

### Phase 1 — Agent sessions

| ID | Feature | Behaviour |
| --- | --- | --- |
| P1.1 | tmux driver | Wraps `tmux -L forge`: create/list/kill windows, send keys, capture pane, attach/switch. |
| P1.2 | Worktree manager | Each session gets a `git worktree` on branch `agent/<name>`; cleanup offered on close. |
| P1.3 | Start named session | Form: name, repo, base branch, template, first prompt. Starts `claude --session-id <uuid>` in the worktree. |
| P1.4 | Live status via hooks | `forge hook <event>` ingests Claude Code hook JSON; list shows working / waiting / idle / error / dead. |
| P1.5 | Attach & resume | Enter attaches; dead sessions are reopened with `claude --resume <uuid>` in the right directory. `b` resumes in the background; ctrl+g picks a session from anywhere. |
| P1.6 | Transcript viewer | Parse session JSONL: recent messages, files touched, tool calls, token estimate. |
| P1.7 | Session templates | Saved prompts with `{pr}`, `{issue}`, `{branch}`, `{repo}` variables. |
| P1.8 | Waiting notifications | Desktop notification after a session waits for input longer than N minutes. |
| P1.9 | Session ↔ issue/PR links | Start from a Plane issue or PR; links shown on all three detail panes. |
| P1.10 | Auto-archive | Sessions whose PR merged move to an archive; notes and summary stay searchable. |

### Phase 2 — GitHub

| ID | Feature | Behaviour |
| --- | --- | --- |
| P2.1 | PR list & sections | YAML-defined sections (search queries), repo filter, columns as in gh-dash. |
| P2.2 | PR detail pane | Overview, checks, activity tabs; review and merge state. |
| P2.3 | PR quick actions | Checkout, open in browser, copy URL, update branch, merge when green. |
| P2.4 | Review queue & diff | Diff in a viewport (through `delta` if installed); approve, request changes, comment. |
| P2.5 | Stale PR markers | Flag PRs with no activity in N days (configurable). |
| P2.6 | Pipelines list | Workflow runs per repo/branch with status, duration, trigger. |
| P2.7 | Log tail & rerun | Failed job logs collapsed to error lines; rerun failed jobs (`t`) or the whole run (`ctrl+r`); cancel (`X`). |
| P2.8 | "Fix this" from CI | Starts a session in the PR's worktree with the failing log in the first prompt. |

### Phase 3 — Plane

| ID | Feature | Behaviour |
| --- | --- | --- |
| P3.1 | Plane client | Work items, states, labels, cycles, members; self-hosted base URL support. |
| P3.2 | Issue list view | Filter by project, state, label, assignee; detail pane renders description. |
| P3.3 | Kanban board | Columns per state; `H`/`L` moves the card between states. |
| P3.4 | Create & edit | Huh form for title, description (via `$EDITOR`), state, priority, labels, assignee. |
| P3.5 | Quick capture | Global key opens a one-line capture; also `forge capture "title"` from any shell or tmux. |
| P3.6 | Cycle summary | Header shows current cycle: done / in progress / blocked, days left. |
| P3.7 | Branch naming | Issue → `PROJ-123-short-title` branch; PRs link back to the Plane issue. |

### Phase 4 — Docs & design artifacts

| ID | Feature | Behaviour |
| --- | --- | --- |
| P4.1 | Docs browser | Tree of `.forge/docs/` per project, rendered with Glamour, `e` opens `$EDITOR`. |
| P4.2 | Docs search | Full-text search across all projects' docs. |
| P4.3 | Send docs to agent | Multi-select docs (space) and attach them to a new or running session (`s`). |
| P4.4 | Decision log | Quick ADR form (`forge adr`, `a`, palette: title, context, decision, alternatives, consequences) saved as numbered markdown in `decisions/` with front matter; a Decisions view (`d`) lists them newest first and `S` marks one superseded with a link to the newer one; the project's CLAUDE.md points agents at the log. |
| P4.5 | Artifact gallery | Watches configured folders for HTML/SVG/PNG/MD made by Claude; list by project and date. |
| P4.6 | Pin & tag artifacts | Pin, tag, rename; pinned ones always on top; `o` opens in browser. |
| P4.7 | Inline previews | Image previews via kitty graphics (Unicode placeholders; kitty, Ghostty) or sixel (WezTerm and others) when supported, `chafa` fallback, text otherwise; HTML shows a text summary. `artifacts.preview` overrides detection. |

### Phase 5 — Context & cache

| ID | Feature | Behaviour |
| --- | --- | --- |
| P5.1 | CLAUDE.md manager | Global, project and nested CLAUDE.md side by side, edit in `$EDITOR`, token estimate each. |
| P5.2 | Context packs | Named bundles of files, docs and prompts attachable to any session. |
| P5.3 | Cache controls | Size and age per cache type (API, transcripts, worktrees, artifacts); clear per project or type. |
| P5.4 | Usage view | Tokens and estimated cost per session, day and project from transcripts. |

### Phase 6 — Workflow glue

| ID | Feature | Behaviour |
| --- | --- | --- |
| P6.1 | Home screen | One "needs me" list: waiting agents, review requests, red pipelines, today's issues. |
| P6.2 | Scratchpad & daily note | Quick notes per day; auto "what I did" from merged PRs, closed issues, finished sessions. |
| P6.3 | Custom commands | Config-defined keybinds running shell commands with `{pr}`, `{branch}`, `{repo}`, `{worktree}`. |
| P6.4 | Polish & packaging | Error toasts, empty states, `goreleaser`, install script, README with screenshots. |

## tmux setup (from a plain install)

You only need three keys. forge runs all agents on its own tmux server (`tmux -L forge`) with its own config file, so it never touches any tmux you use yourself, and you rarely type tmux commands by hand.

### How forge uses tmux

- Each project gets a tmux **session** on the `forge` socket; each agent gets a **window** in it, named after the agent.
- Every tmux call forge makes passes `-L forge -f ~/.config/forge/tmux.conf`, so the forge server never reads `~/.tmux.conf` or `/etc/tmux.conf`. Your `~/.tmux.conf` only affects your own tmux.
- **forge running in a normal terminal:** pressing Enter on an agent suspends the TUI and attaches to that window (`tea.ExecProcess`). Detaching drops you straight back into forge.
- **forge running inside your own tmux:** the agent opens in a **popup** of your tmux (`display-popup -E -w 90% -h 90%`, running `env -u TMUX tmux -L forge -f <conf> attach …`), so nothing nests and forge stays visible behind it. `M-d` closes the popup. On tmux older than 3.2 (no popups) forge falls back to attaching inside the current pane.
- **forge running inside forge's own tmux server:** forge uses `switch-client` instead.
- After you detach or close the popup, forge refreshes that session's status and preview.
- forge reads agent output with `capture-pane` for previews, so you can peek without attaching.

### forge's tmux config (`~/.config/forge/tmux.conf`)

forge writes this file (or `$XDG_CONFIG_HOME/forge/tmux.conf`) on first run and never overwrites it, so it's yours to customise. Every line is commented. Its first line is a version header (`# forge-tmux-config v1`). The defaults:

- Terminal: `escape-time 10`, `tmux-256color` with RGB, `extended-keys` (Shift+Enter reaches Claude Code).
- `mouse on`, `history-limit 50000`, windows and panes numbered from 1, `renumber-windows on`.
- **No prefix at all** (`prefix None`, `prefix2 None`, `unbind C-b`). Nothing clashes with an outer tmux, and Ctrl+a stays start-of-line in claude and your shells.
- Prefix-less bindings only (below).
- A minimal status line in forge colours (ember on charcoal): the agent name on the left, `M-d back · M-[ scroll · M-w windows` on the right.

The server reads the file only when it starts. To apply changes:

- `forge tmux config` opens the file in `$EDITOR`, then offers to restart the server.
- `forge tmux config --reset` restores the default, keeping your file as `tmux.conf.bak.<timestamp>`, then offers a restart.
- `forge tmux restart` stops the forge server; the next action starts it with the current config. If agents are running, it lists them and asks first (`--yes` skips the question). Stopped sessions stay resumable with `claude --resume` (P1.5).

All three are palette commands too ("tmux: …").

### The keys you'll actually use

| Key | Does |
| --- | --- |
| `Alt+d` (`M-d`) | Detach from an agent / close the popup, back to forge |
| `Alt+[` (`M-[`) | Scroll mode (`q` to leave); with mouse on, the wheel works too |
| `Alt+w` (`M-w`) | Pick any agent window from a tree |
| `Alt+c` (`M-c`) | Quick capture a Plane issue in a popup (`forge capture`, P3.5) |

If your own `~/.tmux.conf` binds `M-d` in its root table, your tmux catches it before forge's popup does. forge warns you about this when it opens a popup; remove that binding (e.g. `unbind -n M-d`) and reload your tmux config.

Check your version with `tmux -V`; aim for 3.3 or newer (`display-popup` and `extended-keys` need 3.2+).

`Alt+c` is in forge's tmux.conf since config v2. A forge started with an older file shows a one-time notice to run `forge tmux config --reset`. To capture from **anywhere in your own tmux**, add this line to your `~/.tmux.conf` (forge never edits it):

```tmux
bind -n M-c display-popup -E -w 76 -h 8 "forge capture"
```

`shift+enter` (open the full form) needs `set -s extended-keys on` in that tmux; `alt+enter` does the same without it.

## How to use the prompts

Run one prompt per Claude Code session, in order, and commit after each. Every prompt assumes this doc is in the repo as `docs/SPEC.md` and a `CLAUDE.md` like the one below exists.

1. Create the repo, export this doc as Markdown and save it as `docs/SPEC.md`.
2. Add the `CLAUDE.md` below at the repo root.
3. For each prompt: start `claude`, press `shift+tab` into plan mode, paste the prompt, review the plan, then let it build.
4. Check the prompt's **Done when** list yourself, then commit: `git commit -m "P1.3: start named session"`.
5. Tick the feature off in `docs/PROGRESS.md` (P0.1 creates it).

Once Phase 1 is done, run the remaining prompts as forge sessions themselves: forge starts building forge.

### `CLAUDE.md` for the repo

```markdown
# forge

Personal Go TUI for PRs, CI, Plane, Claude Code sessions, docs and artifacts,
themed as a blacksmith's forge (embers, sparks, hammer and anvil).
Full spec: docs/SPEC.md. Progress: docs/PROGRESS.md.

## Stack
Go 1.23+, Bubble Tea, Bubbles, Lip Gloss, Harmonica, Glamour, Huh, cobra,
modernc.org/sqlite, cli/go-gh/v2, fsnotify.

## Rules
- Never block Update(): all I/O goes through tea.Cmd and returns a Msg.
- Each tab is its own model in internal/tabs/<name>, implementing the Tab interface.
- Shared widgets live in internal/ui; colours and styles only in internal/ui/theme.go.
- Animations only via internal/ui/anim components; they must respect the
  `animations` config (full / reduced / off) and only tick while visible.
- Keybindings only in internal/ui/keymap.go (so the help overlay stays accurate).
- Every user action must also be registered as a palette command.
- Schema changes = a new numbered migration in internal/store/migrations.
- External processes (tmux, git, claude, gh, delta) go through small wrapper
  packages with interfaces, so they can be faked in tests.
- Table-driven tests for parsers and clients; teatest for key flows.
- Run `make lint test` before saying a task is done.
- Don't add dependencies beyond the stack without asking.
- Update docs/PROGRESS.md when a feature is done.
```

## Build prompts

Each prompt is self-contained: paste it as-is into a fresh Claude Code session. They all point back to `docs/SPEC.md` for the bigger picture.

### Phase 0 — Foundations

#### P0.1 Project scaffold

```text
Read docs/SPEC.md (Overview) and CLAUDE.md. Scaffold the forge project.

- Go module `github.com/<me>/forge`, layout exactly as in "Code layout".
- cobra root command `forge` plus stub subcommands: hook, capture, new, config.
- Makefile targets: build, run, test, lint, fmt. golangci-lint config with sensible linters.
- GitHub Actions workflow running lint + test on push.
- docs/PROGRESS.md listing every feature ID from the spec as an unchecked task.
- `forge` with no args launches a Bubble Tea program that shows "forge" and quits on q.

Done when: `make build lint test` passes and `./bin/forge` opens and quits cleanly.
```

#### P0.2 Config

```text
Implement internal/config (spec P0.2).

- Load ~/.config/forge/config.yaml (respect XDG_CONFIG_HOME); create a commented default on first run.
- Projects are zero-config (internal/projects): every git checkout with a GitHub remote
  directly under `project_roots` (default: the parent of the repo forge starts in, plus
  ~/Documents/Projects, ~/code, ~/src, ~/dev that exist), plus every repo forge was started
  in or ran a session in (remembered in the database). The repo comes from the checkout's
  remote, the name from the repo, docs from <path>/.forge/docs.
- Schema: project_roots; projects as optional overrides matched by repo (name, path,
  plane project id, docs dir, worktree_copy; only repo is required),
  github (refresh interval, pr sections: title + search query), plane (base url, workspace slug,
  api key or env var name), sessions (worktree root, default template, notify after minutes),
  artifacts (watch globs), ui (animations: full | reduced | off, theme overrides),
  keybinds overrides, custom commands.
- Validate with clear error messages pointing at the bad key.
- `forge config edit` opens the file in $EDITOR, `forge config path` prints it.
- Unit tests for defaults, validation and env var expansion.

Done when: a fresh machine gets a default config and a broken config shows a readable error.
```

#### P0.3 Store

```text
Implement internal/store with modernc.org/sqlite (spec P0.3).

- DB at ~/.local/share/forge/forge.db (XDG_DATA_HOME aware), WAL mode, busy timeout.
- Embedded numbered SQL migrations with a schema_version table.
- Initial tables: sessions, session_events, links (session<->issue<->pr), pins, kv_cache
  (key, value blob, fetched_at, ttl), usage. Pick columns that cover phases 1-5 of the spec.
- Typed repository functions per table; no SQL outside internal/store.
- Tests using a temp DB.

Done when: migrations run idempotently and repository tests pass.
```

#### P0.4 App shell & theme

```text
Build the app shell (spec P0.4). Layout like gh-dash: tab bar on top, list on the left,
detail pane on the right, status bar at the bottom. Colours from "Look & feel" in the spec:
charcoal background, steel-grey borders, ember orange accent, molten yellow highlights.

- internal/ui/theme.go with all colours and Lip Gloss styles (adaptive light/dark), including
  a heat gradient (dark red -> orange -> yellow -> white) other components can sample.
- Tab interface: Init, Update, View, Title, ShortHelp, Refresh, SetSize, SetProject.
- Root model in internal/app routes keys and WindowSizeMsg; tabs switch with 1-9 and tab/shift+tab.
- Reusable components in internal/ui: Table (selectable rows, column widths, truncation),
  SplitPane (resizable with < and >), StatusBar (left: tab + project, middle: messages,
  right: "updated Ns ago" + "? help"), Toast for errors.
- Placeholder tabs: Home, PRs, Pipelines, Plane, Sessions, Docs, Artifacts, Context.

Done when: all tabs render, resize correctly down to 80x24, and the look matches the theme.
```

#### P0.5 Keymap & help

```text
Centralise keybindings (spec P0.5).

- internal/ui/keymap.go using bubbles/key: global bindings + per-tab bindings.
- vim movement (j/k/g/G, ctrl+d/ctrl+u), / to filter, r to refresh, q / ctrl+c to quit.
- Config overrides from the keybinds section.
- ? opens a full-screen help overlay generated from the bindings, grouped global vs current tab.
- Status bar shows the 4-5 most useful bindings for the current tab.

Done when: remapping a key in config changes both behaviour and help text.
```

#### P0.6 Command palette

```text
Add the command palette (spec P0.6).

- ctrl+k opens a centred overlay with a text input and fuzzy-ranked results (sahilm/fuzzy).
- A registry in internal/app: commands (id, title, keybinding hint, tab, handler) and entity
  providers (each tab can contribute items such as PRs, issues, sessions, docs).
- Results grouped by type with icons; enter runs or jumps to the item; esc closes.
- Recently used commands rank higher (store in kv_cache).
- Register every existing action.

Done when: "quit", "switch tab" and "switch project" work from the palette.
```

#### P0.7 Project switcher

```text
Add the project switcher (spec P0.7).

- ctrl+p opens a fuzzy list of detected projects (most recently used first, with their
  checkout) plus "All projects". Starting forge inside a repo opens its project.
- GitHub tabs never need a local checkout: "All projects" covers every repo gh can see
  (PR searches) or every known repo (runs); actions that need one say how to get it.
- Plane: a repo is linked to the Plane project whose identifier or name matches it
  (case-insensitive, ignoring dashes); otherwise the Plane tab asks once "Which Plane
  project goes with <repo>?" (with "none") and saves the answer in the database.
  The palette command "Plane: link project…" changes it.
- The active project is sent to every tab via SetProject; tabs filter their data by it
  (repo for GitHub, project id for Plane, docs dir for docs, repo for sessions).
- Persist the last active project; show it in the status bar.
- Also available as palette commands.

Done when: switching project changes the status bar and every tab receives the change.
```

#### P0.8 Background refresh

```text
Add background refresh and caching (spec P0.8).

- A scheduler that emits per-source tick messages using intervals from config.
- Cache-first loading: tabs render kv_cache data immediately, then refresh in a tea.Cmd.
- Show a spinner while fetching and "updated Ns ago" when done.
- Track GitHub rate limit from response headers; back off and show remaining calls
  in the status bar when below 10%.
- Pause refresh for a tab when it's not visible, except Home and Sessions.

Done when: restarting forge shows cached data instantly, then refreshes.
```

#### P0.9 Forge animations

```text
Build internal/ui/anim, the forge animation kit (spec P0.9 and "Look & feel"). Make it feel
like a blacksmith's forge at work, but keep every animation short and cheap.

- Engine: one shared tick (tea.Tick at ~12 fps) that only runs while some visible component
  is animating; components are pure functions of (frame, size). Use charmbracelet/harmonica
  for spring motion (pane resize, overlay slide-in).
- Components:
  - Splash: ASCII anvil, a hammer that strikes twice, sparks (random-ish but seeded) flying
    up and fading, the "forge" wordmark heating from dark red to white-hot. Under 1s, any key
    skips, shown only on launch and only if the terminal is at least 60x16.
  - HammerSpinner: drop-in replacement for bubbles/spinner (hammer up / strike / spark frames).
  - HeatBar: progress bar whose filled part samples the theme's heat gradient.
  - SparkBurst: 400ms burst of · * ✦ characters around a point, used for success toasts.
  - StatusFlame: per agent status - flickering flame (working), slow amber pulse (waiting),
    cold grey anvil (idle), cracked red (error), dim ash (dead).
  - EmptyForge: small cold-forge drawing with one glowing ember for empty states.
- Config ui.animations: full | reduced (static icons, no splash, colour pulses only) | off.
  Also honour NO_COLOR and a FORGE_NO_ANIM env var. Off must cost zero ticks.
- A hidden `forge demo anim` command that shows every component side by side.
- Golden-file tests for frames at fixed frame numbers.

Done when: `forge demo anim` looks good, CPU stays near idle when nothing animates, and
setting animations: off removes all motion.
```

From here on, every later prompt should use these components: HammerSpinner for loading, HeatBar for progress, SparkBurst on success, StatusFlame for agents, EmptyForge for empty lists.

### Phase 1 — Agent sessions

#### P1.1 tmux driver

```text
Implement internal/sessions/tmux (spec P1.1 and the "tmux setup" section).

- All calls use a dedicated socket: `tmux -L forge ...`. Never touch the default server.
- Interface + exec implementation: EnsureSession(project), NewWindow(session, name, dir, cmd, env),
  ListWindows, WindowAlive, KillWindow, SendKeys, CapturePane(lines), Attach(target).
- Attach: if $TMUX is set and we're on the forge socket use switch-client; otherwise return an
  *exec.Cmd so the TUI can run it with tea.ExecProcess and resume on detach.
- Use window names as stable ids; escape names safely.
- Fake implementation for tests; integration test guarded by a build tag that needs tmux.

Done when: a test program can create a window running `bash`, capture its output, attach,
detach with Alt+d and land back in forge.
```

#### P1.2 Worktree manager

```text
Implement internal/sessions/worktree (spec P1.2).

- Create: `git worktree add <root>/<repo>/<session> -b agent/<session> <base>` where root
  comes from config. Fetch the base first. Handle an existing branch by asking to reuse it.
- Copy files listed in a per-project `worktree_copy` config (e.g. .env.local) into new worktrees.
- List worktrees and map them to sessions; detect orphans.
- Remove: refuse if there are uncommitted changes unless forced; offer to delete the branch
  if merged.
- Wrap git behind an interface; tests with a temp repo.

Done when: create / list / remove work against a real temp repo in tests.
```

#### P1.3 Start named session

```text
Build the Sessions tab and "new session" flow (spec P1.3).

- `n` (and `forge new` CLI) opens a Huh form: name (slug-validated, unique), project,
  base branch (default main), template (optional), first prompt (multi-line).
- On submit: create the worktree (P1.2), generate a UUID, create a tmux window (P1.1) in the
  worktree running `claude --session-id <uuid>` with env FORGE_SESSION=<name>, then send the
  first prompt. Save the session row (name, uuid, project, branch, worktree, window, status,
  created_at).
- Sessions tab: table with name, project, branch, status, last activity, linked issue/PR;
  detail pane shows a live capture-pane preview (refresh every 2s while visible).
- `x` kills the session (confirm), with an option to remove the worktree.

Done when: I can start a named session from forge, see it in the list and see its output preview.
```

#### P1.4 Live status via hooks

```text
Implement live agent status from Claude Code hooks (spec P1.4).

- `forge hook <event>` reads the hook JSON from stdin (fields include session_id, cwd,
  transcript_path, hook_event_name; check current Claude Code hook docs for exact fields),
  writes a row to session_events, and pings the running TUI over a unix socket at
  ~/.local/share/forge/forge.sock (ignore if no TUI is running). It must exit fast and never fail
  the hook (always exit 0).
- When creating a worktree, write .claude/settings.local.json in it registering forge hooks for
  SessionStart, UserPromptSubmit, PreToolUse, Notification, Stop and SessionEnd. Don't touch
  global settings. Make sure .claude/settings.local.json is git-ignored.
- Status machine: UserPromptSubmit/PreToolUse -> working, Notification -> waiting,
  Stop -> idle, SessionEnd or tmux window gone -> dead, hook error -> error.
- TUI listens on the socket and updates rows immediately; coloured status dots
  (green working, yellow waiting, grey idle, red error, dim dead). Waiting sessions sort first.

Done when: a real session flips between working / waiting / idle in forge within a second.
```

#### P1.5 Attach & resume

```text
Implement attach and resume (spec P1.5).

- enter on a session: if its tmux window is alive, attach (P1.1). If dead, recreate the window
  in the session's worktree running `claude --resume <uuid>` and attach.
- If the worktree is gone, offer to recreate it from the branch.
- `b` resumes without attaching (background resume). (`R` stays the global refresh-all; tab keys
  may reuse another tab's keys, never a global one.)
- After detaching, refresh that session's status and preview.
- Palette command "Go to session..." (also ctrl+g) with fuzzy search by name, issue or PR;
  picking a session attaches to it, resuming it first if needed.
- No transcript for the uuid (the session never got a message): start `claude --session-id <uuid>`
  again, offering to re-send the first prompt if its file still exists. If claude refuses the
  uuid as already in use, give the session a new one (links stay).
- Every resume (re)installs forge's hooks in the worktree.

Done when: after rebooting, I can pick a session and land back in the same Claude conversation.
```

#### P1.6 Transcript viewer

```text
Add a transcript viewer (spec P1.6).

- Locate the JSONL transcript for a session (use transcript_path from hook events; fall back to
  scanning ~/.claude/projects/). Inspect real files on this machine to learn the format;
  parse defensively and skip unknown line types.
- `t` on a session opens a full-screen viewport: user/assistant messages (assistant text
  rendered with Glamour), tool calls collapsed to one line, expandable with enter.
- Summary header: message count, files touched (from Edit/Write tool calls), last activity,
  estimated tokens (from usage fields if present, else chars/4).
- Incremental parsing: remember byte offset per file so re-opening is fast.

Done when: I can read what an agent did without attaching to it.
```

#### P1.7 Session templates

```text
Add session templates (spec P1.7).

- Templates are markdown files in ~/.config/forge/templates/ with YAML front matter
  (name, description, variables).
- Variables: {repo}, {branch}, {pr}, {pr_title}, {pr_body}, {issue}, {issue_title},
  {issue_body}, {failing_log}, {docs}. Unknown variables prompt the user in the form.
- Ship defaults: implement-issue, fix-ci, review-pr, refactor, write-tests, explore.
- Template picker in the new-session form with preview of the rendered prompt.
- Palette: "Edit templates" opens the folder in $EDITOR.

Done when: choosing "implement-issue" fills the first prompt with the issue's details.
```

#### P1.8 Waiting notifications

```text
Add desktop notifications for waiting agents (spec P1.8).

- internal/notify wrapping gen2brain/beeep, falling back to notify-send / osascript.
- When a session has been in "waiting" longer than sessions.notify_after_minutes, notify once
  ("<name> is waiting for you"). Reset when status changes.
- Also notify on error and when a session finishes (Stop) if forge isn't focused (config toggle).
- Notifications work even when the TUI isn't running: `forge hook` handles the Notification
  event itself with a debounce stored in the DB.
- Show a count of waiting sessions in the status bar on every tab.

Done when: leaving an agent waiting produces exactly one notification.
```

#### P1.9 Session <-> issue/PR links

```text
Link sessions to Plane issues and PRs (spec P1.9). Build the data model now; the PR and
Plane tabs will call into it later.

- links table rows: session_id, kind (plane_issue | github_pr), external id, url.
- Exported helper: StartSessionFrom(ctx Source) that pre-fills the new-session form
  (name from issue key or PR number, branch, template, variables).
- When a PR is opened from a session's branch, auto-link it (match head branch on refresh).
- Session detail pane shows linked issue and PR with status; `o` opens the linked PR (the
  issue if there is no PR), `O` opens the issue, with a small picker when there is more than
  one link of that kind. Enter stays attach.
- Expose a LinkedSessions(kind, id) query for other tabs.

Done when: a session started for an issue shows that issue, and later its PR, in the detail pane.
```

#### P1.10 Auto-archive

```text
Add session archiving (spec P1.10).

- On refresh, sessions whose linked PR is merged or closed are marked archived.
- Before archiving, generate a short summary from the transcript (last assistant messages,
  files touched) and store it; keep user notes.
- Offer to remove the worktree and tmux window (config: ask | always | never).
- Sessions tab toggles active / archived with `a`; archived sessions are searchable from the palette.
- Manual archive with `A`.

Done when: merging a PR moves its session to the archive with a summary.
```

### Phase 2 — GitHub

#### P2.1 PR list & sections

```text
Build the PRs tab (spec P2.1). Match the gh-dash screenshots.

- internal/github using cli/go-gh/v2 (reuses `gh auth`). One GraphQL search query per section,
  fetching: repo, number, title, author, isDraft, reviewDecision, statusCheckRollup state,
  additions, deletions, updatedAt, createdAt, headRefName, baseRefName, url, mergeable.
- Sections from config (defaults: "My PRs" author:@me, "Review requested"
  review-requested:@me, "Involved" involves:@me), shown as sub-tabs with counts.
- Columns: repo + title (two lines), review state, CI state, +/- lines, updated, created.
- `/` filters the list; `f` picks a repo filter; active project (P0.7) applies automatically.
- Cache results per section in kv_cache.

Done when: the tab looks like the screenshot and filtering by repo works.
```

#### P2.2 PR detail pane

```text
Add the PR detail pane (spec P2.2).

- Right pane with header (repo #number, title, state pill, base <- head, author, age) and
  three tabs: Overview (body rendered with Glamour, labels, reviewers, approval and
  conflict status cards like the screenshot), Checks (each check with status and duration),
  Activity (comments and reviews, newest first).
- Fetch details lazily on selection with a short debounce; cache by PR + updatedAt.
- Show linked forge sessions (P1.9) in Overview.

Done when: moving through the list updates the pane without lag.
```

#### P2.3 PR quick actions

```text
Add PR quick actions (spec P2.3), each also a palette command.

- o open in browser, y copy URL, c check out locally (in the project's path, or create a
  worktree if the main checkout is dirty), u update branch, m merge (choose method, confirm),
  M enable auto-merge, d toggle draft, s start a forge session for this PR (P1.9).
- Long-running actions show a spinner in the status bar and a toast on success/failure.
- Refresh the row after the action.

Done when: every action works on a real PR and errors show as readable toasts.
```

#### P2.4 Review queue & diff

```text
Add reviewing from forge (spec P2.4).

- `v` opens a full-screen diff viewer for the selected PR: fetch the diff, pipe through `delta`
  (with --paging=never and the terminal width) if installed, otherwise colourise with Lip Gloss.
- File list on the left (n/p to jump between files), diff viewport on the right.
- Actions: a approve, x request changes, C comment; each opens a text area (or $EDITOR) for
  the body, then submits the review.
- "Review requested" section badge shows the count on the Home screen later.

Done when: I can read a diff and approve a PR without leaving forge.
```

#### P2.5 Stale PR markers

```text
Add stale markers (spec P2.5).

- github.stale_after_days in config (default 5).
- PRs not updated within that time get a dim clock icon and the updated column turns amber;
  over twice the threshold turns red.
- A built-in filter "stale only" (key S) and a palette command.

Done when: old PRs are visibly marked and filterable.
```

#### P2.6 Pipelines list

```text
Build the Pipelines tab (spec P2.6).

- REST: list workflow runs for each repo in the active project (or all), with filters for
  branch, workflow, status, and "mine" (actor = me).
- Columns: status icon, workflow name, branch, commit message (truncated), trigger event,
  actor, duration, started.
- Detail pane: jobs and steps with status and duration.
- Auto-refresh faster (every 15s) while any visible run is in progress.
- From a PR row, `p` jumps to its runs.

Done when: I can see live progress of a running pipeline.
```

#### P2.7 Log tail & rerun

```text
Add logs and reruns (spec P2.7).

- `l` on a failed run downloads its failed jobs' logs and opens a viewport collapsed to error context: show
  lines matching error patterns (error, failed, panic, FAIL, exit code, ##[error]) with 5 lines
  around each; `e` toggles the full log; `/` searches (`n` / `N` jump between matches);
  `]` / `[` move between the run's failed jobs.
- Strip ANSI and GitHub timestamps for readability.
- `t` ("retry") reruns failed jobs, in the run list and the log viewer (`R` is global refresh-all, so
  only the modal log viewer also takes `R`); `ctrl+r` reruns the whole run, `X` cancels a running run.
- Save the extracted error summary in kv_cache for P2.8.

Done when: I can find why a job failed in a few seconds and rerun it.
```

#### P2.8 "Fix this" from CI

```text
Add "fix this" (spec P2.8).

- `F` on a failed run or failing PR check: find the PR and its branch, reuse a linked session
  if one exists (send the new prompt into it), otherwise start a new session from the fix-ci
  template (P1.7) on that branch.
- The prompt includes the workflow, job, the extracted error summary from P2.7, and the
  last commit message.
- Link the session to the PR.

Done when: one key on a red check starts an agent with the failure context.
```

### Phase 3 — Plane

#### P3.1 Plane client

```text
Implement internal/plane (spec P3.1).

- Read the current Plane REST API docs first (issues were renamed "work items"; check which
  endpoints the configured instance supports). Base URL from config so self-hosted works;
  auth header X-API-Key from config or an env var.
- Typed client: projects, states, labels, members, cycles, work items (list with filters and
  pagination, get, create, update), comments.
- Respect rate limits (back off on 429) and cache list responses in kv_cache.
- Table-driven tests with recorded JSON fixtures and an httptest server.

Done when: a small debug command `forge plane ls` lists items for a project.
```

#### P3.2 Issue list view

```text
Build the Plane tab list view (spec P3.2).

- Table: key (PROJ-123), title, state (coloured by state group), priority icon, labels,
  assignee, updated.
- Filters: project (follows the active project), state group, label, assignee (default: me),
  cycle. `f` opens a filter picker; `/` text filter.
- Detail pane: description rendered from HTML/markdown, comments, linked forge sessions and PRs.
- Palette provider for issues (search by key or title).

Done when: I can browse and filter my Plane issues per project.
```

#### P3.3 Kanban board

```text
Add a kanban view to the Plane tab (spec P3.3).

- `b` toggles list / board. Columns = states in order, grouped by state group; column header
  shows count. Cards show key, title (wrapped to 2 lines), priority, assignee initials.
- h/l move between columns, j/k between cards, H/L move the selected card to the previous/next
  state (optimistic update, roll back with a toast on failure).
- Columns scroll horizontally when they don't fit; collapse empty columns with `z`.

Done when: I can drag work across the board with the keyboard and Plane reflects it.
```

#### P3.4 Create & edit

```text
Add create and edit for Plane work items (spec P3.4).

- `n` new, `e` edit: Huh form with title, state, priority, labels (multi-select), assignee,
  cycle; description opens in $EDITOR as markdown and is converted for Plane.
- `c` adds a comment, `s` starts a forge session for the item (P1.9, implement-issue template).
- Validate required fields; show the created key in a toast and select the new item.

Done when: I can create, edit and comment on items without opening Plane.
```

#### P3.5 Quick capture

```text
Add quick capture (spec P3.5).

- Global key (default ctrl+n) from any tab opens a one-line overlay: title with inline tokens
  `#project`, `!high`, `@me`, `+label` and completion for each.
- enter creates the item in the background; shift+enter opens the full form instead.
- `forge capture "title #proj !high"` works from any shell; with no args it shows the same
  one-line input as a tiny Bubble Tea program (used by the tmux Alt+c popup).

Done when: I can capture an issue in under 3 seconds from anywhere in tmux.
```

#### P3.6 Cycle summary

```text
Add a cycle summary header (spec P3.6).

- For the active project's current cycle: name, days left, and counts by state group
  (done / in progress / blocked / todo) with a small progress bar.
- Blocked = a label or state configured as blocked in config.
- Clicking or pressing `C` filters the list to the current cycle.

Done when: the header shows accurate cycle progress.
```

#### P3.7 Branch naming

```text
Add branch naming and back-links (spec P3.7).

- Helper BranchFor(item) -> `PROJ-123-short-title` (lowercase, ascii, max 50 chars).
- Sessions started from an issue use that branch (instead of agent/<name>).
- On PR refresh, detect the issue key in branch name or title and link the PR to the item
  (links table), shown in both detail panes.
- Optional (config): post a comment on the Plane item with the PR link when first detected.

Done when: a PR made from an issue session shows up on that issue in forge.
```

### Phase 4 — Docs & design artifacts

#### P4.1 Docs browser

```text
Build the Docs tab (spec P4.1).

- Tree of markdown files under each project's docs dir (default <repo>/.forge/docs/), folders
  collapsible; "All projects" shows a top level per project.
- Detail pane renders the selected doc with Glamour at the pane width; scroll with j/k.
- e opens in $EDITOR (tea.ExecProcess) and re-renders on return; n new doc (name + optional
  template: spec, notes, runbook); r rename; D delete (confirm, moves to .trash).
- Watch the folder with fsnotify so edits made by agents appear live.

Done when: I can browse, read and edit docs per project.
```

#### P4.2 Docs search

```text
Add docs search (spec P4.2).

- `/` in the Docs tab searches titles and content across all projects (or the active one).
- Simple in-memory index rebuilt on fsnotify changes; ranking: title match > heading match >
  body match; show a one-line snippet with the match highlighted.
- Docs are also a palette provider.

Done when: typing a word finds the doc and jumps to the matching heading.
```

#### P4.3 Send docs to agent

```text
Add "send to agent" (spec P4.3).

- space multi-selects docs; `s` (like "start session" on PRs and Plane issues; `>` stays
  "widen list") opens a picker: new session or an existing one.
- New session: the {docs} template variable becomes `@path` references (Claude Code file
  mentions) for each selected doc, relative to the worktree; copy docs into the worktree if
  they live outside it.
- Existing session: send a short message via tmux send-keys referencing the docs.

Done when: selected docs show up in the agent's context.
```

#### P4.4 Decision log

```text
Add a decision log (spec P4.4).

- `forge adr` CLI and a palette command open a Huh form: title, context, decision,
  alternatives, consequences.
- Saved as .forge/docs/decisions/NNNN-slug.md with front matter (date, status: accepted).
- Docs tab has a "Decisions" view sorted newest first; status can be changed to superseded with
  a link to the newer one.
- Add a line to the project's CLAUDE.md (once) telling agents to read docs/decisions.

Done when: I can log a decision in under a minute and agents are pointed at it.
```

#### P4.5 Artifact gallery

```text
Build the Artifacts tab (spec P4.5).

- Watch globs from config (defaults: each worktree's and repo's **/*.html, **/*.svg, **/*.png,
  design/**, .forge/artifacts/**), ignoring node_modules, .git, build output.
- Index into the DB: path, project, session (if inside a session worktree), type, size,
  created/modified.
- List grouped by project, newest first, with type icon and source session.
- `o` opens with xdg-open/open; `y` copies the path; `c` copies the artifact into
  .forge/artifacts/ so it survives worktree removal.

Done when: an HTML mockup written by an agent appears in the gallery within seconds.
```

#### P4.6 Pin & tag artifacts

```text
Add pinning and tagging (spec P4.6).

- `p` pins (pinned float to the top, star icon); `t` edits tags (comma separated, with
  completion from existing tags); `m` renames the display title (`r` is the global refresh).
- Filter by tag and pinned-only. Pinned artifacts are copied to .forge/artifacts/ automatically.
- Pins show on the Home screen later.

Done when: pins and tags persist across restarts and filter correctly.
```

#### P4.7 Inline previews

```text
Add previews in the artifact detail pane (spec P4.7).

- Detect terminal graphics support: kitty graphics protocol, then sixel, else fall back to
  `chafa` if installed, else a metadata card.
- PNG/JPG: render scaled to the pane. SVG: rasterise with `rsvg-convert` if installed.
  HTML: show title, size and the first lines of visible text, plus "press o to open".
- Clear images properly on selection change and resize (no ghost images).

Done when: images preview cleanly in kitty/WezTerm/Ghostty and degrade gracefully elsewhere.
```

### Phase 5 — Context & cache

#### P5.1 CLAUDE.md manager

```text
Build the Context tab's CLAUDE.md manager (spec P5.1).

- For the active project list every memory file Claude Code would load: ~/.claude/CLAUDE.md,
  <repo>/CLAUDE.md, CLAUDE.local.md, nested CLAUDE.md files in subfolders, and files they
  import with @path. Check current Claude Code docs for the exact lookup rules.
- Side-by-side: list on the left with an estimated token count each (chars/4) and a total;
  rendered content on the right. e opens in $EDITOR.
- Warn (amber) when the combined size passes a configurable budget.
- Show which worktrees inherit which files.

Done when: I can see and edit all the memory an agent will start with, and its rough size.
```

#### P5.2 Context packs

```text
Add context packs (spec P5.2).

- A pack = YAML in ~/.config/forge/packs/<name>.yaml: description, files (globs, relative to
  the repo), docs, and an optional preamble prompt.
- Context tab "Packs" view: list, create (pick files with a fuzzy file picker), edit, delete,
  with the total token estimate.
- The new-session form gets a multi-select of packs; selected packs become `@path` references
  plus the preamble in the first prompt. Also "attach pack" for a running session.

Done when: starting a session with the "auth" pack gives the agent those files up front.
```

#### P5.3 Cache controls

```text
Add cache controls (spec P5.3).

- Context tab "Cache" view: rows per cache type (GitHub API, Plane API, transcript index,
  docs index, artifact copies, worktrees, archived sessions) with entries, size, oldest entry.
- Break down by project with enter.
- Actions: clear selected type, clear for project, prune older than N days, remove orphaned
  worktrees (from P1.2), vacuum the DB. Everything destructive asks for confirmation.

Done when: I can see what forge stores and reclaim space safely.
```

#### P5.4 Usage view

```text
Add a usage view (spec P5.4).

- Aggregate token usage from transcript usage fields (input, output, cache read, cache
  write) per session, day and project; store daily rollups in the usage table.
- Cost estimate from a configurable per-model price table (don't hard-code prices; ship
  placeholders the user fills in).
- Views: last 14 days as a small bar chart (block characters), table per project, top sessions.
- Session detail pane shows its own totals.

Done when: I can see which sessions and projects use the most tokens.
```

### Phase 6 — Workflow glue

#### P6.1 Home screen

```text
Build the Home tab (spec P6.1). It becomes the default tab.

- One prioritised "needs me" list, each row with a source icon and one-key action:
  1. agents waiting for input (enter attaches)
  2. agents errored
  3. review requests (enter opens the diff)
  4. my PRs with failing checks (F fixes)
  5. my PRs approved and green (m merges)
  6. Plane items in the current cycle assigned to me, in progress first
- Side panel: running agents with status, pinned artifacts, today's scratchpad.
- Empty state uses the cold-forge animation from P0.9.

Done when: opening forge tells me exactly what to do next.
```

#### P6.2 Scratchpad & daily note

```text
Add the scratchpad and daily note (spec P6.2).

- `ctrl+s` from anywhere opens today's note (~/.local/share/forge/notes/YYYY-MM-DD.md) in a
  pane with a simple text area; `E` opens it in $EDITOR.
- "Generate standup" command appends a "Done yesterday / today" section built from merged PRs,
  reviewed PRs, closed Plane items and finished sessions (with their summaries).
- Browse previous days with [ and ].

Done when: one command produces a standup summary I can paste.
```

#### P6.3 Custom commands

```text
Add custom commands (spec P6.3).

- Config: list of {name, key, tabs, command, mode: background | exec | tmux-window}.
- Variables from the selection: {repo}, {pr}, {branch}, {worktree}, {issue}, {session}, {path}.
- background: run, show a toast with exit status (output viewable); exec: suspend the TUI and
  run interactively; tmux-window: open in a new window on the forge socket.
- Registered as palette commands and in the help overlay.

Done when: a custom "open in editor" command works on a session's worktree.
```

#### P6.4 Polish & packaging

```text
Polish and package forge (spec P6.4).

- Audit every tab for: loading states (forge spinners from P0.9), empty states, error toasts
  with retry, small-terminal behaviour at 80x24.
- `forge doctor`: checks gh auth, tmux version, git, claude, delta, chafa, Plane connectivity,
  hook registration, and prints fixes.
- goreleaser config for Linux and macOS, an install script, a README with a GIF made with
  charmbracelet/vhs.

Done when: a fresh machine can install forge and `forge doctor` comes back green.
```
