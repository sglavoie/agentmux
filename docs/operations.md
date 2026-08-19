# Configuration, testing, and troubleshooting

## Config commands

| Command | Description |
| --- | --- |
| `amx config init` | create the example config |
| `amx config edit` | open the config in `$EDITOR` |
| `amx config show` | print the parsed config |
| `amx config path` | print the config path |
| `amx config validate` | validate the config and configured project directories |
| `amx config hooks-sync` | sync hooks into all configured profiles |
| `amx config hooks-status` | show hook sync status per profile |

## Build and test

```bash
just build
just test
just acceptance
just check
```

`just acceptance` runs the full Go suite, race-sensitive transcript, plan,
handoff, worktree, daemon, and TUI tests, `go vet`, and every committed TUI
scenario. `just check` adds the repository linter and is the final local/CI
gate. The end-to-end coverage map is documented in
[`docs/acceptance.md`](acceptance.md).

### TUI layout regression harness

A hidden developer command, `amx tui-test`, drives the dashboard model
in-process against built-in fixtures to catch overlay/modal sizing regressions
(including wide, narrow, filtered, scrolled, and executed command-palette
flows). It never launches Kitty and never touches your real config or data. See
[`docs/tui-testing/README.md`](tui-testing/README.md) for the command
surface, scenario schema, and committed example scenarios, or run the examples
directly:

```bash
amx tui-test run docs/tui-testing/scenarios/*.json
```

## Troubleshooting

- **Icons, hooks, or cached transcript data look wrong**: run `amx debug status`
  first, then `amx debug explain` with the trace/request/source-event ID from
  `amx debug tail`. `amx doctor` adds live checks for daemon reachability and
  binary freshness, `kitten @ ls`, hooks for every configured
  profile, renderer drift, known Kitty sockets, recent suppression reasons,
  and the reducer/renderer health surface. Use `amx transcripts status` to
  inspect the filesystem transcript cache and `amx transcripts rebuild --all`
  to reconstruct it from source transcripts.
  `amx doctor --json` emits a structured report; `--strict` also evaluates the
  correctness invariants (reducer/renderer agreement, ASL visibility, subagent
  visibility, producer routing, acknowledged-fact pressure, persisted process
  identity, render-generation drift, queue saturation, build identity) and
  exits 2 on any failure. The
  stable routing/retention checks are `owner_mismatch_prompt` (failure for any
  recent prompt mismatch), `owner_mismatch_burst` (warning for at least three
  recent non-prompt mismatches on one window), `unknown_producer_fact`
  (warning), and `acknowledged_fact_pressure` (warning at 49–64 evictable
  acknowledged facts; failure above 64). `malformed_process_identity` fails
  when an exported persisted fact contains a partial, invalid, or
  source-incompatible process fingerprint; it does not perform a live process
  scan. Their event window is 15 minutes. `stop_hold_stuck` warns when a
  terminal stop has been withheld for more than twice the configured
  `[defaults.stop_confirm]` timeout — the window is showing an active icon for
  a 🛑 that is past every release path.
- **Suspected intermittent state bugs**: preserve a causal snapshot with
  `amx debug bundle --mode share`, validate it with `amx debug replay`, then run
  `amx daemon audit --duration 10m --json`. It samples the invariants every
  two seconds for the duration, deduplicates findings, and exits 2 if any
  failure-severity violation was observed in any sample. The daemon must stay
  up for the whole window — a restart mid-audit records `daemon_unreachable`.
- **Deploying a new build**: use `just install` (stages the binary beside the
  destination and renames it into place, so the replacement is atomic) or
  `just install-restart-smoke` (install, restart the daemon, wait for
  `ready: true`, then gate on `amx doctor --strict`, which verifies the CLI
  and daemon are the same artifact by build hash). Durable per-window state is
  restored from `amx_state` Kitty user vars across the restart; restore
  republishes each window's reduced state, so a terminal icon whose
  unacknowledged fact aged past the one-hour tombstone retention while the
  daemon was down clears on restart instead of pinning stale state.
- **No projects appear**: run `amx config validate` and confirm the config is
  at `~/.config/agent-mux/config.toml`.
- **Hooks are missing**: run `amx config hooks-status`, then
  `amx config hooks-sync`.
