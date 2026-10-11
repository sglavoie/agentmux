# CLAUDE.md

Guidance for coding agents working in Agent Mux.

## Project overview

Agent Mux is a Kitty-first, multi-harness session manager for Claude Code,
GitHub Copilot CLI, and OpenAI Codex. The `amx` binary owns:

- a live-session Bubble Tea dashboard with Past Sessions and Plans views;
- exact conversation continuity (focus live, resume closed, native or portable fork);
- isolated managed Git worktrees and deterministic cross-harness handoff packs;
- filesystem-backed transcript and plan caches with disposable search sidecars;
- an always-on local daemon that reduces hook events and exclusively renders Kitty state.

Kitty operations stay behind `internal/kitty` and the `internal/backend` seams.
Domain services must not invoke `kitten @` or spell Kitty match expressions;
`TestDomainServicesIssueNoTerminalCommands` enforces this for
`internal/session` and `internal/actions`.

## Build and acceptance

Requires Go 1.26+, Kitty, and `just`. tmux 3.5 or newer is needed for the tmux
backend and its tests (they skip without it); Agent Mux.app brings its own.

```bash
just build
just test
just acceptance   # gofmt, tests, core race suites, vet, every TUI scenario
just check        # acceptance plus golangci-lint
just install-restart-smoke
just update          # install-restart-smoke plus hooks-sync and mac-install, from any terminal (needs Xcode)
just crash-restart   # fault injection and restart equivalence, under -race
just cockpit-smoke   # real Kitty + installed pair; fails rather than skips
just overseer-integration asl_root=~/scripts   # installed amx + installed asl, isolated world
```

The native menu bar companion lives in `macos/` (Swift, XcodeGen, see
`macos/README.md`). Its recipes need Xcode and are deliberately outside
`check`, `acceptance`, and `paired-release-gate`, which
`TestMacAppRecipesStayOutOfTheGates` in `cmd/release` enforces:

```bash
just mac-app [Release]   # macos/build/Agent Mux.app; Release bundles `just build`'s amx
just mac-test [Release]  # hosted AgentMuxTests XCTest suite
just mac-ghosttykit      # pinned GhosttyKit into macos/Vendor; mac-app and mac-test run it
just mac-tmux            # pinned tmux runtime into macos/Vendor/tmux; mac-app runs it
just mac-install         # Release build, then install or update /Applications and relaunch
just mac-uninstall       # remove the app; --end-sessions and --remove-app-data are opt-in
just mac-release-gate    # the app's own release gate and evidence record; --rehearsal, --notarized
```

The app bundles the tmux its session server runs (`Contents/Resources/tmux`):
`macos/scripts/build-tmux.sh` builds it from the checksummed tarballs pinned
in `macos/TMUX_VERSION`, statically linked so it loads only `/usr/lib`, and
`sign.sh` writes its `SHA256SUMS` after signing. `internal/tmux/runtime.go`
picks the tmux a server runs: `[defaults.tmux] path` or `AMX_TMUX` (final when
set), then the bundled runtime (`AMX_TMUX_RUNTIME`, or `../tmux` beside a
bundled amx), verified and copied to `~/.local/share/agent-mux/runtime/`, then
a supported `tmux` on `PATH`. Two rules hold installs together and must
survive any change here: the copy outside the bundle is what runs, and a
running server keeps the executable recorded beside its socket
(`tmux.binary`) whatever would be resolved now. `install-app.sh` never stops
or replaces the server and fails if its generation changed across an update;
`uninstall-app.sh` removes the app only, unless asked. `amx backend status`
(`internal/cli/backend_status.go`) is the one report of all this, read by the
app's Preferences and by both scripts; `amx backend stop --yes` is the only
command that ends every agent.

With `AMX_MAC_TEST_HOST` set, `just mac-app` also runs Xcode on that Mac and
brings the signed bundle back to `macos/build/`.

