# Troubleshooting

For first setup, start with the [minimal configuration](../README.md#first-session).
For lifecycle behavior and confirmation preferences, see
[Session lifecycle](usage.md#session-lifecycle).

- **Icons, hooks, or cached transcript data look wrong**: run `amx debug status`
  first, then `amx debug explain` with the trace/request/source-event ID from
  `amx debug tail`. `amx doctor` adds live checks for daemon reachability and
  binary freshness, `kitten @ ls`, hooks for every configured
  profile, renderer drift, known Kitty sockets, recent suppressions, and the
  reducer/renderer health surface. Its text report totals the suppressions and
  folds closed-window notes into one line, and says in one line each that the
  hooks of several healthy profiles are in sync, that every screen pattern set
  is embedded, and that a Kitty list coordinator with no failure is ok;
  `amx doctor --verbose` lists each suppression reason, closed window, and
  healthy profile, and every coordinator counter (`amx debug status` always
  prints them). Use `amx transcripts status` to
  inspect the filesystem transcript cache and `amx transcripts rebuild --all`
  to reconstruct it from source transcripts.
  Every failure and warning carries a remediation: the text report prints it
  on its own `remediation:` line under the finding (unless the message already
  says it), and `--json` carries it as the finding's optional `remediation`
  field. The finding's `message` text is unchanged.
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
  a 🛑 that is past every release path. `screenstate_persistent_decline` fails
  when the screen classifier has refused the same unchanging frame for two
  minutes, and `screenstate_override_invalid` warns when a pattern override was
  declined; both are covered in
  [Screen classifier health](screen-classifier-health.md).
  `project_directories` warns once when any configured project points at a
  missing or non-directory path; `amx project list --missing` lists them, and
  `amx config validate` lists them as warnings, and exits 1 on them under
  `--strict`. A project marked `archived = true` is not judged: keep a project
  whose checkout is gone that way instead of deleting its entry.
  A harness CLI newer than its verified hook contract is `hooks_unverified`
  only while no hook event from that profile's home dir has arrived within the
  verification window; once events are arriving it is the
  `hooks_contract_ahead` note instead, which leaves the verdict clean and
  marks the contract as due for re-verification.
  A profile whose hooks are in sync but that nothing exercises — no
  configured project launches it and no hook event from its home dir has ever
  been observed — is the `hooks_unused` note rather than `hooks_unverified`,
  since there is nothing to verify yet; `config hooks-status` shows its
  activation as `unused`, and `debug status` marks it `unused=true`.
  Hook deliveries dropped in the trailing hour are `hook_delivery_failures`:
  a warning when the latest drop happened while the running daemon was up,
  and a note once every drop predates the daemon's start — the restart gap
  the prompt-free hook client accepts by design.
  In text output, per-profile `hooks`, `hooks_unverified`,
  `hooks_contract_ahead`, and `hooks_unused` findings that
  share the same detail print on one line naming every profile, while
  `--json` keeps one finding per profile. A verdict with no failures reads
  `overall: clean` when nothing warned, and otherwise counts its warnings, as
  in `overall: ok (2 warnings)`.
  `shared_profile_homes` notes every home directory that several profiles of
  one harness read; each conversation there is listed under all of them.
  `uninstalled_binary` notes a doctor run from a binary other than the `amx`
  on PATH, such as a fresh `./amx` in a checkout: its socket and hook checks
  are judged against itself, so their failures there say nothing about the
  installation.
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
- **A config change has no effect** (a notification toggle, a cooldown, a
  stop-confirm delay): run `amx config daemon-status`. It compares saved daemon
  settings with the settings captured at startup and names the changed groups.
  Project edits, launch arguments, and reader preferences do not require a
  daemon restart. Run `amx daemon restart`, then check again; **Saved daemon
  settings are applied** confirms the comparison. Settings › Workspace offers
  the same check and restart, with verification afterward. Older daemons report
  that comparison is unavailable; Doctor retains a qualified timestamp warning
  for them. The stable finding remains `config_changed_since_daemon_start`.
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
  After upgrading from an older build, Claude panes stuck on the bolt icon by a
  stale generic title clear on their own within about 8 seconds; no manual
  intervention is required.
  Current releases also recover across daemon restarts on the next cleanup
  event. Both Codex and Claude also have a screen fallback that settles a stuck
  bolt on `🛑` — Codex on a completion marker (`Worked for …` on older builds,
  or 0.149.x's `Completed` line framed by horizontal rules) or its interrupted
  marker, in both cases with an empty composer below it; Claude on an idle
  composer or its interruption marker. Codex's empty composer is recognized by
  its dim placeholder text, which is version-specific: an unrecognized
  placeholder means no settle at all, so a pane on a Codex build newer than this
  one knows holds `⚡` rather than risking a false `🛑`. Look for
  `event: "ScreenStopProbe"` in `amx daemon export-events --window <id>` to
  confirm it fired, and read the row's `reason`, which names the evidence tier,
  the number of samples it required, and the screen fingerprint they agreed on.
  In split panes, run the export against the managed
  tab-title owner because sibling panes do not drive screen-probe title changes.
  **A bolt that never clears is expected on a pane that never stops
  repainting** (a live progress bar, an animated plugin): every tier needs at
  least two consecutive samples on the *same* normalized screen fingerprint, so
  the fallback deliberately refuses rather than risk a false `🛑`. If a native
  terminal hook did arrive it is only held, and commits anyway at
  `[defaults.stop_confirm].timeout`. If none arrived, the watchdog is what ends
  the bolt — and `sweepStale` clears a stale window to the **empty** icon, never
  to `🛑`, so such a pane holds `⚡` until `[defaults.watchdog].active` runs out
  (600 seconds unless configured). Run the daemon at debug level and grep
  `screen stop probe: not settleable` to see the veto and fingerprint behind
  each declined observation; that line also carries `decline_streak` and
  `stable_fingerprint`. A bolt that never clears on a pane that has genuinely
  gone quiet is the other case, and it is the one `amx doctor --strict` names:
  see `screenstate_persistent_decline` in
  [Screen classifier health](screen-classifier-health.md).
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
  unrelated caller tab. Press F8 to dismiss a stranded loop marker that has no
  live daemon lease. Paused runs have no ongoing loop and do not restore the
  marker, even if Kitty reuses their former window ID after a reboot.
- **F8 does nothing from a tab or split pane**: F8 clears a recognized state
  icon or stale loop marker regardless of whether the tab is managed. For a managed tab, inspect
  `amx daemon export-events --window <owner-id>` (the managed title owner); for
  an ownerless subprocess tab, inspect the pane that fired F8. Confirm a
  non-suppressed `user_clear` event. An `owner_mismatch` for `user_clear`
  indicates that the daemon predates this routing behavior — run
  `amx daemon restart` after `just install`.
- **F11 does nothing**: on macOS the system claims F11 for Show Desktop unless
  "Use F1, F2, etc. keys as standard function keys" is enabled; press `fn`+`F11`
  or change that setting, then reload Kitty's config. If the key reaches Kitty,
  diagnose it exactly like F8 and look for a non-suppressed `user_active` event
  on the owner window. A `⚡` that vanished early was retired by a real hook
  event, which is by design.
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
  not your shell's; `amx config asl-sync` resolves it once and persists an
  absolute path, which is the durable fix. A nonzero restart count with a reason
  means the bridge is crashing; ASL's own stderr is captured in the daemon log,
  and the daemon retries on a capped backoff without further action.
- **Everything looks connected but nothing from a run shows up**: read the
  `delivery` dimension in `amx doctor`. Lifecycle hooks are acknowledged whether
  or not the daemon believed them, so a pairing quarantining every event is
  indistinguishable from an idle one anywhere else. A nonzero quarantine count
  with `contract_set_mismatch` or `schema_unsupported` means the two halves were
  built from different corpora; `amx config asl-sync --check` compares them.
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
- **A failure crosses both halves of the pairing**: run
  `amx debug bundle --include-asl`. The bundle then also carries
  `asl-export.json` — the verbatim document from `asl diagnostics export
  --agent-mux` — and `asl-correlation.json`, which joins it against the same
  journal window the rest of the bundle describes.

  The correlation is what makes one archive explain one causal path. Per run it
  reports the plan digest (`sha256(plan_dir)`, the one correlation key that
  survives share-mode path aliasing), both acceptance counters
  (`projection_generation` and `lifecycle_source_sequence` — a stalled cockpit
  is usually one of them frozen, and which one names the failing half), the
  bridge operation IDs and mailbox closure IDs ASL durably observed, its
  assurance verdict as a state and an issue count, and one `deliveries` entry
  per lifecycle notification carrying ASL's own outcome beside the number of
  journal records AgentMux holds under the same request ID. A delivery with
  `matched: false` and an ASL status of `accepted` is the asymmetry worth
  opening a bundle for: ASL believes it delivered, and this bundle's journal
  window has no record of receiving. `contract_set` reports both halves'
  contract set identity and whether they agree, and `builds` reports both
  versions *and* both binary hashes, because the interesting failure is the one
  where the versions match and the behavior does not. `notes` carries stable
  codes (`asl-export-unavailable`, `contract-set-disagreement`,
  `compare-binary-hashes`, `correlation-capped`, `unmatched-deliveries`) that
  survive share-mode redaction.

  AgentMux reads nothing but that public export. `state.json`, `events.jsonl`
  and `mailbox.jsonl` are ASL's private formats and no bundle parses them, which
  is recorded as an explicit exclusion in `manifest.json`. The export carries no
  question body, reply text, prompt, model output or raw bridge stderr, and it
  ships its own `redaction_policy` and `omissions` ledger — counts and SHA-256
  digests with a named reason — so a reader can tell withheld material from
  material that was never there. The decode is typed, so a newer ASL cannot add
  a field that lands in an AgentMux bundle unreviewed.

  Nothing about this can fail a bundle. If ASL is not installed, is older than
  the subcommand, is turned off in `[defaults.asl]`, hangs, or emits something
  unreadable, `asl-export.json` is simply absent and `manifest.json` and
  `asl-correlation.json` both name the reason (`executable-not-found`,
  `export-unsupported`, `disabled`, `timeout`, `oversize`, `malformed`,
  `unsupported-schema`, `foreign-contract-set`, `export-failed`). A bundle
  captured while the other half is broken is the bundle that matters most.
  Repeated capture over frozen inputs is byte-identical, so two bundles taken
  the same way can be compared directly.
- **Copilot profile is unauthenticated**: run `copilot login` with the matching
  `COPILOT_HOME`.
- **Kitty control fails**: confirm `allow_remote_control yes` and the `listen_on`
  socket setting are present, then restart Kitty.

## The tmux backend

Most tmux problems are answered by one command, which reads the setup itself
and so works with the daemon down:

```bash
amx backend status          # launch backend, the tmux in use and where it came from, the server, and each problem with its fix
amx backend status --check  # the same, and exit 1 when launching on tmux would not work
```

`--check` judges tmux whatever `launch_backend` says: a machine that launches
in Kitty still exits 1 when it has no usable tmux (a server that does not
answer fails it only where launches go to tmux). `amx doctor` raises the same
problems under `backend_` plus the code `backend status` gives them, with that
report's hint as the remediation, so the two never advise different things.
[When tmux cannot be used](usage.md#when-tmux-cannot-be-used-and-what-doctor-reports)
has the full table of what doctor reports.

- **`backend_tmux_unavailable`**: no tmux 3.5 or newer could be used. The
  message names each place tried. Install one (`brew install tmux`), point
  `[defaults.tmux] path` at one, or run Agent Mux.app, which bundles one. A
  server that is already running keeps the tmux it was started with, so its
  agents keep working meanwhile.
- **`backend_server_unreadable`**: the private server is listening but did not
  answer. Run `amx backend status` again; a server that was only busy answers
  the second time. If it keeps failing, `amx backend stop --yes` ends the
  server **and every agent in it** (their conversations stay on disk and can be
  resumed). Nothing else ends them: restarting the daemon, quitting the app, or
  closing terminals leaves them running.
- **`amx attach` refuses with `runtime_session_exited`**: the agent's process
  ended where it ran, and the message carries its exit status or signal. It is
  never restarted from here; resume the conversation instead (Past Sessions,
  `s` or `S` in the dashboard, then `c`, or `amx session continue`).
- **`amx attach` refuses with `runtime_session_lost`**: the pane or the whole
  tmux server is gone (a reboot, a killed server), so what became of the agent
  was not seen. Resume the conversation the same way. Both refusals are also
  what F4, F7, and F9 return when the next session has stopped, and the daemon
  stops listing such a session within a couple of seconds.
- **`runtime_reconcile_failing` or `runtime_reconcile_stale`**: the daemon's
  pass over the private server keeps failing, or has stopped. Until a pass
  succeeds, agents that stop or lose their terminal are not noticed. Run
  `amx backend status`: if the server answers there, `amx daemon restart`
  restarts the daemon's maintenance loop; if it does not, follow the
  `backend_server_unreadable` advice above. The `daemon.runtime_reconcile`
  journal rows (`amx debug tail`) record when passes started failing and the
  failure class.
- **`screen_capture_failing`**: the daemon's last ten screen reads all failed,
  so stuck-icon recovery has no evidence to work from. Check the server with
  `amx backend status`; `amx debug probe --runtime-session <id>` reads one
  session's screen the way the daemon does, and `amx daemon restart` resets its
  readers.
- **"Another terminal took the session"**: a session has one owner, the
  terminal that attached to it last. Attaching from a second terminal (or the
  workspace showing the session, or F4/F7/F9 arriving there) detaches the first,
  which tmux reports as `[detached (from session …)]`. The agent is untouched.
  Run `amx attach <runtime-session-id>` in the terminal you want to use to
  take it back; `amx attach <id> --json` reports `clients`, how many
  terminals are attached right now.

## Doctor findings with no section of their own

Each of these is a correctness invariant `--strict` and `amx daemon audit`
evaluate, and each prints a remediation line. "The window's export" below is
`amx daemon export-events --window <id> --last 50` for the window the finding
names.

- **`queue_saturation`** (failure): the daemon's ingress queue was full at
  least once since it started, so some events were degraded. The counter is
  cumulative: find the burst with `amx debug tail` and
  `amx daemon audit --duration 10m`; `amx daemon restart` resets it.
- **`active_after_terminal_churn`** (failure): three or more events from one
  window were suppressed as `active_after_terminal` within fifteen minutes, so
  the source is probably active and the terminal fact wrong. Inspect the
  suppressed events in the window's export, then check
  `amx config hooks-status` and resync with `amx config hooks-sync`.
- **`subagent_visibility`** and **`asl_active_state`** (failures): a live
  subagent, or an ASL run in its turn or verification phase, is shown with an
  icon that hides it. Inspect the facts in the window's export, save
  `amx debug bundle --mode share`, and run `amx daemon restart` to rebuild the
  render state from the reducer; report it with the bundle if it returns.
- **`render_causal_bound`** (failure): a render generation carries more causal
  IDs than the protocol's bound, which only a daemon bug produces. Save a
  bundle, restart the daemon, and report it.
- **`render_generation_drift`** (failure): a window stayed dirty for more than
  two render intervals (four seconds), so the render pipeline is not keeping
  up or a write is stuck. Confirm the backend answers (`kitten @ ls` for
  Kitty, `amx backend status` for tmux), then save a bundle and restart the
  daemon.
- **`shadow_current_mismatch`** (failure): the reducer's effective icon or
  looping state disagrees with what the renderer intends, meaning some path
  still writes the visible state directly. Save a bundle, restart the daemon,
  and report it.
