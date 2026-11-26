# forge keybindings: inventory and redesign (P6.8)

**Status: approved with changes (review of PR #54), final look pending.** No binding has changed
yet. P6.9 implements what's agreed here (the presets, the new defaults, the platform aliases and a
Keys view).

forge was built in phases, and keys were often picked because the obvious one was already taken.
There has been no public release, so no one depends on the current keys. This proposal optimises
for one thing only: a new user being productive straight away.

- Part 1 is the **current inventory**, generated from the keymap by `forge keys --dump`.
- Parts 2–3 cover the **problems** and the **design principles**.
- Parts 4–5 are the **proposed default preset** (every binding, current → proposed, with a reason)
  and a **vim preset**.
- Part 6 covers **platforms**: Alt / Option, macOS Cmd, agent windows, and a compatibility table.
- Parts 7–8 cover **discoverability** and the **preset system** for P6.9.

## Decisions

Settled in review. The "Considered" column records what was rejected.

| # | Question | Decided | Considered |
| --- | --- | --- | --- |
| D1 | What do `tab` / `shift+tab` do? | Next / previous **tab**, as now (browser and terminal habit, symmetric). Views within a tab move to `v` / `V`. | Claude Code's habit: `shift+tab` cycles the tab's views, and tabs use only `1-9` plus `tab`. That's asymmetric, and on tabs without views `shift+tab` would do nothing. |
| D2 | How do you get back to forge from an agent window? | `ctrl+q`, with `ctrl+]` as an alias. | `ctrl+]` only, which needs AltGr / Option on German and Nordic layouts. |
| D3 | Fix with edited prompt (`alt+f` today) | `F` opens the **fix sheet**, which shows a preview of the prompt. `F` again or `enter` sends; `e` edits the prompt inline (a text area, where `ctrl+e` opens `$EDITOR` as in other forms); `E` opens `$EDITOR` directly (the e / E rule in part 3); `esc` cancels. A quick fix is `F F`. | `F` sends straight away and `ctrl+f` edits first. `ctrl+f` is also "find" in VS Code and browsers. |
| D4 | Rerun failed jobs (`t` today) | `ctrl+r`: "run it again", a stronger `r`. | A punctuation key such as vim's `.` "repeat": harder to discover, and `.` is now the next-chunk alias. |
| D5 | Overload `m` = merge (PRs, Home) and `m` = move / rename (Docs, Artifacts)? | Yes. Both are the obvious mnemonic, they never share a tab, and merge always confirms. | Rename on `R`, which would mean moving refresh-all off `R`. |
| D6 | Alt+c capture in agent windows | Dropped. `ctrl+q` then `ctrl+n` does the same with nothing new to learn. | Bind it to `ctrl+^` (that's `ctrl+shift+6` on US layouts, which is awkward). |
| D7 | Two-key chords (`gg`, `dd`, `gt`) | Only in the **vim** preset. The default stays single-key. | No chords at all, so the vim preset only adds `:` and a few aliases. |

## 1. Current inventory

Generated with `go run ./cmd/forge keys --dump --defaults` (headings are one level deeper here). The
same command with `--format json` gives the machine-readable list, and without `--defaults` it shows
your config's overrides and custom commands. The golden test
(`internal/app/testdata/TestKeyInventoryMarkdown.golden`) fails whenever a binding changes, so this
section can't silently drift: regenerate it from the golden file.

**Kinds.**
- *global*: live on every tab.
- *tab*: live on one tab, together with the global keys.
- *overlay*: a full-screen viewer or pane. Only its own keys and force quit are live.
- *modal*: a focused input, picker or form.
- *program*: a separate full-screen program.
- *agent-window*: forge's tmux.conf.
- *(fixed)*: not changeable in `keybinds:`. These are Huh's form keys, the tmux.conf keys (you edit
  that file instead) and the hidden demos.


### Global (every tab)

| Keys | Action id | Does | Palette command |
| --- | --- | --- | --- |
| `q` | `quit` | quit | Quit forge |
| `ctrl+c` | `force_quit` | force quit | — |
| `?` | `help` | help | Show keys |
| `tab` | `next_tab` | next tab | Next tab |
| `shift+tab` | `prev_tab` | prev tab | Previous tab |
| `1` `2` `3` `4` `5` `6` `7` `8` `9` | `goto_tab` | go to tab | Tabs: <tab name> |
| `r` | `refresh` | refresh | Refresh tab |
| `R` | `refresh_all` | refresh all | Refresh all tabs |
| `ctrl+k` | `palette` | commands | — |
| `ctrl+p` | `projects` | switch project | Switch project… |
| `ctrl+g` | `goto_session` | go to session | Go to session… |
| `ctrl+n` | `capture` | quick capture | Quick capture issue… |
| `ctrl+s` | `note` | daily note | Daily note |
| `k` `up` | `up` | up | — |
| `j` `down` | `down` | down | — |
| `g` `home` | `top` | top | — |
| `G` `end` | `bottom` | bottom | — |
| `ctrl+d` | `page_down` | half page down | — |
| `ctrl+u` | `page_up` | half page up | — |
| `/` | `filter` | filter | Filter PRs |
| `>` | `grow_pane` | widen list | Widen list |
| `<` | `shrink_pane` | narrow list | Narrow list |

### Tabs

#### sessions

| Keys | Action id | Does | Palette command |
| --- | --- | --- | --- |
| `n` | `session_new` | new session | New session… |
| `x` | `session_kill` | kill session | Kill session… |
| `D` | `session_delete` | delete session | Delete archived session… |
| `a` | `session_archive_view` | archived view | Show archived sessions |
| `A` | `session_archive` | archive / restore | Archive session |
| `enter` | `session_attach` | attach | Attach session (resume if needed) |
| `b` | `session_resume` | background resume | Resume session in the background |
| `t` | `session_transcript` | transcript | Open session transcript |
| `o` | `session_open_pr` | open linked PR | Open linked PR in browser |
| `O` | `session_open_issue` | open linked issue | Open linked issue in browser |
| `P` | `session_attach_pack` | attach context pack | Attach context pack to session… |

#### prs

| Keys | Action id | Does | Palette command |
| --- | --- | --- | --- |
| `l` `right` | `pr_next_section` | next section | PRs: next section |
| `h` `left` | `pr_prev_section` | prev section | PRs: previous section |
| `f` | `pr_repo_filter` | filter by repo | PRs: filter by repo… |
| `S` | `pr_stale_only` | stale only | PRs: show stale only |
| `]` | `pr_detail_next` | next detail tab | PRs: next detail tab |
| `[` | `pr_detail_prev` | prev detail tab | PRs: previous detail tab |
| `J` | `pr_detail_down` | scroll detail down | PRs: scroll detail down |
| `K` | `pr_detail_up` | scroll detail up | PRs: scroll detail up |
| `o` | `pr_open` | open in browser | PR: open in browser |
| `y` | `pr_copy_url` | copy URL | PR: copy URL |
| `c` | `pr_checkout` | check out | PR: check out locally |
| `u` | `pr_update_branch` | update branch | PR: update branch |
| `m` | `pr_merge` | merge | PR: merge… |
| `M` | `pr_auto_merge` | auto-merge | PR: enable auto-merge… |
| `d` | `pr_toggle_draft` | toggle draft | PR: toggle draft / ready |
| `s` | `pr_session` | start session | PR: start a forge session |
| `v` | `pr_diff` | review diff | PR: review diff |
| `p` | `pr_runs` | pipeline runs | PR: show pipeline runs |
| `F` | `pr_fix` | fix failing checks | CI: fix this (failing checks) |
| `alt+f` | `pr_fix_edit` | fix (edit prompt) | CI: fix this (edit prompt)… |

#### home

| Keys | Action id | Does | Palette command |
| --- | --- | --- | --- |
| `enter` | `home_open` | do it | Home: do it (attach / open diff / show item) |
| `F` | `home_fix` | fix CI | Home: fix failing CI of the selected PR |
| `m` | `home_merge` | merge | Home: merge the selected PR (asks first) |
| `o` | `home_browse` | open in browser | Home: open in browser |
| `a` | `home_accept` | accept proposal | Home: accept the proposed issue (create it in Plane) |
| `e` | `home_edit` | edit proposal | Home: edit the proposed issue |
| `x` | `home_drop` | drop proposal | Home: drop the proposed issue (asks first) |

#### pipelines

| Keys | Action id | Does | Palette command |
| --- | --- | --- | --- |
| `f` | `pipeline_filters` | filters | Pipelines: filters… |
| `m` | `pipeline_mine` | mine only | Pipelines: show only mine |
| `x` | `pipeline_clear_filters` | clear filters | Pipelines: clear filters |
| `J` | `pipeline_detail_down` | scroll detail down | Pipelines: scroll detail down |
| `K` | `pipeline_detail_up` | scroll detail up | Pipelines: scroll detail up |
| `o` | `pipeline_open` | open in browser | Run: open in browser |
| `l` | `pipeline_logs` | job logs | Run: view failed job logs |
| `t` | `pipeline_rerun_failed` | rerun failed jobs | Run: rerun failed jobs |
| `ctrl+r` | `pipeline_rerun` | rerun whole run | Run: rerun whole run… |
| `X` | `pipeline_cancel` | cancel run | Run: cancel… |
| `F` | `pipeline_fix` | fix this | CI: fix this |
| `alt+f` | `pipeline_fix_edit` | fix (edit prompt) | CI: fix this (edit prompt)… |

#### plane

| Keys | Action id | Does | Palette command |
| --- | --- | --- | --- |
| `f` | `plane_filters` | filters | Plane: filters… |
| `m` | `plane_mine` | mine only | Plane: show everyone's |
| `x` | `plane_clear_filters` | clear filters | Plane: clear filters |
| `J` | `plane_detail_down` | scroll detail down | Plane: scroll detail down |
| `K` | `plane_detail_up` | scroll detail up | Plane: scroll detail up |
| `o` | `plane_open` | open in browser | Issue: open in browser |
| `y` | `plane_copy_url` | copy URL | Issue: copy URL |
| `b` | `plane_board` | board / list | Plane: show board |
| `h` `left` | `plane_col_left` | prev column | Board: previous column |
| `l` `right` | `plane_col_right` | next column | Board: next column |
| `H` | `plane_move_prev` | move to prev state | Issue: move to previous state |
| `L` | `plane_move_next` | move to next state | Issue: move to next state |
| `z` | `plane_collapse_column` | collapse column | Board: collapse / expand column |
| `Z` | `plane_collapse_empty` | collapse empty columns | Board: collapse / expand empty columns |
| `enter` | `plane_open_card` | open card | Board: open card |
| `n` | `plane_new` | new issue | Issue: new… |
| `e` | `plane_edit` | edit | Issue: edit… |
| `c` | `plane_comment` | comment | Issue: comment… |
| `s` | `plane_session` | start session | Issue: start a forge session |
| `C` | `plane_cycle` | current cycle | Plane: current cycle only |

