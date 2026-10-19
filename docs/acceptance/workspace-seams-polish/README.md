# Workspace seams polish

Implementation and verification record for the six follow-ups proposed on
2026-10-19. This record distinguishes automated evidence from operator work.

## Scope and completion evidence

| Item | Implementation | Required verification |
| --- | --- | --- |
| Waiting context | Typed load/error/unavailable states, inline refresh/retry, retained content/read time, shared ASL prose renderer | Hosted retry, replacement-question, scrolling, formatting and narrow-window checks |
| Applied configuration | Startup fingerprints of daemon-consumed groups; `config daemon-status`; Doctor comparison; Settings restart and verification | Effective-equivalence and changed-group tests, old daemon and invalid config, failed restart/read recovery |
| Session explanations | Human prompt wording; winning reducer fact kind/time through state and companion snapshots | Projection, restored facts, missing evidence, native inspector tests |
| Find → History | Existing terminal scope labels plus nearby conversation action; generation/owner-checked query read; exact conversation scope and focus return | Real tmux query/ownership tests; native navigation and scoped search checks |
| Large diffs | Reused style attributes, no text replacement for wrapping alone, visible-region layout for large diffs | Measured large-patch render/layout, 30 selections, final hunk, wrapping and restoration regression suites |
| Daily-use evaluation | Task observations below, linked to the existing friction register | Operator observations, still pending |

## Performance measurements

Local Debug hosted XCTest on the development Mac, using a synthetic 1,798,962-byte
patch (6,000 long added lines). These are fixture measurements, not installed-app
latency or a guarantee on other machines.

Before: attributed text construction 42.0 ms unwrapped / 42.9 ms wrapped;
full TextKit layout 285.1 ms / 401.4 ms. Source:
`/tmp/amx-polish-focused.log`, `WorkspaceSeamsPolishTests`.

First optimized measurement: construction 19.0 ms / 18.7 ms; visible-region
layout 5.9 ms / 2.6 ms. A hosted 30-selection fixture with alternating wrapping
measured a maximum main-queue tick gap of 41.7 ms and reached the final hunk.
Source: `/tmp/amx-polish-perf.log`. That run exposed a small-diff restoration
regression; the implementation now retains full layout for those documents.
The final implementation limits deferred layout to initial views, waits for a
usable view size, and lays out the complete character range when restoring a
reading position. Full restoration still pays the complete layout cost; initial
file selection avoids it. Final measurements are recorded below.

## Operator task observations — pending

Use the existing [daily log](../ghostty-desktop/session-20/trial-log.md) and
[friction register](../ghostty-desktop/session-20/friction.md). Do not count
fixture runs as workdays. Capture these tasks when they occur in normal work:

1. Read a waiting question, refresh its context, then answer it. Note any lost
   reading position, unclear failure, or extra navigation.
2. Change a daemon setting during ordinary configuration work. Record whether
   the changed group, restart action, and verified result were clear. Compare
   with a project-only edit, which should not request a restart.
3. Inspect an unexpected session indicator. Record whether the explanation and
   observation time were sufficient, or diagnostics were still necessary.
4. Find an older conversation message from the terminal. Record whether the
   scope distinction, transferred query, exact conversation, and return focus
   matched expectations.
5. Review a substantial real patch. Record input stalls, hunk navigation,
   wrapping, and reading-position restoration.

For each interruption record build, task, recurrence, extra steps, estimated or
measured recovery time, and evidence. Rank recurring lost context and recovery
effort before isolated visual preferences. Unknown recurrence stays unknown.

## VoiceOver — waived

The user waived VoiceOver acceptance for this pass. No walkthrough is required
and no VoiceOver result is claimed.

## Final verification

- `env -u AMX_RUNTIME_SESSION_ID just check` passed: full Go suite, core race
  checks, vet, all 188 TUI acceptance scenarios, and lint (zero issues).
- The full hosted native suite executed 934 tests, with one existing accessibility
  lookup skipped and zero failures. Result:
  `Test-AgentMux-2026.10.19_17-33-02--0600.xcresult`. This final run includes the
  diff-restoration corrections and the History focus guard.
- Six 320×600-point Waiting captures passed geometry and text checks. Loading,
  formatted content, and retained failure were visually reviewed. See
  [loaded light](waiting-loaded-light.png), [failed dark](waiting-failed-dark.png),
  and [capture provenance](manifest.json).
- The final IPC health projection test passed after its additional assertion.

The final large-diff run passed deep wrapped-file reopening and 30 rapid
selections. Attributed text construction took 19.1/19.0 ms (unwrapped/wrapped);
initial visible-region layout took 5.8/2.7 ms. Complete layout for restoration
took 250.3/400.8 ms. The maximum main-queue tick gap during the 30 selections
was 43.3 ms. `just mac-app Release` built and signed the candidate; strict bundle signature
verification passed. The [candidate record](candidate.json) identifies the app,
bundled CLI/runtime, and changed source hashes. A compiler-warning cleanup to
the equivalent weak History capture was compiled in this final Release build
after the full native suite. No installation or operator trial is claimed.
