# Launching sessions

[Back to usage](../usage.md)

## Launcher

The launcher reads configured `projects`, shows their default profile/harness,
and lets you multi-select launches.

The dashboard checks its configuration file on each refresh tick (normally
every two seconds). Valid edits update projects and profiles, including an open
launcher, while preserving the selected project and explicit launch overrides.
An open form or confirmation keeps its settings until it closes. Invalid edits
show a notice and keep the last valid configuration until the file is fixed.
Running agents keep their existing working directory; a changed project path
applies to new launches. Changing `launch_backend` still requires restarting
the dashboard to select a different terminal backend.

Launcher rows likewise show the project title only; profile information stays in
its own badge/metadata columns.

- press `/` to filter live,
- `f` cycles project visibility through each default profile and back to all,
- `g` / `G` jump to the top / bottom,
- `space` toggles selection,
- `enter` launches the selected projects, or the highlighted one when nothing
  is selected. A selected project the `/` filter hides is skipped and the
  launcher says how many; with nothing to launch it stays open and says why,
- `P` pins or unpins the highlighted project so pinned projects stay at the top,
- `s` searches past session transcripts for the highlighted project,
- `C` chooses a **one-time profile override** for the highlighted project,
- `d` chooses a **working directory override** for the highlighted project:
  type a path (`tab` completes directories, `ctrl+u` clears the line) and press
  `enter`. A relative path starts at the project's configured directory; an
  empty line, or the configured directory itself, removes the override. The
  row then reads `(dir: …)`, and launching it, `o`, and `O` all use that
  directory. The override is never saved: it is gone once the launch is sent
  or the launcher is closed. A directory that is not there is refused at the
  prompt,
- `o` opens the directory of every project `enter` would launch: the selected
  projects the filter shows, or the highlighted one when nothing is selected,
  skipping hidden selections and staying open when there is nothing to open.
  Each opens in a shell view of the desktop workspace. In a plain terminal
  only the first opens, in a shell here, and a warning counts the rest.
- `?` shows the keymap,
- `esc` clears an active filter first and leaves the launcher on a second
  press; `q` leaves it in one step. Neither quits Agent Mux — `Ctrl+C` does.

The profile cycle starts at **All** whenever the launcher opens, includes only
profiles that own configured projects, and follows the launcher's alphabetical
profile-group order. It matches each project's default profile and combines
with any active `/` filter.

Pins persist per user across Agent Mux restarts. Pinned and unpinned groups each
retain the launcher's existing profile-then-project ordering, including while a
filter is active.

Overrides are useful for cross-harness launches. For example, a project such as
`sis` can keep `profile = "vta"` as its Claude default while `C` launches that
same project under a Codex profile such as `vta-codex`.

Overrides only affect launcher behavior. They do not rewrite `config.toml`.
The dashboard's own **Launch another session** entry (`a`, then `C`) uses the same profile
picker, but launches from the highlighted running row instead of from the
launcher list. Pressing `enter` in the launcher
is an explicit request for new tab(s): if the selected project/profile is
already running, Agent Mux opens another instance instead of skipping or
focusing the existing tab.

### Launch destinations

| Entry point | Destination |
| --- | --- |
| CLI launch commands | tmux, from any terminal |
| Terminal dashboard | tmux, from any terminal |
| App → Launch Project | Workspace (tmux), shown in embedded Ghostty terminals |
| App → Run a Plan | Workspace (tmux), shown beside the supervisor selection |
| Open an existing live session | Its tmux session; launch preferences do not move it |

The launcher header names its actual destination and its source (`--backend`,
`config.toml`, the workspace host, or the tmux default).
In the launcher, `Ctrl+e` opens configuration. The dashboard reloads valid edits
automatically; an open form or confirmation keeps its captured settings until it
finishes. The backend a dashboard launches on is chosen when it starts, so restart
the dashboard after changing `launch_backend`. Invalid edits retain the previous configuration
and show a notice. Daemon settings still require `amx daemon restart`. The status line names the
highlighted project's effective profile, including a one-time override. App
Settings also reports the CLI backend so an override is visible. The app's
Launch Project menu repeats the destination above the profile groups.

