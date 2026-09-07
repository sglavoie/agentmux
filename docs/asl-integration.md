# agent-session-loop rollout

The daemon speaks bridge protocol 2 to one `agent-session-loop` subprocess.
**Release and install ASL first, then Agent Mux.** The bridge is the older,
more widely installed half; upgrading it first means the new Agent Mux finds a
bridge it can talk to on the first start. The reverse order is safe but inert:
an ASL that predates the bridge is reported as unavailable and the cockpit is
read-only until it is upgraded.

Omitting `[defaults.asl]` entirely is auto mode: the daemon probes once and
stays quiet when ASL is absent. Set `enabled = true` only when you want an
unavailable integration to surface as a `amx doctor` finding, and
`enabled = false` to never start the subprocess.

The capability block is additive on the wire. An older `amx` ignores the `asl`
key in a newer daemon's health response, and a newer `amx` against an older
daemon prints `asl: unreported (older daemon)` instead of failing.

## Pairing the two halves

```bash
amx config asl-sync --check   # full preflight, writes nothing
amx config asl-sync           # pair, restart, and verify
```

Both halves usually install into `~/.local/bin`, both work from any terminal,
and the daemon still reports `executable file not found in $PATH` — because it
was never started from a terminal. It is spawned detached and inherits the
login session's environment, not yours. `asl-sync` ends that class of failure by
resolving the executable once and persisting the absolute path, after which no
part of the running system depends on anyone's `PATH`.

A bare `command` is resolved beside the `amx` binary first and only then on the
daemon's own `PATH`, so a paired install is found without any environment at
all. In auto mode a command that disappears — an upgrade in flight — is
re-probed on a bounded backoff up to five minutes rather than treated as
terminal; in required mode it is a strict-doctor failure, but never a reason for
the core daemon to be unready.

Pairing writes `command`, `state_root`, and `enabled = true` into
`[defaults.asl]` as a surgical text edit: comments, key order, and blank lines
survive, and re-running it produces byte-identical output. It writes
`enabled = true` deliberately — an operator who ran the pairing command asked
for the integration, and required mode is what makes an unavailable one visible
to `amx doctor --strict`. Use `enabled = false` afterwards to turn it off.

Everything that can be wrong is checked before anything is written: the
executable, the contract set, the bridge protocol, the state root, live runs,
migration readiness, and daemon reachability. `--check` stops exactly there.
Past that point the sequence is migrate, replace the config atomically, restart,
and verify against the running daemon — that it is ready, that its bridge is
live and handshaked, and that it paired with the executable that was just
persisted. Any failure restores the previous file and restarts the daemon onto
it, so pairing ends with either a verified live bridge or the configuration you
started with.

**An active run is never migrated or stopped.** If any live run still carries
state this build cannot pair with, the command refuses whole-root and names each
one by pid, run ID, and plan directory. Migrating the corpus around a live run
would leave the one process that is actually writing on the old schema. Let
those runs finish, or stop them yourself, then re-run.

## Diagnosing the integration

`amx doctor` reports six pairing dimensions separately, because a single
"unavailable" line was true for six unrelated conditions with six different
fixes. Every non-ok answer carries the exact command that addresses it.

| Dimension | What it answers |
| --- | --- |
| `shell_resolution` | Would the configured command resolve without an environment? A bare name warns even when your shell resolves it — the daemon does not have your shell. |
| `daemon_resolution` | What the running daemon actually found. An executable found on an inherited `PATH` warns: it works today, and by accident. |
| `contract` | Whether the bridge and the state root both name this build's contract set. A mismatch fails in every mode; it is terminal by construction. |
| `projection` | What the reconciliation sweep could not read. Always a warning — one corrupt plan does not break the pairing. |
| `freshness` | Whether the bridge snapshot or the sweep has gone stale. A stale bridge still serves reads, so nothing else looks wrong. |
| `delivery` | Lifecycle hooks accepted versus quarantined. The hook path answers OK to every producer including the ones it refuses, so this is the only place a silently-refusing pairing shows up. |

Reading them together is the point: `shell_resolution` ok plus
`daemon_resolution` failed *is* the diagnosis, and neither line says it alone.
Severity follows the mode — required fails, auto warns — except contract
mismatch, which always fails, and projection errors, which always warn. Disabled
mode and a machine with no ASL configured or installed report every dimension
`not_applicable` and raise nothing.

