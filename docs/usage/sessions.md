# Sessions and actions

[Back to usage](../usage.md)

## Session lifecycle

**End Session** stops the agent; its saved conversation remains available.
**Detach Terminal** leaves the agent running. **Close View** ends a utility
program such as a shell or editor. These names have the same meaning in the
native app, Dashboard, and Overview. `amx close` keeps its command name and
requires `--yes`; it performs End Session.

The TUI's `confirm_destructive` setting controls End Session, restart, and
restart under another profile, including in the app's embedded Dashboard and
Overview. Native menus and sidebar actions use the separate **Confirm before
closing views or ending sessions** preference. Neither setting turns detaching
into ending a session. Recovery after End Session offers Resume or Relaunch
for 30 seconds; it does not restore the terminated process or unsaved work.

## Action feedback

Retained notices reserve space for `: feedback ↵`: open the command palette,
type `feedback`, and press Enter to read the full notice. Details separate what
happened, any unconfirmed outcome, and the next step.

Failed and unconfirmed dashboard actions remain available in `:` → **Show
Action Feedback**, even after later actions succeed. Each notice carries its
first occurrence time and, when known, the session it concerns. `Tab` and
`Shift+Tab` select notices; `j`/`k`, page keys, and `g`/`G` scroll long messages.
Press `y` to copy the selected message or `Y` to copy all notices; `d` dismisses
the selected notice and `D` dismisses all completed notices. Active operations
and available session recovery remain visible.

`Enter` inspects the notice's session without attaching or repeating the action;
notices without a session open health details. `h` opens health, `c` opens the
configuration in the configured editor, and `Esc` returns to the dashboard.
`u` recovers the most recently ended session while its countdown is visible.

A lifecycle operation still awaiting its result after ten seconds says **Still
waiting**. This is not a timeout verdict or permission to repeat it: inspect
the live session and connection health first. A late result replaces that
operation's notice. Dismissing a notice does not cancel the operation.
Successful feedback clears after four seconds, except a send whose form was
closed: its outcome remains until dismissed. End Session recovery keeps
its separate 30-second deadline. Notices live for this dashboard process.
Recovery resumes the exact conversation when its identity is known, validating
its transcript first. Otherwise it explicitly offers a project relaunch. A failed
resume never silently starts a fresh conversation.

## Terminal appearance

The dashboard, overview, and pickers adapt their colors to the terminal's
reported background. If the terminal cannot report it, the dark palette is
used. Set `AMX_TUI_THEME=light`, `dark`, or `auto` to override or restore
automatic selection, for example `AMX_TUI_THEME=light amx`. Captured agent
output keeps its original colors.

## Live session overview

```bash
amx overview
```

`F2` opens the same surface from an attached tmux terminal or the macOS workspace.
It is a grid of managed tmux sessions. Each tile has a
three-line header — which session, what it is doing and for how long, and what
is running it — and a live preview of that pane's screen below a divider. It
answers "what is every session doing right now" in one glance, which the list
answers one row at a time.

The tiles put sessions needing attention first, followed by Done sessions,
then other statuses that are not Working, then Working sessions. On opening,
each tier uses project and tab identity order. While the overview remains
open, refreshes update statuses and previews and move a session to the tier
its status now belongs in. Compact mode keeps each tier alphabetically ordered
by project, including newly added sessions. Normal mode retains relative order
within a tier, with new sessions joining the end of their tier. Selection stays
on the same session in both modes. Switching to compact mode or reopening F2
restores project and tab identity order within each tier.

The header is the full inspector's six-line metadata block cut to the three
questions asked of a session you are *not* currently looking at. Branch,
directory, and the supervision/lock/mute line are the ones asked of a session
already picked, and the inspector is where picking one leads. The status
vocabulary is shared with the dashboard and inspector. In the overview, the
agent's turn and its supervising ASL run are shown independently: for example,
`Done · 0:42 | ASL paused` or `Done · 0:42 | ASL running`. Done means the agent
finished its turn, not that the whole plan finished. The timer beside Done is
the time since that completion. A new agent turn changes the label back to
Working; an ASL heartbeat alone does not. Run phases can also say verifying,
waiting, or between turns. Multiple live run phases are separated by `/`.

Idle is bold warning yellow; Done is bold green. Permission
and reply requests retain their amber emphasis, failures remain red, and the
elapsed time and ASL phase are dim. An unread completion keeps its amber notice
dot beside the green Done label.

This distinction requires the updated daemon as well as the overview binary.
With an older or unavailable daemon, the overview retains the aggregate status
and shows `ASL` when a loop marker is present, without guessing a phase or an
agent completion. The dashboard's grouping, tab icons, and notification rules
continue to use their existing aggregate state.

