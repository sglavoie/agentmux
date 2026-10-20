# Upgrading

What changes for someone coming from an older Agent Mux release. Day-to-day use
is in [Usage](usage.md).

- [Upgrading from the old dashboard shortcuts](#upgrading-from-the-old-dashboard-shortcuts)
- [One-off cleanup after upgrading from a SQLite release](#one-off-cleanup-after-upgrading-from-a-sqlite-release)

## Upgrading from the old dashboard shortcuts

The dashboard used to bind roughly thirty keys directly. It now has twelve
command bindings (`Enter`, `s`, `S`, `t`, `/`, `Esc`, `l`, `i`, `p`, `u`, `?`,
and `q`/`Ctrl+C`), plus the keys that move the cursor or open a surface (`a`,
`:`, `n`/`N`, `Tab`/`Shift+Tab`, and the paging keys). The rest of the
capabilities moved to the two surfaces the [dashboard](usage.md#dashboard)
opens, the actions menu (`a`) and the command palette (`:`); no capability was
dropped. A retired key is *unbound* rather than rebound: pressing it does
nothing at all.

Two keys kept their spelling but changed meaning, so check these first:

- `p` toggled the preview pane; it opens the project switcher now (the
  inspector, `i`, absorbed the preview).
- `Tab` cycled the focused filter chip; it switches the focused pane now
  (filter chips are edited inside `/`).

`Enter` keeps its meaning (it opens the session: attaches this terminal on
tmux, focuses the tab on Kitty), but no longer shares it with `space`, which
is retired, and no longer removes a focused filter chip.

| You used to press | Do this instead |
| --- | --- |
| `space` (focus) | `Enter` |
| `e` peek, `r` restart, `c` profile override, `C` launch another, `x` clear icon, `X` close, `o` shell, `O` editor, `y` copy title, `L` lock, `m` mute | `a`, then the same letter. The menu's mnemonics are unchanged; you can also move to the entry and press `Enter` |
| `u` undo close | `u` — it came back. What it undoes expires after 30 seconds, and a capability that has to be found by name before it lapses is not reachable. `:` → **Recover Ended Session** still works |
| `s` / `S` past sessions | `s` / `S` — both came back for browsing Past Sessions. `:` → **Search This Project's Past Sessions** / **Search All Past Sessions** still works |
| `P` plans, `W` worktrees, `A` runs | `:` → **Search Plans** / **Manage Worktrees** / **Show Runs** |
| `d` session detail, `T` timeline | `i` for the inspector, or `:` → **Show Session Details** / **Show Session Timeline** |
| `v` urgency ↔ projects | `:` → **Show Projects View** / **Show Urgency View** |
| `f` cycle profile | `:` → **Cycle Profile Filter**, or `/` for any filter component |
| `R` resort tabs | `:` → **Resort Tabs** |
| `p` preview | `i` — the inspector absorbed it. `p` is the project switcher now |
| `b` project rail | `p` — the rail is gone; the switcher applies the same scopes |
| `g` / `G` | Restored as aliases for `Home` / `End`, including the dashboard's focused queue or inspector |
| `Ctrl+d` / `Ctrl+u` | Restored half-page movement in the focused pane; `PgUp` / `PgDn` move a full page |
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
capability went to, is the [dashboard keymap inventory](dashboard-keymap-inventory.md).

## One-off cleanup after upgrading from a SQLite release

Old installations may still contain retired database artifacts. This is a
manual deployment cleanup, not a recurring migration. First build and pass
`just acceptance`, stop the installed daemon, and confirm `amx daemon status`
no longer reports it running. Preserve the caches and assets listed under
[Local storage](usage.md#local-storage), then remove only these entries from
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
