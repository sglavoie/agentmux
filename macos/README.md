# Agent Mux.app

A native macOS menu bar companion for Agent Mux. It is an accessory app (no
Dock icon) that talks to the always-on amx daemon over its Unix socket and runs
the `amx` CLI for actions. Kitty stays the terminal: the app never hosts a
session itself.

The app shows the live managed sessions, with attention counts in the menu bar,
and a click focuses a session's Kitty tab and brings Kitty to the front. Two
global shortcuts reach the dashboard and the sessions waiting on you from any
app. The daemon's notifications arrive as native banners while the app runs,
and ASL supervisor questions are answered in a native panel.

## Requirements

- macOS 14 or later.
- Xcode (the `xcodebuild` command line tools), `curl`, `shasum`, `unzip`, and
  `ditto`.
- The rest of the repository's toolchain (Go, `just`) for a Release build,
  which bundles `amx`.

XcodeGen is pinned in `XCODEGEN_VERSION`, by version and by the sha256 of its
release zip. `scripts/generate-project.sh` uses a `xcodegen` on `PATH` only if
it reports exactly that version. Otherwise it downloads the release zip,
verifies its checksum, and unpacks it into `macos/.cache/`. To bump XcodeGen,
change both values together.

## Recipes

None of these is part of `just check`, `just acceptance`, or
`just paired-release-gate`. Those gates must run on a machine without Xcode,
and `cmd/release`'s `TestMacAppRecipesStayOutOfTheGates` keeps it that way.

| Recipe | What it does |
| --- | --- |
| `just mac-app` | Debug build to `macos/build/Agent Mux.app`. It bundles `amx` only when `AMX_BINARY` names one. |
| `just mac-app Release` | Runs `just build`, then builds Release and bundles that `amx` at `Contents/Resources/bin/amx`. |
| `just mac-test` | Runs the hosted `AgentMuxTests` XCTest suite (`xcodebuild test`). |
| `just mac-install` | Release build. It quits a running copy, `ditto`s the app to `/Applications`, re-registers it with Launch Services, and relaunches it. |

The project file `macos/AgentMux.xcodeproj` is generated from `project.yml` and
is gitignored, so edit `project.yml` instead. `macos/build/`,
`macos/DerivedData/`, and `macos/.cache/` are gitignored as well, so a build
leaves `git status` clean for the paired release gate.

### Signing

`scripts/sign.sh` signs inside out: the bundled `amx` first (identifier
`io.github.sglavoie.agentmux.cli`), then the app.

- **Default.** The first valid Apple Development identity in the keychain. A
  certificate gives the app a designated requirement that survives a rebuild,
  so a notification permission is granted once rather than after every install.
- **No identity.** Without one the app is signed ad-hoc, and macOS treats every
  build as a new app.
- **`AMX_CODESIGN_IDENTITY`.** Overrides the identity. `-` forces ad-hoc.
- **Developer ID.** A Developer ID identity adds the hardened runtime and a
  secure timestamp. With `AMX_NOTARY_PROFILE`, the name of an
  `xcrun notarytool store-credentials` profile, the app is also notarized and
  stapled.

### Other environment

- `AMX_MACOS_CACHE`: the cache root (default `macos/.cache`).
- `AMX_APP_INSTALL_DIR`: the install destination (default `/Applications`).
- `AMX_APP_NO_LAUNCH=1`: install without relaunching.

### Icons

The PNGs in `AgentMux/Resources/Assets.xcassets` are committed. They are drawn
from code by `scripts/make-icons.swift`, so rerun it only when the artwork
changes:

```sh
swift macos/scripts/make-icons.swift
```

### Notarizing a release build

Notarization needs a Developer ID Application certificate and a notary
credentials profile, stored once:

