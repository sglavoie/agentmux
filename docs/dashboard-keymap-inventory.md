# Dashboard keymap inventory

Every key the dashboard (`ModeList`) answers today, and where its capability
goes under the replacement keymap.

This is an inventory, not a change. The bindings listed as **retiring** still
work exactly as before; the table in `tui/dashboard_bindings.go` is what will
be edited to retire them, and it is the only place a retirement has to be
written down. The point of the inventory is that no capability is dropped by
accident: a key can only retire once the row naming it says which surviving
surface still reaches it.

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
| `:` | Command palette |
| `?` | Help |
| `q`, `Ctrl+C` | Quit dashboard |
| `Esc` | Close overlay, then leave full-screen/inspector focus, then clear filters |

Three of these keys survive their own spelling but change meaning: `p` moves
from the preview toggle to the project switcher, `Tab` from filter-chip cycling
to pane switching, and `Enter` keeps focusing but no longer shares the job with
`space`.

## Command-backed bindings

Each row is one entry in `dashboardBindings()`. "Successor" names the surface
that still reaches the capability once the key is gone. A palette entry exists
today for every command in this table — the catalog is the complete list — so
"palette" never means "to be built"; where something more direct is planned,
it is named first.

| Key | Command ID | Capability | Disposition | Successor |
| --- | --- | --- | --- | --- |
| `enter` / `space` | `FocusSession` | Focus the session's Kitty tab | `enter` survives, `space` retires | `Enter`; actions `a` → focus tab; palette |
| `e` | `PeekScrollback` | Peek at the pane's scrollback | Retires | actions `a`; palette |
| `r` | `RestartSession` | Restart the harness in place (confirmed) | Retires | actions `a`; palette |
| `c` | `OverrideProfile` | Relaunch under a different profile | Retires | actions `a`; palette |
| `C` | `LaunchAnother` | Launch another session in the same project | Retires | actions `a`; palette |
| `x` | `ClearStatusIcon` | Acknowledge the status icon | Retires | actions `a`; palette |
| `X` | `CloseSession` | Close the session (confirmed, undoable) | Retires | actions `a`; palette |
| `o` | `OpenDirectoryID` | Open a shell in the session's directory | Retires | actions `a`; palette |
| `O` | `OpenEditorID` | Open the configured editor there | Retires | actions `a`; palette |
| `y` | `CopyTitleID` | Copy the tab title | Retires | actions `a`; palette |
| `L` | `ToggleLockID` | Lock/unlock the session | Retires | actions `a`; palette |
| `m` | `ToggleMuteID` | Mute/unmute notifications | Retires | actions `a`; palette |
| `s` | `SearchProject` | Past Sessions scoped to this project | Retires | palette only — the actions overlay has no entry for it |
| `d` | `DismissDeprecation` | Dismiss the config deprecation banner | Retires | palette |
| `d` | `ShowDetails` | Per-session event detail overlay | Retires | inspector `i`; palette |
| `T` | `ShowTimeline` | Per-session phase timeline | Retires | palette |
| `/` | `FilterSessions` | Edit the filter | Survives | `/` |
| `f` | `CycleProfileFilter` | Cycle the profile filter component | Retires | project switcher `p` for the project component; `/` for the rest; palette |
| `esc` | `ClearFilters` | Clear all filter components | Survives, as the last rung of the Escape ladder | `Esc` |
| `l` | `OpenLauncher` | Open the launcher | Survives | `l` |
| `S` | `SearchAllSessions` | Past Sessions across all projects | Retires | palette |
| `P` | `SearchPlans` | Plans browser | Retires | palette |
| `W` | `ManageWorktrees` | Managed worktrees | Retires | palette |
| `A` | `ShowRuns` | ASL Runs | Retires | palette |
| `p` | `TogglePreview` | Toggle the preview pane | Retires the meaning, keeps the key for the project switcher | inspector `i`; palette |
| `b` | `ToggleRail` | Toggle the project rail | Retires | project switcher `p`; palette |
| `v` | `ToggleListView` | Urgency ↔ Projects view | Retires | palette (the contract keeps Projects view palette-accessible) |
| `R` | `ResortTabs` | Resort Kitty tabs | Retires | palette |
| `u` | `UndoClose` | Undo the last close | Retires | palette |
| `?` | `ShowKeyboardHelp` | Keyboard help | Survives | `?` |
| `q` / `ctrl+c` | `Quit` | Quit the dashboard | Survives | `q`, `Ctrl+C` |

## Keys that are not commands

These stay in `handleListKey`'s own switch because they move the cursor, fold
list structure, or open a surface that offers commands rather than being one.
They have no catalog entry and no palette row, so a retiring one needs its
successor named here or it has nowhere to go.

| Key | Capability | Disposition | Successor |
| --- | --- | --- | --- |
| `j`/`k`, `↓`/`↑` | Move the cursor | Survives | same |
| `PgUp`/`PgDn` | Page the list | Survives | same, plus focused-pane paging |
| `ctrl+d`/`ctrl+u` | Half-page the list | Retires | `PgUp`/`PgDn` |
| `g`/`G` | Top/bottom | Retires | `Home`/`End` |
| `]`/`[` | Arm the chord, then jump to the next/previous row in a state | Retires | `n`/`N` over the Needs you set; `/` to filter by state |
| `]]`/`[[` | Jump to the next/previous attention row | Retires | `n`/`N` |
| `}`/`{` | Jump to an attention row **and** focus its tab | Retires | `n`/`N`, then `Enter` — the new keymap never opens a terminal automatically |
| `enter` on a focused chip | Remove that filter component | Retires | filter editing inside `/` |
| `enter`/`space` on a summary | Fold/unfold the family | `enter` survives, `space` retires | `Enter` |
| `a` | Selected-session actions overlay | Survives | `a` |
| `:` | Command palette | Survives | `:` |
| `←`/`→` | Fold/unfold a family; `←` focuses the rail in Urgency view | Retires | `Enter` folds; the rail's successor is the project switcher `p` |
| `tab` | Cycle the focused filter chip | Retires the meaning, keeps the key for pane switching | filter editing inside `/` |
| `1`–`9` | Remove the *n*th filter component | Retires | filter editing inside `/` |
| `backspace` | Remove the focused chip | Retires | filter editing inside `/` |

## What the successors still owe

Five capabilities above point at a surface that does not exist yet. Each is a
later session's work; listed here so the retirement and the replacement are
read together rather than one landing without the other.

- `n`/`N` attention navigation, over the filtered Needs you set, revealing a
  target inside a folded family and never focusing its terminal.
- `Tab`/`Shift+Tab` pane switching, which is what makes "focused pane" mean
  something for `j`/`k` and the paging keys.
- `p` project switcher, changing only the project filter component and
  preserving the rest.
- `i` inspector toggle, which absorbs both the preview pane (`p`) and the
  per-session detail overlay (`d`).
- Individual chip editing and removal inside `/`, which is where `tab`, `1`–`9`
  and `backspace` go.

Kitty-level shortcuts and the bindings of every other mode — launcher, history,
plans, worktrees, Runs — are out of scope and unchanged.
