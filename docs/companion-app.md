# Menu bar companion

`Agent Mux.app` is an optional native macOS menu bar app for Agent Mux. It
shows the live managed sessions with attention counts in the menu bar, focuses
a session's Kitty tab from a menu row or a global shortcut, shows the daemon's
notifications as native banners, and answers ASL supervisor questions in a
native panel. Kitty stays the terminal, and the daemon stays the source of
truth: the app subscribes to the daemon's companion stream and runs the `amx`
CLI for actions. Nothing in Agent Mux depends on it, and quitting it returns
every behavior to what it was without it.

This page is the operator's guide. How the app is built, signed, and laid out,
and how each feature works inside, is in [`macos/README.md`](../macos/README.md).

## Install

Requirements: macOS 14 or later, Xcode command line tools, and the
repository's usual toolchain (Go, `just`). The first build also builds the
embedded terminal engine from source, which needs `git`, network access,
several minutes, and about 5 GB under `macos/.cache/`; later builds reuse it.
See [GhosttyKit](../macos/README.md#ghosttykit).

```bash
just install        # amx and the daemon first: the app needs a daemon that streams
just mac-install    # Release build, quit any running copy, copy to /Applications, relaunch
```

Install `amx` first. The app talks to whichever daemon is running, and a
daemon older than the companion protocol leaves the app in degraded mode (see
[Troubleshooting](#troubleshooting)). After an upgrade of the pair, reinstall
the app too, so its bundled `amx` and the installed one stay close.

The app must live in `/Applications` for the login item to work, which is
where `just mac-install` puts it (`AMX_APP_INSTALL_DIR` overrides it, at the
cost of the login item). To remove it, quit it from its menu and delete
`/Applications/Agent Mux.app`; its preferences are in the
`io.github.sglavoie.agentmux` defaults domain.

## Permissions

The app needs no Accessibility, Automation, or Screen Recording permission.
Two grants matter:

- **Notifications.** The app asks the first time the daemon sends it a
  notification, not at launch. While it has never been asked, or while it is
  allowed, it claims the daemon's notification backend role; when it is denied
  or turned off in Preferences, the daemon uses Alerter as before. Change it
  later in System Settings › Notifications › Agent Mux. The app rereads the
  permission every 10 seconds and whenever its menu opens.
- **Login item.** Preferences › Launch at Login registers the app with
  `SMAppService`. It shows in System Settings › General › Login Items, and
  removing it there is respected: the app does not add it back.

macOS ties both grants to the app's code signature. A build signed with an
Apple Development or Developer ID certificate keeps them across rebuilds. An
ad-hoc build (no certificate in the keychain, or `AMX_CODESIGN_IDENTITY=-`)
looks like a new app every time, so both grants are asked for again after each
install. See [Signing](../macos/README.md#signing).

## Kitty requirements

Focus, the dashboard shortcut, Launch Project, Recent, and Mute all reach Kitty
through its remote-control socket, so `kitty.conf` needs:

```conf
allow_remote_control yes
listen_on unix:/tmp/kitty
```

Kitty appends its PID, so each instance listens on `unix:/tmp/kitty-<pid>`;
that is how the app tells two Kitty instances apart and brings the right one
forward. When the app cannot find any socket to try, its menu says so in place
of acting: "Kitty has no remote-control socket: add `allow_remote_control yes`
and `listen_on unix:/tmp/kitty` to kitty.conf, then restart Kitty." A changed
`listen_on` needs a Kitty restart, not a reload. If you use another socket
path, set it in Preferences › Kitty socket.

The daemon accepts a `focus_window` only for a socket it learned itself, so a
Kitty instance that has never run a managed session is not focusable until it
has.

## Global shortcuts

| Default | Does |
| --- | --- |
| ⌃⌥⌘A | Brings the running dashboard forward, or opens one (`amx open-dashboard`). |
| ⌃⌥⌘N | Focuses the next session needing attention, oldest first, across Kitty instances. Beeps when nothing waits. |

Change or clear them in Preferences. They are Carbon hotkeys and need no
Accessibility permission. Preferences warns when a shortcut is taken by the
other one, by another app, or by macOS itself in System Settings › Keyboard ›
Keyboard Shortcuts, which Carbon cannot detect. A shortcut that cannot act
beeps and leaves the reason in the menu.

## Notifications

With the app running and allowed, the daemon still applies every gate (quiet
hours, mute, snooze, cooldown) and then hands each notification to the app,
which shows it through Notification Center. Clicking it focuses the pane the
way an Alerter click does; the banner's actions mute the session, snooze for an
hour, or open the ASL answer panel. If the app does not confirm delivery
within 3 seconds, or reports a failure, the daemon withdraws it and Alerter
shows it instead. Quitting the app falls back to Alerter at once.

```bash
amx notifications test   # "confirmed via app" when the app showed it
```

The first notification after a fresh install usually goes to Alerter: the
permission prompt it raises outlasts the 3-second limit.

## Checking the pairing

```bash
amx daemon status        # the companion line names each subscriber and its version
amx doctor --strict      # companion_app (info) per connected app
amx debug subscribe --frames 5   # the same stream the app reads
```

`amx doctor` raises `companion_build_mismatch` (warning) when the app reported
an `amx` build that is not the daemon's. The app reports the `amx` it resolved
in `subscribe.amx_build`, with the full sha256 of the executable rather than
the prefix `amx --version` prints, and subscribes again whenever a new
resolution selects a different binary; until one is resolved it reports none
and nothing is compared. Preferences shows which `amx` binary the app resolved
and why.

## Troubleshooting

**"daemon too old — run `amx daemon restart`".** The running daemon predates
the companion stream (it answered `subscribe` as an unknown request). The app
falls back to polling `health` every 10 seconds and retries `subscribe`; menu
items that need the daemon are greyed out with the same advice. Restart the
daemon after installing a current `amx`.

**`not running` in the menu.** No daemon owns the socket. The app never starts
one on its own; choose **Start daemon** or run `amx daemon start`.

**`reconnecting` or `unresponsive`.** The stream ended or went quiet. The app
redials with backoff, reconnects within 0.5s after a `bye` (as after
`amx daemon restart`), and treats 30 seconds without a frame as a half-open
socket. A wake from sleep reconnects at once.

**Build mismatch.** `companion_build_mismatch` means the app runs an `amx`
that is not the daemon's build, usually after `just install` without
`just mac-install`, or the reverse. Reinstall the side that is behind. The app
prefers `~/.local/bin/amx` over its bundled copy when the local one matches
the daemon, is newer, or is the same clean build; to pin one, set
Preferences › amx binary.

**Grayed-out menu items.** The resolved `amx` lacks the command
(`project`, `open-dashboard`, `mute-toggle --json`); the tooltip says which.
Install a current `amx`.

**Kitty does not come forward, or shows the wrong Space.** The app is an
accessory app and activates Kitty cooperatively: it yields activation to the
Kitty process that owns the socket and asks it to activate. macOS 14 and later
may decline a cooperative activation when the app itself was not active, and
when only one Kitty runs the app also opens it through Launch Services, which
macOS does honor. With several Kitty instances that fallback cannot name one,
so it is skipped. Kitty focuses the tab and window either way; whether macOS
switches to the Space holding that OS window is governed by System Settings ›
Desktop & Dock › "When switching to an application, switch to a Space with
open windows for the application". The app deliberately does not use
AppleScript or Accessibility to force a Space switch.

**Notifications never arrive in the app.** Check System Settings ›
Notifications › Agent Mux and Preferences › Use app notifications. After an
ad-hoc install the permission resets; allow it again. `amx debug tail` names
the backend each dispatch used (`app` or `alerter`).

**Logs.**

```bash
log show --last 5m --predicate 'subsystem == "io.github.sglavoie.agentmux"'
```

## Known limits

- **Mute lag.** A mute made from the app (or `amx mute-toggle`) is written to
  Kitty at once, but the daemon learns it on its next Kitty listing, at most
  one render cadence (2 seconds) later. A notification raised inside that
  window is not yet suppressed. The menu row shows a mute made from the app
  at once and keeps showing it until a snapshot agrees, for up to 5 seconds,
  so reopening the menu right away does not offer the toggle again. See
  [Daemon › Mute lag](daemon.md#mute-lag) for why there is no `mute_set`
  request.
- **Paused ASL runs** are listed for answering only when their pause left an
  open supervisor question.
- **One Kitty per window ID.** Window IDs mean something only inside the Kitty
  that issued them, so Mute never tries another instance's socket.
- **Other Kitty instances.** The daemon lists the instance whose hook it heard
  last and, on the same cadence, each other instance that has a session, so
  those sessions are named from their own tabs. Each is kept until its own
  instance stops reporting the window or that Kitty exits, never because a
  listing of another instance could not see it. Window IDs are still bare, so
  two instances' sessions that share a window number share one daemon state.
- **Codex hooks run in a shared process.** Codex 0.160 runs hooks inside its
  managed `app-server`, which carries the Kitty window of the pane that started
  it, so every Codex session's hooks are attributed to that one window.
