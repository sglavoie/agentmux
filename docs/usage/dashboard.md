# Dashboard

[Back to usage](../usage.md)

```bash
amx
```

With no sessions, the dashboard uses existing configuration and health checks
to suggest adding a profile, adding a project, checking connection health, or
pressing `l` to launch.

This opens the dashboard, or focuses the existing dashboard tab if one is
already running. To go straight to Past Sessions with no dashboard behind it,
run `amx past-sessions` or press `Ctrl+Shift+Y` in a terminal; leaving Past
Sessions then closes it. `⌘Y` in Agent Mux.app opens native Conversation History. When opened with the bundled `Ctrl+Shift+D` binding, the
dashboard selects the attached tmux session it was opened from. An unmanaged
source leaves the current/default selection alone.

The dashboard answers a small keymap. Everything else it can do is reached
through two surfaces it opens: `a` for the row under the cursor, `:` for the
rest. Both are browsable lists you move a cursor through rather than sheets of
letters to aim at, and both read the same declaration of what a session action
is, so they cannot disagree about which actions exist or when one is allowed.
If you are upgrading from a release with the larger direct keymap,
[Upgrading from the old dashboard shortcuts](../upgrading.md#upgrading-from-the-old-dashboard-shortcuts)
says where each key went.

- `j`/`k` or `↑`/`↓`: move the cursor, or scroll the inspector when it has focus
- `PgUp`/`PgDn`, `Home`/`End`: page, or jump to the ends of the focused pane
- `g`/`G`: aliases for `Home`/`End`; `Ctrl+D`/`Ctrl+U`: half-page down/up in the focused pane
- `Enter`: open the selected tmux session; on a family summary, fold or
  unfold the family instead
- `n`/`N`: move to the next/previous **Needs you** session. A target inside a
  folded family unfolds it and lands the cursor on the row; nothing is focused,
  opened, or marked as read — finding a waiting session is not answering it
- `Tab`/`Shift+Tab`: switch the focused pane. Its heading is highlighted and marked `focused`; the inactive pane's selection loses its reverse highlight.
- `a`: open the actions menu for the selected row — focus, peek at the
  scrollback, restart, restart under another profile, launch another session in
  the project, clear the status icon, mark it active, end the session, open a shell or the
  configured editor in its directory, copy the title, lock, mute. It is a menu
  you move through, not a sheet of letters: `j`/`k` or `↑`/`↓` move the selection,
  `PgUp`/`PgDn`, `Ctrl+f`/`Ctrl+b`, `Ctrl+d`/`Ctrl+u`, and `g`/`G` move it
  further, and `Enter` runs the selected entry. Each entry still answers its own
  mnemonic immediately, from wherever the cursor happens to be. A fresh menu
  opens on **Open session**, so `a` then `Enter` does what `Enter` on the row did.
  An entry that cannot apply to the row is dimmed but still selectable, and the
  detail block under the list is where its reason is written. `Esc` or `q`
  closes the menu; `Ctrl+C` quits the dashboard
- `t`: send to the selected tmux session without attaching. The form shows
  the tail of the agent's screen and has two halves, with `Tab` moving between
  them. In **Text**, `Enter` is a newline and `Ctrl+s` sends the draft as one
  prompt, then closes the form. In **Keys**, each press of `0`-`9`, `y`, `n`,
  an arrow, `Space`, or `Enter` is sent at once and the form stays open on the
  screen that results; a row that is asking something opens there. `Esc`
  closes. It is `amx send` underneath ([Answering or prompting an agent
  without attaching](sessions.md#answering-or-prompting-an-agent-without-attaching)), so
  the same refusals apply: text is not typed onto a prompt, a key is not
  pressed where nothing is asked, and a session running an agent-session-loop
  run asks you to send a second time to confirm. The key is disabled on a row
  whose run has a question open, which is answered from Runs.
  The Text half supports paste, arrow keys, Home/End, Ctrl+Home/End,
  Ctrl+w or Alt+Backspace to delete a word, and Page Up/Down to navigate long
  drafts. Paste never presses keys in the Keys half. Escape keeps an unsent
  draft in memory for that session; Ctrl+x explicitly discards it. Drafts are
  never saved to disk and disappear when the dashboard exits. A pending send
  continues after Escape, and its outcome appears in Action Feedback; another
  send to that session waits for the outcome.
- `u`: resume the exact conversation when known, otherwise relaunch the project, while the 30-second recovery
  window named on the toast is still open
- `:`: open the command palette; it is one line per command rather than a card,
  so more of them are on screen at once. Type to search, use `↑`/`↓`,
  `Ctrl+j`/`Ctrl+k`, or `Ctrl+n`/`Ctrl+p` to move, `Backspace` to edit, `Enter`
  to run, `Esc` to close, and `Ctrl+c` to quit
- `/`: edit the filter. The editor opens on the filter already on screen, so
  `Backspace` removes the last component; `Esc` restores what was committed
- The selected session’s footer prioritizes `t answer` or `t send prompt` when
  available; supervisor questions point to `: runs ↵`. The inspector combines
  status and agent identity when they fit, omits absent metadata, and gives the
  remaining rows to output. For full, wrapped field values, open **Show Session
  Details** from `:` and press `m`; page keys scroll the metadata.
- `i`: toggle the session inspector, full-screen when the terminal is too narrow
  to split
- `p`: open the project switcher
- `l`: open the launcher
- `s`: Past Sessions for the selected row's project
- `S`: Past Sessions across every project, newest activity first; it clears the
  history filters a previous `s` left behind
- `?`: show the full keymap
- `Esc`: close an overlay, then leave inspector focus, then clear the filters
- `q`, `Ctrl+C`: quit

Until you type a search, each Past Sessions row follows the conversation's
title with `→` and the opening of the agent's last reply, so conversations
started from the same prompt can be told apart without opening them. Once you
search, the row shows the line that matched instead.

Notable commands behind `:` include **Search This Project's Past Sessions** and
**Search All Past Sessions**, **Search Plans** (saved agent plans — rows and the
viewer show the configured project title, `r` filters by project including
`Unknown`, `Y` copies the full plan path, and the viewer wraps long lines,
supports less-style `Ctrl+d/u/f/b` paging, and `y` copies the selected raw
line), **Manage Worktrees**, **Show Runs** (the `agent-session-loop`
intervention cockpit), **Show Projects View** / **Show Urgency View**, **Cycle
Profile Filter**, **Show Session Details** and **Show Session Timeline** (the
per-session hook event log), **Resort Tabs**, and **Recover Ended Session** (it resumes the exact conversation when known,
otherwise relaunches the project).

The dashboard profile cycle is an `@profile` filter: it follows
case-insensitive alphabetical profile order, preserves any active word,
harness, and state filters, and includes profiles represented by current
running sessions. The active profile appears as the existing filter chip;
wrapping to **All** removes only that chip.

The command palette searches command titles, descriptions, aliases, and shown
shortcuts with case-insensitive, whitespace-separated AND terms. Disabled
commands remain visible with an inline reason and can be selected, but `Enter`
does nothing on them. Its shortcut labels are references: printable keys edit
the query while the palette is open. A command shows a shortcut only when the
dashboard actually binds one, so the palette never advertises a key that no
longer dispatches. `:` does not open the palette from other modes.

Rows marked with `🔁` have at least one active `agent-session-loop` run whose
workspace matches the project row. Loop matching canonicalizes the workspace
path, prefers the same harness when a matching managed row is open,
and falls back to legacy CWD-only matching for older loop state or unknown
harnesses. Agent Mux displays `🔁` on the session while its loop is active.
Finished runs stop counting as active when loop discovery sees
`run_finished_at` in `state.json`, and the `AgentSessionLoopFinished` hook
clears active-class icons while reconciling the `🔁` marker.

The dashboard keeps identity metadata out of the list entirely. A row is three
cells — the name, a Status phrase saying what it is doing, and the run timer —
behind a two-column notice gutter and the group's border:

```
    Needs you (1)
• ▌ charlie        Done         —
    Running (3)
  ▌ alpha          Working   0:42
  ▌ bravo          Working   3:07
    Idle (1)
  ▌ delta          Done         —
```

The `•` marks a finished turn you have not looked at yet. Its gutter is reserved
for the whole render pass whenever any row has news, so scrolling toward an
unread row never shifts the columns under you. `▌` is the group border, and
each heading carries its own count.

Everything the row does not say, the inspector says about the selected row: the
harness and profile, the Git branch with its `*` dirty marker, the working
directory, the watchdog reading, and the pane's live output. `i` shows and hides
it, and `tab` moves the keys between the two panes.

The branch is what tells two managed worktrees of one repository apart. Because
it is drawn once for the selected row rather than in a column every row reserves,
a long branch such as `amx/feature-name` is drawn whole instead of truncated. An
empty `Branch` line is the ordinary case rather than an error: the directory is
not a repository, `HEAD` is detached, the daemon has not probed it yet, or
`git_probe = false` turned the probe off entirely. The facts come from the
daemon, so a dashboard running with no daemon reachable shows no branch at all.

### Triage order

The default view sorts by urgency bucket, then by severity inside that bucket,
then by the time the row's visible state last changed. **Needs you** puts
terminal errors first, permission/input prompts next, and unread completed turns
last. **Running** puts warnings first, explicit running/subagent/compaction states
next, and iconless recent activity last. **Idle** has no severity subdivision.
Within one severity rank, the newest nonzero transition time leads; rows with no
transition time follow, and exact ties retain the configured/pinned order.

That transition clock is daemon-owned and survives a daemon restart. Activity
time and the run timer do not substitute for it: activity decides only whether
an iconless row is Active or Idle, while presentation recency decides order
within an already-selected severity rank.

### Responsive layout

The row is one shape at every width. It spends its budget down rather than
switching layouts: the run timer goes first, then the Status phrase, and the
name is the last thing standing. There are no longer three row presets, and the
80/120 width bands survive only as an input to the footer, which decides how
many hints it can afford to spell out.

What a wider terminal actually buys is the second pane. The inspector needs both
a width floor and at least ten body rows; below either one the dashboard is the
queue alone, with no divider and no metadata. The width floor is whichever is
larger of the built-in pane minimums (48 columns of queue, a divider, and 40 of
inspector) and `[defaults.preview] min_cols`, which defaults to 100 — so 100
columns of terminal is where the split appears out of the box. A terminal that
shrinks past a floor drops back to the queue rather than promoting the inspector
to full screen: losing sight of the session list is not a reasonable answer to a
resize you did not ask for. Full screen is available, but only because you
pressed `i` on a terminal too narrow to split.

Nothing the row drops is restated beneath the list. The inspector says all of it,
at every width where it fits, beside the selected row. The icon legend that used
to sit above the list is gone too — `?` opens the help overlay, which is where
the state icons are now named.

### Project switcher

The header's third segment is the current scope, reading `all projects` or the
project label. `p` and a click on that segment both open the same overlay, in
either list arrangement and at every width; the overlay is a modal, so it dims
the dashboard rather than taking a column from it.

Inside it, `↑`/`↓` or `j`/`k` move between scopes, `g`/`G` and the page keys
reach the ends, `/` searches the scopes by label, `enter` applies the
highlighted scope, and `esc`, `q`, or `p` closes it without touching the filter.
Clicking a scope row applies it. A scope list too long for the overlay scrolls
and marks omitted rows with `▲`/`▼`. `All projects` is offered first and
survives every search, because it is the way back out of a scope.

Each row shows the project label and its Needs Attention/Active/Idle tallies
under `need`/`act`/`idle` headings, with `›` on the row the keys are on and `●`
on the scope the dashboard is actually narrowed to — which are different facts
while the operator is reading the list. Labels aggregate case-insensitively
across live sessions, profiles, and repository identities; the highest-priority
member supplies the displayed casing and state icon. A label such as
`Client Portal` is therefore one row even when another live checkout reports
`client portal`.

The tallies are contextual. Building them removes only the project selector
from the active filter, so `#docs` can be swapped for `#api` in one keystroke
while a `@work` chip or a text term still constrains what the numbers count and
which scopes are offered at all. Applying a scope likewise replaces only that
one component and preserves the rest, quoting a label that needs it; cancelling
changes nothing. The selection is stored as the project's identity rather than
a row number, so a refresh that reorders or drops scopes under the open overlay
leaves the highlight on the same project.

Mouse behavior mirrors these controls: click a filter chip to remove that exact
component, wheel over the list to move the session selection, and wheel over the
inspector to scroll it. One click on a session selects it; a second click on the
same session opens its existing terminal. The three things the header still offers a
pointer are clickable: the view label, the health reading at the right edge, and
the scope. Terminal-native text selection remains available with
Shift-drag; Agent Mux does not intercept the modified drag.

### Projects view

Run **Show Projects View** from the command palette (`:`), or click the view
name in the header, to switch the list from the default urgency order to the
Projects view; **Show Urgency View** switches back. The selected session is
carried across both ways, so the cursor stays on the row you were looking at;
switching back out of a project summary selects that project's leading session.

The Projects view replaces the **Needs you** / **Running** / **Idle**
headings with one summary line per project, followed by that project's sessions
indented underneath it. A summary reads as its state icon, the project label, and
the bucket tally of its members — `▾ 🔑 api  1 attention · 1 active · 1 idle`.
Projects keep the position of their highest-urgency member, so the most urgent
work stays at the top of the list in either view.

Only a project with more than one live session earns a summary line. A
single-checkout project and a session that resolved to no project at all both
render as ordinary top-level rows, which keeps a summary a claim about a group
rather than decoration on a lone session.

Tree key, inert in the urgency view:

- `enter`: fold or unfold the selected project. This is the one place
  the key does not act on a session — on a project summary every session action
  (focus, restart, close, peek, open directory) is refused rather than applied to
  a guessed member.

Projects start expanded, and a fold lasts until `amx` exits. Neither the current
view nor the set of folded projects is written to disk: a project key is built
from filesystem paths, and persisting it would record checkout paths on behalf of
a keystroke that only asked to hide a few rows. Both reset on every launch. The
view also has no ordering of its own — it is the urgency order, grouped — and it
adds no Git work, since every fact it groups by was already stamped on the row at
discovery time.

### Header, footer, and the feedback slot

The dashboard spends three rows on chrome, and each says one kind of thing.

The **header** is the first line: the app name, the current view, and the
current scope on the left — `Agent Mux · Urgency · all projects` — and a single
health reading at the right edge, such as `● all checks ok`, `▲ sessions 12s
stale`, or `✖ hooks drifted`. The reading names the one check in the worst
state, or counts them when several are equally bad; it is clickable, and `:` →
`health` opens the same details overlay, which is where every check's own
finding and remediation command lives. When the width runs out the reading
degrades to its glyph alone before anything on the left is dropped.

Directly below the header sit the active filter chips, when a filter is active,
and then a blank spacer. Nothing else: the per-check health pips, the icon
legend, and the deprecation banner that used to live here are retired to the
health overlay, the help overlay, and the command palette respectively.

The **footer** is the final line and is exactly one row at every size. It is
generated from the same binding source the command palette reads, ordered by
priority, and it omits lower-priority hints rather than wrapping; `? help`
stays on the row and opens the full list, which is why the trailing `? more`
other footers add when hints are dropped never appears here. The essentials go
only after every other hint: movement, the enter binding, `t` when the selected
session can be sent to (or `: runs ↵ answer run` when its run has a question
open), `a actions` when a row is selected, `: commands`, and `? help`.
`/ filter` is a primary hint, dropped with the rest of its band. When even the
essentials will not fit spelled out, the whole row falls back at once to its
bare-key spelling (for example `j/k · ↵ · a · : · ?`) rather than shortening
some hints and not others. Narrower still, `t`, the answer hint, and `a` go
too, leaving movement, enter, `:`, and `?`.

Between the list and the footer is the **feedback slot**, one reserved row that
is genuinely blank when there is nothing to report. Action errors, in-progress
work, transient toasts, and undo feedback appear there. Standing state never
does: counts belong to the group headings, a session's state to its own row icon
and the inspector, and every health reading — including a delayed refresh —
to the header. A row that always said something would be a row the eye stops
checking.

Other roots — Past Sessions, Plans, the launcher, the ASL runs view — keep their
own footer and status-line behavior unchanged.

The detail pane's event history persists through terminal hooks (`Stop`,
`SessionEnd`, etc.) and through the watchdog's stale sweep, so you can still
review what a session did after its icon clears. History is held in memory per session and capped at 5 000 events (oldest
dropped); it is released when the session ends or the daemon restarts.
