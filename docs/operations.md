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

Three gates sit outside `just check` because each needs something a hermetic
suite does not have:

| Recipe | Needs | Answers |
| --- | --- | --- |
| `just crash-restart` | nothing | Does state survive a crash, a restart, or a degraded start? Fault injection plus restart-equivalence replay, under `-race`. |
| `just reliability-stress` | a quiet machine | Do the daemon lifecycle scenarios still hold under repetition and contention, and are the latency budgets met? |
| `just cockpit-smoke` | a real Kitty window and an installed pair | Does the cockpit observe a live window through the real daemon? |

`crash-restart` counts the scenarios that ran and fails if the count collapses.
`go test -run PATTERN` exits 0 when the pattern matches nothing, so a renamed
scenario would otherwise turn the gate into a green step that checked nothing.

`cockpit-smoke` is read-only and fails rather than skips. It requires
`KITTY_WINDOW_ID`, a working `kitten @ ls`, a ready daemon, live
`export-events` for the current window, strict doctor, and a clean
`config asl-sync --check`. A smoke that passes because Kitty was absent reports
a boundary as exercised that was never touched.

CI runs `linux-portable`, `macos-full`, and `cross-repository-contract`
(`.github/workflows/check.yml`). The macOS lane speaks for a release; the Linux
lane is deliberately partial. Neither lane runs `cockpit-smoke` — no hosted
runner has a real Kitty — so that boundary is exercised only by an operator, in
`just paired-release-gate`.

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

### Pairing the two halves

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

### Diagnosing the integration

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

The report is `doctor --json` schema v2. `asl_unavailable` and `asl_read_only`
keep their exact names and meanings; the six dimension findings
(`asl_shell_resolution`, `asl_daemon_resolution`, `asl_contract_mismatch`,
`asl_projection_errors`, `asl_bridge_stale`, `asl_hook_delivery`) are new, which
is what the schema bump is for.

### Run presence is a paired cutover

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

### Deferred

These are known gaps, not bugs. Nothing in the cockpit is waiting on them:

- **Plans readiness and launch.** Agent Mux does not evaluate whether a plan is
  ready to run, and does not start runs. Use `asl prepare` and `asl run`.
- **Worktree-registry binding.** Runs are matched to managed worktrees by
  canonicalized workspace path, not by a shared registry identity.
- **Assurance inbox.** Verification findings are visible in the Workbench, not
  as an Agent Mux queue.
- **Model and profile brokering.** ASL chooses its own harness and model;
  Agent Mux profiles do not override what a run launches.

## Paired releases

Agent Mux and agent-session-loop are released as a pair. Neither is correct
alone: ASL owns the compatibility corpus and the bridge protocol, Agent Mux
vendors the corpus and speaks the protocol, and a release that ships one side
against an untested peer is the failure the whole contract-set mechanism exists
to prevent. `just paired-release-gate` is the procedure that makes a pair
releasable, and its signed evidence record is what says it was.

```bash
just paired-release-gate asl_root=~/scripts signing_key=~/.ssh/amx_release
just paired-release-gate asl_root=~/scripts signing_key=~/.ssh/amx_release tag=yes
```

Run it from a Kitty window, on the machine the pair is installed on. It has real
side effects: it installs both programs, restarts the running daemon, and with
`tag=yes` writes annotated tags into both repositories. Omitting `signing_key`
is allowed and produces an unsigned record — a rehearsal, not a release.

**`tag=yes` requires `signing_key`.** A rehearsal may run the whole procedure
and keep its unsigned record, but it may not tag: the pair tags are the durable
claim that a release happened, and the record's signature is the only thing that
makes that claim checkable. The recipe refuses the combination before starting
the toolchain and the gate refuses it again before reading anything, so no step
runs, no record is written, and neither repository's refs are touched.

As with `just asl-contract-sync`, `just` hands everything after the recipe name
to the recipe positionally, so `asl_root=` and `signing_key=` are stripped by the
recipe rather than parsed by `just`. Bare paths work too. Because the arguments
are positional, `tag=` has to come third: `asl_root=… tag=yes` puts `tag=yes` in
the signing-key slot, and the recipe refuses that rather than reading it as a key
path and quietly dropping the tag request.

### What the gate runs

