# Agent Mux.app

A native macOS menu bar companion for Agent Mux. It is an accessory app (no
Dock icon) that talks to the always-on amx daemon over its Unix socket and runs
the `amx` CLI for actions. Kitty stays the terminal: the app never hosts a
session itself.

This is a first draft. The app currently shows the daemon line and a
Preferences window. The live session feed, focus, notifications, hotkeys, and
actions are added in later steps, and this file grows with them.

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
three cases:

- **It matches the daemon.** It is the build the running daemon reports in
  `health.build`. The content hash decides when both sides carry one;
  otherwise version, revision, and dirty marker must all agree.
- **It is newer.** Its `built` time is later. That field is `vcs.time`, the
  commit time, not the link time.
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
  unknown command, so the app hides what is missing rather than failing when
  it is used.

## The daemon line

The menu's first line comes from a one-shot `health` request to
`~/.local/share/agent-mux/daemon.sock`, the same path `ipc.SocketPath`
computes. The request is sent on every menu open and every 10 seconds. The line
reads:

| Line | Meaning |
| --- | --- |
| `ready · 0.8.0 (52189e529adf-dirty)` | The daemon is ready. Shows its version and revision. |
| `degraded · <reason>` | The daemon answered but is not ready. |
| `not running` | There is no socket, or nothing accepts on it. |
| `unresponsive · <reason>` | Something accepted the connection but did not answer in time. |

The app never starts the daemon on its own.

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
