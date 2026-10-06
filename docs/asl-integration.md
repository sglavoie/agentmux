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
| `daemon_resolution` | What the running daemon actually found. An executable found on an inherited `PATH` warns: it works today, and by accident. An executable the daemon launched but whose bridge keeps exiting is reported with the last failure from the daemon's private log (`daemon/daemon.jsonl`), because the usual cause is a script interpreter the daemon's `PATH` resolves differently from your shell, which `asl-sync` cannot fix. |
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

Agent Mux implements `asl-amx-1.85ec3041b9f03f47`, which added the
`run.redirect`, `run.redirect_status`, and `run.recover_conversation` bridge
methods. Loop observation also accepts the previous set,
`asl-amx-1.6f30193cd1744a98`: its projection and lifecycle schemas and fixtures
match the vendored components exactly, so state written by an ASL that has not
moved yet stays observable. The bridge accepts only the exact current set, and
`amx doctor` judges the state root by the same rule the reconciler uses.
Unknown contract identities remain
indeterminate, and ongoing loop discovery still authenticates the PID and birth
token. Paused runs have no loop process and do not restore `🔁` onto new tabs.

An unpaired half does not fail loudly, and it does not fail destructively
either. State with no projection Agent Mux can read is classified
*indeterminate*, which means:

- existing leases are preserved — an unpaired upgrade cannot retire a healthy
  run's `🔁` or duplicate its work;
- no new runs are picked up from disk until the loop restarts under a version
  that publishes the projection;
- `amx doctor --json` reports the reason under `daemon.asl_reconcile`
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

## How a plan's repositories are authorized

The opt-in above is a per-project answer. A plan is not a project: it names one
or more repositories, and every one of them has to be permitted before the
overseer may act, so a plan touching an excluded repository is refused whole
rather than trimmed down to the permitted part. A plan that names no repository
at all is refused too — there is nothing to have consented.

Matching a directory to a project declaration is deliberately stricter here than
the resolution behind a dashboard label:

- **Every applicable declaration is examined, not the most specific one.** A
  dashboard row shows the innermost project that contains a directory. An
  authorization request looks at all of them, in both directions of nesting: a
  declined enclosing project denies a nested opt-in, and a declined nested
  project denies a plan running at the enclosing path, because that plan works
  on the nested project's files too.
- **Aliases are one place with several names.** The same directory configured
  once per harness profile is still one repository. If any of those entries
  declines or never answered, the repository is excluded — an alias is not a
  second opinion to choose between.
- **Symlinks and `~` are resolved on both sides.** The configured directory and
  the plan's path are canonicalized before they are compared, so naming a
  repository through a symlink neither grants nor escapes an answer. A
  configured directory that does not exist on this machine cannot be
  canonicalized and therefore governs nothing, which refuses rather than grants.
- **Linked worktrees inherit the repository's answer.** A checkout is matched by
  its Git common directory and by its position inside the checkout, so an
  exclusion on a main checkout reaches every linked worktree of the same
  repository, in either direction. Position is what keeps sibling projects in
  one monorepo independent: sharing a repository is not sharing an answer.
- **Unresolved is refused.** A path that does not exist, is not a directory, or
  sits behind a dangling symlink is refused. So is a directory whose Git
  identity could not be established — a probe that timed out inside a linked
  worktree looks exactly like a plain directory, and reading it as one would
  drop the exclusion that applies. Only Git positively reporting "not a
  repository" makes a plain directory a plain directory.

Every refusal carries a stable reason (`declined`, `unenrolled`, `undeclared`,
`path_unresolved`, `identity_unresolved`, `no_repositories`) and names the
declaration or path that produced it, so "why was my plan refused" has an answer
that points at the line to edit.

## Asking the question: `amx overseer policy`

The rules above are reachable as a command. `amx overseer policy` takes the
complete repository set of one plan and the category of access being requested,
and answers whether the overseer may act:

```bash
amx overseer policy --json \
  --plan e24c7728 \
  --access context \
  --repository api=~/projects/api \
  --repository ~/projects/api-docs
```

`--repository` is repeatable and takes `alias=path` or a bare `path`. The path
must be absolute or start with `~` — a relative path is rejected rather than
resolved against the caller's working directory, which is the same rule
`state_root` follows and for the same reason. The text before the first `=` is
read as an alias only when it is a non-empty name with no path separator, so a
path containing `=` still works unaliased. Aliases must be distinct within one
request; they are labels for reading the answer back, not identities.

