# Building and distributing Agent Mux.app

[Back to the macOS development guide](../README.md)

## Requirements

- macOS 14 or later.
- Xcode (the `xcodebuild` command line tools), `curl`, `shasum`, `unzip`, and
  `ditto`.
- For the first build, or after a Ghostty bump: `git`, `tar`, network access,
  and about 5 GB under `macos/.cache/` to build GhosttyKit (see
  [GhosttyKit](#ghosttykit)). Xcode's Metal Toolchain component is downloaded
  if it is missing.
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
| `just mac-app` | Debug build to `macos/build/Agent Mux.app`. It bundles `amx` only when `AMX_BINARY` names one, and always the tmux runtime. With `AMX_MAC_TEST_HOST` set, Xcode runs on that Mac and the product is brought back. |
| `just mac-app Release` | Runs `just build`, then builds Release and bundles that `amx` at `Contents/Resources/bin/amx` and the tmux runtime at `Contents/Resources/tmux`. |
| `just mac-test` | Runs the hosted `AgentMuxTests` XCTest suite (`xcodebuild test`). `just mac-test Release` runs it against the optimized app, built with testability into `macos/DerivedData/ReleaseTests`. A run that executes no test fails. With `AMX_MAC_TEST_HOST=user@host` set the suite runs on that Mac instead (see below). |
| `just mac-ghosttykit` | Builds the pinned GhosttyKit into `macos/Vendor/`. `mac-app` and `mac-test` run it themselves; it does nothing when the vendored build is current. |
| `just mac-tmux` | Builds the pinned tmux runtime into `macos/Vendor/tmux/`. `mac-app` runs it itself; it does nothing when the vendored build is current. |
| `just mac-install` | Release build, then `scripts/install-app.sh`: a first install or an update of `/Applications/Agent Mux.app` that leaves the session server and its agents running (see [Install, update, uninstall](#install-update-uninstall)). |
| `just mac-uninstall [flags]` | `scripts/uninstall-app.sh`: removes the app. Agents keep running and data stays unless `--end-sessions` or `--remove-app-data` says otherwise. |
| `just mac-release-gate [flags]` | The app's release gate: builds the Release bundle, checks it with `scripts/release-gate.sh` (seal, tmux runtime, that its `amx` is this checkout's, how it is signed), runs the hosted suite against the Debug and the Release app, and writes an evidence record. It installs nothing. With no Xcode to be had the record says `pending` and the recipe fails. See [Desktop releases](../../docs/desktop-releases.md). |

The project file `macos/AgentMux.xcodeproj` is generated from `project.yml` and
is gitignored, so edit `project.yml` instead. `macos/build/`,
`macos/DerivedData/`, `macos/Vendor/`, and `macos/.cache/` are gitignored as
well, so a build leaves `git status` clean for the paired release gate.

### GhosttyKit

The embedded terminal is libghostty, linked statically as
`GhosttyKit.xcframework`. Nothing prebuilt is committed or fetched:
`scripts/build-ghosttykit.sh` builds it from source and vendors it, with
`ghostty.h` and the resources the app bundles (themes, shell integration,
terminfo), into `macos/Vendor/`. The build reads no checkout outside this
repository.

- **Pins.** `GHOSTTY_VERSION` names the Ghostty tag, the commit that tag must
  resolve to, the Zig version, and the checksums of the Zig tarballs. A tag
  that resolves to another commit is refused.
- **Patches.** `patches/ghostty/*.patch` are applied to a clean checkout of the
  pinned commit before every build. Each exists because the pinned tag needs
  it on a current Xcode; re-check them on a bump.
- **Stamp.** `Vendor/.stamp` records the tag, commit, Zig version, target, and
  a digest of the patches. A matching stamp skips the build, so changing any
  pin or patch rebuilds. `GHOSTTYKIT_FORCE=1` rebuilds regardless.
- **Zig.** A `zig` on `PATH` is used only if it reports the pinned version,
  then `mise` if installed, else the checksummed tarball unpacked into
  `macos/.cache/zig/`. On an SDK whose stubs Zig cannot link against, the
  script builds an SDK shim under `macos/.cache/sdk-shim/` for host tools.
- **Target.** `GHOSTTYKIT_TARGET=native` builds for this Mac's architecture
  only; the default `universal` is what a distributed app needs.

The first build takes several minutes. The pins, patches, and script began as a
copy of [sglavoie/tt](https://github.com/sglavoie/tt)'s and are kept
byte-identical to its pins and patches while both embed the same tag.

The Swift sources under `AgentMux/Ghostty/` are ported and trimmed from
Ghostty's macOS app. `NOTICE-ghostty` carries its MIT license and is bundled
into the app.

### The tmux runtime

The app ships the tmux its session server runs, so a Mac needs nothing
installed. `scripts/build-tmux.sh` builds it from the release tarballs pinned
in `macos/TMUX_VERSION` (tmux, libevent, ncurses, utf8proc, jemalloc), each
checked against its sha256 before it is unpacked, with no pkg-config lookup
and a `PATH` of system directories only, so nothing from a package manager
can be linked. The four libraries are linked statically: the result is one
executable that loads only `/usr/lib` (the script fails if `otool -L` says
otherwise, or if the binary does not report the pinned version), built for
this Mac's architecture and the app's deployment target. jemalloc is there
because tmux asks for it on macOS, whose `calloc` it found unreliable; the
whole archive is force-loaded so its zone registration is linked in. A
matching stamp in `macos/Vendor/tmux/.stamp` (versions, architecture, a digest
of the pins and of the script) skips the build; a cold one takes a few
minutes. Terminal descriptions come from the system's terminfo database.

`build-app.sh` copies `bin/tmux`, `VERSION`, and `licenses/` to
`Contents/Resources/tmux`. `sign.sh` signs the executable and then writes
`SHA256SUMS` over every file of the runtime, so the checksums are of the
signed files and are themselves under the app's seal.

At run time `internal/tmux` (`runtime.go`) decides which tmux a server runs:

1. `[defaults.tmux] path` in `config.toml`, else `AMX_TMUX`. An explicit
   choice is final: if it cannot be used nothing else is tried.
2. The bundled runtime: the directory `AMX_TMUX_RUNTIME` names, which the app
   sets for every amx it runs, or `../tmux` beside the directory of the amx
   executable, which is where a bundled amx finds it with no environment at
   all. Every file is checked against `SHA256SUMS`, then the runtime is copied
   to `~/.local/share/agent-mux/runtime/tmux-<version>-<digest>/` and checked
   again there. **The copy is what runs.** A copy that is missing or altered
   is installed again; the directory it replaces is renamed aside, never
   written over.
3. `tmux` on `PATH`, if it is 3.5 or newer: what a standalone amx uses.
4. A runtime an earlier run installed, for an amx started without the app on
   a Mac with no tmux.

**A running server keeps its tmux.** The executable chosen for a server is
recorded beside its socket (`tmux.binary`), and while a server answers on the
socket that executable is used for every client, whatever the list above
would pick now. So an update that ships another tmux changes nothing for live
sessions: neither their server nor the clients that talk to it.
`amx backend status` reports the tmux in use, where it came from, and whether
the running server is on an earlier one.

### Signing

`scripts/sign.sh` signs inside out: the bundled `amx` first (identifier
`io.github.sglavoie.agentmux.cli`), then the app.

- **Default.** The first valid Apple Development identity in the keychain. A
  certificate gives the app a designated requirement that survives a rebuild,
  allowing Screen Recording, Accessibility, and other privacy grants to carry
  forward across builds. See Apple's [code signing requirements guide](https://developer.apple.com/documentation/technotes/tn3127-inside-code-signing-requirements).
- **No identity.** Without one the app is signed ad-hoc, and macOS treats every
  changed build as a new app. The build prints a warning; a keychain lookup
  failure stops the build instead of silently falling back to ad-hoc.
- **`AMX_CODESIGN_IDENTITY`.** Overrides the identity by certificate name or
  SHA-1 fingerprint. Names are resolved to valid fingerprints before reaching
  Xcode. `-` forces ad-hoc. It names a certificate in this Mac's keychain even
  for a remote build, which is re-signed here.
- **Xcode's own products.** `generate-project.sh` records the resolved signing
  identity and team in the generated project, so direct `xcodebuild` and Xcode
  GUI builds use the same certificate as `build-app.sh` and `test-app.sh`
  (`scripts/signing-lib.sh`). Regenerate the project after changing signing
  identities. The copies in
  `DerivedData`, the hosted tests' app among them, share the installed app's
  designated requirement. They share its bundle identifier too, and an ad-hoc
  copy would take the privacy grants (Files and Folders, notifications) the
  installed app was given, so macOS would ask again on every switch between
  them.
- **Developer ID.** A Developer ID identity adds the hardened runtime and a
  secure timestamp. With `AMX_NOTARY_PROFILE`, the name of an
  `xcrun notarytool store-credentials` profile, the app is also notarized and
  stapled.
- **Updates.** Before quitting or replacing the installed app, the installer
  checks that old and new signatures satisfy each other's designated
  requirements. An incompatible update stops with instructions to select the
  original certificate. For an intentional signing migration, set
  `AMX_ALLOW_SIGNING_CHANGE=1`; existing permissions may then need granting
  again once. This also catches an accidental switch to ad-hoc signing.

Signing regression checks (mock keychain and signatures; no permission changes):
`python3 -m unittest discover -s macos/scripts/tests -v` from the repository root.

### Other environment

- `AMX_MACOS_CACHE`: the cache root (default `macos/.cache`).
- `AMX_APP_INSTALL_DIR`: the install destination (default `/Applications`).
- `AMX_APP_NO_LAUNCH=1`: install without relaunching.
- `AMX_APP_SOURCE`: the bundle `install-app.sh` installs (default
  `macos/build/Agent Mux.app`).
- `TMUX_RUNTIME_FORCE=1`: rebuild the tmux runtime even when its stamp matches.

### Install, update, uninstall

`scripts/install-app.sh` treats a first install and an update alike, and is
written around one fact: agents run in the session server, which the app
neither contains nor owns.

1. The new bundle is checked before anything is touched: its seal
   (`codesign --verify --strict --deep`), its `amx`, and its tmux runtime
   against `SHA256SUMS`.
2. The new bundle's `amx` is asked for `backend status`. That verifies and
   installs the runtime under the data directory, and reports the running
   server's generation (its PID and start time) and agent count.
3. The app is quit through an Apple event and waited for.
4. The bundle is copied to `.Agent Mux.app.installing` beside its
   destination, verified there, and renamed into place; the earlier copy is
   moved aside first and put back if the rename fails. Nothing is ever copied
   over a bundle in place.
5. The server's generation is read again. If a server was running and it is
   not the same one, the installer exits nonzero: an update that lost agents
   must not report success.

`scripts/uninstall-app.sh` removes the bundle and nothing else by default.
`--end-sessions` ends the session server (`amx backend stop --yes`, asked of
the installed bundle's amx before it is removed); without it the script
reports how many agents it left running and prints the command that ends them
with the recorded tmux alone. `--remove-app-data` removes the app's defaults
domain, its saved window state, and the installed runtime, and is refused
while a server runs from that runtime unless `--end-sessions` is given too.
Neither script touches `~/.config/agent-mux` or anything else under
`~/.local/share/agent-mux`. `scripts/app-lib.sh` holds what the two share.

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
ticket. With `AMX_MAC_TEST_HOST` set, the other Mac builds with its own default
signature and the bundle is re-signed and notarized here once it is back, so
the certificate and the profile belong in this Mac's keychain, not that one's.
`just release` does all of this, and publishes the result
([docs/paired-releases.md](../../docs/paired-releases.md#publishing-on-github)).
An Apple Development build is enough for your own machine; notarize
only to hand the app to someone else.
