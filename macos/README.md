# Agent Mux.app

A native macOS menu bar companion for Agent Mux. It is an accessory app (no
Dock icon) that talks to the always-on amx daemon over its Unix socket and runs
the `amx` CLI for actions. Kitty stays the terminal for Kitty sessions. For
tmux sessions the app embeds Ghostty (`AgentMux/Ghostty/`, see
[Embedded terminals](#embedded-terminals)) and shows each session's terminal
in its [workspace window](#workspace), beside a sidebar of every live session.

The app shows the live managed sessions, with attention counts in the menu bar,
and a click focuses a session's Kitty tab and brings Kitty to the front. Two
global shortcuts reach the dashboard and the sessions waiting on you from any
app. The daemon's notifications arrive as native banners while the app runs,
and ASL supervisor questions are answered in a native panel.

## Requirements

- macOS 14 or later.
- Xcode (the `xcodebuild` command line tools), `curl`, `shasum`, `unzip`, and
  `ditto`.
- For the first build, or after a Ghostty bump: `git`, `tar`, network access,
  and about 5 GB under `macos/.cache/` to build GhosttyKit (see
  [GhosttyKit](#ghosttykit)). Xcode's Metal Toolchain component is downloaded
  if it is missing.
- The rest of the repository's toolchain (Go, `just`) for a Release build,
  which bundles `amx`.

XcodeGen is pinned in `XCODEGEN_VERSION`, by version and by the sha256 of its
release zip. `scripts/generate-project.sh` uses a `xcodegen` on `PATH` only if
it reports exactly that version. Otherwise it downloads the release zip,
verifies its checksum, and unpacks it into `macos/.cache/`. To bump XcodeGen,
change both values together.

## Recipes

None of these is part of `just check`, `just acceptance`, or
`just paired-release-gate`. Those gates must run on a machine without Xcode,
and `cmd/release`'s `TestMacAppRecipesStayOutOfTheGates` keeps it that way.

| Recipe | What it does |
| --- | --- |
| `just mac-app` | Debug build to `macos/build/Agent Mux.app`. It bundles `amx` only when `AMX_BINARY` names one, and always the tmux runtime. With `AMX_MAC_TEST_HOST` set, Xcode runs on that Mac and the product is brought back. |
| `just mac-app Release` | Runs `just build`, then builds Release and bundles that `amx` at `Contents/Resources/bin/amx` and the tmux runtime at `Contents/Resources/tmux`. |
| `just mac-test` | Runs the hosted `AgentMuxTests` XCTest suite (`xcodebuild test`). `just mac-test Release` runs it against the optimized app, built with testability into `macos/DerivedData/ReleaseTests`. A run that executes no test fails. With `AMX_MAC_TEST_HOST=user@host` set the suite runs on that Mac instead (see below). |
| `just mac-ghosttykit` | Builds the pinned GhosttyKit into `macos/Vendor/`. `mac-app` and `mac-test` run it themselves; it does nothing when the vendored build is current. |
| `just mac-tmux` | Builds the pinned tmux runtime into `macos/Vendor/tmux/`. `mac-app` runs it itself; it does nothing when the vendored build is current. |
| `just mac-install` | Release build, then `scripts/install-app.sh`: a first install or an update of `/Applications/Agent Mux.app` that leaves the session server and its agents running (see [Install, update, uninstall](#install-update-uninstall)). |
| `just mac-uninstall [flags]` | `scripts/uninstall-app.sh`: removes the app. Agents keep running and data stays unless `--end-sessions` or `--remove-app-data` says otherwise. |
| `just mac-release-gate [flags]` | The app's release gate: builds the Release bundle, checks it with `scripts/release-gate.sh` (seal, tmux runtime, that its `amx` is this checkout's, how it is signed), runs the hosted suite against the Debug and the Release app, and writes an evidence record. It installs nothing. With no Xcode to be had the record says `pending` and the recipe fails. See [Desktop releases](../docs/desktop-releases.md). |

The project file `macos/AgentMux.xcodeproj` is generated from `project.yml` and
is gitignored, so edit `project.yml` instead. `macos/build/`,
`macos/DerivedData/`, `macos/Vendor/`, and `macos/.cache/` are gitignored as
well, so a build leaves `git status` clean for the paired release gate.

### GhosttyKit

The embedded terminal is libghostty, linked statically as
`GhosttyKit.xcframework`. Nothing prebuilt is committed or fetched:
`scripts/build-ghosttykit.sh` builds it from source and vendors it, with
`ghostty.h` and the resources the app bundles (themes, shell integration,
terminfo), into `macos/Vendor/`. The build reads no checkout outside this
repository.

- **Pins.** `GHOSTTY_VERSION` names the Ghostty tag, the commit that tag must
  resolve to, the Zig version, and the checksums of the Zig tarballs. A tag
  that resolves to another commit is refused.
- **Patches.** `patches/ghostty/*.patch` are applied to a clean checkout of the
  pinned commit before every build. Each exists because the pinned tag needs
  it on a current Xcode; re-check them on a bump.
- **Stamp.** `Vendor/.stamp` records the tag, commit, Zig version, target, and
  a digest of the patches. A matching stamp skips the build, so changing any
  pin or patch rebuilds. `GHOSTTYKIT_FORCE=1` rebuilds regardless.
- **Zig.** A `zig` on `PATH` is used only if it reports the pinned version,
  then `mise` if installed, else the checksummed tarball unpacked into
  `macos/.cache/zig/`. On an SDK whose stubs Zig cannot link against, the
  script builds an SDK shim under `macos/.cache/sdk-shim/` for host tools.
- **Target.** `GHOSTTYKIT_TARGET=native` builds for this Mac's architecture
  only; the default `universal` is what a distributed app needs.

The first build takes several minutes. The pins, patches, and script began as a
copy of [sglavoie/tt](https://github.com/sglavoie/tt)'s and are kept
byte-identical to its pins and patches while both embed the same tag.

The Swift sources under `AgentMux/Ghostty/` are ported and trimmed from
Ghostty's macOS app. `NOTICE-ghostty` carries its MIT license and is bundled
into the app.

### The tmux runtime

The app ships the tmux its session server runs, so a Mac needs nothing
installed. `scripts/build-tmux.sh` builds it from the release tarballs pinned
in `macos/TMUX_VERSION` (tmux, libevent, ncurses, utf8proc, jemalloc), each
checked against its sha256 before it is unpacked, with no pkg-config lookup
and a `PATH` of system directories only, so nothing from a package manager
can be linked. The four libraries are linked statically: the result is one
executable that loads only `/usr/lib` (the script fails if `otool -L` says
otherwise, or if the binary does not report the pinned version), built for
this Mac's architecture and the app's deployment target. jemalloc is there
because tmux asks for it on macOS, whose `calloc` it found unreliable; the
whole archive is force-loaded so its zone registration is linked in. A
matching stamp in `macos/Vendor/tmux/.stamp` (versions, architecture, a digest
of the pins and of the script) skips the build; a cold one takes a few
minutes. Terminal descriptions come from the system's terminfo database.

`build-app.sh` copies `bin/tmux`, `VERSION`, and `licenses/` to
`Contents/Resources/tmux`. `sign.sh` signs the executable and then writes
`SHA256SUMS` over every file of the runtime, so the checksums are of the
signed files and are themselves under the app's seal.

At run time `internal/tmux` (`runtime.go`) decides which tmux a server runs:

1. `[defaults.tmux] path` in `config.toml`, else `AMX_TMUX`. An explicit
   choice is final: if it cannot be used nothing else is tried.
2. The bundled runtime: the directory `AMX_TMUX_RUNTIME` names, which the app
   sets for every amx it runs, or `../tmux` beside the directory of the amx
   executable, which is where a bundled amx finds it with no environment at
   all. Every file is checked against `SHA256SUMS`, then the runtime is copied
   to `~/.local/share/agent-mux/runtime/tmux-<version>-<digest>/` and checked
   again there. **The copy is what runs.** A copy that is missing or altered
   is installed again; the directory it replaces is renamed aside, never
   written over.
3. `tmux` on `PATH`, if it is 3.5 or newer: what a standalone amx uses.
4. A runtime an earlier run installed, for an amx started without the app on
   a Mac with no tmux.

**A running server keeps its tmux.** The executable chosen for a server is
recorded beside its socket (`tmux.binary`), and while a server answers on the
socket that executable is used for every client, whatever the list above
would pick now. So an update that ships another tmux changes nothing for live
sessions: neither their server nor the clients that talk to it.
`amx backend status` reports the tmux in use, where it came from, and whether
the running server is on an earlier one.

### Signing

`scripts/sign.sh` signs inside out: the bundled `amx` first (identifier
`io.github.sglavoie.agentmux.cli`), then the app.

- **Default.** The first valid Apple Development identity in the keychain. A
  certificate gives the app a designated requirement that survives a rebuild,
  so a notification permission is granted once rather than after every install.
- **No identity.** Without one the app is signed ad-hoc, and macOS treats every
  build as a new app.
- **`AMX_CODESIGN_IDENTITY`.** Overrides the identity. `-` forces ad-hoc.
- **Xcode's own products.** `build-app.sh` and `test-app.sh` have xcodebuild
  sign with the same identity (`scripts/signing-lib.sh`), so the copies in
  `DerivedData`, the hosted tests' app among them, share the installed app's
  designated requirement. They share its bundle identifier too, and an ad-hoc
  copy would take the privacy grants (Files and Folders, notifications) the
  installed app was given, so macOS would ask again on every switch between
  them.
- **Developer ID.** A Developer ID identity adds the hardened runtime and a
  secure timestamp. With `AMX_NOTARY_PROFILE`, the name of an
  `xcrun notarytool store-credentials` profile, the app is also notarized and
  stapled.

### Other environment

- `AMX_MACOS_CACHE`: the cache root (default `macos/.cache`).
- `AMX_APP_INSTALL_DIR`: the install destination (default `/Applications`).
- `AMX_APP_NO_LAUNCH=1`: install without relaunching.
- `AMX_APP_SOURCE`: the bundle `install-app.sh` installs (default
  `macos/build/Agent Mux.app`).
- `TMUX_RUNTIME_FORCE=1`: rebuild the tmux runtime even when its stamp matches.

### Install, update, uninstall

`scripts/install-app.sh` treats a first install and an update alike, and is
written around one fact: agents run in the session server, which the app
neither contains nor owns.

1. The new bundle is checked before anything is touched: its seal
   (`codesign --verify --strict --deep`), its `amx`, and its tmux runtime
   against `SHA256SUMS`.
2. The new bundle's `amx` is asked for `backend status`. That verifies and
   installs the runtime under the data directory, and reports the running
   server's generation (its PID and start time) and agent count.
3. The app is quit through an Apple event and waited for.
4. The bundle is copied to `.Agent Mux.app.installing` beside its
   destination, verified there, and renamed into place; the earlier copy is
   moved aside first and put back if the rename fails. Nothing is ever copied
   over a bundle in place.
5. The server's generation is read again. If a server was running and it is
   not the same one, the installer exits nonzero: an update that lost agents
   must not report success.

`scripts/uninstall-app.sh` removes the bundle and nothing else by default.
`--end-sessions` ends the session server (`amx backend stop --yes`, asked of
the installed bundle's amx before it is removed); without it the script
reports how many agents it left running and prints the command that ends them
with the recorded tmux alone. `--remove-app-data` removes the app's defaults
domain, its saved window state, and the installed runtime, and is refused
while a server runs from that runtime unless `--end-sessions` is given too.
Neither script touches `~/.config/agent-mux` or anything else under
`~/.local/share/agent-mux`. `scripts/app-lib.sh` holds what the two share.

### Icons

The PNGs in `AgentMux/Resources/Assets.xcassets` are committed. They are drawn
from code by `scripts/make-icons.swift`, so rerun it only when the artwork
changes:

```sh
swift macos/scripts/make-icons.swift
```

### Notarizing a release build

Notarization needs a Developer ID Application certificate and a notary
credentials profile, stored once:

```sh
xcrun notarytool store-credentials agentmux-notary \
  --apple-id you@example.com --team-id TEAMID --password APP_SPECIFIC_PASSWORD
AMX_CODESIGN_IDENTITY="Developer ID Application: …" \
  AMX_NOTARY_PROFILE=agentmux-notary just mac-app Release
```

`sign.sh` then submits the signed app, waits for the verdict, and staples the
ticket. An Apple Development build is enough for your own machine; notarize
only to hand the app to someone else.

## Layout

| Path | Holds |
| --- | --- |
| `project.yml` | The XcodeGen spec for the `AgentMux` app and the hosted `AgentMuxTests` target. |
| `AgentMux/App/` | `main.swift`, `AppDelegate` (wiring, with one `AppDelegate+….swift` extension per surface: workspace, function keys, actions, feed, amx, notifications, Settings, menu validation), the state it owns as types of their own (`DaemonConnection`: the feed, health polled only while it is not live, daemon start and restart; `AMXLookup`: resolving amx and its bounded retries; `SnoozeController`), and the single-instance check. |
| `AgentMux/Daemon/` | The socket path, line-framed socket, request client, subscription client, and the wire and companion models. |
| `AgentMux/CLI/` | Resolving, describing, and running `amx`: binary resolver, version, `--help` capabilities, action gating, login-shell environment, detached spawn, the shared wording for an amx not yet found or not found (`AMXWording`), and `QueueWorkRunner`, which runs blocking work on a queue and hands the result back on the main actor. |
| `AgentMux/Kitty/` | Choosing a Kitty socket and bringing the owning Kitty forward. |
| `AgentMux/Ghostty/` | The libghostty embedding: the app object and its runtime callbacks, layered configuration, per-surface configuration, the surface view, and its input handling. |
| `AgentMux/Model/` | Pure state: the session store, glyphs, attention cycler, the workspace sidebar and its selection, the Go to… list, project and recent catalogs, snooze, and the ASL response rules. |
| `AgentMux/Shell/` | macOS integration: hotkeys, preferences storage, notification presenter, and the `config.toml` watcher. |
| `AgentMux/UI/` | The status item, menu builder, daemon line, workspace window and its main menu, Preferences window, and ASL answer panel. |
| `AgentMux/Resources/` | `Info.plist` and the asset catalog; `internal/palette/icons.json` is bundled from the repository. |
| `AgentMuxTests/` | XCTest suites, with a fake stream server standing in for the daemon. |
| `scripts/` | `build-ghosttykit.sh`, `build-tmux.sh`, `generate-project.sh`, `build-app.sh`, `sign.sh`, `install-app.sh`, `uninstall-app.sh`, `app-lib.sh`, `test-app.sh`, `remote-ui.sh`, `make-icons.swift`. |
| `GHOSTTY_VERSION`, `patches/ghostty/`, `NOTICE-ghostty` | The GhosttyKit pins, the build patches, and Ghostty's license notice. |
| `GhosttyHarness/` | The Session 2 feasibility harness: a separate app with the wrapper as first ported, linking the same vendored GhosttyKit. Not part of any recipe. |

The operator's guide (install, permissions, Kitty requirements,
troubleshooting) is [docs/companion-app.md](../docs/companion-app.md).

## Embedded terminals

`Ghostty.App` owns the one `ghostty_app_t`. `AppDelegate.terminalRuntime()`
makes it the first time a terminal is wanted, so a companion used only beside
Kitty never starts Ghostty, and `applicationWillTerminate` shuts it down.
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
  Preferences › Workspace › Default zoom saves a size from 6–72 points and
  applies it immediately to all open terminals, including hidden views.
  Reset removes that override and restores the config's `font-size`.
  Preferences › Workspace › Theme picks one of the themes the app bundles
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
  text, controls, and spacing; **⌥0** restores their original size. Sidebar
  zoom works while the terminal has focus and is independent of terminal zoom.
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
  [Notifications](#notifications)).
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

## Workspace

**Open Workspace** in the status menu shows the workspace window
(`UI/WorkspaceWindow.swift`): a native sidebar of the live sessions beside the
terminal of the selected one. Opening the app again while it runs (Finder,
Spotlight, `open`) shows it too, and Preferences › Open the workspace at
launch (`amxOpenWorkspaceAtLaunch`) opens it at launch. While the window is open the app is a regular app, with a
Dock icon and a main menu (`UI/WorkspaceMenu.swift`); closing it returns the
app to the menu bar accessory. The status item, the global shortcuts, and the
ASL answer panel work the same either way.

- **Which sessions.** The app subscribes with the `runtime_sessions` feature,
  so the daemon sends v2 snapshots: Kitty sessions as before, plus the
  sessions on other backends, each with its runtime identity
  (`CompanionSession.runtime`). A daemon that does not know the feature keeps
  sending v1 and the sidebar lists Kitty sessions only. A session is
  identified by its Kitty socket and window, or, when it has no window, by its
  runtime-session ID (`AttentionCycler.Key`).
- **Sidebar.** `Model/WorkspaceSidebar.swift` builds it as data: one group per
  project, sorted by name, and under it one row per session labeled with its
  profile, sorted by profile. A row shows the state glyph the tab title and
  the menu use, the harness, branch, age, and the reason it waits; a Kitty
  session is marked `Kitty`. A project's line counts its sessions that wait on
  you. Sessions needing attention temporarily move into **Needs attention**
  above the pinned and project groups, even when their project is collapsed.
  Like Overview, this includes permission requests, errors, questions, and
  unread completed turns. When the daemon marks a completion seen or resolves
  the waiting status, the session returns to its saved position. Its terminal,
  selection, pin, and dragged order are preserved. The temporary section
  always stays expanded, names each session's project, and cannot be dragged.
- **Dragging.** Press a session's row and drag it to move it among its
  project's sessions; the terminals beside it go with it. The order is a
  `WorkspaceOrder`, kept in the `amxWorkspaceSessionOrder` preference, and a
  session launched later follows the dragged ones. Dragging other sessions
  preserves the saved places of those temporarily needing attention. A terminal
  beside a session drags among that session's terminals (`WorkspaceViews.moveTerminal`),
  for as long as the app runs. `dropSlots(forItemAt:)` is the whole rule:
  nothing leaves its project or its session, and the project lines and the
  **Workspace** section do not move.
- **Pinning.** **Pin to Top** in a session's right-click menu, or **Session ›
  Pin or Unpin Session** (⌘.) for the one the pane is on, moves the session out of
  its project into a **Pinned** group listed above every project
  (`WorkspaceSidebar.groups(_:order:)`, `setPinned(_:_:)`); its line then
  names its project. A pinned session needing attention temporarily appears
  in **Needs attention** while keeping its pin. Pinned sessions keep the order
  they were pinned in until they are dragged among themselves, and **Unpin**
  puts one back under its project. The pins are part of the `WorkspaceOrder`, kept in the
  `amxWorkspacePinnedSessions` preference, and everything that follows
  sidebar order (⌘1 to ⌘0, the next-attention shortcut, the inspector's
  waiting list) follows them. Any session can be pinned, a Kitty one too. A
  pin of a session that ended is forgotten the next time a pin changes while
  the feed is live.
- **Selecting and focusing.** Selecting a row (arrow keys, **Session ›
  Select Next/Previous Session**) shows what there is to show and never leaves
  the app: a tmux session's terminal, or a note that the session is a Kitty
  window. Focusing a row (a click, a status-menu row, the next-attention
  shortcut, a banner) goes where the session is: the keyboard moves into the
  embedded terminal, or the daemon focuses the Kitty window and Kitty comes
  forward, exactly as a status-menu click does.
- **Terminals.** Each embedded session has at most one `Ghostty.SurfaceView`,
  running `amx attach <runtime-session-id>`, made the first time the session
  is selected and kept, hidden, while another is shown. A terminal is let go
  when its `amx attach` ends (the session was attached somewhere else, or
  detached with **F12**; the pane then offers **Reattach**, its default
  button, instead of taking the session back by itself; a click on the
  session's line, Return on it, or choosing it in the Go to… list reattaches
  too), when a live snapshot no longer lists its session, or when the window
  closes. None of those ends the agent. A hidden terminal hears of a new
  size, and of a new backing scale, only once it is shown, so tmux is never
  sized for a grid nobody sees.
- **Accessibility.** A terminal is a text area to VoiceOver
  (`SurfaceView+Accessibility.swift`, ported from upstream): its value is
  the screen's text, reread at most every half second, with the selection
  and ranges in UTF-16 units of that text (never Ghostty's grid-cell
  offsets, which `selectedRange()` keeps for the input method).
- **Keyboard with the sidebar hidden.** A hidden sidebar never takes the
  keyboard: when a terminal it would have gone to ends, the pane's terminal,
  the placeholder's button, or the window takes it instead, so the arrow keys
  never switch sessions in a list that cannot be seen.
- **Session colors.** An embedded session's line is drawn on its color
  (`WorkspaceRowView`): the profile's color, or the one F6 picked, in the
  palette's `bright_bg`, with black text; a palette test holds every
  `bright_bg` to a 7:1 contrast with black. The selected line shows the list's own
  selection, with a border in the accent
  color around it (any selected line has one, a view's too). The daemon publishes the color
  as the session's `tab_color` (`#rrggbb`) and reads the pick from the pane
  on every reconciliation pass, so a pick shows within about two seconds. A
  Kitty session's color stays on its tab in Kitty, and the workspace's own
  views (Dashboard, Overview, shells) take none.
- **Harness icons.** An embedded session's line starts with its harness
  icon, ahead of the profile: the `[harness_emojis]` emoji its launch put in
  its title, with nothing to set up, until F5 picks another or clears it.
  The daemon reads the title on every reconciliation pass and publishes the
  configured emoji it starts with as the session's `harness_icon`, so a pick
  shows as soon as a color does. A Kitty session's icon stays on its tab in
  Kitty.
- **Closing from the sidebar.** A session row's × and ⌘W detach its terminal,
  leaving the agent running and listed. Click the row or choose Reattach to
  return. The Session menu and row context menu provide **End Session…** (⌘⇧W),
  which runs `amx close <runtime-session-id> --yes` after confirmation by default.
  Its conversation stays on disk. ⌘W's menu item says **Detach Terminal** on
  a session and **Close View** on a utility view. Closing a utility view ends
  its program, asking first when Ghostty reports it still needs confirmation.
  Turn off **Confirm before closing views or ending sessions** in Settings →
  Workspace to skip both close confirmations immediately, including the same
  actions in menus and the sidebar. This choice persists across launches;
  app quit confirmation remains enabled.
  Kitty sessions, Dashboard, and Overview have no sidebar ×.

- **Losing the daemon.** `WorkspaceState` replaces its rows only from a live
  snapshot. While the feed is down the last rows stay listed, dimmed, with the
  daemon line under them; the selection and every terminal stay as they are,
  since tmux holds the sessions and `amx attach` does not need the daemon.
  The first snapshot after the reconnect then decides what is still there.
  The daemon line is `WorkspaceSidebar.feedNotice` (`Connecting to the
  daemon…`, `Daemon not running`, `Daemon too old — choose Restart Daemon`,
  `Reconnecting… (reason)`). Under it, whatever is listed, the sidebar offers
  **Start Daemon** when it is not running (`workspaceStartDaemon`, the menu's
  `amx daemon start`) and **Restart Daemon** when it is too old, or when it is
  reconnecting for a reason waiting will not cure: it stopped answering, or
  sends sessions the app cannot read (`WorkspaceSidebar.stuckReasons`; the
  menu bar offers the same) (`workspaceRestartDaemon`); a second start or restart is refused while one
  runs. With no rows to hold, the pane says the same instead of "No live
  sessions" (`feedExplanation`) and offers the same action. The inspector
  shows the daemon line above the lines it holds, and its Answer… and action
  buttons wait until the feed is live again.
  A wake from sleep keeps a healthy feed and redials one that was waiting to
  retry; the terminals are local processes and are not touched.
- **Coming back.** `WorkspaceRestoration` (`Model/WorkspaceRecovery.swift`)
  keeps two preferences: whether the window was open (`amxWorkspaceWindowOpen`)
  and where its pane was (`amxWorkspacePlace`: `session:<id>`,
  `kitty:<window>:<socket>`, or `view:dashboard|overview`). The controller
  reports every move (`workspacePlaceChanged`); quitting does not count as
  closing the window. At launch the app reopens the window if it was open and
  calls `restore(_:)`: a view is shown at once; a session is shown once a live
  snapshot lists it **and** `amx attach <id> --json` (which checks and never
  attaches) says no terminal is attached to it. A session that has one is
  restored **Detached**: coming back never takes a session from whoever
  attached meanwhile. The pane says `Restoring` until then.
- **Agents that stopped.** When a live snapshot no longer lists the session
  the pane was on, or the session being restored, the app asks amx what
  became of it (`WorkspaceSessionStatus`) and the pane says **Agent exited**
  (`runtime_session_exited`: its process ended, with the status) or **Agent
  lost** (`runtime_session_lost`: its pane or server is gone). A session whose
  conversation is known (`WorkspaceRow.conversation`, and for the session the
  pane was on at the last quit the `amxWorkspaceConversation` preference)
  offers **Resume from Past Sessions**, which opens a `history` view:
  `amx workspace-view history HARNESS:SESSION_ID`, the TUI started in Past
  Sessions with that conversation selected, where the operator presses `c`.
  Any other is offered the Dashboard. No code path in the workspace launches
  an agent: the view only lists, and the key is the operator's.
- **Main menu.** The terminals offer every key equivalent to the main menu
  before encoding it for their program, so the menu is the list of shortcuts
  the workspace keeps: ⌘C/⌘V/⌘A, ⌘+/⌘-/⌘⇧-, ⌘⇧A (next needing attention),
  ⌘] and ⌘[ (next and previous session or terminal under one), ⌘D (Dashboard), ⌘O
  (Overview), ⌘1 to ⌘0 (the first ten sessions in sidebar order,
  `WorkspaceMenu.sessionKeys`), the same keys with Shift (a Kitty session is
  raised in Kitty rather than selected here, and ⌘⇧] and ⌘⇧[ pass over terminals), ⌘L (back to the place shown before, `toggleRecentPlace`), ⌘P (the Go to… list, `toggleSwitcher`), ⌘B (hide or show the sidebar), ⌘⇧B (hide or show the inspector on the right, `toggleInspector`), ⌘T (one more shell in the home folder each time, Shell 2 and on, `openNewShell`), ⌘↩ (a terminal below the session shown), ⌘R (name the session or terminal shown, `nameShownSession`), ⌘W (close what the pane is on, `closeShown`: a view, or an embedded session attachment as its × does; the agent keeps running; another window that has the keyboard closes instead), ⌘⇧W (end the shown session, with confirmation enabled by default), ⌘M,
  ⌘, and ⌘Q.
- **Two panes.** View and the command palette can split with a session shell or
  native checkout review; the palette can also select another agent. Clicking a
  pane promotes its selection and moves the existing surface between the active
  and inactive containers without reattaching. Header controls show focus; View
  also offers focus-other, swap, and hide-other. Detached and failed surfaces stay
  explicit recovery states in either pane. Hiding a pane preserves its program;
  closing the workspace detaches agents and collapses the split. Review close and
  replacement are blocked during landing. `WorkspacePanesTests` covers these
  transitions and confirms terminal geometry and attachment counts.
- **Go to.** ⌘P shows `WorkspaceSwitcherView` above the pane: a field over
  the places `Model/WorkspaceSwitcher.swift` lists, which are the sidebar's
  sessions in its order and then the views, overlays left out, except that
  the place the pane was on before comes first, so ⌘P then Return goes back
  as ⌘L does. A query keeps the entries that hold every word of it, case and
  accents aside, those whose title starts with it first. Return or a click calls `activate(_:)` for a session and
  `showView(_:focus:)` for a view, what a click on the sidebar line calls, so
  ordinary navigation never launches an agent. A leading `>` switches to actions
  (also opened directly with **⌘⇧P**, **Session › Command Palette…**): launch
  a configured project, focus or resume a recent conversation, open a shell,
  review changes, search history, or use the workspace commands. Unavailable
  actions show their reason and cannot be invoked. The field has the keyboard while the list is
  up; Escape and ⌘P give it back to what had it, and a click elsewhere leaves
  it where the click put it. A terminal under the list claims a click only
  when the window's own hit test lands on it, so a click on a row is the
  list's. A snapshot that changes the places re-filters
  an open list. A query that keeps nothing says **No matches**; the selected
  row is drawn as a focused list's (`WorkspaceSwitcherRowView`), since it is
  what Return goes to; and the list shrinks to stay 16 points above the
  pane's bottom. A terminal, pager, or picker is listed with the name of the
  session it is about (`WorkspaceUtilityView.detail(session:)`), the one it
  was last listed under once that session is gone, and is found by it; the
  sidebar line and its tooltip say the same.
- **Inspector.** ⌘⇧B shows `WorkspaceInspectorView` on the pane's other
  side, built as data by `Model/WorkspaceInspector.swift`. A Waiting row
  carries a `WorkspaceWaitingAnswer` when it can be answered without going
  to its session: `.asl` when `MenuBuilder.waitingBySession` binds a waiting
  run to it, on either backend, else `.prompt` when the workspace embeds it
  and its state is `key` or `question`, else none. The row's **Answer…**
  button reports it through `workspaceAnswer(_:)`, and the app opens the
  panel the menu and the banners open (`answer(_:)`, `answerPrompt(_:)`).
  The inspector decides nothing about sending: the prompt panel runs
  `amx send --look` before it enables a key, and `amx send` refuses a session
  with a supervisor question open. The button refuses first responder, so
  the terminal keeps the keyboard.
- **Session names.** ⌘R and the sidebar's right-click menu
  (`menuNeedsUpdate`, `contextMenuItems(forRow:)`) ask for a name in a sheet
  and run `amx name-session --runtime-session <id> --json -- <name>`, which writes
  the pane's `cc_name`; an empty name removes it. The daemon reads it on
  every reconciliation pass and publishes it as the v2 snapshot's `name`, so
  the line changes with the next snapshot and not before. A named row is
  taller by one line (`WorkspaceRowCellView.nameLabel`, bold, dark blue on
  the session's color and a lighter blue on a dark list). A Kitty session is
  not named here: its item is listed and disabled. A session is called by
  its name, else "project — profile" (`WorkspaceRow.displayName`), in the
  window title, the inspector, and the end, name, and failure sheets.
- **Terminal names.** ⌘R and **Name Terminal…** in the sidebar’s right-click
  menu name terminals opened below sessions and standalone shells. The name
  appears in the sidebar, window and pane titles, Go to… search, and close
  prompts. Clearing it restores the original label. Naming keeps the same
  running process; saved pane layouts retain the name when restored.
- **Right-click menu.** A session's line offers **Name Session…** and **Pin
  to Top**/**Unpin**, and an embedded one also **New Terminal Below**, **Copy
  Runtime Session ID**, and **End Session…** (the same sheet as its ×). An
  opened view, or the Dashboard or Overview while its program runs, offers
  **Close**; shells and terminals also offer **Name Terminal…**. A line with nothing to offer, a heading among them, shows no
  menu (`WorkspaceSidebarView.hasMenu`).
- **Function keys.** F1 to F11 are the menu's too, with no modifier
  (`WorkspaceMenu.functionKeys`, listed under **Session › Function Keys**):
  the keys Kitty binds for its tabs (`internal/kittykeys`), acting on the
  session the pane is on (`shownSessionRow`). A surface declines an unbound
  function key, so AppKit offers it to the menu. F1, F5, and F6 open overlay
  views (`isOverlay`): `amx workspace-view keybindings`, `icon-picker
  <runtime-session-id>`, and `color-picker <runtime-session-id>`, which go
  back to where the pane was when their program ends and are never one of
  ⌘L's two places. F8 and F11 run `amx hook-indicator user_clear|user_active`
  and F10 `amx mute-toggle`, naming an embedded session by `--runtime-session`
  and a Kitty one by window and socket. F3 has no target: it is
  `SurfaceView.toggleMarkers`, the eighth history request, disabled where
  there is no server to ask. F1's list is `kittykeys.Workspace` on the Go
  side, and `TestWorkspaceFunctionKeysMatchTheApp` fails when the two lists
  name different keys.

### Decision inbox, conversation history, and changes

The inspector’s **Waiting** tab has category filters (permissions, questions,
errors, completed turns, other), optional oldest-first order, and **Next waiting**.
Filtering never changes the main sidebar order or the global attention cycle.
**Context** fetches the actual supervisor question or current screen output,
without attaching or sending keys. Context is memory-only and discarded when its
question changes, the feed disconnects, or the inspector closes. **Answer…** still
uses the existing independently authorized answer panels.

**View › Conversation History…** opens a native search and transcript view. Search
uses the same transcript backend as the CLI, with **All history**, **This project**,
and **This conversation** scopes captured from the workspace session when opened.
The automated-session toggle includes ASL and subagent conversations. Results show
matching excerpts; the preview initially loads 50 messages and **Load earlier
messages** adds 100. **Focus live session**, **Resume**, and **Fork** use exact
harness-qualified identities. Resumes and forks from this view open in the workspace.
An error remains visible, and an unknown launch outcome is never retried automatically.

**View › Review Checkout Changes…**, or **Changes…** in the Session inspector,
opens a checkout-wide review. An embedded session supplies its current directory;
otherwise a folder picker selects the checkout. The comparison base defaults to
HEAD and accepts another commit or reference. Files show additions/deletions,
binary status, and untracked status; selecting one loads its unified diff, bounded
to 2 MiB with an explicit truncation notice. **Open in editor** uses the configured
editor in a workspace utility view. Git external diff drivers and textconv do not
run. Changes belong to the checkout, which can be shared by multiple agents.

Managed checkouts additionally show their source branch, ahead/behind counts,
landing verdict, and warnings. **Compare with source** uses the recorded merge base.
**Land…** requires explicit confirmation and uses the existing CLI fast-forward
rules, which recheck clean/inactive status. It retains the worktree and branch.
The review requires the companion `amx session changes` command from this build.

### Native ASL Runs and plan launch

**View › ASL Runs…** uses the complete `asl_snapshot` projection rather than the
companion feed's waiting subset. Questionless pauses remain visible. The selected
run shows its phase, current session, timestamps, blockers, and ASL-provided action
availability. `WorkspaceRunsModel` fetches a fresh row before a control request and
binds confirmation to the original run identity. Unknown outcomes retain the exact
operation ID and parameters for **Retry Receipt**; polling never retries mutations.
Open questions continue to use `ASLQuestionFlow`.

`amx plans status PLAN --json` reads ASL's public status report for plan progress.
It projects recorded verification from the event path ASL reports, filtering to the
exact run and excluding arbitrary output and question prose. Reads are bounded to
the last 4 MiB and 200 verification records, with truncation and missing evidence
reported explicitly. The app rejects details belonging to a different run or plan.

**Run a Plan…** previews `amx plans run --check` with explicit optional project,
profile, worker, review, and commit choices. Only a successful matching readiness
check enables launch; the existing CLI rechecks before creating a tmux supervising
session. A pending, submitted, or uncertain launch cannot be repeated by the form.
The app must be rebuilt with its companion CLI for the progress endpoint.

### Dashboard and utility views

The sidebar's first section, **Workspace**, lists utility views
(`Model/WorkspaceViews.swift`): the Dashboard and the Overview always, and a
shell, editor, or pager while one is open. A utility view is a
`Ghostty.SurfaceView` running `amx workspace-view <kind> [target]`
(`cmd/dashboard/workspace_view.go`); the app passes a kind and a target and
never a command line, and amx decides the program and removes every session
and Kitty variable from its environment before running it.

- **Not sessions.** A `WorkspaceUtilityView` has no `AttentionCycler.Key` and
  nothing about it comes from a snapshot, so it cannot be counted as waiting,
  appear in the status menu, or be reached by the attention shortcut. The
  window keeps `viewSelection` beside `WorkspaceState.selection`; only one of
  the two is ever set.
- **Routing.** The app subscribes with the `workspace_views` feature. The
  embedded dashboard and overview send the daemon a `workspace_view` request
  when they launch or focus a session or want a utility view; the daemon
  validates it and forwards it as a `workspace_view` frame
  (`StreamFrame.workspaceView`), and `WorkspaceWindowController.open(_:)` acts
  on it: a session request focuses that session's own `amx attach` terminal,
  a just-launched one (`await`) is held until a snapshot lists it or
  `awaitLimit` passes, and a utility request opens its view. A request that
  arrives while the window is closed is dropped. If no workspace is connected
  the daemon refuses and the surface shows the view in its own terminal.
- **Lifetime.** A utility terminal is made when its view is first shown. A
  shell, editor, or pager that exits unlists its view and the pane goes back
  to where it was; the Dashboard or the Overview that quits stays listed and
  offers **Reopen**. Closing the window hides utility terminals without ending
  them, unlike attachments.
- **Terminals beside a session.** ⌘↩ (`openTerminalBesideSession`) opens a
  `.terminal` view: `amx workspace-view session-shell <runtime-session-id>`,
  a login shell in the directory of that session's pane, which amx reads only
  while the private server holds the pane for that session. The sidebar lists
  it under the session's row (`reloadSidebar`) rather than in **Workspace**,
  and moves it there if a live snapshot stops listing the session. It is a
  utility view in every other respect: `WorkspaceState.rows` never holds it,
  so the digit shortcuts cannot land on it; `selectNeighbor` walks the
  sidebar's sessions and the terminals under them, so ⌘] and ⌘[ do, and
  with `raising` (⌘⇧] and ⌘⇧[) it walks the sessions alone.
- **The dashboard shortcut.** It shows the workspace's Dashboard unless
  `amxDashboardPlace` is `kitty`. Then `DashboardOpener.destination(for:)`
  sends it to the workspace only when there is no Kitty socket to try; a Kitty
  that answered with a failure, or sockets nobody answers on, are still
  reported. **Launch Project** runs `amx project launch --backend tmux` and
  shows the session in the workspace, unless `amxLaunchBackend` is `kitty`.

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
- **Kitty.** amx learns which Kitty to talk to only from `KITTY_LISTEN_ON`.
  Every invocation strips any inherited `KITTY_*` variables and sets
  `KITTY_LISTEN_ON` explicitly when a command needs Kitty.
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

A row for a session on tmux opens the [workspace](#workspace) on its terminal;
it has no Kitty window, and its ⌥ **Mute** runs
`amx mute-toggle --runtime-session ID --json`, as F10 does in the workspace.
Everything below is for a Kitty session.

A click on a session row sends `focus_window` to the daemon. The request names
a Kitty socket, which the daemon accepts only if it learned that socket itself
(`Kitty/KittyEndpoint.swift`). The app tries these sockets in order, and moves
to the next on `unknown_socket`:

1. the Kitty socket set in Preferences, if any;
2. the session's own socket;
3. the socket the snapshot's window tree was read through;
4. `unix:/tmp/kitty-<pid>` for each running `net.kovidgoyal.kitty` process,
   only when that file exists.

If there is nothing to try, the menu explains that Kitty needs
`allow_remote_control yes` and `listen_on unix:/tmp/kitty` in `kitty.conf`.

Once the daemon has focused the window, `Kitty/KittyActivator.swift` brings
the Kitty process that owns the socket to the front. It finds that process from
the pid in the socket's name, so with two Kitty instances the right one comes
forward. It calls `NSApp.yieldActivation(to:)` and then asks Kitty to
activate. When that is the only Kitty running, it also opens Kitty through
`NSWorkspace.openApplication` with `activates`, which brings a running app
forward even when Agent Mux was never active, as after a global shortcut. With
several Kitty instances that call cannot say which one, so it is skipped. A `stale_target` reply means the session has closed. The row is hidden
until a snapshot no longer lists it, and the menu says so.

## Menu actions

After the sessions, the menu offers these (`Model/ProjectCatalog.swift`,
`Model/RecentSessions.swift`, `Model/NotificationSnooze.swift`,
`CLI/ActionGate.swift`):

| Item | Does |
| --- | --- |
| Open Dashboard | What the ⌃⌥⌘A shortcut does. |
| Next Waiting Session | What the ⌃⌥⌘N shortcut does, and continues the same cycle. |
| Launch Project ▸ | Configured projects, one header per profile, from `amx project list --json`. The list is fetched at launch and again whenever `config.toml` changes (the file `AMX_CONFIG` names for the amx in use, else `~/.config/agent-mux/config.toml`, watched with its directory so an editor's atomic save counts), so config edits reach an open workspace automatically. Opening the menu also refreshes a list older than ten seconds. Each project's directory is checked on disk when the list arrives and every time the menu opens; a project whose directory is missing stays listed but greyed out, with the path in its tooltip, and its ⌥ `… in Folder…` alternate still launches it in a folder you choose. Choosing one runs `amx project launch --title T --profile P --backend tmux --json` and opens the workspace on the runtime session it reports, or, when Preferences launches into Kitty, the same without `--backend` in the Kitty used last. |
| Recent ▸ | The newest `amxRecentLimit` conversations from `amx session list --since <amxRecentDays>d --json`. The list runs in the background with a 15s timeout, and the menu shows the previous result until it lands. Choosing one runs `amx session continue harness:id --backend tmux --json` and opens the workspace on the runtime session it reports, or, when Preferences launches into Kitty, the same without `--backend` in the Kitty used last. |
| ⌥ on a session row | Shows `Mute · …` or `Unmute · …`, which runs `amx mute-toggle --runtime-session ID --json` for a session on tmux, or `amx mute-toggle --window-id N --json` with `KITTY_LISTEN_ON` set to that row's own socket for a Kitty one. |
| Notifications ▸ | Snooze for 1 hour, until the next 08:00 (today's before 08:00, titled in the locale's time format and "Tomorrow" only when it is), or until resumed, and Resume Now, all through the daemon's `notification_snooze` request. Quiet hours are shown read-only with the config path they come from, because the app never rewrites `config.toml`. |

New tabs open in the Kitty that was frontmost last (the app watches
application activation), then the Preferences override, the snapshot's Kitty,
and each `/tmp/kitty-<pid>`. After a launch or a continue, the app asks the
daemon to focus the window amx reported, when it reported one, and brings that
Kitty forward. A Recent conversation that a live session already runs is
focused where it runs, in the workspace or through `focus_window` in
whichever Kitty runs it, instead of asking amx: `session continue` only sees the Kitty it is pointed at, so for a session
in another instance it would resume a second copy. Mute never tries another
socket, because window IDs mean something only inside the Kitty that issued
them.

Items are greyed out when the resolved amx lacks `project` (Launch Project),
`open-dashboard` (Open Dashboard, only when `amxDashboardPlace` is `kitty`;
the workspace's own Dashboard needs no amx command), or `mute-toggle --json`
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

## Notifications

While it runs, the app can be the daemon's notification backend
(`Shell/NotificationPresenter.swift`). The daemon still applies every gate
(quiet hours, mute, snooze, cooldown). It then sends each notification that
passes them as a `notification` frame to the subscriber holding the role, and
the app shows it through `UNUserNotificationCenter`.

**The role.** The app claims the role in `subscribe` while
`amxUseAppNotifications` is on and macOS allows its notifications. It also
claims the role while it has never been asked, because the daemon sends frames
only to a claimant. It asks for permission when the first frame arrives, not
at launch. Turning the preference off, a denied prompt, or permission revoked
in System Settings drops the claim, and the app subscribes again without it.
The app reads the permission again whenever it comes to the front, whenever
the menu opens, and when Settings opens, because macOS gives no notice of a
change.
While it is denied, Settings says so under Use app notifications, with an
Open System Settings button for System Settings › Notifications.

**Fallback.** The app reports `delivered` once `add` succeeds. A failed `add`,
a denied permission, or the preference turned off reports `failed`. The daemon
then sends that notification through Alerter, as it does when no app holds the
role, or when `delivered` takes longer than 3 seconds. That 3-second limit
covers the first permission prompt, so the notification that raised the prompt
usually ends up on Alerter.

**Banners.** Each banner's identifier is the daemon's `notification_id`, and
its thread is the session's runtime session ID when the daemon names one,
else its Kitty socket and window ID, so one session's banners group together.
Permission and question (`elicitation`) banners play the default sound, also
while the app is in the foreground (its menu open); other banners are silent. The
category depends on the actions the daemon offered, and a banner never shows
an action that was not offered:

| Category | Actions | Used for |
| --- | --- | --- |
| `amx.session` | Mute Session, Snooze All for 1 Hour | A session notification, of a Kitty window or a tmux session |
| `amx.session.snooze` | Snooze All for 1 Hour | One with no session to mute |
| `amx.asl` / `amx.asl.snooze` | Answer…, then the offered session actions | An ASL question |
| `amx.prompt` | Answer… | A permission prompt of a tmux session, when the daemon offered `respond` |
| `amx.plain` | none | Every other permission prompt, and any notification offered none |

**Responses.** A click reports `clicked`, and the daemon focuses the pane
exactly as it does for an Alerter click. The app then brings that Kitty
forward. A dismissal reports `dismissed` (every category sets
`customDismissAction`). An action reports `action_mute`, `action_snooze`,
`action_answer`, or `action_respond`, and the app carries it out:
- Mute runs `amx mute-toggle` on that session, unless the session is already
  muted: `--runtime-session` for a session the workspace embeds, else only
  when exactly one listed session has that window ID.
- Snooze sets the same 1-hour snooze of every notification as the
  Notifications menu.
- Answer opens the [ASL answer panel](#asl-answer-panel) on the notice's run.
- Answer on a permission prompt opens the [prompt answer
  panel](#prompt-answer-panel) on the notice's session.

A `notification_retract` frame removes the banner. A retracted notification
that is still waiting on the permission prompt, or on `add`, is never shown.
When the feed's connection ends the daemon ends every delivery it made on it
and can never retract one, so the app removes the banners it showed on that
connection then (`NotificationPresenter.connectionEnded`); the app's own
terminal notices stay. Banners left by an earlier launch are removed at
launch.
The `amx debug tail` dispositions name the `app` backend.

**Terminal notices.** A program's own desktop notification (OSC 9 or 777) in
an embedded terminal is shown as a plain banner titled by its session or view
(`NotificationPresenter.presentTerminal`), only while app notifications are on
and allowed: it never asks for permission, and the daemon never hears of it.
It obeys the snooze (the daemon's, or the mirrored one while the feed is
down) and its session's mute, and is not shown while its session or view is
in the pane of the key workspace window. Its thread is its session or view,
and the app withdraws that thread's notices when the pane moves to it or the
workspace window becomes key on it. Clicking one from a session shows that
session; from a utility view, it opens the workspace on that view, if it is
still open (navigation only: a view never counts toward attention).

## ASL answer panel

An ASL run waiting on its supervisor shows ❓ on its session row, and an
indented **Answer…** item follows that row (`Answer… (3 questions)` when more
than one is open). A run binds to the session that names it, else to the one
listed session with its window ID; a run no listed session claims, or whose
window ID is ambiguous across Kitty instances, is listed under **Supervisor
questions** instead. The `amx.asl` banner's Answer… opens the same panel.

The panel (`UI/ASLQuestionPanel.swift`) floats above other windows and does not
activate the app until the reply editor takes focus. It reads the daemon, never
`amx`:

1. `asl_snapshot` finds the run's row, its permitted actions, and its action
   token. The menu's waiting list carries neither a body nor a token.
2. `asl_question` fetches one question body at a time, as you move between the
   run's open questions.
3. `asl_action` sends each submission once. It is never resent automatically.

The rules are ported from `tui/asl` into `Model/ASLResponseState.swift`, so
the panel and the TUI's Runs view agree:

- A live wait offers **Reply**, **Continue**, and **Pause**. A paused run
  offers only **Reply and Resume**. Reply needs text; ⌘↩ sends it.
- The form opens only when the daemon reports ASL reachable, the snapshot is
  not stale, mutations are enabled, the row carries a token, and the row
  permits the method. Otherwise the panel shows the same fixed sentence the TUI
  shows, including ASL's own `disabled_reasons`.
- A failed submission keeps its operation ID, so retrying the same button after
  an unknown outcome replays the bridge's receipt instead of applying it twice.
  Another button mints a new ID.
- After an accepted closure the panel moves on to the run's next open
  question. That question's fetch re-mints the token, and nothing is sent
  until it has. The last question, or a resume, closes the panel.
- A refusal is shown as a fixed sentence chosen by its bridge code, never the
  bridge's message. A refusal meaning the run moved (a stale token, a closed
  question, a stale snapshot) closes the panel, shows a notice in the menu,
  and rebuilds the menu.
- A question body that cannot be read says "could not load the question",
  never an action's wording, and nothing can be sent until **Load Again**
  has read it. That includes the re-authorizing fetch after a closure.
- A disabled button says why in its tooltip, and the caption under the reply
  says why Reply is: "Type a reply", "Loading…", "Re-authorizing…".
- While a submission is on its way, Close (and Escape, and the title bar's
  close button) wait for it. One whose panel went anyway, because another run
  was opened, still reports how it went as a notice in the menu.

The question body is held only while the panel is open. Drafts not yet sent
are kept, in memory only, per run: closing the panel or opening another run
keeps them, and reopening that run puts them back for the questions it still
has open. They go when the run's questions move: answered, a moved run, or a
run no longer waiting. `SecretText` and the state's `description`, `dump`
mirror, and request descriptions are all redacted, so the logs (category
`asl`) name only methods, run, question, and operation IDs, and refusal codes.

## Prompt answer panel

A permission banner never approves anything: the operator has to see the
prompt before answering it. For a session on the private tmux server the
banner can offer to show it. Its **Answer…** opens a floating panel
(`UI/PromptAnswerPanel.swift`) with the tail of the session's screen and a
strip of keys, and the workspace does not have to come forward or attach.

The panel decides nothing about whether a session may be written to. It runs
two `amx send` invocations and shows what they answer:

1. `amx send <runtime-session-id> --look --json` reads the screen. It runs
   when the panel opens, on **Look Again**, and 0.6 seconds after every key.
2. `amx send <runtime-session-id> --key <name> --json` presses one key.

The rules are in `Model/PromptAnswer.swift`:

- No key can be pressed before a look has answered, or while a key or a look
  is on its way.
- Keys are enabled only while the look says the screen is asking something.
  `amx send` checks the same thing again when it writes, so a prompt that
  went away in between is a refusal and not a stray key.
- The strip is `1`–`9`, `↑` `↓` `↩`, and `y` `n` `esc`. Typing a digit, `y`,
  `n`, or Return in the panel presses that key; Escape closes the panel, so
  the session's Escape is the `esc` button.
- A session running an agent-session-loop run answers the first press with
  "press the key again to confirm"; the second carries `--yes`. A different
  key is asked about afresh.
- A refusal is shown as a sentence chosen by its code, not `amx send`'s own
  message, which names command-line flags.
- A key held with ⌘, ⌃, or ⌥ is left to the menus and never reaches the
  session.
- A first look that fails can be tried again with **Look Again**, and
  reopening the panel on that session starts over. A look you ask for clears
  the notice an earlier key or look left (the look 0.6 seconds after a key
  keeps that key's), and a confirmation asked before it is asked afresh.

The daemon offers `respond` only on a `permission` notice whose target is a
tmux runtime session, and the app shows Answer… only when that was offered
and the notice names such a session. A Kitty session's permission banner is
unchanged: it offers nothing but its click. The logs (category `prompt`) name
sessions, key names, and refusal codes, never screen text.

## Workspace shortcuts

Settings › Shortcuts › Workspace shortcuts records keys for the new View commands and Command
Palette. `WorkspaceShortcut` owns stable IDs, default bindings, and action
selectors. Changes update existing menu items immediately; launch restores them
from `AppPreferences`. These are local menu equivalents and are never registered
with Carbon. Command-only combinations are accepted without weakening the global
shortcut recorder's restrictions. Existing menu keys (including hidden aliases),
other configurable commands, and the two global bindings are checked for conflicts.
Conflicting stored bindings remain visible in Settings but are inactive.

The terminal checks configured equivalents before encoding keys, including
shifted punctuation and Control-Return. It retains Ghostty key sequences and IME
composition handling, and validates the menu action before invoking it. Settings
stops recording when another recorder starts, the selected command changes, the
window closes, or the window loses focus.

## Global shortcuts

Two Carbon hotkeys (`Shell/HotKey.swift`), which need no Accessibility
permission:

| Default | Does |
| --- | --- |
| ⌃⌥⌘A | Shows the workspace's Dashboard. With `amxDashboardPlace` set to `kitty`, runs `amx open-dashboard --json`, which focuses the running dashboard or opens one, then brings that Kitty forward. |
| ⌃⌥⌘N | Focuses the next session needing attention (`Model/AttentionCycler.swift`). |

Sent to Kitty, the dashboard shortcut names a socket the same way focus does: the
Preferences override, the running dashboard's Kitty, the snapshot's, then each
`/tmp/kitty-<pid>`. amx dials the socket itself, so only `kitty_unavailable`
moves on to the next one.

The next-attention shortcut visits the attention rows (stop, key, error,
question) in the order the workspace sidebar lists every session
(`WorkspaceSidebar.ordered`): promoted attention sessions, then pinned sessions
and projects in their saved order. How long a session has waited moves
nothing, and the menu's Needs attention section lists them in the same order.
Each press focuses the one after the session the last
press focused, and wraps after the last one. A session is its Kitty socket
plus its window ID, so the cycle spans Kitty instances. When the session it
last focused no longer waits, or is gone, the cycle carries on from where
that session stood instead of starting over. With nothing waiting, it beeps.

A shortcut that cannot act beeps and leaves the reason as the menu's notice.
Preferences records shortcuts, and a change is registered at once. The
recorder refuses, saying why in its button, a key with no ⌃, ⌥, or ⌘ (other
than F12 to F20), ⌘ alone or with ⇧ (every app's own menu keys), and a bare F1
to F11 (the workspace's keys); `HotKeyCombo.refusal` is the rule. It warns
when a shortcut is already used by the other one (the dashboard's wins), is
held by another app (Carbon's `eventHotKeyExistsErr`), is one macOS owns in
System Settings › Keyboard › Keyboard Shortcuts, which Carbon cannot see, or is
also a key of the app's own main menu. While a recorder listens, both
shortcuts are unregistered so they can be recorded; the recorded one is saved
before the recorder stops, so registering again happens once, with the new
shortcut. Shortcuts are shown with the key the current keyboard layout puts
there (`UCKeyTranslate`; ⌃⌥⌘Q on AZERTY for the stored `ctrl+opt+cmd+a`), and
the status menu's Open Dashboard and Next Waiting Session show their
shortcuts as their keys. Settings and the conflict warnings name each
shortcut by its menu item (`HotKeyPreference.label`).

## Preferences

Preferences (`UI/PreferencesWindow.swift`, SwiftUI in an NSWindow titled
Settings, opened by Settings… in the status menu and the app menu)
saves each change as it is made, in the `io.github.sglavoie.agentmux` defaults
domain. The amx binary path is saved when its field is submitted or left (or
the window closes), not per keystroke, so a half-typed path never replaces the
amx the app runs.
Settings are organized into General, Shortcuts, Appearance, Workspace, and
Advanced tabs. The tabs stay visible while each pane scrolls independently.
Launch Settings opens the Workspace tab at the launch destination. Switching
tabs commits edited binary/environment fields and ends shortcut recording.
An absent, mistyped, or out-of-range value reads as its default.

| Key | Default | Meaning |
| --- | --- | --- |
| `amxBinaryPath` | empty | The amx to run; empty is automatic. Preferences shows the running path and version. |
| `amxLaunchAtLogin` | false | Mirrors the `SMAppService.mainApp` login item, which survives a logout. |
| `amxHotKeyDashboard` | `ctrl+opt+cmd+a` | Empty turns it off. |
| `amxHotKeyNextAttention` | `ctrl+opt+cmd+n` | Empty turns it off. |
| `amxWorkspaceShortcuts` | `{}` | Command ID → combo string; absent uses the command default, empty clears it. |
| `amxStatusStyle` | `glyphs` | `glyphs` (`⚡2 🔑1`), `compact` (the attention total), or `iconOnly`. |
| `amxShowWorkingCount` | true | Includes `⚡N` in the glyph title. |
| `amxRecentDays` | 7 | How far back the Recent submenu looks (1–90). |
| `amxRecentLimit` | 15 | How many conversations it lists (1–50). |
| `amxUseAppNotifications` | true | Offers the app as the daemon's notification backend. |
| `amxSnoozeUntil` | none | The last snooze the daemon reported, re-applied after a daemon restart (see Menu actions). The daemon owns the snooze, and Preferences shows the daemon's while the feed is live. |
| `amxKittySocketOverride` | empty | A Kitty socket tried first by focus and the dashboard shortcut. |
| `amxEnvironment` | empty | Values amx runs with instead of the login shell's, for `PATH`, `XDG_*`, and `AMX_*`. |
| `amxLaunchBackend` | `workspace` | `workspace` uses tmux; `kitty` explicitly uses Kitty; `follow-cli` reads the CLI configuration before project launches and Recent conversation resumes. A stored legacy `automatic` migrates to `workspace`. Unknown or missing values use `workspace`. |
| `amxDashboardPlace` | `workspace` | Where the dashboard shortcut and Open Dashboard show the dashboard: `workspace` (the workspace's own view), or `kitty` (a dashboard tab in Kitty when a Kitty is running, else the workspace). |
| `amxOpenWorkspaceAtLaunch` | false | Shows the workspace window when the app starts, even if it was closed last time. |
| `amxConfirmBeforeClosing` | true | Asks before closing a running utility view (⌘W) or ending a session (⌘⇧W). Changes apply immediately to shortcuts, menus, and sidebar close actions; app quit confirmation is separate. |
| `amxReadingProseSize` | 13 | Prose size in History, Activity and Waiting (11–24 points), independent of code/diff/terminal-preview size. |
| `amxDefaultFontSize` | none | Default terminal zoom in points (6–72); unset uses the Ghostty config. Changes apply to all open terminals; ⌘⇧- resets to this size. |
| `amxTerminalTheme` | none | The Ghostty `theme` every terminal uses, as Ghostty writes it: `Name` or `light:Name,dark:Name`; unset uses the Ghostty config. Changes recolor all open terminals. |

Preferences › Workspace also shows the backend setup, read from
`amx backend status --json` each time amx is resolved
(`Model/BackendSetup.swift`): which tmux runs and where it came from, whether
the session server is running and with how many agents, and every problem amx
reports with what to do about it. A missing tmux is shown there before a
launch fails on it; when a launch does fail, its message carries the same
places tried and the same fix. The app names the bundle's runtime to every
amx it runs through `AMX_TMUX_RUNTIME`, so `~/.local/bin/amx`, when the
resolver prefers it, still runs the pinned tmux.

A change to the binary override or an environment value resolves amx again
once typing pauses; the login shell is not asked twice. The login item is
SMAppService's to keep. At launch the stored choice is set to match it, so a
login item removed in System Settings is not added back.

## Single instance and tests

A second copy of the app exits at launch if one is already running, but first
asks the running one to come forward (a distributed notification,
`SingleInstance.reopenRequest`, answered as a reopen is: the workspace
opens) and activates it, so a double launch is never silently nothing. The tests
run hosted inside the app. When the app sees XCTest in its environment, it
skips the single-instance check and the status item, and it never spawns
`amx`. This means `just mac-test` works while the installed app is running.

`WorkspaceWorkflowPreviewTests` captures fixture windows in light and dark
appearances using ScreenCaptureKit's current-process window API (macOS 14.4 or
newer). It verifies the requested window dimensions and recognizes visible
labels with Vision, so a blank or unexpectedly enlarged window fails. Captures
cover History, Changes, Activity, and Runs in populated, empty, loading, and
failed states, plus search highlights, diff navigation, Waiting, and the command
palette. Fixtures use injected services and never read real conversations or
operate a real run. PNGs are attached to the XCTest result and written to
`/tmp/agentmux-polish-previews`; older macOS versions skip only these capture
tests. Reviewed reference images and their provenance are recorded in
[`docs/acceptance/desktop-improvements.md`](../docs/acceptance/desktop-improvements.md).

### Running the UI on another Mac

The hosted tests launch the app, so they show windows on the Mac that runs
them. To keep that off the machine you are working at:

```bash
AMX_MAC_TEST_HOST=user@other-mac just mac-test
```

`scripts/remote-ui.sh` copies this checkout to a directory of its own under
that user's `Library/Caches/agent-mux-remote-ui/` on the other Mac, runs the
same `test-app.sh` there, and reports its result and which Mac ran it. It
refuses a host that cannot be reached or that turns out to be this Mac. With
`AMX_UI_TEST_REMOTE_ONLY=1` the suite never runs locally: no host, or a host
that fails, is an error rather than a fallback. SSH takes its credentials from
your agent or askpass helper and is given no terminal. The other Mac needs
Xcode; GhosttyKit and XcodeGen travel with the copy.

## Logs

The app logs to the unified log under the subsystem
`io.github.sglavoie.agentmux`:

```sh
log show --last 5m --predicate 'subsystem == "io.github.sglavoie.agentmux"'
```

### Recorded activity

View › Since Last Looked uses `amx session activity --json --days N --limit 1000`.
The command projects actual daemon journal transitions, settled process exits,
and accepted ASL lifecycle records; unchanged, suppressed, and duplicate reducer
records are omitted before limiting. Stable record IDs survive journal rereads.
Read IDs and the last marking time persist atomically in
`~/Library/Application Support/AgentMux/activity-read.json` (0600), without output
or transcript text. IDs expire after 91 days, beyond the maximum 90-day query.
Marking read is explicit: the Mark read menu offers the selected record or all
loaded filtered results, with an exact count. Undo restores the last acknowledgement
and its prior selection; neither action includes records omitted by a truncated query. Late-spooled events remain unread regardless of their timestamps.

Supporting excerpts use the exact harness-qualified conversation and the latest
assistant message timestamp at or before the recorded event. Missing timestamps
or transcripts are explicit, and earlier messages can be loaded. Stale requests
cannot replace a newer selection. Event navigation rechecks the live session
feed and never uses a recycled terminal window ID to identify an agent.