### The tmux backend

The dashboard and the overview run in any terminal by hosting agents on
Agent Mux's private tmux server. The macOS app embeds Ghostty to display
those sessions. The server has its own socket and configuration and never
reads `~/.tmux.conf`.

| Where `amx` runs | New agents launch on | Sessions listed |
| --- | --- | --- |
| any terminal (tmux installed) | tmux | tmux sessions |

`amx overview` and `amx past-sessions` follow the same flag, configuration,
and default precedence.

The launch commands (`amx project launch`, `amx session continue|fork|
fork-worktree|handoff`) take `--backend` too. Without it they launch where
`[defaults] launch_backend` says. The supported value is `tmux`:

```toml
[defaults]
launch_backend = "tmux"
```

Unset or empty also selects `tmux`. Other values are rejected. If tmux
is unavailable, launches report the setup failure. Existing tmux
sessions keep running across app and terminal restarts.

**Which tmux.** The private server needs tmux 3.5 or newer. `amx` uses, in
order: `[defaults.tmux] path` from `config.toml` or `AMX_TMUX`, either of
which is the only one tried when set; the tmux bundled with Agent Mux.app,
verified against its checksums; `tmux` on `PATH`; and a runtime the app
installed earlier. A server that is already running keeps the tmux it was
started with, so installing another one never disturbs live agents.

```bash
amx backend status          # the launch backend, the tmux in use and where it came from, the server, and any problem with its fix
amx backend status --json   # the same as data; --check exits 1 when a tmux launch would not work
amx backend stop --yes      # end the private server and every agent in it; nothing else does
amx close ID --yes          # end one agent and its tmux session, by runtime-session ID
amx send ID "run the tests" # prompt one agent without attaching; --key answers a prompt it is showing
```

A runtime-session ID is `rs-` followed by 32 hex digits. <Tab> completes the
running ones for `attach`, `close`, `send`, and every `--runtime-session` flag,
read from the runtime-session registry alone (no daemon, no tmux), and
`amx session list --live --json` reports each one as
`live.runtime_session_id`. A value that is not one is refused with that hint.

`amx close` kills only the pane its launch tagged with that runtime session,
on the server generation it was registered on. Closing a session whose agent
already stopped cleans up what is left of it, and closing one that is already
gone succeeds with `closed: false` under `--json`.

#### When tmux cannot be used, and what doctor reports

When no tmux can be used, a tmux launch fails with `backend_unavailable` and a
message that names each place tried and what to do:

```text
tmux backend unavailable: no supported tmux found (need 3.5 or newer):
  bundled runtime: not present (this amx was not started by Agent Mux.app)
  PATH: tmux not found
Install tmux 3.5 or newer (`brew install tmux`), set [defaults.tmux] path in config.toml, or run Agent Mux.app, which bundles one.
```

`amx doctor` reads the same setup, and with a daemon running adds what the
daemon is doing for the sessions:

```text
backend: launches go to tmux; tmux 3.7c (bundled, verified); server running with 30 agents in 30 sessions
runtime sessions: 30 live (30 on tmux, 1 server), 4 with a terminal attached, 26 unattached; reconciled 1.2s ago in 15ms (412 passes, 0 failed)
screen captures: 320 read, 2 failed, last good one 1.2s ago (slowest 31ms); tmux clients: 1450 run, at most 2 at once (limit 4), 0 timed out
versions: amx 0.8.0, daemon 0.8.0, go1.26.0, tmux 3.7c (needs 3.5 or newer)
```

The `backend:` and `versions:` lines are read by `doctor` itself, so they are
there with the daemon down. `amx debug status` prints the same `backend:`
line, and with a daemon running the `runtime sessions:` and `screen captures:`
lines, without doctor's findings; it prints no `versions:` line. `doctor --json` carries them as `backend`, `versions`, and `daemon.runtime`.
What they can raise, each under a stable identifier:

