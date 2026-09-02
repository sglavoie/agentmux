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

- `cmd`: Cobra surfaces, common schema-v1 JSON envelopes, safe selector errors,
  doctor/transcript maintenance, and hidden TUI scenario commands.
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
- `internal/daemon`: IPC, event ingestion and routing, process inspection, live
  activity evidence, diagnostics, and the single renderer writer.
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
- `internal/diagnostics`: private fail-open causal journals, operational and
  native logs, temporary raw captures, and bounded diagnostic storage.
- `internal/debugger`: offline-first status, query, explanation, bundle, and
  replay services shared by debug, doctor, and hook-status surfaces.
- `internal/release`: the paired ASL/Agent Mux release gate — ordered step plan,
  artifact hashing, deterministic signed evidence records, and annotated pair
  tags. Every external command goes through an injectable runner and time comes
  from an injectable clock, which is what keeps the gate testable without
  touching the live installation.
- `tui`: live-session dashboard, specialized transcript/plan search, and
  deterministic in-process scenario harness.

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
