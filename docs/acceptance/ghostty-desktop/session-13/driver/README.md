# Session 13 acceptance drivers

The scripts that produced `../workspace-acceptance.log` and the screenshots
beside it, kept as they ran. They drive the real `Agent Mux.app` in an isolated
world rooted at `/tmp/amxs13`; nothing reads or writes the operator's daemon,
tmux server, Kitty, config, or the installed app's preferences.

- `env.sh` is sourced by every script: it points `HOME`, `TMPDIR`, `PATH`, and
  the XDG directories at the world and unsets every Kitty, tmux, ASL, Claude,
  and AgentMux variable.
- `setup.sh REPO` (after `just mac-app`) builds this checkout's `amx` into
  `bin/`, installs `standin-claude.sh` as `bin/claude`, links the installed
  `asl`, writes `config.toml`, a three-file ASL plan, and three Git
  repositories, compiles `click.swift`, and copies the Debug app to
  `app/Agent Mux.app` under the bundle identifier
  `io.github.sglavoie.agentmux.acceptance13`, re-signed ad hoc. The other
  identifier gives the copy its own preferences domain and lets it run beside
  the installed app, whose single-instance rule matches by identifier.
- `world.sh up` starts a separate Kitty (`--config NONE`, its own
  `--listen-on` socket named for its pid, `kitty-zshenv` as `zdot/.zshenv`
  because Kitty starts launched tabs through `login(1)`, which resets `HOME`),
  the world's daemon, two tmux sessions of project `alpha` (profiles `work`
  and `personal`), and one Kitty session of project `beta`. `world.sh down`
  stops all of it and removes the world's state and the copy's preferences.
- `amx-shim.sh` is the `amx` the app copy is pointed at (`amxBinaryPath`).
  libghostty also starts a terminal's command through `login(1)`; the shim
  puts the world's `HOME` back and execs the real `amx`. With the real `HOME`
  no shim is needed.
- `lib.sh` holds the helpers: the private tmux server's clients, the
  registry, the daemon's v2 snapshot read by a probe subscriber, the Kitty
  tree, the app's unified log (`log show`, notice level), and the app's
  windows and menus through System Events accessibility. `click` posts a real
  left click (CGEvent) at a screen point; keys go through System Events.
- `drive.sh OUTDIR CASE...` runs the cases and appends to
  `OUTDIR/workspace-acceptance.log`: `fresh env sidebar focus attention
  reconnect retained close`. Every `check` line is one observed condition,
  `PASS` or `FAIL`; the log ends each run with its count of failed checks.

The app copy's shortcuts are set to ⌃⌥⇧⌘N and ⌃⌥⇧⌘D so they do not collide
with the installed app's ⌃⌥⌘N and ⌃⌥⌘A, and its notification backend is off so
no permission prompt appears.