#### docs

| Keys | Action id | Does | Palette command |
| --- | --- | --- | --- |
| `n` | `docs_new` | new doc | Docs: new doc… |
| `e` | `docs_edit` | edit in $EDITOR | Docs: edit in $EDITOR |
| `m` | `docs_rename` | rename / move | Docs: rename / move… |
| `D` | `docs_delete` | delete (to .trash) | Docs: delete (move to .trash)… |
| `enter` `l` `right` | `docs_open` | open / expand | Docs: open doc / expand folder |
| `h` `left` | `docs_back` | back / collapse | Docs: back to tree / collapse folder |
| `J` | `docs_detail_down` | scroll detail down | Docs: scroll doc down |
| `K` | `docs_detail_up` | scroll detail up | Docs: scroll doc up |
| `space` | `docs_mark` | mark for agent | Docs: mark / unmark for agent |
| `s` | `docs_send` | send to agent | Docs: send to agent (new or running session)… |
| `a` | `docs_decision` | log decision | Docs: log a decision (ADR)… |
| `d` | `docs_decisions` | decisions view | Docs: toggle decisions view |
| `S` | `docs_supersede` | mark superseded | Docs: mark decision superseded… |

#### artifacts

| Keys | Action id | Does | Palette command |
| --- | --- | --- | --- |
| `o` | `artifacts_open` | open | Artifacts: open in default app / browser |
| `y` | `artifacts_copy_path` | copy path | Artifacts: copy path |
| `c` | `artifacts_keep` | keep a copy | Artifacts: keep a copy in .forge/artifacts |
| `p` | `artifacts_pin` | pin / unpin | Artifacts: pin / unpin |
| `t` | `artifacts_tags` | edit tags | Artifacts: edit tags |
| `m` | `artifacts_rename` | rename | Artifacts: rename (display title) |
| `f` | `artifacts_filters` | filters | Artifacts: filter by tag / pinned |
| `P` | `artifacts_pinned_only` | pinned only | Artifacts: toggle pinned only |
| `x` | `artifacts_clear_filters` | clear filters | Artifacts: clear filters |

#### context

| Keys | Action id | Does | Palette command |
| --- | --- | --- | --- |
| `e` | `context_edit` | edit in $EDITOR | Context: edit memory file / pack / command override in $EDITOR |
| `i` | `context_worktrees` | worktree inheritance | Context: toggle worktree inheritance |
| `enter` `l` `right` | `context_open` | focus file | Context: focus the rendered file |
| `h` `left` | `context_back` | back to list | Context: back to the file list |
| `J` | `context_detail_down` | scroll detail down | Context: scroll file down |
| `K` | `context_detail_up` | scroll detail up | Context: scroll file up |
| `p` | `context_packs` | memory / packs | Context: switch memory files / packs |
| `n` | `context_new_pack` | new pack | Context: new context pack |
| `a` | `context_add_files` | pick files | Context: pick the pack's files |
| `D` | `context_delete_pack` | delete pack | Context: delete context pack |
| `s` | `context_send_pack` | send pack to session | Context: send pack to a session |
| `c` | `context_cache` | memory / cache | Context: switch memory files / cache |
| `x` | `context_cache_clear` | clear | Context: clear the selected cache |
| `P` | `context_cache_prune` | prune older than… | Context: prune cache entries older than N days |
| `o` | `context_cache_orphans` | remove orphaned worktrees | Context: remove orphaned worktrees |
| `V` | `context_cache_vacuum` | vacuum database | Context: vacuum the database |
| `u` | `context_usage` | memory / usage | Context: switch memory files / token usage |
| `m` | `context_commands` | memory / commands | Context: switch memory files / slash commands |
| `X` | `context_commands_reset` | reset to built-in | Context: reset the slash command to its built-in |
| `S` | `context_commands_sync` | re-sync sessions | Context: re-sync slash commands into all running sessions |

### Overlays (full-screen viewers and panes)

#### transcript

| Keys | Action id | Does | Palette command |
| --- | --- | --- | --- |
| `k` `up` | `transcript_up` | prev entry | — |
| `j` `down` | `transcript_down` | next entry | — |
| `ctrl+u` `pgup` | `transcript_page_up` | page up | — |
| `ctrl+d` `pgdown` | `transcript_page_down` | page down | — |
| `g` `home` | `transcript_top` | first entry | — |
| `G` `end` | `transcript_bottom` | last entry | — |
| `enter` | `transcript_toggle` | expand tool call | Transcript: expand / collapse tool call |
| `e` | `transcript_toggle_all` | expand all | Transcript: expand / collapse all tool calls |
| `f` | `transcript_follow` | follow | Transcript: follow new entries |
| `esc` `q` | `transcript_close` | close | Transcript: close |

#### diff

| Keys | Action id | Does | Palette command |
| --- | --- | --- | --- |
| `k` `up` | `diff_up` | scroll up | — |
| `j` `down` | `diff_down` | scroll down | — |
| `ctrl+u` `pgup` | `diff_page_up` | page up | — |
| `ctrl+d` `pgdown` `space` | `diff_page_down` | page down | — |
| `g` `home` | `diff_top` | top | — |
| `G` `end` | `diff_bottom` | bottom | — |
| `n` | `diff_next_file` | next file | Diff: next file |
| `p` | `diff_prev_file` | prev file | Diff: previous file |
| `a` | `diff_approve` | approve | PR: approve… |
| `x` | `diff_request_changes` | request changes | PR: request changes… |
| `C` | `diff_comment` | comment | PR: comment… |
| `o` | `diff_open` | open in browser | PR: open in browser |
| `esc` `q` | `diff_close` | close | Diff: close |

#### log

| Keys | Action id | Does | Palette command |
| --- | --- | --- | --- |
| `k` `up` | `log_up` | scroll up | — |
| `j` `down` | `log_down` | scroll down | — |
| `ctrl+u` `pgup` | `log_page_up` | page up | — |
| `ctrl+d` `pgdown` `space` | `log_page_down` | page down | — |
| `g` `home` | `log_top` | top | — |
| `G` `end` | `log_bottom` | bottom | — |
| `e` | `log_toggle_full` | full log / errors | Log: full log / errors only |
| `/` | `log_search` | search | Log: search |
| `n` | `log_next_match` | next match | Log: next match |
| `N` | `log_prev_match` | prev match | Log: previous match |
| `]` | `log_next_job` | next failed job | Log: next failed job |
| `[` | `log_prev_job` | prev failed job | Log: previous failed job |
| `t` `R` | `log_rerun_failed` | rerun failed jobs | Run: rerun failed jobs |
| `ctrl+r` | `log_rerun` | rerun whole run | Run: rerun whole run… |
| `X` | `log_cancel` | cancel run | Run: cancel… |
| `F` | `log_fix` | fix this | CI: fix this |
| `alt+f` | `log_fix_edit` | fix (edit prompt) | CI: fix this (edit prompt)… |
| `o` | `log_open` | open job in browser | Log: open job in browser |
| `esc` `q` | `log_close` | close | Log: close |

#### note

| Keys | Action id | Does | Palette command |
| --- | --- | --- | --- |
| `e` `enter` | `note_edit` | edit | — |
| `E` | `note_external` | $EDITOR | Edit daily note in $EDITOR |
| `[` | `note_prev_day` | prev day | — |
| `]` | `note_next_day` | next day | — |
| `t` | `note_today` | today | — |
| `s` | `note_standup` | standup | Generate standup |
| `y` | `note_copy` | copy standup | Copy last standup |
| `esc` `q` | `note_close` | close | — |

### Modal (inputs, pickers, forms)

#### filepicker

| Keys | Action id | Does | Palette command |
| --- | --- | --- | --- |
| `tab` | `file_picker_toggle` | mark file | — |
| `ctrl+a` | `file_picker_toggle_all` | mark all shown | — |

#### capture

| Keys | Action id | Does | Palette command |
| --- | --- | --- | --- |
| `enter` | `capture_submit` | create | — |
| `shift+enter` `alt+enter` | `capture_full_form` | full form | — |
| `tab` | `capture_complete` | complete | — |
| `shift+tab` | `capture_complete_prev` | prev completion | — |
| `left` `ctrl+b` | `capture_left` | left | — |
| `right` `ctrl+f` | `capture_right` | right | — |
| `home` `ctrl+a` | `capture_home` | start of line | — |
| `end` `ctrl+e` | `capture_end` | end of line | — |
| `ctrl+w` `alt+backspace` | `capture_delete_word` | delete word | — |
| `delete` | `capture_delete_forward` | delete char under cursor | — |

#### input

| Keys | Action id | Does | Palette command |
| --- | --- | --- | --- |
| `esc` | `cancel` | cancel | — |
| `enter` | `accept` | accept | — |
| `backspace` | `delete_back` | delete char | — |
| `ctrl+u` | `clear_input` | clear input | — |
| `down` `ctrl+n` | `next` | next | — |
| `up` `ctrl+p` | `prev` | prev | — |

#### form

| Keys | Action id | Does | Palette command |
| --- | --- | --- | --- |
| `esc` | `form_cancel` (fixed) | cancel | — |
| `enter` `tab` | `form_next_field` (fixed) | next | — |
| `shift+tab` | `form_prev_field` (fixed) | back | — |
| `enter` | `form_submit` (fixed) | submit | — |
| `ctrl+e` | `form_accept_suggestion` (fixed) | complete | — |
| `alt+enter` `ctrl+j` | `form_text_newline` (fixed) | new line | — |
| `ctrl+e` | `form_text_editor` (fixed) | open editor | — |
| `up` `k` `ctrl+k` `ctrl+p` | `form_select_up` (fixed) | up | — |
| `down` `j` `ctrl+j` `ctrl+n` | `form_select_down` (fixed) | down | — |
| `/` | `form_select_filter` (fixed) | filter | — |
| `space` `x` | `form_multiselect_toggle` (fixed) | toggle | — |
| `ctrl+a` | `form_multiselect_all` (fixed) | select all | — |
| `h` `l` `right` `left` | `form_confirm_toggle` (fixed) | toggle | — |
| `y` `Y` | `form_confirm_accept` (fixed) | Yes | — |
| `n` `N` | `form_confirm_reject` (fixed) | No | — |
| `enter` | `form_confirm_submit` (fixed) | submit | — |

