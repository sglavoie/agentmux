# Build and test

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

Two of acceptance's steps are cheap enough to run on their own while iterating:
`just fmt-check` fails on any tracked Go file that is not gofmt-clean and
prints the paths (it leads `acceptance`, so a formatting slip fails in seconds
rather than after the race suites), and `just vet` runs `go vet ./...`.

### Running the suite from inside an agent session

The suite is safe to run from a shell inside an Agent Mux session: a tmux pane
on the private server, a Kitty window, or a terminal of the desktop workspace.
Such a shell carries variables naming its session and terminal
(`AMX_RUNTIME_SESSION_ID`, `AMX_WORKSPACE_HOSTED`, `TMUX`, `TMUX_PANE`,
`KITTY_WINDOW_ID`, `KITTY_LISTEN_ON`, and others), and production code reads
them to decide which session a hook or command is about. Every package whose
code reads them clears them in its `TestMain` through
`internal/testenv.ClearSessionEnv`, so a test sees a plain terminal unless it
sets one of them itself with `t.Setenv`. Wrapping the run in
`env -u AMX_RUNTIME_SESSION_ID ...`, as older acceptance records do, is no
longer needed.

`testenv.SessionVars` is the list, and it holds only session and terminal
identity. Configuration overrides (`AMX_CONFIG`, `AMX_TMUX`, `XDG_*`, `HOME`)
stay with each package's `TestMain`, which isolates them as it needs. A new
package whose code reads the session environment, directly or through
`internal/cli`, `internal/kitty`, `internal/tmux`, or `internal/indicator`,
calls `testenv.ClearSessionEnv()` first thing in its `TestMain`, and a new
variable of that kind goes into `SessionVars`. To check, run the suite with
bogus values set:

```bash
AMX_RUNTIME_SESSION_ID=rs-bogus TMUX=/tmp/x,1,0 TMUX_PANE=%9 KITTY_WINDOW_ID=7 \
  go test ./cmd/... ./internal/... ./tui/...
```

### Which install or smoke recipe

| Recipe | Does | Use it when |
| --- | --- | --- |
| `just install` | Builds `amx` and renames it atomically into `~/.local/bin`; writes a starter `config.toml` if none exists. Restarts nothing; a running daemon keeps its open executable. | You want the new CLI on `PATH` and will restart the daemon yourself, or no daemon runs. |
| `just install-restart-smoke` | `install`, then restarts the daemon onto the new binary, waits for it to report ready, and gates on `amx doctor --strict` (CLI and daemon the same artifact, no invariant violated). | The usual way to deploy a Go change from this checkout when there is no Xcode, or the app has not changed. |
| `just update` | `install`, syncs the hook files, builds and installs the Release `Agent Mux.app`, then restarts and verifies as `install-restart-smoke` does. Needs Xcode. | You want everything from this checkout installed: CLI, hooks, menu bar app, and daemon, judged as a pair. |
| `just cockpit-smoke` | Read-only check of what is already installed: the tmux `amx backend status` resolves runs, the daemon is ready, `export-events`, strict doctor, and `config asl-sync --check`. Fails rather than skips. | After an install, to confirm the installed pair works against a real tmux; from any terminal. |
| `just kitty-smoke` | Read-only check that the installed daemon observes a live Kitty window. In no gate. | Only if you launch agents into Kitty; run it from a Kitty window. |

The recipes that take a checkout path (`asl-contract-sync`,
`overseer-integration`, `paired-release-gate`) accept the documented
`asl_root=~/scripts` form from zsh as well as bash: zsh leaves a `~` after `=`
unexpanded, so the recipes expand a leading `~` themselves.

Three gates sit outside `just check` because each needs something a hermetic
suite does not have:

| Recipe | Needs | Answers |
| --- | --- | --- |
| `just crash-restart` | tmux | Does state survive a crash, a restart, or a degraded start? Fault injection plus restart-equivalence replay, under `-race`. Its tmux scenarios run real agents on a private server: the same process after a daemon restart, a killed viewer, or a change of owner; exited or lost, and never relaunched, when the agent or its server dies. |
| `just reliability-stress` | a quiet machine; tmux for the fleet budgets | Do the daemon lifecycle scenarios still hold under repetition and contention (including health asked while maintenance reconciles a real tmux server), and are the latency budgets met? The budgets include the fleet ones: one, ten, and thirty tmux agents, idle and printing, against the packaged tmux when `just mac-tmux` has built it (`internal/daemon/performance_fleet_test.go` holds the measured table and the ceilings). |
| `just cockpit-smoke` | tmux and an installed pair, from any terminal | Does the installed `amx` resolve a tmux that runs, is the real daemon ready and answering for its sessions, and do its invariants hold? |
| `just kitty-smoke` | a real Kitty window and an installed `amx` | Opt-in, in no gate: does the daemon observe a live Kitty window? |

`crash-restart` counts the scenarios that ran and fails if the count collapses.
`go test -run PATTERN` exits 0 when the pattern matches nothing, so a renamed
scenario would otherwise turn the gate into a green step that checked nothing.