```sh
xcrun notarytool store-credentials agentmux-notary \
  --apple-id you@example.com --team-id TEAMID --password APP_SPECIFIC_PASSWORD
AMX_CODESIGN_IDENTITY="Developer ID Application: …" \
  AMX_NOTARY_PROFILE=agentmux-notary just mac-app Release
```

`sign.sh` then submits the signed app, waits for the verdict, and staples the
ticket. An Apple Development build is enough for your own machine; notarize
only to hand the app to someone else.

## Layout

| Path | Holds |
| --- | --- |
| `project.yml` | The XcodeGen spec for the `AgentMux` app and the hosted `AgentMuxTests` target. |
| `AgentMux/App/` | `main.swift`, `AppDelegate` (wiring), and the single-instance check. |
| `AgentMux/Daemon/` | The socket path, line-framed socket, request client, subscription client, and the wire and companion models. |
| `AgentMux/CLI/` | Resolving, describing, and running `amx`: binary resolver, version, `--help` capabilities, action gating, login-shell environment, detached spawn. |
| `AgentMux/Kitty/` | Choosing a Kitty socket and bringing the owning Kitty forward. |
| `AgentMux/Model/` | Pure state: the session store, glyphs, attention cycler, project and recent catalogs, snooze, and the ASL response rules. |
| `AgentMux/Shell/` | macOS integration: hotkeys, preferences storage, notification presenter. |
| `AgentMux/UI/` | The status item, menu builder, daemon line, Preferences window, and ASL answer panel. |
| `AgentMux/Resources/` | `Info.plist` and the asset catalog; `internal/palette/icons.json` is bundled from the repository. |
| `AgentMuxTests/` | XCTest suites, with a fake stream server standing in for the daemon. |
| `scripts/` | `generate-project.sh`, `build-app.sh`, `sign.sh`, `install-app.sh`, `test-app.sh`, `make-icons.swift`. |

The operator's guide (install, permissions, Kitty requirements,
troubleshooting) is [docs/companion-app.md](../docs/companion-app.md).

## Which amx the app runs

`AMXBinaryResolver` picks the first match:

1. **Override.** The `amxBinaryPath` default:
   `defaults write io.github.sglavoie.agentmux amxBinaryPath /path/to/amx`. An
   override that names no executable is an error. The app does not fall back
   to another amx.
2. **Bundled.** The copy bundled in the app.
3. **`~/.local/bin/amx`.** Where `just install` puts it.
4. **`PATH`.** The first `amx` on the login shell's `PATH`.

When both a bundled copy and `~/.local/bin/amx` exist, the local copy wins in
four cases:

- **It matches the daemon.** It is the build the running daemon reports in
  `health.build`. The content hash decides when both sides carry one;
  otherwise version, revision, and dirty marker must all agree.
- **It is newer.** Its `built` time is later. That field is `vcs.time`, the
  commit time, not the link time.
- **It is the same clean build.** Both are clean builds of one version and
  revision, so they differ only by the signature `scripts/sign.sh` puts on the
  bundled copy. The local copy is the CLI you run, so a daemon the app starts
  is one `amx doctor` does not report as a `build_mismatch`.
- **It wins a tie.** On equal commit times, which two dirty builds of one
  commit share, it wins when its content differs and its file was written
  more recently.

Preferences shows which binary was chosen and why.

## How the app runs amx

- **Environment.** The app is launched by Finder or launchd, so it does not
  inherit your shell's environment. It asks your login shell (`$SHELL -l -i`)
  for `PATH`, every `XDG_*` variable, and every `AMX_*` variable, so
  `AMX_CONFIG` and friends match the terminal's.
- **Kitty.** amx learns which Kitty to talk to only from `KITTY_LISTEN_ON`.
  Every invocation strips any inherited `KITTY_*` variables and sets
  `KITTY_LISTEN_ON` explicitly when a command needs Kitty.
- **Output.** `--json` output is decoded through the schema-v1 envelope.
  Unknown fields are ignored. A data envelope with a nonzero exit is a partial
  success. The error envelope's `code`, `candidates`, and `parameters` are kept.
