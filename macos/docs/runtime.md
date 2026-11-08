# App layout, terminals, and runtime

[Back to the macOS development guide](../README.md)

## Layout

| Path | Holds |
| --- | --- |
| `project.yml` | The XcodeGen spec for the `AgentMux` app and the hosted `AgentMuxTests` target. |
| `AgentMux/App/` | `main.swift`, `AppDelegate` (wiring, with one `AppDelegate+….swift` extension per surface: workspace, function keys, actions, feed, amx, notifications, Settings, menu validation), the state it owns as types of their own (`DaemonConnection`: the feed, health polled only while it is not live, daemon start and restart; `AMXLookup`: resolving amx and its bounded retries; `SnoozeController`), and the single-instance check. |
| `AgentMux/Daemon/` | The socket path, line-framed socket, request client, subscription client, and the wire and companion models. |
| `AgentMux/CLI/` | Resolving, describing, and running `amx`: binary resolver, version, `--help` capabilities, action gating, login-shell environment, detached spawn, the shared wording for an amx not yet found or not found (`AMXWording`), and `QueueWorkRunner`, which runs blocking work on a queue and hands the result back on the main actor. |
| `AgentMux/Ghostty/` | The libghostty embedding: the app object and its runtime callbacks, layered configuration, per-surface configuration, the surface view, and its input handling. |
| `AgentMux/Model/` | Pure state: the session store, glyphs, attention cycler, the workspace sidebar and its selection, the Go to… list, project and recent catalogs, snooze, and the ASL response rules. |
| `AgentMux/Shell/` | macOS integration: hotkeys, preferences storage, notification presenter, and the `config.toml` watcher. |
| `AgentMux/UI/` | The status item, menu builder, daemon line, workspace window and its main menu, Preferences window, and ASL answer panel. |
| `AgentMux/Resources/` | `Info.plist` and the asset catalog; `internal/palette/icons.json` is bundled from the repository. |
| `AgentMuxTests/` | XCTest suites, with a fake stream server standing in for the daemon. |
| `scripts/` | `build-ghosttykit.sh`, `build-tmux.sh`, `generate-project.sh`, `build-app.sh`, `sign.sh`, `install-app.sh`, `uninstall-app.sh`, `app-lib.sh`, `test-app.sh`, `remote-ui.sh`, `make-icons.swift`. |
| `GHOSTTY_VERSION`, `patches/ghostty/`, `NOTICE-ghostty` | The GhosttyKit pins, the build patches, and Ghostty's license notice. |
| `GhosttyHarness/` | The Session 2 feasibility harness: a separate app with the wrapper as first ported, linking the same vendored GhosttyKit. Not part of any recipe. |

The operator's guide (install, permissions, runtime setup,
troubleshooting) is [docs/companion-app.md](../../docs/companion-app.md).

## Embedded terminals

`Ghostty.App` owns the one `ghostty_app_t`. `AppDelegate.terminalRuntime()`
makes it the first time a terminal is wanted, and
`applicationWillTerminate` shuts it down.
`main.swift` runs `ghostty_init` once at launch; if that fails the companion
runs without embedded terminals.

- **Surfaces.** `Ghostty.App.makeSurface(_:)` returns a `Ghostty.SurfaceView`,
  an `NSView` hosting one `ghostty_surface_t`. Any number share the app. A
  surface holds its app strongly and the app lists its surfaces weakly, so the
  app is never freed under a surface. `close()` frees the surface, which ends
  the program it ran; releasing the view does the same; `shutdown()` closes
  every surface, then frees the app. A closed view is inert.
- **What a surface runs.** `Ghostty.SurfaceConfiguration` carries a program,
  arguments, environment, working directory, and font size per surface.
  `attachment(amx:runtimeSessionID:environment:)` is `amx attach <id>`, and
  `AppDelegate.attachmentConfiguration(runtimeSessionID:)` fills in the
  resolved amx and the login-shell environment every other amx run gets.
  Ghostty runs a surface's command as `bash -c "exec -l …"` under `login(1)`;
  the command goes through `/usr/bin/env` so the program keeps a plain
  `argv[0]`, and every word is quoted. `TERM`, `COLORTERM`, `TERM_PROGRAM`,
  and `TERMINFO` are Ghostty's and are dropped from a caller's environment.