### Programs (setup, hidden demos)

#### setup

| Keys | Action id | Does | Palette command |
| --- | --- | --- | --- |
| `enter` | `setup_next` | continue | — |
| `s` | `setup_skip` | skip | — |
| `r` | `setup_rerun` | check again | — |
| `k` `up` | `setup_up` | up | — |
| `j` `down` | `setup_down` | down | — |
| `tab` | `setup_focus` | steps / step | — |
| `l` | `setup_login` | gh auth login | — |
| `e` | `setup_edit` | edit | — |
| `t` | `setup_test` | run test | — |
| `R` | `setup_reset` | reset tmux.conf | — |
| `c` | `setup_copy` | copy | — |
| `a` | `setup_attach` | attach | — |
| `+` | `setup_add_root` | add root | — |
| `-` | `setup_remove_root` | remove root | — |
| `f` | `setup_fix_link` | fix Plane link | — |
| `q` `ctrl+c` | `setup_quit` | quit | — |

#### demo anim

| Keys | Action id | Does | Palette command |
| --- | --- | --- | --- |
| `r` | `demo_anim_replay` (fixed) (hidden) | replay splash | — |
| `space` | `demo_anim_burst` (fixed) (hidden) | spark burst | — |
| `1` | `demo_anim_full` (fixed) (hidden) | full | — |
| `2` | `demo_anim_reduced` (fixed) (hidden) | reduced | — |
| `3` | `demo_anim_off` (fixed) (hidden) | off | — |
| `q` `esc` `ctrl+c` | `demo_anim_quit` (fixed) (hidden) | quit | — |

#### demo tmux

| Keys | Action id | Does | Palette command |
| --- | --- | --- | --- |
| `enter` | `demo_tmux_attach` (fixed) (hidden) | attach | — |
| `n` | `demo_tmux_restart` (fixed) (hidden) | new window | — |
| `x` | `demo_tmux_kill` (fixed) (hidden) | kill window | — |
| `q` `ctrl+c` | `demo_tmux_quit` (fixed) (hidden) | quit | — |

### Agent windows (forge's tmux.conf)

| Keys | Action id | Does | Palette command |
| --- | --- | --- | --- |
| `alt+d` | `tmux_detach_client` (fixed) | detach, back to forge (or close the popup forge opened) | — |
| `alt+[` | `tmux_copy_mode` (fixed) | scroll / copy mode (q leaves it; the mouse wheel works too) | — |
| `alt+w` | `tmux_choose_tree` (fixed) | pick any agent window from a tree, zoomed | — |
| `alt+c` | `tmux_display_popup` (fixed) | quick capture a Plane issue in a popup (forge capture; tmux 3.2+) | — |

### Palette commands without a key

| Action id | Palette command |
| --- | --- |
| `notify_test` | Notifications: send test notification |
| `adr` | Log a decision (ADR)… |
| `templates_edit` | Edit templates |
| `templates_sync` | Templates: sync forge tools section |
| `doctor` | Run doctor |
| `setup` | Run setup |
| `setup_deps` | Run setup: Dependencies |
| `setup_github` | Run setup: GitHub |
| `setup_plane` | Run setup: Plane |
| `setup_projects` | Run setup: Projects |
| `setup_tmux` | Run setup: tmux |
| `setup_animations` | Run setup: Animations |
| `setup_capture` | Run setup: Quick capture |
| `setup_smoke` | Run setup: Smoke test |
| `pr_section_my-prs` | PRs: My PRs |
| `pr_section_review-requested` | PRs: Review requested |
| `pr_section_involved` | PRs: Involved |
| `pr_detail_overview` | PRs: show overview |
| `pr_detail_checks` | PRs: show checks |
| `pr_detail_activity` | PRs: show activity |
| `plane_link_project` | Plane: link project… |
| `tmux_restart` | tmux: restart forge server… |
| `tmux_config` | tmux: edit forge tmux config |
| `tmux_config_reset` | tmux: reset forge tmux config… |
| `artifacts_detach` | Artifacts: detach from session (stop syncing the pinned copy) |
| `artifacts_redraw_preview` | Artifacts: redraw preview |

## 2. Problems

Everything below comes from the dump in part 1.

### 2.1 One key, different meanings across tabs

Movement keys (`j k g G h l`, `ctrl+d/u`) and keys that mean the same thing everywhere (`o` open,
`y` copy, `F` fix, `q`/`esc` close) are fine. These are not:

| Key | Meanings today |
| --- | --- |
| `a` | archived view (Sessions) · accept proposal (Home) · log decision (Docs) · pick files (Context) · approve (diff) |
| `c` | check out (PRs) · comment (Plane) · keep a copy (Artifacts) · cache view (Context) |
| `m` | merge (PRs, Home) · mine only (Pipelines, Plane) · rename (Docs, Artifacts) · commands view (Context) |
| `x` | kill session · drop proposal · clear filters (Pipelines, Plane, Artifacts) · clear cache · request changes |
| `t` | transcript (Sessions) · rerun failed jobs (Pipelines, log) · edit tags (Artifacts) · today (note) |
| `p` | pipeline runs (PRs) · pin (Artifacts) · packs view (Context) · previous file (diff) |
| `d` | toggle draft (PRs) · decisions view (Docs) |
| `b` | background resume (Sessions) · board / list (Plane) |
| `u` | update branch (PRs) · usage view (Context) |
| `l` | next section / column (vim right) · **job logs** (Pipelines) |
| `n` | new (Sessions, Plane, Docs, Context) · next file (diff) · next match (log) |
| `o` | open in browser (everywhere) · **remove orphaned worktrees** (Context) |
| `e` | edit (Home, Plane, Docs, Context, note) · expand all (transcript) · full log (log) |
| `f` | filter form (PRs, Pipelines, Plane, Artifacts) · follow (transcript) |
| `s` | start session / send to agent · standup (note) |
| `S` | stale only (PRs) · mark superseded (Docs) · re-sync sessions (Context) |
| `P` | attach context pack (Sessions) · pinned only (Artifacts) · prune cache (Context) |
| `X` | cancel run (Pipelines, log) · reset command (Context) |
| `D` | delete session · delete doc · delete pack (consistent, but on a capital for no reason) |
| `C` | current cycle (Plane) · comment (diff) |
| `R` | refresh all (global) · rerun failed (log) |
| `space` | mark for agent (Docs) · page down (diff, log) |

The worst is `o` on Context: it means "open" on every other tab, but there it **removes orphaned
worktrees** (after a confirm).

### 2.2 One action, different keys

| Action | Keys today |
| --- | --- |
| delete (with confirm) | `D` (Sessions, Docs, Context packs) · `x` (Home drop, Context cache) · `X` (Context reset command) |
| clear filters | `x` (three tabs). `esc` already clears a text filter. |
| copy something | `y` (PRs, Plane, Artifacts, note) · `c` (Artifacts keep a copy, setup copy) |
| comment | `c` (Plane) · `C` (diff) |
| rerun failed jobs | `t` (Pipelines) · `t` and `R` (log) |
| primary action on the row | `enter` (Sessions, Home, Plane, Docs, Context) · `v` (PRs: review diff) · `l` (Pipelines: logs) |
| switch view | `a` · `b` · `d` · `i` · `p` · `c` · `u` · `m` · `e`, a different letter for each view |
| next / previous chunk | `[` `]` (detail tabs, failed jobs, days) · `n` `p` (diff files) |
| fix with edited prompt | `alt+f` (three places) |
| mine only | `m` (Pipelines, Plane). Capture writes it as `@me`. |
| go somewhere | `ctrl+p` (projects) · `ctrl+g` (sessions) · palette for tabs and entities |

### 2.3 Keys chosen because the obvious one was taken

- `b` background resume, `t` rerun failed, `P` attach context pack, `X` cancel run: leftover letters.
- `v` review diff: `d` was toggle draft and `enter` was unused.
- `ctrl+g` go to session: `ctrl+s` went to the note and `ctrl+p` to projects.
- `D` for delete: when delete was added, `d` was already toggle draft or decisions view.
- `c` keep a copy (Artifacts): `y` was copy path.
- `m` commands view, `u` usage view, `c` cache view, `p` packs view, `i` worktrees: each Context view
  got whatever letter was free.
- `o` remove orphans: the mnemonic won over the `o` = open convention.

### 2.4 Frequent actions on awkward keys

- Review a PR (the most common PR action): `v`. `enter` does nothing on the PR list.
- Open a failed run's logs (the most common Pipelines action): `l`, which looks like vim right.
- Fix with an edited prompt: `alt+f`. It doesn't work on macOS without Option-as-Meta.
- All four agent-window keys (`alt+d` back, `alt+[` scroll, `alt+w` windows, `alt+c` capture) need
  Alt. That's the first key a new user must press, and setup needs a whole "Option as Meta per
  terminal" help screen for it.

### 2.5 Rare actions on prime keys

- `o`, `P`, `V` on Context (remove orphans, prune, vacuum): rare maintenance on single letters.
- `S` re-sync sessions (forge already syncs on start), `X` reset command, `d` toggle draft,
  `b` background resume, `ctrl+r` rerun the whole run (costly).

### 2.6 Hard to discover

- 26 palette commands have no key at all (part 1, last table). That's fine for the rare ones, but
  `pr_detail_*`, `pr_section_*` and the setup steps are only reachable by typing in the palette.
- `note_standup`, `note_copy` and `note_external` have keys only inside the note pane, and the
  palette shows them with no hint.
- Viewer commands (diff, log, transcript) only appear in the palette while the viewer is open,
  which is reasonable. But the help overlay is the only place that lists the viewer keys before
  you open one.
- `esc` clearing a text filter isn't shown anywhere.
- `goto_tab` has no hint for which number is which tab, beyond the tab bar order.

