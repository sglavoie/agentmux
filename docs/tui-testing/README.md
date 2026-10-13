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

Its subject is the **dashboard** model and stays that way. The F2 live session
overview is a separate Bubble Tea program with its own `Update`/`View` loop, so
driving it from here would put a second model behind one scenario schema and one
set of fixtures. Its equivalent evidence is the package-level acceptance suite in
`tui/overview/acceptance_test.go` — the real loop against a frozen clock,
scripted snapshots, and a recording Kitty client that counts a write as a
violation — run with `go test ./tui/overview -run '^TestAcceptance'`. What the
two surfaces share is the declaration of what a session action is
(`tui/sessionactions`), and the record that both obey it is the parity matrix in
[`../acceptance.md`](../acceptance.md#session-actions-across-surfaces).

## Why it exists

The dashboard composes modals (the actions menu, the profile/config picker,
confirmations) on top of a base view that clips content to preserve the status
and footer rows. A modal that renders wider or taller than the terminal can
provide gets squeezed or truncated by root composition. Screen-wide width and
height checks alone miss this, because the root render always fits the screen.
The harness therefore inspects each open modal's own natural geometry
*before* composition and reports the mismatch as a layout issue.

## How it works

- **In-process driver** (`tui/uitest_driver.go`): feeds window sizes, keys,
  text, ticks, and render-routed mouse events through the model, draining the resulting
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
- **Modal-fit detection** (`detectModalFitIssues`, in `tui/uitest_screen.go`):
  renders the active modal at a
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
# attention_overlap
# basic
# blocked_reasons
# duplicate_names
# family_unread
# health_states
# no_sessions
# paused_output
# paused_queue
# preview_failure
# project_families
# run_timers
# triage_integration
# triage_vertical
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
| `actions` | one session with a very long title | stresses the actions menu box width |
| `asl_question` | `basic` with a human-actionable `❓🔁` supervisor handoff and a `⚡🔁` active run | ASL handoff presentation: attention grouping, gloss, and the `n`/`N` jump that replaced the retired `]q` chord |
| `asl_runs` | four supervisor runs spanning exact, ambiguous, second-exact, and unbound live sessions | Runs view focus outcomes and binding presentation |
| `attention_overlap` | rows carrying several claims on the operator at once — blocked-and-unread, failed-and-unread, looping-and-asking-and-unread — beside a single-reason unread row and a recoverable tool warning | simultaneous attention reasons: that a row can want the operator for more than one reason, that its Status column names only one of them, and that a tool warning stays in Running rather than joining them |
| `blocked_reasons` | four blocked rows — two `🔑`, two `❓` — carrying a Codex permission kind, a Claude permission kind, a request-for-input kind, and no kind at all | the Status column: two harnesses' distinct kind strings both reading `Needs permission`, a request for input reading `Needs input`, and an unlabelled blocked row reading `Awaiting reply` rather than going blank |
| `duplicate_names` | three colliding row names, each pair separated by exactly one thing — only the branch, only the profile, only the session identity — plus one name that occurs once | name disambiguation: each rung of the branch/worktree → profile → stable-identifier ladder in isolation, and the uncollided row a live-set-driven rename must leave alone |
| `family_unread` | a three-checkout family holding one unread `🛑` beside a seen `🛑` and an `⚡`, a second family with nothing unread, and an unfamilied seen `🛑` | the unread-completion marker inside a family: which row the `•` lands on when the family is folded versus unfolded, that a seen sibling never earns one, and that a family with no unread member stays unmarked in both folds |
| `health_states` | a live daemon whose hook files are in all three states at once — one profile in sync, one drifted, one missing | health presentation: the degradation that is NOT a dead daemon, which is the only health state the hermetic default cannot produce |
| `no_sessions` | a configured operator with two projects and no live windows at all | the empty hero, which is a different sentence from the filtered one and unreachable from any populated fixture |
| `paused_output` | one working session whose scrollback is longer than any pane the fixture's size can draw, numbered `output line 01`…`40` | the inspector's follow/hold split: a numbered buffer where following the tail and holding a scrolled position render visibly different lines |
| `paused_queue` | five rows spanning all three urgency bands — two working, an unread stop, a seen stop, and a working row with a non-ASCII name and an over-long directory — over a 60-line numbered scrollback, with destructive confirmation turned on | combined runs: a list that can be reordered or refiltered underneath a held inspector, round trips that return to the session inspector, and the only fixture that reaches the confirmation prompt |
| `portable_tmux` | three sessions hosted by the private tmux server — one needing permission, one working, one at rest — and no Kitty client at all | the dashboard as it runs in a stock terminal: rows no Kitty window stands behind are listed, selected, and inspected, and a Kitty-only command explains itself instead of reaching for a client that is not there |
| `preview_failure` | two working rows sharing a 40-line numbered scrollback whose first read fails and whose every read after it succeeds | the inspector's failed-read state and, more importantly, its recovery — that the pane reports the error without latching on it |
| `project_families` | two multi-checkout repository families whose members span all three urgency buckets, plus a single-checkout family and a row belonging to no family | the Projects view: summaries, fold/unfold, the singleton that earns no summary, the unfamilied leaf, the project card, and count semantics under a filter |
| `run_timers` | five busy stretches: sub-minute, multi-minute, hours-long, warning-band, and idle | per-row run timer formatting bands and the `⏳` watchdog warning |
| `triage_integration` | twelve sessions spanning every triage severity rank, zero/equal/distinct presentation timestamps, eleven canonical project labels, and `Client Portal`/`client portal` across profiles and repository identities | release-level ordering, case-insensitive project aggregation, quoted filtering, contextual completion, and project-switcher scrolling and contextual tallies |
| `triage_vertical` | the exact `triage_integration` data with `direction = "vertical"` configured | proves the dashboard ignores the setting — the inspector is a side-by-side split or it is full screen, and there is no stacked form for the setting to select |
| `unseen_stops` | two `🛑` rows the daemon separates by seenness, plus one `⚡` row | the unseen-stop demotion: a read `🛑` falls to Idle while an unread one holds Needs you |
| `wide_profiles` | long profile names and home dirs | stresses the profile/config picker box width |
| `worktree_branches` | three rows in one repository — a dirty `main` checkout and two managed worktrees — plus a directory that is not a repository | the branch as inspector detail rather than row column: two worktrees told apart by the branch only the selected one paints, the dirty `*` marker, and a non-repository row whose strip is the bare directory |
| `worktree_lifecycle` | deterministic reopen and cleanup outcomes | exercises focus/resume/fresh reopen, ambiguity, replacement profiles, cleanup refusals, safe deletion, forgetting, and partial recovery without real Git or Kitty mutations |
| `worktrees` | clean, dirty, live, looping, missing, and invalid managed worktrees | hermetic Worktrees browsing, filtering, badges, and layout |

The `basic` fixture's list is state/profile-grouped, not globally
alpha-sorted: the cursor starts on `dotfiles` (personal).

### Daemon-owned state and the frozen clock

A fixture may also carry the per-window state the daemon owns — icon state,
looping, the busy-stretch anchor, the durable presentation-transition time, the
unseen bit, the blocked reason, the Git branch and dirty bit, and watchdog thresholds —
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
temporary repositories — whose paths would then appear in the inspector's `Dir`
line and in the project card, which is exactly what a committed scenario cannot
assert against. `project_families` is the fixture that supplies
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

`triage_integration` also pins the presentation clock matrix directly: some
rows have no clock, two same-rank rows share one timestamp, and the remaining
rows have distinct timestamps. Its case variants intentionally use different
profiles and repository-family keys, proving that project completion and the
project switcher aggregate by operator-facing label rather than by either
implementation identity.

### Reads that fail

A fixture may also declare that its first `N` pane reads fail rather than
answering with its scrollback, which is what a `kitten @ get-text` against a
window that has gone away or a socket that is not answering yet looks like from
the inspector's side. It is a count rather than a flag because the property
worth committing a scenario to is recovery: a fixture that failed forever could
only ever show that the pane *reports* an error, never that it can stop
reporting one. Failures are counted per read in the order the reads reach the
client, not per target, so a fixture that sets `1` fails the opening paint and
answers every later read — a reopened inspector, a moved cursor — normally.

The error sentence is fixed rather than generated, because scenarios assert on
the line the inspector paints and a message that varied by run would make that
assertion untestable. `preview_failure` is the fixture that uses this, and the
pair of scenarios over it is the point: one asserts the error is on screen, the
other asserts it is gone after a second read.

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
| `mouse` | `x`, `y`, `event` | route a zero-based cell coordinate through the last rendered view; `event` is `left`, `wheel_up`, or `wheel_down` |
| `command` | `command_id` | run the catalog command `command_id` through the command palette |

A `command` action is how a scenario reaches another screen when the screen —
not the key that opens it — is the subject. It is not a way to call an operation
directly: it presses `:`, walks the palette's cursor to the named row with the
palette's own navigation keys, and presses enter, so everything a live operator's
keystrokes go through is still exercised.

Because the path is the real one, each way it can fail is reported as itself and
the scenario fails without the operation ever being issued:

- an unknown `command_id` is rejected by scenario validation, before the run
  starts;
- a command the dashboard **hides** for the current selection has no palette row,
  so there is nothing to walk to;
- a command that is visible but **disabled** is refused with its own
  unavailability reason, checked before enter is pressed;
- a `command_id` carried by more than one palette row is ambiguous and is never
  guessed at.

The palette opens from the session list only, so a `command` action is also
refused from inside another screen and while the project switcher is open
(`:` is not in the switcher's keymap). Use a `key` action for those, and for
any scenario whose subject IS a binding.

Mouse coordinates address the last rendered frame, so clicks and wheel events
exercise the same immutable hit map a live Bubble Tea view captured. Shift-drag
is intentionally not a scenario event: Kitty consumes that modified gesture for
terminal-native text selection before the application receives a mouse event,
so it remains part of the attended live checklist in `docs/acceptance.md`.

**Key names** accepted by `key` actions:

- named: `enter`, `esc`, `tab`, `shift+tab`, `backspace`, `space`, `up`, `down`, `left`,
  `right`, `home`, `end`, `pgup`, `pgdown`
- control chords: `ctrl+<printable>`, e.g. `ctrl+c`
- any single printable rune, e.g. `j`, `/`, `?`, `a`, `C`

### Assertions

| `type` | Fields | Passes when |
| --- | --- | --- |
| `no_layout_issues` | — | the final screen has no layout issues attached |
| `excludes_layout_issue` | `value` | the final screen has no layout issue of the type named by `value` — the narrow form, for a scenario that deliberately provokes one issue and must still forbid another. An unknown issue type fails validation rather than passing vacuously |
| `contains_text` | `text` | the final screen contains `text` |
| `excludes_text` | `text` | the final screen does not contain `text` |
| `mode_equals` | `mode` | the active UI mode equals `mode` |
| `selected_title` | `value` | the selected session's project title equals `value` |
| `selected_profile` | `value` | the selected session's profile equals `value` |
| `selected_action` | `value` | the actions menu is open and the highlighted entry's action ID equals `value`. Only meaningful in `actions` mode; asserting it in any other mode fails. `value` must name an action the menu actually offers, so a renamed or retired ID fails validation rather than passing vacuously |
| `cursor_equals` | `cursor` | the relevant cursor equals `cursor` |
| `kitty_calls` | `op`, `count` | recorded Kitty calls for `op` match `count` (exactly, when set) or number at least one (when `count` is omitted) |

**Mode names**: `list`, `search`, `confirm`, `picker`, `config_picker`,
`help`, `health`, `detail`, `actions`, `session_search`, `session_view`,
`delete_choice`, `chip_picker`, `message_detail`, `plan_search`, `plan_view`,
`worktrees`, `live_session_picker`, `asl_runs`, `asl_response`,
`project_switcher`, `command_palette`, `send`, and `plan_run`. An unmapped mode reports `unknown`
rather than a bare integer, so a `mode_equals` assertion against a mode nobody
named fails visibly.

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
      "cursor": 0,
      "selected_action": "session.focus",
      "screen": { "size": { "width": 120, "height": 40 }, "lines": ["..."], "width": 118, "height": 40 }
    }
  }
]
```

- `ok` is true only when every assertion passed.
- `cursor` and `selected_action` are omitted when they carry nothing to say:
  `selected_action` is present only while the actions menu is open, so its
  absence is "no menu", not "no selection".
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
| [`baseline-layout.json`](scenarios/baseline-layout.json) | `basic` | the default wide dashboard draws one-line Session/Status/Time rows whose opening cell is the name — no tab number, no profile cell, no harness badge — with the selected row's identity — its agent, profile, and directory — carried by the inspector beside the list |
| [`actions-popup-fit.json`](scenarios/actions-popup-fit.json) | `actions` | actions menu fits after pressing `a` on a long-titled session |
| [`actions-menu-initial-selection.json`](scenarios/actions-menu-initial-selection.json) | `actions` | the menu opens on Open session at the narrowest target size, with the last band still drawn — the selection is a named action ID, not a row number, so a reordered menu fails here rather than silently pointing elsewhere |
| [`actions-menu-selection-moves.json`](scenarios/actions-menu-selection-moves.json) | `actions` | `j` twice walks the selection down the Open band to Launch another session at the widest target size |
| [`actions-menu-disabled-entry-explains.json`](scenarios/actions-menu-disabled-entry-explains.json) | `actions` | `↓` six times reaches Clear status on a row carrying no badge: a disabled entry is still selectable and the menu states why it will not run rather than hiding it |
| [`actions-menu-nested-picker-fit.json`](scenarios/actions-menu-nested-picker-fit.json) | `basic` | the profile picker the menu opens for a restart-with-another-profile fits 80×24 — the menu's own fit is only half the claim, since every nested surface it raises has to fit the same terminal |
| [`actions-menu-nested-confirmation-fit.json`](scenarios/actions-menu-nested-confirmation-fit.json) | `paused_queue` | the close confirmation the menu raises fits 80×24 with a Unicode subject, names that subject, and carries its whole consequence sentence; nothing was closed, because the prompt is still up |
| [`asl-abort-confirmation-fit.json`](scenarios/asl-abort-confirmation-fit.json) | `asl_runs` | `a` confirms an abort against the captured run and states the 120-second grace and its degrade-to-stop behavior |
| [`asl-question-pending-narrow-fit.json`](scenarios/asl-question-pending-narrow-fit.json) | `asl_question` | a human-actionable ASL handoff leads Needs you in a compact pane, reading `Awaiting reply` in its Status column while the `🔁` supervision marker is off screen entirely — 30 cells is below the split floor, so there is no inspector to carry it — and the `awaiting response` gloss is gone with the rest of the wide footer |
| [`asl-question-pending-wide.json`](scenarios/asl-question-pending-wide.json) | `asl_question` | `n` walks back from an unrelated row to the human-actionable ASL handoff, whose row carries only its name, `Awaiting reply`, and a time — the `🔁` belongs to the strip and the `❓` gloss to the footer |
| [`asl-response-live-wide.json`](scenarios/asl-response-live-wide.json) | `asl_runs` | `r` opens the live response form with an answer editor, the Reply/Continue/Pause buttons, and the multiple-question counter |
| [`asl-response-paused-narrow-fit.json`](scenarios/asl-response-paused-narrow-fit.json) | `asl_runs` | a paused run's form offers only reply-and-resume and still fits a narrow terminal while typing |
| [`asl-runs-narrow-fit.json`](scenarios/asl-runs-narrow-fit.json) | `asl_runs` | narrow Runs rows keep state, label, reason, and age, and move the rest into the selected-row panel |
| [`asl-runs-wide.json`](scenarios/asl-runs-wide.json) | `asl_runs` | opening Runs by name gives needs-action rows first, the full wide columns, and no Kitty focus |
| [`attention-overlap-reasons.json`](scenarios/attention-overlap-reasons.json) | `attention_overlap` | rows wanted for several reasons at once all land in Needs you, their Status column naming one reason each, but only the unread stop earns the `•`, and the recoverable tool warning stays in Running |
| [`band-boundary-79-compact-fit.json`](scenarios/band-boundary-79-compact-fit.json) | `project_families` | one cell below the standard band the queue rows are exactly the rows every other width draws — the width bands no longer reach a session row — and the quiet header and essentials footer read the same as at every other width, 79 having bought only that the inspector key is not among the hints that fit |
| [`band-boundary-80-standard.json`](scenarios/band-boundary-80-standard.json) | `project_families` | exactly at 80 nothing in the chrome moves: the same header line and the same essentials footer, with the retired `/ to filter` hint and the spacious legend both gone for good |
| [`band-boundary-119-standard.json`](scenarios/band-boundary-119-standard.json) | `project_families` | one cell below the spacious band the rows are unchanged again, the icon legend is gone for good, and the footer has already bought `tab switch pane` and `i inspector` |
| [`band-boundary-120-spacious.json`](scenarios/band-boundary-120-spacious.json) | `project_families` | exactly at 120 the reclaimed cell buys nothing — the legend is retired, so the header and footer match 119 exactly while the queue rows stay identical to all three narrower bands |
| [`blocked-reason-column-narrow-fit.json`](scenarios/blocked-reason-column-narrow-fit.json) | `blocked_reasons` | every blocked kind reads as a whole Status phrase at a width too narrow to keep the Time column, with the profile, harness, and directory nowhere on screen — that width is below the split floor, so nothing is left to carry them |
| [`blocked-reason-column-wide.json`](scenarios/blocked-reason-column-wide.json) | `blocked_reasons` | a Codex and a Claude permission prompt both read `Needs permission`, a request for input reads `Needs input`, an unlabelled blocked row reads `Awaiting reply`, and no raw prompt kind reaches the screen |
| [`branch-column-narrow-fit.json`](scenarios/branch-column-narrow-fit.json) | `worktree_branches` | at 66 cells no branch is on screen at all: the branch and directory belong to no row at any width, and this width is below the split floor, so the inspector that would carry them is gone and the two checkouts are told apart by name alone |
| [`branch-column-unreserved.json`](scenarios/branch-column-unreserved.json) | `worktree_branches` | filtering down to the non-repository row leaves no branch anywhere, so the strip's second line is the bare directory rather than a dangling separator |
| [`branch-column-wide.json`](scenarios/branch-column-wide.json) | `worktree_branches` | the branch is the inspector's, not the row's: only the selected worktree's `amx/feat-x` is on screen, and its siblings' branches are nowhere |
| [`combined-actions-during-refresh.json`](scenarios/combined-actions-during-refresh.json) | `paused_queue` | three refresh ticks under an open actions menu leave the menu open on the row it was opened for: the refresh reaches only the chrome behind it — the health reading goes cold — and neither the refresh nor the captured overlay touches a Kitty tab |
| [`combined-filter-then-resize.json`](scenarios/combined-filter-then-resize.json) | `paused_queue` | an applied `@work` chip survives narrowing to 80 cells and widening back: the same three rows, the same selection, the chip still in the header, and the inspector's metadata redrawn whole once the split floor is cleared again |
| [`combined-folded-attention-reveal.json`](scenarios/combined-folded-attention-reveal.json) | `family_unread` | `n` reaches an attention row folded inside a collapsed family, unfolding it on the way, and the inspector swaps from the family card back to the session inspector for the checkout it landed on |
| [`combined-preview-read-failure.json`](scenarios/combined-preview-read-failure.json) | `preview_failure` | a failed read paints `error: kitty read failed` where the buffer would be, with none of the scrollback on screen, while the metadata block above the divider still names the session the read was for |
| [`combined-preview-read-recovery.json`](scenarios/combined-preview-read-recovery.json) | `preview_failure` | hiding and reopening the inspector re-reads the same pane, the second read succeeds, and the tail of the numbered buffer replaces the error — the pane recovers rather than latching on the failure |
| [`combined-reorder-while-paused.json`](scenarios/combined-reorder-while-paused.json) | `paused_queue` | a held inspector keeps its scrolled position while the view toggle rebuilds the queue underneath it into family order: the `── Paused ──` marker and the same buffer line survive a reorder that moves every row on screen |
| [`command-action-by-name.json`](scenarios/command-action-by-name.json) | `basic` | three `command` actions reach Plans, the worktree inventory, and a live Kitty focus by capability name, each leaving the palette closed behind it |
| [`command-palette-execute-help.json`](scenarios/command-palette-execute-help.json) | `basic` | palette search executes the safe keyboard-help transition without Kitty mutations |
| [`command-palette-filter.json`](scenarios/command-palette-filter.json) | `basic` | printable text filters the palette to the matching plans command |
| [`command-palette-narrow-scroll.json`](scenarios/command-palette-narrow-scroll.json) | `basic` | narrow palette scrolls the active command into view without layout issues |
| [`command-palette-wide.json`](scenarios/command-palette-wide.json) | `basic` | wide empty-query palette shows grouped commands and inline disabled reasons |
| [`compact-below-split-floor.json`](scenarios/compact-below-split-floor.json) | `project_families` | below the split's width floor the dashboard is the queue alone — no divider, no inspector, and no metadata the one-line row cannot carry — and the footer is the six labelled essentials, which is all 72 cells pay for |
| [`compact-family-below-split-floor.json`](scenarios/compact-family-below-split-floor.json) | `project_families` | the same floor in Projects view: a collapsed family summary carries its own tally and nothing beside it, and the one-cell-wider fold hint tips the footer wholesale into its bare-key spelling |
| [`config-picker-fit.json`](scenarios/config-picker-fit.json) | `wide_profiles` | profile/config picker fits with long profile names, reached the way the dashboard reaches it now — `a` for the actions menu, then its `C` entry |
| [`config-picker-selection-transition.json`](scenarios/config-picker-selection-transition.json) | `wide_profiles` | `l` → `C` → `down` restores the old colored profile row and keeps the new selection within the modal |
| [`contextual-help-from-session-view.json`](scenarios/contextual-help-from-session-view.json) | `basic` | `?` opens the generated keymap over the truncated transcript viewer, which stays as the backdrop |
| [`duplicate-names-collisions.json`](scenarios/duplicate-names-collisions.json) | `duplicate_names` | conditional disambiguation: each colliding pair is separated by exactly the thing that differs — `(main)`/`(amx/feat-x)`, `(work)`/`(personal)`, `(#51)`/`(#61)` — and the name that occurs once is left bare |
| [`edge-cold-daemon-after-refresh.json`](scenarios/edge-cold-daemon-after-refresh.json) | `paused_queue` | refresh ticks against an unreachable daemon settle on `✖ daemon down` rather than staying on the provisional `○ checking health`, and the rows keep rendering from what the fixture supplied |
| [`edge-filter-matches-nothing.json`](scenarios/edge-filter-matches-nothing.json) | `paused_queue` | a chip that matches nothing reaches the filtered hero — `esc  to clear filters`, not `l  to launch` — with the chip still in the header and no row text behind it |
| [`edge-long-metadata-truncation.json`](scenarios/edge-long-metadata-truncation.json) | `paused_queue` | a non-ASCII name beside ASCII ones keeps the Status and Time cells aligned, and a directory too long for the pane truncates to `…` in the inspector's Dir line rather than wrapping it |
| [`edge-single-session.json`](scenarios/edge-single-session.json) | `wide_profiles` | one session is a list of one: a single `Running (1)` heading with neither other bucket printed, and the inspector on the only row there is |
| [`edge-zero-sessions.json`](scenarios/edge-zero-sessions.json) | `no_sessions` | nothing running at all reaches the other hero — `l  to launch` — and the inspector reads `(no session selected)` rather than going blank |
| [`family-unread-collapsed.json`](scenarios/family-unread-collapsed.json) | `family_unread` | folding the family moves the unread child's marker onto the summary, which reports the family's own tally while its members — read and unread alike — are hidden, and the family with nothing unread stays unmarked beside it |
| [`family-unread-expanded.json`](scenarios/family-unread-expanded.json) | `family_unread` | unfolding clears the summary's marker and puts exactly one `•` on the unread checkout, leaving its seen and active siblings aligned underneath |
| [`footer-cap-79-compact-fit.json`](scenarios/footer-cap-79-compact-fit.json) | `project_families` | one cell below the standard band the single row still spells all six essentials in full and offers no `? more`, because the labelled row fits and nothing above it was dropped |
| [`footer-cap-80-standard.json`](scenarios/footer-cap-80-standard.json) | `project_families` | exactly at 80 the footer is the same one row of essentials — the retired band boundary is no longer a footer input — with no `? more` and no jump chord |
| [`footer-cap-119-standard.json`](scenarios/footer-cap-119-standard.json) | `project_families` | one cell below the spacious band the single row spends its surplus on `n/N needs you` and `tab switch pane` and stops, with `i inspector` and the rest left behind the `? more` marker |
| [`footer-cap-120-spacious.json`](scenarios/footer-cap-120-spacious.json) | `project_families` | exactly at 120 the reclaimed cell buys no further hint and no legend: the row is identical to 119, and the cap holds at one row |
| [`footer-cap-short-height.json`](scenarios/footer-cap-short-height.json) | `project_families` | a 14-row viewport still spends exactly one row on hints — the pagination token pays for itself by displacing `tab switch pane` — and the reserved feedback slot above it leaves the list its rows plus the more-indicator |
| [`footer-filter-chips-active.json`](scenarios/footer-filter-chips-active.json) | `project_families` | two active chips add their own removal hints to the single footer row, displacing lower-priority hints behind `? more` rather than wrapping, and the retired session tally does not come back to carry them |
| [`footer-gloss-narrow-fit.json`](scenarios/footer-gloss-narrow-fit.json) | `basic` | narrow footer keeps the surviving dashboard hints readable |
| [`footer-survives-inspector.json`](scenarios/footer-survives-inspector.json) | `project_families` | the inspector shares the screen with the footer rather than displacing it, and the footer is now the only thing naming the key that hides the pane — the header's duplicate hint is gone |
| [`full-screen-inspector-tab-inert.json`](scenarios/full-screen-inspector-tab-inert.json) | `paused_queue` | a terminal below the split floor opens the inspector over the whole body, and both Tab spellings answer nothing there: the keys stay on the one pane that is drawn, `home` still pauses the output on its first line, the hidden selection has not moved off `charlie`, and the footer withholds `switch pane` rather than naming a move that cannot happen |
| [`health-details-overlay-narrow.json`](scenarios/health-details-overlay-narrow.json) | `health_states` | the details overlay wraps rather than clips at 62 columns, and `G` reaches the last body line — the configuration reading — instead of stopping a row short of it |
| [`health-details-overlay-wide.json`](scenarios/health-details-overlay-wide.json) | `health_states` | the palette opens the full reading for every check at once: a running daemon, drifted hooks whose headline is the aggregate count with its remediation, and a configuration section that says it observed no deprecated keys rather than staying silent |
| [`health-states-hooks-degraded.json`](scenarios/health-states-hooks-degraded.json) | `health_states` | one refresh tick reduces the two health causes — a live daemon beside hook files that are in sync, drifted, and missing at once — to the single worst reading `✖ hooks drifted` in the header, with the per-check pips and the degraded banner retired to the details overlay |
| [`held-inspector-resumes-on-new-selection.json`](scenarios/held-inspector-resumes-on-new-selection.json) | `paused_queue` | the same round trip with the selection moved while the split was gone: the widening follows it, so the hold is dropped rather than carried onto another session — no `Paused`, no `output line 01`, and the tail of the new target on screen |
| [`held-inspector-survives-launcher-preview-toggle.json`](scenarios/held-inspector-survives-launcher-preview-toggle.json) | `paused_queue` | the same visit with the launcher's own preview turned off and on again before leaving: turning it off is not giving the pane back, so both toggles land on the launcher's borrowed copy and the dashboard still reads `Paused` on `output line 01` when the visit ends |
| [`held-inspector-survives-launcher-preview.json`](scenarios/held-inspector-survives-launcher-preview.json) | `paused_queue` | a paused inspector lends its pane to the launcher's preview and takes it back on `esc` still holding the snapshot it lent: `Paused` and `output line 01` rather than the tail that pointing the shared pane at a launcher row used to resume |
| [`held-inspector-survives-launcher-session-search.json`](scenarios/held-inspector-survives-launcher-session-search.json) | `paused_queue` | the same visit left through the launcher's `s` into Past Sessions instead of through `esc`: that is a third surface rather than a deeper room in the launcher, so the pane goes back on the way out and the dashboard behind it still reads `Paused` on `output line 01` |
| [`held-inspector-survives-resize.json`](scenarios/held-inspector-survives-resize.json) | `paused_queue` | a paused inspector crosses the split floor and comes back for the same selection still holding the snapshot it went away with: `Paused` and `output line 01` rather than the tail a geometry change used to resume |
| [`help-modal-narrow-fit.json`](scenarios/help-modal-narrow-fit.json) | `basic` | help remains bounded in a narrow viewport |
| [`help-modal-scroll-bottom.json`](scenarios/help-modal-scroll-bottom.json) | `basic` | help scroll reaches the final surviving bindings |
| [`launcher-session-search-leaves-no-stale-hold.json`](scenarios/launcher-session-search-leaves-no-stale-hold.json) | `paused_queue` | the Past Sessions exit carries the borrow no further than the visit: after coming back the operator presses `End` to resume, and a second launcher visit has nothing outstanding to put back, so the pane stays unpaused on `output line 60` rather than resurrecting the pre-resume snapshot |
| [`legend-narrow-fit.json`](scenarios/legend-narrow-fit.json) | `actions` | the live-session dashboard remains bounded at narrow width; the icon legend the name refers to has since moved into the generated help, so what is left here is the narrow-layout guard |
| [`mouse-chip-removal.json`](scenarios/mouse-chip-removal.json) | `project_families` | a click on a rendered project chip removes exactly the filter component named by the captured chip identity |
| [`mouse-header-controls.json`](scenarios/mouse-header-controls.json) | `project_families` | the three things the quiet header still offers a pointer — the view label, the health reading, and the scope — invoke the view toggle, the health overlay, and the project switcher — the same three the palette's **Show Projects View**, **Show Health**, and `p` reach — and the scope stays clickable after the health overlay has been opened and dismissed over it |
| [`mouse-list-wheel.json`](scenarios/mouse-list-wheel.json) | `project_families` | a wheel event over the session pane moves the selection by three selectable rows |
| [`mouse-switcher-ignores-wheel.json`](scenarios/mouse-switcher-ignores-wheel.json) | `project_families` | the open switcher publishes click targets only: a wheel over it and a click on the dashboard behind it both do nothing, leaving mode, cursor, and filter untouched |
| [`mouse-switcher-scope.json`](scenarios/mouse-switcher-scope.json) | `project_families` | clicking a rendered scope row inside the switcher applies that project and closes the overlay |
| [`mouse-session-select-focus.json`](scenarios/mouse-session-select-focus.json) | `project_families` | one click selects a live session and a second click on the same identity focuses its Kitty tab |
| [`needs-you-reveals-folded-target.json`](scenarios/needs-you-reveals-folded-target.json) | `project_families` | `N` reaches an attention row folded inside a collapsed project: the family unfolds, the cursor lands on the row itself, and no tab is focused and no pager opens — finding a waiting session is not answering it |
| [`paused-output-follows-tail.json`](scenarios/paused-output-follows-tail.json) | `paused_output` | the inspector opens over a scrollback longer than the pane: the six-line metadata block is drawn above the divider and the output below it follows the tail, so the last lines are on screen, the first are gone, and nothing says Paused because no key yet holds a position — the quiet chrome having handed the viewport two more rows of it |
| [`picker-dsl-narrow-fit.json`](scenarios/picker-dsl-narrow-fit.json) | `basic` | launcher DSL/help text clamps at narrow width |
| [`portable-dashboard-narrow-fit.json`](scenarios/portable-dashboard-narrow-fit.json) | `portable_tmux` | at 80 columns the selection moves onto a tmux-hosted row and its actions menu opens over it with the full session vocabulary, the focus entry saying it attaches here rather than jumping to a tab |
| [`portable-dashboard-wide.json`](scenarios/portable-dashboard-wide.json) | `portable_tmux` | with no Kitty client the queue still buckets tmux-hosted rows by the daemon state keyed to them, selects the one that needs the operator, and fills the inspector from that pane's own scrollback |
| [`portable-resort-always-sorted.json`](scenarios/portable-resort-always-sorted.json) | `portable_tmux` | `Resort Tabs` runs outside Kitty instead of being refused: with no tab to move it answers that sessions are always in sort order and returns to the dashboard |
| [`projects-cross-view-selection.json`](scenarios/projects-cross-view-selection.json) | `project_families` | the view toggle carries the selected session across to Projects, where the cursor lands on the same session at its new tree index |
| [`projects-cross-view-summary-return.json`](scenarios/projects-cross-view-summary-return.json) | `project_families` | toggling back out of Projects from a family summary selects that family's primary session in Urgency rather than losing the cursor |
| [`projects-family-collapse.json`](scenarios/projects-family-collapse.json) | `project_families` | `Enter` on a summary folds its family away while the sibling family stays expanded and each summary keeps its own tally, there being no status tally left to disagree with |
| [`projects-family-expand.json`](scenarios/projects-family-expand.json) | `project_families` | a second `Enter` unfolds the collapsed family and restores every checkout underneath it as an indented Session/Status/Time row, still visibly distinct from the summary above them |
| [`projects-filtered-attention-counts.json`](scenarios/projects-filtered-attention-counts.json) | `project_families` | the `!attention` chip keeps only the two attention rows and drops the now-empty family summaries, the chip itself being what says the list is filtered now that the `V/N visible` status line is gone |
| [`projects-summary-action-refusal.json`](scenarios/projects-summary-action-refusal.json) | `project_families` | a family summary refuses session actions by both routes: `a` does not open the actions menu on it, and the retired `X`/`r`/`e`/`o` spellings pressed afterwards land at the list and do nothing, so no Kitty focus, launch, close, or pager call is made and the model ends where it started |
| [`projects-summary-preview.json`](scenarios/projects-summary-preview.json) | `project_families` | the inspector's family card for a selected family summary lists the family's bucket tally and every checkout with its branch and directory |
| [`projects-tree-expanded.json`](scenarios/projects-tree-expanded.json) | `project_families` | the view toggle replaces the urgency headings with family summary lines and indented checkout rows, leaving unfamilied and single-checkout rows flat |
| [`project-scope-survives-resize.json`](scenarios/project-scope-survives-resize.json) | `project_families` | an applied scope survives a narrowing resize and the switcher reopens on it, so the chip, the header selector, and the highlighted row all still name the same project |
| [`project-switcher-keyboard-apply.json`](scenarios/project-switcher-keyboard-apply.json) | `project_families` | `p` then `↓` then Enter applies a scope and closes the overlay, leaving the chip, the header scope, and the group headings in agreement about the three rows that survive |
| [`project-switcher-narrow-fit.json`](scenarios/project-switcher-narrow-fit.json) | `actions` | the switcher box clamps to a 60-cell terminal without folding a scope row onto a second line |
| [`project-switcher-over-split.json`](scenarios/project-switcher-over-split.json) | `project_families` | the overlay composites over a wide list+inspector split without disturbing either column underneath it, and the dashboard header stays on screen above it because the overlay is dashboard chrome |
| [`resize-stress.json`](scenarios/resize-stress.json) | `basic` | the dashboard survives a sequence of resizes and ends clean |
| [`picker-fit.json`](scenarios/picker-fit.json) | `actions` | launch picker rows stay within width after pressing `l` |
| [`picker-preview-fit.json`](scenarios/picker-preview-fit.json) | `actions` | launch picker list+preview split fits after pressing `l` then `p` |
| [`plan-run-form-wide.json`](scenarios/plan-run-form-wide.json) | `basic` | `R` on a plan opens the run form over the Plans list; rows cycle with `h`/`l` and the `asl run` line follows the choices |
| [`plan-run-refused-narrow-fit.json`](scenarios/plan-run-refused-narrow-fit.json) | `basic` | a plan agent-session-loop holds as not ready keeps the run form open on its findings, wrapped to a narrow width |
| [`plan-search-header-narrow-fit.json`](scenarios/plan-search-header-narrow-fit.json) | `basic` | Plans header and filters fit at narrow width |
| [`plan-search-narrow-fit.json`](scenarios/plan-search-narrow-fit.json) | `basic` | Plans results fit at narrow width |
| [`plan-search-sort-rebind.json`](scenarios/plan-search-sort-rebind.json) | `basic` | `o` cycles the Plans sort order and the footer advertises `o sort`, not `s sort` |
| [`plan-view-fit.json`](scenarios/plan-view-fit.json) | `basic` | plan viewer read-error line wraps to width after Plans is opened by name and `enter` selects a row |
| [`plan-view-narrow-fit.json`](scenarios/plan-view-narrow-fit.json) | `basic` | plan viewer remains bounded at narrow width |
| [`round-trip-actions-to-inspector.json`](scenarios/round-trip-actions-to-inspector.json) | `paused_queue` | `a` then `esc` closes the actions menu back onto the inspector without having acted: the overlay is gone, the row is still selected, and no tab was closed |
| [`round-trip-confirmation-cancel-to-inspector.json`](scenarios/round-trip-confirmation-cancel-to-inspector.json) | `paused_queue` | a cancelled close confirmation returns the same way — the prompt is gone, the inspector is back on the same row, and the tab the prompt named is still open |
| [`round-trip-health-to-family-card.json`](scenarios/round-trip-health-to-family-card.json) | `family_unread` | the health details overlay over a selected family summary, dismissed, restores the family card whole |
| [`round-trip-help-to-family-card.json`](scenarios/round-trip-help-to-family-card.json) | `family_unread` | the same round trip through the generated keymap |
| [`round-trip-history-to-inspector.json`](scenarios/round-trip-history-to-inspector.json) | `paused_queue` | the same round trip through Past Sessions |
| [`round-trip-launcher-to-inspector.json`](scenarios/round-trip-launcher-to-inspector.json) | `paused_queue` | opening the launcher by capability name and dismissing it returns to the dashboard with the same row selected and the session inspector redrawn whole — name, state and timer, agent, and directory |
| [`round-trip-plans-to-family-card.json`](scenarios/round-trip-plans-to-family-card.json) | `family_unread` | the other inspector presentation: Plans opened over a selected family summary and dismissed restores the family card, its bucket tally, branch count, and member list included |
| [`round-trip-runs-to-family-card.json`](scenarios/round-trip-runs-to-family-card.json) | `family_unread` | the same round trip through Runs |
| [`round-trip-worktrees-to-inspector.json`](scenarios/round-trip-worktrees-to-inspector.json) | `paused_queue` | the same round trip through the managed-worktree inventory |
| [`run-timer-drop-order.json`](scenarios/run-timer-drop-order.json) | `run_timers` | narrowing to 27 cells drops Time before Status and Status before the name: every timer is gone while every row still says what it is doing |
| [`run-timer-narrow-fit.json`](scenarios/run-timer-narrow-fit.json) | `run_timers` | at 57 cells all three columns still fit, so every row keeps its aligned timer and only the `⏳` warning is missing — it belongs to the inspector, which 57 cells does not pay for |
| [`run-timer-wide.json`](scenarios/run-timer-wide.json) | `run_timers` | run timers survive refresh ticks, the Time column stays right-aligned across four magnitude bands, and the `⏳` watchdog warning rides the inspector's `Watch` line rather than the row |
| [`search-narrow-fit.json`](scenarios/search-narrow-fit.json) | `basic` | live-list filter rows truncate at a sub-floor terminal after `/` |
| [`session-fork-continuity.json`](scenarios/session-fork-continuity.json) | `basic` | Past Sessions dispatches a native fork through the shared action registry |
| [`session-search-chips-fit.json`](scenarios/session-search-chips-fit.json) | `basic` | session-search filter chips clamp to width after Past Sessions is opened by name |
| [`project-session-shortcut.json`](scenarios/project-session-shortcut.json) | `basic` | dashboard `s` opens Past Sessions with the selected project filter |
| [`session-search-header-narrow-fit.json`](scenarios/session-search-header-narrow-fit.json) | `basic` | Past Sessions header and filters fit at narrow width |
| [`session-search-ending-wide.json`](scenarios/session-search-ending-wide.json) | `basic` | an unfiltered Past Sessions row follows its summary with `→` and how the conversation ended |
| [`session-search-ending-narrow.json`](scenarios/session-search-ending-narrow.json) | `basic` | at 60 cells the summary yields room so the ending still shows, and the row fits |
| [`session-view-hints-wide.json`](scenarios/session-view-hints-wide.json) | `basic` | transcript viewer advertises the full continuity palette including `c continue` |
| [`session-view-hints-narrow-fit.json`](scenarios/session-view-hints-narrow-fit.json) | `basic` | transcript viewer hints truncate to the row budget and surface the actionable `? more` marker |
| [`size-60x15-triage.json`](scenarios/size-60x15-triage.json) | `triage_integration` | the smallest reviewed terminal |
| [`size-80x24-triage.json`](scenarios/size-80x24-triage.json) | `triage_integration` | the classic default |
| [`size-100x24-triage.json`](scenarios/size-100x24-triage.json) | `triage_integration` | exactly at the split floor |
| [`size-120x30-triage.json`](scenarios/size-120x30-triage.json) | `triage_integration` | a typical wide window |
| [`size-160x45-triage.json`](scenarios/size-160x45-triage.json) | `triage_integration` | the widest reviewed terminal. These five assert the same two strings — the leading `Needs you (4)` heading and the aligned `release-guard` row — on purpose: what a terminal size buys is chrome and pane count, never row content, and five scenarios agreeing on the same rows is how that claim is committed |
| [`size-extreme-short-window.json`](scenarios/size-extreme-short-window.json) | `paused_queue` | a four-row viewport keeps the header, one row, and the footer's `1–1 of 5` position token, and drops the inspector rather than drawing a divider it has no rows to fill |
| [`size-split-floor-99-no-split.json`](scenarios/size-split-floor-99-no-split.json) | `paused_queue` | resizing down across the split's width floor removes the inspector wholesale — no divider anywhere on screen — leaving the queue alone with its rows unchanged |
| [`size-split-floor-100-split.json`](scenarios/size-split-floor-100-split.json) | `paused_queue` | the same boundary from the other side: 99 → 100 is exactly where the divider and the inspector's metadata appear, so the floor is asserted from both directions rather than assumed |
| [`spacious-identity-columns.json`](scenarios/spacious-identity-columns.json) | `wide_profiles` | a profile name too long for any row is carried whole by the inspector's `Agent` line, so the truncated `claude-produc…` the retired identity column produced is nowhere on screen |
| [`triage-contextual-project-completion.json`](scenarios/triage-contextual-project-completion.json) | `triage_integration` | `%claude #` completes only labels remaining in that harness context and quotes labels containing spaces |
| [`triage-default-wide-ordering.json`](scenarios/triage-default-wide-ordering.json) | `triage_integration` | the default wide dashboard renders the integrated severity/recency order as aligned Needs you and Running rows, and the switcher over it carries the exact All/project bucket tallies under their need/act/idle headings |
| [`triage-horizontal-preview.json`](scenarios/triage-horizontal-preview.json) | `triage_integration` | a wide side-by-side preview coexists with the header project selector and the 80-cell main-list floor |
| [`triage-quoted-scope-round-trip.json`](scenarios/triage-quoted-scope-round-trip.json) | `triage_integration` | a quoted spaced-label selector aggregates both case variants and survives opening and cancelling the switcher, which restores the list without touching the filter |
| [`triage-selector-survives-width-change.json`](scenarios/triage-selector-survives-width-change.json) | `triage_integration` | the header project selector is offered on both sides of the old 105/106-cell rail threshold, which no longer suppresses anything |
| [`triage-switcher-at-narrow-width.json`](scenarios/triage-switcher-at-narrow-width.json) | `triage_integration` | the switcher opens over a 105-cell dashboard with its legend and tallies intact |
| [`triage-switcher-contextual-counts.json`](scenarios/triage-switcher-contextual-counts.json) | `triage_integration` | with a text chip and a project chip both applied, the switcher drops only the project selector: All reports the text-filtered totals rather than the whole instance, and scopes emptied by the text chip are not offered |
| [`triage-switcher-scroll.json`](scenarios/triage-switcher-scroll.json) | `triage_integration` | a short terminal scrolls the switcher's scope list with both markers shown while the applied scope named in the status bar stays fixed |
| [`triage-vertical-direction-ignored.json`](scenarios/triage-vertical-direction-ignored.json) | `triage_vertical` | `direction = "vertical"` selects nothing on the dashboard: the inspector is the same side-by-side split, since the setting now governs only the launcher's preview |
| [`triage-view-round-trip.json`](scenarios/triage-view-round-trip.json) | `triage_integration` | a switcher-applied project scope survives Urgency → Projects → Urgency, and reopening the switcher still lands on the canonical aggregate |
| [`unseen-stop-grouping-narrow-fit.json`](scenarios/unseen-stop-grouping-narrow-fit.json) | `unseen_stops` | two rows whose Status column both read `Done` split across Needs you and Idle by the daemon's unseen bit, with the unread one leading the list at narrow width and wearing the only `•` the gutter paints |
| [`unseen-stop-grouping-wide.json`](scenarios/unseen-stop-grouping-wide.json) | `unseen_stops` | the seen 🛑 demotes to Idle while the unseen one holds Needs you, walking the cursor down still reaches the demoted row across the group headings, and landing on it neither marks it nor clears the marker on the row that has one |
| [`worktrees-browse.json`](scenarios/worktrees-browse.json) | `worktrees` | opening the hermetic managed-worktree inventory by name and whitespace-AND filtering narrows to the dirty row without exposing lifecycle mutations |
| [`worktrees-cleanup-active-refusal.json`](scenarios/worktrees-cleanup-active-refusal.json) | `worktree_lifecycle` | checkout removal is refused from a snapshot with a live session |
| [`worktrees-cleanup-dirty-refusal.json`](scenarios/worktrees-cleanup-dirty-refusal.json) | `worktree_lifecycle` | checkout removal is refused from a dirty snapshot |
| [`worktrees-cleanup-looping-refusal.json`](scenarios/worktrees-cleanup-looping-refusal.json) | `worktree_lifecycle` | checkout removal is refused from a snapshot with an active loop |
| [`worktrees-delete-merged-branch.json`](scenarios/worktrees-delete-merged-branch.json) | `worktree_lifecycle` | `D` confirms and completes safe deletion of a branch recorded as merged |
| [`worktrees-delete-unmerged-refusal.json`](scenarios/worktrees-delete-unmerged-refusal.json) | `worktree_lifecycle` | `D` refuses an unmerged branch before confirmation |
| [`worktrees-diff-missing-refusal.json`](scenarios/worktrees-diff-missing-refusal.json) | `worktree_lifecycle` | `v` refuses to review a worktree whose checkout is missing |
| [`worktrees-forget-invalid.json`](scenarios/worktrees-forget-invalid.json) | `worktree_lifecycle` | `F` explicitly forgets invalid sidecar metadata |
| [`worktrees-forget-missing.json`](scenarios/worktrees-forget-missing.json) | `worktree_lifecycle` | `F` explicitly forgets retained metadata for a missing checkout |
| [`worktrees-land-active-refusal.json`](scenarios/worktrees-land-active-refusal.json) | `worktree_lifecycle` | `L` refuses to land a worktree with a live session before confirmation |
| [`worktrees-land-confirm-narrow-fit.json`](scenarios/worktrees-land-confirm-narrow-fit.json) | `worktree_lifecycle` | the landing confirmation stays usable at narrow width |
| [`worktrees-land-diverged-refusal.json`](scenarios/worktrees-land-diverged-refusal.json) | `worktree_lifecycle` | `L` refuses a worktree whose source branch has moved on before confirmation |
| [`worktrees-land-fast-forward.json`](scenarios/worktrees-land-fast-forward.json) | `worktree_lifecycle` | `L` confirms and completes a fast-forward landing, keeping the worktree row |
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
`modal_width_overflow`. Which modals that constraint binds on is not uniform,
and the difference is the point rather than a quirk of the detector:

- The **actions menu** is responsive. It clamps to the viewport instead of
  claiming a natural width, so it fits every target size down to 80×24 and
  below, and the scenarios above assert that positively rather than asserting
  it overflows. When it runs out of *rows* it sheds its own decoration before
  it sheds entries, which is why a short terminal still shows a list; only a
  viewport too short for even one entry plus a header raises
  `modal_height_overflow`.
- The **`wide_profiles` config picker** still claims a natural width and needs
  ≥ ~130 cols.

Author a *negative* scenario by opening a modal that claims a natural width at
a narrow size and asserting the overflow is caught — that is the intended
"fails deterministically when an overlay needs more columns than the terminal
can provide" behavior, and `config-picker-fit.json` plus the in-package
`TestUITestModalConfigPickerWidthOverflow` are where it is still guarded.
