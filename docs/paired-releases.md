# Paired releases

Agent Mux and agent-session-loop are released as a pair. Neither is correct
alone: ASL owns the compatibility corpus and the bridge protocol, Agent Mux
vendors the corpus and speaks the protocol, and a release that ships one side
against an untested peer is the failure the whole contract-set mechanism exists
to prevent. `just paired-release-gate` is the procedure that makes a pair
releasable, and its signed evidence record is what says it was.

```bash
just paired-release-gate asl_root=~/1_dev_projects/sglavoie_agent-session-loop signing_key=~/.ssh/amx_release
just paired-release-gate asl_root=~/1_dev_projects/sglavoie_agent-session-loop signing_key=~/.ssh/amx_release tag=yes
```

Run it from any terminal, on the machine the pair is installed on. It needs a
tmux the installed `amx` can start agents on (`amx backend status --check`)
and no Kitty: no step runs or names one. It has real
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
recipe (`scripts/named-arg`, which also expands a leading `~`) rather than parsed
by `just`. Bare paths work too. Because the arguments
are positional, `tag=` has to come third: `asl_root=… tag=yes` puts `tag=yes` in
the signing-key slot, and the recipe refuses that rather than reading it as a key
path and quietly dropping the tag request.

The gate never needs Xcode. Agent Mux.app is released through a gate of its
own, `just mac-release-gate`, with its own evidence record; see
[Desktop releases](desktop-releases.md).

## What the gate runs

The order is the argument, not an implementation detail. A step that fails stops
the run; everything after it is recorded as `not-run` rather than omitted, so a
truncated gate can never read as a complete one.

| Phase | Steps | Why here |
| --- | --- | --- |
| `source` | both revisions, both working trees | A release built from an unrecorded tree is unreproducible. Uncommitted changes stop the run before anything else executes. |
| `environment` | `sw_vers`, `asl harness-compat --json` | Seconds, not minutes. An unsupported harness should not surface after a twenty-minute stress run. No terminal is probed: Kitty is an optional backend, and the tmux the pair runs on is judged by the smoke, against the installation. No model is used and no provider is contacted. |
| `build` | `just build`, then hash the artifacts | The artifact exists before anything claims to have tested it. |
| `verify` | `just check`; ASL `test-all` and `lint`; `asl-contract-sync`; `asl-integration`; `crash-restart`; both `reliability-stress` gates | Cheapest gate first, the long stress and crash/restart runs last, so an ordinary regression fails fast. |
| `install` | ASL `just all`, ASL `just verify-asl-install`, Agent Mux `just install-restart-smoke` | **ASL first, then Agent Mux.** The Agent Mux install restarts the daemon, and a daemon restarted against a stale ASL proves nothing about the pair. |
| `smoke` | `just overseer-integration`, then `just cockpit-smoke` | Against the installation the previous phase just produced. The overseer smoke needs only the two installed programs; the cockpit smoke also needs a real tmux and the daemon the install phase restarted, so the step that depends on the machine goes last. |

Three artifact hashes are recorded: the built `amx` binary, the ASL wrapper, and
a digest over ASL's Python sources. The source-tree digest exists because ASL
ships as Python — there is no compiled artifact whose hash pins the code that
will run, and hashing only the wrapper would pin a twenty-line launcher while
the program it launches changed underneath it. Paths participate in the digest,
so a renamed module changes it.

**The cockpit smoke observes; it does not intervene.** `cockpit-smoke` proves
that the installed `amx` resolves a tmux that runs (a round trip in a server of
its own, on a socket in a temporary directory, never the one holding your
agents), that the real daemon is ready and answers for the sessions it holds,
and that `amx doctor --strict` passes. It launches nothing and writes to no
pane. Reducing a real tmux session's hooks is proven by `just asl-integration`,
which drives real `asl run`s inside a private tmux session. Fetching a question
body, refusing a stale token, accepting a mutation, and receiving a body-free
receipt are proven against the installed ASL wrapper by `just asl-integration`,
which the gate runs separately in the verify phase. The two halves are recorded
as two steps because they are two claims.

**Kitty is not part of the gate.** `just kitty-smoke` is the smoke the cockpit
step used to be: run from a Kitty window, it requires a real `kitten @ ls`
round trip and the daemon's events for that window, and fails when there is no
Kitty. It is opt-in, for an operator who launches into Kitty; no gate runs it
and no record names it.