`cockpit-smoke` is read-only and fails rather than skips. It requires a clean
`amx backend status --check`, a round trip of the tmux that names (a session
started, listed, and ended in a server of its own, never the one holding the
operator's agents), a ready daemon, `export-events`, strict doctor, and a clean
`config asl-sync --check`. A smoke that passes because tmux was absent reports
a boundary as exercised that was never touched. It names no Kitty, and
`TestJustCockpitSmokeFailsRatherThanSkips` holds it to that.

`kitty-smoke` is what the cockpit smoke was while Kitty was required:
`KITTY_WINDOW_ID`, a working `kitten @ ls`, a ready daemon, live
`export-events --current-window`, and strict doctor. It fails without a Kitty
and belongs to no gate.

CI runs `linux-portable`, `macos-full`, and `cross-repository-contract`
(`.github/workflows/check.yml`). The macOS lane speaks for a release; the Linux
lane is deliberately partial. Neither lane runs `cockpit-smoke` — no hosted
runner has an installed pair and a running daemon — so that boundary is
exercised only by an operator, in `just paired-release-gate`.

The app's suite, `just mac-test`, needs Xcode and is outside every gate above.
Its release gate is `just mac-release-gate`, which records its own evidence and
reports a machine with no Xcode as pending rather than passed; see
[Desktop releases](desktop-releases.md).

`WorkspaceReviewUsabilityTests` covers remembered Changes/Runs sidebars, native
diff keyboard focus after collapse, evidence inspection below a long plan, and
copy-feedback expiry through real window clicks and an isolated pasteboard.
`WorkspaceWorkflowPreviewTests` captures the expanded/collapsed layouts, selected
failed check, and simplified code blocks in light and dark appearances. Captures
use only fixture windows; own-window capture requires macOS 14.4 or newer.
`WorkspaceLifecyclePolishTests` exercises compact Waiting controls at 220, 300,
and 420 points; reading focus, changed-question refusal, accessibility actions,
return to the original terminal, and independent update ages. Increased-contrast
captures use 24-point prose and 20-point code in both appearances. The Copy
accessibility test reports a skip if SwiftUI's virtual nodes are unavailable to
the hosted lookup and the test host lacks system Accessibility access. That skip
is an unverified workflow, not an accessibility pass. The operator waived the
manual VoiceOver check for this pass; [workflow observations](acceptance/workspace-consistency/observations.json)
record the waiver separately from verified behavior.

The Codex completion lifecycle fixtures do not replace the installed-build and
physical sleep/wake observations recorded for
[F-001](acceptance/desktop-improvements.md#remaining-installed-build-check-for-f-001).

`just asl-integration` is separate and opt-in. Every other recipe is hermetic
and needs no `agent-session-loop` installed; this one launches the real bridge
against a throwaway plan and state root and checks the handshake, capabilities,
snapshot decoding, action and receipt shapes, the Workbench URL shape, and a
clean shutdown. When it is asked for, a missing or protocol-incompatible ASL
fails it rather than skipping — a silent skip would report a compatible pair
that was never actually checked.

Its second half (`internal/daemon`, `TestInstalledBridgeRoutesRunsByRuntimeSession`)
runs two real `asl run`s with a stand-in harness in a throwaway HOME: one in a
pane of a private tmux server, one as a Kitty runtime session. The `amx` ASL
finds on `PATH` is built from the checkout and dials a socket the test serves,
so real lifecycle and mailbox hooks reach this build's handler. It checks that
each run's lease, question, notification, and reply stay with its own session
across a daemon restart and with no viewer attached, that a stale action token
is refused, and that no question is announced twice. It needs `tmux` as well
as `asl`, and fails rather than skips without either.

## Live Kitty worktree smoke checklist

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

## Live Kitty session-actions checklist

`a` on a dashboard row, `a` in the F2 overview, and the command palette all run
the same session actions, and every automated test of them substitutes the
Kitty client. The live half is operator-attended, and it runs in a **disposable
Kitty instance with its own `HOME`, `AMX_CONFIG`, `PATH`, and remote-control
socket** — never the operator's. Automated verification must not replace the
installed binary, restart the working daemon, or mutate live sessions. The
isolation recipe, and the record of the last run, are in
[`docs/acceptance.md`](acceptance.md#session-actions-in-a-live-kitty).

Cover, on disposable managed tabs: focus; open a shell and open the configured
editor in a session's directory; a profile replacement end to end; a close and
its 30-second recovery (Resume for an exact conversation, otherwise Relaunch); a restart whose relaunch is refused; and closing or
restarting the tab the surface is itself running in. Record the Kitty version,
the artifact's revision and hash, the isolated roots, and the observed result
per check. An item the environment cannot safely provide is recorded as pending,
not as passed.

## TUI layout regression harness

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

It drives the **dashboard** model, and deliberately only that one. The F2
overview is a separate Bubble Tea program with its own `Update`/`View` loop, so
teaching this driver to speak to it would mean a second model behind one
scenario schema. Its equivalent evidence is the package-level acceptance suite
in `tui/overview/acceptance_test.go`, which drives the real loop against a frozen
clock, scripted snapshots, and a recording Kitty client that counts a write as a
violation:

```bash
go test ./tui/overview -run '^TestAcceptance' -count=1
```
