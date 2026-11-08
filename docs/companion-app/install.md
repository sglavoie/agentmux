# Installation and permissions

[Back to the companion guide](../companion-app.md)

## Install

For a release download, you need macOS 14 or later and an installed,
authenticated agent CLI. Download `Agent-Mux-<version>-macos-<arch>.zip` from
[Releases](https://github.com/sglavoie/agentmux/releases), choosing
`arm64` for Apple silicon or `amd64` for Intel when that architecture is
available. Expand it, move Agent Mux.app to `/Applications`, and open it.
If no compatible download is listed, build from source below.

The bundle includes `amx`, tmux, and Ghostty. You do not need Go, Xcode,
Homebrew, or a separately installed `amx` to run it. On a fresh installation,
the workspace opens with **Set Up First Project** before a daemon is needed.
Choose the agent's existing authenticated home and a project directory, keep
the tmux default, and explicitly choose whether to sync its hooks. Press
Enter after the summary, choose **Start Daemon** if offered, then
**Launch Project**. Invalid or unreadable existing configuration is reported
as an error rather than replaced by first-project setup.

Checkout review and worktree actions need Git. When Apple's Git is unavailable,
background status reads leave Git metadata empty; they do not open the developer
tools installer. Install Git separately to use those checkout features.

For command-line access without a separate installation, the bundled binary is:

```bash
"/Applications/Agent Mux.app/Contents/Resources/bin/amx" --version
```

### Building from source

Build requirements: macOS 14 or later, Xcode command line tools, and the
repository's usual toolchain (Go, `just`). The first build also builds the
embedded terminal engine from source, which needs `git`, network access,
several minutes, and about 5 GB under `macos/.cache/`; later builds reuse it.
See [GhosttyKit](../../macos/docs/building.md#ghosttykit).

```bash
just install        # amx and the daemon first: the app needs a daemon that streams
just mac-install    # Release build, then install or update /Applications/Agent Mux.app and relaunch it
```

Install `amx` first. The app talks to whichever daemon is running, and a
daemon older than the companion protocol leaves the app in degraded mode (see
[Troubleshooting](operations.md#troubleshooting)). After an upgrade of the pair, reinstall
the app too, so its bundled `amx` and the installed one stay close.

The app must live in `/Applications` for the login item to work, which is
where `just mac-install` puts it (`AMX_APP_INSTALL_DIR` overrides it, at the
cost of the login item).

**Nothing else needs installing.** The bundle carries its own `amx` and its
own tmux: a pinned release, built from checksummed sources and linked
statically, so the app runs on a Mac with no Homebrew and no tmux. Before
`amx` runs that tmux it checks every file of it against the checksums sealed
into the bundle and copies it to `~/.local/share/agent-mux/runtime/`. The copy
is what runs, so the tmux a session server was started from is never inside
the app.

### Updating

For a downloaded app, quit Agent Mux, expand the new release ZIP, replace
the app in `/Applications`, and reopen it. Do not run **End Session** or
`amx backend stop` as part of an update. If macOS says the app is in use,
make sure it has quit from its menu before replacing it.

For a source installation, run `just mac-install` again. The installer checks the new bundle (signature,
`amx`, tmux against its checksums), quits the app, stages the new copy beside
the old one, and renames it into place.

**Agents keep running through an update.** They live in the session server,
not in the app, and the installer never stops, restarts, or replaces that
server: it reads the server's identity before and after and fails if it
changed. When the new app opens, the same agents are in its sidebar. If the
update ships a newer tmux, the running server keeps the tmux it was started
with until its last agent has ended; the next server starts with the new one.
`amx backend status` shows which is in use.

### Removing

```bash
just mac-uninstall                                # the app only
just mac-uninstall --end-sessions                 # and end every running agent
just mac-uninstall --end-sessions --remove-app-data --yes
```

Removing the app removes the app. What else happens is yours to choose:

- **Running agents keep running** unless you pass `--end-sessions`. Without
  it the uninstaller says how many it left, and prints the one command that
  ends them later without `amx`.
- **Your data stays.** `config.toml`, conversations and their indexes,
  handoffs, worktrees, and the session registry are never touched.
  `--remove-app-data` removes only what is the app's alone: its preferences
  (the `io.github.sglavoie.agentmux` defaults domain), its saved window
  state, and the tmux runtime it installed. It is refused while agents run on
  that runtime unless you end them too.

Both choices ask before acting; `--yes` answers for a script.

## Permissions

The app needs no Accessibility, Automation, or Screen Recording permission.
Two grants matter:

- **Notifications.** The app asks the first time the daemon sends it a
  notification, not at launch. While it has never been asked, or while it is
  allowed, it claims the daemon's notification backend role; when it is denied
  or turned off in Preferences, the daemon uses Alerter as before. Change it
  later in System Settings › Notifications › Agent Mux. The app rereads the
  permission whenever it comes to the front and whenever its menu opens.
- **Login item.** Settings › Open at Login registers the app with
  `SMAppService`. It shows in System Settings › General › Login Items, and
  removing it there is respected: the app does not add it back.

macOS ties both grants to the app's code signature. A build signed with an
Apple Development or Developer ID certificate keeps them across rebuilds. An
ad-hoc build (no certificate in the keychain, or `AMX_CODESIGN_IDENTITY=-`)
looks like a new app every time, so both grants are asked for again after each
install. Every copy shares the bundle identifier, so the build scripts sign
the `DerivedData` copies with the same certificate: an ad-hoc copy answering a
prompt would otherwise take the grant from the installed app. See
[Signing](../../macos/docs/building.md#signing).