- **Configuration.** Three layers: Ghostty's defaults, then
  `~/.config/agent-mux/ghostty.conf` if it exists, then the overrides the app
  forces (`confirm-close-surface = false`, and a font size and a theme when
  one is set).
  Ghostty.app's own config is not read; the file may
  `config-file = ~/.config/ghostty/config` to share it. The app configuration
  names no command. `reloadConfig()` rebuilds the layers and hands the result
  to every surface; a manually zoomed surface keeps its zoom until reset.
  Preferences › Appearance › Default zoom saves a size from 6–72 points and
  applies it immediately to all open terminals, including hidden views.
  Reset removes that override and restores the config's `font-size`.
  Preferences › Appearance › Theme picks one of the themes the app bundles
  (or any in `~/.config/ghostty/themes`), optionally another for Dark Mode,
  and recolors every open terminal at once without touching its zoom;
  "From the Ghostty config" removes the override. Colors the config sets one
  by one (`background`, `palette`, …) still win over the theme, as in Ghostty.
  **⌘+** (or **⌘=**) and **⌘−** adjust only the focused terminal;
  **⌘⇧-** restores the saved default. New terminals start at that default;
  shortcut changes are not persisted, but a session's terminal made again
  while the app runs (after the window closed, say) comes back at the zoom
  its last one had (`WorkspaceWindowController.fontSizes`, read through
  `SurfaceView.fontSize`, which is why the overrides force
  `window-inherit-font-size = true`).
  **⌥+** (or **⌥=**) and **⌥−** zoom both sidebars together, including their
  text, controls, and spacing, in 10% steps from 70% to 200%; **⌥0** restores
  100%. Sidebar zoom works while the terminal has focus, is independent of
  terminal zoom, and is saved: Preferences › Appearance › Sidebar zoom shows
  and sets the same value (`amxSidebarZoomPercent`), and the next launch
  opens at it.
- **Callbacks.** Runtime actions are sorted by `Ghostty.disposition(of:)`. The
  app answers quit (passed to `GhosttyAppDelegate`, which ignores it), open
  URL, and open and reload config. A surface-targeted title, working
  directory, pointer, background, notification, bell, hovered link, renderer
  health, child exit, or close request goes to the surface it names and from
  there to its `GhosttySurfaceDelegate`, on the main thread and after
  Ghostty's callback has returned, so a delegate may close the surface in
  response. Secure input, key sequences, and key tables only change the
  surface's state, at once when Ghostty calls on the main thread. Tabs,
  splits, extra windows, and Ghostty's own chrome are refused; workspace panes are
  managed by `WorkspacePaneLayout` and reuse the existing surface for each session.