- **Capabilities.** The app reads `--help` once per binary to learn which
  commands the chosen amx offers (`project`, `open-dashboard`,
  `notifications snooze`, `mute-toggle --json`). An older amx refuses an
  unknown command, so the app greys out what is missing, with the reason as
  the item's tooltip, rather than failing when it is used.

## The live feed

The app holds one companion subscription open to
`~/.local/share/agent-mux/daemon.sock`, the same path `ipc.SocketPath`
computes (`Daemon/SubscriptionClient.swift`). It sends `subscribe` as the
client `Agent Mux.app` with its version, reads the ack, and then reads frames.
Every snapshot frame replaces the menu's state wholesale.

- **Reconnect.** When the stream ends, the client dials again. The delay
  doubles from 0.5s to 10s and drops back to 0.5s once a subscription is
  acknowledged. After a `bye` (such as `amx daemon restart`) it retries at
  0.5s, so a restart reconnects without relaunching the app.
- **Heartbeat watchdog.** The daemon sends a heartbeat every 15s (the ack's
  `heartbeat_ms`). If no frame arrives for two intervals, the client treats the
  socket as half-open and reconnects. A wake from sleep reconnects at once.
- **Tolerant decoding.** Unknown fields are ignored, and a field of an
  unexpected type falls back to its default. Unknown frame types are skipped.
  A line that is not a frame is counted and skipped.

The status item's title shows only the non-zero counts, with the glyphs from
`internal/palette/icons.json`, which is bundled as a resource: `⚡2 🛑1 🔑1`.
The menu lists the sessions in three sections:

- **Needs attention**: rows like `🔑 api · claude · 4m · permission`, with the
  longest-waiting first.
- **Working**: the `bolt`, `subagent`, and `compact` sessions.
- **Idle**: everything else. More than 10 rows collapse into a submenu.

## The daemon line

The menu's first line says where the feed stands:

| Line | Meaning |
| --- | --- |
| `ready · 0.8.0 (52189e529adf-dirty)` | The feed is live and the daemon is ready. Shows its version and revision. |
| `degraded · <reason>` | The daemon answered but is not ready. |
| `not running` | There is no socket, or nothing accepts on it. A **Start daemon** item follows. |
| `reconnecting · <reason>` | The stream ended; the client is dialing again. |
| `unresponsive · <reason>` | Something accepted the connection but did not answer in time. |

A daemon older than the companion protocol answers `subscribe` with
"unknown request type". The menu then shows "daemon too old — run
`amx daemon restart`". The app falls back to a `health` request every 10
seconds and on every menu open, and it retries `subscribe` every 10 seconds.

The app never starts the daemon on its own. **Start daemon** runs the resolved
`amx daemon start` in a session of its own, detached from the app, only when
the operator chooses it.

## Focus

A click on a session row sends `focus_window` to the daemon. The request names
a Kitty socket, which the daemon accepts only if it learned that socket itself
(`Kitty/KittyEndpoint.swift`). The app tries these sockets in order, and moves
to the next on `unknown_socket`:

1. the Kitty socket set in Preferences, if any;
2. the session's own socket;
3. the socket the snapshot's window tree was read through;
4. `unix:/tmp/kitty-<pid>` for each running `net.kovidgoyal.kitty` process,
   only when that file exists.

If there is nothing to try, the menu explains that Kitty needs
`allow_remote_control yes` and `listen_on unix:/tmp/kitty` in `kitty.conf`.

Once the daemon has focused the window, `Kitty/KittyActivator.swift` brings
the Kitty process that owns the socket to the front. It finds that process from
the pid in the socket's name, so with two Kitty instances the right one comes
forward. It calls `NSApp.yieldActivation(to:)` and then asks Kitty to
activate. When that is the only Kitty running, it also opens Kitty through
`NSWorkspace.openApplication` with `activates`, which brings a running app
forward even when Agent Mux was never active, as after a global shortcut. With
several Kitty instances that call cannot say which one, so it is skipped. A `stale_target` reply means the session has closed. The row is hidden
until a snapshot no longer lists it, and the menu says so.

