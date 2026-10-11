# Session 19 acceptance driver

The scripts that produced the logs in `../live/`. They are started on one Mac
(the Studio) and act only on another (the MacBook Pro): Kitty, Agent Mux.app,
`amx`, the daemon, the tmux server, and the agents all run there. The Studio
shows nothing and is sent no input.

`../packaged/install-acceptance.log` is not from this driver: it is the
Session 17 driver (`../../session-17/driver`), run again unchanged against
this session's Release bundle.

## The world

`setup.sh` builds `/tmp/amxs19` on the MacBook around the packaged app. The
Release bundle `just mac-app Release` left in the checkout copy is installed
by the repository's own `install-app.sh` into `/tmp/amxs19/Applications`, and
from then on:

| | |
| --- | --- |
| `amx` | the one inside the installed bundle, reached through a wrapper in the world's `bin`. There is no other `amx` in the world until the cockpit rehearsal installs one. |
| tmux | the one the bundle brings, verified and copied by `amx` to the world's `~/.local/share/agent-mux/runtime/`. No tmux is on `PATH`, and the MacBook has none installed. |
| Kitty | `/Applications/kitty.app` of the MacBook (0.48.2), started as a separate process with `--config NONE`, its own `--listen-on` socket, and its own instance group |
| `HOME`, `TMPDIR`, `PATH` | `/tmp/amxs19/home`, `/tmp/amxs19/tmp`, and the world's `bin` plus the system directories. No Homebrew. |
| the agents | `standin-claude.sh`, installed as the world's `claude`: no model and no network. It sends real hooks through `amx hook-indicator` and reads `ask`, `work`, and `quit` from its terminal. |
| the editor | `standin-editor.sh` |

The MacBook user's own `~/.local/bin/amx`, AgentMux directories, Kitty
configuration, and app preferences are listed at setup (`realstate`) and
compared at teardown.

Kitty and libghostty both start a shell through `login(1)`, which resets
`HOME` to the account's real one. Kitty's tabs are put back in the world by a
private `ZDOTDIR` (`zshenv`); the app's terminals by `amx-shim.sh`, set as the
app's `amxBinaryPath`, which restores the world and runs the bundle's own
`amx`. An app installed for real needs neither.

## The runs

`run.sh REPO OUTDIR STEP...`, in the order they were recorded:

- `setup`: the world.
- `tmux` (`drive_tmux.sh`, `isolated-tmux.log`): the dashboard and the overview
  on the tmux backend with no Kitty in their environment. The surface runs in
  a window of an observer tmux on a socket of its own, which is only a screen
  recorder and a keyboard; it is headless. Sixteen workflows: empty dashboard,
  launch, attach and detach with a hook in between, session actions and
  utility views, a Kitty-only command that says why it is unavailable,
  restart, Past Sessions and continuity, a worktree fork, Plans, the overview,
  the pickers as portable views of a tmux session (W11), event export selected
  by runtime session (W13), a stop held for confirmation and released at its
  deadline and the session marked seen when a terminal attaches (W14), the
  context-limit banner read off a pane nobody is attached to (W15), close, and
  the private server killed under running agents with the list kept (W16).
  W13 to W16 do not go through the dashboard: they type at one agent through
  the private server, which attaches nothing, and read what the daemon holds.
- `pair` (`pair.sh`, `../pair/`): not on the MacBook and not part of `run.sh`.
  The isolated installed pair, headless on the Studio; see the script's
  header.
- `kitty` (`drive_kitty.sh`, `isolated-kitty-legacy.log` and
  `isolated-kitty-tmux.log`): a real Kitty window on the MacBook, driven and
  read with `kitten @ send-text`, `send-key`, `get-text`, and `ls`. First the
  Kitty backend as it has always worked (L1 to L9), then the tmux backend
  hosted by the same Kitty with a Kitty session beside it (T1 to T5).
- `packaged` (`drive_packaged.sh`, `packaged-workspace.log`): the installed
  Release app as a workspace. It is moved with the `workspace_view` request
  the Dashboard sends (`dview.py`), quit with an Apple event, and killed with
  a signal. No key, pointer, or accessibility grant is used, and the window is
  never looked at as pixels: what it shows is read from the private server's
  client list, the process table, the daemon's snapshot, the app's saved
  preferences, and its unified log.
