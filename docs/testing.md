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
