# Session 20 rehearsal driver

The scripts that produced `../rehearsal/transition-rollback.log` and
`../rehearsal/rollback-step4.log`. They
rehearse the transition and the rollback that
[docs/daily-driver-trial.md](../../../../daily-driver-trial.md) gives the
operator, with stand-in agents, in a disposable world on another Mac. They are
not the daily-driver trial and stand in for none of its days.

Like the Session 19 driver they are started on one Mac (the Studio) and act
only on another (the MacBook Pro, `AMX_MAC_TEST_HOST`). Nothing is shown or
driven on the Studio.

## The world

The Session 19 world, unchanged: `run.sh` sends `../../session-19/driver` and
then `drive_transition.sh` to `/tmp/amxs19/driver`, and uses that driver's
`setup.sh`, `remote-env.sh`, `dview.py`, `amx-shim.sh`, stand-in `claude`, and
`down.sh` as they are. Read that driver's README first.

One thing is added, to make the world look like the operator's Mac: an `amx`
outside the app. `drive_transition.sh` copies the bundle's `amx` to the
world's `~/.local/bin/amx` and points the world's `amx` at the copy, so the
daemon, the hooks, and every command below run from an `amx` that is still
there after the app is removed. It finds tmux as "a runtime the app installed
earlier".

## The run

`run.sh REPO OUTDIR setup transition down`. `transition` is eight steps, each
`PASS` or `FAIL` line one observed condition:

0. the candidate: the Mac, the installed bundle and its seal, both `amx`
   builds, Kitty, and a configuration with no launch backend;
1. before: `amx project launch alpha` opens a Kitty tab, starts no tmux
   server, and its hook marks the tab;
2. the transition: `launch_backend = "tmux"` in `config.toml`, `amx backend
   status --check`, and the app started with its launch preference on the
   workspace;
3. after: `amx project launch beta` with no `--backend` goes to the private
   server; the app attaches its own terminal when asked to show it (the
   `workspace_view` request the Dashboard sends); the Kitty session is the
   same process in the same tab and its hooks still arrive; `--backend kitty`
   still opens a Kitty tab;
4. the Kitty sessions end by themselves and their tabs go; the tmux agent is
   not touched;
5. rollback 1: the configuration line removed, the app's preference back on
   Kitty; a launch opens a Kitty tab; the tmux agent is the same process on
   the same server;
6. rollback 2: `uninstall-app.sh`; the app and its process are gone; the tmux
   agent is the same process on the same server, answers its terminal, and is
   found live by `amx attach --json`; a `--backend tmux` launch still works
   with the app gone;
7. rollback 3: `amx backend stop --yes` ends the server and both agents; the
   registry holds no live session; the Kitty session and its hooks are
   untouched; `config.toml` is as it began.

Launches are requested with `amx project launch`; for Kitty launches the
command runs outside Kitty with `KITTY_LISTEN_ON` naming the world's Kitty.
Text is sent to the stand-in agents through `kitten @ send-text` and the
private tmux server. No key, pointer, or accessibility grant is used, and the
app's window is never looked at as pixels.

## What it does not show

- `just update` in the earlier checkout was not run: step 4 does its parts by
  hand, in the world (see below).
- The app was installed into `/tmp/amxs19/Applications` with
  `AMX_APP_NO_LAUNCH=1`, not into `/Applications`, and the bundle is ad-hoc
  signed.
- No real Claude, Codex, or Copilot ran.
- The app's launch preference was written with `defaults write` and then
  observed only through the app's log and the terminal it attached; a launch
  made from the app's own menu was not driven.

## Rollback step 4

`run.sh REPO OUTDIR old setup rollback4 down`.

`old` builds the release the operator ran before the candidate (`OLD_REV`,
5645f028f120): `amx` on the Studio, headless, from a detached worktree under
`/tmp` that is removed again, so that it carries its revision; the app on the
MacBook, from that tree copied to `/tmp/amxs20-old`, with the earlier
checkout's own `build-app.sh Release`. `down` removes `/tmp/amxs20-old`, so
`old` comes before `setup` every time.

`rollback4` (`drive_rollback4.sh`) is eight steps:

0. the two builds: different revisions, the earlier app sealed and with no
   tmux runtime;
1. the candidate in use: a tmux agent, the daemon's runtime checkpoint, the
   transcript index, a Kitty session, and files a rollback must leave alone
   (a handoff, a worktree file, `launch_overrides.json`);
2. the earlier `amx` reading a configuration that still names
   `launch_backend`;
3. rollback step 1, and a listing with checksums;
4. step 4: the candidate's daemon stopped, the earlier `amx` put at
   `~/.local/bin/amx` (as `just install` does), the earlier checkout's
   `install-app.sh` run against the world's install root, and the daemon
   started by the earlier `amx`. The tmux agent is left running on purpose;
5. the earlier build at work: the Kitty session from before, a new Kitty
   launch, hooks, the conversation list, the commands it does not have, its
   app;
6. what it did to what it does not know: the tmux agent, the checksums again;
7. forward: the candidate's `amx` back, the daemon restarted, the tmux
   session found live, then `amx backend stop --yes`.

The daemon is stopped and started in two commands where `just update` uses one
(`amx daemon restart`), so that what the candidate's daemon writes as it stops
is not counted against the earlier build. The registry and checkpoint are
compared from "the candidate's daemon has stopped" to "the earlier build has
run".

`amx daemon restart` and `amx doctor` are given `KITTY_LISTEN_ON`, because the
operator types them inside Kitty and the driver does not. `amx doctor
--strict` is recorded and not judged: in this world it fails with either
build (no hooks installed, no notifier, and the world's `amx` is a wrapper).

## Runs

`rollback4` ran twice. The first pass recorded 32 passed and 3 failed. Two
were the driver's: it restarted the daemon from a shell that named no Kitty,
so the daemon came up not ready (`kitten @ ls` had no terminal); hooks, which
carry their own socket, worked regardless. The third was the checkpoint
comparison: `runtime-state.json` differed between a listing taken the moment
the tmux agent had asked for permission and the end of the run, and that pass
could not say whether the candidate's daemon or the earlier build had written
it. The driver now names Kitty, lets the checkpoint settle, and takes its
baseline after the candidate's daemon has stopped. Rebuilt world, second
pass: 35 passed, 0 failed, with no difference in the checkpoint at any of the
three points. That the first pass's difference was the candidate's own daemon
is therefore likely and not proven. The second pass is the record.


`transition` also ran twice. The first pass recorded 32 passed and 9 failed, and
every failure was the driver's: it expected `amx project launch` of a project
that is already running to start nothing (it launches another session, on
either backend, by design), which threw off the checks that counted tabs and
panes after it; and after the uninstall it looked at the private server with
the tmux inside the bundle it had just removed. The driver now launches a
different project where it wants a second session and looks with the runtime
copy outside the app. The world was torn down and rebuilt, and the second
pass (43 passed, 0 failed) is the record. `down` after each pass reported the
MacBook user's own state unchanged.