## Menu actions

After the sessions, the menu offers these (`Model/ProjectCatalog.swift`,
`Model/RecentSessions.swift`, `Model/NotificationSnooze.swift`,
`CLI/ActionGate.swift`):

| Item | Does |
| --- | --- |
| Open Dashboard | The same `amx open-dashboard` the ⌃⌥⌘A shortcut runs. |
| Launch Project ▸ | Configured projects, one header per profile, from `amx project list --json`. The list is fetched at launch, cached, and refreshed when the menu opens on one older than 60s. Each project's directory is checked on disk when the list arrives and every time the menu opens; a project whose directory is missing stays listed but greyed out, with the path in its tooltip. Choosing one runs `amx project launch --title T --profile P --json`. |
| Recent ▸ | The newest `amxRecentLimit` conversations from `amx session list --since <amxRecentDays>d --json`. The list runs in the background with a 15s timeout, and the menu shows the previous result until it lands. Choosing one runs `amx session continue harness:id --json`. |
| ⌥ on a session row | Shows `Mute · …` or `Unmute · …`, which runs `amx mute-toggle --window-id N --json` with `KITTY_LISTEN_ON` set to that row's own socket. |
| Notifications ▸ | Snooze for 1 hour, until tomorrow 08:00, or until resumed, and Resume Now, all through the daemon's `notification_snooze` request. Quiet hours are shown read-only with the config path they come from, because the app never rewrites `config.toml`. |

New tabs open in the Kitty that was frontmost last (the app watches
application activation), then the Preferences override, the snapshot's Kitty,
and each `/tmp/kitty-<pid>`. After a launch or a continue, the app asks the
daemon to focus the window amx reported, when it reported one, and brings that
Kitty forward. A Recent conversation that a live session already runs is
focused through `focus_window`, in whichever Kitty runs it, instead of asking
amx: `session continue` only sees the Kitty it is pointed at, so for a session
in another instance it would resume a second copy. Mute never tries another
socket, because window IDs mean something only inside the Kitty that issued
them.

Items are greyed out when the resolved amx lacks `project` (Launch Project),
`open-dashboard` (Open Dashboard), or `mute-toggle --json` (the ⌥ Mute
alternates). Notifications is greyed out while the feed is not live, and when
the daemon answers that it has no `notification_snooze`.

**Snooze across daemon restarts.** The daemon keeps the snooze in memory only.
The app mirrors the snooze each snapshot reports into `amxSnoozeUntil`
(`distantFuture` for "until resumed"), and records which daemon reported it as
`pid:started_at` in `amxSnoozeDaemon`. When a snapshot comes from a different
daemon that has no snooze while the stored one has not run out, the app sends
the stored snooze to the new daemon. A snooze cleared on the same daemon, for
example with `amx notifications snooze off`, is adopted rather than undone.

## Notifications

While it runs, the app can be the daemon's notification backend
(`Shell/NotificationPresenter.swift`). The daemon still applies every gate
(quiet hours, mute, snooze, cooldown). It then sends each notification that
passes them as a `notification` frame to the subscriber holding the role, and
the app shows it through `UNUserNotificationCenter`.

**The role.** The app claims the role in `subscribe` while
`amxUseAppNotifications` is on and macOS allows its notifications. It also
claims the role while it has never been asked, because the daemon sends frames
only to a claimant. It asks for permission when the first frame arrives, not
at launch. Turning the preference off, a denied prompt, or permission revoked
in System Settings drops the claim, and the app subscribes again without it.
The app reads the permission again every 10 seconds and whenever the menu
opens, because macOS gives no notice of a change.

