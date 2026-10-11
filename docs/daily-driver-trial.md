# Daily-driver trial and transition

The last step of the desktop migration ([backend-migration.md](backend-migration.md))
is not a test run. It is at least five normal workdays of the operator's own
work on the desktop workflow, with the defects it turns up fixed, and only then
the move of every future launch to it. This guide is what the operator does
for that: install the candidate, switch, work, write down what happened, and
roll back if it goes wrong.

Nothing in this guide can be done by a test run or by an agent on the
operator's behalf. The record at
`docs/acceptance/ghostty-desktop/session-20.json` stays `pending` until the
operator has done it and said so.

## What is being tried

| | |
| --- | --- |
| Candidate | the checkout at `911ce009f3d0` or later: `amx` 0.8.0 and Agent Mux.app 0.1.0 built from it by `just mac-app Release` |
| Signing | ad-hoc (`this-mac-only`, see [desktop-releases.md](desktop-releases.md)); no Developer ID signature or notarization has been observed |
| Installed before the trial | `amx` 0.8.0 revision `5645f028f120` and the Agent Mux.app of that revision: this is what a full rollback returns to |
| Rehearsal | the transition and the rollback below, with stand-in agents in a disposable world on another Mac: `transition-rollback.log` and `rollback-step4.log` under `docs/acceptance/ghostty-desktop/session-20/rehearsal/` |

The rehearsal shows that the instructions work. It is not the trial and
covers none of its days.

## Install

From the checkout, at the candidate commit, in a terminal of your own (these
build with Xcode and restart your daemon; no gate and no test run does them
for you):

```sh
git status --short        # expect nothing: install what is committed
just update               # amx, the Kitty snippet, hooks, Agent Mux.app, daemon restart, strict doctor
amx --version             # the candidate's revision
amx backend status        # the tmux in use and where it came from
amx doctor --strict
```

`just update` installs the app over the one in `/Applications`
(`macos/scripts/install-app.sh`): the old bundle is moved aside and the new
one renamed into place, and the installer exits nonzero if a session server
that was running is not the same one afterwards. Kitty sessions are not
touched by any of it; their hooks reach the restarted daemon as before.

Before installing, write down the revision you are leaving
(`amx --version`), so the rollback has a name to go back to.

## Transition

Two switches move new launches to the desktop workflow. Neither touches a
session that is already running.

1. `amx` and the dashboard: in `~/.config/agent-mux/config.toml`

   ```toml
   [defaults]
   launch_backend = "tmux"
   ```

   then `amx config validate` and `amx backend status --check`.

2. Agent Mux.app: Preferences, Workspace: set **Launch projects in** to
   **The workspace (tmux)** and turn on **Open the workspace at launch**.

From then on a launch with no `--backend` starts an agent on the private tmux
server and the workspace shows it. Sessions already in Kitty tabs stay where
they are, keep reporting to the daemon, and end when you end them: let them
finish in place. Nothing migrates a running Kitty session, and nothing needs
to.

Kitty stays a supported backend throughout. `--backend kitty` on any launch
command, or `amx` run inside Kitty without `launch_backend = "tmux"`, launches
a Kitty tab exactly as before.

During the trial, hold the switch for the days you count: a day worked mostly
in Kitty is not a day of the trial.

## What to cover

Five or more distinct workdays. Across them, each of these has to actually
happen in real work, not be staged once:

| Case (key in the record) | What counts |
| --- | --- |
| `multiple-profiles` | agents under at least two configured profiles at once, each in its own profile home, attention and conversations not crossing over |
| `all-harnesses` | Claude, Codex, and Copilot each launched, worked with, and resumed in the workspace |
| `permission-prompts` | permission and question prompts raised, seen (sidebar, notification), answered from the embedded terminal, and cleared |
| `long-running-jobs` | agents left working for hours, with the app closed and reopened and the Mac asleep in between, found again with their terminal state |
| `asl` | at least one real `asl run` supervised from the workspace: lifecycle, a mailbox question, its reply |
| `overview` | the overview used to watch several agents, previews current, focus from a tile |
| `conversation-continuity` | continue, fork, and handoff from Past Sessions; a conversation resumed after its agent ended |
| `affected-acceptance-rerun` | every defect fixed during the trial has the acceptance checks it touches run again, or the trial found no defect and says so |
| `rollback` | the rollback below carried out on this installation, or its rehearsal accepted by the operator as sufficient with the reason written down |
| `kitty-fallback` | a Kitty launch made on purpose during the trial with tmux as the default, and Kitty sessions from before the transition finished in place |

Four kinds of defect block the transition whatever else passes: an agent
process lost, an event routed to the wrong session, a session that does not
come back after the app, the daemon, or a terminal goes away, and input an
agent needs that cannot be typed.

## Keeping the log

