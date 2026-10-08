# Usage

## Dashboard views and continuity

The default TUI opens on the live-session dashboard and selects the session
from the Kitty tab that opened it when possible. `/` filters the live-session
list; `:` opens the command palette, from which **Search All Past Sessions**
opens Past Sessions and **Search Plans** opens Plans.

Beside it, `amx overview` (bound to `F2`) opens the
[live session overview](#live-session-overview): a separate program that shows
every managed session at once as a grid of live pane previews.

The same workflows are scriptable. Machine-readable commands use one
`{"schema_version":1,"data":...}` success envelope; failures use a structured
`error` envelope and ambiguous selectors return candidates instead of guessing.

```bash
amx session list              # a table with relative ages, message counts, short session IDs any command accepts, and each conversation's title (its opening prompt when the harness named it nothing) followed by `→` and the opening of the agent's last reply, so runs of the same prompt read apart, in a terminal, the 25 most recent unless -n says otherwise; every session as tab-separated RFC 3339 rows, with full IDs followed by the opening prompt, the title (empty when there is none), the message count, a LIVE field (`here`, `live`, empty when closed, `?` when the daemon or Kitty could not be asked), and that last reply (empty when there was none) when piped
amx session list --json
amx session list --since 7d   # only sessions active in the last week (or 12h, 90m, …)
amx session list --here -n 5  # the five latest sessions started in this directory or below it
amx session list --harness codex --profile codex-work   # both repeatable
amx session list --project amx   # one project's sessions; "amx" reaches "🐱amx" as project launch reads it, and a directory-named project works too (repeatable)
amx session list --live       # only conversations open in a Kitty tab right now; refused, not empty, when the daemon or Kitty cannot say
amx session list --all        # also automated sessions (ASL runs, subagents, exec), hidden by default as in Past Sessions
amx session continue "$(amx session list | fzf | cut -f5)"   # pick by opening prompt
amx session search race daemon            # conversations mentioning both words, newest first, with match counts
amx session search '"race condition"' --here --since 7d   # a quoted phrase; takes every list selector
amx session search --source user flaky    # only your own prompts (or --source harness for the agent's output)
amx session show 09467ec1                 # one conversation: title, directory, ages, whether it is open in Kitty, message count, transcript path, how it ended, and the commands that continue or fork it
cd "$(amx session show 09467ec1 --json | jq -r .data.session.cwd)"
amx session show 09467ec1 --last 4       # the same, then its last 4 user and assistant messages in full, oldest first
amx session continue codex <session-id>
amx session continue --here   # the most recently active conversation started in this directory or below it, named on stderr before acting
amx session fork claude <session-id>
amx session fork-worktree codex <session-id> --yes
amx session handoff claude:<session-id> --profile codex-work
```

`continue`, `fork`, and `fork-worktree` also accept the single
`<harness>:<session-id>` selector that `handoff` takes, so
`amx session continue codex:<session-id>` works too. All four also accept a
bare `<session-id>`: it resolves when exactly one indexed conversation carries
that ID, and an ID shared across harnesses is refused with the
harness-qualified candidates rather than guessed. Any of these forms may give
just the start of the ID, at least four characters, the way a short Git hash
works: `amx session continue 7f67dc81`. An exact ID always wins over a longer
one it begins, and a prefix that several conversations share is refused with
every candidate.

`session continue` focuses the exact live Kitty owner when one exists and
otherwise resumes the closed conversation. `session list` and `session search`
in a terminal mark those conversations with `●` in a LIVE column, and the one
open in the window the command runs in with `here`; `session show` names their
Kitty window, and all three put the tab under `live` in `--json`; when the daemon or
Kitty cannot be asked, the column and line are left out rather than reporting
nothing open. Native forks are used for Claude
and Codex. Copilot forks use a portable handoff because Copilot does not expose
native fork semantics. Worktree forks create a tracked isolated Git worktree
and never copy uncommitted changes unless `--allow-dirty` is explicitly
confirmed.

## Dashboard

```bash
amx
```

This opens the dashboard, or focuses the existing dashboard tab if one is
already running. When opened with the bundled `Ctrl+Shift+D` binding, the
dashboard selects the running session from the Kitty tab that triggered it;
an unmanaged source tab leaves the current/default dashboard selection alone.

The dashboard answers a small keymap. Everything else it can do is reached
through two surfaces it opens: `a` for the row under the cursor, `:` for the
rest. Both are browsable lists you move a cursor through rather than sheets of
letters to aim at, and both read the same declaration of what a session action
is, so they cannot disagree about which actions exist or when one is allowed.
If you are upgrading from a release with the larger direct keymap, see
[Upgrading from the old dashboard shortcuts](#upgrading-from-the-old-dashboard-shortcuts)
below for where each key went.

- `j`/`k` or `↑`/`↓`: move the cursor, or scroll the inspector when it has focus
- `PgUp`/`PgDn`, `Home`/`End`: page, or jump to the ends of the focused pane
- `Enter`: focus the selected session's Kitty tab; on a family summary, fold or
  unfold the family instead
- `n`/`N`: move to the next/previous **Needs you** session. A target inside a
  folded family unfolds it and lands the cursor on the row; nothing is focused,
  opened, or marked as read — finding a waiting session is not answering it
- `Tab`/`Shift+Tab`: switch the focused pane. Its heading is highlighted and marked `focused`; the inactive pane's selection loses its reverse highlight.
- `a`: open the actions menu for the selected row — focus, peek at the
  scrollback, restart, restart under another profile, launch another session in
  the project, clear the status icon, mark it active, close, open a shell or the
  configured editor in its directory, copy the title, lock, mute. It is a menu
  you move through, not a sheet of letters: `j`/`k` or `↑`/`↓` move the selection,
  `PgUp`/`PgDn`, `Ctrl+f`/`Ctrl+b`, `Ctrl+d`/`Ctrl+u`, and `g`/`G` move it
  further, and `Enter` runs the selected entry. Each entry still answers its own
  mnemonic immediately, from wherever the cursor happens to be. A fresh menu
  opens on **Open session**, so `a` then `Enter` does what `Enter` on the row did.
  An entry that cannot apply to the row is dimmed but still selectable, and the
  detail block under the list is where its reason is written. `Esc` or `q`
  closes the menu; `Ctrl+C` quits the dashboard
- `u`: relaunch the session you just closed, while the five-second recovery
  window named on the toast is still open
- `:`: open the command palette; it is one line per command rather than a card,
  so more of them are on screen at once. Type to search, use `↑`/`↓`,
  `Ctrl+j`/`Ctrl+k`, or `Ctrl+n`/`Ctrl+p` to move, `Backspace` to edit, `Enter`
  to run, `Esc` to close, and `Ctrl+c` to quit
- `/`: edit the filter. The editor opens on the filter already on screen, so
  `Backspace` removes the last component; `Esc` restores what was committed
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
per-session hook event log), **Resort Tabs**, and **Undo Last Close**.

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

### Upgrading from the old dashboard shortcuts

The dashboard used to bind roughly thirty keys directly. It binds eleven now,
and the rest of the capabilities moved to the two surfaces above — no capability
was dropped. A retired key is *unbound* rather than rebound: pressing it does
nothing at all, so nothing you remember can now mean something else.

| You used to press | Do this instead |
| --- | --- |
| `space` (focus) | `Enter` |
| `e` peek, `r` restart, `c` profile override, `C` launch another, `x` clear icon, `X` close, `o` shell, `O` editor, `y` copy title, `L` lock, `m` mute | `a`, then the same letter. The menu's mnemonics are unchanged; you can also move to the entry and press `Enter` |
| `u` undo close | `u` — it came back. What it undoes expires after five seconds, and a capability that has to be found by name before it lapses is not reachable. `:` → **Undo Last Close** still works |
| `s` / `S` past sessions | `s` / `S` — both came back for browsing. `:` → **Search This Project's Past Sessions** / **Search All Past Sessions** still works |
| `P` plans, `W` worktrees, `A` runs | `:` → **Search Plans** / **Manage Worktrees** / **Show Runs** |
| `d` session detail, `T` timeline | `i` for the inspector, or `:` → **Show Session Details** / **Show Session Timeline** |
| `v` urgency ↔ projects | `:` → **Show Projects View** / **Show Urgency View** |
| `f` cycle profile | `:` → **Cycle Profile Filter**, or `/` for any filter component |
| `R` resort tabs | `:` → **Resort Tabs** |
| `p` preview | `i` — the inspector absorbed it. `p` is the project switcher now, which is the one key whose meaning changed rather than disappearing |
| `b` project rail | `p` — the rail is gone; the switcher applies the same scopes |
| `g` / `G` | `Home` / `End`. Both still work inside the project switcher, the health details overlay, and the detail timeline |
| `Ctrl+d` / `Ctrl+u` | `PgUp` / `PgDn` |
| `]`/`[`, `]]`/`[[`, `}`/`{` attention jumps | `n`/`N`. The chord is gone, and so is the variant that focused the terminal on arrival: press `Enter` when you want that |
| `Tab` chip cycling, `1`–`9` chip removal, `Backspace` on a focused chip | `/`, which now opens on the filter already applied; `Backspace` removes the last component. `Tab` switches panes instead |
| `←`/`→` to fold a family | `Enter` on the family summary |

Two configuration keys changed meaning with them. `[defaults.jump]` no longer
selects anything — `n`/`N` walk the Needs You group as displayed — and is
reported as a deprecated key until you delete it; your config file is never
rewritten. `[defaults.preview]` still sets the live pane's ratio, refresh, and
minimum size, now for the dashboard's inspector as well as the launcher's
preview; `direction` applies to the launcher only.

The full row-by-row record, including which surface each retired key's
capability went to, is `docs/dashboard-keymap-inventory.md`.

Rows marked with `🔁` have at least one active `agent-session-loop` run whose
workspace matches the project row. Loop matching canonicalizes the workspace
path, prefers the same harness when a matching managed Kitty tab or row is open,
and falls back to legacy CWD-only matching for older loop state or unknown
harnesses. Agent Mux also mirrors `🔁` into the Kitty project tab that owns the
loop while it is active, preserving existing state, star, and mute icons.
Finished runs stop counting as active when loop discovery sees
`run_finished_at` in `state.json`, and the `AgentSessionLoopFinished` hook
clears active-class icons while reconciling the tab-title `🔁` marker.

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
same session focuses its Kitty tab. The three things the header still offers a
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

Tree keys, all of which are inert in the urgency view:

- `→`: expand the selected project
- `←`: collapse the selected project; pressed on a session it selects that
  session's project first, so two presses fold the project you are inside
- `enter` / `space`: fold or unfold the selected project. This is the one place
  these keys do not act on a session — on a project summary every session action
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
priority, and it omits lower-priority hints rather than wrapping — a trailing
`? more` says hints were dropped, and `?` opens the full list. Six essentials
are never dropped: movement, the enter binding, `a actions`, `: commands`,
`/ filter`, and `? help`. When even those will not fit spelled out, the whole
row falls back at once to its bare-key spelling (`j/k · ↵ · a · : · / · ?`)
rather than shortening some hints and not others.

Between the list and the footer is the **feedback slot**, one reserved row that
is genuinely blank when there is nothing to report. Action errors, in-progress
work, transient toasts, and undo feedback appear there. Standing state never
does: counts belong to the group headings, a session's state to its own row icon
and the inspector, and every health reading — a delayed Kitty refresh included —
to the header. A row that always said something would be a row the eye stops
checking.

Other roots — Past Sessions, Plans, the launcher, the ASL runs view — keep their
own footer and status-line behavior unchanged.

The detail pane's event history persists through terminal hooks (`Stop`,
`SessionEnd`, etc.) and through the watchdog's stale sweep, so you can still
review what a session did after its icon clears. History is held in memory per
Kitty window and capped at 5 000 events (oldest dropped); it is released only
when the window closes or the daemon restarts.

