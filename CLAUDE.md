# CLAUDE.md

Guidance for coding agents working in Agent Mux.

## Project overview

Agent Mux is a Kitty-first, multi-harness project workbench for Claude Code,
GitHub Copilot CLI, and OpenAI Codex. The `amx` binary owns:

- a project-centric Bubble Tea workspace and legacy live-session power view;
- exact conversation continuity (focus live, resume closed, native or portable fork);
- isolated managed Git worktrees and deterministic cross-harness handoff packs;
- SQLite-backed conversation, plan, workspace, search, activity, discovery, and relationship data;
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
```

Use `amx doctor --strict`, `amx index status`, and `amx --version` when
validating an installed artifact. The CLI and daemon build identities must
match.

## Architecture

### Durable and source data

`internal/datastore` owns `~/.local/share/agent-mux/agent-mux.db`, migrations,
WAL policy, transactions, integrity checks, and private permissions. SQLite is
the active index/relationship store. These remain authoritative source
artifacts and must not be rewritten as a side effect of indexing:

- harness transcript files;
- saved plan directories;
- `config.toml`;
- handoff Markdown artifacts.

`transcripts.json`, `plans.json`, and search sidecars are read-only rollback
inputs for the compatibility release. `amx index rebuild` refreshes derived
conversation, plan, search, project, activity-link, and discovery projections
while preserving explicit plan links, handoff metadata, activity/read history,
and managed-worktree relationships.

### Main packages

- `cmd`: Cobra surfaces, common schema-v1 JSON envelopes, safe selector errors,
  doctor/index operations, and hidden TUI scenario commands.
- `internal/actions`: shared typed action registry used by CLI and TUI.
- `internal/workspace`: stable repository/directory identity, configured launch
  variants, worktree contexts, project snapshots, and discovery candidates.
- `internal/conversation`: canonical `(harness, session_id)` identity.
- `internal/session`: Kitty-backed lifecycle, exact live-owner resolution, and
  harness invocation construction.
- `internal/harness`: adapter-owned fresh/resume/fork argv and capability metadata.
- `internal/transcript`, `internal/plans`: SQLite facades with legacy read fallback.
- `internal/search`: safe FTS5 query compiler and cross-entity result service.
- `internal/handoff`: bounded deterministic pack creation, private artifacts,
  metadata, and cross-harness launch workflow.
- `internal/worktree`: bounded managed Git worktree creation and cleanup.
- `internal/activity`: prompt-free durable outcomes, asynchronous writer, feeds,
  and monotonic read markers.
- `internal/daemon`: IPC, reducer facts, durable Kitty state, activity emission,
  diagnostics, and the single renderer writer.
- `tui`: workspace home, command palette, unified/specialized search, legacy
  session view, and deterministic in-process scenario harness.

### Identity and safety rules

- Conversation identity is `(harness, session_id)`; live instance identity is
  the freshly validated Kitty window/title owner.
- Workspace identity is the Git common directory or canonical physical path.
  Configured project `id` values are case-insensitively unique stable selectors.
- Plan/workspace links are explicit. Path/title matches are suggestions only.
- Ambiguous selectors return ordered candidates and never guess.
- Handoff/worktree/adoption mutations require explicit confirmation where
  exposed by the command/action contract.
- Activity stores classified outcomes only, never raw prompts or hook payloads.
- Do not hold locks or perform SQLite work on the hook acknowledgement path.

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

[defaults.activity]
keep_days = 30

[defaults.handoff]
max_bytes = 65536
recent_messages = 12

[[profiles]]
name = "codex-work"
harness = "codex"
home_dir = "~/.codex-work"
color = "purple"

[[projects]]
id = "api"
title = "API"
directory = "~/projects/api"
profile = "codex-work"
```

Profile names are launch identities. Harness-specific args belong on a profile
or project, not in shared defaults. The full commented template is
`embed/example_config.toml`.

## Command and JSON conventions

Workbench commands live under `workspace`, `session`, `plan`, `search`, and
`index`. Successful `--json` output uses
`{"schema_version":1,"data":...}`. Failures use the common structured error
envelope; partial durable success returns data with a nonzero quiet exit.
`doctor --json` is a versioned diagnostic report with the same top-level schema
version but intentionally includes health fields directly.

When adding a command:

1. provide dependency seams for filesystem, datastore, daemon, and Kitty work;
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
amx index status
amx daemon export-events --current-window --last 50
```

`amx doctor` must retain the existing daemon/reducer/renderer invariants when
new datastore or writer checks are added. Stable finding identifiers are JSON
consumer contracts.

## Adding a harness

1. Implement `harness.Impl` and invocation capabilities in `internal/harness`.
2. Register the adapter and hook contract.
3. Add transcript reader fixtures and config coverage.
4. Extend fresh/resume/fork cross-harness tests, explicitly representing
   unsupported native fork capability.
5. Run `just check`.

Version is the constant in `cmd/root.go`; update it with `just bump VERSION`.