**What the overseer smoke proves.** `just overseer-integration asl_root=…` is
the only step that judges the two *installed* programs against each other. It
drives ASL's own overseer leaves -- policy, history, watch, checkpoint, resume --
against the `amx` in `~/.local/bin` and the `asl` on `PATH`, and it checks the
compatibility both ways: that the installed binary answers the contracts ASL
speaks, and that a component which does not is refused rather than read past.
Everything it touches is a throwaway world it builds and deletes -- its own Git
repositories, its own state root, its own `config.toml` reached through
`AMX_CONFIG`, its own profile home and transcripts, and fake harness processes
standing in for the ones a lock names. It never reads the operator's
configuration and never touches a real plan.

The other three paired gates cannot see what it sees. `just check` judges Agent
Mux's sources; `asl-integration` judges the installed ASL against *those*
sources; `asl-contract-sync` compares two checkouts offline. A version skew
between the two installed programs is invisible to all three, and it is the skew
an operator meets, because the two are released separately. Like the others it
fails rather than skips: the ASL suite behind it skips when there is no
installed pair, and the recipe sets `AMX_ASL_OVERSEER_SMOKE` to turn that skip
into a failure.

## Active runs

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

## The evidence record

Every run writes `<pair-id>.json` under the evidence directory —
`[defaults.release] evidence_dir`, `--evidence-dir`, or
`~/.local/share/agent-mux/release-evidence` — with a detached `<pair-id>.json.sig`
beside it. The record names the two revisions, the three artifact hashes, the
contract set ID and the manifest SHA-256, the OS, Go, and harness versions, and
every command with its exit code, status, and duration.

The record is `"schema_version": 2`. Schema 2 differs from 1 in one way: the
gate no longer runs `kitty --version`, so there is no `kitty-version` step and
`environment.kitty` is absent. A schema-1 record, which names the Kitty it was
exercised in, is still read (`release.LoadEvidence` accepts 1 and 2), so the
releases already signed stay verifiable; any other version is refused.

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

## Verifying a release record

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

