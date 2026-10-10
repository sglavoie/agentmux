# Session 16 acceptance driver

The scripts that produced `../recovery-acceptance.log`. They run on one Mac
(the Studio) and act only on another (the MacBook Pro). Unlike the Session 15
driver, this one synthesizes no input at all, on either machine: no key, no
pointer, no VNC, no accessibility grant, no picture. Everything it does is a
process started or killed over SSH, a request on the world's daemon socket,
or a command to a tmux server; everything it checks is read back over SSH.

## What runs where

| | Studio (driver) | MacBook Pro (everything under test) |
| --- | --- | --- |
| The app under test | | a copy of this checkout's Debug build, bundle id `io.github.sglavoie.agentmux.acceptance16`, built there |
| `amx`, the daemon, the registry, the config | `amx` is built here and copied | `/tmp/amxs16` (own `HOME`, `TMPDIR`) |
| tmux | | a relocated copy of tmux 3.7c inside the world; nothing installed |
| The agents | | three stand-ins (`agent.sh`, installed as the world's `claude`), one per project: `alpha`, `beta`, `gamma` |
| Sleep and wake | sends a wake-on-LAN packet (`wake.py`) | `pmset sleepnow`, then `caffeinate -u` once it answers |

The agents are stand-ins on purpose. Recovery and ownership are about the
process a pane holds, whatever that process is, so no harness is signed in or
run: each stand-in sends one managed `SessionStart` hook and then counts, one
line a second, on its screen and in a log of its own (`ticks-PROJECT.log`:
count, PID, clock). "The same agent" is checked three ways: the registry's
record, the operating system's PID and start time for the pane's process
(`ps -o pid=,lstart=`), and a count that never starts over or skips.

## How the app is moved without input

- **To a session:** `dview.py session ID` sends the daemon the same
  `workspace_view` request the Dashboard sends when a row is chosen; the
  daemon forwards it to the app, which shows that session's terminal.
- **To the Overview or the Dashboard:** `dview.py overview|dashboard`.
- **Quit:** an Apple event (`tell application id ... to quit`), which macOS
  allows over SSH for this app copy without a prompt. **Crash:** `kill -9`.
- **Started again:** `open -n` on the app copy. Where it comes back to is the
  app's own doing: it reads the two preferences it saved.
- **A second terminal:** `amx attach ID` on a pseudo-terminal of its own size
  (`script`, `stty cols 90 rows 25`), held open in the background.
- **One key to the Overview's own terminal.** The Overview starts in its
  compact layout, which has no previews. The world's `amx` shim hosts every
  utility view in an observer tmux so its screen can be read; `c` is sent to
  the Overview there, as a tmux command, to switch it to the preview layout.
- **Commands to an agent:** `ask` and `exit N` are sent to a pane with
  `tmux send-keys` on the private server. That is the test speaking to its
  stand-in, not a viewer: no terminal is attached to do it.

## What is read

`remote-lib.sh` holds the readers: the registry file, `tmux list-clients` and
`list-panes` on the private server, the daemon's v2 snapshot (a subscribe on
its socket), `amx attach ID --json`, the app copy's saved preferences, the
app's unified log (`log show`, its own subsystem and process), the stand-ins'
count logs, the daemon's log, and `pmset -g log` for the sleep and the wake.

The app's pane is never looked at as pixels. What it shows is taken from its
log, which names every state the pane enters (`workspace: showing ...`) and
every decision behind it; the words on the placeholders are covered by the
hosted tests (`WorkspaceRecoveryTests`).

## Files

- `env.sh` (Studio) and `remote-env.sh` (MacBook) define the two sides.
- `setup.sh REPO` builds `amx` on the Studio, has the MacBook build the Debug
  app in the isolated copy that `macos/scripts/remote-ui.sh` keeps there, and
  installs the world under `/tmp/amxs16` on the MacBook.
- `run.sh OUTDIR [CASE...]` runs the cases, in order: `up crash daemon
  transfer passive detached sleep exited absent lost down`. `purge` removes
  the world from the MacBook.
- `agent.sh`, `amx-shim.sh`, `obs.sh`, `config.toml`, `dview.py`, `wake.py`,
  `lib.sh`, `remote-lib.sh`: as described above.

## What the MacBook is asked to do, and what is left there

- **It is put to sleep** (`pmset sleepnow`, which needs no privilege and
  overrides a keep-awake assertion for that one sleep) and woken by a
  wake-on-LAN packet for its network interface, followed by `caffeinate -u`
  for five seconds so the wake is a full one. Nothing is scheduled and no
  power setting is changed.
- The app copy has its own preferences domain, deleted by `down`.
- `purge` removes `/tmp/amxs16`. The isolated checkout copy under
  `~/Library/Caches/agent-mux-remote-ui/` is kept: the remote route uses it.
- The MacBook's clipboard, focus, input sources, and system settings are not
  touched. The app copy's window does appear on its screen while it runs.

## Things that mislead

- `kill` on the app is not a quit: AppKit does not turn SIGTERM into a
  termination, so nothing the app does on quitting runs. Use the Apple event.
- After a wake-on-LAN packet a Mac may be in a dark wake: SSH answers but
  no application has been told the machine woke. `caffeinate -u` makes it a
  full wake.
- The Overview's compact layout lists sessions without reading their
  screens; a check that the previews read the panes has to switch it first.
- tmux reports a client's creation time in whole seconds, so two clients
  that attached within one second are ordered by activity and then by name.