`--access` is required and must be one of `context` (read project or
conversation content), `mutation` (prepare a mutation to a repository), or
`action` (dispatch an action into a run) — the three points at which permission
is rechecked. All three are gated by the same per-project opt-in today, so
naming one does not change the decision; what it changes is what the answer is
an answer *to*. Inspecting minimal identity metadata has no category on purpose:
it is the one thing allowed before authorization, so a category for it would be
a request that always succeeds.

The answer carries the decision, the stable reason, every repository with its
canonicalized path and the declarations that applied to it, a
`config_fingerprint` naming the configuration state it was decided against, and
a `request_digest` binding it to this exact question. It carries no token, no
expiry, and no grant, because it is not a lease: ask again before the next
access. Configuration is read on every invocation — there is no cached evaluator
and no daemon in the path — so withdrawing an opt-in takes effect on the next
question, and both the fingerprint and the digest move when it does.

**A refusal exits nonzero in both text and JSON mode**, so
`amx overseer policy … && act` fails closed for a caller that reads only the
process status. A refusal is still a decision and comes back in the ordinary
`{"schema_version":1,"data":…}` envelope; only a malformed request or unreadable
configuration uses the structured error envelope, with `unsupported_access`,
`unsupported_contract_version`, `invalid_repository`, `missing_parameter`, or
`config_error`.

Inside the envelope, a `contract` block names the contract (`overseer_policy`),
its version, and the access categories this build accepts. It is versioned
separately from the schema-v1 envelope and from the ASL bridge protocol, which
this command does not touch: pass `--contract-version` to state what the caller
understands, and a build that speaks a different version refuses the request
outright instead of answering it in a dialect the caller may misread.

## Reading the evidence: `amx overseer history`

`amx overseer policy` answers a question; `amx overseer history` is the surface
that acts on the answer. It returns bounded excerpts from the harness
conversations that worked inside the repositories of an authorized plan, which
is what turns "the run is stuck" into "here is what was said just before it got
stuck":

```bash
amx overseer history --json \
  --plan e24c7728 \
  --repository api=~/projects/api \
  --limit 5 --messages 10 --max-chars 1500
```

`--plan` and `--repository` mean exactly what they mean for `policy`, and the
access category is fixed at `context` rather than being a flag: reading a
conversation is reading conversation content, and a caller that could label the
request `action` would be labelling it wrongly. `--limit`, `--messages`, and
`--max-chars` bound the answer (conversations, messages per conversation taken
from the end, and characters per excerpt); each has a default and a ceiling, and
the bounds actually applied come back in a `limits` block so a clamped request is
visible rather than silent. `--harness` and `--since` narrow the corpus.

Authorization happens before any message is read, and twice. Once for the plan,
exactly as `policy` decides it — a plan-level refusal ends the command with the
corpus never walked, not even for identity. Then once per conversation, against
the working directory the conversation's own identity metadata names: it must lie
inside one of the plan's authorized repositories, and it must pass the policy
evaluation on its own, which examines every declaration applicable to it. The
transcript metadata cache is not in the path at any point. That matters because
`amx transcripts rebuild` reads every message body and writes the text into a
search sidecar, so a cache lookup would be a way to serve an excluded
conversation without ever asking about it.

Both questions are decided against the configuration that exists when they are
asked, not against a snapshot taken when the command started. Every conversation
reloads the configuration file, rebuilds the evaluator, and re-decides the plan's
**whole** participating repository set before it is opened — walking a corpus and
reading transcripts takes time, and an opt-in withdrawn in the middle of that has
been withdrawn. The recheck covers the whole set rather than the conversation's
own directory because authorization is per plan: one repository losing its opt-in
denies the request, so a conversation admitted afterwards would be admitted under
a permission the plan no longer holds.

Losing authorization mid-request stops it. No further conversation is opened, the
evidence gathered before the withdrawal is dropped rather than returned beside a
refusal, and the answer is a plan-level refusal naming the configuration it was
decided against — a request half-served under a permission that has since been
withdrawn is not half-authorized, and the client contract is binary in any case
(an allowed answer carries conversations and exits zero; a refused one carries
none and exits nonzero). `examined` and any corpus `warnings` survive, so a
stopped request does not read as an empty corpus. Configuration that becomes
unreadable partway through stops the request the same way and reports
`config_error`, because an answer nobody can currently give is not an answer of
yes.

