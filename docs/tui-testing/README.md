# TUI test harness

`amx tui-test` is a hidden developer/automation command that drives the Bubble
Tea dashboard model **in-process** to catch layout regressions — the kind of
overlay sizing bugs fixed in commits like "size profile picker box to fit its
widest row" and "size actions overlay box to fit its widest row".

It never launches a live Kitty instance, never talks to a real terminal, and
never reads or writes the operator's real config or data. Every run is
deterministic and isolated, so it is safe to run in CI or from an agent.

The repository acceptance gate runs every committed wide and narrow layout
scenario:

```bash
just tui-scenarios
just acceptance
```

See [`../acceptance.md`](../acceptance.md) for the cross-package workflow
coverage matrix.

The command is hidden from `amx --help` on purpose: it is a testing tool, not
part of the normal user workflow.

## Why it exists

The dashboard composes modals (the actions overlay, the profile/config picker,
confirmations) on top of a base view that clips content to preserve the status
and footer rows. A modal that renders wider or taller than the terminal can
provide gets squeezed or truncated by root composition. Screen-wide width and
height checks alone miss this, because the root render always fits the screen.
The harness therefore inspects each open modal's own natural geometry
*before* composition and reports the mismatch as a layout issue.

## How it works

- **In-process driver** (`tui/uitest_driver.go`): feeds window sizes, keys,
  text, and ticks straight through `Model.Update`, draining the resulting
  Bubble Tea commands with a short timeout so refresh-style flows settle before
  an observation is captured. Time-based commands (the refresh ticker) are
  abandoned rather than blocking.
- **Fixtures** (`tui/uitest_fixtures.go`): deterministic, self-contained data
  sources — a prepared config plus fake Kitty windows — so a run needs no live
  Kitty and no user config.
- **No-op recording Kitty client**: a `kitty.KittyClient` that returns canned
  window data and records every mutating/focus call instead of executing it, so
  scenarios can assert on the side effects the model *attempted* without a
  subprocess ever running.
- **Safe config mode**: config-backed scenarios stage `HOME`, `AMX_CONFIG`,
  `AMX_OVERRIDES_PATH`, `XDG_DATA_HOME`, and `XDG_STATE_HOME` in a throwaway
  temp directory, so a run can never touch the real config, overrides, plan
  index, transcript index, or daemon socket.
- **Normalized observations**: each step's screen is ANSI-stripped, measured by
  display cells, and annotated with any detected layout issues.
- **Modal-fit detection** (`tui/uitest_modal.go`): renders the active modal at a
  large viewport to recover its pre-clamp natural size, then compares against
  what root composition actually gave it.

## Command surface

All subcommands live under the hidden parent `amx tui-test`. `--json` is a
persistent flag on the parent, so it works after any subcommand.

### `amx tui-test list-fixtures [--json]`

Print the built-in fixture names (one per line, or a JSON string array with
`--json`).

```bash
amx tui-test list-fixtures
# actions
# asl_question
# asl_runs
# basic
# blocked_reasons
# family_unread
# project_families
# run_timers
# unseen_stops
# wide_profiles
# worktree_branches
# worktree_lifecycle
# worktrees
```

### `amx tui-test run SCENARIO... [--json]`

Run one or more scenario JSON files. With `--json` the output is a JSON array of
scenario results; without it, a compact `PASS`/`FAIL` summary with a nested line
per failure.

```bash
amx tui-test run docs/tui-testing/scenarios/*.json
amx tui-test run docs/tui-testing/scenarios/actions-popup-fit.json --json
```

### `amx tui-test fixture NAME [--width N] [--height N] [--key K ...] [--json]`

Run an ad hoc key sequence against a built-in fixture without authoring a
scenario file. `--key` is repeatable and applied in order. `--width`/`--height`
default to `0`, meaning "use the fixture's own default for that dimension"; each
dimension falls back independently, so overriding only one keeps the other at the
fixture default rather than zeroing it. With `--json` the
output is a JSON array of observations (one per step; index 0 is the pre-action
render).

```bash
amx tui-test fixture basic --width 120 --height 40 --key a --json
amx tui-test fixture wide_profiles --width 140 --key C
```

## Built-in fixtures