The overview is a **separate program**, not a dashboard mode. It has its own
keymap and its own refresh, the dashboard keeps running behind it, and closing
one does not disturb the other. The one thing the two surfaces do share is what
a session action *is*: `a` here opens the same menu, offering the same entries
under the same rules and the same confirmations, as `a` on a dashboard row.

- `a`: open the actions menu for the selected session — every entry the
  dashboard offers on a row, in the same order and with the same mnemonics.
  `j`/`k` or `↑`/`↓` move the selection, `Enter` runs it, and a mnemonic runs its
  entry from wherever the cursor is. An entry the selected session cannot run is
  dimmed with its reason, and pressing its mnemonic anyway says the reason on the
  feedback row rather than doing nothing. Choosing a restart, a profile
  replacement, or a close asks for a y/n confirmation when
  `confirm_destructive = true`; the prompt names the session and what saying yes
  destroys. `Esc` or `q` backs out; `Ctrl+C` leaves the overview entirely
- `u`: recover the session you just closed from here: resume its exact conversation
  when known, otherwise relaunch its project. It is offered for 30
  seconds after a confirmed close and is named on the feedback row for exactly
  that long, which is why it is the one real binding that is deliberately not on
  the footer
- `?`: show the whole keymap, generated from the same table the footer and the
  dispatcher read, so it cannot advertise a key that is not bound. It scrolls
  with `j`/`k`, `Ctrl+f`/`Ctrl+b`, `Ctrl+d`/`Ctrl+u`, and `g`/`G`, and `?`, `Esc`,
  or `q` closes it. Opening and closing it changes nothing underneath: the same
  session stays selected, on the same page, with the same reads in flight
- `c`: toggle compact/normal mode. Compact cards show project plus status and
  elapsed time on line one, harness/profile on line two, and no terminal preview.
  The selected session stays selected. The daemon remembers the choice across
  closing and reopening F2; restarting the daemon resets it to the configured
  opening mode, `[defaults.overview] compact` (compact unless set to `false`,
  which opens the full preview grid). The toggle never edits the config file. If saving is unavailable, the local toggle still works and
  the feedback row reports the problem.
- `←↑↓→` or `h`/`j`/`k`/`l`: move within the page. These are spatial: the end of
  a row is a wall rather than a wrap onto a tile the eye did not expect
- `Tab`/`Shift+Tab`: the next/previous session in the whole list, crossing rows
  and pages
- `PgUp`/`PgDn`: one page; `Home`/`End`: the first and last session
- `n`/`N`: next/previous session that needs you, wrapping across all pages in
  either compact or normal mode. Uses the dashboard's rules: permission/reply
  requests, failures, and unread completions qualify; working, idle, recovered
  tool warnings, and seen completions do not. The jump selects the card without
  focusing its pane or marking its completion read. No matches leaves selection
  unchanged.
- `Enter`: open the selected session. A tmux session attaches here (F12 returns),
  or switches the hosting terminal when the overview is a popup. The target is captured when you press
  the key, so a refresh cannot hand you a different pane. A failure leaves
  you on the overview with
  the reason on the feedback row and the same tile selected, which is the only
  state retrying makes sense from
- `1`–`9`, `0`: select and immediately focus the corresponding card among the
  first ten overall (`0` is the tenth), regardless of the current page. Digits
  appear at the right end of the existing second information row, in reading
  order: left to right across each row, then top to bottom. Later cards have no
  digit; a digit without a matching card does nothing. Like `Enter`, a failed
  focus leaves the target selected for retry, and pending focus ignores repeats.
- Mouse: a left click on a tile selects and focuses that session, exactly as
  its digit would, for any tile on the page; a click anywhere else does
  nothing. The wheel turns the page when there is another one. Hold `Shift` to
  select text instead, and the mouse is not reported while a menu, a
  confirmation, or the help is open.
- `Esc`, `q`, `Ctrl+C`: close, whether or not a read is still out

Browsing itself still only observes sessions and their screen text: moving the
selection, paging, and `n`/`N` touch nothing, `c` changes only the daemon's
in-memory view preference, and `Enter`, the digits, and a click focus a tab. Everything
that changes a session is behind `a`, where it is named and — when
`confirm_destructive = true` — confirmed. Closing immediately after a compact
toggle lets its bounded save finish before exiting.

Closing or restarting the tab the overview is itself running in is still
offered — an operator acting on the session they raised the overview beside is
an ordinary thing to do, and refusing would be the surface protecting itself
rather than the operator. What changes is that its confirmation says so in an
extra sentence, and that a close there publishes no undo: `u` would relaunch it
from a grid that is going away with the tab, so the offer would die before it
could be used. A profile with no entry in the loaded configuration is the other
close that offers no undo, for the plainer reason that there is no launch path
to relaunch it with.

The footer uses the dashboard's colors: available keys are bold teal, unavailable
keys are muted gray, and descriptions stay dim. Availability follows the current
selection and layout, including attention targets, navigation boundaries, and a
pending focus request. Unavailable actions stay visible in the same positions.