### 2.7 Anything that needs Alt / Option

`alt+f` (×3: PRs, Pipelines, log), `alt+enter` (capture full form; `shift+enter` needs extended
keys), `alt+backspace` (capture, alias of `ctrl+w`), and in agent windows `M-d`, `M-[`, `M-w` and
`M-c`. Huh's text field uses `alt+enter` for a newline, but `ctrl+j` also works.

### 2.8 Smaller clashes

- `ctrl+u` is half page up in lists and "clear input" in inputs. Both are live in different
  scopes, and both match their convention (readline in inputs, vim in lists), so this is fine.
- `ctrl+n` / `ctrl+p` are capture / projects globally and next / prev inside pickers. Inside the
  palette the picker wins, which is VS Code's behaviour, so this is fine too.
- Huh's confirm accepts `y` / `n` and toggles with `h` / `l`. Fine, and it isn't ours to change.

## 3. Design principles

1. **One verb = one key, everywhere.** If a tab has the verb, it's on that key:

   | Key | Verb |
   | --- | --- |
   | `enter` | the primary action on the row (attach, review, open logs, open card, open doc) |
   | `esc` | back / cancel / close one level. It clears an active filter or search before anything else. It never quits forge. |
   | `q` | quit from the top level; close a viewer (the `less` habit) |
   | `o` / `O` | open in the browser or default app / open the secondary link |
   | `y` / `Y` | copy (URL, path, text) / copy the thing itself (keep a copy of the artifact) |
   | `e` / `E` | edit inline or in a form / edit in `$EDITOR` |
   | `n` | new (in viewers with search: next match, with `N` previous) |
   | `s` | send to an agent / start a session with this |
   | `d` | delete, always with a confirm |
   | `x` | stop, cancel or reject (kill session, cancel run, request changes), always with a confirm |
   | `a` | accept / approve / add |
   | `c` | comment |
   | `m` / `M` | merge / auto-merge (PRs, Home); move / rename (Docs, Artifacts). See D5. |
   | `b` | branch: check out |
   | `f` | the structured filter form (repo, state, stale, pinned, cycle…) |
   | `/` | text filter (search in viewers) |
   | `@` | toggle "mine only" (also a field in the `f` form, so `@` is never the only way) |
   | `#` / `*` | tags / pin |
   | `v` / `V` | next / previous view of this tab |
   | `[` `,` / `]` `.` | previous / next chunk (detail tab, file, failed job, day). `,` / `.` are aliases for layouts where brackets need AltGr / Option. |
   | `z` / `Z` | fold one / all |
   | `F` | fix with an agent |
   | `r` / `R` / `ctrl+r` | refresh / refresh all tabs / run again on the server (rerun failed) |
   | `?` | help (every key in this context) |
   | `1`–`9`, `tab`, `shift+tab` | tabs |

2. **Capitals are a stronger or reversed form of the same verb** (`M` auto-merge, `Y` keep a copy,
   `O` open the issue, `N` previous match, `V` previous view, `Z` fold all), never an unrelated
   action that ran out of letters.
3. **Overload a letter across tabs only when both meanings are its obvious mnemonic**, they never
   share a tab, and the destructive one confirms. Today that's only `m` (merge / move), and `t`
   inside the note pane (today).
4. **Primary actions get `enter`, frequent actions get lowercase letters**, and rare maintenance
   goes to the palette (`ctrl+k`, which lists every action anyway).
5. **VS Code habits where they fit a TUI:** `ctrl+p` goes to anything (tabs, projects, sessions,
   PRs, issues, docs), `ctrl+k` opens the command palette (as in Slack, Linear, GitHub and Raycast;
   VS Code's `ctrl+shift+p` isn't reliable in terminals), and `cmd+,` opens settings where Cmd is
   available.
6. **Claude Code habits:** `esc` backs out, `ctrl+c` always force-quits, `?` shows the keys. For
   `shift+tab` see D1.
7. **vim movement stays:** `j k h l g G ctrl+d ctrl+u / n N`, plus the arrows, `home`/`end` and
   `pgup`/`pgdown`.
8. **Nothing in the default preset requires Alt / Option**, and no default needs a terminal setting.
   Alt and Cmd may be *extra* aliases.
9. **Every action stays a palette command, and the palette shows its key.** A key that only exists
   in a viewer still shows in the help overlay.

## 4. Proposed default preset

A ✱ marks every change. "—" means no key: palette only, folded into another key, or a new
palette-only command. The full table was checked against the keymap's conflict rules (one key
per scope, and no tab key may shadow a global key): there are **no conflicts**. Huh's form keys and
the hidden demos don't change, so they're left out.

New global actions: `goto` (`ctrl+p`), `view_next` / `view_prev` (`v` / `V`), and the palette
commands `settings` (plus `cmd+,`) and `agent_scroll` ("Scroll agent output"). Context gets one contextual `context_delete` (`d`) for its views. Behaviour
changes that come with the keys:
- `esc` clears active filters.
- `G` follows the transcript.
- `F` opens the fix sheet (D3).
- `f` forms gain stale / pinned-only / cycle, and "mine only" on Pipelines and Plane.
- In the decisions view, `n` creates a decision.

#### global

| Action | Current | Proposed | Why |
| --- | --- | --- | --- |
| `quit` | `q` | `q` | Keep; quits from the top level only (esc never quits). |
| `force_quit` | `ctrl+c` | `ctrl+c` | Keep. |
| `help` | `?` | `?` | Keep (universal). |
| `next_tab` | `tab` | `tab` | Keep (see decision D1). |
| `prev_tab` | `shift+tab` | `shift+tab` | Keep (see decision D1). |
| `goto_tab` | `1` `2` `3` `4` `5` `6` `7` `8` `9` | `1` `2` `3` `4` `5` `6` `7` `8` `9` | Keep. |
| `refresh` | `r` | `r` | Keep. |
| `refresh_all` | `R` | `R` | Keep. |
| `palette` | `ctrl+k` | `ctrl+k` | Keep: ctrl+k is the command palette in Slack, Linear, GitHub, Raycast; ctrl+shift+p is not reliable in terminals. |
| `projects` ✱ | `ctrl+p` | — | Merged into ctrl+p *go to anything* (projects are one group in it). VS Code's ctrl+p habit; "Switch project…" stays in the palette. |
| `goto_session` ✱ | `ctrl+g` | — | Merged into ctrl+p *go to anything* (sessions are a group); ctrl+g was picked because ctrl+s/ctrl+p were taken. |
| `capture` | `ctrl+n` | `ctrl+n` | Keep: n = new, ctrl = from anywhere. |
| `note` | `ctrl+s` | `ctrl+s` | Keep: ctrl+s = save / scratchpad, and it saves the note when pressed inside it. |
| `up` | `k` `up` | `k` `up` | Keep. |
| `down` | `j` `down` | `j` `down` | Keep. |
| `top` | `g` `home` | `g` `home` | Keep. |
| `bottom` | `G` `end` | `G` `end` | Keep. |
| `page_down` ✱ | `ctrl+d` | `ctrl+d` `pgdown` | Add pgdown so lists page like the viewers do. |
| `page_up` ✱ | `ctrl+u` | `ctrl+u` `pgup` | Add pgup, as above. |
| `filter` | `/` | `/` | Keep. |
| `grow_pane` | `>` | `>` | Keep. |
| `shrink_pane` | `<` | `<` | Keep. |

#### sessions

| Action | Current | Proposed | Why |
| --- | --- | --- | --- |
| `session_new` | `n` | `n` | Keep. |
| `session_kill` | `x` | `x` | Keep: x = stop. |
| `session_delete` ✱ | `D` | `d` | d = delete (with confirm) everywhere; D was used because nothing reserved d. |
| `session_archive_view` ✱ | `a` | — | Folded into the global v (view_next): active / archived. |
| `session_archive` | `A` | `A` | Keep: capital because it hides the row (a stays accept / add). |
| `session_attach` | `enter` | `enter` | Keep. |
| `session_resume` ✱ | `b` | — | Rare: enter already resumes a dead session and attaches. b was a leftover letter. Palette only. |
| `session_transcript` | `t` | `t` | Keep. |
| `session_open_pr` | `o` | `o` | Keep. |
| `session_open_issue` | `O` | `O` | Keep. |
| `session_attach_pack` ✱ | `P` | `s` | s = send to agent; sending a pack to the selected session is exactly that. |

#### prs

| Action | Current | Proposed | Why |
| --- | --- | --- | --- |
| `pr_next_section` | `l` `right` | `l` `right` | Keep. |
| `pr_prev_section` | `h` `left` | `h` `left` | Keep. |
| `pr_repo_filter` | `f` | `f` | Keep. |
| `pr_stale_only` ✱ | `S` | — | A filter: moves into the f filter form (stale only), plus the palette. Frees S. |
| `pr_detail_next` ✱ | `]` | `]` `.` | Add . as a layout-safe alias: , / . are aliases for keyboards where [ ] need AltGr / Option. |
| `pr_detail_prev` ✱ | `[` | `[` `,` | Add , (see pr_detail_next). |
| `pr_detail_down` | `J` | `J` | Keep. |
| `pr_detail_up` | `K` | `K` | Keep. |
| `pr_open` | `o` | `o` | Keep. |
| `pr_copy_url` | `y` | `y` | Keep. |
| `pr_checkout` ✱ | `c` | `b` | b = branch (check out). c becomes comment everywhere. |
| `pr_update_branch` | `u` | `u` | Keep. |
| `pr_merge` | `m` | `m` | Keep. |
| `pr_auto_merge` | `M` | `M` | Keep. |
| `pr_toggle_draft` ✱ | `d` | — | Rare, and d now means delete. Palette only ("PR: toggle draft / ready"). |
| `pr_session` | `s` | `s` | Keep. |
| `pr_diff` ✱ | `v` | `enter` | Reviewing is the primary thing you do with a PR; enter did nothing on the PR list. v becomes views. |
| `pr_runs` | `p` | `p` | Keep: p = pipeline runs. |
| `pr_fix` | `F` | `F` | Keep (F = fix with an agent), now opening the fix sheet (D3). |
| `pr_fix_edit` ✱ | `alt+f` | — | Alt is not available on macOS by default. F opens the fix sheet with a prompt preview: F again or enter sends, e edits inline, E opens $EDITOR (D3). |