These dimensions arrived in `doctor --json` schema v2. `asl_unavailable` and
`asl_read_only` keep their exact names and meanings; the six dimension findings
(`asl_shell_resolution`, `asl_daemon_resolution`, `asl_contract_mismatch`,
`asl_projection_errors`, `asl_bridge_stale`, `asl_hook_delivery`) are new, which
is what that schema bump was for. The report is at v3 today; see
[Screen classifier health](screen-classifier-health.md) for what v3 added.

## Run presence is a paired cutover

The `🔁` loop marker and ASL lease handling do not use the bridge. They read
one atomic `agent_mux` projection that ASL publishes inside its own state, and
nothing else in that state is interpreted — the loop's turn, verification,
pause, and lock bookkeeping are private to ASL. This is a hard paired cutover:
Agent Mux keeps no parser for state that predates the projection.

An unpaired half does not fail loudly, and it does not fail destructively
either. State with no projection Agent Mux can read is classified
*indeterminate*, which means:

- existing leases are preserved — an unpaired upgrade cannot retire a healthy
  run's `🔁` or duplicate its work;
- no new runs are picked up from disk until the loop restarts under a version
  that publishes the projection;
- `amx daemon health --json` reports the reason under `asl_reconcile`
  (`missing_projections`, `unsupported_contracts`, `invalid_projections`).

Live hook events are unaffected throughout, so a run started after both halves
are upgraded still renders immediately.

Reconciliation sweeps the state root at daemon startup and every ten seconds.
A run confirmed gone — ASL recorded it finished, or the process it named has
exited — is retired after two consecutive confirmations, so presence is
restored and confirmed absence retired well inside thirty seconds. Evidence
that establishes nothing never counts toward that, in either direction.

The startup sweep runs on the first maintenance tick rather than inside the
bounded initial snapshot, and it is deliberately not part of what `amx daemon
health` calls readiness. The projection is small, but it is nested in a state
document that carries the whole run's history, so a sweep parses everything
every plan has ever accumulated — on a machine with hundreds of plans that is
tens of megabytes. Unchanged documents are reused between sweeps, which keeps
the steady-state cost to one stat per plan; only the first sweep after a daemon
start pays the full parse. Startup presence therefore lands within a tick of
the first render, not before it.

Set `state_root` under `[defaults.asl]` only if ASL's state has been moved off
its default location. It must be absolute or start with `~`; a relative path is
rejected at load rather than resolved against the daemon's working directory,
where it would silently find no runs.

## Per-project overseer opt-in

`[defaults.asl]` decides whether the daemon talks to ASL at all. It does not
decide which projects an overseer may supervise: that is stated once per
project, in the operator's own config file.

```toml
[[projects]]
title = "api"
directory = "~/projects/api"
profile = "claude-work"
asl_overseer = true
```

Only `true` opts in. `false`, an omitted key, and a directory that matches no
configured project are all "excluded", and so is a project whose configuration
cannot be read or parsed — the field fails closed in every direction, because
the alternative is supervising a repository nobody consented to.

The load path keeps three states apart rather than two. Omitted is *never
asked*, `false` is *asked and declined*, and only `true` is consent; write
`asl_overseer = false` when you want the decision recorded rather than left
open. Both grant the same access today, so the distinction exists for
diagnostics and enrollment surfaces, not for behavior.

Nothing enrolls a project for you. Installing or upgrading Agent Mux never
writes the key, `amx config asl-sync` writes only `[defaults.asl]`, and the
overseer does not turn on the flag for a project it wants to watch. The
deprecated `[[sessions]]` table accepts the field for compatibility; when both
tables are present `[[projects]]` wins whole, so an opt-in left behind in
`[[sessions]]` does not carry over to a project that was migrated without it.

## Deferred

These are known gaps, not bugs. Nothing in the cockpit is waiting on them:

- **Plans readiness and launch.** Agent Mux does not evaluate whether a plan is
  ready to run, and does not start runs. Use `asl prepare` and `asl run`.
- **Worktree-registry binding.** Runs are matched to managed worktrees by
  canonicalized workspace path, not by a shared registry identity.
- **Assurance inbox.** Verification findings are visible in the Workbench, not
  as an Agent Mux queue.
- **Model and profile brokering.** ASL chooses its own harness and model;
  Agent Mux profiles do not override what a run launches.