## Live session overview

```bash
amx overview
```

`F2` in the bundled Kitty snippet opens the same surface as an overlay. It is a
grid: one tile per managed session in this Kitty instance, each with a
three-line header — which session, what it is doing and for how long, and what
is running it — and a live preview of that pane's screen below a divider. It
answers "what is every session doing right now" in one glance, which the list
answers one row at a time.

On opening, the tiles put sessions needing attention first, followed by other
statuses that are not Working, then Working sessions. Within each tier they use
project and tab identity order. While the overview remains open, refreshes
update statuses and previews and move sessions needing attention to the top.
Relative order within the attention and non-attention groups stays stable;
new sessions join the end of their group. Selection stays on the same session.
Closing and reopening F2 takes a fresh triage order.

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
- `u`: relaunch the session you just closed from here. It is offered for five
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
- `Enter`: focus the selected session's Kitty tab and leave. The tab is captured
  when you press the key, so a refresh landing during the round trip cannot
  hand you a different pane. A focus that fails leaves you on the overview with
  the reason on the feedback row and the same tile selected, which is the only
  state retrying makes sense from
- `1`–`9`, `0`: select and immediately focus the corresponding card among the
  first ten overall (`0` is the tenth), regardless of the current page. Digits
  appear at the right end of the existing second information row, in reading
  order: left to right across each row, then top to bottom. Later cards have no
  digit; a digit without a matching card does nothing. Like `Enter`, a failed
  focus leaves the target selected for retry, and pending focus ignores repeats.