- `cockpit` (`drive_cockpit.sh`, `isolated-install-cockpit.log`): a rehearsal,
  not an acceptance run. `just install-restart-smoke` and `just cockpit-smoke`
  are typed into the world's Kitty window with the world's `HOME`, twice: as
  the world stood, and then with what the first pass's `doctor` asked for. It
  records what happened and judges nothing.
- `down` (`down.sh`, `teardown.log`): ends the world's processes, removes the
  app's preferences and the world, and compares the MacBook user's own state
  with what it was at setup.

Every `PASS` or `FAIL` line in a log is one observed condition, and each log
ends with its counts.

## Two rounds

The session ran in two rounds. The first (on fcd31c0b plus the working tree
of the time) recorded the live runs and found nine matrix rows without
evidence. The second, after the changes that gave those rows their behaviour
and tests (the daemon reads tmux panes, a terminal attaching marks a session
seen, the TUI keeps tmux rows through an outage, the pickers are portable,
event export selects by runtime session), rebuilt the bundle and ran `tmux`,
`kitty`, `packaged`, `down`, and the Session 17 installation driver again.
The logs in `../live/` and `../packaged/` are the second round's, each once,
with no failed pass before it. `isolated-install-cockpit.log` is the first
round's rehearsal and was not repeated: it is of the earlier tree. The first
round's logs are in the repository's history at 1510134c.

## What was run more than once in the first round, and why

- `tmux` twice. The first pass failed two steps in the overview case: the
  driver sent F12 and `q` before the attach it had just asked for had
  happened, because the text it waited for was already on screen in a preview
  tile. The driver now waits for the grid to be gone. The second pass is the
  record.
- `packaged` twice. The first pass failed two checks that expected a session's
  terminal to detach when the workspace moves to another session or view. The
  app does not do that: it keeps one terminal per session while its window is
  open, and detaches them all when the window closes or the app quits. The
  checks were wrong, not the app, and now assert that there is one terminal
  per session and never a second. The second pass is the record.
- `cockpit` twice. The first run had one pass and showed what `doctor
  --strict` refuses in the world; the second run added the prepared pass and
  is the record. Because the world was already prepared by then, its "first
  pass" is the world as the first run left it, less the configuration change,
  which the script applies between its passes.
- `kitty` once.

In the second round `pair.sh` ran three times. Its first run compared the
clone with the source by `diff -r`, which the source's symlink loops made
noisy; the script now compares tree hashes. Its second run passed, and was
followed by one more Go change (the lifecycle tests' startup probe), so it was
run a third time on the final tree: that one is the record in `../pair/`. All
three passed every recipe.

## What the rehearsal found

`just install-restart-smoke` and `just cockpit-smoke` can be run against an
installation that is entirely the world's: the first builds and installs
`amx` under the world's `~/.local/bin`, links `agent-mux.conf` into the
world's `~/.config/kitty`, restarts the world's daemon, and passes strict
doctor; the second reaches the world's Kitty, daemon, and live window. Three
things have to be true first, and `doctor --strict` names each:

- no notification may be attempted: `[defaults.notifications]` with every
  category off and an `alerter_path` that does not exist, as in the Session 18
  driver;
- the installed `amx` must be the `amx` on `PATH`: put `~/.local/bin` ahead of
  anything else that holds one;
- the profile's hooks must be installed by that `amx`: `amx config
  hooks-sync`.

With those, `install-restart-smoke` exits 0 and `cockpit-smoke` passes its
Kitty, daemon, live-window, and invariants steps, then stops at `amx config
asl-sync --check`, because the world has no agent-session-loop. That last
step needs an ASL installed in the world.

The checkout copy on the MacBook has no `.git` (`remote-ui.sh` does not send
it), so the `amx` built there reports `revision unknown`. A rehearsal that has
to name a revision needs a clone there, not the copy.

## Things that mislead

- `isolated-kitty-tmux.log` ends `30 passed` and holds 28 `PASS` lines: the
  two row selections (`select_row beta`) are counted as steps and write no
  line of their own. What they did is in the capture that follows each.

- A capture of the Kitty window in the cockpit log includes its scrollback,
  so earlier commands appear again above the one being recorded.
- The Go module cache a build fills under the world's home is read-only;
  `down.sh` makes it writable before removing the world.
- `pkill -f /tmp/amxs19/` from a script that lives there ends the script too.
- The overview's preview tiles show `conversation:` for every session, so
  that text on screen does not mean a terminal is attached.