#### home

| Action | Current | Proposed | Why |
| --- | --- | --- | --- |
| `home_open` | `enter` | `enter` | Keep. |
| `home_fix` | `F` | `F` | Keep. |
| `home_merge` | `m` | `m` | Keep. |
| `home_browse` | `o` | `o` | Keep. |
| `home_accept` | `a` | `a` | Keep. |
| `home_edit` | `e` | `e` | Keep. |
| `home_drop` ✱ | `x` | `d` | Dropping a proposal is a delete (it asks first): d. |

#### pipelines

| Action | Current | Proposed | Why |
| --- | --- | --- | --- |
| `pipeline_filters` | `f` | `f` | Keep. |
| `pipeline_mine` ✱ | `m` | `@` | @ = me (as in capture's @me); m stays merge. Also a "mine only" toggle in the f form, so @ is never the only way. |
| `pipeline_clear_filters` ✱ | `x` | — | esc clears active filters (esc backs out one level); palette "clear filters" stays. |
| `pipeline_detail_down` | `J` | `J` | Keep. |
| `pipeline_detail_up` | `K` | `K` | Keep. |
| `pipeline_open` | `o` | `o` | Keep. |
| `pipeline_logs` ✱ | `l` | `enter` | Opening the failed logs is the primary action on a run; l is vim right. |
| `pipeline_rerun_failed` ✱ | `t` | `ctrl+r` | ctrl+r = run again on the server (the r of refresh, made stronger); t was a leftover letter. |
| `pipeline_rerun` ✱ | `ctrl+r` | — | Rerunning the whole run is rare and costly; palette only (it confirms). |
| `pipeline_cancel` ✱ | `X` | `x` | x = stop / cancel / reject everywhere (it confirms). |
| `pipeline_fix` | `F` | `F` | Keep. |
| `pipeline_fix_edit` ✱ | `alt+f` | — | As pr_fix_edit: no Alt; the F sheet has e / E to edit the prompt. |

#### plane

| Action | Current | Proposed | Why |
| --- | --- | --- | --- |
| `plane_filters` | `f` | `f` | Keep. |
| `plane_mine` ✱ | `m` | `@` | @ = me, as in Pipelines; also in the f form. |
| `plane_clear_filters` ✱ | `x` | — | esc clears active filters; palette stays. |
| `plane_detail_down` | `J` | `J` | Keep. |
| `plane_detail_up` | `K` | `K` | Keep. |
| `plane_open` | `o` | `o` | Keep. |
| `plane_copy_url` | `y` | `y` | Keep. |
| `plane_board` ✱ | `b` | — | Folded into v (view_next): list / board. |
| `plane_col_left` | `h` `left` | `h` `left` | Keep. |
| `plane_col_right` | `l` `right` | `l` `right` | Keep. |
| `plane_move_prev` | `H` | `H` | Keep. |
| `plane_move_next` | `L` | `L` | Keep. |
| `plane_collapse_column` | `z` | `z` | Keep. |
| `plane_collapse_empty` | `Z` | `Z` | Keep. |
| `plane_open_card` | `enter` | `enter` | Keep. |
| `plane_new` | `n` | `n` | Keep. |
| `plane_edit` | `e` | `e` | Keep. |
| `plane_comment` | `c` | `c` | Keep. |
| `plane_session` | `s` | `s` | Keep. |
| `plane_cycle` | `C` | `C` | Keep: C = current cycle (frequent for sprint users). |

#### docs

| Action | Current | Proposed | Why |
| --- | --- | --- | --- |
| `docs_new` | `n` | `n` | Keep. |
| `docs_edit` | `e` | `e` | Keep. |
| `docs_rename` | `m` | `m` | Keep: m = move (mv), and Docs rename is a move. Deliberate overlap with merge, see the overload rule. |
| `docs_delete` ✱ | `D` | `d` | d = delete with confirm (to .trash). |
| `docs_open` | `enter` `l` `right` | `enter` `l` `right` | Keep. |
| `docs_back` | `h` `left` | `h` `left` | Keep. |
| `docs_detail_down` | `J` | `J` | Keep. |
| `docs_detail_up` | `K` | `K` | Keep. |
| `docs_mark` | `space` | `space` | Keep. |
| `docs_send` | `s` | `s` | Keep. |
| `docs_decision` ✱ | `a` | — | In the decisions view, n creates a decision (n = new); from anywhere, the palette's "Log a decision". a stays accept / add. |
| `docs_decisions` ✱ | `d` | — | Folded into v (view_next): docs / decisions. |
| `docs_supersede` | `S` | `S` | Keep: S is free everywhere else now. |

#### artifacts

| Action | Current | Proposed | Why |
| --- | --- | --- | --- |
| `artifacts_open` | `o` | `o` | Keep. |
| `artifacts_copy_path` | `y` | `y` | Keep. |
| `artifacts_keep` ✱ | `c` | `Y` | Keeping a copy in the project is a copy: Y, the stronger y. c becomes comment. |
| `artifacts_pin` ✱ | `p` | `*` | * reads as a star / pin; p stays "pipeline runs" on PRs. |
| `artifacts_tags` ✱ | `t` | `#` | # reads as a tag; t was a leftover letter. |
| `artifacts_rename` | `m` | `m` | Keep: m = move / rename, as in Docs. |
| `artifacts_filters` | `f` | `f` | Keep. |
| `artifacts_pinned_only` ✱ | `P` | — | A filter: into the f form, plus the palette. |
| `artifacts_clear_filters` ✱ | `x` | — | esc clears active filters; palette stays. |

#### context

| Action | Current | Proposed | Why |
| --- | --- | --- | --- |
| `context_edit` | `e` | `e` | Keep. |
| `context_worktrees` ✱ | `i` | — | Folded into v (view_next). One cycle for every Context view: memory → packs → commands → usage → cache → worktrees; V goes back. |
| `context_open` | `enter` `l` `right` | `enter` `l` `right` | Keep. |
| `context_back` | `h` `left` | `h` `left` | Keep. |
| `context_detail_down` | `J` | `J` | Keep. |
| `context_detail_up` | `K` | `K` | Keep. |
| `context_packs` ✱ | `p` | — | Folded into v (view_next); was p. |
| `context_new_pack` | `n` | `n` | Keep. |
| `context_add_files` | `a` | `a` | Keep. |
| `context_delete_pack` ✱ | `D` | `d` | Becomes `context_delete`: d deletes the selection of the current view (a pack, a cache type's entries, a command override), always asking first. |
| `context_send_pack` | `s` | `s` | Keep. |
| `context_cache` ✱ | `c` | — | Folded into v (view_next); was c. |
| `context_cache_clear` ✱ | `x` | — | Folded into context_delete (d) in the cache view. |
| `context_cache_prune` ✱ | `P` | — | Rare maintenance: palette only. |
| `context_cache_orphans` ✱ | `o` | — | Rare maintenance, and o means open everywhere else: palette only. |
| `context_cache_vacuum` ✱ | `V` | — | Rare maintenance: palette only. |
| `context_usage` ✱ | `u` | — | Folded into v (view_next); was u. |
| `context_commands` ✱ | `m` | — | Folded into v (view_next); was m. |
| `context_commands_reset` ✱ | `X` | — | Folded into context_delete (d) in the commands view: it deletes your override. |
| `context_commands_sync` ✱ | `S` | — | Rare (forge syncs on start): palette only. |

#### filepicker

| Action | Current | Proposed | Why |
| --- | --- | --- | --- |
| `file_picker_toggle` | `tab` | `tab` | Keep. |
| `file_picker_toggle_all` | `ctrl+a` | `ctrl+a` | Keep. |

#### transcript

| Action | Current | Proposed | Why |
| --- | --- | --- | --- |
| `transcript_up` | `k` `up` | `k` `up` | Keep. |
| `transcript_down` | `j` `down` | `j` `down` | Keep. |
| `transcript_page_up` | `ctrl+u` `pgup` | `ctrl+u` `pgup` | Keep. |
| `transcript_page_down` | `ctrl+d` `pgdown` | `ctrl+d` `pgdown` | Keep. |
| `transcript_top` | `g` `home` | `g` `home` | Keep. |
| `transcript_bottom` | `G` `end` | `G` `end` | Keep. |
| `transcript_toggle` ✱ | `enter` | `enter` `z` | Add z for the fold family. |
| `transcript_toggle_all` ✱ | `e` | `Z` | z / Z fold one / all, as on the Plane board (z also folds the entry under the cursor, next to enter). |
| `transcript_follow` ✱ | `f` | — | G jumps to the newest entry and keeps following; any move up stops (like tail -f / less +F). One key fewer. |
| `transcript_close` | `esc` `q` | `esc` `q` | Keep. |

#### diff

| Action | Current | Proposed | Why |
| --- | --- | --- | --- |
| `diff_up` | `k` `up` | `k` `up` | Keep. |
| `diff_down` | `j` `down` | `j` `down` | Keep. |
| `diff_page_up` | `ctrl+u` `pgup` | `ctrl+u` `pgup` | Keep. |
| `diff_page_down` | `ctrl+d` `pgdown` `space` | `ctrl+d` `pgdown` `space` | Keep. |
| `diff_top` | `g` `home` | `g` `home` | Keep. |
| `diff_bottom` | `G` `end` | `G` `end` | Keep. |
| `diff_next_file` ✱ | `n` | `]` `.` | [ / ] = previous / next chunk everywhere (file, job, detail tab, day), with , / . aliases. |
| `diff_prev_file` ✱ | `p` | `[` `,` | As above. |
| `diff_approve` | `a` | `a` | Keep. |
| `diff_request_changes` | `x` | `x` | Keep: x = reject. |
| `diff_comment` ✱ | `C` | `c` | c = comment everywhere (Plane already used c). |
| `diff_open` | `o` | `o` | Keep. |
| `diff_close` | `esc` `q` | `esc` `q` | Keep. |

#### log

| Action | Current | Proposed | Why |
| --- | --- | --- | --- |
| `log_up` | `k` `up` | `k` `up` | Keep. |
| `log_down` | `j` `down` | `j` `down` | Keep. |
| `log_page_up` | `ctrl+u` `pgup` | `ctrl+u` `pgup` | Keep. |
| `log_page_down` | `ctrl+d` `pgdown` `space` | `ctrl+d` `pgdown` `space` | Keep. |
| `log_top` | `g` `home` | `g` `home` | Keep. |
| `log_bottom` | `G` `end` | `G` `end` | Keep. |
| `log_toggle_full` ✱ | `e` | `v` | v = next view, as on the tabs (errors / full log). The log viewer is modal, so it binds v itself. |
| `log_search` | `/` | `/` | Keep. |
| `log_next_match` | `n` | `n` | Keep. |
| `log_prev_match` | `N` | `N` | Keep. |
| `log_next_job` ✱ | `]` | `]` `.` | Add . as a layout-safe alias: , / . are aliases for keyboards where [ ] need AltGr / Option. |
| `log_prev_job` ✱ | `[` | `[` `,` | Add , (see log_next_job). |
| `log_rerun_failed` ✱ | `t` `R` | `ctrl+r` | Same key as on the Pipelines tab (was t and R). |
| `log_rerun` ✱ | `ctrl+r` | — | As pipeline_rerun: palette only. |
| `log_cancel` ✱ | `X` | `x` | x = stop / cancel. |
| `log_fix` | `F` | `F` | Keep. |
| `log_fix_edit` ✱ | `alt+f` | — | As pr_fix_edit. |
| `log_open` | `o` | `o` | Keep. |
| `log_close` | `esc` `q` | `esc` `q` | Keep. |

#### capture

| Action | Current | Proposed | Why |
| --- | --- | --- | --- |
| `capture_submit` | `enter` | `enter` | Keep. |
| `capture_full_form` ✱ | `shift+enter` `alt+enter` | `shift+enter` `ctrl+o` | ctrl+o (open the full form) replaces alt+enter: no Alt. shift+enter stays where the terminal reports it. |
| `capture_complete` | `tab` | `tab` | Keep. |
| `capture_complete_prev` | `shift+tab` | `shift+tab` | Keep. |
| `capture_left` | `left` `ctrl+b` | `left` `ctrl+b` | Keep. |
| `capture_right` | `right` `ctrl+f` | `right` `ctrl+f` | Keep. |
| `capture_home` | `home` `ctrl+a` | `home` `ctrl+a` | Keep. |
| `capture_end` | `end` `ctrl+e` | `end` `ctrl+e` | Keep. |
| `capture_delete_word` | `ctrl+w` `alt+backspace` | `ctrl+w` `alt+backspace` | Keep. |
| `capture_delete_forward` | `delete` | `delete` | Keep. |

#### note

| Action | Current | Proposed | Why |
| --- | --- | --- | --- |
| `note_edit` | `e` `enter` | `e` `enter` | Keep. |
| `note_external` | `E` | `E` | Keep. |
| `note_prev_day` ✱ | `[` | `[` `,` | Add , (see note_next_day). |
| `note_next_day` ✱ | `]` | `]` `.` | Add . as a layout-safe alias: , / . are aliases for keyboards where [ ] need AltGr / Option. |
| `note_today` | `t` | `t` | Keep (t = today; only inside the note pane). |
| `note_standup` | `s` | `s` | Keep (only inside the note pane). |
| `note_copy` | `y` | `y` | Keep. |
| `note_close` | `esc` `q` | `esc` `q` | Keep. |

#### setup

| Action | Current | Proposed | Why |
| --- | --- | --- | --- |
| `setup_next` | `enter` | `enter` | Keep. |
| `setup_skip` | `s` | `s` | Keep. |
| `setup_rerun` | `r` | `r` | Keep. |
| `setup_up` | `k` `up` | `k` `up` | Keep. |
| `setup_down` | `j` `down` | `j` `down` | Keep. |
| `setup_focus` | `tab` | `tab` | Keep. |
| `setup_login` | `l` | `l` | Keep. |
| `setup_edit` | `e` | `e` | Keep. |
| `setup_test` | `t` | `t` | Keep. |
| `setup_reset` | `R` | `R` | Keep. |
| `setup_copy` ✱ | `c` | `y` | y = copy everywhere. |
| `setup_attach` | `a` | `a` | Keep. |
| `setup_add_root` | `+` | `+` | Keep. |
| `setup_remove_root` | `-` | `-` | Keep. |
| `setup_fix_link` | `f` | `f` | Keep. |
| `setup_quit` | `q` `ctrl+c` | `q` `ctrl+c` | Keep. |

#### input

| Action | Current | Proposed | Why |
| --- | --- | --- | --- |
| `cancel` | `esc` | `esc` | Keep. |
| `accept` | `enter` | `enter` | Keep. |
| `delete_back` | `backspace` | `backspace` | Keep. |
| `clear_input` | `ctrl+u` | `ctrl+u` | Keep. |
| `next` | `down` `ctrl+n` | `down` `ctrl+n` | Keep. |
| `prev` | `up` `ctrl+p` | `up` `ctrl+p` | Keep. |

#### tmux

| Action | Current | Proposed | Why |
| --- | --- | --- | --- |
| `tmux_detach_client` ✱ | `alt+d` | `ctrl+q` `ctrl+]` | ctrl+q ("quit back to forge") works without Option-as-Meta on every layout and Claude Code leaves it free; ctrl+] (telnet / ssh escape habit) as an alias on US layouts. |
| `tmux_copy_mode` ✱ | `alt+[` | `shift+pageup` | The usual terminal scrollback key, entering tmux scroll mode one page up. Fallback (ships regardless): the mouse wheel and the palette command "Scroll agent output". P6.9 decides after the terminal checks. No Alt. |
| `tmux_choose_tree` ✱ | `alt+w` | `ctrl+\` | Free in Claude Code and every terminal (US layout). Most people switch agents from forge (ctrl+q, then ctrl+p) anyway. |
| `tmux_display_popup` ✱ | `alt+c` | — | Dropped from agent windows: ctrl+q then ctrl+n is two presses with nothing new to learn. The snippet for your own tmux stays, with a key you choose. |

#### global

| Action | Current | Proposed | Why |
| --- | --- | --- | --- |
| `goto` ✱ | — | `ctrl+p` | **New.** Go to anything: tabs, projects, sessions, PRs, issues, docs (VS Code's ctrl+p). Replaces projects (ctrl+p) and goto_session (ctrl+g). |
| `view_next` ✱ | — | `v` | **New.** Next view of the current tab (Sessions active/archived, Plane list/board, Docs docs/decisions, Context views, log errors/full). Was a, b, d, i, p, c, u, m, e. |
| `view_prev` ✱ | — | `V` | **New.** Previous view. |
| `settings` | — | — | **New palette command** "Settings: edit config.yaml" (cmd+, where Cmd is available). |
| `agent_scroll` | — | — | **New palette command** "Sessions: scroll agent output": attaches the selected session's window in copy mode (the shift+pageup fallback). |

### 4.1 The default at a glance

```
anywhere   ctrl+k commands   ctrl+p go to   ctrl+n capture   ctrl+s note   ? help   q quit
tabs       1-9   tab / shift+tab          views  v / V           refresh  r / R
lists      j k g G ctrl+d ctrl+u   / filter   f filters   @ mine   esc back / clear
rows       enter do it   o open   y copy   e edit   n new   s send to agent   d delete   x stop
PRs        enter review   m merge   M auto-merge   b check out   u update   p runs   F fix
viewers    [ ] or , . chunk   n N match   z Z fold   c comment   a approve   x reject   esc close
agent      ctrl+q back to forge   shift+pgup scroll   ctrl+\ windows
```

## 5. The `vim` preset

Only where vim users would want something different. Anything not listed is the default.

| Action | Default | vim preset | Why |
| --- | --- | --- | --- |
| `palette` | `ctrl+k` | `ctrl+k` `:` | `:` is vim's command line. |
| `top` | `g` `home` | `g g` `home` | `gg`, which frees `g` as a chord prefix (needs chords, D7). |
| `next_tab` / `prev_tab` | `tab` / `shift+tab` | adds `g t` / `g T` | vim tab pages. |
| `quit` | `q` | `q` `Z Z` | `ZZ` (and `:q` through the palette). |
| `*_delete` | `d` | `d d` | `dd` deletes, so `d` alone can't fire by accident. |
| `*_copy*` | `y` | `y y` | `yy`, matching `dd`. |
| `page_down` / `page_up` | `ctrl+d` / `ctrl+u` | adds `ctrl+f` / `ctrl+b` | Full pages, as in vim. |
| viewer search | `/` `n` `N` | adds `?` (search backwards) in viewers | `?` stays help on tabs. |
| `transcript_toggle` | `enter` `z` | adds `z a` / `z A` (with `z o` / `z c`) | vim folds. |
| `close` (viewers) | `esc` `q` | adds `ctrl+[` | Same as esc in vim. |

Without chords (if D7 is "no"), the vim preset shrinks to `:` and `ctrl+f` / `ctrl+b`, and probably
isn't worth having as a preset.

## 6. Platforms

### 6.1 No Alt / Option in the default

macOS terminals type a character (`ƒ`, `∂`) for Option+key unless the user turns on Option-as-Meta:

| Terminal (macOS) | Option by default | Setting |
| --- | --- | --- |
| iTerm2 | types characters | Profiles › Keys › Left/Right Option = Esc+ |
| Terminal.app | types characters | Profiles › Keyboard › Use Option as Meta key |
| Ghostty | Alt on US layouts, characters on others | `macos-option-as-alt` |
| WezTerm | left Option = Alt, right Option types characters | `send_composed_key_when_left/right_alt_is_pressed` |
| Kitty | types characters | `macos_option_as_alt` |
| Alacritty | types characters | `option_as_alt` |

On Linux every terminal sends Alt as an ESC prefix. The proposed default has no Alt binding.
`alt+backspace` stays as an *alias* of `ctrl+w` in capture, because Mac users expect it to delete a
word and it costs nothing where it doesn't arrive. Setup's "Option as Meta" help screen becomes
unnecessary.

### 6.2 macOS Cmd (super) as optional aliases

**How Bubble Tea v2 reports it.** Bubble Tea v2.1.2 asks the terminal for the kitty keyboard
protocol (disambiguation, flag 1) by default, and for xterm modifyOtherKeys too. When the terminal
answers, the program gets a `tea.KeyboardEnhancementsMsg` (`SupportsKeyDisambiguation()`), and keys
then carry `tea.ModSuper`. `msg.String()` spells them `super+k`, with modifiers always in the order
ctrl, alt, shift, meta, hyper, super. forge ignores that message today and enables nothing more.
Plain `tab` and `enter` stay legacy bytes, so nothing breaks if the protocol is off.

**Which terminals send Cmd at all:**

| Terminal | kitty protocol | Cmd reaches the app |
| --- | --- | --- |
| Kitty | yes | yes, unless kitty binds the combo itself |
| Ghostty | yes, on by default | yes, unless Ghostty binds it |
| WezTerm | needs `enable_kitty_keyboard = true` | yes, once that's on |
| iTerm2 3.5+ | apps can opt in | only with Profiles › Keys › Command = "Super"; menu shortcuts win |
| Alacritty 0.13+ | yes | yes, unless Alacritty binds it |
| Terminal.app | no | never |
| GNOME Terminal (VTE) | no (VTE #2601) | never (VTE #2738) |
| **any terminal → tmux** | tmux doesn't speak it (it uses modifyOtherKeys) | **no**: tmux has no super modifier and turns it into Meta, so `cmd+k` arrives as `alt+k` |

**Which Cmd combos are free by default.** Most terminals keep `cmd+k` / `cmd+f` / `cmd+,` for
themselves:

| Combo | Kitty | Ghostty | WezTerm | iTerm2 | Alacritty |
| --- | --- | --- | --- | --- | --- |
| `cmd+k` | clear | clear screen | clear scrollback | Clear Buffer | clear history |
| `cmd+f` | search | search | search | Find | search |
| `cmd+,` | edit config | open config | free (unverified) | Settings | free (unverified) |
| `cmd+p` | **free** | **free** | free (unverified) | Print | free (unverified) |
| `cmd+s` | **free** | **free** | free (unverified) | taken (unverified) | free (unverified) |
| `cmd+e` | **free** | free unless text is selected | free (unverified) | Use Selection for Find | free (unverified) |
| `cmd+1..9` | tab switch | tab switch | tab switch | tab switch | free (unverified) |

To hand a combo to forge:
- **Kitty:** `map cmd+k` (no action).
- **Ghostty:** `keybind = super+k=unbind`. Not `=ignore`, which blocks it from the app too.
- **WezTerm:** `{key='k', mods='SUPER', action=wezterm.action.DisableDefaultAssignment}`.
- **Alacritty:** `{ key = "K", mods = "Command", action = "ReceiveChar" }`.
- **iTerm2:** remap the menu item in System Settings › Keyboard › App Shortcuts. Whether a profile key
  mapping beats a menu shortcut is unverified.

**Proposed aliases.** Every alias is an extra key; the plain binding always works.

| Alias | Same as | Works with a stock config in |
| --- | --- | --- |
| `cmd+p` | `ctrl+p` go to | Kitty, Ghostty (Alacritty, WezTerm likely) |
| `cmd+k` | `ctrl+k` palette | after unbinding clear-screen |
| `cmd+f` | `/` filter / search | after unbinding search |
| `cmd+,` | settings (edit config.yaml) | WezTerm, Alacritty likely; Kitty / Ghostty after unbinding |
| `cmd+s` | `ctrl+s` note (save and close inside it) | Kitty, Ghostty |
| `cmd+n` | `ctrl+n` capture | after unbinding new window |
| `cmd+r` | `r` refresh | Kitty, Ghostty likely |

**Deliberately not aliased:** `cmd+w` and `cmd+enter`. When an alias isn't active (inside tmux, the
query unanswered, or the terminal binding still in place) the combo falls through to the terminal,
and there `cmd+w` closes the tab or window and `cmd+enter` opens a window or goes fullscreen. A
missed `cmd+p` merely does nothing; a missed `cmd+w` closes forge mid-task.

**When aliases turn on:** all four of these hold.
1. `keys.platform_aliases` is `auto` (the default) or `on`.
2. `$TMUX` is unset, because inside tmux super arrives as Meta.
3. The terminal answered the kitty query (`KeyboardEnhancementsMsg` with disambiguation).
4. The OS is macOS. On Linux, super usually belongs to the window manager.

The help overlay and the palette show `⌘K`-style hints only while aliases are active. `forge doctor`
lists which aliases are on and why.

### 6.3 Agent windows (forge's tmux)

**Claude Code's own keys** (from its docs). These are what a root-table binding must not take:

| Taken by Claude Code | |
| --- | --- |
| `ctrl+c` interrupt / exit · `ctrl+d` exit / delete · `ctrl+l` redraw · `ctrl+r` history search · `ctrl+o` transcript · `ctrl+t` to-dos · `ctrl+b` background task · `ctrl+g` and `ctrl+x ctrl+e` external editor · `ctrl+s` stash prompt · `ctrl+z` suspend · `ctrl+_` undo · `ctrl+v` paste image · `ctrl+j` newline · `ctrl+p` / `ctrl+n` history · `ctrl+enter` send now · `ctrl+x …` chords (about 20) · readline `ctrl+a e k u w y` · `ctrl+m` / `ctrl+i` / `ctrl+h` / `ctrl+[` (enter / tab / backspace / esc) | ctrl |
| `alt+p` model · `alt+t` thinking · `alt+o` fast mode · `alt+b` / `alt+f` / `alt+d` word moves · `alt+y` paste history · `option+enter` newline · `alt+up/down` | alt |
| `esc` cancel · `esc esc` rewind · `shift+tab` permission modes · `shift+enter` newline · `tab` complete · `enter` submit · `?` `!` `/` `@` `:` `\`+enter at the prompt | other |
| vim mode: the usual normal-mode keys | vim |

**Free in Claude Code by default:** `ctrl+]`, `ctrl+\`, `ctrl+^`, `ctrl+q`, and `ctrl+f` outside
agent view.

**Proposed agent-window keys** (in tmux.conf; config version bump):

| Key | Does | Notes |
| --- | --- | --- |
| `ctrl+q` | detach / close the popup, back to forge | Mnemonic ("quit back"), layout-safe, needs no terminal setup. tmux runs raw, so XON flow control doesn't apply. |
| `ctrl+]` | same (alias) | The telnet / ssh escape habit. Needs AltGr / Option on German and Nordic layouts, hence the alias role. |
| `shift+pageup` | scroll mode, one page up (`copy-mode -eu`) | The normal terminal scrollback key. `q` / `esc` leave it. On a Mac laptop it's `shift+fn+up`. Kept provisionally; see the fallback below. |
| `ctrl+\` | window tree (all agents) | US layout. Switching from forge (`ctrl+q`, `ctrl+p`) is the main path. |

**Scroll fallback (specified now, ships regardless).** Many terminals scroll their own buffer on
`shift+pageup` instead of passing it on, so scrolling never depends on it alone:
- **Mouse wheel:** forge's tmux.conf already has `mouse on`; wheel-up enters copy mode.
- **Palette command "Scroll agent output"** (`agent_scroll`, on the Sessions tab and in the palette
  from anywhere): attaches the selected session's window and runs `copy-mode -e` immediately, so
  `j` / `k`, `ctrl+u` / `ctrl+d`, `/` search and `q` work as in tmux; leaving copy mode at the
  bottom returns to the live agent.

P6.9 decides after the terminal checks in 6.4 whether `shift+pageup` stays the key, gets replaced
(and with what), or is dropped in favour of the fallback alone.

The status line becomes `ctrl+q back · shift+pgup scroll · ctrl+\ windows`. Setup's key test checks
`ctrl+q` and drops its Option-as-Meta screen. Your own `~/.tmux.conf` snippet for capturing from
anywhere stays optional, with `M-c` shown for people who have Option-as-Meta and `C-^` for those who
don't.

### 6.4 Compatibility

**Y** = works with a stock config · **C** = needs a terminal setting · **N** = not possible ·
**?** = expected but unverified. "In tmux" means forge or Claude running inside a tmux with
`extended-keys on` (forge's own tmux.conf already sets it).

| Binding | iTerm2 | Terminal.app | Ghostty | WezTerm | Kitty | GNOME Term. | Alacritty | in tmux |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| letters, `?`, `/`, `,`, `.`, `<`, `>` (any layout) | Y | Y | Y | Y | Y | Y | Y | Y |
| `[ ] @ # *` on a US layout | Y | Y | Y | Y | Y | Y | Y | Y |
| `[ ] @ # *` on non-US layouts (DE / Nordic, macOS and Linux) | Y / N¹ | Y / N¹ | Y / N¹ | Y¹ | Y / N¹ | Y² | Y / N¹ | as outside |
| `enter`, `esc`, `tab`, `shift+tab`, arrows, `home` / `end`, `pgup` / `pgdown` | Y | Y | Y | Y | Y | Y | Y | Y |
| `ctrl+k`, `ctrl+p`, `ctrl+n`, `ctrl+s`, `ctrl+r`, `ctrl+d`, `ctrl+u`, `ctrl+o`, `ctrl+w`, `ctrl+a` | Y | Y | Y | Y | Y | Y | Y | Y |
| `ctrl+c` force quit | Y | Y | Y | Y | Y | Y | Y | Y |
| `shift+enter` (capture full form) | Y | N | Y | C (kitty protocol) | Y | N | Y | C (`extended-keys`; not with Kitty outside) |
| `ctrl+o` (capture full form, the fallback) | Y | Y | Y | Y | Y | Y | Y | Y |
| `alt+backspace` (optional alias) | C | C | Y (US) | Y (left) | C | Y | C | as outside |
| today's `alt+f`, `alt+enter`, `M-d`, `M-[`, `M-w`, `M-c` | C | C | Y (US) / C | Y (left) / C | C | Y | C | as outside |
| `cmd+p` / `cmd+s` aliases | C (Super) | N | Y | C (kitty protocol) | Y | N | Y? | N |
| `cmd+k` / `cmd+f` / `cmd+,` aliases | C | N | C | C | C | N | C | N |
| agent `ctrl+q` | Y | Y | Y | Y | Y | Y | Y | Y |
| agent `ctrl+]`, `ctrl+\` (US layout) | Y | Y | Y | Y | Y | Y | Y | Y |
| agent `ctrl+]`, `ctrl+\` (DE / Nordic) | N? | N? | N? | N? | N? | N? | N? | N? |
| agent `shift+pageup` | C (iTerm2 scrolls its own buffer) | C (same) | Y? | C (same) | C (same) | C (same) | Y? | Y once inside |

Notes:
- **Non-US layouts** (¹ ²): on German and Nordic layouts `[` `]` and `@` are typed with Option
  (macOS: `⌥5` `⌥6`, `⌥L` on DE) or AltGr (Linux: `AltGr+8` `AltGr+9`, `AltGr+Q` on DE, `AltGr+2`
  on Nordic); `#` is a plain key on DE and `*` is `shift++`. ¹ They type fine while Option types
  characters, which is the macOS default, but become impossible once the user turns on
  Option-as-Meta for that Option key (WezTerm: right Option still composes). ² GNOME Terminal on
  Linux leaves AltGr alone, so they always type. Every one has a layout-safe route: `,` / `.` for
  `[` / `]`, the `f` form for `@` mine only, `#` tags and `*` pin in the palette. The same layout
  caveat is why agent `ctrl+]` / `ctrl+\` are aliases (rows below).
- **`shift+pageup`:** most terminals scroll their own scrollback on it, but in the alternate screen
  (tmux) many pass it through. Each terminal needs a quick check with `cat -v`. The fallback in 6.3
  (mouse wheel plus "Scroll agent output") ships either way; P6.9 decides on the key after the
  checks.
- **Kitty outside tmux:** Kitty doesn't support modifyOtherKeys, so tmux running in Kitty gets no
  extended keys and `shift+enter` doesn't arrive. `ctrl+o` covers it.
- **Unverified cells:** the "?" cells and iTerm2's menu shortcuts can be settled by running
  `printf '\e[>1u'; cat -v` and pressing the key in each terminal. That's a P6.9 task before the
  defaults ship.

## 7. Discoverability

What a new user should see without opening any docs:

1. **Status bar, contextual.**
   - The left side shows up to four keys for the *selected row's state*, not a fixed list. Examples:
     a red PR shows `enter review · F fix · p runs · o open`; a waiting session shows
     `enter attach · t transcript · s send pack`.
   - The right side always shows `ctrl+k commands · ? keys`.
   - In filters, forms, pickers and viewers, the bar switches to that mode's keys (it partly does
     this today).
2. **Help overlay (`?`).**
   - Grouped by *verb* in the principles' order, so "where is delete" has one answer.
   - Three columns: this tab / global / viewers this tab can open.
   - Typing filters it.
   - It shows Cmd aliases only when they're active.
   - It's generated from the same registry as `forge keys --dump`, so the two can't drift.
3. **Palette (`ctrl+k`).**
   - Every item shows its key on the right. Fill the empty hints, for example `note_standup` shows
     "ctrl+s, s".
   - Commands for the current tab rank first.
   - Typing a key (for example `F`) as the query finds what that key does here.
4. **Go to (`ctrl+p`).** One box for tabs, projects, sessions, PRs, issues and docs, with group
   headings and number hints for tabs.
5. **Unbound key hint.** Pressing a letter with no action here shows a quiet hint once per session,
   for example "`x` does nothing here · ctrl+k to search actions". No moved-key notices: with no
   public release there are no old habits to redirect.
6. **First run.** Setup's last screen and the first Home view show the glance card from 4.1. Agent
   windows show `ctrl+q back` in the tmux status line from the first attach.
7. **The empty states** of each tab name their primary key, for example "No sessions · n to start
   one".

## 8. Preset system (design for P6.9)

**Config:**

```yaml
keys:
  preset: default        # default | vim | <name of a file in ~/.config/forge/keys/>
  platform_aliases: auto # auto | on | off  (Cmd aliases, part 6.2)