- `Esc`, `q`, `Ctrl+C`: close, whether or not a read is still out

Browsing itself still only observes sessions and their screen text: moving the
selection, paging, and `n`/`N` touch nothing, `c` changes only the daemon's
in-memory view preference, and `Enter` and the digits focus a tab. Everything
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

The overview is a singleton. Invoking it again — `F2` from any tab, or `amx
overview` from a shell — focuses the one already open rather than drawing a
second copy, and hands it nothing: it already shows every session, so it keeps
the selection and the page you left it on. Unlike the dashboard and the overlay
pickers, it reports a Kitty listing or focus failure as an error instead of
assuming nothing is open, because the degraded answer there is a second
full-screen surface polling Kitty beside the first.

The topology it draws comes from the daemon's published window snapshot when one
is available and attributable to this Kitty instance, exactly as the dashboard's
does, and falls back to listing Kitty directly when it is not. A refresh that is
delayed says so on the feedback row — `Kitty refresh delayed; showing cached
sessions (Ns old)` — and keeps drawing the last good list rather than emptying
the grid.

## The agent-session-loop cockpit

**Show Runs** in the command palette opens the runs view: every active or
actionable `agent-session-loop` run, newest need first. It is an *intervention* surface, not a loop runner. Agent Mux
never starts, schedules, or tails a loop — start runs with `agent-session-loop`
directly and use the cockpit to answer and steer the ones already in flight.