The order is the argument, not an implementation detail. A step that fails stops
the run; everything after it is recorded as `not-run` rather than omitted, so a
truncated gate can never read as a complete one.

| Phase | Steps | Why here |
| --- | --- | --- |
| `source` | both revisions, both working trees | A release built from an unrecorded tree is unreproducible. Uncommitted changes stop the run before anything else executes. |
| `environment` | `sw_vers`, `kitty --version`, `asl harness-compat --json` | Seconds, not minutes. A missing Kitty or an unsupported harness should not surface after a twenty-minute stress run. No model is used and no provider is contacted. |
| `build` | `just build`, then hash the artifacts | The artifact exists before anything claims to have tested it. |
| `verify` | `just check`; ASL `test-all` and `lint`; `asl-contract-sync`; `asl-integration`; `crash-restart`; both `reliability-stress` gates | Cheapest gate first, the long stress and crash/restart runs last, so an ordinary regression fails fast. |
| `install` | ASL `just all`, ASL `just verify-asl-install`, Agent Mux `just install-restart-smoke` | **ASL first, then Agent Mux.** The Agent Mux install restarts the daemon, and a daemon restarted against a stale ASL proves nothing about the pair. |
| `smoke` | `just cockpit-smoke` | Against the installation the previous phase just produced, in a real Kitty window. |

Three artifact hashes are recorded: the built `amx` binary, the ASL wrapper, and
a digest over ASL's Python sources. The source-tree digest exists because ASL
ships as Python — there is no compiled artifact whose hash pins the code that
will run, and hashing only the wrapper would pin a twenty-line launcher while
the program it launches changed underneath it. Paths participate in the digest,
so a renamed module changes it.

**The gate does not prove intervention through Kitty.** `cockpit-smoke` proves
the cockpit observes a live window through the real daemon. Fetching a question
body, refusing a stale token, accepting a mutation, and receiving a body-free
receipt are proven against the installed ASL wrapper by `just asl-integration`,
which the gate runs separately in the verify phase. The two halves are recorded
as two steps because they are two claims.

### Active runs

`cockpit-smoke` ends with `amx config asl-sync --check`, which is the full
pairing preflight and writes nothing. **An active run is never migrated or
stopped.** If a live run still carries state this build cannot pair with, the
preflight refuses whole-root and names each run by pid, run ID, and plan
directory, and the gate fails on that step. Let those runs finish, or stop them
yourself, then re-run the gate. Do not work around it by pairing anyway:
migrating the corpus around a live run leaves the one process that is actually
writing on the old schema.

The gate never runs `amx config asl-sync` itself. Persisting a new executable
path and restarting onto it is an operator decision, and it is the one step in a
release that should be taken deliberately after reading the preflight.

### The evidence record

Every run writes `<pair-id>.json` under the evidence directory —
`[defaults.release] evidence_dir`, `--evidence-dir`, or
`~/.local/share/agent-mux/release-evidence` — with a detached `<pair-id>.json.sig`
beside it. The record names the two revisions, the three artifact hashes, the
contract set ID and the manifest SHA-256, the OS/Kitty/Go/harness versions, and
every command with its exit code, status, and duration.

The pair ID is `amx-asl-1.` plus the first sixteen hex characters of
`sha256(agentmux_revision \n asl_revision \n contract_set_id \n)`. Nothing
environmental goes in, so two operators releasing the same revisions arrive at
the same ID — which is what lets a tag in either repository be correlated with a
record.

The encoding is deterministic: every collection is an ordered list rather than a
map, so re-encoding the same run is byte-identical. The values that legitimately
differ between two runs of the same revisions are the timestamp and the measured
durations. Output is captured only for a failed step; a passing gate's record
would otherwise carry megabytes of test output that no consumer reads.

A failed run is still written and still signed. The signature attests that this
gate produced this record; `"result"` is what says whether the pair passed. **A
verifier must check both.**

The record describes its own signature. `"signature"` names whether the run
signed, under which namespace, with which key, and where the detached signature
was written — and it is written *before* `ssh-keygen` runs, so those bytes are
inside what the signature covers. A record that says `"signed": false` was not
signed by the run that produced it, whatever sits beside it on disk.

### Verifying a release record

