# Session 18 acceptance driver

`run.sh REPO OUTDIR` produced `../production-fleet.log` and the files beside
it. It measures what the real daemon and the packaged tmux cost with fleets of
1, 10, and 30 agents, idle and printing, and then breaks the backend three
ways to see what the diagnostics say.

It is headless. It shows nothing and sends no input, so it runs on the machine
it is started on; nothing here needs the remote route the UI drivers use.

## The world

Everything runs with `HOME=/tmp/amxs18/home`, `TMPDIR=/tmp/amxs18/tmp`, and a
`PATH` of `/tmp/amxs18/bin` and the system directories, under `env -i`: no
Homebrew, so no Kitty, no `alerter`, and no tmux but the packaged one. The
user's own daemon, tmux server, Kitty, and AgentMux directories are never read
or written; the first checks confirm that the daemon being measured is the
world's own executable.

| | |
| --- | --- |
| `amx` | built from the checkout into `/tmp/amxs18/app/bin/amx`, the layout of a bundle |
| tmux | the runtime `just mac-app Release` left in `macos/build/Agent Mux.app/Contents/Resources/tmux`, copied to `/tmp/amxs18/app/tmux`: found as the bundled runtime beside `amx`, verified against its checksums, and installed under the world's `~/.local/share/agent-mux/runtime/` |
| configuration | `launch_backend = "tmux"`, one profile, thirty projects, every notification category off and an `alerter_path` that does not exist, ASL disabled |
| the agents | `agent.sh`, installed as the world's `claude` |
| the daemon | `amx daemon start`, production cadences, nothing overridden |

The agents are stand-ins on purpose: the cost of watching a fleet is about how
many panes there are and how much they print. Each sends a managed
`SessionStart` and a `UserPromptSubmit` hook, then either sleeps (`idle`) or
runs `yes` (`output`): every pane printing as fast as the server takes it,
which is the most an agent can do and far more than one does.

## What is measured

For each fleet, after six seconds to settle, over a thirty-second window:

- the daemon's CPU time and resident memory, and the tmux server's, from `ps`;
- from `amx doctor --json` before and after: how many reconciliation passes
  ran, how long the last and the slowest took, how many tmux clients the
  daemon started, and the most it ran at once.

`ps` reports CPU time in hundredths of a second, so a daemon that used less
than 10 ms in the window reads as `0.00%`: the resolution is 0.03% of a core.

What the driver does not measure is a screen read: the daemon reads no tmux
pane today (its screen probes read Kitty windows), so `screen captures` says
`no screen read yet` throughout. What a read costs is measured by
`TestPerformance_FleetBudgets` in `internal/daemon`, recorded in
`../fleet-budgets.log`.

## The privacy check

Every agent puts two canaries where a diagnostic could pick them up: a prompt
and an API key, in its `UserPromptSubmit` hook payload and on its screen (in
every line it prints, for the `output` fleet). With thirty agents printing,
the driver takes `amx doctor`, `amx doctor --json`, `amx debug status`, and a
share-mode `amx debug bundle`, and greps those, every member of the bundle,
the journals, the daemon log, the state directory, and the data directory for
either canary. Two checks first establish that the canaries really are on a
pane's screen and really were delivered through the hook path, so that "not
found" means something.

## The failures

- **The server stops answering** (`SIGSTOP`): every client times out. Doctor
  must fail `--strict` on `backend_server_unreadable`, name the daemon's side
  (`runtime_reconcile_failing`, class `timeout`), and no agent may be given up
  on. After `SIGCONT` doctor passes again with the same agents, and the
  journal holds one failure edge and one recovery for the whole outage.
- **The server is killed**: every agent is recorded as stopped, none
  relaunched, and the journal records the pass that found them gone.
- **No tmux at all, and no daemon**: a standalone `amx` with a `HOME` and
  `TMPDIR` of its own. Doctor must fail on `backend_tmux_unavailable` and say
  how to get a tmux, with nothing running to ask.

## Things that mislead

- `seq -w 1 1` prints `1`, not `01`.
- A server that is stopped still accepts connections, so it reads as running;
  it is the survey that times out.
- The `output` agents scroll their first lines out of a 50,000-line history in
  under a second, so a canary printed once at start is gone before anything
  reads it.
- A window shorter than about ten seconds measures nothing: one pass every two
  seconds, and CPU time in hundredths.