Inside the runs view:

- `j`/`k`, `g`/`G`, `Ctrl+d`/`Ctrl+u`, `Ctrl+f`/`Ctrl+b`: move the cursor
- `r`: open the response form for the selected run
- `s`: stop the run after the current session, after confirmation
- `a`: abort the run, after confirmation
- `w`: open the run's Workbench page in your browser
- `Enter` or `f`: focus the Kitty window bound to the run
- `Y`: copy the run's workspace root
- `?`: show the keymap; `q`, `Esc`, or `A` returns to the dashboard (`A` is a
  runs-view key now — it closes the view it used to open)

The response form shows the question and the replies the run will accept.
`Tab`/`Shift+Tab` moves between the editor and the buttons, `←`/`→` picks a
button, `Enter` submits the focused button (and inserts a newline in the
editor), `Ctrl+s` submits from anywhere, `Ctrl+n`/`Ctrl+p` step through a
multi-question set, and `Esc` closes the form. A reply button that needs text
refuses an empty draft rather than sending one.

At 100 columns or wider the list shows state, label, session/phase, harness,
age, and workspace root. Below that it drops to state, label, age, and reason,
and moves session, harness, and workspace into the detail panel underneath.

### Cockpit boundary

The cockpit answers questions, resumes, stops, and aborts. Everything else —
plan authoring, run scheduling, verification detail, per-session logs, and any
configuration — lives in the Workbench, which `w` opens. Workbench URLs are
minted per request, carry a single-use token, and are handed straight to the
browser: Agent Mux never stores, caches, or logs one, so a failed open simply
asks again rather than reusing a link.

### Degraded behavior

Agent Mux talks to one `agent-session-loop` bridge subprocess owned by the
daemon. When that process is gone, the header shows `[unavailable]` and the list
reads `(agent-session-loop is unavailable)` with a short sanitized reason.

When the process is alive but the last snapshot has aged out, the header shows
`[stale]` and a banner reads `read-only: showing the last agent-session-loop
snapshot`. Reading keeps working; every action refuses immediately with
`actions are unavailable: this snapshot is read-only` rather than sending a
mutation whose outcome nobody can predict. The daemon restarts the bridge on a
capped backoff and the view returns to `[live]` on its own. The same banner
appears for the first few seconds after a daemon restart on a large plan corpus:
a snapshot projects every discovered plan, and actions wait for the first one.

Actions are also refused, each with its own message, when the daemon reports
mutations disabled, when the selected run carries no action token, and when the
run's own state has moved on — a plan that vanished, a run that already
finished, a question answered somewhere else. Every action carries a token
fingerprinting the state you were looking at; the bridge re-reads that state
before writing and rejects a stale token without touching anything. Agent Mux
never retries a mutation on your behalf. Refresh and reissue.

### What the cockpit never keeps

Question and reply text is fetched only to render the form you are looking at.
It is never written into snapshots, caches, logs, the causal journal, hook
events, toasts, `amx doctor`, or a debug bundle. The same holds for Workbench
tokens. What *is* published about the integration is deliberately limited to
counters and identities: whether the bridge is available, its version and
protocol, its restart count, snapshot age, how many actions were accepted,
consumed, rejected, expired, or superseded, and a sanitized failure reason that
is reduced to a stable error code.

### Recovery

Start with `amx doctor --strict`, whose `asl:` line reports mode, executable,
version, protocol, snapshot age, restart count, action outcomes, and the current
reason. `amx daemon status` prints the same line. If the bridge is missing or
too old, install or upgrade `agent-session-loop` first, then restart the daemon.
`docs/troubleshooting.md` covers the individual failures.