```bash
ssh-keygen -Y verify \
  -f ~/.config/agent-mux/allowed_signers \
  -I release@example.com \
  -n agentmux-asl-release \
  -s ~/.local/share/agent-mux/release-evidence/amx-asl-1.<id>.json.sig \
  < ~/.local/share/agent-mux/release-evidence/amx-asl-1.<id>.json
```

The `allowed_signers` file is one line per trusted key:
`release@example.com ssh-ed25519 AAAA...`.

The namespace is not decoration. A signature made under
`agentmux-asl-release` verifies only when the verifier names that same
namespace, so a release key that also signs commits or Git objects cannot have
one of those signatures replayed as a release approval. Verifying without `-n`,
or with a different namespace, fails — that is the point.

Then check the record itself: `"result": "passed"`, `"schema_version": 1`,
`"signature": {"signed": true, ...}` naming this namespace, the revisions match
the tags, and the artifact hashes match what is installed (`amx --version`
reports the installed binary's own hash).

`"result"` has a third value. `"partial"` means the gate passed but pair tagging
broke midway and left a tag behind in one repository — see
[Tags](#tags). It is not a release, and the record says so in the one field
every consumer already reads.

### Tags

`tag=yes` writes the same annotated tag name — the pair ID — into both
repositories, carrying the pair ID, the manifest SHA-256, the contract set, both
revisions, the artifact hashes, and the evidence file name. Either repository
alone then answers "what was this released with".

Tagging requires a signing key. The tags are indistinguishable between a real
release and a rehearsal, so the thing that separates them has to be the record
they point at — and an unsigned record proves nothing about who produced it.
`--tag` without `--signing-key` is refused as an invocation error before the
gate reads a revision, so an unsigned run can never reach the tagging step at
all.

The gate refuses to re-tag a pair that already carries its tag, and it checks
for that **before** writing or signing anything, so a refusal cannot leave a
signed record describing tags that say something else. It also refuses to
overwrite an existing evidence record unless `--force` is passed.

**The two tags are one transaction.** They are written one after the other into
two independent repositories, and Git has no cross-repository commit, so the
atomicity is the gate's own: it remembers the object each tag creation produced,
and a failure on the second write removes the first before returning. A pair
tagged on one side only would be worse than an untagged pair — a release claim
the peer contradicts, next to a record written before either tag existed and
therefore naming neither.

The removal is a compare-and-delete —
`git update-ref -d refs/tags/<pair-id> <oid>` — so it clears only the object
this run created. A ref that has since been moved or replaced is left alone, and
a tag that predates the run is never a candidate at all.

**Both refs existing is not yet a release.** After tagging, the record is
rewritten to name the tags and re-signed over those bytes — and signing removes
the previous detached signature before it starts. Until that republication
lands, the tags are backed by the record written before they existed: one that
names no tags, and, mid-re-sign, one with no signature beside it at all. So the
republication is part of the same transaction. If the rewrite cannot be written,
or the signature over it cannot be produced, both tags are removed by the same
compare-and-delete and the run reports what it took back. Durable release tags
are never left standing next to evidence that is unsigned or that predates them.

The account of that retraction is then published in turn. When publication
itself is what failed, that write can fail too — in which case the record on
disk stays the pre-tag one, which names no tags and is therefore still true of
the repositories, and the returned error names both failures. The refs are gone
either way: a retracted claim beside an out-of-date record is recoverable, a
live claim beside one is not.

When a removal itself fails, the leftover ref is not left silent:

- the run's result becomes `"partial"`, never `"passed"`, so no reader — including
  one written before this state existed — can take a half-tagged pair for a
  released one;
- `"tags"` stays empty, because it means "this pair is tagged" and a pair tagged
  on one side is not;
- `"tag_failure"` names the cause, the stranded repository and tag, why the
  removal failed, and — for every stranded tag whose object this run
  established — the exact command that clears it, in `"recovery"`;
- the command is printed too, and it is printed even under `--json`, because it
  is the one gate outcome that left durable state outside the evidence
  directory.

Run that command, then re-run the gate. Nothing else has to be undone — the
evidence record for a `partial` run is a truthful account of an unreleased pair,
and the retry writes its own. The re-tag refusal is what stops a retry from
proceeding around a ref still sitting there.

**A stranded tag the run could not identify gets no command.** The gate reads the
object back immediately after writing the tag, and when that read fails the ref
is real but nameless: a tag exists under that name, and the run does not know
that the object under it is the one it wrote. Every removal the gate performs or
publishes is a compare-and-delete against that object, so there is no safe
removal to offer — and `git tag -d <pair-id>` is not a substitute, because it
deletes whatever answers to the name today, which is exactly what the
compare-and-delete exists to refuse.

Such a tag is listed in `"stranded"` like any other, and additionally in
`"unidentified"`, which carries why no object is known, read-only `"inspect"`
commands that report what the ref points at and what that object says about
itself, and a `"requirement"`: what must be established before anything is
deleted, and the shape of the deletion once it has been. The summary prints it
as `UNIDENTIFIED`, `inspect it with:`, and `before removing it:` — deliberately
not the `clear it with:` wording that marks a command as ready to paste. Nothing
published for an unidentified tag is executable as a deletion, so an operator or
script that runs every command in the record deletes nothing.

Clearing one is manual by design. Run the inspection, read the annotation — this
run's tag carries `paired release <pair-id>` and names its evidence file — and
only if it is this run's tag, remove it by the object id the inspection printed,
with `git -C <root> update-ref -d refs/tags/<pair-id> <oid>`. If the annotation
says something else, the ref is somebody else's and the gate was right not to
touch it. Then re-run the gate.

### Rolling back

Rollback is the install phase in reverse, in the reverse order — Agent Mux
first, then ASL — because the constraint is unchanged: never leave a daemon
running against a peer it was not tested with.

1. Check out the previous pair's revisions in both repositories. The tags name
   them; the evidence record for that pair confirms them.
2. `just install-restart-smoke` in Agent Mux. This rebuilds, installs
   atomically, restarts the daemon, and gates on strict doctor.
3. `just all` in the ASL checkout, then `just verify-asl-install`.
4. `amx config asl-sync --check`, then `amx config asl-sync` if the resolved
   path changed. Pairing is transactional: any failure after the write restores
   the previous configuration and restarts the daemon onto it.
5. `just cockpit-smoke`.

There is no state migration to undo. The contract set is checked, not converted,
and `asl-sync` refuses rather than migrating around a live run — so a rollback
that lands on a contract set the state root does not name will say so instead of
half-working.

### Maintenance installation

Reinstalling the current pair — after a Go toolchain upgrade, a Homebrew Kitty
update, or a machine restore — does not need the full gate. It needs the install
phase and the smoke:

```bash
just verify-asl-install        # in the ASL checkout
just install-restart-smoke     # in the Agent Mux checkout
just cockpit-smoke
amx doctor --strict
amx config hooks-status
```

This deliberately produces no evidence record. The record is a statement about a
*pair of source revisions*, and reinstalling the same revisions does not create
a new pair. If the revisions changed at all, run the gate.

### Harness compatibility policy

ASL owns the policy and Agent Mux records its verdict. `asl harness-compat`
classifies each installed harness against a pinned window per harness:

- **supported** — inside the tested window.
- **unknown** — outside it, or unreadable. This *warns*. A new upstream release
  is news, not a broken build, and blocking on it would mean every harness
  update breaks every user until someone moves a pin.
- **incompatible** — explicitly known-bad. This *blocks*.

The probe uses no model and contacts no provider: the only subprocess it builds
is `<executable> --version`, and ASL's own test pins that argv. The gate records
the verdict for all three harnesses in the evidence record's `environment`
block, so a release can be traced to the harness versions it was actually
exercised against. ASL's scheduled `harness-compatibility.yml` workflow keeps
the pinned window honest between releases.

### Continuously testing the gate itself

A gate whose logic is only exercised during a release is a gate that is
discovered to be broken during a release. `go test ./internal/release` runs the
entire procedure against a stand-in world: two throwaway Git repositories, an
ephemeral SSH key, a fake `just`, a fake `kitty`, and a fake ASL wrapper, all
under `t.TempDir()`. Git and `ssh-keygen` are real — the signature the test
produces is verified with the same command this document gives an operator — and
nothing in it touches the live installation.

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
  to `🛑`, so with no `[defaults.watchdog]` threshold configured such a pane can
  hold `⚡` indefinitely. Run the daemon at debug level and grep
  `screen stop probe: not settleable` to see the veto and fingerprint behind
  each declined observation.
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