keybinds:                # per-action overrides, as today (action id -> keys)
  pr_merge: [m]
```

**User presets** live in `~/.config/forge/keys/<name>.yaml`:

```yaml
extends: vim             # a built-in or another user preset
bindings:
  palette: [ctrl+k, ":"]
  docs_send: [S]
  pr_toggle_draft: [D]   # give a palette-only action a key
  session_resume: []     # unbind
```

**Built-in presets** are Go data in `internal/ui/keymap.go` (`DefaultKeyMap` becomes
`presetDefault`, plus `presetVim` as a delta on it), so the rule "keybindings only in keymap.go"
holds. The agent-window keys are part of the preset too: tmux.conf is rendered from the resolved
keymap, and a version bump triggers the existing "your tmux.conf is old" notice.

**Resolution order.** Each layer replaces whole key lists per action, never single keys.

1. **Built-in default.**
2. **Preset chain:** the named preset and its `extends` ancestors, applied root first. Cycles and
   unknown names are config errors, and depth is capped at 4.
3. **`keybinds:` overrides** from config.yaml.
4. **Platform aliases.** They are *added* to the resolved keys and never replace any. An alias that
   would clash in a scope is dropped silently and listed by `forge doctor`. Aliases only apply under
   the conditions in 6.2, and they're re-evaluated when `KeyboardEnhancementsMsg` arrives.
5. **Custom commands** (`custom_commands[].key`), checked as today by `SetCustom`.

**Validation.** After step 3, `conflicts()` runs exactly as today: one key per scope, tab keys
mustn't shadow global ones, and errors name the action and the layer that set it ("`keybinds.x` /
preset vim"). Chords (D7):
- A chord's first key mustn't also be a single-key binding in the same scope.
- The resolver keeps a pending-chord state with a 1 s timeout, shown in the status bar ("g…").

**Seeing the result.** `forge keys --dump` prints the resolved map. P6.9 adds a `source` column
(default / preset name / keybinds / alias) and `--preset <name>` to preview a preset without
switching to it.

### 8.1 Keys view (P6.9)

A settings screen for keys, so nobody has to hand-edit YAML to rebind. It's a full-screen overlay
opened from the palette ("Settings: keys"), or from the help overlay (`?`) with `e` "edit keys".
It writes the same config as above (`keys.preset`, `keybinds:` and `~/.config/forge/keys/`), so
YAML and the view stay interchangeable.

- **Presets list (left).** Built-ins (`default`, `vim`) plus every file in `~/.config/forge/keys/`,
  showing which one is active and what each `extends`.
- **Preview (right).** The selected preset as a diff against the active one, listing only the
  actions whose keys differ, grouped by scope, plus the glance card (4.1) rendered for it.
- **Live switch.** `enter` on a preset applies it immediately (the keymap is rebuilt in place, help
  and palette hints update) and writes `keys.preset`.
- **Rebind by pressing a key.** Pick an action (type to filter, `/`) and press `enter`, then
  press the new key. Pressing it again adds it as an extra key instead of replacing. Conflicts
  are checked live with the same `conflicts()` rules (same scope, tab vs global, chord prefixes)
  and show the clashing action, with three choices:
  - **swap**: the other action gets the old key
  - **unbind the other**: it becomes palette-only
  - **cancel**
  
  `backspace` removes a key, and `r` resets the action to the preset's keys. Fixed keys (Huh form
  keys, the hidden demos) are listed read-only. Keys forge can't receive here (super outside the
  alias conditions, Alt where Option types characters) are accepted with a warning.
- **Where changes go.** By default to `keybinds:` in config.yaml, on top of the active preset.
  **Save as preset** writes `~/.config/forge/keys/<name>.yaml` with `extends: <active preset>` and
  only the deltas, then switches to it and clears the moved overrides from `keybinds:`.
- **Agent-window keys** (the `tmux` scope) can be rebound here too. Saving re-renders tmux.conf and
  bumps its version, so the existing "your tmux.conf is old" notice offers the reload.
- **Export cheat sheet.** Writes markdown from the same renderer as `forge keys --dump` (resolved
  keymap, with the source column) or the one-page glance card, to a file or the clipboard.
  `forge keys --dump --preset <name>` does the same from the CLI.

Every one of these is also a palette command ("Keys: switch preset…", "Keys: rebind…", "Keys: save as
preset…", "Keys: export cheat sheet"), so the rule that every action is a palette command holds.

### 8.2 Migrations

None in the database. The config template (`internal/config/default.yaml`) gains
the `keys:` section, and its `# <action>: [...]` comment list is regenerated from the registry, so
`TestDefaultConfigListsActions` keeps it complete.

