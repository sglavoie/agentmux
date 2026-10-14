# tmux as a complete backend: live acceptance

The record is `../tmux-complete-backend.json`. Its status is `incomplete`:
ten of the eleven cases and the hosted tests passed on a Mac with no Kitty
installed, and the paired release gate passed, but on a Mac that has Kitty
installed. Two items of the acceptance are unmet:

- `notification-without-app` was not executed: the daemon's Alerter
  notification is never presented on macOS 27 (below). What would meet it is a
  change to the daemon's Alerter sender, then the case run again on a
  Kitty-free Mac at the new revision.
- The passed gate did not run on a machine with Kitty not installed. What
  would meet it is the gate run on a Kitty-free Mac on which the pair is a
  working installation.

This file says how the results were taken and what stands in the way of the
rest.

Everything ran on 2026-10-14 (UTC) against revision `8f063a9c`.

## Two machines

As in `../ghostty-desktop/session-15/driver/README.md`: the Mac Studio only
orchestrates, and everything seen, launched, typed at, or clicked is the
MacBook Pro's (macOS 27.0.1).

For the run the MacBook had no Kitty installed: `/Applications/kitty.app`
(0.48.2) was moved aside to a directory in that user's home at 00:30:54Z and
moved back at 03:17:53Z, with the operator's approval and with the same owner
and mode afterwards. In between there was no `kitty.app` in `/Applications`, no
`kitty` or `kitten` on `PATH`, no Kitty process, and no `KITTY_*` variable in
any shell a run used. `~/.config/kitty` was left alone. Each log starts by
recording those four facts.

## The world

`driver/setup world` builds `/tmp/amx75w` on the MacBook: its own `HOME`,
`TMPDIR`, config, daemon, registry, and private tmux server (the MacBook's
Homebrew tmux 3.7c). `amx` is built there from that Mac's own checkout of
`8f063a9c` (`amx --version` names the revision; sha256 `d7d4629f8a61`, the same
artifact the gate built). The config (`driver/world-config`) sets
`launch_backend = "tmux"`, as a Kitty-less installation does: the default has
not moved.

The three harnesses are real: Claude Code 2.1.296, codex-cli 0.162.1, and
GitHub Copilot CLI 1.0.97. They are signed in on the Studio only, so each
runs there, headless, and its terminal is carried into the tmux pane AgentMux
launched for it by session 15's `bridge.py`. Unlike session 15, their hooks
are real too:

- each harness is given the hooks `amx config hooks-sync` wrote for its
  profile in the world, with the command replaced by `driver/hook`: Claude as
  `--settings`, Codex as `-c hooks.<Event>=...` overrides, Copilot from its
  project directory. Nothing is written to a harness's real home;
- `hook` hands the payload over SSH to `driver/hookrelay` on the MacBook,
  which runs the world's `amx hook-indicator` in the environment and directory
  of that pane, saved by `driver/agent` when the pane started. The daemon
  receives exactly what the harness sent, as a managed hook of that pane's
  runtime session;
- the daemon confirms a Copilot stop from the session transcript, which
  Copilot writes on the Studio. `driver/mirror` carries that one file to the
  world's Copilot home twice a second, as the bridge carries the terminal.

The `plain` and `second` projects run a stand-in with no model that sends
real hooks itself (`ask`, `work`, `done`).

The plain terminal is one window of an observer tmux (own socket, no
configuration) running `amx attach`: a real client of the private server with
a keyboard (`send-keys`) and a screen (`capture-pane`).

## The cases

`live/` holds one log per case; every `PASS` or `FAIL` line is one observed
condition and each log ends with its counts.

| Case | Result | How |
| --- | --- | --- |
| `launch-two-profiles` | 13 passed | `amx project launch` for three profiles and the stand-in; registry, panes, v2 snapshot, per-profile color; `amx attach` |
| `claude-stop-held-confirmed` | 8 passed | a prompt typed in the attached terminal; `stop_confirmed_by=probe:claude_screen`, held 8211 ms |
| `codex-stop-held-confirmed` | 8 passed | `probe:codex_screen`, held 6000 ms |
| `copilot-stop-held-confirmed` | 8 passed | `probe:copilot_transcript`, held 6001 ms |
| `function-keys-f1-f11` | 28 passed | F1, F2, F5, F6 as popups; F11 and F8 by the daemon's state; F10 by the pane's mute; F3 by the pane's mode; F4 to the session needing attention; F7 and F9 once |
| `navigation-f7-f9` | 11 passed | three F9 and three F7 across four sessions, the client and the owner record after each |
| `dashboard-popup-return` | 12 passed | ctrl+shift+d, `q` back to the session, then Enter on another row switches the terminal and ends the popup |
| `doctor-strict` | 3 passed | exit 0; two warnings that ASL is not installed in the world |
| `notification-with-app` | 13 passed | a real click, through the VNC viewer, on the app's notification; the workspace opens on that session (`focus_delegated`, `amx-workspace-` owner) |
| `automatic-launch-workspace` | 10 passed | Launch Project from the app's menu with `amxLaunchBackend` unset; the app logs `launch ... on tmux` and the workspace shows it |
| `notification-without-app` | **not executed** | see below |