## Launcher

The launcher reads configured `projects`, shows their default profile/harness,
and lets you multi-select launches.

Launcher rows likewise show the project title only; profile information stays in
its own badge/metadata columns.

- press `/` to filter live,
- `f` cycles project visibility through each default profile and back to all,
- `g` / `G` jump to the top / bottom,
- `space` toggles selection,
- `enter` launches the selected projects,
- `P` pins or unpins the highlighted project so pinned projects stay at the top,
- `s` searches past session transcripts for the highlighted project,
- `C` chooses a **one-time profile override** for the highlighted project,
- `o` opens every selected project's directory; with no selection, it opens
  the highlighted project's directory instead.
- `?` shows the keymap,
- `esc` clears an active filter first and leaves the launcher on a second
  press; `q` leaves it in one step. Neither quits Agent Mux — `Ctrl+C` does.

The profile cycle starts at **All** whenever the launcher opens, includes only
profiles that own configured projects, and follows the launcher's alphabetical
profile-group order. It matches each project's default profile and combines
with any active `/` filter.

Pins persist per user across Agent Mux restarts. Pinned and unpinned groups each
retain the launcher's existing profile-then-project ordering, including while a
filter is active.

Overrides are useful for cross-harness launches. For example, a project such as
`sis` can keep `profile = "vta"` as its Claude default while `C` launches that
same project under a Codex profile such as `vta-codex`.

Overrides only affect launcher behavior. They do not rewrite `config.toml`.
The dashboard's own **launch new** entry (`a`, then `C`) uses the same profile
picker, but launches from the highlighted running row instead of from the
launcher list. Pressing `enter` in the launcher
is an explicit request for new tab(s): if the selected project/profile is
already running, Agent Mux opens another instance instead of skipping or
focusing the existing tab.

### Launching from a shell

The same launch action is available without the dashboard, which is what the
menu bar companion and scripts use:

```bash
amx project list                               # PROFILE TITLE HARNESS DIRECTORY GROUPS in a terminal; piped, headerless tab-separated rows of profile, title, harness, directory as configured, groups, and a problem field ("missing", …) last
IFS=$'\t' read -r profile title _ < <(amx project list | fzf) && amx project launch "$title" --profile "$profile"   # pick a project to launch
amx project list --json                        # [{profile,title,harness,directory,directory_problem?,groups}]
amx project list --missing                     # only projects whose directory is unusable
amx project list --archived                    # only projects marked archived = true, which are otherwise left out
amx project list --group work --harness codex  # filters combine; each flag repeats, ignoring case
amx project launch API                         # one project titled API
amx project launch amx                         # the project titled 🐱amx
amx project launch --title API --profile codex-work --json
```

A project is identified by its profile and title, given as the argument or as
`--title`. An exact title always wins. Otherwise the title still selects a
project when it is the only one whose title reads the same without its leading
emoji or symbols, ignoring case, so `amx` launches `🐱amx`. `--profile` is
needed only when several projects share a title. An ambiguous or unmatched
selector launches nothing and lists the configured projects it could have meant
(`ambiguous_project` or `not_found`, with `candidates` whose `id` is
`profile/title`); an unmatched title lists only the projects whose titles
contain it, when there are any. Like `enter` in the launcher, `project launch` always opens a
new tab. Its `--json` data is `{action, changed, message, project, tab_id,
window_id}`; `tab_id` and `window_id` name the new tab when exactly one new tab
carrying the title appeared, and are omitted otherwise.

A project whose directory is missing or is not a directory is never launched,
because Kitty would quietly open the tab somewhere else: `project launch`
refuses with `directory_unusable`, naming the project and the problem, and the
launcher marks the row `(missing)` and its preview's `dir:` line with the
reason. `amx project list --missing` lists every such project.

A project marked `archived = true` stays in `config.toml` but is offered
nowhere: the launcher, `project list`, and `project launch`'s candidates leave
it out, naming it refuses with `project_archived`, and `config validate` and
`doctor` do not warn about its directory. Overseer policy still reads it, so
archiving a project never lifts an `asl_overseer` exclusion.

`amx open-dashboard` brings the dashboard forward without drawing it in the
calling terminal: it focuses a running dashboard (the singleton rule) or opens
one in a new tab, and `--json` reports `{window_id, tab_id, opened}`. It refuses
rather than opening a second dashboard when Kitty cannot be listed or the
running one cannot be focused.

Both work from outside Kitty when `KITTY_LISTEN_ON` names the instance's
remote-control socket:

```bash
KITTY_LISTEN_ON=unix:/tmp/kitty-12345 amx project launch --title API --json
KITTY_LISTEN_ON=unix:/tmp/kitty-12345 amx open-dashboard --json
```

## Managed worktrees

Run **Manage Worktrees** from the command palette (`:`) to open the
Git-authoritative inventory under
`~/.local/share/agent-mux/worktrees`. `/` filters by stable ID, project,
branch, path, profile, harness, and lifecycle labels. `j`/`k`, `g`/`G`, and
`Ctrl+d/u/f/b` navigate; `Y` copies the canonical checkout path, `o` opens a
shell, `O` opens the configured editor, and `r` refreshes Git and activity
facts.

`Enter` smart-reopens the selected worktree. A sole live Kitty session is
focused. Multiple live sessions open a picker that identifies the project,
profile, harness, Kitty tab/window, candidate ID, and native harness session ID
when known. With no live session, Agent Mux resumes the newest indexed
conversation inside the checkout or launches a fresh session. If recorded
profile provenance is no longer usable, the normal profile picker requests an
explicit replacement; it does not silently choose another identity.

Cleanup is always explicit:

- `d` removes a clean, inactive checkout and its sidecar while retaining the
  branch.
- `D` additionally deletes the worktree branch, but only after fresh Git checks
  prove it is merged into the source branch recorded when the worktree was
  created.
- `F` forgets retained metadata for a missing checkout or invalid metadata. It
  changes neither a checkout nor a Git branch.

The TUI shows refusal reasons known from its latest snapshot before opening a
confirmation, then the shared action performs fresh activity, dirtiness, lock,
registration, metadata, ref, and ancestry checks immediately before mutation.
Agent Mux never force-removes a worktree, deletes an unmerged branch, closes a
Kitty session, stops an active loop, or acts on a worktree outside its managed
root. Partial cleanup is reported with the durable steps that succeeded; the
inventory refreshes and retains the recovery row instead of presenting the
operation as all-or-nothing.

The same lifecycle is scriptable:

```bash
amx worktree list [--json]
amx worktree show <selector> [--json]
amx worktree reopen <selector> [--session <candidate>] [--profile <name>] [--json]
amx worktree shell <selector> [--json]
amx worktree edit <selector> [--json]
amx worktree remove <selector> --yes [--delete-branch] [--json]
amx worktree forget <selector> --yes [--json]
amx worktree prune [--merged] [--yes] [--delete-branch] [--json]
```

`prune` selects every worktree `remove` would accept on its own — present,
valid, clean, unlocked, and used by no live session or loop — and with
`--merged` only those whose branch is reachable from its source. Without
`--yes` it lists them and changes nothing. With `--yes` each one goes through
`remove`, which re-checks everything at that moment, so a worktree that became
active in the meantime is kept and reported. Branches are kept unless
`--delete-branch` is given, and even then only merged branches are deleted.

A selector is a full stable ID, unique ID prefix, branch, or canonical path.
Ambiguous selectors and live sessions return structured candidates rather than
guessing. Machine-readable output keeps the common schema-v1 envelope; all
worktree fields are additive.

Each newly created checkout has an adjacent private schema-v1
`<checkout>.amx-worktree.json` sidecar. Git remains authoritative for checkout,
branch, status, and merge facts; the durable sidecar stores the stable ID and
provenance Git does not retain: source root/branch/base commit, project,
profile, harness, source conversation, and creation time. Older managed-root
checkouts on `amx/…` branches without sidecars remain visible as `legacy`.
Default branch-retaining removal is available after fresh safety checks, but
branch deletion requires validated schema-v1 provenance. Malformed sidecars
stay visible as invalid recovery records and are never discarded automatically.

## Past sessions

Run **Search All Past Sessions** from the command palette (`:`) to search past
sessions across every configured profile and project, or **Search This
Project's Past Sessions** to open the same view filtered to the highlighted
project. On the dashboard, `s` opens the scoped search for the selected
project and `S` opens the all-project search; in the launcher, `s` is the
direct key for the scoped search.
Agent Mux warms the filesystem cache
at `~/.local/share/agent-mux/transcripts.json`, then refreshes it from the
configured Claude, Copilot, and Codex transcript directories in the background.
Existing rows remain visible during refreshes. The header and sticky status bar
mark them as cached while scanning, and the active text/chip query refreshes
automatically when the scan completes.

The JSON metadata file and `transcripts.search/` sidecars are disposable
caches. If either is missing, malformed, or partially unreadable, Agent Mux
quarantines or rebuilds the affected cache from transcript source files. Stable
`.lock` sidecars coordinate writers across processes, and cache replacement
uses atomic renames. Transcript source files remain authoritative and are not
rewritten by refresh or search.