**Fallback.** The app reports `delivered` once `add` succeeds. A failed `add`,
a denied permission, or the preference turned off reports `failed`. The daemon
then sends that notification through Alerter, as it does when no app holds the
role, or when `delivered` takes longer than 3 seconds. That 3-second limit
covers the first permission prompt, so the notification that raised the prompt
usually ends up on Alerter.

**Banners.** Each banner's identifier is the daemon's `notification_id`, and
its thread is the window ID, so one session's banners group together. The
category depends on the actions the daemon offered, and a banner never shows
an action that was not offered:

| Category | Actions | Used for |
| --- | --- | --- |
| `amx.session` | Mute session, Snooze 1h | A session notification |
| `amx.session.snooze` | Snooze 1h | One with no pane to mute |
| `amx.asl` / `amx.asl.snooze` | Answer…, then the offered session actions | An ASL question |
| `amx.plain` | none | Permission prompts, which get no actions, and any notification offered none |

**Responses.** A click reports `clicked`, and the daemon focuses the pane
exactly as it does for an Alerter click. The app then brings that Kitty
forward. A dismissal reports `dismissed` (every category sets
`customDismissAction`). An action reports `action_mute`, `action_snooze`, or
`action_answer`, and the app carries it out:
- Mute runs `amx mute-toggle` on that session, unless the session is already
  muted, and only when exactly one listed session has that window ID.
- Snooze sets the same 1-hour snooze as the Notifications menu.
- Answer opens the [ASL answer panel](#asl-answer-panel) on the notice's run.

A `notification_retract` frame removes the banner. A retracted notification
that is still waiting on the permission prompt, or on `add`, is never shown.
The `amx debug tail` dispositions name the `app` backend.

## ASL answer panel

An ASL run waiting on its supervisor shows ❓ on its session row, and an
indented **Answer…** item follows that row (`Answer… (3 questions)` when more
than one is open). A run binds to the session that names it, else to the one
listed session with its window ID; a run no listed session claims, or whose
window ID is ambiguous across Kitty instances, is listed under **Supervisor
questions** instead. The `amx.asl` banner's Answer… opens the same panel.

The panel (`UI/ASLQuestionPanel.swift`) floats above other windows and does not
activate the app until the reply editor takes focus. It reads the daemon, never
`amx`:

1. `asl_snapshot` finds the run's row, its permitted actions, and its action
   token. The menu's waiting list carries neither a body nor a token.
2. `asl_question` fetches one question body at a time, as you move between the
   run's open questions.
3. `asl_action` sends each submission once. It is never resent automatically.

The rules are ported from `tui/asl` into `Model/ASLResponseState.swift`, so
the panel and the TUI's Runs view agree:

- A live wait offers **Reply**, **Continue**, and **Pause**. A paused run
  offers only **Reply and resume**. Reply needs text; ⌘↩ sends it.
- The form opens only when the daemon reports ASL reachable, the snapshot is
  not stale, mutations are enabled, the row carries a token, and the row
  permits the method. Otherwise the panel shows the same fixed sentence the TUI
  shows, including ASL's own `disabled_reasons`.
- A failed submission keeps its operation ID, so retrying the same button after
  an unknown outcome replays the bridge's receipt instead of applying it twice.
  Another button mints a new ID.
- After an accepted closure the panel moves on to the run's next open
  question. That question's fetch re-mints the token, and nothing is sent
  until it has. The last question, or a resume, closes the panel.
- A refusal is shown as a fixed sentence chosen by its bridge code, never the
  bridge's message. A refusal meaning the run moved (a stale token, a closed
  question, a stale snapshot) closes the panel, shows a notice in the menu,
  and rebuilds the menu.

The question body and every draft are held only while the panel is open, and
closing it drops them. `SecretText` and the state's `description`, `dump`
mirror, and request descriptions are all redacted, so the logs (category
`asl`) name only methods, run, question, and operation IDs, and refusal codes.

