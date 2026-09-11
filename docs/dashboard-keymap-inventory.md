# Dashboard keymap inventory

Every key the dashboard (`ModeList`) used to answer, and where its capability
lives now.

The migration is complete: every row below reads **Retired** or **Survives**,
and nothing is pending. The inventory is kept because it is the record that no
capability was dropped — a key could only retire once the row naming it said
which surviving surface still reached it, and that claim is worth being able to
re-read after the fact.

`tui/dashboard_bindings.go` remains the single declaration: a key absent from
that table is not dispatched, not advertised in the palette, and not in the
footer or the generated help. Retiring anything else needs no other edit, and
adding anything back needs no more than a row there plus a row here.

Both halves of that claim are asserted as a set rather than only per family.
`tui.TestEveryRetiredDashboardKeyIsInertAtTheList` presses every spelling in
the two tables below that reads **Retired** — in each of the seven model states
the binding suite sweeps — and requires the model to come back unchanged, which
catches a key that starts doing something new as well as one that never stopped.
`tui.TestEveryFinalDashboardBindingDispatches` is its complement, since a table
of uniformly inert keys would satisfy the first test perfectly.
`tui.TestCommandCatalogDashboardShortcutBindings` closes the loop from the other
end: it maps every catalog command to the key it advertises, exhaustively in
both directions, so a retirement that left a stale shortcut on a palette row
fails there.

## The replacement keymap

| Key | Meaning |
| --- | --- |
| `j`/`k`, `↑`/`↓` | Navigate queue or scroll focused inspector |
| `PgUp`/`PgDn`, `Home`/`End` | Page or reach beginning/end of focused pane |
| `Enter` | Open selected terminal; toggle a selected family summary |
| `n`/`N` | Next/previous Needs you session, preserving pane focus |
| `Tab`/`Shift+Tab` | Switch between visible panes |
| `p` | Project switcher |
| `i` | Toggle inspector; open full-screen when necessary |
| `/` | Edit filters |
| `a` | Selected-session actions |
| `l` | Launcher |
| `s` | Past Sessions for the selected project |
| `S` | All-project Past Sessions, newest activity first; clears previous history filters |
| `:` | Command palette |
| `?` | Help |
| `q`, `Ctrl+C` | Quit dashboard |
| `Esc` | Close overlay, then leave full-screen/inspector focus, then clear filters |

Three of these keys survive their own spelling but change meaning: `p` moved
from the preview toggle to the project switcher, `Tab` from filter-chip cycling
to pane switching, and `Enter` keeps focusing but no longer shares the job with
`space`.

## Command-backed bindings

Each surviving row is one entry in `dashboardBindings()`, which is ten entries
long. "Successor" names the surface that reaches the capability now. A palette
entry exists for every command in this table — the catalog is the complete list
— so "palette" never means "gone"; where something more direct exists, it is
named first.

| Key | Command ID | Capability | Disposition | Successor |
| --- | --- | --- | --- | --- |
| `enter` / `space` | `FocusSession` | Focus the session's Kitty tab | `enter` survives, `space` **retired** | `Enter`; actions `a` → focus tab; palette |
| `e` | `PeekScrollback` | Peek at the pane's scrollback | **Retired** | actions `a`; palette |
| `r` | `RestartSession` | Restart the harness in place | **Retired** | actions `a`; palette |
| `c` | `OverrideProfile` | Relaunch under a different profile | **Retired** | actions `a`; palette |
| `C` | `LaunchAnother` | Launch another session in the same project | **Retired** | actions `a`; palette |
| `x` | `ClearStatusIcon` | Acknowledge the status icon | **Retired** | actions `a`; palette |
| `X` | `CloseSession` | Close the session (undoable) | **Retired** | actions `a`; palette |
| `o` | `OpenDirectoryID` | Open a shell in the session's directory | **Retired** | actions `a`; palette |
| `O` | `OpenEditorID` | Open the configured editor there | **Retired** | actions `a`; palette |
| `y` | `CopyTitleID` | Copy the tab title | **Retired** | actions `a`; palette |
| `L` | `ToggleLockID` | Lock/unlock the session | **Retired** | actions `a`; palette |
| `m` | `ToggleMuteID` | Mute/unmute notifications | **Retired** | actions `a`; palette |
| `s` | `SearchProject` | Past Sessions scoped to this project | Survives (restored) | `s`; palette |
| `d` | `DismissDeprecation` | Silence the configuration deprecation notice | **Retired** | palette; the health details overlay's own `d`. The banner this used to hide is gone — the notice reads as the header's health indicator and as one issue in that overlay, and both recompute from the same dismissible issue |
| `d` | `ShowDetails` | Per-session event detail overlay | **Retired** | inspector `i`; palette |
| `T` | `ShowTimeline` | Per-session phase timeline | **Retired** | palette |
| `/` | `FilterSessions` | Edit the filter | Survives | `/` |
| `f` | `CycleProfileFilter` | Cycle the profile filter component | **Retired** | project switcher `p` for the project component; `/` for the rest; palette |
| `esc` | `ClearFilters` | Clear all filter components | Survives, as the last rung of the Escape ladder | `Esc` |
| `l` | `OpenLauncher` | Open the launcher | Survives | `l` |
| `S` | `SearchAllSessions` | Past Sessions across all projects | Survives (restored for recency browsing) | `S`; palette |
| `P` | `SearchPlans` | Plans browser | **Retired** | palette |
| `W` | `ManageWorktrees` | Managed worktrees | **Retired** | palette |
| `A` | `ShowRuns` | ASL Runs | **Retired** | palette |
| `p` | `TogglePreview` | Toggle the preview pane | **Retired.** The command is `ToggleInspector` (`explore.toggle-inspector`) on `i`, and `p` has since been claimed by the project switcher (`explore.switch-project`) | inspector `i`; palette |
| `b` | `ToggleRail` | Toggle the project rail | **Retired.** The rail, its command, and its key are gone; every scope it could apply is reachable from the switcher, which keeps `rail` as a palette alias so the old word still finds it | project switcher `p`; palette |
| `v` | `ToggleListView` | Urgency ↔ Projects view | **Retired** | palette (the contract keeps Projects view palette-accessible) |
| `R` | `ResortTabs` | Resort Kitty tabs | **Retired** | palette |
| `u` | `UndoClose` | Undo the last close | **Retired** | palette. The undo toast names `:` rather than `u`, because a toast advertising a retired key would be the only thing on screen claiming it still works |
| `?` | `ShowKeyboardHelp` | Keyboard help | Survives | `?` |
| `q` / `ctrl+c` | `Quit` | Quit the dashboard | Survives | `q`, `Ctrl+C` |

