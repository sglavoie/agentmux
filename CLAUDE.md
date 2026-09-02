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

Kitty operations stay behind `internal/kitty` and session/action seams. Domain
services must not invoke `kitten @` directly.

## Build and acceptance

Requires Go 1.22+, Kitty, and `just`.

```bash
just build
just test
just acceptance   # tests, core race suites, vet, every TUI scenario
just check        # acceptance plus golangci-lint
just install-restart-smoke
just crash-restart   # fault injection and restart equivalence, under -race
just cockpit-smoke   # real Kitty + installed pair; fails rather than skips
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
daemon first and use the exact allowlist documented in README; it must preserve
caches, handoffs, worktrees, launch overrides, and ordinary daemon locks.

### Main packages

- `cmd`: the root command and the command families that have not yet been split
  out — `rootCmd` itself with `Execute`'s exit mapping, the session, worktree,
  transcripts, notifications, and Kitty tab-control leaves, and the assembly
  that registers every child package's commands. It is the only package in the
  `cmd` tree that any other `cmd` package may not import.
- `cmd/daemon`: the `amx daemon` family — the always-on local daemon seen from
  the CLI side. It owns the daemon's lifecycle as a command (`start`, which
  blocks and is the process the detached launch re-execs, plus `stop` and
  `restart` over the lifecycle client seam), the `status` readiness/build/
  notification/ASL line, and `export-events`, the investigation snapshot with
  its window resolution, per-window trimming, and schema projection. `audit` is
  registered here but implemented in `cmd/debug`, whose invariant evaluator it
  shares. The family exports exactly one identifier, `Command`, so no daemon
  internal is re-exported through the CLI; everything it needs from the daemon
  it takes from `internal/daemon`, `internal/daemon/ipc`,
  `internal/daemon/eventlog`, and `internal/daemon/protocol`. A file here may
  name the imported `internal/daemon` package unqualified as `daemon`, because
  a package's own name is not in scope inside its files.
- `cmd/dashboard`: the interactive surfaces `amx` opens inside Kitty — the
  live-session dashboard, which is what a bare `amx` runs rather than a
  subcommand of its own, and the color, harness-icon, and keybinding overlays.
  What makes them one family is the singleton rule: each asks Kitty whether its
  surface is already open and focuses that window instead of opening a second
  copy, and the dashboard additionally hands the running instance the tab the
  caller came from.
- `cmd/config`: the `amx config` family — the config file's lifecycle (init,
  edit, show, path, validate, including the `$EDITOR` field splitter and the
  project-directory audit) and `asl-sync`, the ordered preflight-migrate-write-
  restart-verify pairing with agent-session-loop and its structured error codes.
  It registers `cmd/hooks`' two leaves under itself.
- `cmd/hooks`: harness hook files and hook delivery — the `hook-indicator`
  entry point harness hooks call, with its fail-open legacy path and its strict
  75/2 exit mapping, and the `hooks-sync`/`hooks-status` maintenance leaves the
  config family surfaces.
- `cmd/debug`: the daemon-state diagnostic surfaces and the vocabulary they
  share — `amx debug` (status, tail, probe, explain, capture, bundle, replay),
  `amx doctor` with its stable finding identifiers and versioned report, the
  invariant evaluator both run, and `amx daemon audit`, which is a doctor
  surface wearing a daemon subcommand name. It also owns the export projections
  (`ExportWindowIDs`, `ExportLiveMetadata`, `ASLStatusLine`) that doctor and the
  daemon command family must agree on, so a doctor cannot report drift that
  `daemon export-events` does not see.
- `cmd/release` and `cmd/tuitest`: the two hidden operator/developer commands.
  `release-gate` drives the paired release procedure in `internal/release`;
  `tui-test` drives the in-process TUI scenario harness in `tui`.
- `internal/cli`: the shared bootstrap every command family runs before it does
  its own job — how a failure becomes a process exit status (the `ExitError`
  quiet-exit contract and the nonzero status vocabulary), how a command answers
  a machine caller (the common schema-v1 envelope, the structured error payload,
  and its candidate/parameter help shapes), how a command assembles the runtime
  it acts on (the validated configuration, the Kitty client and the window the
  CLI was invoked from, and the daemon export-client seam), and how the process
  coordinates with itself (the `--debug`/`AMX_DEBUG` verbosity policy with the
  logger it installs, and locating the running `amx` binary that hook commands
  write and daemon start/restart re-execs). Command-specific flag parsing,
  command construction, and command-specific output stay in the command
  packages: this is not a helpers bag, and an identifier belongs here only when
  more than one command family needs it or when it is the vocabulary that makes
  such a contract nameable. It must never import a `cmd` package, and it reaches
  the daemon through `internal/daemon/ipc` only — the transport, auto-start, and
  detached-spawn policies themselves stay in `internal/daemon/ipc` and
  `internal/daemon/process`.
- `internal/actions`: shared typed action registry used by CLI and TUI.
- `internal/conversation`: canonical `(harness, session_id)` identity.
- `internal/session`: Kitty-backed lifecycle, exact live-owner resolution, and
  harness invocation construction.
- `internal/harness`: adapter-owned fresh/resume/fork argv and capability metadata.
- `internal/transcript`, `internal/plans`: filesystem discovery, metadata caches,
  source-backed search, pruning, and corruption recovery.
- `internal/cachefile`, `internal/searchstore`: shared atomic cache primitives
  and disposable search-sidecar storage.
- `internal/handoff`: bounded deterministic pack creation, private artifacts,
  and cross-harness launch workflow.
- `internal/worktree`: bounded managed Git worktree creation and cleanup.
- `internal/daemon`: the orchestration root — daemon construction and lifecycle,
  the sweep/probe/render schedules, reconciliation coordination, and the public
  entry points `Run`, `NewOperationalLogger`, and `NewHandler`/`Handler`. It owns
  the one thing none of its children may: the `Handler` itself, and with it
  `stateMu`, every reducer mutation taken under it, the single renderer writer,
  and every Kitty and IPC call the daemon makes. Its children hold vocabulary,
  pure projections, and caller-locked trackers; the root decides what an
  observation *does* to a window. It imports all eleven daemon subpackages and
  none of them imports it back, which is what keeps the graph acyclic.
- `internal/daemon/protocol`: the daemon wire contract — request/response model,
  command names, durable and exported envelopes, and the serialization
  vocabularies. It holds shapes and pure projections only, and must never import
  the daemon root.
- `internal/daemon/state`: the side-effect-free fact reducer and the durable
  per-window state — source facts, effective-state reduction, retention, and the
  amx_state user-var schema with its version migrations. Like protocol it must
  never import the daemon root, and it performs no Kitty, IPC, or notification
  I/O of its own.
- `internal/daemon/notification`: notification settings, the Alerter backend
  and its capability probe, the delivery decision pipeline (gates, permission
  debounce, cooldown), click-to-focus, and the disposition vocabulary. Like
  protocol and state it must never import the daemon root; the daemon drives it
  through the `Notifier` seams rather than reaching into its state.
- `internal/daemon/rendering`: the daemon's status presentation — the icon the
  effective state paints, the tab-title text that carries it, the tab's title
  owner, the latest-generation render queue, the presentation cadence, and the
  bounded render-generation traces exported as the renderer's display model. It
  is domain-specific, not a formatting utility package: it must never import the
  daemon root, and it performs no Kitty, IPC, or journal I/O — the daemon root
  keeps the render loops and every Kitty write.
- `internal/daemon/events`: what one hook delivery *is*, before anything decides
  what to do about it — the agent-session-loop wire vocabulary and its
  compatibility gate, payload normalization into the identity-stamped
  `protocol.NormalizedEvent`, the state-update request built from it, per-event
  routing evidence and its outcome/confidence classification, and the durable
  session-event record. Like protocol, state, and rendering it must never import
  the daemon root, and it performs no Kitty, IPC, or journal I/O. Routing
  *decisions* and transport dispatch stay in the daemon root: this package
  supplies the vocabulary and the evidence, the root chooses and acts.
- `internal/daemon/activity`: whether a window is working or resting, and the
  evidence that settles the question — the legacy action guard, the
  stabilization-sample and screen-decline trackers with their per-(harness,tier)
  sample policy, the Claude title and Codex prompt classifiers, the per-sweep
  screen-text cache, authoritative-idle naming and probe eligibility, foreground
  harness classification, and the stale/hook-silence thresholds. The trackers are
  deliberately CALLER-LOCKED and zero-value ready: the daemon mutates a streak in
  the same critical section as the reducer state it is about to settle, so an
  internal mutex would hide an atomicity requirement rather than satisfy it. Like
  protocol, state, rendering, and events it must never import the daemon root,
  and it performs no Kitty, IPC, or journal I/O — deciding what an idle
  observation DOES to a window stays in the root, which owns the reducer, the
  renderer, and the notifier.
- `internal/daemon/probes`: the daemon's external-evidence layer — how it looks
  at the world outside its own hook stream, and how it decides that a look is
  still valid. Two kinds of evidence live here: the OS process table (the
  `ps` PID/lstart harness-liveness inspector, its bounded batches, and its
  cross-platform absent-process fallback) and the Kitty window tree (the
  immutable snapshot cache that keeps `ListWindows` off the hook path, the
  derived per-tab and per-pane freshness epochs, and the producer/source/target
  probe binding with its receipt and epoch validation). Like protocol, state,
  rendering, events, and activity it must never import the daemon root, and it
  performs no Kitty, IPC, or journal I/O of its own — it interprets a captured
  tree rather than fetching one. Deciding what an observation MEANS for a window
  stays in the root, which owns the reducer, the renderer, the notifier, and
  every harness probe adapter. Process launch and control belong to
  `internal/daemon/process`, not here.
- `internal/daemon/process`: the daemon's own life as an operating-system
  process — how a new daemon is launched (`amx daemon start` in a fresh session
  with the platform detach attributes), how it is cut loose from the terminal
  that launched it (stdio redirected into the private bootstrap log, and the
  `AMX_DAEMON_DETACHED`/`AMX_DAEMON_LAUNCH_ID`/`AMX_DAEMON_BOOTSTRAP_LOG` launch
  handshake the child reads back), how exactly one daemon is allowed to own a
  socket (the flock single-instance lock and the client-side auto-start lock),
  and when a running daemon has been superseded by a newer `amx` binary. It is
  the counterpart of `internal/daemon/probes`: probes observes processes the
  daemon did not start, this package starts and owns the one it did. Like
  protocol, state, rendering, events, activity, and probes it must never import
  the daemon root, and it holds no IPC transport and no process-table
  inspection — the retry/deadline path around a spawn stays with the client, and
  reconciling an observed harness exit into window state stays in the root with
  the reducer.
- `internal/daemon/asl`: what the daemon knows about an agent-session-loop run,
  and how it reads it. Two directions of evidence meet here. Outbound, the
  optional bridge subprocess seam, its capability block, and the four
  snapshot/question/action/workbench request handlers, plus the client-side
  decoding of their replies. Inbound, the durable per-run state ASL publishes:
  the projection phase a discovered run is in, the supervisor mailbox markers
  that say a human is being waited on and the closure markers that say they are
  not, the per-run monotonic generation/sequence position that decides whether a
  read is stale, the lease-evidence rule that keeps one unreadable plan from
  retiring a healthy run's lease, and the reconciliation and hook-delivery
  counters that make a silent pairing legible. Like protocol, state, rendering,
  events, activity, probes, and process it must never import the daemon root,
  and it performs no Kitty, IPC, or journal I/O. It does not own what an ASL
  observation *does* to a window: seeding a lease, retiring one, raising the
  question icon, and notifying about it are reducer mutations under stateMu and
  stay in the root. Generic hook decoding likewise stays in
  `internal/daemon/events`, which normalizes an ASL lifecycle event alongside
  every other hook.
- `internal/daemon/eventlog`: the daemon's record of what happened — the
  bounded per-window ring of accepted, suppressed, and daemon-synthesized
  rows, the global monotonic receipt cursor that totally orders them and the
  reference/request lookups that read a row back by it, the durable per-window
  counters and the deep-copied read projections served to session detail and
  event export, the render-completion and probe-trace correlation stamped back
  onto the rows that caused them, and the `daemon.decision` journal record —
  with its `replay.*` and `route.*` evidence — that outlives the ring and that
  `internal/debugger` decodes offline. The boundary is the row, not the
  decision: like protocol, state, rendering, events, activity, probes, process,
  and asl it must never import the daemon root, and it performs no Kitty, IPC,
  or journal I/O — it builds the record the root appends, and every reducer,
  renderer, and notifier mutation under stateMu stays in the root. It
  deliberately does not absorb the tail, cursor, and replay services in
  `internal/debugger`, which read immutable diagnostics artifacts with no
  daemon running.
- `internal/daemon/ipc`: how a request reaches the daemon and how an answer
  gets back — the Unix-socket server with its owner-only 0600 bind and
  drain-on-close lifecycle, the newline-delimited JSON framing with its request
  and response size ceilings and bad-payload throttling, the bounded
  single-consumer ingress queue that serializes application and degrades inline
  under saturation, the request-ID dedupe cache that makes a retried delivery
  at-most-once, the transport's own request-outcome and queue-latency counters,
  and the whole client side: the socket path, the send deadlines, daemon
  auto-start and restart, and the typed health, event-export, and ASL calls. A
  connection carries exactly one request/response pair; there is no
  multiplexing and no subscription channel, so a second answer means a second
  connection. It sees its application only as the three-method `RequestHandler`
  seam, which is what keeps it — like protocol, state, rendering, events,
  activity, probes, process, asl, and eventlog — from ever importing the daemon
  root. It dispatches a request to the application and routes it onto the
  serialized or the inline path; naming the concrete behavior a request type
  means — and every reducer, renderer, and notifier mutation under stateMu —
  stays in the root, which implements `RequestHandler`.
- `internal/diagnostics`: private fail-open causal journals, operational and
  native logs, temporary raw captures, and bounded diagnostic storage.
- `internal/debugger`: offline-first status, query, explanation, bundle, and
  replay services shared by debug, doctor, and hook-status surfaces.
- `internal/release`: the paired ASL/Agent Mux release gate — ordered step plan,
  artifact hashing, deterministic signed evidence records, and annotated pair
  tags. Every external command goes through an injectable runner and time comes
  from an injectable clock, which is what keeps the gate testable without
  touching the live installation.
- `tui`: the Bubble Tea program itself — the `Model`, the mode dispatcher, every
  key handler, every `tea.Cmd` and `tea.Msg`, the shared theme, one projection
  seam file per leaf (`dashboard_view.go`, `history_view.go`, `launch_view.go`,
  `plans_view.go`, `worktrees_view.go`, `asl_view.go`,
  `command_palette_view.go`), and the deterministic in-process uitest harness
  that `cmd/tuitest.go` drives. It is the only package in the TUI tree that
  names `Model`: the nine leaves below take explicit `*Input` values and return
  strings, so vocabulary and pure rendering move out while what-a-keypress-*does*
  stays in. The uitest files cannot become a child package because `cmd` reaches
  them through the root. The whole TUI tree — root included — depends on
  `internal/daemon/ipc`, `internal/daemon/process`, and
  `internal/daemon/protocol` only; `internal/daemon` itself is not in its
  transitive imports at all.
- `tui/layout`: terminal geometry, and nothing else — the visible-window slice
  of a flat list, wrap/clamp width math, grapheme-aware display width,
  truncation and padding, and the overlay compositor that splices a popup into
  a base view. It is the one leaf every other leaf imports and the only package
  in the repository's TUI tree with no in-repo dependency whatsoever.
- `tui/dashboard`: the live-session list — the filter DSL with its removable
  chips and completion menu, row/title/activity formatting, group borders and
  the state gloss, cursor restoration across a rebuilt list, pagination, the
  preview pane, and profile/harness color assignment (which `history`, `launch`,
  and the root all read).
- `tui/history`: the two histories that exist here — the per-session event
  detail overlay with its phase timeline, and Past Sessions transcript history
  (search rows, the transcript viewer, the full-message overlay, the delete
  prompt). There is no archive/restore flow.
- `tui/launch`: the launcher list and its preview, filter/profile-cycle/pin
  controls, the profile-override overlay, persisted launcher preferences, the
  `ConfigPickerPurpose` vocabulary, and launch batch targets and progress
  wording. The root keeps the `tea.Cmd` builders that actually launch.
- `tui/plans`: the Plans browser — listing and sort order, search result rows,
  and the viewer's wrapped rows with their source-line mapping.
- `tui/worktrees`: managed-worktree rows — state classification and badges,
  compact age, source labels, filter, sort, and durable mutation summaries.
- `tui/asl`: the ASL Runs surface — run rows and their sort, exact and fallback
  session binding, the fixed refusal sentences mapped from bridge codes, the
  bounded operation-receipt history, and the response form's state with its
  `String`/`GoString` redaction contract, which is load-bearing because the
  fields are exported.
- `tui/commands`: command and key-action definitions, parameterized over the
  root model with generics (`Command[M]`, `Action[M]`) so the leaf never names
  `Model` yet the palette can still ask a live model whether a command is
  visible and available — availability is re-derived at render time, not frozen
  when the palette opens. It also owns the dashboard refresh command cycle and
  the package-level seams root tests swap (`Registry`, `DaemonRequest`,
  `SpawnDaemonDetached`, `CopyToClipboard`, `ActiveLoopDiscover`).
- `tui/customization`: the standalone color, icon, and keybinding overlays, each
  its own Bubble Tea program. It is a sibling of the root rather than one of its
  leaves: `cmd` launches it directly and root `tui` does not import it.

### Identity and safety rules

- Conversation identity is `(harness, session_id)`; live instance identity is
  the freshly validated Kitty window/title owner.
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

When adding a command:

1. provide dependency seams for filesystem, daemon, and Kitty work;
2. preserve text and JSON exit-code consistency;
3. use `missingParameter`, `commandFailure`, and candidate-bearing ambiguity errors;
4. add the command to the JSON surface audit when it supports `--json`.

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

1. Implement `harness.Impl` and invocation capabilities in `internal/harness`.
2. Register the adapter and hook contract.
3. Add transcript reader fixtures and config coverage.
4. Extend fresh/resume/fork cross-harness tests, explicitly representing
   unsupported native fork capability.
5. Run `just check`.

Version is the constant in `cmd/root.go`; update it with `just bump VERSION`.
