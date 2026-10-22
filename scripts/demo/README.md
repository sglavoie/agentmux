# README animation and desktop recording

## README animation

The README embeds `docs/media/demo.svg`, generated from the real dashboard
using synthetic session data and the offline TUI driver. The 21.2-second tour
inspects Claude, Codex, and Copilot output, jumps to a session needing
permission, filters by harness, opens the project switcher, and searches the
command palette. No app windows, tmux sessions, daemon, or live AI requests
are needed.

Requires the repository's Go toolchain and `uv` (which installs the pinned
Rich renderer dependency):

```sh
just readme-media        # regenerate docs/media/demo.svg
just readme-media-check  # fail if the committed SVG is stale
```

Edit the fixture and keyboard tour in `tui/readme_demo_test.go`; adjust SVG
styling in `scripts/demo/render_svg.py`. Every captured frame is checked for
layout issues and expected content before export. The SVG is self-contained,
uses CSS animation, and holds its opening frame for reduced-motion viewers.
Review it in a browser after changing the tour, including at README width.

## Desktop recording on a physical Mac

This tooling drives and captures the real macOS workspace using deterministic
agent stand-ins. It makes no AI requests and does not load agent credentials.
It is local tooling; never run it in GitHub Actions.

The linked desktop video was recorded on a separate MacBook Pro over SSH.
It exercises the actual desktop app; terminal responses come from the
clearly labeled stand-ins, not live AI services.

## Prepare without opening windows

Requires macOS 14+, Xcode, Python 3, the repository's Go/just toolchain, and
`ffmpeg` for encoding. Build from the repository root:

```sh
just build
just mac-app
sh scripts/demo/build-recorder.sh
python3 scripts/demo/world.py prepare
```

The last command prints a new directory such as `/private/tmp/amxd-abcdefgh`.
Use that exact path as `WORLD` below. Each run has its own home, configuration,
daemon, tmux server, three synthetic Git checkouts, and harness executables.
Preparation changes no app preferences and starts no processes.

```sh
python3 scripts/demo/world.py start WORLD
python3 scripts/demo/world.py status WORLD
```

`start` waits for the actual daemon to report a completed Claude session,
a working Codex session, and a Copilot session waiting for approval. The
stand-ins emit real managed hooks and receive terminal input through tmux.
Their terminal output explicitly identifies itself as scripted demo data.

## Use a separate Mac over SSH

Choose the recording Mac with the operator. A spare Mac keeps UI automation
from interrupting their desktop. The existing remote runner verifies that the
destination has a different hardware identity and refuses to fall back locally.
After building, set the destination and copy the checkout and build products:

```sh
export AMX_MAC_TEST_HOST=user@test-mac.local
export AMX_UI_TEST_REMOTE_ONLY=1
sh macos/scripts/remote-ui.sh sync
sh macos/scripts/remote-ui.sh put amx amx
sh macos/scripts/remote-ui.sh put 'macos/build/Agent Mux.app' macos/build/
sh macos/scripts/remote-ui.sh put 'macos/build/Agent Mux Demo Recorder.app' macos/build/
sh macos/scripts/remote-ui.sh sh 'python3 scripts/demo/world.py prepare'
```

Run the remaining commands through `remote-ui.sh sh 'COMMAND'`, using the
world path printed **by the remote Mac**. Keep that Mac unlocked and logged
in. Its privacy grants are separate from those on the machine driving SSH.
The recorder runs on the remote Mac; no viewer window opens locally.

For an additional persistent block in the driving checkout, run
`touch macos/build/.demo-remote-only`. Both demo UI entry points refuse to run
beside this marker. It is ignored by Git and excluded from the remote sync.
Headless preparation, inspection, shutdown, and compilation still work.

## Record on the chosen Mac

The following commands open and activate the demo app, resize its workspace,
select accessible menu items and buttons, and type the scripted approval.
They must not run while the operator needs to use that desktop.

