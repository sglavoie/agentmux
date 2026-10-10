# Desktop releases

Agent Mux.app has a release gate of its own, `just mac-release-gate`, and its
own evidence record. It is separate from the [paired release
gate](paired-releases.md) for one reason: everything about the app needs Xcode,
and nothing about `amx` or its pairing with agent-session-loop may. A machine
that can build, test, and release `amx` must not need Xcode or a signing
certificate, so `just check`, `just acceptance`, and `just paired-release-gate`
never reach a `mac-*` recipe or the `macos/` tree. Two tests hold that:
`TestMacAppRecipesStayOutOfTheGates` (`cmd/release`) reads the Justfile, and
`TestTheTwoGatesStayApart` (`internal/release`) reads the two gates' own step
plans.

```bash
just mac-release-gate --signing-key ~/.ssh/amx_release
just mac-release-gate --notarized --signing-key ~/.ssh/amx_release
just mac-release-gate --rehearsal --evidence-dir /tmp/evidence
```

Unlike the paired gate, this one is safe to run on the machine it is typed on.
It installs nothing, restarts nothing, and writes no tag. The Xcode work goes
wherever `macos/scripts/remote-ui.sh` sends it: with `AMX_MAC_TEST_HOST` set the
build and both test runs happen on that Mac, and with
`AMX_UI_TEST_REMOTE_ONLY=1` they never happen here.

A desktop release needs both gates. The paired gate says the `amx` in the
bundle and its ASL peer are a tested pair; this gate says the bundle is that
`amx`, sealed, with its tmux, and that the app passes its suite as built for
release. Neither record stands in for the other.

## What the gate runs

A step that fails stops the run, and every later step is recorded as `not-run`.

| Phase | Step | What it establishes |
| --- | --- | --- |
| `source` | revision and working tree | Which tree is being released. Uncommitted changes stop the run unless `--rehearsal` is given. |
| `environment` | `sw_vers` | The machine the gate ran on. |
| `toolchain` | `macos/scripts/release-gate.sh toolchain` | That there is something to build with, and what: the Mac that runs Xcode, its macOS and Xcode versions, and how many Developer ID identities its keychain holds. |
| `build` | `just mac-app Release` | The Release bundle, with the `amx` that `just build` wrote and the pinned tmux runtime, signed inside out. |
| `build` | `macos/scripts/release-gate.sh bundle` | What the bundle is: its seal (`codesign --verify --strict --deep`), its tmux runtime against `SHA256SUMS`, that its `amx` is the one this checkout built, and how it is signed. |
| `verify` | `just mac-test` | The hosted suite against the Debug app. |
| `verify` | `just mac-test Release` | The same suite against the optimized app, the build that ships. |

The bundled `amx` is compared with the checkout's by Go build ID, not by file
hash: `sign.sh` re-signs the copy it bundles, so the two files differ in their
signatures and in nothing else.

The gate does not install the app. Installing, updating, and uninstalling are
exercised by the live acceptance under `docs/acceptance/ghostty-desktop/`
(`session-17` and `session-19`), which run the repository's installer against a
disposable install root; `just mac-install` is the operator's own step.

## Pending is not passed

The gate has a result the paired gate does not need: `pending`. It means the
gate could not judge the app because what it needs is missing:

- no Xcode on the Mac that builds;
- `AMX_MAC_TEST_HOST` names a Mac that does not answer;
- `AMX_UI_TEST_REMOTE_ONLY=1` with no other Mac named;
- `--notarized` on a Mac whose keychain holds no Developer ID Application
  identity.

A pending run writes a record with `"result": "pending"`, a `pending_reason`,
the `toolchain` step as `pending`, and every later step as `not-run`. The
command exits 2, the same status as a failed gate. Nothing downstream may read
it as success: `Passed()` is true only for `"passed"`, and a consumer that
knows only the paired gate's vocabulary reads any other value as not passed.

A step reports missing infrastructure by exiting 75. Anything else that goes
wrong once the toolchain has answered is a failure, including a remote Mac that
stops answering halfway.

## Signing and notarization

Without `--notarized` the gate accepts the bundle as `sign.sh` produced it and
records what that was in `bundle`:

| `distribution` | Meaning |
| --- | --- |
| `this-mac-only` | Ad-hoc or Apple Development signature. The bundle runs on the Mac that made it; this is what `just mac-install` installs. |
| `developer-id-not-notarized` | Developer ID signature, no stapled ticket. |
| `notarized` | Developer ID signature with a stapled notarization ticket. |

`--notarized` requires the last. Set `AMX_CODESIGN_IDENTITY` and
`AMX_NOTARY_PROFILE` as `macos/README.md` describes; the gate then fails on a
bundle that is anything else, and is pending when the keychain has no Developer
ID to sign with.

## Rehearsals

`--rehearsal` lets the gate run over a tree with uncommitted changes. The
record is marked `"rehearsal": true`, lists the changed paths, and carries
`tree_sha256`, a digest over every file Git tracks or would track, by path and
content. Its record ID folds that digest in, so a rehearsal never occupies the
record of the clean revision it stands on. A rehearsal can pass, and a passing
rehearsal is still not a release record: it describes a tree no revision names.

Without `--rehearsal` a dirty tree is refused before any step runs, and no
record is written.

## The evidence record

Every run that gets past the source check writes `<record-id>.json` under the
evidence directory (`--evidence-dir`, `[defaults.release] evidence_dir`, or
`~/.local/share/agent-mux/release-evidence`, the directory the paired gate
uses), with a detached `.sig` beside it when `--signing-key` is given.

```json
{
  "schema_version": 1,
  "gate": "desktop",
  "record_id": "amx-desktop-1.<16 hex>",
  "result": "passed",
  "rehearsal": false,
  "notarized_required": false,
  "repository": {"repo": "agentmux", "revision": "…", "clean": true},
  "environment": {"os_version": "…", "toolchain": [{"name": "xcode", "value": "…"}]},
  "bundle": [{"name": "amx_go_build_id", "value": "…"}, {"name": "signature", "value": "…"}],
  "steps": [{"name": "toolchain", "status": "passed", "exit_code": 0, "duration_ms": 0}]
}
```

`"gate": "desktop"` is what tells this record from a pair's; the paired gate's
reader refuses it. The record ID is `amx-desktop-1.` plus the first sixteen hex
characters of `sha256(revision \n tree_sha256 \n)`, with an empty tree digest
for a clean tree, so the same revision always addresses the same record. A
second run over it is refused unless `--force` is passed. As in the paired
record, collections are ordered lists and output is kept only for a failed
step.

The signature is made under the namespace `agentmux-desktop-release`:

```bash
ssh-keygen -Y verify \
  -f ~/.config/agent-mux/allowed_signers \
  -I release@example.com \
  -n agentmux-desktop-release \
  -s <record>.json.sig < <record>.json
```

A desktop signature does not verify under `agentmux-asl-release`, and a pair's
does not verify here. Then read the record: `"result": "passed"`,
`"rehearsal": false`, the revision is the one being released, and
`amx_go_build_id` in `bundle` is the build ID of the `amx` the paired record
hashed (`go tool buildid amx`).

## Testing the gate itself

`go test ./internal/release` runs the desktop gate against the stand-in world
the paired gate uses: a throwaway Git repository, a fake `just`, and a fake
`release-gate.sh`, with Git and `ssh-keygen` real. It covers a passing run, a
pending one, a failed step, a dirty tree with and without `--rehearsal`, and
the signature's namespace. It needs no Xcode, so it runs in `just check`.
