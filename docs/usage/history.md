# Past sessions

[Back to usage](../usage.md)

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

For bounded CLI/JSON reads, `amx session list` and `amx session search` accept
`--offset N --limit N`. An explicit offset keeps search results in stable archive
order (most recent first, with deterministic ties), including the current
conversation. `--conversation harness:session-id` matches one exact conversation
before reading transcript text; project filters also run before text scanning.
JSON results include `total`, `offset`, `has_more`, and `hidden_automated`; search
also returns parsed `match_terms` so clients highlight phrases consistently.
Offsets describe the current index, not a frozen snapshot: clients should deduplicate
identities across pages and refresh when an actively changing archive needs a fresh
ordering. Existing commands without pagination still return all JSON rows.

```sh
amx session search --json --offset 0 --limit 50 -- '"race condition"'
amx session list --json --conversation claude:EXACT_SESSION_ID
```

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
