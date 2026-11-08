# Plans and the ASL cockpit

[Back to usage](../usage.md)

## The agent-session-loop cockpit

**Show Runs** in the command palette opens the runs view: every active or
actionable `agent-session-loop` run, newest need first. It is an *intervention* surface, not a loop runner. The cockpit
never starts, schedules, or tails a loop — start runs with `agent-session-loop`
directly, or from Plans, which launches an agent to supervise one
([Starting a run of a plan](#starting-a-run-of-a-plan)), and use the cockpit to
answer and steer the ones already in flight.

Inside the runs view:

- `j`/`k`, `g`/`G`, `Ctrl+d`/`Ctrl+u`, `Ctrl+f`/`Ctrl+b`: move the cursor
- `r`: open the response form for the selected run
- `s`: stop the run after the current session, after confirmation
- `X`: abort the run, after confirmation (`a` opens actions elsewhere and does nothing here)
- `w`: open the run's Workbench page in your browser
- `Enter` or `f`: focus or attach the session bound to the run
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
## Plans and local storage

Run **Search Plans** from the command palette (`:`) to search saved plan
directories. Agent Mux loads warm rows from the
filesystem cache first and refreshes the configured `plans_directory` in the
background. A missing or malformed cache is quarantined and rebuilt from the
plan directories; an unreadable plan never blanks rows that were read
successfully. Each row and viewer header shows the associated configured
project title. Press `r` to filter by the projects represented in the index or
by `(unknown)`; equally specific configuration aliases are separate filter
choices and match the same plans. `/` searches, `o` cycles the result sort
between match count and recency, `O` (or `e`) opens
the selected plan directory in your editor, `Y` copies the plan directory, and `enter` opens the viewer. The viewer
supports `j`/`k`, `g`/`G`, `Ctrl+d/u/f/b`, `n`/`N` to move between matches,
`/` to refine the query, `y` to copy the selected raw line, `Y` to copy the plan
directory, and `O` (or `e`) to open the plan directory in your editor. `esc` or
`q` returns to the plan list. On the list, `q` leaves Plans in one step; `esc`
first clears an active query and leaves Plans only when there is none.

### Starting a run of a plan

`R` on a plan opens the run form, and `amx plans run PLAN` is the same thing
from a shell. Either one starts an `agent-session-loop` run by launching an
ordinary managed session whose first prompt is agent-session-loop's own
supervision skill (`/asl asl run PLAN_DIR` for Claude and Copilot, `$asl asl
run PLAN_DIR` for Codex; Agent Mux reads the spelling from `asl
supervisor-guide`). The run is supervised by an agent you can attach to, and
it appears in the dashboard, the overview, and the runs view like any other.
Agent Mux itself still runs no loop.

The plan is checked before anything launches, and the check is
agent-session-loop's, not Agent Mux's:

- `asl preflight` has to pass. A finding that is not green, red or yellow,
  refuses the run, and each one is shown as agent-session-loop worded it. A
  plan that was reviewed but never marked loop-ready is the usual one;
  preparing a plan stays with `asl prepare` and `asl preflight -m`.
- A plan whose run holds agent-session-loop's lock is refused as running, and
  one with no session left is refused as complete.

The form has five rows, each a choice among a few values (`j`/`k` or `tab`
moves between rows, `h`/`l` or `space` changes one, `enter` checks the plan
and starts the run, `esc` closes):

| Row | What it chooses |
| --- | --- |
| Project | the configured project the session launches from. The projects whose directory covers the plan's working directory are offered; a plan no project covers offers every project. |
| Supervisor | the profile the supervising agent runs under. It follows the project and can be changed on its own. |
| Harness | `asl run --harness`: what implements the plan's sessions. `asl default` passes nothing. |
| Review harness | `asl run --review-harness`: a final review by a different harness. `none` passes nothing. |
| Commit | `--commit-per-session` or `--commit-at-end`. `asl default` passes nothing. |

The supervisor and the harness are independent: the first is the agent
watching the run, the second is what the run launches for each session. The
line under the rows is the `asl run` command the choices amount to. The
session starts in the plan's own working directory, since that is where `asl
run` reads its workspace from. A plan that is refused keeps the form open on
its findings, and changing a row clears them.

```bash
amx plans run 2e774ec64570 --check          # readiness only; starts nothing and needs no backend
amx plans run 2e774ec64570 --harness codex --review-harness claude --commit-per-session
amx plans run ~/.local/share/agent-plans/2e774ec64570-1a09b1daef1c --project api --profile claude-work --backend tmux
```

`PLAN` is a plan directory, or a name under `plans_directory`: the exact one,
or the start of it when only one plan begins that way. `--project` takes a
title, or the `profile/title` identifier an ambiguity lists, and is needed
only when the plan's working directory names no project or several; `--check`
answers without one. Under `--json` a started run and a check are the usual
data envelope (`started`, `readiness`, `command`, `project`, `profile`,
`directory`, and for a started run `backend` and `runtime_session_id`). A plan
that may not be started is the same envelope with `started: false`, a
`readiness.state` of `not_ready`, `running`, or `complete`, its
`readiness.findings`, and a quiet nonzero exit: that is a decision about the
plan, not a failure to decide. Everything else (an unknown plan, an ambiguous
project, an agent-session-loop that did not answer) is the error envelope.

`amx plans status PLAN` asks agent-session-loop (`asl status`) how far a split
plan has got, and reads `PLAN` the same way. A terminal gets the plan's title,
its complete and pending session counts, and the run working on it; `--json`
carries the whole status, including each session's recorded verification.
With agent-session-loop disabled in `[defaults.asl]` it is refused as
`asl_disabled`, and an agent-session-loop that does not answer within 20
seconds, or answers with no session progress, is `asl_status_failed`.

```bash
amx plans status 2e774ec64570
amx plans status ~/.local/share/agent-plans/2e774ec64570-1a09b1daef1c --json
```

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

### Local storage

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

Upgrading from a release that used SQLite? The one-off cleanup of its leftover files is in
[Upgrading](../upgrading.md#one-off-cleanup-after-upgrading-from-a-sqlite-release).