| Name | Shape | Purpose |
| --- | --- | --- |
| `basic` | two Claude work projects + a Codex personal project | realistic multi-profile dashboard baseline |
| `actions` | one session with a very long title | stresses the actions overlay box width |
| `asl_question` | `basic` with a human-actionable `❓🔁` supervisor handoff and a `⚡🔁` active run | ASL handoff presentation: attention grouping, gloss, `]q` jump |
| `asl_runs` | four supervisor runs spanning exact, ambiguous, second-exact, and unbound live sessions | Runs view focus outcomes and binding presentation |
| `blocked_reasons` | four blocked rows — two `🔑`, two `❓` — carrying a Codex permission kind, a Claude permission kind, a request-for-input kind, and no kind at all | the blocked-reason column: two harnesses' distinct kind strings reading as one `perm` tag, the widest tag setting the column, and an unlabelled row padding rather than collapsing |
| `family_unread` | a three-checkout family holding one unread `🛑` beside a seen `🛑` and an `⚡`, a second family with nothing unread, and an unfamilied seen `🛑` | the unread-completion marker inside a family: which row the `•` lands on when the family is folded versus unfolded, that a seen sibling never earns one, and that a family with no unread member stays unmarked in both folds |
| `project_families` | two multi-checkout repository families whose members span all three urgency buckets, plus a single-checkout family and a row belonging to no family | the Projects view: summaries, fold/unfold, the singleton that earns no summary, the unfamilied leaf, the project card, and count semantics under a filter |
| `run_timers` | five busy stretches: sub-minute, multi-minute, hours-long, warning-band, and idle | per-row run timer formatting bands and the `⏳` watchdog warning |
| `unseen_stops` | two `🛑` rows the daemon separates by seenness, plus one `⚡` row | the unseen-stop demotion: a read `🛑` falls to Idle while an unread one holds Needs Attention |
| `wide_profiles` | long profile names and home dirs | stresses the profile/config picker box width |
| `worktree_branches` | three rows in one repository — a dirty `main` checkout and two managed worktrees — plus a directory that is not a repository | the branch column: two worktrees told apart after the shared directory prefix has truncated away, the dirty `*` marker, a non-repository row padding rather than collapsing, and the column declining to be reserved when a filter leaves no branch in the view |
| `worktree_lifecycle` | deterministic reopen and cleanup outcomes | exercises focus/resume/fresh reopen, ambiguity, replacement profiles, cleanup refusals, safe deletion, forgetting, and partial recovery without real Git or Kitty mutations |
| `worktrees` | clean, dirty, live, looping, missing, and invalid managed worktrees | hermetic Worktrees browsing, filtering, badges, and layout |

The `basic` fixture's list is state/profile-grouped, not globally
alpha-sorted: the cursor starts on `dotfiles` (personal).

### Daemon-owned state and the frozen clock

A fixture may also carry the per-window state the daemon owns — icon state,
looping, the busy-stretch anchor, the unseen bit, the blocked reason, the Git
branch and dirty bit, and watchdog thresholds —
which no Kitty tab title can express. The `unseen_stops` fixture is the clearest
case: both of its `🛑` rows carry the same icon in the same title, and only the
daemon state tells them apart. `blocked_reasons` is the same argument one field
over: four rows wearing two icons between them, where what each is actually
waiting for is only ever on the wire. `worktree_branches` is the third: a branch
name is read by running `git` in the row's directory, which the harness never
does and a tab title never carries, so the fixture supplies what a probe found
exactly as the daemon would. The harness overlays it on the enumerated
sessions and
replays it on every refresh, so a `tick` action does not wipe the columns it
feeds. The daemon is never contacted: a fixture that supplies no such state
renders exactly as it would against an unreachable daemon, which stays the
default.

A fixture may likewise supply the repository-family identity discovery stamps
onto each row. That stamp normally comes from a Git probe against a directory
on disk, so a multi-checkout family could otherwise only be built out of real
temporary repositories — whose paths would then appear in the directory column,
in the detail strip, and in the project card, which is exactly what a committed
scenario cannot assert against. `project_families` is the fixture that supplies
it. Unlike the daemon-state block, a supplied family map is **authoritative for
every discovered row**, not a patch over what discovery produced: a window it
does not name has its family fields cleared. That is the only way to express
"this row belongs to no family at all", since discovery gives a configured
project a singleton family of its own. It is replayed on every refresh for the
same reason the daemon state is. Real-Git family resolution keeps its own
coverage in `internal/projectresolve` and `internal/session`, where temporary
repositories are appropriate because nothing there asserts on rendered text.