| Finding | Severity | Means |
| --- | --- | --- |
| `backend_tmux_unavailable` | failure where launches go to tmux; warning while a server still runs; a note otherwise | No tmux can be used. The message names each place tried and the fix. |
| `backend_server_unreadable` | failure where launches go to tmux, else warning | A server is listening but does not answer. Its agents are not counted and not given up on. |
| `backend_server_binary_unknown` | warning | The running server's tmux is not recorded or no longer exists. |
| `backend_server_keeps_previous_tmux` | note | The server runs the tmux it was started with; a newer one is used after its last agent ends. |
| `runtime_reconcile_failing` | warning | Three or more passes in a row could not read a server or the registry (`failure_class` says which: `timeout`, `command_failed`, `registry_unreadable`, ...). Agents that stop are not noticed until one succeeds. |
| `runtime_reconcile_stale` | warning | No pass finished for thirty seconds past its due time: the daemon's maintenance loop is stalled. |
| `screen_capture_failing` | warning | The last ten screen reads all failed. |
| `tmux_client_timeouts` | note | Some of the daemon's tmux clients timed out since it started. |

The daemon reads the private server once every two seconds, with one tmux
client however many agents there are, and spaces failing passes out to thirty
seconds so a server that does not answer cannot hold its maintenance loop. No
program runs more than four tmux clients at once; the rest wait their turn.


None of this reads a pane. The lines, the JSON, the `daemon.runtime_reconcile`
journal records (one when passes start failing, one when they recover, one for
each pass that found an agent stopped), and a share-mode `amx debug bundle`
hold counts, durations, releases, and words from fixed vocabularies; a bundle
keeps a problem's code and severity and drops its text and every path.

#### What changes for a tmux session

