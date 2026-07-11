# TUI test harness

`amx tui-test` is a hidden developer/automation command that drives the Bubble
Tea dashboard model **in-process** to catch layout regressions — the kind of
overlay sizing bugs fixed in commits like "size profile picker box to fit its
widest row" and "size actions overlay box to fit its widest row".

It never launches a live Kitty instance, never talks to a real terminal, and
never reads or writes the operator's real config or data. Every run is
deterministic and isolated, so it is safe to run in CI or from an agent.

The repository acceptance gate runs every scenario, including both split-pane
wide layouts and narrow/short drill-down layouts:

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
# basic
# wide_profiles
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
| `wide_profiles` | long profile names and home dirs | stresses the profile/config picker box width |

Workspace scenarios also use hermetic, scenario-only fixtures: `workspace_empty`,
`workspace_attention`, `workspace_profiles`, and `workspace_worktrees`. They
inject an immutable workspace snapshot directly, so their coverage never reads
an operator's datastore, transcripts, plans, or Git checkout.

The `basic` fixture's list is state/profile-grouped, not globally
alpha-sorted: the cursor starts on `dotfiles` (personal).

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
`workspace`, `palette`, `unified_search`.

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

## Example scenarios

The [`scenarios/`](scenarios) directory holds committed examples covering the
layout classes this harness targets:

| File | Fixture | Covers |
| --- | --- | --- |
| [`baseline-layout.json`](scenarios/baseline-layout.json) | `basic` | dashboard list renders clean with all projects visible |
| [`actions-popup-fit.json`](scenarios/actions-popup-fit.json) | `actions` | actions overlay fits after pressing `a` on a long-titled session |
| [`config-picker-fit.json`](scenarios/config-picker-fit.json) | `wide_profiles` | profile/config picker fits after pressing `C` with long profile names |
| [`resize-stress.json`](scenarios/resize-stress.json) | `basic` | the dashboard survives a sequence of resizes and ends clean |
| [`picker-fit.json`](scenarios/picker-fit.json) | `actions` | launch picker rows stay within width after pressing `l` |
| [`picker-preview-fit.json`](scenarios/picker-preview-fit.json) | `actions` | launch picker list+preview split fits after pressing `l` then `p` |
| [`plan-view-fit.json`](scenarios/plan-view-fit.json) | `basic` | plan viewer read-error line wraps to width after `P` then `enter` |
| [`session-search-chips-fit.json`](scenarios/session-search-chips-fit.json) | `basic` | session-search filter chips clamp to width after pressing `S` |
| [`search-narrow-fit.json`](scenarios/search-narrow-fit.json) | `basic` | filter-mode list rows truncate to width at a sub-floor terminal after `/` |
| [`list-preview-narrow-fit.json`](scenarios/list-preview-narrow-fit.json) | `actions` | list+preview split falls back to the plain list below the row-column floor after `p` |
| [`workspace-empty.json`](scenarios/workspace-empty.json) | `workspace_empty` | default workspace home handles an empty project set |
| [`workspace-wide-attention.json`](scenarios/workspace-wide-attention.json) | `workspace_attention` | split project/detail panes preserve attention, live, conversation, plan, and activity sections |
| [`workspace-narrow-drill.json`](scenarios/workspace-narrow-drill.json) | `workspace_attention` | narrow terminals open the same detail ordering with Enter |
| [`workspace-short-drill.json`](scenarios/workspace-short-drill.json) | `workspace_attention` | short terminals retain a usable full-width drill-down |
| [`workspace-multi-profile.json`](scenarios/workspace-multi-profile.json) | `workspace_profiles` | project home stays legible with multiple launch profiles |
| [`workspace-worktrees.json`](scenarios/workspace-worktrees.json) | `workspace_worktrees` | live rows retain their linked-worktree context |
| [`workspace-activity.json`](scenarios/workspace-activity.json) | `workspace_attention` | durable activity rows navigate to their related live session |
| [`workspace-unread.json`](scenarios/workspace-unread.json) | `workspace_attention` | unread counts and concise meaningful transitions remain visible |
| [`workspace-activity-order.json`](scenarios/workspace-activity-order.json) | `workspace_attention` | live urgency remains ahead of durable activity ordering cues |
| [`palette-narrow.json`](scenarios/palette-narrow.json) | `workspace_attention` | global command palette stays usable on a narrow terminal |
| [`palette-disabled-action.json`](scenarios/palette-disabled-action.json) | `workspace_attention` | unavailable actions remain visible with a context reason |
| [`palette-filtering.json`](scenarios/palette-filtering.json) | `workspace_attention` | palette text filtering narrows the action set deterministically |
| [`palette-parameter-picker.json`](scenarios/palette-parameter-picker.json) | `workspace_profiles` | launch actions reuse the profile picker and selected workspace |
| [`palette-return-focus.json`](scenarios/palette-return-focus.json) | `workspace_attention` | closing the palette restores the originating workspace detail focus |

Run them all:

```bash
amx tui-test run docs/tui-testing/scenarios/*.json
```

These examples are exercised in-package by
`tui/uitest_example_scenarios_test.go`, so `just test` fails if a committed
example stops parsing or regresses a layout guarantee — the documentation and
the harness cannot drift apart.

### Sizing note

Modal-open `no_layout_issues` scenarios must size the terminal wide enough for
the overlay's natural width, or the detector will (correctly) flag
`modal_width_overflow`. Empirically the actions overlay needs a terminal
≥ ~100 cols; the `wide_profiles` config picker needs ≥ ~130 cols. Author a
*negative* scenario by opening a modal at a narrow size and asserting the
overflow is caught — that is the intended "fails deterministically when an
overlay needs more columns than the terminal can provide" behavior.