The header counts the sessions and, when more than one page of them exists, says
which page you are on (`page 2/3`). The grid sizes itself to the terminal — a
tile targets 48 columns and 12 rows, so a 120x30 window shows four sessions at a
time and a 160x45 one shows nine. Compact mode targets 48 columns and four rows
(two content rows plus borders), showing 12 and 30 sessions respectively at
those sizes. Hidden terminal previews are not captured. That target is not a floor: a terminal too
small for even one full tile still draws one cramped tile rather than an empty
grid, and a tile too small to wear a box drops the box before it drops the
session's name. Resizing keeps the selected session selected
and re-aims the previews at whatever is now on screen; the page follows the
selection rather than the other way round.

Previews refresh on their own cadence and are **bounded**: at most four screen
reads are in flight at once, one per window, queued fairly and coalesced, so a
screenful of tiles costs a handful of reads rather than one per tile per tick. A
read that fails is confined to its own tile — the other tiles keep showing their
output — and a session that disappears takes its tile and its retained text with
it on the next refresh. A tile whose read has not landed yet says `(loading…)`;
a pane that has genuinely printed nothing says `(no output)`, which is a
different answer.

The overview runs locally in a terminal; a tmux popup uses the attached terminal.

Rows come from the private tmux server and runtime registry. A delayed refresh keeps the last good
list and says `Session refresh delayed; showing cached sessions (Ns old)`;
a partial tmux outage is identified separately. Cached rows do not prove that
their agents are still running.
## Answering or prompting an agent without attaching

`amx send` writes to a running agent's terminal with nothing attached, and
takes the session from nobody who has it. It works on tmux sessions (see
[The tmux backend](launching.md#the-tmux-backend)); the dashboard's
`t` form and Agent Mux.app's prompt answer panel run it underneath.

```bash
amx send ID "run the tests and fix what fails"   # pasted as one prompt, then entered
git diff | amx send ID --stdin --no-enter         # typed into its prompt box, not entered
amx send ID --key 1                               # answer the permission prompt it is showing
amx send ID --key down --key enter --wait 10s     # and watch for the agent taking it
```

`--key` takes `enter`, `escape`, `tab`, `shift-tab`, `up`, `down`, `left`,
`right`, `space`, `y`, `n`, and the digits. Text may span lines and may not
carry control characters.

What is sent has to fit what the agent's screen shows, which is read just
before writing with the same classifiers the daemon uses:

| Sent | Screen | Result |
| --- | --- | --- |
| text | at rest, or working | written; what a prompt typed mid-turn does is the harness's to decide |
| text | asking something | refused, `prompt_showing`: a sentence typed into a menu answers it |
| keys | asking something | written |
| keys | anything else | refused, `no_prompt_showing` |
| text | no prompt showing and nothing else to go on | written, reported as `screen: "unknown"`: a session before its first turn, and Copilot at rest, read this way |
| either | unreadable, or a harness with no screen adapter | refused, `screen_unreadable` |

`--any-screen` skips the reading, for when you have looked yourself.

**agent-session-loop.** `amx send` types at a terminal; it is not a reply to
a run. A supervisor question is answered through the run's mailbox, from the
Runs view or Agent Mux.app's answer panel, so that the run records the
reply. A session the daemon reports as having such a question
open is therefore refused whatever is passed (`asl_question_open`). A session
that runs a run with no question open needs `--yes`, because what is typed
reaches the run's supervisor, and so does any session while the daemon cannot
be asked (`daemon_unavailable`); with the daemon unreachable, `--yes` sends
without knowing whether a question is open.

**Once.** `--operation-id` names a send; without one a name is made up and
reported. The operation is recorded before its input is written and again
after, so repeating a finished operation writes nothing and reports
`delivery: "replayed"`, and repeating one that was never seen to finish is
refused (`operation_outcome_unknown`) instead of risking a second copy.
Operation IDs are remembered for the lifetime of their runtime session,
including when other sends have happened since.

**What it claims.** `delivery` is `accepted` once the input is written, which
says nothing about the agent. With `--wait`, a session that was at rest or
asking is watched, and `delivery` becomes `consumed` when the daemon's state
for it moves. An agent that was already working moves by itself, so it is not
watched and stays `accepted`. `--json` reports `{runtime_session_id, backend,
project, operation_id, input, screen, delivery}` and never the text, which is
passed to tmux on standard input and appears in no argument list, log, or
journal.

**Looking first.** `amx send ID --look` sends nothing and prints the last
rows of the agent's screen; with `--json` it reports `{runtime_session_id,
backend, project, screen, lines}`, where `screen` is the word a send would
report. It is what Agent Mux.app shows when a permission banner's Answer… is
chosen, so the prompt is read before a key is pressed.
