# Usage

## Dashboard views and continuity

The default TUI opens on the live-session dashboard and selects the session
from the Kitty tab that opened it when possible. `/` filters the live-session
list; `S` opens Past Sessions and `P` opens Plans.

The same workflows are scriptable. Machine-readable commands use one
`{"schema_version":1,"data":...}` success envelope; failures use a structured
`error` envelope and ambiguous selectors return candidates instead of guessing.

```bash
amx session list --json
amx session continue codex <session-id>
amx session fork claude <session-id>
amx session fork-worktree codex <session-id> --yes
amx session handoff claude:<session-id> --profile codex-work

```

`session continue` focuses the exact live Kitty owner when one exists and
otherwise resumes the closed conversation. Native forks are used for Claude
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

Key flows:

- `:`: open the command palette from the main dashboard list; type to search,
  use `↑`/`↓`, `Ctrl+j`/`Ctrl+k`, or `Ctrl+n`/`Ctrl+p` to move, `Backspace`
  to edit, `Enter` to run, `Esc` to close, and `Ctrl+c` to quit
- `f`: cycle visible sessions through each currently running profile and back
  to all profiles
- `l`: open the launcher
- `r`: restart the selected running session, after confirmation
- `c`: restart the selected running session under another profile
- `C`: launch a new session from the selected row, with profile selection first
- `s`: search past session transcripts for the selected project
- `x`: clear the selected session's status icon
- `X`: close the selected running session
- `o`: open the selected project's directory in a plain shell tab
- `p`: toggle the preview pane
- `d`: show the per-session hook event log (scrollable detail pane)
- `S`: search past session transcripts across every configured profile
- `P`: search saved agent plans; rows and the viewer show the configured
  project title, `r` filters by project (including `Unknown`), `Y` copies the
  full plan path, and the viewer wraps long lines, supports less-style
  `Ctrl+d/u/f/b` paging, and `y` copies the selected raw line
- `?`: show the full keymap

The dashboard profile cycle is an `@profile` filter: it follows
case-insensitive alphabetical profile order, preserves any active word,
harness, and state filters, and includes profiles represented by current
running sessions. The active profile appears as the existing filter chip;
wrapping to **All** removes only that chip.

The command palette searches command titles, descriptions, aliases, and shown
shortcuts with case-insensitive, whitespace-separated AND terms. Disabled
commands remain visible with an inline reason and can be selected, but `Enter`
does nothing on them. Its shortcut labels are references: printable keys edit
the query while the palette is open. All existing dashboard shortcuts remain
available directly from the main list, and `:` does not open the palette from
other modes.

Rows marked with `🔁` have at least one active `agent-session-loop` run whose
workspace matches the project row. Loop matching canonicalizes the workspace
path, prefers the same harness when a matching managed Kitty tab or row is open,
and falls back to legacy CWD-only matching for older loop state or unknown
harnesses. Agent Mux also mirrors `🔁` into the Kitty project tab that owns the
loop while it is active, preserving existing state, star, and mute icons.
Finished runs stop counting as active when loop discovery sees
`run_finished_at` in `state.json`, and the `AgentSessionLoopFinished` hook
clears active-class icons while reconciling the tab-title `🔁` marker.
Run and tail loops outside the dashboard with `agent-session-loop` directly.

The dashboard keeps the profile badge and harness metadata separate from the
title column, so visible titles are just the project name.

A sticky status row occupies the final terminal line for health, active mode
summaries, and transient action messages; keybinding hints stay immediately
above it.

The detail pane's event history persists through terminal hooks (`Stop`,
`SessionEnd`, etc.) and through the watchdog's stale sweep, so you can still
review what a session did after its icon clears. History is held in memory per
Kitty window and capped at 5 000 events (oldest dropped); it is released only
when the window closes or the daemon restarts.

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
Dashboard `C` uses the same profile picker, but launches from the highlighted
running row instead of from the launcher list. Pressing `enter` in the launcher
is an explicit request for new tab(s): if the selected project/profile is
already running, Agent Mux opens another instance instead of skipping or
focusing the existing tab.

## Managed worktrees

Press `W` from the dashboard, or run **Manage Worktrees** from the command
palette, to open the Git-authoritative inventory under
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
```

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

Press `S` from the dashboard to search past sessions across every configured
profile and project. Press `s` from the dashboard or launcher to open the same
view filtered to the highlighted project. Agent Mux warms the filesystem cache
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
  demand and show the per-session match count.
- `h`, `p`, and `r` open single-value chip pickers for harness, profile,
  and project.
- `i` copies the selected session ID, and `Y` copies its transcript path.
- `enter` opens the transcript viewer; `n`/`N` move between matches,
  `j`/`k`, `g`/`G`, and `Ctrl+d/u/f/b` scroll, `y` copies the selected
  message, and `Y` copies the transcript path. The viewer also accepts the
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
amx transcripts rebuild --all
```

`status` is read-only and reports metadata, sidecar, missing-sidecar, and
orphan-sidecar usage. `compact` takes the transcript cache lock, migrates any
legacy inline search text, persists metadata, and then removes only regular
hash-named sidecars that are no longer referenced by a cache key. It preserves
referenced sidecars, transcript source files, temporary files, symlinks,
directories, and unrelated artifacts. `rebuild --all` is the complete reset:
it reparses every discoverable source transcript and recreates the entire
disposable sidecar store. Normal refreshes never compact orphan sidecars.

Retention is controlled by `[defaults.session_archive] keep_days` (default
30). Refresh and prune operations hide cache entries older than `keep_days`;
the on-disk transcript files remain intact unless explicitly deleted. Each
refresh only walks files modified within `scan_max_age_days` days (default:
inherits `keep_days`). Set it to `-1` for an unbounded walk.

## Plans and local storage

Press `P` to search saved plan directories. Agent Mux loads warm rows from the
filesystem cache first and refreshes the configured `plans_directory` in the
background. A missing or malformed cache is quarantined and rebuilt from the
plan directories; an unreadable plan never blanks rows that were read
successfully. Each row and viewer header shows the associated configured
project title. Press `r` to filter by the projects represented in the index or
by `Unknown`; equally specific configuration aliases are separate filter
choices and match the same plans. `/` searches, `o` cycles the result sort
between match count and recency, `e` opens the selected plan directory in your
editor, `Y` copies the plan directory, and `enter` opens the viewer. The viewer
supports `j`/`k`, `g`/`G`, `Ctrl+d/u/f/b`, `y` to copy the selected raw line,
and `e` to open the plan directory in your editor.

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

Install atomically with `just install`, restart with `amx daemon start`, and
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
| `!state` | state match: `active`, `idle`, `bolt`, `key`, `stop`, `warning`, `error`, `subagent`, `compact`, `question` |
| `\@foo`, `\!x` | literal leading `@` or `!` |