The app embeds Ghostty for tmux sessions (`macos/AgentMux/Ghostty/`, ported
from Ghostty's macOS app; `macos/NOTICE-ghostty`). GhosttyKit is built from
source by `macos/scripts/build-ghosttykit.sh` at the tag, commit, Zig version,
and patches pinned in `macos/GHOSTTY_VERSION` and `macos/patches/ghostty/`, and
vendored into the gitignored `macos/Vendor/`; the build reads no other
checkout. One `Ghostty.App` serves any number of `Ghostty.SurfaceView`s, each
with its own `Ghostty.SurfaceConfiguration` (program, arguments, environment);
the app configuration names no command. The workspace window
(`macos/AgentMux/UI/WorkspaceWindow.swift`) lists every live session from the
daemon's v2 companion snapshot, grouped by project and labeled by profile, and
embeds one `amx attach` terminal per tmux session; a Kitty session is focused
in Kitty. A session is keyed by Kitty socket and window, or by runtime-session
ID when it has no window (`AttentionCycler.Key`); never key one by window ID
alone. The workspace's Dashboard and Overview are the existing TUI in
terminals of their own, and shells, editors, and pagers open beside them as
utility views (`macos/AgentMux/Model/WorkspaceViews.swift`); each runs the
hidden `amx workspace-view <kind> [target]` (`cmd/dashboard/workspace_view.go`),
which strips every session and Kitty variable before running anything. A
utility view is never a session: it has no key, comes from no snapshot, and
must never count toward attention.

F1 to F11 do in the workspace what Kitty binds them to
(`WorkspaceMenu.functionKeys`, the main menu's only keys with no modifier):
the reference and the icon and color pickers are overlay views of their own
kinds, and the keys about a session act on the one the pane is on, named to
amx by runtime session. The reference F1 shows is `kittykeys.Workspace`;
`TestWorkspaceFunctionKeysMatchTheApp` holds it to the menu.

The workspace comes back where it was (`Model/WorkspaceRecovery.swift`): the
app saves whether the window was open and which session or view its pane was
on, and at launch `WorkspaceWindowController.restore(_:)` returns there once a
live snapshot lists the session and `amx attach --json` reports no terminal
attached to it. Restoring never takes a session from another terminal, and
nothing in the workspace launches an agent: one that stopped is shown as
exited or lost, with the Dashboard as the only way on.

A session's terminal only views a pane the private tmux server holds, so its
scrollback is the server's (`Ghostty.Scrollback.server`): the View and Edit
menus' paging and Find items write tmux user keys to the client
(`SurfaceView+History.swift`, bound in `internal/tmux`'s `Config`), and
`TestViewerKeysMatchTheApp` holds the two sides together. Ghostty's own paging
and search keys are `performable` bindings; `Ghostty.menuMayClaim` offers them
to the main menu first, or they act on a buffer a session view does not have.
tmux tracks the mouse in every session view, so there a link opens with
⌘⇧-click, and only for modifiers Ghostty saw pressed as keys.

Anything that shows or drives UI can run on another Mac, and under an
operator's remote-only rule must: with `AMX_MAC_TEST_HOST=user@host`,
`just mac-test` runs there (`macos/scripts/remote-ui.sh`), and with
`AMX_UI_TEST_REMOTE_ONLY=1` it never runs locally, whatever fails. Live
acceptance of the app is driven the same way: read
`docs/acceptance/ghostty-desktop/session-15/driver/README.md` first. Its
driver sends keys and pointer through a headless VNC client and reads state
over SSH, because synthetic input posted on the machine itself has side
effects: a modifier-flagged mouse event from the HID-system event source
leaves that modifier held system-wide.

Releases of the Agent Mux / agent-session-loop pair go through
`just paired-release-gate asl_root=… signing_key=…`, which runs both
repositories' gates, installs the pair (ASL first), restarts the real daemon,
drives a real Kitty smoke, and writes a signed evidence record. The gate lives
in `internal/release` and is tested in isolation; see
`docs/paired-releases.md`.

The app is released through a separate gate, `just mac-release-gate` (the
hidden `desktop-release-gate`, `internal/release/desktop.go`): it builds the
Release bundle, checks it (`macos/scripts/release-gate.sh`), runs the hosted
suite against the Debug and Release app, and writes its own record. It
installs nothing. When there is no Xcode to be had, or `--notarized` is asked
of a keychain with no Developer ID, the record says `pending` and the command
fails: missing infrastructure is never a pass. The paired gate must stay free
of it (`TestTheTwoGatesStayApart`); see `docs/desktop-releases.md`.

Use `amx doctor --strict`, `amx transcripts status`, and `amx --version` when
validating an installed artifact. The CLI and daemon build identities must
match.

## Architecture

### Durable and source data

These are the authoritative source artifacts and must not be rewritten as a
side effect of indexing:

- harness transcript files;
- saved plan directories;
- `config.toml`;
- handoff Markdown artifacts.

`transcripts.json`, `plans.json`, and their search sidecars are disposable
caches. Refresh, rebuild, and corruption quarantine may replace them, but the
source transcript and plan files remain untouched. Cache replacement uses
stable advisory lock sidecars plus atomic file renames.

The default cache paths are `~/.local/share/agent-mux/transcripts.json` and
`plans.json`, with sibling `transcripts.search/` and `plans.search/`
directories. `handoffs/`, `worktrees/`, and `launch_overrides.json` are
independent user assets, not cache internals. Successful handoffs and worktrees
survive launch; compensation may remove only the worktree created by the
failed request.

There is no SQLite runtime or migration path. Never add automatic deletion of
legacy database files. A one-off operator cleanup must stop the installed
daemon first and use the exact allowlist documented in `docs/usage.md` ("One-off
cleanup after upgrading from a SQLite release"); it must preserve caches,
handoffs, worktrees, launch overrides, and ordinary daemon locks.

### Main packages

Each package's Go doc comment (`go doc ./internal/daemon`, or the `doc.go` /
package comment in the directory) is the authoritative description of what it
owns, what it must not do, and why. Read it before changing the package; when a
change moves a boundary, update that comment rather than this file. This map
only says where to look and which rules cross package lines.

Command tree (`cmd/...`; every user-facing command lives in a child package):

- `cmd`: `rootCmd`, version, exit mapping, and registration of every family —
  nothing else. No other `cmd` package may import it.
  `TestCommandFamiliesAreRegisteredUnderRoot` is the one registration check.
- `cmd/daemon` (`amx daemon`), `cmd/dashboard` (dashboard, overview,
  `open-dashboard`, overlays; the singleton rule), `cmd/project`,
  `cmd/config` (incl. `asl-sync`), `cmd/hooks`, `cmd/debug` (`debug`, `doctor`,
  `daemon audit`), `cmd/session` (conversation commands and root tab controls),
  `cmd/worktree`, `cmd/transcripts`, `cmd/notifications`, `cmd/overseer`
  (authorization surface for the ASL overseer), `cmd/release` and `cmd/tuitest`
  (hidden).

Shared and domain packages (`internal/...`):

- `internal/cli`: bootstrap every command family shares (exit statuses, JSON
  envelope and structured errors, tables, runtime assembly). Never imports a
  `cmd` package; reaches the daemon only through `internal/daemon/ipc`.
- `internal/actions`: typed action registry shared by CLI and TUI.
- `internal/conversation`: canonical `(harness, session_id)` identity.
- `internal/runtimesession`: opaque AgentMux runtime-session IDs and the
  durable registry mapping them to generation- and fingerprint-checked
  backend targets. Hooks and the daemon read it (`AMX_RUNTIME_SESSION_ID`,
  `Reader`); tmux launches register in it (Kitty launches do not yet).
- `internal/backend`: narrow terminal-backend interfaces (discover, launch,
  close, capture, metadata, presentation), opaque group/pane refs, capability
  errors; `internal/backend/backendtest` is the shared contract every adapter
  runs (`internal/kitty.Backend` is the Kitty adapter). `backend.Screen` is
  the backend-neutral screen observation (`Observer.Observe`): text plus the
  instance generation, pane size, alternate-screen and exit state, and capture
  time. Screen probes read it only through `probes.CheckScreen`, so a failed,
  stale, wrong-pane, wrong-generation, or exited-pane capture is never read.
  The daemon probes a tmux session through the same Claude and Codex bodies
  as a Kitty window (`internal/daemon/runtime_screen_probes.go`), with a
  binding built from its slot instead of a window tree; a stop there is held
  for confirmation only when its pane can confirm it.
- `internal/tmux`: the private tmux backend — AgentMux's own socket and
  configuration (never the operator's server or `~/.tmux.conf`), one session
  and one agent pane per runtime session, exited panes retained until
  `Close`, generation-checked `Instance`. Its tests run a real tmux and skip
  only when tmux is not installed; `internal/tmux/tmuxtest` gives other
  packages the same isolated server, a registry, and a stand-in agent shell.
- `internal/session`, `internal/harness`: lifecycle (still reading Kitty
  topology, issuing commands through `internal/backend`) and harness
  argv/capabilities. `session.Runtime` selects the launch backend (`--backend`
  on launch entrypoints, else `[defaults] launch_backend`, else Kitty): tmux launches dedup,
  register (`Begin`, launch, `Commit`), and compensate under one lock beside
  the registry, and actions on a live session follow its recorded backend.
  `amx attach <runtime-session-id>` (`cmd/session/attach.go`) transfers
  interactive ownership with `tmux attach-session -d`; it never relaunches,
  and refuses an agent that stopped as `runtime_session_exited` (its process
  ended) or `runtime_session_lost` (its pane or server is gone). One terminal
  owns a session: it has the keyboard and sets the size, reading a pane
  attaches nothing, and the size stays when the owner leaves
  (`internal/tmux/ownership.go`). The registry learns of agents that stop,
  owners whose terminal died, and competing terminals from
  `tmux.Reconciler`, which the daemon runs on maintenance and before it
  restores its checkpoint; it never starts a server or an agent. The same
  pass is the focus observation of a tmux session: a terminal that attached
  or was used after the session came to rest clears its unseen bit
  (`observeRuntimeViewers`); one merely still attached clears nothing.
- `internal/transcript`, `internal/plans`: discovery, metadata caches, search,
  recovery. `transcript.History` is the overseer's metadata-only admission path
  and never touches the index or search sidecars.
- `internal/cachefile`, `internal/searchstore`, `internal/searchquery`: atomic
  cache primitives, search sidecars, and what a search query means.
- `internal/handoff`, `internal/worktree`: bounded handoff packs and managed
  worktrees.
- `internal/gitstate`: cheap, advisory branch/dirty probe. Use `worktree` to
  decide something and `gitstate` to paint a column, never the reverse.
- `internal/overseerpolicy`: may the overseer act on a plan's repositories.
  Unlike `internal/projectresolve`, it has no degraded answer.
- `internal/diagnostics`, `internal/debugger`: journals and logs; offline debug
  services.
- `internal/release`: the paired release gate and the desktop release gate,
  behind injectable runner/clock.

Daemon (`internal/daemon/...`):

- `internal/daemon` is the root: the `Handler`, `stateMu`, every reducer
  mutation, the single renderer writer, every Kitty and IPC call, and the
  companion hub. Subprocesses and Kitty calls run off `stateMu`.
- Its children — `protocol`, `state`, `notification`, `rendering`, `events`,
  `activity`, `probes`, `process`, `asl`, `eventlog`, `ipc`, `companion` — must
  never import the root. Apart from `ipc` (the transport) and `process`
  (launching the daemon), they perform no Kitty, IPC, or journal I/O; deciding
  what an observation does to a window stays in the root.

TUI (`tui/...`):

- `tui` is the only package that names `Model`; leaves take explicit `*Input`
  values and return strings. Effects run in `tui/commands`.
- Dependency direction: `tui` → `tui/sessionactions` → `tui/commands` →
  `tui/dashboard`; `tui/layout` and `tui/theme` have no in-repo dependencies.
  Other leaves: `history`, `launch`, `plans`, `worktrees`, `asl`.
- `tui/overview` and `tui/customization` are standalone programs that root `tui`
  does not import. The overview is read-only toward Kitty except for actions the
  operator explicitly asks for.
- The whole TUI tree reaches the daemon only through `internal/daemon/ipc`,
  `process`, and `protocol`. Only the dashboard's `commands.Refresh` may write to
  Kitty or consume `amx_dashboard_select`; `RefreshOverview` writes nothing.
- The TUI also runs without Kitty (`tui/commands/portable.go`).
  `commands.Portable`, installed once by `cmd/dashboard` (`selectTerminals`),
  is the tmux half of the terminals a surface works with; the Kitty client is
  nil outside Kitty, so guard any direct use of it. A tmux session is listed
  under a process-local integer ID (`PortableIDBase` and up) that stands in for
  the tab and window ID everywhere in the tree, and the daemon's state for it
  is re-keyed from `Response.RuntimeSlots`. Focus on such a row attaches this
  terminal (`commands.Attach`, **F12** detaches) and the surface keeps running;
  Kitty-only actions report why they are unavailable. Build action dependencies
  with `commands.TerminalDependencies`, never a bare `actions.Dependencies{Kitty: …}`.
  A tmux that fails to answer, or a server that vanished under agents nobody
  ended, keeps its rows on the list (`commands.PortableOutage`, carried as
  `SessionsMsg.PortableErr`); rows kept that way are not evidence that
  anything is running, so `LiveSessions` never counts them.
- Hosted by the desktop workspace (`commands.Portable.Desktop`, set only by
  `amx workspace-view`), the TUI asks the daemon to have the workspace show
  things instead of taking over its own terminal: `commands.Attach`, the pager,
  `ShellHere`, `EditHere`, and every launch (`actions.Dependencies.Launched`)
  send a `workspace_view` request. The daemon validates and forwards it to the
  subscribers that named the `workspace_views` feature
  (`internal/daemon/workspace_view.go`); a refusal falls back to this terminal.
  The request names a kind and a target, never a command line.

### Identity and safety rules

- Conversation identity is `(harness, session_id)`; live instance identity is
  the freshly validated Kitty window/title owner, or for a managed hook its
  runtime session. The daemon translates every hook to a reducer slot
  (`internal/daemon/runtime_routes.go`): a slot belongs to one runtime session
  at a time, and deliveries from another session, another backend instance,
  or an ended session are refused rather than merged.
- An ASL run in a managed session is routed by that session's slot
  (`internal/daemon/handler_asl_runtime.go`): lease, supervisor question,
  notification, and attention clearing never consult Kitty topology, so they
  work for tmux sessions and with no viewer. Workspace matching is for legacy
  hooks only and never receives a run a runtime session has claimed.
- Configured projects are launch templates identified by profile and title;
  continuity actions use exact transcript identities.
- Ambiguous selectors return ordered candidates and never guess.
- Handoff/worktree mutations require explicit confirmation where
  exposed by the command/action contract.
- Hook acknowledgement remains prompt-free and does not perform cache refreshes.

## Configuration

Default path: `~/.config/agent-mux/config.toml` (override with `AMX_CONFIG`).
Canonical sections are `[[profiles]]` and `[[projects]]`; `[[accounts]]` and
`[[sessions]]` remain deprecated aliases for compatibility.

```toml
[defaults]
sort = "alpha"
refresh_interval = 2
guard_duration = 1
plans_directory = "~/.local/share/agent-plans"
git_probe = true   # per-directory branch/dirty probe; false stops running git

[defaults.handoff]
max_bytes = 65536
recent_messages = 12

[[profiles]]
name = "codex-work"
harness = "codex"
home_dir = "~/.codex-work"
color = "purple"

[[projects]]
title = "API"
directory = "~/projects/api"
profile = "codex-work"
```

Profile names are launch identities. Harness-specific args belong on a profile
or project, not in shared defaults. The full commented template is
`embed/example_config.toml`.

## Command and JSON conventions

Retained continuity commands live under `session`. Successful `--json` output uses
`{"schema_version":1,"data":...}`. Failures use the common structured error
envelope; partial durable success returns data with a nonzero quiet exit.
`doctor --json` is a versioned diagnostic report with the same top-level schema
version but intentionally includes health fields directly. The `debug` report
families likewise emit command-specific reports directly rather than using the
common `data` wrapper: `status`, `tail`, `explain`, and `bundle` use schema v2,
while `probe`, `capture`, and `replay` remain on schema v1. `debug tail
--follow --json` emits one schema-v2 record wrapper per JSON line. The hidden
`release-gate --json` (and `desktop-release-gate --json`) likewise emits its evidence record directly: the record is
the release artifact and carries its own `schema_version`, so wrapping it would
put a second, unrelated version number in front of the one that governs it.
`overseer policy --json` and `overseer history --json` keep the common `data`
wrapper but version their own capability contracts (`overseer_policy`,
`overseer_history`) inside it, independently of each other; a refusal is a
decision, returned as data with a nonzero quiet exit. `cmd/overseer`'s package
comment explains why.

When adding a command:

1. provide dependency seams for filesystem, daemon, and Kitty work;
2. preserve text and JSON exit-code consistency;
3. use `cli.MissingParameter`, `cli.CommandFailure`, and candidate-bearing ambiguity errors;
4. add a case to the family's `json_contract_test.go` when it supports `--json`.

## Testing and diagnostics

TUI scenarios are isolated from the operator's config, data, daemon, and Kitty.
Add narrow and wide scenarios for layout changes and run:

```bash
go run . tui-test run docs/tui-testing/scenarios/*.json
```

The acceptance map is in `docs/acceptance.md`. Operational troubleshooting
starts with:

```bash
amx doctor --strict
amx transcripts status
amx daemon export-events --current-window --last 50
```

`--current-window` is the session the command is typed in: its runtime session
(`AMX_RUNTIME_SESSION_ID`) when it has one, else its Kitty window.

Release acceptance must keep explicit coverage for warm Past Sessions,
partial transcript/plan failures, CLI and TUI continuity, handoff/worktree
retention and compensation, and the absence of retired database artifacts from
TUI, daemon, doctor, and session-command paths. Every JSON file in
`docs/tui-testing/scenarios/` is both discovered by `just tui-scenarios` and
executed in-package; keep the documented scenario inventory exact.

`amx doctor` must retain the existing daemon/reducer/renderer invariants when
new checks are added. Stable finding identifiers are JSON consumer contracts.

The tmux backend is diagnosed from two sources that fail apart
(`cmd/debug/doctor_backend.go`): `cli.BackendStatus`, read by the CLI and so
reported with the daemon down, and the daemon's `protocol.RuntimeHealth`
(`internal/daemon/runtime_health.go`): reconciliation passes, attachment,
screen reads, tmux client activity. Everything in the daemon's block and in
the `daemon.runtime_reconcile` journal rows is a count, a duration, or a word
from a fixed vocabulary (`tmux.FailureClass`): never put pane text, an error
string, or a path in them. The CLI's report does carry paths and messages for
the operator; a share bundle keeps its codes, counts, and releases and drops
the rest (`internal/debugger/redact.go`). Every tmux client of
a process passes one gate (`internal/tmux/gate.go`, four at a time), a
maintenance pass is one client however many agents there are, and a tmux slot
settles through the runtime checkpoint rather than the Kitty render queue.
`internal/daemon/performance_fleet_test.go` holds the measured fleet table and
the budgets `just reliability-stress` enforces.

## Adding a harness

Follow `docs/adding-a-harness.md`. Its screen-adapter step (capture-first,
committed byte-for-byte, every capture asserted) is not optional.

Version is the constant in `cmd/root.go`; update it with `just bump VERSION`.