When multiple profiles point at the same harness transcript home, Agent Mux
scans that home once and stores one metadata row per physical transcript. Those
rows keep a stable display profile but remain searchable under every candidate
profile because older transcript files do not reliably encode the exact launch
profile.

Projects configured with the same directory intentionally share that
directory's transcript history. The first configured project remains the
stable title in unfiltered Past Sessions, while selecting another shared
project alias shows that alias in result rows and carries it through viewing,
continuing, forking, handoff, and deletion prompts. The project picker remains
configuration-backed, so every configured alias stays selectable even when it
is not the stored primary title.

- `/` enters a free-text query; results scan matching source transcripts on
  demand and show the per-session match count. Words are case-insensitive and
  ANDed: `auth refactor` finds transcripts that mention both, anywhere and in
  any order, and `"auth refactor"` keeps a phrase together. The Plans search
  reads queries the same way.
- `h`, `p`, and `r` open single-value chip pickers for harness, profile,
  and project; `m` picks the message source (`user` or `harness`).
- `i` copies the selected session ID, and `Y` copies its transcript path.
- `enter` opens the transcript viewer; `n`/`N` move between matches,
  `j`/`k`, `g`/`G`, and `Ctrl+d/u/f/b` scroll, `y` copies the selected
  message, `Y` copies the transcript path, and `/` returns to the list with
  the query open for refining. The viewer also accepts the
  `c`, `f`, `w`, `H`, `E`, and `i` actions below for the conversation it has
  open and returns to the viewer once they complete.
- `c` continues the selected conversation, `f` forks it, `w` creates an
  isolated Git worktree fork, `H` creates a handoff, and `E` creates an
  editable handoff.
- `d` opens a three-way delete prompt: `i` hides the cache row while keeping
  the source file, and `f` also removes the source transcript after explicit
  confirmation.

Maintenance commands:

```bash
amx transcripts status
amx transcripts compact
amx transcripts rebuild
amx transcripts rebuild --all
```

`status` is read-only and reports metadata, sidecar, missing-sidecar, and
orphan-sidecar usage. In a terminal it reads as a short summary — what Past
Sessions holds, how much was removed from it, sizes in KiB/MiB, paths under
`~`, and the command that clears any orphaned or missing sidecars — and
`compact` likewise says what it moved and removed. Piped output keeps the
`key: value` lines with raw byte counts and full paths. All three accept `--json`, which emits the same keys in
the schema-v1 envelope (`compact` adds `before` and `after` store snapshots). `compact` takes the transcript cache lock, migrates any
legacy inline search text, persists metadata, and then removes only regular
hash-named sidecars that are no longer referenced by a cache key. It preserves
referenced sidecars, transcript source files, temporary files, symlinks,
directories, and unrelated artifacts. `rebuild` reparses source transcripts in
the configured scan window and recreates the disposable sidecar store; run it
after installing an Agent Mux transcript-parser upgrade so unchanged cached
files are parsed with the new reader. Add `--all` only for a full backfill of
discoverable history that ignores the configured scan age. Normal refreshes never
compact orphan sidecars.

Retention is controlled by `[defaults.session_archive] keep_days` (default
30). Refresh and prune operations hide cache entries older than `keep_days`;
the on-disk transcript files remain intact unless explicitly deleted. Each
refresh only walks files modified within `scan_max_age_days` days (default:
inherits `keep_days`). Set it to `-1` for an unbounded walk.

## Plans and local storage

Run **Search Plans** from the command palette (`:`) to search saved plan
directories. Agent Mux loads warm rows from the
filesystem cache first and refreshes the configured `plans_directory` in the
background. A missing or malformed cache is quarantined and rebuilt from the
plan directories; an unreadable plan never blanks rows that were read
successfully. Each row and viewer header shows the associated configured
project title. Press `r` to filter by the projects represented in the index or
by `Unknown`; equally specific configuration aliases are separate filter
choices and match the same plans. `/` searches, `o` cycles the result sort
between match count and recency, `O` (or `e`) opens
the selected plan directory in your editor, `Y` copies the plan directory, and `enter` opens the viewer. The viewer
supports `j`/`k`, `g`/`G`, `Ctrl+d/u/f/b`, `n`/`N` to move between matches,
`/` to refine the query, `y` to copy the selected raw line, `Y` to copy the plan
directory, and `O` (or `e`) to open the plan directory in your editor. `esc` or
`q` returns to the plan list, and pressing either again there leaves Plans.