## Keys that are not commands

These stay in `handleListKey`'s own switch because they move the cursor, fold
list structure, or open a surface that offers commands rather than being one.
They have no catalog entry and no palette row, so a retiring one needed its
successor named here or it had nowhere to go.

| Key | Capability | Disposition | Successor |
| --- | --- | --- | --- |
| `j`/`k`, `↓`/`↑` | Move the cursor | Survives | same |
| `PgUp`/`PgDn` | Page the list | Survives | same, plus focused-pane paging |
| `Home`/`End` | Top/bottom | Added | same |
| `n`/`N` | Next/previous Needs you row | Added | same |
| `tab`/`shift+tab` | Switch the focused pane | Added | same |
| `ctrl+d`/`ctrl+u` | Half-page the list | **Retired** | `PgUp`/`PgDn` |
| `g`/`G` | Top/bottom | **Retired** | `Home`/`End`. Both spellings survive inside the project switcher, the health details overlay, and the detail timeline, which have their own handlers and their own scroll geometry |
| `]`/`[` | Arm the chord, then jump to the next/previous row in a state | **Retired** | `n`/`N` over the Needs you set; `/` to filter by state. The chord state itself is gone, so there is no pending-key display left to render |
| `]]`/`[[` | Jump to the next/previous attention row | **Retired** | `n`/`N` |
| `}`/`{` | Jump to an attention row **and** focus its tab | **Retired** | `n`/`N`, then `Enter` — the new keymap never opens a terminal automatically |
| `enter` on a focused chip | Remove that filter component | **Retired** | filter editing inside `/` |
| `enter`/`space` on a summary | Fold/unfold the family | `enter` survives, `space` **retired** | `Enter` |
| `a` | Selected-session actions overlay | Survives | `a` |
| `:` | Command palette | Survives | `:` |
| `←`/`→` | Fold/unfold a family; `←` focuses the rail in Urgency view | **Retired** | `Enter` folds; the rail's successor is the project switcher `p` |
| `tab` | Cycle the focused filter chip | The meaning retired, the key kept for pane switching | filter editing inside `/` |
| `1`–`9` | Remove the *n*th filter component | **Retired** | filter editing inside `/` |
| `backspace` | Remove the focused chip | **Retired** | filter editing inside `/` |

## What the successors owed, and what settled it

Everything the inventory listed as owed has been built:

- `n`/`N` attention navigation walks the filtered Needs you set, reveals a
  target inside a folded family by unfolding it, moves the cursor onto the row,
  and focuses no terminal. Finding a waiting session is not answering it, which
  is what the `needs-you-reveals-folded-target` scenario asserts by counting
  zero `FocusTab` calls.
- `Tab`/`Shift+Tab` pane switching gives "focused pane" its meaning for `j`/`k`
  and the paging keys.
- Individual chip editing and removal live inside `/`, which is why entering the
  filter editor prefills the committed filter rather than clearing it:
  backspacing one component off the end of the buffer is how a single chip is
  removed from the keyboard now. `Esc` still restores the saved filter, so an
  entry-and-abort is unchanged.
- The `i` inspector toggle absorbed both the preview pane (`p`) and the
  per-session detail overlay (`d`), and the `p` project switcher changes only
  the project filter component and preserves the rest.

One configuration key outlived its feature. `[defaults.jump].attention_states`
named the states the bracket chords treated as attention; `n`/`N` take no
configuration, because the set that counts as attention is the Needs You
grouping the list already shows. The key still loads and still validates — a
file that was valid before the upgrade is valid after it, and Agent Mux does not
rewrite the operator's config — but nothing reads it, and its presence is
reported once as a dismissible Configuration warning in the health reading.

Kitty-level shortcuts and the bindings of every other mode — launcher, history,
plans, worktrees, Runs — were out of scope and are unchanged. The launcher in
particular keeps its own full direct keymap; only the dashboard's shrank.