- **What the workspace does with them.** A bell in the terminal shown beeps
  only while the app is in front and `bell-features` includes `system`; a bell
  in a hidden session or view marks its sidebar line with 🔔 until it is shown
  (or for a minute), and with another app in front either asks the Dock for
  attention. A utility view's program title is its line's second line and
  follows its name in the window title; a session keeps its own name. The
  link under the pointer is shown at the pane's bottom corner. A session
  terminal whose renderer fails is set aside for a placeholder offering
  **Reattach**, which makes a new terminal; a utility view's (the Dashboard,
  the Overview, a shell, editor, or pager) offers **Restart**, which ends its
  program and starts it in a new terminal. Either comes back by itself if
  the renderer recovers. OSC 9 and 777 notifications become app notices (see
  [Notifications](notifications.md#notifications)).
- **Links.** http, https, and mailto links open at once; a file (a `file:`
  URL or a bare path) is shown in the Finder, never opened; any other scheme
  is asked about first (`Ghostty.App.linkDisposition`).
- **Clipboard.** A paste Ghostty finds unsafe, and an OSC 52 read or write,
  is asked about in a sheet on the terminal's window naming the view or
  session that asked, with the whole text in a scrollable preview. The
  request is always answered, with nothing when denied or unreadable, and
  dropped only if its terminal closed meanwhile.
- **Drops.** Dropped files and URLs arrive as shell-escaped words; dropped
  text of more than one line goes through Ghostty's paste (and so its paste
  protection and bracketed paste) without touching the clipboard.
- **Secure Keyboard Entry.** A program's `SECURE_INPUT` request holds one
  system reference per surface, only while that surface has the keyboard,
  and gives it back on unfocus and close (`Ghostty.SecureInput`).
- **Keys.** While Ghostty is in a key sequence or key table, the menu claims
  none of its keys. ⌘Page Up and ⌘Page Down page like ⇧Page Up and ⇧Page Down
  (hidden menu aliases), so in a session view they page the server's
  history. A hidden terminal is resized when it is shown, not on every frame
  of a live resize. QuickLook is placed at the selection, as upstream does.
- **Exit.** A surface with its own program stays open after the program exits;
  the delegate hears `surfaceChildDidExit` and closes it. No exit status is
  reported, because Ghostty only sees `login(1)`'s.
- **Scale, visibility, focus.** A surface follows its window's backing scale
  and screen. It stops rendering when its window is not visible, when it or an
  ancestor is hidden, or when it leaves a window; the terminal keeps running.
  Focus is first responder in the key window, so at most one surface in a
  window has it.

`AgentMuxTests/Ghostty*Tests.swift` drive real surfaces in offscreen windows:
several at once, each with its own program, environment, and directory, freed
in any order, with the programs' pids checked afterwards.
## Which amx the app runs

`AMXBinaryResolver` picks the first match:

1. **Override.** The `amxBinaryPath` default:
   `defaults write io.github.sglavoie.agentmux amxBinaryPath /path/to/amx`. An
   override that names no executable is an error. The app does not fall back
   to another amx.
2. **Bundled.** The copy bundled in the app.
3. **`~/.local/bin/amx`.** Where `just install` puts it.
4. **`PATH`.** The first `amx` on the login shell's `PATH`.

When both a bundled copy and `~/.local/bin/amx` exist, the local copy wins in
four cases:

- **It matches the daemon.** It is the build the running daemon reports in
  `health.build`. The content hash decides when both sides carry one;
  otherwise version, revision, and dirty marker must all agree.
- **It is newer.** Its `built` time is later. That field is `vcs.time`, the
  commit time, not the link time.
- **It is the same clean build.** Both are clean builds of one version and
  revision, so they differ only by the signature `scripts/sign.sh` puts on the
  bundled copy. The local copy is the CLI you run, so a daemon the app starts
  is one `amx doctor` does not report as a `build_mismatch`.
- **It wins a tie.** On equal commit times, which two dirty builds of one
  commit share, it wins when its content differs and its file was written
  more recently.

Preferences shows which binary was chosen and why. When none is found, the
menu and every notice say so in one line ("amx not found — set it in Settings
› amx binary"); Preferences and the log keep the full message with every path
tried.

## How the app runs amx

- **Environment.** The app is launched by Finder or launchd, so it does not
  inherit your shell's environment. It asks your login shell (`$SHELL -l -i`)
  for `PATH`, every `XDG_*` variable, and every `AMX_*` variable, so
  `AMX_CONFIG` and friends match the terminal's.
- **Output.** `--json` output is decoded through the schema-v1 envelope.
  Unknown fields are ignored. A data envelope with a nonzero exit is a partial
  success. The error envelope's `code`, `candidates`, and `parameters` are kept.
- **Capabilities.** The app reads `--help` once per binary to learn which
  commands the chosen amx offers (`project`, `open-dashboard`,
  `notifications snooze`, `mute-toggle --json`). An older amx refuses an
  unknown command, so the app greys out what is missing, with the reason as
  the item's tooltip, rather than failing when it is used.

## The live feed

The app holds one companion subscription open to
`~/.local/share/agent-mux/daemon.sock`, the same path `ipc.SocketPath`
computes (`Daemon/SubscriptionClient.swift`). It sends `subscribe` as the
client `Agent Mux.app` with its version and the `runtime_sessions` feature,
reads the ack, and then reads frames. Every snapshot frame replaces the menu's
state wholesale.

- **Reconnect.** When the stream ends, the client dials again. The delay
  doubles from 0.5s to 10s and drops back to 0.5s once a subscription is
  acknowledged. After a `bye` (such as `amx daemon restart`) it retries at
  0.5s, so a restart reconnects without relaunching the app. For 5 seconds
  after a `bye` a daemon that is not running is taken to be restarting: the
  last sessions stay listed under `reconnecting · the daemon is restarting`
  rather than the menu emptying.
- **Reconnect now.** A wake from sleep, **Start daemon**, and a health check
  that finds the daemon answering while the feed is not live each dial at
  once and start the backoff over. After **Start daemon** the app redials
  every 0.25s for up to 5 seconds, until the feed is live.
- **Heartbeat watchdog.** The daemon sends a heartbeat every 15s (the ack's
  `heartbeat_ms`). If no frame arrives for two intervals, the client treats the
  socket as half-open and reconnects. A wake from sleep reconnects at once.
- **Tolerant decoding.** Unknown fields are ignored, and a field of an
  unexpected type falls back to its default. Unknown frame types are skipped.
  A line that is not a frame is counted and skipped.

The status item's title shows only the non-zero counts, with the glyphs from
`internal/palette/icons.json`, which is bundled as a resource: `⚡2 🛑1 🔑1`.
The menu lists the sessions in three sections:

- **Needs attention**: rows like `🔑 api · claude · 4m · permission`, in the
  order the next-attention shortcut visits them (`AttentionCycler.ordered`:
  the workspace sidebar's order, dragged or not).
- **Working**: the `bolt`, `subagent`, and `compact` sessions.
- **Idle**: everything else. More than 10 rows collapse into a submenu.

## The daemon line

The menu's first line says where the feed stands:

| Line | Meaning |
| --- | --- |
| `ready · 0.8.0 (52189e529adf-dirty)` | The feed is live and the daemon is ready. Shows its version and revision. |
| `degraded · <reason>` | The daemon answered but is not ready. |
| `not running` | There is no socket, or nothing accepts on it. A **Start daemon** item follows. |
| `reconnecting · <reason>` | The stream ended; the client is dialing again. When the daemon stopped answering, or sends sessions the app cannot read, a **Restart Daemon** item follows. |
| `unresponsive · <reason>` | Something accepted the connection but did not answer in time. |

Reasons are operator phrases, never socket errors: "the daemon is not
answering", "the daemon is restarting", "too many app connections — quit
other copies" (`subscriber_limit`), "the daemon is shutting down". The raw
error goes to the log (`log stream --predicate 'subsystem ==
"io.github.sglavoie.agentmux"'`). Whenever the feed is not live the menu bar
icon is dimmed.

A notice (a refusal, "Starting the daemon…") stays in the menu until the
menu has been opened on it and at least 30 seconds have passed. A progress
line ("Starting the daemon…", "Launching API…", "Restarting the daemon…")
instead gives way to its outcome as soon as there is one. An open menu is
rebuilt whenever what it shows changes.

A Launch Project, Recent, or Restart Daemon that fails says so at once: the
menu line names the failure, and the app posts its own notification (never
seen by the daemon) when app notifications are on and allowed, else beeps
and blinks the menu bar icon.

A daemon older than the companion protocol answers `subscribe` with
"unknown request type". The menu then says the daemon is too old for the app
and offers **Restart Daemon**, which runs the resolved `amx daemon restart`
and redials the feed once it returns. The app falls back to a `health` request every 10
seconds and on every menu open, and it retries `subscribe` every 10 seconds.

The app never starts the daemon on its own. **Start daemon** runs the resolved
`amx daemon start` in a session of its own, detached from the app, only when
the operator chooses it. Its standard error goes to `daemon-start.log` in the
app's caches (`AMX_DAEMON_DETACHED=1` keeps the daemon's own log in its
journal). When it exits with a failure, or the feed is still down 10 seconds
later, "Starting the daemon…" becomes "The daemon did not start — run amx
doctor", and the last lines of that file go to the log, not the menu.

## Focus

A session row opens the workspace on its embedded tmux terminal. The app can
request `focus_session` by runtime-session ID when a daemon-directed focus is
needed; a stopped session receives `stale_target`. The row's ⌥ **Mute** runs
`amx mute-toggle --runtime-session ID --json`, as F10 does in the workspace.

## Menu actions

After the sessions, the menu offers these (`Model/ProjectCatalog.swift`,
`Model/RecentSessions.swift`, `Model/NotificationSnooze.swift`,
`CLI/ActionGate.swift`):

| Item | Does |
| --- | --- |
| Open Dashboard | What the ⌃⌥⌘A shortcut does. |
| Next Waiting Session | What the ⌃⌥⌘N shortcut does, and continues the same cycle. |
| Launch Project ▸ | Lists configured projects by profile from `amx project list --json`; choosing one runs `amx project launch --title T --profile P --backend tmux --json` and opens the returned runtime session in the workspace. |
| Recent ▸ | Lists recent conversations from `amx session list`; choosing one runs `amx session continue harness:id --backend tmux --json` and opens the returned runtime session. |
| ⌥ on a session row | Runs `amx mute-toggle --runtime-session ID --json` to mute or unmute that session. |
| Notifications ▸ | Snooze for 1 hour, until the next 08:00 (today's before 08:00, titled in the locale's time format and "Tomorrow" only when it is), or until resumed, and Resume Now, all through the daemon's `notification_snooze` request. Quiet hours are shown read-only with the config path they come from, because the app never rewrites `config.toml`. |

A project launch or conversation continuation opens the returned runtime
session in the workspace. When a recent conversation is already running, the
app focuses that session instead of starting another copy.

Items are greyed out when the resolved amx lacks `project` (Launch Project),
`open-dashboard` (Open Dashboard), or `mute-toggle --json`
(the ⌥ Mute alternates).

**Timeouts.** amx runs from the menu are bounded (`CLI/AMXCommand.swift`):
queries and toggles get 10s, `project launch` and `session continue` 120s
(`launchTimeout`), and `close --yes` and `amx send` 30s (`actionTimeout`).
An amx that could not be started is reported as such; one that changes
something and does not answer in time is reported as "amx did not answer
within Ns; the outcome is unknown — check the session list", because the
launch or close may still have happened. Launches, continues, and closes run
on a queue of their own, so a slow one never holds up a click on a session,
and health checks run apart from resolving amx. An answer whose data this app
cannot decode reads "amx answered in a format this app does not understand;
update the app or amx", and the decoding error goes to the log. Notifications is greyed out while the feed is not live, and when
the daemon answers that it has no `notification_snooze`.

**Snooze across daemon restarts.** The daemon keeps the snooze in memory only.
The app mirrors the snooze each snapshot reports into `amxSnoozeUntil`
(`distantFuture` for "until resumed"), and records which daemon reported it as
`pid:started_at` in `amxSnoozeDaemon`. When a snapshot comes from a different
daemon that has no snooze while the stored one has not run out, the app sends
the stored snooze to the new daemon. A snooze cleared on the same daemon, for
example with `amx notifications snooze off`, is adopted rather than undone.

Preferences shows the snooze under General with a Resume Now button, greyed
out while nothing is snoozed. With the feed live it clears the daemon's snooze,
as the menu item does; with the daemon away it forgets the stored snooze, so
nothing is re-applied when a daemon comes back.