```sh
python3 scripts/demo/world.py launch-app WORLD
```

The app is copied into the world with a unique bundle identifier. Its binary
override and preferences belong to that copy. Global shortcuts and native
notifications are disabled for the copy. The installed app is left alone.

Before recording for the first time, request the two macOS privacy permissions:

```sh
open -n -W 'macos/build/Agent Mux Demo Recorder.app' --args \
  --request-permissions /tmp/agentmux-demo-permissions.json
```

Enable **Agent Mux Demo Recorder** in System Settings → Privacy & Security →
Accessibility and Screen & System Audio Recording. Recheck after granting them:

```sh
open -n -W 'macos/build/Agent Mux Demo Recorder.app' --args \
  --permissions /tmp/agentmux-demo-permissions.json
cat /tmp/agentmux-demo-permissions.json
```

Both values must be `true`. Launching the signed recorder through `open` keeps
these grants associated with its app identity rather than the terminal.

Read `bundle_id` from `WORLD/demo.json`, then run:

```sh
open -n -W 'macos/build/Agent Mux Demo Recorder.app' --args \
  DEMO_BUNDLE_ID /tmp/agentmux-demo-frames
```

Choose a fresh, absolute output path: apps launched with `open` do not inherit
the shell's working directory. The recording command checks both permissions
before activating the app and fails without silently requesting them. If the recorder
fails, it writes an adjacent `FRAME_DIRECTORY.error.txt`; `open` alone does not
reliably communicate the app's exit status.

The recorder captures only the demo workspace window, at five frames per
second for a 46-second walkthrough. It switches sessions, approves the
scripted documentation check, opens the dashboard and overview, and shows the
inspector, then opens Checkout Changes beside the agent. Input targets the
demo process. The recorder raises its window at scene changes, so reserve the chosen desktop for the duration of the run.
Each scene checks the foreground app and expected window title; a mismatch
aborts the capture instead of reporting a successful walkthrough.
`recording.json` lists the actions and elapsed capture time. A partial run
does not produce that completion record.

For SSH runs, expand the absolute output path in the remote shell, then retrieve
the result without opening local UI:

```sh
sh macos/scripts/remote-ui.sh sh 'open -n -W "macos/build/Agent Mux Demo Recorder.app" --args DEMO_BUNDLE_ID "$PWD/macos/build/demo-frames"'
sh macos/scripts/remote-ui.sh get macos/build/demo-frames /tmp/agentmux-capture
```

After capture, verify that the documentation session reached `icon_state:
stop` using `world.py status`. Review frames at each scene transition for
correct focus, readable text, and unexpected personal information. A completed
capture alone does not prove that every UI action succeeded.

## Encode and review

After reviewing the captured frames, encode locally:

```sh
ffmpeg -framerate 5 -i /tmp/agentmux-demo-frames/%04d.png \
  -filter_complex 'fps=2,scale=1120:-2:flags=lanczos,split[s0][s1];[s0]palettegen[p];[s1][p]paletteuse=dither=none:diff_mode=rectangle' \
  -loop 0 /tmp/agentmux-desktop-demo.gif
ffmpeg -framerate 5 -i /tmp/agentmux-demo-frames/%04d.png \
  -c:v libx264 -crf 18 -pix_fmt yuv420p -movflags +faststart \
  /tmp/agentmux-desktop-demo.mp4
```

Inspect the resulting animation before copying selected media to `docs/media/`
and embedding it in the README. Label the agent output as scripted demo data.
Do not commit raw frame directories, the temporary world, or capture logs.

## Stop

```sh
python3 scripts/demo/world.py stop WORLD
```

This stops the demo app (after verifying its PID still names that exact
executable), ends its sessions, stops its daemon, waits for the daemon to exit,
and removes the demo copy's preferences. It preserves the temporary world and
logs for inspection. You may remove that world directory afterward.