## Sources

- Claude Code: [interactive mode](https://code.claude.com/docs/en/interactive-mode.md),
  [keybindings](https://code.claude.com/docs/en/keybindings.md),
  [terminal config](https://code.claude.com/docs/en/terminal-config.md)
- [kitty keyboard protocol](https://sw.kovidgoyal.net/kitty/keyboard-protocol/) ·
  [kitty.conf](https://sw.kovidgoyal.net/kitty/conf/) ·
  [kitty default mappings](https://raw.githubusercontent.com/kovidgoyal/kitty/master/kitty/options/definition.py)
- [Ghostty keybinds](https://ghostty.org/docs/config/keybind) ·
  [Ghostty defaults (Config.zig)](https://raw.githubusercontent.com/ghostty-org/ghostty/main/src/config/Config.zig)
- [WezTerm default keys](https://wezterm.org/config/default-keys.html) ·
  [enable_kitty_keyboard](https://wezterm.org/config/lua/config/enable_kitty_keyboard.html)
- [iTerm2 profile keys](https://iterm2.com/documentation-preferences-profiles-keys.html) ·
  [iTerm2 CSI u](https://iterm2.com/documentation-csiu.html)
- [Alacritty config](https://alacritty.org/config-alacritty.html) ·
  [Alacritty bindings](https://alacritty.org/config-alacritty-bindings.html)
- [Apple Terminal keyboard settings](https://support.apple.com/guide/terminal/trmlkbrd/mac)
- VTE: [kitty protocol #2601](https://gitlab.gnome.org/GNOME/vte/-/issues/2601) ·
  [Super combos #2738](https://gitlab.gnome.org/GNOME/vte/-/issues/2738)
- tmux: [Modifier Keys wiki](https://github.com/tmux/tmux/wiki/Modifier-Keys) ·
  [CHANGES](https://raw.githubusercontent.com/tmux/tmux/master/CHANGES) ·
  [tty-keys.c](https://raw.githubusercontent.com/tmux/tmux/master/tty-keys.c)
- Bubble Tea v2.1.2 source: `tea.go` (`KeyboardEnhancements`), `keyboard.go`
  (`KeyboardEnhancementsMsg`), `key.go` (modifier order), `mod.go` (`ModSuper`)