## Global shortcuts

Two Carbon hotkeys (`Shell/HotKey.swift`), which need no Accessibility
permission:

| Default | Does |
| --- | --- |
| ⌃⌥⌘A | Runs `amx open-dashboard --json`, which focuses the running dashboard or opens one, then brings that Kitty forward. |
| ⌃⌥⌘N | Focuses the next session needing attention (`Model/AttentionCycler.swift`). |

The dashboard shortcut names a Kitty socket the same way focus does: the
Preferences override, the running dashboard's Kitty, the snapshot's, then each
`/tmp/kitty-<pid>`. amx dials the socket itself, so only `kitty_unavailable`
moves on to the next one.

The next-attention shortcut orders the attention rows (stop, key, error,
question) by the oldest `presentation_changed_at_ms`, the same order as the
menu's Needs attention section. Each press focuses the one after the session
the last press focused, and wraps after the last one. A session is its Kitty
socket plus its window ID, so the cycle spans Kitty instances. When the session
it last focused no longer waits, the cycle carries on from where that session
stood instead of starting over. With nothing waiting, it beeps.

A shortcut that cannot act beeps and leaves the reason as the menu's notice.
Preferences records shortcuts, and a change is registered at once. It warns
when a shortcut is already used by the other one (the dashboard's wins), is
held by another app (Carbon's `eventHotKeyExistsErr`), or is one macOS owns in
System Settings › Keyboard › Keyboard Shortcuts, which Carbon cannot see. While
a recorder listens, both shortcuts are unregistered so they can be recorded.

## Preferences

Preferences (`UI/PreferencesWindow.swift`, SwiftUI in an NSWindow) saves each
change as it is made, in the `io.github.sglavoie.agentmux` defaults domain.
An absent, mistyped, or out-of-range value reads as its default.

| Key | Default | Meaning |
| --- | --- | --- |
| `amxBinaryPath` | empty | The amx to run; empty is automatic. Preferences shows the running path and version. |
| `amxLaunchAtLogin` | false | Mirrors the `SMAppService.mainApp` login item, which survives a logout. |
| `amxHotKeyDashboard` | `ctrl+opt+cmd+a` | Empty turns it off. |
| `amxHotKeyNextAttention` | `ctrl+opt+cmd+n` | Empty turns it off. |
| `amxStatusStyle` | `glyphs` | `glyphs` (`⚡2 🔑1`), `compact` (the attention total), or `iconOnly`. |
| `amxShowWorkingCount` | true | Includes `⚡N` in the glyph title. |
| `amxRecentDays` | 7 | How far back the Recent submenu looks (1–90). |
| `amxRecentLimit` | 15 | How many conversations it lists (1–50). |
| `amxUseAppNotifications` | true | Offers the app as the daemon's notification backend. |
| `amxSnoozeUntil` | none | The last snooze the daemon reported, re-applied after a daemon restart (see Menu actions). The daemon owns the snooze, and Preferences shows the daemon's while the feed is live. |
| `amxKittySocketOverride` | empty | A Kitty socket tried first by focus and the dashboard shortcut. |
| `amxEnvironment` | empty | Values amx runs with instead of the login shell's, for `PATH`, `XDG_*`, and `AMX_*`. |

A change to the binary override or an environment value resolves amx again
once typing pauses; the login shell is not asked twice. The login item is
SMAppService's to keep. At launch the stored choice is set to match it, so a
login item removed in System Settings is not added back.

## Single instance and tests

A second copy of the app exits at launch if one is already running. The tests
run hosted inside the app. When the app sees XCTest in its environment, it
skips the single-instance check and the status item, and it never spawns
`amx`. This means `just mac-test` works while the installed app is running.

## Logs

The app logs to the unified log under the subsystem
`io.github.sglavoie.agentmux`:

```sh
log show --last 5m --predicate 'subsystem == "io.github.sglavoie.agentmux"'
```