Because those columns include a seconds-resolution run timer, the harness pins
the model's render clock to one fixed instant instead of reading the wall
clock. Fixtures anchor their timestamps at fixed offsets from it, so every cell
renders identical text on every run and scenarios can assert it exactly. Only
the render clock is frozen; pulse decay and idle classification still follow
the wall clock.

### Asserting the unread-completion marker

When any line in the list has news, a two-column notice gutter is reserved in
front of the group border for the whole render pass, so a row reads `• ▌ …`
when it is the one with news and `  ▌ …` when it is not. Reservation scans the
whole projection rather than the visible window, so scrolling toward an unread
row does not shift the columns. A list where nothing has news drops the gutter
entirely and every row moves two cells left — which is why a scenario asserting
a marker should also assert the padded form of at least one unmarked row.

Three rules decide what a scenario will see:

- a **leaf** row carries `•` when the daemon says it is stopped *and* unseen. A
  stopped row whose completion has already been seen carries nothing, which is
  why every marker scenario pairs the marked row with a seen `🛑` control;
- a **collapsed family summary** carries `•` when any member it is hiding is
  unread, and the members themselves are off-screen. This is aggregation over
  the unread bit, not over the stop state: a folded family whose only stop has
  been read stays unmarked;
- an **expanded family summary** never carries `•`. The marker moves down to the
  unread child, so folding and unfolding relocates exactly one marker rather
  than duplicating it.

A live pulse outranks both: a pulsing row paints the state-colored `▌` pulse
glyph in the same gutter cell instead of the dot, and the dot returns unchanged
when the pulse decays, because seenness is daemon-owned and no keypress clears
it. Fixtures written to prove marker behaviour therefore avoid pulses entirely,
so the gutter's contents are decided by the unseen bit alone and a scenario
cannot mistake one arm of the rule for the other.

`family_unread` exists because the folded/unfolded question can only be asked
inside a multi-checkout family, and `project_families` could not answer it
without changing the counts nine scenarios already assert verbatim. Prefer
adding a narrow fixture over widening a load-bearing one for the same reason.

## Scenario schema

A scenario is a JSON object. Exactly one of `fixture` or `config` selects the
model source; `size` is the initial terminal size; `actions` are applied in
order; `assertions` are checked against the final observation.

```json
{
  "name": "actions-popup-fit",
  "fixture": "actions",
  "size": { "width": 120, "height": 40 },
  "actions": [{ "type": "key", "key": "a" }],
  "assertions": [
    { "type": "mode_equals", "mode": "actions" },
    { "type": "no_layout_issues" }
  ]
}
```

| Field | Meaning |
| --- | --- |
| `name` | optional label echoed in results |
| `fixture` | built-in fixture name (mutually exclusive with `config`) |
| `config` | path to a `config.toml` to load in a staged, isolated environment |
| `size` | initial terminal size (`width` and `height` must both be positive) |
| `actions` | ordered list of steps (see below) |
| `assertions` | checks run against the final observation |
| `include_observations` | when true, the result carries every per-step observation, not just the final one |

Unknown fields are rejected, so a typo like `"fixtures"` fails fast instead of
silently selecting nothing.

### Actions

| `type` | Fields | Effect |
| --- | --- | --- |
| `key` | `key` | press one named or printable key |
| `text` | `text` | type a run of printable characters, one key at a time |
| `resize` | `width`, `height` | change the terminal viewport (both must be positive) |
| `tick` | `count` | advance the refresh timer `count` times (0 or omitted = one tick) |

**Key names** accepted by `key` actions:

- named: `enter`, `esc`, `tab`, `shift+tab`, `backspace`, `space`, `up`, `down`, `left`,
  `right`, `home`, `end`, `pgup`, `pgdown`
- control chords: `ctrl+<printable>`, e.g. `ctrl+c`
- any single printable rune, e.g. `j`, `/`, `?`, `a`, `C`

### Assertions