Project association is source-backed rather than inferred from plan prose.
Agent Mux resolves the canonical plan workspace in this order: retained ASL
run state, `plan-metadata.json`, then legacy
`prepare-validation.json.codebase_root`. It maps that path to the most-specific
configured project (including registered Git worktrees). A plan with no valid
workspace metadata, or a workspace that matches no configured project, is
shown as `Unknown`; if a previously valid source becomes malformed, the cache
keeps its last-good association and surfaces a refresh warning. `asl prepare`
and non-dry-run `asl run` write the versioned `plan-metadata.json` contract for
new and resumed plans.

Agent Mux does not use SQLite. Local files have deliberately narrow ownership:

| Path | Ownership and cleanup |
| --- | --- |
| `~/.local/share/agent-mux/transcripts.json` and `transcripts.search/` | Disposable metadata and search caches rebuilt from harness transcript files. Malformed metadata is renamed with a `.corrupt-<timestamp>` suffix; orphan sidecars are measured by `transcripts status`, pruned explicitly by `transcripts compact`, or fully reset by `transcripts rebuild --all`. |
| `~/.local/share/agent-mux/plans.json` and `plans.search/` | Disposable metadata and search caches rebuilt from directories under `plans_directory`. |
| `~/.local/share/agent-mux/handoffs/` | Private standalone Markdown artifacts. Successful and partially launched handoffs remain until you delete them. |
| `~/.local/share/agent-mux/worktrees/` and adjacent `*.amx-worktree.json` files | Durable managed Git worktrees plus private schema-v1 provenance. Git owns checkout/branch truth; sidecars retain stable identity and source/profile/harness lineage. Use `amx worktree remove ... --yes` for safe cleanup or `amx worktree forget ... --yes` only for missing/invalid metadata. These are not disposable caches. |
| `~/.local/share/agent-mux/launch_overrides.json` | Per-user launcher choices, including one-time profile overrides and pinned projects; independent of transcript and plan caches. |
| `daemon.sock`, `daemon.lock`, and `daemon.autostart.lock` | Ordinary daemon coordination files, not database state. |

The corresponding `.lock` sidecars are stable advisory locks and may remain
between runs. Do not delete the whole `agent-mux` data directory to repair a
cache: transcript files, plan directories, handoffs, durable worktrees and sidecars, configuration,
and launch overrides have separate lifecycles.

## One-off cleanup after upgrading from a SQLite release

Old installations may still contain retired database artifacts. This is a
manual deployment cleanup, not a recurring migration. First build and pass
`just acceptance`, stop the installed daemon, and confirm `amx daemon status`
no longer reports it running. Preserve the caches and assets in the table
above, then remove only these entries from
`~/.local/share/agent-mux/`:

```text
agent-mux.db
agent-mux.db-wal
agent-mux.db-shm
agent-mux.db.recovery.lock
agent-mux.db.rebuild
agent-mux.db.rebuild-startup.lock
recovery/
```

Install atomically with `just install`, restart with `amx daemon restart`, and
verify `amx daemon status`, `amx doctor --strict`, `amx session list`, Past
Sessions, and Plans. Recheck the allowlist above afterward; none of the files
or the `recovery/` directory should be recreated. Future runs never delete
these legacy paths automatically.

## Filter DSL

The list and launcher share the same filter DSL:

| Token | Meaning |
| --- | --- |
| `word` | case-insensitive substring against title, directory, or profile |
| `@profile` | exact profile match |
| `%harness` | exact harness match, e.g. `%claude`, `%codex`, `%copilot` |
| `!state` | state match: one whole urgency group (`attention`, `active`, `idle`) or one icon (`bolt`, `key`, `stop`, `warning`, `error`, `subagent`, `compact`, `question`) |
| `#project` | exact case-insensitive project-label match |
| `#"project name"` | project label containing whitespace; inside quotes use `\"` for `"` and `\\` for `\` |
| `\@foo`, `\!x`, `\%y`, `\#z` | literal leading `@`, `!`, `%`, or `#` |

`!attention`, `!active`, and `!idle` select exactly the three groups the
dashboard already sorts rows into, so `!attention` always matches the rows under
the **Needs you** heading and nothing else. They are groups, not shorthand
for a set of icons: an unseen `🛑` is in the attention group and a `🛑` you have
already looked at is not, so the two rows carry the same icon and answer
`!attention` differently.

Project completion is contextual: type `#` in the filter editor and the popup
lists the case-insensitive canonical labels remaining after every other active
token is applied. Labels that need quoting are inserted in their escaped quoted
form.

Tokens are whitespace-separated and combine with AND. Lone `@`, `!`, `%`, and `#`
markers and unrecognized `!state` names are ignored rather than rejected — a
typo like `!attn` leaves the list unfiltered instead of emptying it.
