# Session 20 rehearsal driver

The scripts that produced `../rehearsal/transition-rollback.log`. They
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

- Step 4 of the guide's rollback (reinstalling the earlier build) was not
  rehearsed: it replaces the operator's own tools.
- The app was installed into `/tmp/amxs19/Applications` with
  `AMX_APP_NO_LAUNCH=1`, not into `/Applications`, and the bundle is ad-hoc
  signed.
- No real Claude, Codex, or Copilot ran.
- The app's launch preference was written with `defaults write` and then
  observed only through the app's log and the terminal it attached; a launch
  made from the app's own menu was not driven.

## Runs

`transition` ran twice. The first pass recorded 32 passed and 9 failed, and
every failure was the driver's: it expected `amx project launch` of a project
that is already running to start nothing (it launches another session, on
either backend, by design), which threw off the checks that counted tabs and
panes after it; and after the uninstall it looked at the private server with
the tmux inside the bundle it had just removed. The driver now launches a
different project where it wants a second session and looks with the runtime
copy outside the app. The world was torn down and rebuilt, and the second
pass (43 passed, 0 failed) is the record. `down` after each pass reported the
MacBook user's own state unchanged.
