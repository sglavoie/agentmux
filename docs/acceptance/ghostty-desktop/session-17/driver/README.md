# Session 17 acceptance driver

The scripts that produced `../install-acceptance.log`. They run on one Mac
(the Studio) and act only on another (the MacBook Pro): the installer, the
app, `amx`, the daemon, the tmux server, and the agents all run there. No
input is synthesized on either machine: every step is a script run over SSH,
and every check is read back over SSH.

## What is under test, and where

| | Studio (driver) | MacBook Pro |
| --- | --- | --- |
| The bundle | `just mac-app Release` is started here; Xcode runs on the MacBook | built and signed there, in the checkout copy `macos/scripts/remote-ui.sh` keeps |
| The installer and uninstaller | | `macos/scripts/install-app.sh` and `uninstall-app.sh` of that copy, run as they are |
| Install root | | `/tmp/amxs17/Applications` (`AMX_APP_INSTALL_DIR`); nothing goes to `/Applications` |
| `amx`, the daemon, the registry, the config | | the bundle's own `amx`, with `HOME=/tmp/amxs17/home` and `TMPDIR=/tmp/amxs17/tmp` |
| tmux | | none on the Mac at all (no Homebrew tmux, none on the login `PATH`); the world's `PATH` is system directories only |
| The agents | | stand-ins (`agent.sh`, installed as the world's `claude`), for the projects `alpha` and `beta` |

The MacBook is the clean supported Mac: macOS 27.0.1, arm64, no tmux, no
Agent Mux.app, and no preferences for it. It does have the operator's own
`~/.local/bin/amx` and AgentMux directories; the world never reads or writes
them, and `down` checks that a listing of them is what it was at the start.

The agents are stand-ins on purpose. Surviving an update is about the process
a pane holds, whatever it is: each stand-in sends one managed `SessionStart`
hook and then counts, one line a second, in a log of its own (count, PID,
clock). "The same agent, undisturbed" is checked three ways: the operating
system's PID and start time for the pane's process, the registry record
compared whole, and a count that never starts over, skips, or pauses.

## The cases

`run.sh OUTDIR [CASE...]`, in order:

- `install`: preconditions, then `install-app.sh` as a first install; the app
  is started from the install root and its log read; a daemon is started with
  the bundled `amx`; `alpha` is launched on the tmux backend.
- `bundle`: the runtime in the bundle and its installed copy against their
  checksums, `otool -L`, the release, architecture and minimum macOS, the
  server process's executable, the recorded executable, the allocator.
- `signed`: `codesign` on the bundle and on both executables; then three
  altered copies of the bundle, each offered to the installer, and an altered
  runtime offered to `amx` directly.
- `diagnostics`: a standalone `amx` with no bundle, no runtime, no server and
  no tmux (status, `--check`, a tmux launch in JSON and text); a tmux that is
  too old; a configured tmux that does not exist; an unknown launch backend;
  and the app with a tmux it cannot use.
- `update`: a second bundle (`make_v2`: version 0.1.1, and a tmux runtime
  whose `VERSION` has one more line, so its checksums and install directory
  differ; signed with `sign.sh`) is installed over the first while the app
  runs and `alpha` runs detached.
- `server`: the server process, its generation, its executable and the record
  beside its socket after that update; `beta` launched into the same server.
- `uninstall`: refusal of `--remove-app-data` while agents run; the default
  uninstall; `--end-sessions` without and with `--yes` (the app already gone,
  so the server is asked with its own recorded tmux); reinstall, a new server
  on the update's runtime, and `--end-sessions --remove-app-data --yes`.
- `down`: the world's processes are ended, the app's preferences removed, and
  `/tmp/amxs17` deleted.

`setup.sh REPO` builds the world first. It installs nothing; it syncs the
checkout so the scripts that run on the MacBook are this checkout's, and it
needs the Release bundle `just mac-app Release` left there.

## What the update is, exactly

The update's tmux executable is byte for byte the first one's: only the
runtime's `VERSION` differs, which changes `SHA256SUMS` and therefore the
directory the runtime is installed in. What the case shows is what was
asked: a server that is running keeps its process and the executable file it
was started from while another runtime is installed beside it, and the next
server starts from the new one. It does not show two different tmux releases
talking to each other, because no client of the new runtime is ever pointed
at the old server.

## How the app is started

The installer is run with `AMX_APP_NO_LAUNCH=1` and the driver starts the app
itself (`open -n … --env HOME=… --env TMPDIR=… --env PATH=…`), because an app
opened by Launch Services does not inherit the caller's environment and would
otherwise use the MacBook user's real home. The installer's own `open` is
therefore the one line of it this run does not exercise. The app's window is
never opened: what it found is taken from its log (`backend: …`, `running …
(bundled)`, `feed: live`), and what Preferences shows from the same report is
covered by the hosted tests (`BackendSetupTests`).

The app runs under its real bundle identifier, so its preferences are the
MacBook user's `io.github.sglavoie.agentmux` domain. That domain did not
exist before the run (the first case checks it) and `down` removes it.

## Things that mislead

- A standalone `amx` given only another `HOME` still shares the world's
  `TMPDIR`, and so its session server: a launch "without tmux" then succeeds,
  through the tmux the running server recorded. The diagnostics case gives it
  a `TMPDIR` of its own.
- The app logs the resolved `amx` under `/private/tmp/…`, not `/tmp/…`.
- `vmmap` names the process's malloc zones on standard error.
- `defaults delete DOMAIN` can leave an empty domain and a 42-byte plist
  behind; "no preferences" is checked as "no value left".
- In the recorded log the third altered-bundle step is labelled `amx given a
  runtime that was altered …`; what ran is `install-app.sh` (the line above
  the label's output says so), which refuses it by the broken seal.
