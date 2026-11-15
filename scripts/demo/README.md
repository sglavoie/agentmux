# Desktop demo and terminal animation

The README embeds `docs/media/desktop-demo.gif` and links to the full-resolution
`docs/media/desktop-demo.mp4`. Both show the real macOS workspace with scripted
agent data. Follow [Desktop recording on a physical Mac](#desktop-recording-on-a-physical-mac)
to reproduce them.

## Desktop recording on a physical Mac

This tooling drives and captures the real macOS workspace using deterministic
agent stand-ins. It makes no AI requests and does not load agent credentials.
It is local tooling; never run it in GitHub Actions.

The README's desktop animation and video were recorded on a separate MacBook
Pro over SSH. They exercise the actual desktop app; terminal responses come
from the clearly labeled stand-ins, not live AI services.

## Prepare without opening windows

Requires macOS 14.4+, Xcode, Python 3, the repository's Go/just toolchain, and
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

`start` waits for the actual daemon to report a working Claude session,
a completed Codex session, and a Copilot session waiting for approval. The
stand-ins emit real managed hooks and receive terminal input through tmux.
Their terminal output and saved transcripts explicitly identify themselves as
scripted demo data. Recap generation is disabled in the demo configuration;
Briefing reads these synthetic transcripts and real local activity records.

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

The recorder captures only the demo app and its floating panels in a fixed
1280 × 800 region. A continuous
ScreenCaptureKit stream captures display updates at up to 30 fps. The tour is
scheduled over 32 seconds; UI reads and validation can extend it: the working sidebar tool/timer, a move into Needs attention, two answers
through the native queue, Briefing, then checkout review beside the agent. The
changed-file list is hidden for readability. The stand-ins accept single-key
answers like real terminal agents; the api-service stand-in’s `r` key triggers
its scripted permission request.
Input targets the demo process; reserve the chosen desktop for the duration.
Each scene checks the foreground app and expected view, and queue stages require
an enabled answer panel for the next session or a closed panel after the second
answer. Scene screenshots and accessibility trees are saved for review. A failed
control action or scene check aborts the capture.
`capture.mov` contains the source video; `recording.json` lists the actions,
captured update count, and elapsed time. `scene-N.png` and `scene-N.json` retain
visual and accessibility evidence at each transition. A partial run
does not produce that completion record.

For SSH runs, expand the absolute output path in the remote shell, then retrieve
the result without opening local UI:

```sh
sh macos/scripts/remote-ui.sh sh 'open -n -W "macos/build/Agent Mux Demo Recorder.app" --args DEMO_BUNDLE_ID "$PWD/macos/build/demo-frames"'
sh macos/scripts/remote-ui.sh get macos/build/demo-frames /tmp/agentmux-capture
```

After capture, verify that both api-service and documentation reached
`icon_state: stop` using `world.py status`. Review frames at each scene transition for
correct focus, readable text, and unexpected personal information. A completed
capture alone does not prove that every UI action succeeded.

## Encode and review

After reviewing the captured frames, encode locally:

```sh
# Preserve the whole capture and hold checkout review for four more seconds.
duration=$(python3 -c 'import json, math; print(math.ceil(json.load(open("/tmp/agentmux-demo-frames/recording.json"))["elapsed_seconds"]) + 4)')
ffmpeg -i /tmp/agentmux-demo-frames/capture.mov -t "$duration" \
  -filter_complex "tpad=stop_mode=clone:stop_duration=${duration},fps=10,scale=1120:-2:flags=lanczos,split[s0][s1];[s0]palettegen[p];[s1][p]paletteuse=dither=none:diff_mode=rectangle" \
  -loop 0 /tmp/agentmux-desktop-demo.gif
ffmpeg -i /tmp/agentmux-demo-frames/capture.mov -t "$duration" -vf "tpad=stop_mode=clone:stop_duration=${duration},fps=30" \
  -c:v libx264 -crf 18 -pix_fmt yuv420p -movflags +faststart \
  /tmp/agentmux-desktop-demo.mp4
ffprobe -v error -select_streams v:0 \
  -show_entries stream=width,height,avg_frame_rate:format=duration,size \
  -of json /tmp/agentmux-desktop-demo.gif
ffprobe -v error -select_streams v:0 \
  -show_entries stream=width,height,avg_frame_rate:format=duration,size \
  -of json /tmp/agentmux-desktop-demo.mp4
```

Keep the GIF below 8 MB and at least 10 fps. Trim duration or reduce dimensions
before considering a frame-rate change; retain all five required tour stages.
The MP4 must be 30 fps. The stream records real motion during transitions;
constant-rate encoding repeats stationary frames when the display does not change.

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

## Terminal dashboard animation

The former README animation, `docs/media/demo.svg`, remains available as a
terminal dashboard demo, generated using synthetic session data and the
offline TUI driver. The 21.2-second tour inspects Claude, Codex, and Copilot
output, jumps to a session needing permission, filters by harness, opens the
project switcher, and searches the command palette. No app windows, tmux
sessions, daemon, or live AI requests are needed.

Requires the repository's Go toolchain and `uv` (which installs the pinned
Rich renderer dependency):

```sh
just readme-media        # regenerate docs/media/demo.svg (terminal demo only)
just readme-media-check  # fail if the committed SVG is stale
```

Edit the fixture and keyboard tour in `tui/readme_demo_test.go`; adjust SVG
styling in `scripts/demo/render_svg.py`. Every captured frame is checked for
layout issues and expected content before export. The SVG is self-contained,
uses CSS animation, and holds its opening frame for reduced-motion viewers.
Review it in a browser after changing the tour, including at README width.