Then check the record itself: `"result": "passed"`, `"schema_version": 2`,
`"signature": {"signed": true, ...}` naming this namespace, the revisions match
the tags, and the artifact hashes match what is installed (`amx --version`
reports the installed binary's own hash).

`"result"` has three values beyond `"passed"` and `"failed"`, and all three mean
a tagging attempt did not complete — see [Tags](#tags). `"partial"` means it
left a tag behind in one repository, which somebody has to clear.
`"not-released"` means it was taken back in full: every step passed, the pair
was asked to be tagged, and neither repository carries the tag. `"tagging"`
means the attempt never got far enough to say: it is what a tagging run writes
before its first ref exists, so it is what survives a run that was interrupted
or that could not publish its outcome at all. None of the three is a release,
and the record says so in the one field every consumer already reads —
including a consumer written before these values existed, which reads anything
other than `"passed"` as not passed.

A `"tagging"` record is the one to check the repositories against. It reports
that a run was in flight and nothing more: no tags, no cause, and no signature.
Look for the pair ID in both repositories' tags. Neither carries it in the
ordinary case, and the fix is to re-run with `--force`. If one or both do, the
run died between writing a ref and recording it; clear the pair tag with
`git update-ref -d refs/tags/<pair-id> <oid>` in each repository that has it,
then re-run.

## Tags

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

**Both refs existing is not yet a release.** A tagging run publishes twice. The
first record goes out before the first ref is written, says `"result":
"tagging"`, and is deliberately unsigned. The release claim — the record naming
both tags, and the signature over exactly those bytes — is the second
publication: the record is rendered and signed beside where it will live, and
only then are the signature and the record moved into place, in that order.

Those are two ordered renames rather than one atomic step, and the order they
happen in is what bounds the damage. A crash or a failure between them leaves
the provisional `"tagging"` record standing beside a signature that does not
cover it — a signature that verifies nothing, next to a record that claims
nothing, which the next attempt replaces. The guarantee is directional: a
`"passed"` record never becomes visible at the canonical path without the
signature covering it already there beside it. An operator who finds a signature
next to a `"tagging"` record has found an interrupted publication, not a
released pair.

Publishing the provisional record before the first ref is written is what makes
the failure case safe rather than merely recoverable. Between the two
publications the pair is in the one state the gate cannot make
atomic: refs in two independent repositories, and a record on disk that predates
them. If the final publication cannot be written or cannot be signed, both tags
are removed by the same compare-and-delete and the run reports what it took
back — and what outlives the process is the provisional record. It is not a
release claim, `Passed()` is false for it, and there is no signature anyone can
verify against it. Durable release tags are never left standing next to evidence
that predates them, and evidence that predates them never reads as a release.

The account of that retraction is then published in turn, so the file says which
refs went back and why rather than only that something was attempted. When
publication itself is what failed, that write can fail too. So the gate also
**retracts the superseded record in place**: it rewrites the file it already
owns with the same `"not-released"` account and discards any detached signature
beside it. Publication writes a sibling staging file and renames it, so it needs
to write the evidence *directory*; the retraction needs to write only that one
*file*, which is why it can still land when publication cannot. What it gives up
is atomicity — an interrupted retraction leaves a record that no longer parses,
and unreadable evidence is not a release claim either.

Neither of those writes is load-bearing. Both go to the medium that just failed,
and a recovery that assumes its own precondition is not one. They improve the
durable state; the guarantee comes from the provisional record, which is already
safe when they both fail.

The returned error still names the publication failure, because a retraction is
not a success: it means the account of the run could only be written by giving
up the guarantee an ordinary publication provides. What it buys is that all of
it agrees — the refs are gone, the record says the pair was not released and
which refs went back, `"signature"` reads `{"signed": false}`, and no signature
on disk verifies the record beside it.

**A retracted attempt is not a passed run**, whether the tags went back because
the second one could not be written or because the record that vouches for them
could not be published. The result becomes `"not-released"`, `"tags"` stays
empty, and `"tag_failure"` lists both refs under `"rolled_back"` with the cause
that stopped the attempt. The summary leads with that outcome — `pair <id>
(not-released)` — and the command exits non-zero, so the record, the operator's
screen, and the exit status say the same thing. There is nothing to clean up:
fix the cause and re-run. This is what separates it from `"partial"`, where a
ref is still out there and the next attempt is refused until it is gone.

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

## Publishing on GitHub

Use `just ship` to bump the version and publish a release:

```bash
just ship patch --dry-run
just ship patch                  # 0.8.0 -> 0.8.1
just ship minor                  # 0.8.0 -> 0.9.0
just ship major                  # 0.8.0 -> 1.0.0
```

Run from a clean `main` that contains `origin/main`. The command checks the
release prerequisites, updates `cmd/root.go`, commits the version, pushes
`main`, waits for the successful push CI run on that exact commit, then runs
`just release`. It installs both programs, restarts the daemon, runs both
release gates, pushes their pair tags and publishes the notarized downloads.
It preserves the existing pair tag scheme; it does not create `vX.Y.Z` tags.

The defaults are the sibling `../sglavoie_agent-session-loop` checkout and
the SSH evidence signing key `~/.ssh/amx_release`. Set `AMX_ASL_ROOT` and
`AMX_RELEASE_SIGNING_KEY` to override them. The ASL checkout must be clean and
its HEAD already on its `origin/main`; shipping does not commit or push ASL's
branch. `--dry-run` checks GitHub, both checkouts and the signing/build
prerequisites without changing the version, pushing, or running the gates.
It still fetches `origin/main` in both checkouts.

It needs a Developer ID Application identity in this Mac's keychain and a
notary profile, stored once (`macos/README.md`, "Notarizing a release build"):

```bash
xcrun notarytool store-credentials agentmux-notary --apple-id you@example.com --team-id TEAMID
```

`AMX_CODESIGN_IDENTITY` defaults to the keychain's first Developer ID
Application identity and `AMX_NOTARY_PROFILE` to `agentmux-notary`.
The SSH key signs the evidence records; it is separate from the Apple
Developer ID certificate. To create it once if it does not exist:

```bash
ssh-keygen -t ed25519 -f ~/.ssh/amx_release -C agentmux-release
```

To build and test on another Mac, set `AMX_MAC_TEST_HOST` and
`AMX_UI_TEST_REMOTE_ONLY` as for `just mac-test`:

```bash
AMX_MAC_TEST_HOST=user@other-mac AMX_UI_TEST_REMOTE_ONLY=1 just ship patch
```

The gates tag locally and push nothing. To publish an already committed,
pushed version without bumping it, use the lower-level command:

```bash
just release asl_root=~/1_dev_projects/sglavoie_agent-session-loop signing_key=~/.ssh/amx_release
```

`scripts/publish-release preflight` runs first and fails in seconds when `gh`
is not signed in, either tree is dirty or its HEAD is not on `origin/main`, the
version already has a GitHub release (`amx <version>`), or
`release-gate.sh toolchain --notarized` reports something missing: no Mac to
build on, no Xcode there, or no Developer ID or notary profile here. Then it
runs `paired-release-gate` with `tag=yes` and `mac-release-gate --notarized`
over the same revision, and on two passes publishes what they wrote:

- the pair tag, pushed to both repositories' `origin`;
- a GitHub release named `amx <version>` on the pair tag in Agent Mux's
  repository, carrying `Agent-Mux-<version>-macos.zip` (the notarized,
  stapled app), `amx-<version>-darwin-<arch>.zip` (amx signed with Developer
  ID and notarized on its own), and both evidence records with their
  signatures. agent-session-loop gets the tag, not a release.

Publishing reads only the records. It refuses a pair record that is not a
`"passed"`, signed record naming the tag in both repositories on the revisions
it names, a desktop record that is not a signed, notarized, non-rehearsal pass
of the same Agent Mux revision, an `amx` whose hash is not the one the pair
gate recorded, and an app that is not the bundle the desktop gate passed (its
executable's hash, the bundled amx's Go build ID, its stapled ticket, and
Gatekeeper). The standalone amx's signature changes the file, so it is tied
to the record by Go build ID, as the bundled copy is. Everything is checked
and amx is notarized before anything is pushed.

### Retrying a release

If shipping fails after the version commit, that commit is preserved. Do not
run `just ship patch` again: it would bump another version. If the failure
was in pushing `main` or waiting for CI, push `main`, let CI pass, then run
`just release` with the paths above. If the paired gate failed before tagging,
follow its evidence recovery instructions before rerunning it; an existing
record may need to be moved aside.

If the paired gate passed but the desktop gate failed, keep the pair's
evidence record and tags. Fix the desktop failure and run
`just mac-release-gate --notarized --signing-key ~/.ssh/amx_release`
(move aside a previous failed desktop record if the gate requests it).
Then publish using both evidence paths. When notarization, a tag push, or
the upload fails after both gates passed, use the same command:

```bash
just publish-release record=~/.local/share/agent-mux/release-evidence/<pair-id>.json \
  desktop_record=~/.local/share/agent-mux/release-evidence/<record-id>.json
```

The gates print their evidence paths. Publication is re-runnable: a release
that already exists only has its assets replaced.

## Rolling back

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
5. `just overseer-integration asl_root=…`, then `just cockpit-smoke`.

There is no state migration to undo. The contract set is checked, not converted,
and `asl-sync` refuses rather than migrating around a live run — so a rollback
that lands on a contract set the state root does not name will say so instead of
half-working.

## Maintenance installation

Reinstalling the current pair — after a Go toolchain upgrade, a Homebrew tmux
update, or a machine restore — does not need the full gate. It needs the install
phase and the smoke:

```bash
just verify-asl-install        # in the ASL checkout
just install-restart-smoke     # in the Agent Mux checkout
just overseer-integration asl_root=~/1_dev_projects/sglavoie_agent-session-loop
just cockpit-smoke
amx doctor --strict
amx config hooks-status
```

This deliberately produces no evidence record. The record is a statement about a
*pair of source revisions*, and reinstalling the same revisions does not create
a new pair. If the revisions changed at all, run the gate.

## Harness compatibility policy

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

## Continuously testing the gate itself

A gate whose logic is only exercised during a release is a gate that is
discovered to be broken during a release. `go test ./internal/release` runs the
entire procedure against a stand-in world: two throwaway Git repositories, an
ephemeral SSH key, a fake `just`, a `kitty` and a `kitten` that fail if anything
runs them, and a fake ASL wrapper, all
under `t.TempDir()`. Git and `ssh-keygen` are real — the signature the test
produces is verified with the same command this document gives an operator — and
nothing in it touches the live installation.