- **Doctor reports `owner_mismatch_prompt`, `owner_mismatch_burst`, or
  `unknown_producer_fact`**: run `amx config hooks-status` and resynchronize
  with `amx config hooks-sync` if generated commands lack `--harness`. Then
  inspect both the managed target and producer panes with
  `amx daemon export-events --window <id> --last 50`: a routed row should keep
  the producer's `origin_window_id` and `source_key` while recording
  `route_decision: "owner"`. A new inactive unmanaged session should remain
  suppressed; a direct prompt from the focused pane should not.
- **Doctor reports `acknowledged_fact_pressure`**: inspect
  `shadow.facts[]` in `amx daemon export-events`. Schema-v3 `aa` timestamps mark
  acknowledged replay cutoffs; nonterminal cutoffs are retained for one hour
  and capped at 64. Live ASL leases and unacknowledged facts are intentionally
  exempt. A failure above 64 means the running daemon has not applied current
  compaction logic; restart from the installed current binary.
- **The bolt icon stays after the agent is done**: run `amx config hooks-status`
  and `amx config hooks-sync` to restore terminal hooks like
  `sessionEnd`/`Stop`/`agentStop` and the child-complete `TeammateIdle` hook.
  Current releases also recover across daemon restarts on the next cleanup
  event. For Codex, a completed-turn screen fallback also settles a stuck bolt
  on `🛑` when the tab shows `Worked for …` with an empty composer; look for
  `event: "ScreenStopProbe"` in `amx daemon export-events --window <id>` to
  confirm it fired. In split panes, run the export against the managed
  tab-title owner because sibling panes do not drive screen-probe title changes.
  For Copilot, also upgrade to version `1.0.45` or newer, because older
  releases had `agentStop` hook issues.
- **`/clear` still shows a stop icon or stop notification**: inspect
  `amx daemon export-events --window <id>`. Claude/Copilot should show the
  documented hook reset payloads; Codex may show `ScreenClearProbe` when idle
  `/clear` happens without an immediate hook. If neither appears, confirm the
  tab is managed by Agent Mux and Kitty `get-text` remote control works.
- **The loop marker stays after a run is finished**: inspect
  `amx daemon export-events --current-window --last 50` for
  `AgentSessionLoopFinished`, then inspect the loop `state.json` for
  `run_finished_at`. If the marker remains, confirm no other active loop state
  still matches the same workspace and harness. Legacy loop state without a
  recognized harness still matches by CWD, and if no same-harness managed Kitty
  tab is open, Agent Mux falls back to the same workspace rather than updating an
  unrelated caller tab.
- **F8 does nothing from a tab or split pane**: F8 clears a recognized state
  icon regardless of whether the tab is managed. For a managed tab, inspect
  `amx daemon export-events --window <owner-id>` (the managed title owner); for
  an ownerless subprocess tab, inspect the pane that fired F8. Confirm a
  non-suppressed `user_clear` event. An `owner_mismatch` for `user_clear`
  indicates that the daemon predates this routing behavior — run
  `amx daemon restart` after `just install`.
- **A loop running in a split pane doesn't drive the tab**: an
  `agent-session-loop` started in an *unmanaged* sibling pane only routes to the
  managed owner when loop discovery finds a live, unfinished candidate whose
  window ID matches that pane exactly. Inspect
  `amx daemon export-events --window <owner-id>` and confirm the loop's states
  land on the owner (not `owner_mismatch` on the source pane). If the marker or
  states stay put, confirm the loop's `state.json` has no `run_finished_at`, the
  PID is still live, and the source pane's window ID matches the discovered
  candidate.
- **Daemon can't reach Kitty after an F8-triggered auto-start**: check that the
  window's renderer metadata records a `unix:`/`tcp:` socket, never `fd:`.
  `amx doctor` reports known Kitty sockets and renderer drift; a `fd:` value or
  render failures indicate a stale binary — `just install` then
  `amx daemon restart`.
- **Copilot profile is unauthenticated**: run `copilot login` with the matching
  `COPILOT_HOME`.
- **Kitty control fails**: confirm `allow_remote_control yes` and the `listen_on`
  socket setting are present, then restart Kitty.
