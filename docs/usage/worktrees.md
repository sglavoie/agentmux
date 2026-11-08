# Managed worktrees

[Back to usage](../usage.md)

Run **Manage Worktrees** from the command palette (`:`) to open the
Git-authoritative inventory under
`~/.local/share/agent-mux/worktrees`. `/` filters by stable ID, project,
branch, path, profile, harness, and lifecycle labels. `j`/`k`, `g`/`G`, and
`Ctrl+d/u/f/b` navigate; `Y` copies the canonical checkout path, `o` opens a
shell, `O` opens the configured editor, and `r` refreshes Git and activity
facts.

`Enter` smart-reopens the selected worktree. A sole live tmux session is
opened. Multiple live sessions open a picker that identifies the project,
profile, harness, candidate ID, and native harness session ID
when known. With no live session, Agent Mux resumes the newest indexed
conversation inside the checkout or launches a fresh session. If recorded
profile provenance is no longer usable, the normal profile picker requests an
explicit replacement; it does not silently choose another identity.

`v` pages a review of the selected worktree: what it changed since it left
its source branch, file by file with the full diff, and how its committed
work would land. It reads and never writes, so it is offered for a worktree
with a live session too.

`L` lands the selected worktree after a confirmation: the recorded source
branch is fast-forwarded to the worktree branch, and nothing else. The
worktree and its branch are kept, and since the branch is then merged, `D`
will remove both. Landing is refused, with the reason, for a worktree that is
dirty, locked, detached, or in use, when there is nothing to land, when the
source branch's own checkout has uncommitted changes or an unfinished Git
operation, and when the source branch has moved on — rebase the worktree
branch onto it and land again.
Agent Mux never merges, squashes, or resolves a conflict.

Cleanup is always explicit:

- `d` removes a clean, inactive checkout and its sidecar while retaining the
  branch.
- `D` additionally deletes the worktree branch, but only after fresh Git checks
  prove it is merged into the source branch recorded when the worktree was
  created. Git verifies both branch tips atomically when deleting: if either
  moved after the checks, the branch and recovery metadata remain. Review the
  branches before retrying; the checkout may already have been removed.
- `F` forgets retained metadata for a missing checkout or invalid metadata. It
  changes neither a checkout nor a Git branch.

The TUI shows refusal reasons known from its latest snapshot before opening a
confirmation, then the shared action performs fresh activity, dirtiness, lock,
registration, metadata, ref, and ancestry checks immediately before mutation.
Agent Mux never force-removes a worktree, deletes an unmerged branch, stops an
active loop, or acts on a worktree outside its managed
root. Partial cleanup is reported with the durable steps that succeeded; the
inventory refreshes and retains the recovery row instead of presenting the
operation as all-or-nothing.

Dirtiness checks include untracked files and submodule changes even when Git
configuration hides them from ordinary `git status` output.

The same lifecycle is scriptable:

```bash
amx worktree list [--json]
amx worktree show <selector> [--json]
amx worktree diff <selector> [--committed] [--patch] [--json]
amx worktree land <selector> --yes [--remove] [--json]
amx worktree reopen <selector> [--live-session <candidate>] [--profile <name>] [--backend tmux] [--json]
amx worktree shell <selector> [--backend tmux] [--json]
amx worktree edit <selector> [--backend tmux] [--json]
amx worktree remove <selector> --yes [--delete-branch] [--json]
amx worktree forget <selector> --yes [--json]
amx worktree prune [--merged] [--yes] [--delete-branch] [--json]
```

The worktree commands use tmux. `reopen` launches on the private server, what runs there is what counts as a live session in a
worktree, and `shell` and `edit` open a workspace view when Agent Mux.app
hosts the terminal and otherwise run the shell or the editor in the terminal
the command was typed in. With `--json` they run nothing there and report
`"continuation": "here"` with the path, for the caller to open.

`list` and `show` count a worktree's live tmux sessions. `land` and `remove`
refuse while the live count cannot be determined.

`prune` selects every worktree `remove` would accept on its own — present,
valid, clean, unlocked, attached to a branch, and used by no live session or
loop — and with `--merged` only those whose branch is reachable from its source.
Without `--yes` it lists them and changes nothing. With `--yes` each one goes through
`remove`, which re-checks everything at that moment, so a worktree that became
active in the meantime is kept and reported. Branches are kept unless
`--delete-branch` is given, and even then only merged branches are deleted.
A branch checked out, being rebased, or used by an active bisect in another
worktree is kept. Unreadable Git operation state also prevents branch deletion.

Removal refuses local files marked `assume-unchanged` or `skip-worktree`, since
Git status can hide edits to them and Git would silently delete those edits.
Clear the index flags and inspect the files before retrying. Absent paths
excluded by sparse checkout are allowed. This check also applies to cleanup
after a failed launch.

Removal also refuses a detached HEAD, even without `--delete-branch`, so it
preserves commits with no branch reference and paused rebases. Finish or abort
the rebase, or attach HEAD to a branch that preserves the work, before retrying.
The structured refusal code is `worktree_detached`.
An attached checkout with an unfinished merge, cherry-pick, revert, rebase,
`git am`, or bisect is also kept, even when its files are clean. Finish or
abort that operation before retrying; the refusal code is `worktree_busy`.
Landing uses the same check on the managed checkout.

`diff` reviews what a worktree did since it left its recorded source branch:
commits ahead and behind, each changed file with its line counts, files Git
does not track yet, and a verdict on how the committed work would land —
`nothing_to_land`, `fast_forward`, `diverged_clean`, `conflicts` (naming the
files), or `unknown` when the merge could not be predicted. The comparison
starts at the merge base, so what the source branch gained meanwhile is not
counted as the worktree's change. Uncommitted changes are included unless
`--committed` is given; `--patch` appends the unified diff. External diff drivers
and text converters are disabled. It reads and never
writes — the merge is predicted in Git's object database, not in a checkout —
so it needs no `--yes` and is safe on a worktree with a live session in it. A
worktree with no recorded source branch (a legacy checkout) cannot be reviewed.

`land` fast-forwards the recorded source branch to the worktree branch under
the same lock and with the same fresh checks as `remove`, and never forces.
Where the source branch is checked out, that checkout moves with it and must
have no uncommitted changes to tracked files. Landing refuses if that checkout
has switched branches or detached HEAD during preflight. It also refuses to overwrite
untracked files, including ignored files. It uses the commit validated during
preflight and fast-forwards even when the source branch has a Git squash merge
preference. Where no worktree has the source branch checked out, only the branch
is advanced. A `diff` verdict of `fast_forward` is the one
that lands; `diverged_clean` and `conflicts` are refused as `branch_diverged`.
The other refusals are `nothing_to_land`, `worktree_dirty`, `worktree_locked`,
`worktree_detached`, `worktree_active`, `source_dirty`, `source_busy` (the
source branch is mid-rebase, in an active bisect, or its checkout has another
unfinished Git operation), and `source_branch_missing`.
Unreadable Git operation state prevents landing. `--remove` goes on
to remove the worktree and delete its now-merged branch; if that second step
is refused, the landing stands and the command reports a partial result with
a nonzero exit.

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
If cleanup after a failed launch cannot remove the checkout or branch, its
sidecar is retained for recovery. New worktrees also avoid names with retained
sidecars, so creation cannot overwrite an earlier worktree's provenance.
