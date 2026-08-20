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

`just asl-integration` is separate and opt-in. Every other recipe is hermetic
and needs no `agent-session-loop` installed; this one launches the real bridge
against a throwaway plan and state root and checks the handshake, capabilities,
snapshot decoding, action and receipt shapes, the Workbench URL shape, and a
clean shutdown. When it is asked for, a missing or protocol-incompatible ASL
fails it rather than skipping — a silent skip would report a compatible pair
that was never actually checked.

### Live Kitty worktree smoke checklist

The automated gate uses hermetic Kitty and Git fixtures. Before a release, run
these checks in a disposable managed worktree when a live Kitty instance is
available:

- `Enter` focuses a sole live session; an ambiguous pair opens the candidate
  picker instead of guessing.
- A closed conversation resumes, while a worktree with no indexed conversation
  launches fresh.
- Dirty work, a live session, and an active ASL loop each block cleanup.
- `d` removes the checkout but leaves its branch.
- `D` deletes only a branch merged into the source branch recorded by the
  schema-v1 sidecar; an unmerged branch is refused.

If the environment cannot safely provide Kitty, a disposable Git repository,
or an isolated transcript index, record the skipped item as residual manual QA.
The automated `just check` result remains the release completion gate.

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

## agent-session-loop rollout

The daemon speaks bridge protocol 1 to one `agent-session-loop` subprocess.
**Release and install ASL first, then Agent Mux.** The bridge is the older,
more widely installed half; upgrading it first means the new Agent Mux finds a
bridge it can talk to on the first start. The reverse order is safe but inert:
an ASL that predates the bridge is reported as unavailable and the cockpit is
read-only until it is upgraded.

There is no migration. Omitting `[defaults.asl]` entirely is auto mode: the
daemon probes once, stays quiet when ASL is absent, and every pre-existing
Agent Mux behavior — including the `🔁` loop marker and ASL lease handling,
which read loop state from disk and do not use the bridge — is unchanged. Set
`enabled = true` only when you want an unavailable integration to surface as a
`amx doctor` finding, and `enabled = false` to never start the subprocess.

The capability block is additive on the wire. An older `amx` ignores the `asl`
key in a newer daemon's health response, and a newer `amx` against an older
daemon prints `asl: unreported (older daemon)` instead of failing.

### Deferred

These are known gaps, not bugs. Nothing in the cockpit is waiting on them:

- **Plans readiness and launch.** Agent Mux does not evaluate whether a plan is
  ready to run, and does not start runs. Use `asl prepare` and `asl run`.
- **Unified diagnostics bundle.** `amx debug bundle` carries the ASL capability
  block via `doctor.json` only. ASL's own diagnostics are not collected into
  the same archive.
- **Worktree-registry binding.** Runs are matched to managed worktrees by
  canonicalized workspace path, not by a shared registry identity.
- **Assurance inbox.** Verification findings are visible in the Workbench, not
  as an Agent Mux queue.
- **Model and profile brokering.** ASL chooses its own harness and model;
  Agent Mux profiles do not override what a run launches.

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
- **The `A` view says `agent-session-loop is unavailable`**: read the `asl:` line
  from `amx doctor` or `amx daemon status`. It names the mode, the resolved
  executable, the negotiated version and protocol, snapshot age, restart count,
  action outcomes, and a sanitized reason. `mode disabled` means
  `[defaults.asl] enabled = false`. A mode line with no executable means the
  configured `command` is not on the daemon's `PATH` — the daemon's environment,
  not your shell's. A nonzero restart count with a reason means the bridge is
  crashing; ASL's own stderr is captured in the daemon log, and the daemon
  retries on a capped backoff without further action.
- **The `A` view is stuck read-only / `[stale]`**: the bridge process is alive
  but its last snapshot aged out, so reads continue and every action refuses
  rather than sending a mutation with an unpredictable outcome. This clears
  itself once a fresh snapshot arrives. If it does not, the restart count in the
  `asl:` line is the thing to watch: a climbing count is a crashing bridge, a
  frozen count with a growing snapshot age is a hung one. `amx daemon restart`
  replaces the subprocess. Read-only for the first several seconds after a
  restart is expected rather than a fault: a snapshot projects every discovered
  plan, so on a corpus of several hundred the first one takes seconds, and
  actions stay disabled until it lands.
- **An action was refused with a code**: the codes are stable and each names a
  distinct race. `stale-action-token` means the plan or run moved between the
  snapshot you read and the action you sent; `invalid-plan`, `no-active-run`,
  `run-mismatch`, `no-open-question`, and `question-mismatch` each name what
  moved. All of them are resolved the same way: let the view refresh and reissue.
  Agent Mux never resubmits a mutation on its own, because a bridge that
  accepted a write and then died leaves an outcome nobody can infer.
- **`w` does not open the Workbench**: the link is minted per request and never
  cached, so a failure is always current, not stale. `workbench-unavailable`
  means ASL could not bind or start the server; check whether another Workbench
  is already bound. `no supported URL opener on this platform` means Agent Mux
  found no `open`/`xdg-open` equivalent. Only `http`/`https` URLs are opened.
- **A debug bundle needs to be shared**: the ASL block in `doctor.json` carries
  availability, version, protocol, restart count, snapshot age, per-method
  request counts, and action outcome counts. It carries no plan path, run ID,
  question ID, question or reply text, and no Workbench token; bridge-supplied
  failure messages are reduced to their stable error code before publication.
- **Copilot profile is unauthenticated**: run `copilot login` with the matching
  `COPILOT_HOME`.
- **Kitty control fails**: confirm `allow_remote_control yes` and the `listen_on`
  socket setting are present, then restart Kitty.