Agent Mux.app embeds the dashboard and the overview as views of its workspace window (see
[the companion app](../companion-app/workspace.md#dashboard-and-utility-views)). Hosted
there they launch on tmux, and what would take over the terminal elsewhere —
attaching to a session, the scrollback pager, a project shell, the editor —
opens as a view of the workspace instead, so the dashboard stays on screen. If
the app is not connected to the daemon they behave as in any other terminal.

What changes for a tmux session, and only for it:

- **Focus attaches.** `enter` hands this terminal to the session
  (`amx attach`), and **F12** detaches and returns to the dashboard or
  overview, which keeps running instead of quitting. The agent is untouched by
  either; attaching from a second terminal takes the view over. A session
  has one owner at a time: the terminal that attached last has the keyboard
  and sets the size, and the size stays as it was when nobody is attached.
- **An agent that stopped is not attached, and not restarted.** `amx attach`
  refuses with `runtime_session_exited` when the agent's process ended (the
  message carries its exit status or signal), `runtime_session_lost` when the
  pane or the tmux server it ran in is gone (a reboot, a killed server), and
  `runtime_session_ended` when the session was closed on purpose. The daemon
  records the same thing by itself within a couple of seconds and stops
  listing the session. `amx attach ID --json` also reports `clients`: how
  many terminals are attached right now.
- **Launching stays put.** A tmux launch opens no tab, so the dashboard does not
  quit: the new row appears on the next refresh.
- **Utility views run in this terminal.** Peek pages the pane's scrollback in
  `less`; open-shell, open-editor, the plan editor, and the worktree shell and
  editor suspend the dashboard, run in place, and return to it.
- **The function keys work in the attached terminal.** The private tmux
  configuration binds the same session shortcuts in a plain
  terminal attached to a session has the same keys: **F1** the keybinding
  reference, **F2** the overview, **F5** and **F6** the icon and color
  pickers, **ctrl+shift+d** the dashboard, and **ctrl+shift+y** Past
  Sessions, each as a popup over the terminal; **F7** and **F9** the
  previous and next session, and **F4** the next one waiting on you; **F8**
  clear, **F10** mute, **F11** mark active; **F3** the scrollback markers;
  **F12** detach. A key acts on the session the terminal is on. In Agent
  Mux.app the main menu takes these keys first, as before. ctrl+shift+d and
  ctrl+shift+y need a terminal that reports modified keys; `amx open-dashboard` opens
  the dashboard popup from anywhere.
- **Going to another session switches the terminal.** F7, F9, and F4 (and
  `amx focus-prev-managed`, `focus-next-managed`, and `focus-next`, which
  they run) take the sessions in the order the dashboard lists them and
  switch the attached terminal to the one chosen. Typed by hand they start
  from `$AMX_RUNTIME_SESSION_ID` or `--runtime-session`, and move the
  terminal attached to that session or the one named by `--client`. The
  terminal that arrives is the session's one owner: any other terminal there
  is detached. It never arrives at an agent that exited or was lost, which is
  refused with the same codes `amx attach` uses. Run from a view the desktop
  workspace hosts, or typed in a session the workspace is showing, the same
  commands ask the workspace to show the session instead; once a plain
  terminal attaches to that session they switch that terminal again. A
  terminal named by `--client` is always switched.
- **The dashboard is a popup over the session.** From an attached terminal
  the dashboard and the overview open over the session you are in, selected
  on it. `enter` on a row switches the terminal to that session and closes
  the popup; `q` closes it and leaves you where you were. A terminal shows
  one popup at a time, so there is one dashboard per terminal. Peek, the project shell, and the editor run
  inside the popup rather than taking over the terminal. `amx` typed in a
  terminal that is attached to nothing is still an ordinary full-screen
  dashboard, where `enter` attaches and F12 comes back. `amx --select
  RUNTIME_SESSION_ID` opens either on a session's row.
- **Sessions are always in sort order.** The order (`[defaults] sort`, pins
  first) is computed, not held by a terminal, and the dashboard, the overview,
  and what the daemon publishes to Agent Mux.app all list sessions in it.

- **The terminal shows the session's state.** The daemon renders the state icon
  ahead of the session's title, and the attached terminal uses that title. A plain terminal
  also gets one line under the pane with the same text on the session's
  color; in Agent Mux.app the sidebar says all of that, and the line stays
  off. The line belongs to whichever terminal attached last. It follows the
  daemon within a couple of seconds, and with no daemon running it keeps what
  was last rendered.
- **The pickers edit a tmux session.** `amx color-picker` and `amx icon-picker`
  open in the terminal they are typed in and edit the session: the one named by `--runtime-session`, else
  the one the command is typed in, else the only one running; with several and
  none named they list them and open nothing. The icon goes into the session's
  title, where its launch already put its harness's `[harness_emojis]` icon,
  and Agent Mux.app shows the one the title carries ahead of the session's
  profile in its sidebar. The color is kept with the session, which starts in its
  profile's: the dashboard draws the
  session's rows in it, Agent Mux.app draws the session's sidebar line on
  it, and a plain terminal's status line is drawn on it. "reset" returns to the
  profile's color. `amx keybindings` opens in place.
- **A tmux session can be given a name.** `amx name-session --runtime-session
  <id> <name>` keeps a name with the session, on one line and at most 64
  characters, and `amx name-session --runtime-session <id> --clear` removes
  the one it has; with neither a name nor `--clear` the command is refused,
  so a slip never erases a name. Agent Mux.app
  shows the name above the session's profile in its sidebar, and names a
  session itself with ⌘R or a right-click on its line.
- **A session can be pinned above every other.** The pin is the `★` in front
  of the session's title, so the dashboard
  and Agent Mux.app list the same pins: the dashboard under a **Pinned**
  heading above Needs you, the app in its **Pinned** group above Needs
  attention. A pinned session that is waiting stays under Pinned; it still
  counts as waiting, and `n`/`N` still reach it. Pin from the dashboard's
  command palette (`:` then *Pin Session*), from the app (⌘. or a right-click),
  or with `amx pin-session [--runtime-session <id>] [--unpin]`.
- **A tmux outage keeps the list.** When tmux stops answering, the tmux rows
  stay as last listed and the dashboard's health indicator (the overview's
  status line) says they are kept. When the tmux server itself is gone, the
  sessions it took stay listed, marked with the error icon, until a server
  answers again; closing such a row dismisses it. Sessions you closed are not
  an outage: the list is simply empty.
