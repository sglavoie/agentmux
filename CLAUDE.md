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

Requires Go 1.26+, Kitty, and `just`.

```bash
just build
just test
just acceptance   # gofmt, tests, core race suites, vet, every TUI scenario
just check        # acceptance plus golangci-lint
just install-restart-smoke
just update          # install-restart-smoke plus mac-install, in one go (needs Xcode)
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
just mac-test            # hosted AgentMuxTests XCTest suite
just mac-install         # Release build, quit, ditto to /Applications, relaunch
```

Releases of the Agent Mux / agent-session-loop pair go through
`just paired-release-gate asl_root=… signing_key=…`, which runs both
repositories' gates, installs the pair (ASL first), restarts the real daemon,
drives a real Kitty smoke, and writes a signed evidence record. The gate lives
in `internal/release` and is tested in isolation; see
`docs/paired-releases.md`.

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
- `internal/tmux`: the private tmux backend — AgentMux's own socket and
  configuration (never the operator's server or `~/.tmux.conf`), one session
  and one agent pane per runtime session, exited panes retained until
  `Close`, generation-checked `Instance`. Its tests run a real tmux and skip
  only when tmux is not installed; `internal/tmux/tmuxtest` gives other
  packages the same isolated server, a registry, and a stand-in agent shell.
- `internal/session`, `internal/harness`: lifecycle (still reading Kitty
  topology, issuing commands through `internal/backend`) and harness
  argv/capabilities. `session.Runtime` selects the launch backend (Kitty by
  default; `--backend tmux` on launch entrypoints): tmux launches dedup,
  register (`Begin`, launch, `Commit`), and compensate under one lock beside
  the registry, and actions on a live session follow its recorded backend.
  `amx attach <runtime-session-id>` (`cmd/session/attach.go`) transfers
  interactive ownership with `tmux attach-session -d`; it never relaunches.
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
- `internal/release`: the paired release gate, behind injectable runner/clock.

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

### Identity and safety rules

- Conversation identity is `(harness, session_id)`; live instance identity is
  the freshly validated Kitty window/title owner, or for a managed hook its
  runtime session. The daemon translates every hook to a reducer slot
  (`internal/daemon/runtime_routes.go`): a slot belongs to one runtime session
  at a time, and deliveries from another session, another backend instance,
  or an ended session are refused rather than merged.
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
`release-gate --json` likewise emits its evidence record directly: the record is
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

Release acceptance must keep explicit coverage for warm Past Sessions,
partial transcript/plan failures, CLI and TUI continuity, handoff/worktree
retention and compensation, and the absence of retired database artifacts from
TUI, daemon, doctor, and session-command paths. Every JSON file in
`docs/tui-testing/scenarios/` is both discovered by `just tui-scenarios` and
executed in-package; keep the documented scenario inventory exact.

`amx doctor` must retain the existing daemon/reducer/renderer invariants when
new checks are added. Stable finding identifiers are JSON consumer contracts.

## Adding a harness

Follow `docs/adding-a-harness.md`. Its screen-adapter step (capture-first,
committed byte-for-byte, every capture asserted) is not optional.

Version is the constant in `cmd/root.go`; update it with `just bump VERSION`.
