# Session 14 acceptance drivers

The scripts that produced `../desktop-acceptance.log` and the screenshots
beside it, kept as they ran. They drive the real `Agent Mux.app` in an isolated
world rooted at `/tmp/amxs14` that has **no Kitty in it**: no Kitty is started,
no Kitty socket is given to any process, and the world's daemon reports
`kitty_snapshot.ready=false` throughout. Nothing reads or writes the operator's
daemon, tmux server, Kitty, config, or the installed app's preferences.

- `env.sh` is sourced by every script: it points `HOME`, `TMPDIR`, `PATH`, and
  the XDG directories at the world and unsets every Kitty, tmux, ASL, Claude,
  and AgentMux variable.
- `setup.sh REPO` (after `just mac-app`) builds this checkout's `amx` into
  `bin/`, installs `standin-claude.sh` as `bin/claude` and `standin-editor.sh`
  as the configured editor, links the installed `asl`, writes `config.toml`,
  one closed Claude conversation for Past Sessions, a three-file ASL plan, and
  three Git repositories, compiles `click.swift`, and copies the Debug app to
  `app/Agent Mux.app` under the bundle identifier
  `io.github.sglavoie.agentmux.acceptance14`, re-signed ad hoc, so it has its
  own preferences domain and runs beside the installed app.
- `world.sh up` starts the world's daemon with no Kitty variable and launches
  one tmux session of project `beta`. `world.sh down` stops the app, the
  daemon, the private tmux server, and the observer, and removes the world's
  state and the copy's preferences.
- `amx-shim.sh` is the `amx` the app copy is pointed at (`amxBinaryPath`). It
  puts the world's `HOME` back, because libghostty starts a terminal's command
  through `login(1)`. For a utility view (`amx workspace-view KIND ...`) it
  also logs what the terminal's environment carried and then hosts the real
  `amx` in the **observer tmux** (`obs.sh`, its own socket and no config), in
  a session named for the view's kind, with exactly the environment the app's
  terminal gave it. The observer is only a screen recorder and a keyboard for
  the views: it is not AgentMux's private server and hosts no agent. Agent
  terminals (`amx attach`) are not wrapped.
- `lib.sh` holds the helpers: the private tmux server's clients and panes, the
  registry, the daemon's v2 snapshot and health read by a probe, the app's
  unified log (`log show`, notice level), the app's window, sidebar, and menus
  through System Events accessibility, and the utility views' screens and
  keys through the observer (`vscreen`, `vwait`, `vkey`, `vtype`). `click`
  posts a real left click (CGEvent) at a screen point; typing into a session's
  native view goes through System Events.
- `drive.sh OUTDIR CASE...` runs the cases and appends to
  `OUTDIR/desktop-acceptance.log`: `fresh env launch focus overview pager
  shell editor isolation workflows nokitty`. Every `check` line is one
  observed condition, `PASS` or `FAIL`; each run ends with its count of failed
  checks.

The app copy is started with `AMX_RUNTIME_SESSION_ID` set to the live session
`beta`, plus `KITTY_WINDOW_ID=999` and `CLAUDECODE=1`, as if it had been
started from inside a managed session. Every terminal it embeds inherits them;
the isolation case shows that no utility view keeps them and that the same
identity, when a process does carry it, reaches `beta`.

Its shortcuts are set to ⌃⌥⇧⌘N and ⌃⌥⇧⌘D so they do not collide with the
installed app's, and its notification backend is off so no permission prompt
appears. The dashboard shortcut and the status menu's Launch Project are not
pressed in this run: the operator's own Kitty is running on this machine with
a socket at the default address, which the app would rightly try first.