| `type` | Fields | Passes when |
| --- | --- | --- |
| `no_layout_issues` | — | the final screen has no layout issues attached |
| `contains_text` | `text` | the final screen contains `text` |
| `excludes_text` | `text` | the final screen does not contain `text` |
| `mode_equals` | `mode` | the active UI mode equals `mode` |
| `selected_title` | `value` | the selected session's project title equals `value` |
| `selected_profile` | `value` | the selected session's profile equals `value` |
| `cursor_equals` | `cursor` | the relevant cursor equals `cursor` |
| `kitty_calls` | `op`, `count` | recorded Kitty calls for `op` match `count` (exactly, when set) or number at least one (when `count` is omitted) |

**Mode names**: `list`, `search`, `confirm`, `picker`, `config_picker`,
`help`, `detail`, `actions`, `session_search`, `session_view`,
`delete_choice`, `chip_picker`, `message_detail`, `plan_search`, `plan_view`,
`worktrees`, and `command_palette`.

**Kitty `op` names** recorded by the no-op client: `LaunchTab`,
`LaunchOverlay`, `CloseTab`, `MoveTab`, `SetTabColor`, `SetTabTitle`,
`SetUserVars`, `FocusTab`, `FocusWindow`, `LaunchOverlayPager`. Reads
(`ListWindows`/`GetText`) are not recorded.

## Layout issue types

`no_layout_issues` fails when the final screen carries any of:

| Issue | Meaning |
| --- | --- |
| `invalid_utf8` | a normalized line is not valid UTF-8 |
| `line_width_overflow` | a line is wider than the terminal width |
| `screen_height_overflow` | the view is taller than the terminal |
| `modal_width_overflow` | an open modal's natural content is wider than the box it was clamped to |
| `modal_height_overflow` | an open modal's rendered height exceeds the body area root composition gave it |
| `modal_clipped_by_root` | a modal fits by intrinsic size but its placement offset pushes it past the body area |

## Result JSON

`amx tui-test run --json` emits a JSON array of scenario results:

```json
[
  {
    "ok": true,
    "scenario": "actions-popup-fit",
    "final_observation": {
      "size": { "width": 120, "height": 40 },
      "mode": "actions",
      "screen": { "size": { "width": 120, "height": 40 }, "lines": ["..."], "width": 118, "height": 40 }
    }
  }
]
```

- `ok` is true only when every assertion passed.
- `failures` (present only on failure) preserves assertion order, each with a
  `kind` and a `message`.
- `final_observation` is always the last captured screen.
- `observations` is present only when the scenario set `include_observations`.

`amx tui-test fixture --json` instead emits a JSON array of observations (one
per step).

## Exit codes

Distinct codes let automation branch on process status alone:

| Code | Meaning |
| --- | --- |
| `0` | pass — every assertion held, no layout issue detected |
| `2` | a clean run with a failing assertion or a detected layout issue |
| `3` | invalid input — unreadable/malformed scenario, unknown fixture, unparseable key, or a staging failure |

On a scenario failure (code `2`) the harness prints its structured result and
exits with no extra stderr line. Only invalid-input errors (code `3`) print a
single reason line.

## Committed scenarios

The [`scenarios/`](scenarios) directory is the complete committed acceptance
inventory. `just tui-scenarios` passes `scenarios/*.json` to the harness, and
the in-package scenario test independently discovers and executes every JSON
file. The exact inventory is:

| File | Fixture | Covers |
| --- | --- | --- |
| [`baseline-layout.json`](scenarios/baseline-layout.json) | `basic` | dashboard list renders clean with all projects visible |
| [`actions-popup-fit.json`](scenarios/actions-popup-fit.json) | `actions` | actions overlay fits after pressing `a` on a long-titled session |
| [`asl-abort-confirmation-fit.json`](scenarios/asl-abort-confirmation-fit.json) | `asl_runs` | `a` confirms an abort against the captured run and states the 120-second grace and its degrade-to-stop behavior |
| [`asl-question-pending-narrow-fit.json`](scenarios/asl-question-pending-narrow-fit.json) | `asl_question` | a human-actionable ASL handoff leads Needs Attention in a compact pane, where the `awaiting response` gloss is gone with the rest of the wide footer and the one prioritized footer row has fallen back to bare keys |
| [`asl-question-pending-wide.json`](scenarios/asl-question-pending-wide.json) | `asl_question` | `]q` jumps from an unrelated row back to the human-actionable ASL handoff |
| [`asl-response-live-wide.json`](scenarios/asl-response-live-wide.json) | `asl_runs` | `r` opens the live response form with an answer editor, the Reply/Continue/Pause buttons, and the multiple-question counter |
| [`asl-response-paused-narrow-fit.json`](scenarios/asl-response-paused-narrow-fit.json) | `asl_runs` | a paused run's form offers only reply-and-resume and still fits a narrow terminal while typing |
| [`asl-runs-narrow-fit.json`](scenarios/asl-runs-narrow-fit.json) | `asl_runs` | narrow Runs rows keep state, label, reason, and age, and move the rest into the selected-row panel |
| [`asl-runs-wide.json`](scenarios/asl-runs-wide.json) | `asl_runs` | `A` opens Runs with needs-action rows first, the full wide columns, and no Kitty focus |
| [`band-boundary-79-compact-fit.json`](scenarios/band-boundary-79-compact-fit.json) | `project_families` | one cell below the standard band the rows have dropped the profile cell and the harness name, and the selected row's identity is carried by the detail strip instead |
| [`band-boundary-80-standard.json`](scenarios/band-boundary-80-standard.json) | `project_families` | exactly at 80 the standard band appears with the initial-only `[W]` profile cell and the harness name, and still without the spacious legend |
| [`band-boundary-119-standard.json`](scenarios/band-boundary-119-standard.json) | `project_families` | one cell below the spacious band the initial-only profile cell survives alongside the full activity, branch, and directory columns |
| [`band-boundary-120-spacious.json`](scenarios/band-boundary-120-spacious.json) | `project_families` | exactly at 120 the spacious band widens the profile cell to the padded name and adds the icon legend |
| [`blocked-reason-column-narrow-fit.json`](scenarios/blocked-reason-column-narrow-fit.json) | `blocked_reasons` | the reason tags survive the compact band, which has already dropped the profile, harness, activity, branch, and directory, and the selected row's context has moved into the two-line detail strip |
| [`blocked-reason-column-wide.json`](scenarios/blocked-reason-column-wide.json) | `blocked_reasons` | a Codex and a Claude permission prompt both read `perm`, a request-for-input reads `input`, an unlabelled blocked row pads to the same column, and no raw prompt kind reaches the screen |
| [`branch-column-narrow-fit.json`](scenarios/branch-column-narrow-fit.json) | `worktree_branches` | at 66 cells the compact band has taken the branch and directory off every row, and the detail strip is what tells the selected checkout from its sibling |
| [`branch-column-unreserved.json`](scenarios/branch-column-unreserved.json) | `worktree_branches` | filtering down to the non-repository row leaves no branch in the view, so the column is not reserved and the directory recovers its full width |
| [`branch-column-wide.json`](scenarios/branch-column-wide.json) | `worktree_branches` | two worktrees of one repository read their own branches, a dirty checkout carries the `*` marker, and the non-repository row pads so every directory starts in the same column |
| [`command-palette-execute-help.json`](scenarios/command-palette-execute-help.json) | `basic` | palette search executes the safe keyboard-help transition without Kitty mutations |
| [`command-palette-filter.json`](scenarios/command-palette-filter.json) | `basic` | printable text filters the palette to the matching plans command |
| [`command-palette-narrow-scroll.json`](scenarios/command-palette-narrow-scroll.json) | `basic` | narrow palette scrolls the active command into view without layout issues |
| [`command-palette-wide.json`](scenarios/command-palette-wide.json) | `basic` | wide empty-query palette shows grouped commands and inline disabled reasons |
| [`compact-selected-family-strip-fit.json`](scenarios/compact-selected-family-strip-fit.json) | `project_families` | the compact strip under a selected family summary reports the family's own bucket tally and then the primary checkout's profile, harness, branch, and directory |
| [`compact-selected-session-strip-fit.json`](scenarios/compact-selected-session-strip-fit.json) | `project_families` | the compact strip under a selected session recovers the profile, harness, branch, and directory the compact rows dropped |
| [`config-picker-fit.json`](scenarios/config-picker-fit.json) | `wide_profiles` | profile/config picker fits after pressing `C` with long profile names |
| [`config-picker-selection-transition.json`](scenarios/config-picker-selection-transition.json) | `wide_profiles` | `l` → `C` → `down` restores the old colored profile row and keeps the new selection within the modal |
| [`contextual-help-from-session-view.json`](scenarios/contextual-help-from-session-view.json) | `basic` | `?` opens the generated keymap over the truncated transcript viewer, which stays as the backdrop |
| [`family-unread-collapsed.json`](scenarios/family-unread-collapsed.json) | `family_unread` | folding the family moves the unread child's marker onto the summary, which reports the family's own tally while its members — read and unread alike — are hidden, and the family with nothing unread stays unmarked beside it |
| [`family-unread-expanded.json`](scenarios/family-unread-expanded.json) | `family_unread` | unfolding clears the summary's marker and puts exactly one `•` on the unread checkout, leaving its seen and active siblings aligned underneath |
| [`footer-cap-79-compact-fit.json`](scenarios/footer-cap-79-compact-fit.json) | `project_families` | one cell below the standard band the footer falls back wholesale to the single compact row, which spells the hints short and offers no `? more` because nothing was dropped |
| [`footer-cap-80-standard.json`](scenarios/footer-cap-80-standard.json) | `project_families` | exactly at 80 the footer is two standard rows ending in `? more`, and `q quit` is one of the hints that marker is standing in for |
| [`footer-cap-119-standard.json`](scenarios/footer-cap-119-standard.json) | `project_families` | one cell below the spacious band two rows still carry the full vocabulary through `x clear`, with `r restart` left behind the `? more` marker |
| [`footer-cap-120-spacious.json`](scenarios/footer-cap-120-spacious.json) | `project_families` | exactly at 120 the reclaimed cell buys `r restart` and pulls `p preview` up onto the first row, and the cap holds at two rows |
| [`footer-cap-short-height.json`](scenarios/footer-cap-short-height.json) | `project_families` | a 14-row viewport still spends only two rows on hints — the pagination token pays for itself by displacing `r restart` — and the list keeps four rows plus the more-indicator |
| [`footer-filter-chips-active.json`](scenarios/footer-filter-chips-active.json) | `project_families` | two active chips add their own removal hints to the footer without exceeding two rows or displacing the pinned status line |
| [`footer-gloss-narrow-fit.json`](scenarios/footer-gloss-narrow-fit.json) | `basic` | narrow footer keeps the surviving dashboard hints readable |
| [`footer-jump-chord-pending.json`](scenarios/footer-jump-chord-pending.json) | `project_families` | a pending jump chord replaces the whole footer with the chord's own vocabulary and its escape hatch, and the status line stays pinned underneath |
| [`footer-preview-suppressed.json`](scenarios/footer-preview-suppressed.json) | `project_families` | opening the preview pane suppresses the footer hint rows entirely, moves the toggle's own hint into the header, and still pins the status line |
| [`help-modal-narrow-fit.json`](scenarios/help-modal-narrow-fit.json) | `basic` | help remains bounded in a narrow viewport |
| [`help-modal-scroll-bottom.json`](scenarios/help-modal-scroll-bottom.json) | `basic` | help scroll reaches the final surviving bindings |
| [`legend-narrow-fit.json`](scenarios/legend-narrow-fit.json) | `actions` | live-session legend remains bounded at narrow width |
| [`list-preview-narrow-fit.json`](scenarios/list-preview-narrow-fit.json) | `actions` | list+preview falls back to the plain list below the row-column floor |
| [`picker-dsl-narrow-fit.json`](scenarios/picker-dsl-narrow-fit.json) | `basic` | launcher DSL/help text clamps at narrow width |
| [`projects-cross-view-selection.json`](scenarios/projects-cross-view-selection.json) | `project_families` | `v` carries the selected session across to Projects, where the cursor lands on the same session at its new tree index |
| [`projects-cross-view-summary-return.json`](scenarios/projects-cross-view-summary-return.json) | `project_families` | `v` back out of Projects from a family summary selects that family's primary session in Urgency rather than losing the cursor |
| [`projects-family-collapse.json`](scenarios/projects-family-collapse.json) | `project_families` | `←` on a summary folds its family away while the sibling family stays expanded and the status tally is unchanged |
| [`projects-family-expand.json`](scenarios/projects-family-expand.json) | `project_families` | `→` unfolds a collapsed family and restores every checkout row underneath it |
| [`projects-filtered-attention-counts.json`](scenarios/projects-filtered-attention-counts.json) | `project_families` | the `!attention` chip keeps only the two attention rows, drops the now-empty family summaries, and the status bar switches to the `V/N visible` form |
| [`projects-summary-action-refusal.json`](scenarios/projects-summary-action-refusal.json) | `project_families` | session actions pressed on a family summary are refused outright — no Kitty focus, launch, close, or pager call is made |
| [`projects-summary-preview.json`](scenarios/projects-summary-preview.json) | `project_families` | the preview pane for a selected family summary lists the family's bucket tally and every checkout with its branch and directory |
| [`projects-tree-expanded.json`](scenarios/projects-tree-expanded.json) | `project_families` | `v` replaces the urgency group borders with family summary lines and indented checkouts, leaving unfamilied and single-checkout rows flat |
| [`resize-stress.json`](scenarios/resize-stress.json) | `basic` | the dashboard survives a sequence of resizes and ends clean |
| [`picker-fit.json`](scenarios/picker-fit.json) | `actions` | launch picker rows stay within width after pressing `l` |
| [`picker-preview-fit.json`](scenarios/picker-preview-fit.json) | `actions` | launch picker list+preview split fits after pressing `l` then `p` |
| [`plan-search-header-narrow-fit.json`](scenarios/plan-search-header-narrow-fit.json) | `basic` | Plans header and filters fit at narrow width |
| [`plan-search-narrow-fit.json`](scenarios/plan-search-narrow-fit.json) | `basic` | Plans results fit at narrow width |
| [`plan-search-sort-rebind.json`](scenarios/plan-search-sort-rebind.json) | `basic` | `o` cycles the Plans sort order and the footer advertises `o sort`, not `s sort` |
| [`plan-view-fit.json`](scenarios/plan-view-fit.json) | `basic` | plan viewer read-error line wraps to width after `P` then `enter` |
| [`plan-view-narrow-fit.json`](scenarios/plan-view-narrow-fit.json) | `basic` | plan viewer remains bounded at narrow width |
| [`run-timer-narrow-fit.json`](scenarios/run-timer-narrow-fit.json) | `run_timers` | the compact band drops the activity column from every row and the selected row's timer survives in the detail strip |
| [`run-timer-wide.json`](scenarios/run-timer-wide.json) | `run_timers` | run timers survive refresh ticks and only running rows carry the `⏳` warning |
| [`search-narrow-fit.json`](scenarios/search-narrow-fit.json) | `basic` | live-list filter rows truncate at a sub-floor terminal after `/` |
| [`session-fork-continuity.json`](scenarios/session-fork-continuity.json) | `basic` | Past Sessions dispatches a native fork through the shared action registry |
| [`session-search-chips-fit.json`](scenarios/session-search-chips-fit.json) | `basic` | session-search filter chips clamp to width after pressing `S` |
| [`session-search-header-narrow-fit.json`](scenarios/session-search-header-narrow-fit.json) | `basic` | Past Sessions header and filters fit at narrow width |
| [`session-view-hints-wide.json`](scenarios/session-view-hints-wide.json) | `basic` | transcript viewer advertises the full continuity palette including `c continue` |
| [`session-view-hints-narrow-fit.json`](scenarios/session-view-hints-narrow-fit.json) | `basic` | transcript viewer hints truncate to the row budget and surface the actionable `? more` marker |
| [`spacious-identity-columns.json`](scenarios/spacious-identity-columns.json) | `wide_profiles` | the spacious band gives a long profile name its own truncated cell and lets a long title and its full directory share the same row |
| [`unseen-stop-grouping-narrow-fit.json`](scenarios/unseen-stop-grouping-narrow-fit.json) | `unseen_stops` | two identically-iconed 🛑 rows split across Needs Attention and Idle by the daemon's unseen bit, with the unread one leading the list at narrow width and wearing the only `•` the compact gutter paints |
| [`unseen-stop-grouping-wide.json`](scenarios/unseen-stop-grouping-wide.json) | `unseen_stops` | the seen 🛑 demotes to Idle while the unseen one holds Needs Attention, `]s` still reaches the demoted row, and jumping to it neither marks it nor clears the marker on the row that has one |
| [`worktrees-browse.json`](scenarios/worktrees-browse.json) | `worktrees` | `W` opens the hermetic managed-worktree inventory and whitespace-AND filtering narrows to the dirty row without exposing lifecycle mutations |
| [`worktrees-cleanup-active-refusal.json`](scenarios/worktrees-cleanup-active-refusal.json) | `worktree_lifecycle` | checkout removal is refused from a snapshot with a live session |
| [`worktrees-cleanup-dirty-refusal.json`](scenarios/worktrees-cleanup-dirty-refusal.json) | `worktree_lifecycle` | checkout removal is refused from a dirty snapshot |
| [`worktrees-cleanup-looping-refusal.json`](scenarios/worktrees-cleanup-looping-refusal.json) | `worktree_lifecycle` | checkout removal is refused from a snapshot with an active loop |
| [`worktrees-delete-merged-branch.json`](scenarios/worktrees-delete-merged-branch.json) | `worktree_lifecycle` | `D` confirms and completes safe deletion of a branch recorded as merged |
| [`worktrees-delete-unmerged-refusal.json`](scenarios/worktrees-delete-unmerged-refusal.json) | `worktree_lifecycle` | `D` refuses an unmerged branch before confirmation |
| [`worktrees-forget-invalid.json`](scenarios/worktrees-forget-invalid.json) | `worktree_lifecycle` | `F` explicitly forgets invalid sidecar metadata |
| [`worktrees-forget-missing.json`](scenarios/worktrees-forget-missing.json) | `worktree_lifecycle` | `F` explicitly forgets retained metadata for a missing checkout |
| [`worktrees-partial-recovery.json`](scenarios/worktrees-partial-recovery.json) | `worktree_lifecycle` | a typed partial cleanup reports the removed checkout and retained recovery row |
| [`worktrees-picker-narrow-fit.json`](scenarios/worktrees-picker-narrow-fit.json) | `worktree_lifecycle` | the ambiguous live-session picker remains usable at narrow width |
| [`worktrees-remove-retain-branch.json`](scenarios/worktrees-remove-retain-branch.json) | `worktree_lifecycle` | default `d` removal retains the branch and refreshes away the checkout row |
| [`worktrees-reopen-ambiguous.json`](scenarios/worktrees-reopen-ambiguous.json) | `worktree_lifecycle` | ambiguous live sessions open a detailed picker without focusing either candidate |
| [`worktrees-reopen-fresh.json`](scenarios/worktrees-reopen-fresh.json) | `worktree_lifecycle` | smart reopen launches fresh when no live session or transcript exists |
| [`worktrees-reopen-live.json`](scenarios/worktrees-reopen-live.json) | `worktree_lifecycle` | smart reopen focuses the sole live Kitty session |
| [`worktrees-reopen-replacement-profile.json`](scenarios/worktrees-reopen-replacement-profile.json) | `worktree_lifecycle` | a stale recorded profile detours through the shared profile picker and launches with the replacement |
| [`worktrees-reopen-resume.json`](scenarios/worktrees-reopen-resume.json) | `worktree_lifecycle` | smart reopen resumes the newest indexed conversation |
| [`worktrees-short-layout.json`](scenarios/worktrees-short-layout.json) | `worktree_lifecycle` | the inventory remains bounded in a short terminal |

Run them all:

```bash
amx tui-test run docs/tui-testing/scenarios/*.json
```

These scenarios are exercised in-package by
`tui/uitest_example_scenarios_test.go`, so `just test` fails if a committed
scenario stops parsing, regresses a guarantee, or differs from this documented
inventory. Retired palette and Workbench modes are not valid scenario modes.

### Sizing note

Modal-open `no_layout_issues` scenarios must size the terminal wide enough for
the overlay's natural width, or the detector will (correctly) flag
`modal_width_overflow`. Empirically the actions overlay needs a terminal
≥ ~100 cols; the `wide_profiles` config picker needs ≥ ~130 cols. Author a
*negative* scenario by opening a modal at a narrow size and asserting the
overflow is caught — that is the intended "fails deterministically when an
overlay needs more columns than the terminal can provide" behavior.