Two shapes are unavailable no matter how the configuration reads. A conversation
whose metadata names **no** working directory cannot be shown to be outside an
exclusion. A conversation that names **more than one** may hold messages from
several projects, and which messages belong to which is a question only its
content could answer — the content the decision is supposed to gate. Both are
refused with the file still closed.

Each returned conversation carries its harness, session id, working directory,
participating projects, session class, total message count, and its excerpts.

`session_class` is a contract rather than a label. It is always one of `main`,
`automated`, or `unknown` — anything the classifier cannot place normalizes to
`unknown` before the answer is written, so the field is never empty and never
carries a fourth spelling. ASL's overseer reads it fail-closed to decide whether
a `user` turn can carry a human authorization: only `main` yields an
instruction, `automated` yields a generated prompt, and `unknown` yields a
statement that authorizes nothing. That is what makes a role field safe to act
on at all — ASL injects its own composed prompts under the `user` role, and a
subagent or an `exec` run types its own. Adding a class here without ASL
learning it means every conversation in it stops authorizing, which is the safe
direction and still a coordinated change.

An excerpt carries its role, its timestamp, its text, and its `index` — the
message's position in the conversation, so an excerpt says where it came from
rather than only that the conversation contained it. Excerpts are drawn from the
handoff-safe message text, so thinking blocks and tool payloads are not in them.

Every conversation that was discovered and not returned appears under `withheld`
with its harness, its session id, and a reason (`out_of_scope`,
`ambiguous_project`, `unidentified`, `path_unresolved`, `declined`,
`unenrolled`, …). It carries nothing else on purpose: the path, the working
directory, and the project are what an exclusion is about, so reporting them
under the heading of explaining a refusal would hand back exactly what the
operator withheld. An answer that silently omitted them would be worse still — a
mostly-refused corpus would read as an empty one.

Like `policy`, a plan-level refusal returns the ordinary
`{"schema_version":1,"data":…}` envelope with a nonzero quiet exit, and only a
malformed request or unreadable configuration uses the structured error envelope
(`unsupported_contract_version`, `invalid_repository`, `invalid_since`,
`config_error`, `history_error`). The `contract` block names `overseer_history`
and versions independently of both the schema-v1 envelope and the policy
contract. The `request_digest` binds the bounds as well as the plan and the
repository set: an answer taken under a 10-message bound is not evidence for a
request that asked for 200.

## Checking the installed pair: `just overseer-integration`

Everything above is a contract between two programs that are released
separately, so the version an operator runs is the only version that matters.
`just overseer-integration asl_root=/path/to/scripts` is the gate that judges
it, and the only one that does.

```bash
just overseer-integration asl_root=~/scripts
```

It drives ASL's own overseer leaves — policy, history, watch, checkpoint,
resume — against the `amx` in `~/.local/bin` and the `asl` on `PATH`, and checks
compatibility in both directions: the installed binary must answer the contracts
this ASL speaks, and a component that does not must be refused rather than read
past. A genuinely older AgentMux is not one reporting a lower contract number —
version 1 is the first — it is one whose payload carries no `contract` block at
all, and that has to read as unavailable rather than as permission.

The world it runs in is built and deleted by the suite: its own Git
repositories, its own state root, its own `config.toml` reached through
`AMX_CONFIG`, its own profile home and transcripts, and fake harness processes
standing in for the ones a lock names. It reads no operator configuration,
touches no real plan, and launches no runner — the resume path replaces the
launcher and nothing else.

The recipe refuses rather than shrugging when a half is missing or wrong: no
installed `amx`, no `asl` on `PATH`, or an `asl` that resolves outside the
checkout it was given each fail with the fix named. The ASL suite behind it
(`tests/test_asl_overseer_paired_smoke.py`) skips when there is no installed
pair, because a checkout is a legitimate place to have none; the recipe sets
`AMX_ASL_OVERSEER_SMOKE` to turn that skip into a failure, because a gate that
silently skips reports success for having done nothing.

The paired release gate runs this in its `smoke` phase, before `cockpit-smoke`.
See `docs/paired-releases.md`.

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