The app in the two app cases is the Release bundle of this checkout, built on
the MacBook by `just mac-app Release` and signed on the Studio with its Apple
Development identity by `macos/scripts/sign.sh`, under its real bundle
identifier. A copy re-signed ad hoc under another identifier, as session 15
used, is refused by macOS 27's notification service (`usernoted: Failed to
find or validate client`).

While a viewer shares the display macOS presents no banner (`usernoted:
interruptionSuppression: silence; resolutionReason: display shared`), so the
app's notification is clicked where it is delivered, as the first card of
Notification Center. Pictures are of that card and of the app's own window and
menu only.

`just mac-test` ran on the MacBook in the same window: 459 tests, 0 failures
(`mac-test/mac-test-remote.log`).

## What was run more than once, and why

- `mac-test` twice: the first run started before Kitty was moved aside. The
  second is the record.
- The whole terminal sequence twice. The first pass found two faults in the
  driver, not the product: it expected F4 to go to the stand-in while the
  three real sessions were at rest after their turns (which is attention too),
  and the world's Codex profile lacked the `--dangerously-bypass-hook-trust`
  the sample profile carries, which `doctor --strict` reports. The second pass,
  on a fresh world, is the record.
- `dashboard-popup-return` once more after that pass: the driver had assumed
  the dashboard's row order, which is by urgency. It now finds the row by what
  the inspector names. That third run is the record for this case.

## Not executed: the notification click with no app

With no app connected the daemon posts through Alerter, started with
`--sender fr.vjeantet.alerter` (`internal/daemon/notification/backends.go`).
On macOS 27.0.1 that notification is never presented, as a banner or in
Notification Center, so there was nothing to click. The daemon journals
`dispatch attempted backend_started` and nothing after it.

Each time `usernoted` logs `Unable to find notifier for identifier
fr.vjeantet.alerter and backupPath: <the binary>` and NotificationCenter logs
`Couldn't find application fr.vjeantet.alerter`. That held for Alerter 26.5:

- copied into the world, and started inside the GUI session;
- copied under the home directory;
- wrapped in a bundle of that identifier and registered with `lsregister`;
- installed for real with `brew install vjeantet/tap/alerter`;
- and again after `usernoted` and `NotificationCenter` were restarted.

Run without `--sender`, the same binary is presented, as Terminal. So the
identifier the daemon passes names no application on a Mac where none was ever
installed under it, and macOS 27 drops what it cannot attribute. On the Studio
the installed daemon's journal shows every notification going through the app.

No stand-in was put in place of a click.

## The paired release gate

### On the MacBook (Kitty-free): never passed

`just paired-release-gate` ran three times on the MacBook, unsigned and
without a tag. No step ran or named Kitty. The three records are in `gate/`:

1. ASL `dbf5676`: `asl-tests` failed in ASL's own committed tree.
2. ASL `fae855c`: `asl-lint` failed; `ruff` and `mypy` were missing on that
   Mac, and once installed `mypy` found errors in ASL's committed tests.
3. ASL `61e23fe`: every verification step passed (`amx-check`, `asl-tests`,
   `asl-lint`, `contract-sync`, `paired-integration`, `crash-restart`, both
   stress suites) and `asl-install` failed: that Mac had only `/bin/bash` 3.2.

After the third run the install and smoke steps were run by hand
(`gate/install-and-smoke-by-hand.log`). `verify-asl-install` and
`overseer-integration` pass. Three steps fail, all on how that Mac is set up:
one of its LaunchAgents cannot load because its program is not installed
there, and `amx doctor --strict` reports seven `[hooks]` health failures in
its real profiles (hooks never synced, harnesses not signed in, no Copilot
CLI).

### On the Studio (Kitty installed): passed

At the operator's instruction the gate then ran on the Studio, the driver
machine, where the pair is a working installation and Kitty is installed (not
running, and no `KITTY_*` variable in the gate's environment). It ran detached
from the session that started it, since its install step restarts the daemon
that session runs under. Its records are in `gate/studio/`:

1. Refused before any step: the ASL checkout had uncommitted changes. The
   operator cleaned it.
2. ASL `f63ffef`: every verification step passed, and `asl-install` failed:
   ASL's installer reloads the LaunchAgents, and two long-running ones failed
   to bootstrap again (`Bootstrap failed: 5`). A plain `launchctl bootstrap`
   loaded both straight afterwards.
3. All 16 steps passed, and the gate refused to write the record because the
   second run's record for the same pair ID was still in the evidence
   directory.
4. **Passed** (`gate/studio/amx-asl-1.df1580f1ff696edb.json`, finished
   05:18:04Z): schema 2, all 16 steps passed with exit 0, `cockpit-smoke`
   present and no `kitty-version` step, unsigned, no tag. It names Agent Mux
   `985c3b6b`, which is `8f063a9c` plus the two commits that add these
   acceptance files and nothing else, and ASL `f63ffef`.

The Studio now runs the `amx` that gate installed (`985c3b6b`), its daemon
was restarted by the gate, ASL was restowed from its checkout, and its
LaunchAgents were reloaded.

## What was changed on the MacBook

With the operator's approval each time:

- Kitty moved aside and back, and `VNCAlwaysStartOnConsole` set at 00:54:28Z
  and deleted at 03:18:01Z (four `sudo` commands; no other).
- Both checkouts fast-forwarded: Agent Mux to `8f063a9c`, ASL to `61e23fe`.
- Installed: Homebrew `tmux`, `golangci-lint`, `bash`, `alerter`; `ruff` and
  `mypy` as `uv` tools.
- The by-hand install steps ran ASL's installer and `just install`: the
  MacBook now has `amx` 0.8.0 at `8f063a9c` with its daemon running, ASL's
  links restowed, and its LaunchAgents reloaded. Two regular files in its
  `~/.local/bin` that stood where links belong were moved to the aside
  directory and are still there.
- macOS notification permission was allowed, through the viewer, for
  "Terminal" (asked by Alerter run without a sender) and for "Agent Mux".
- `usernoted` and `NotificationCenter` were restarted once.

The world, its app preferences, and every scratch file under `/tmp` were
removed.