- **Looking at a session marks it seen.** A tmux session that came to rest
  unseen is marked seen when a terminal attaches to it (Enter, `amx attach`),
  or when you type in a terminal already attached to it. A terminal that is
  only still attached changes nothing.
- **Continue shows a live conversation.** Continuing a conversation that is
  already running in a tmux session attaches to it rather than resuming it a
  second time. Past Sessions, Plans, Worktrees (fork, reopen, shell, editor),
  and the agent-session-loop Runs view work unchanged.

Restart, close, lock, mute, clear, and mark-active act on the selected tmux session.

### Launching from a shell

The same launch action is available without the dashboard, which is what the
menu bar companion and scripts use:

```bash
amx project list                               # PROFILE TITLE HARNESS DIRECTORY GROUPS in a terminal; piped, headerless tab-separated rows of profile, title, harness, directory as configured, groups, and a problem field ("missing", …) last
IFS=$'\t' read -r profile title _ < <(amx project list | fzf) && amx project launch "$title" --profile "$profile"   # pick a project to launch
amx project list --json                        # [{profile,title,harness,directory,directory_problem?,groups}]
amx project list --missing                     # only projects whose directory is unusable
amx project list --archived                    # only projects marked archived = true, which are otherwise left out
amx project list --group work --harness codex  # filters combine; each flag repeats, ignoring case
amx project launch API                         # one project titled API
amx project launch amx                         # the project titled 🐱amx
amx project launch --title API --profile codex-work --json
amx project launch API --directory ../api-hotfix   # this one session, in another directory
amx project launch API --profile work --launch-profile codex-work   # another harness/profile, without changing the project
```

A project is identified by its profile and title, given as the argument or as
`--title`. An exact title always wins. Otherwise the title still selects a
project when it is the only one whose title reads the same without its leading
emoji or symbols, ignoring case, so `amx` launches `🐱amx`. `--profile` is
needed only when several projects share a title. An ambiguous or unmatched
selector launches nothing and lists the configured projects it could have meant
(`ambiguous_project` or `not_found`, with `candidates` whose `id` is
`profile/title`); an unmatched title lists only the projects whose titles
contain it, when there are any. Like `enter` in the launcher, `project launch` always starts a
new tmux session. Its `--json` data is `{action, changed,
message, project, tab_id, window_id, backend, runtime_session_id}`. `backend`
is where the session launched. `runtime_session_id` is the runtime session a
tmux launch registered, the one `amx attach` takes, and is omitted otherwise.
`tab_id` and `window_id` are omitted for tmux launches.

`--launch-profile PROFILE` uses another configured profile and its harness for
this launch. `--profile` still selects the source project template. The override
does not require a separate project under the target profile and is not saved.

`--directory DIR` starts that one session in `DIR` instead of the project's
configured directory, read from the current directory when relative. The
session keeps the project's title, profile, arguments, and environment, and
`config.toml` is not touched; `project.directory` in the `--json` data is the
directory used.

A project whose directory is missing or is not a directory is never launched,
because the agent would run in the wrong place: `project launch`
refuses with `directory_unusable`, naming the project and the problem, and the
launcher marks the row `(missing)` and its preview's `dir:` line with the
reason. `amx project list --missing` lists every such project.

A project marked `archived = true` stays in `config.toml` but is offered
nowhere: the launcher, `project list`, and `project launch`'s candidates leave
it out, naming it refuses with `project_archived`, and `config validate` and
`doctor` do not warn about its directory. Overseer policy still reads it, so
archiving a project never lifts an `asl_overseer` exclusion.

`amx open-dashboard` opens the dashboard as a popup of a terminal attached
to a tmux session:
`amx open-dashboard` opens it over the terminal used last, selected on the
session that terminal shows, and `--json` reports that terminal as `client`
with `window_id` and `tab_id` zero. A terminal that already shows a popup
keeps it. With no terminal attached anywhere, the dashboard starts in the
terminal the command is typed in, and `--json` refuses with
`no_attached_terminal` instead.