One entry per workday in
`docs/acceptance/ghostty-desktop/session-20/trial-log.md`, written that day:
the date, what was used, and every point of friction or failure, with what was
on screen and, for a failure, a debug bundle (`amx debug bundle`) or the
relevant `amx daemon export-events` output saved beside the log. A day with
nothing to report is still an entry.

For each defect: fix it, name the commit, run the checks it touches again
(`just check`, and whichever of `just crash-restart`, `just reliability-stress`,
`just asl-integration`, `just mac-test`, or a live driver under
`docs/acceptance/ghostty-desktop/` covers the behaviour), install the fixed
build with `just update`, and note in the log which day the fix went in. A fix
to process survival, routing, recovery, or input restarts the count of days
for the cases it touches.

## Rollback

The steps are independent and in order of how much they undo. Stop at the one
that is enough. Each was rehearsed; what the rehearsals did and did not show
is at the end of this section.

1. **Launch in Kitty again.** Remove `launch_backend = "tmux"` from
   `config.toml` (or set it to `"kitty"`) and set the app's **Launch projects
   in** to **Kitty**. New launches open Kitty tabs. Agents already on the tmux server
   keep running, stay listed, and are reached with `amx attach
   <runtime-session-id>` or from the dashboard.

2. **Remove the app.** `just mac-uninstall` quits and removes the bundle and
   nothing else. It reports the agents it left running; any `amx` still
   attaches to them, and the tmux backend still works from a terminal
   (`--backend tmux`) with the runtime the app installed.

3. **End the tmux agents.** `amx backend stop --yes` ends the private server
   and every agent in it. It is the only step that ends agents; their
   conversations stay on disk and can be resumed in Kitty. Kitty sessions are
   not affected.

4. **Go back to the earlier build.** In a checkout of the revision written
   down before installing, inside Kitty: `just update` (or `just
   install-restart-smoke` for `amx` alone). Do step 1 first: an `amx` older
   than the tmux backend still starts with `launch_backend` in `config.toml`,
   but warns about it as an unknown key on every validation (`--strict`
   included, which still passes).

   What the earlier build does and does not do, as rehearsed with revision
   `5645f028f120`:

   - It launches into Kitty tabs, its daemon takes hooks from Kitty sessions
     launched before the rollback, it lists the indexed conversations, and its
     app (the menu bar companion, with no workspace and no tmux) connects to
     its daemon.
   - It leaves `config.toml`, transcripts, handoffs, worktrees, plans, and
     `launch_overrides.json` byte for byte as they were.
   - It does not know the tmux backend at all: no `--backend` flag, no `amx
     attach`, no `amx backend`. The runtime-session registry, the runtime
     checkpoint, and the installed tmux runtime under
     `~/.local/share/agent-mux` are left exactly as the candidate's daemon
     wrote them when it stopped.
   - Agents still on the tmux server keep running through the downgrade, and
     the earlier build cannot list, show, or end them. End them first (step
     3), or end them afterwards with the tmux command `just mac-uninstall`
     prints in step 2; or leave them, and they are found live again, the same
     processes, when the candidate is installed again.
   - Its installer replaces the app in place (`rm` then copy) and quits any
     running copy; it knows nothing about a session server and does not check
     one.

`just mac-uninstall --end-sessions --remove-app-data --yes` does 2 and 3 and
also removes the app's preferences and its installed tmux runtime.

The rehearsal did steps 1 to 3 on the candidate build
(`session-20/rehearsal/transition-rollback.log`) and step 4 separately, with
a tmux agent deliberately left running across it, and then went forward to
the candidate again (`session-20/rehearsal/rollback-step4.log`). In step 4
the earlier `amx` was put in place and the daemon restarted by hand, as `just
install` and `amx daemon restart` do; `just update` itself was not run, and
its closing `amx doctor --strict` was not judged, because the disposable
world has no hooks installed, no notifier, and no agent-session-loop. After a
real rollback, expect to run `amx config hooks-sync` with the earlier `amx`
if doctor reports the hooks as drifted: that was not observed either way.

## Closing the trial

When the days are done and no blocking defect is open, the record is filled
in from the log, by or with the operator:

- `status`: `passed`;
- `artifact_revision`: the revision used on the last day, and each earlier
  one if fixes went in;
- `workdays`: the ISO dates of the days counted, five or more, none in the
  future;
- `cases`: each key above with `status` `passed` and `evidence` saying what
  was observed and where it is written down (the log entry, a bundle, a
  rerun's log);
- `unresolved_blockers`: `[]`;
- `operator_confirmed`: `true`, set only on the operator's explicit word.

A case that did not happen stays `pending`, and so does the record. After the
record passes, the two switches under Transition stay as they are: that is the
transition. Kitty remains installed and supported as the fallback.
