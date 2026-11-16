# Desktop demo and terminal animation

The README embeds `docs/media/desktop-demo.gif` and links to the full-resolution
`docs/media/desktop-demo.mp4`. Both show the real macOS workspace with scripted
agent data. Follow [Desktop recording](#desktop-recording)
to reproduce them.

## Desktop recording

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

## Use the Parallels macOS guest

For session 9, the operator selected the running `macOS` Parallels guest
(UUID `854c91e2-d015-4367-9693-dea2bde7c75d`), user `sglavoie`. All tests,
media checks and UI actions must run there. Keep the host's
`macos/build/.demo-remote-only` marker in place.

`macos/scripts/parallels.py` uses guest tools and the shared Home folder; SSH
is unnecessary. It never falls back to executing a command on the host.
Provision Xcode, Go, just, uv, golangci-lint and ffmpeg/ffprobe inside the guest.
Pinned GhosttyKit/tmux products can be transferred with the initial sync:

```sh
python3 macos/scripts/parallels.py sync
python3 macos/scripts/parallels.py run just build
python3 macos/scripts/parallels.py run just mac-app
python3 macos/scripts/parallels.py run sh scripts/demo/build-recorder.sh
python3 macos/scripts/parallels.py run python3 scripts/demo/world.py prepare
```

Every `run` refreshes source files, including Git metadata, before executing
as the guest user in its private checkout under
`~/Library/Caches/agent-mux-parallels/`. It preserves guest build/cache and
Vendor directories. Archives avoid Parallels shared-folder symlink traversal;
frameworks and SDKs should likewise be archived before copying into the guest.
The wrapper selects the guest Xcode and provisioned tools under
`/usr/local/agentmux/` and `/opt/homebrew/`, with ad-hoc signing. `--vm` and
`--user` override the defaults for another explicitly selected guest.

Prefix **every** remaining command in this guide with
`python3 macos/scripts/parallels.py run`, including encoding and validation.
Use the world path returned by the guest. To launch UI in its logged-in GUI
session, pass `python3 macos/scripts/run-in-gui.py` before `open` or the
`world.py launch-app` command. Privacy permissions must be granted normally
inside the guest; host grants do not apply. For example:

```sh
python3 macos/scripts/parallels.py run python3 macos/scripts/run-in-gui.py \
  /usr/bin/open -n -W 'macos/build/Agent Mux Demo Recorder.app' --args \
  --request-permissions /tmp/agentmux-demo-permissions.json
python3 macos/scripts/parallels.py run just check
python3 macos/scripts/parallels.py run just mac-test
python3 macos/scripts/parallels.py run just readme-media-check
```

Retrieve new artifacts with `python3 macos/scripts/parallels.py get
RELATIVE_GUEST_PATH LOCAL_DIRECTORY`. Retrieve generated media before the next
`run`, which refreshes tracked files from the host. Stop the isolated guest
world with `run python3 scripts/demo/world.py stop WORLD` when finished.

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
scheduled over 32 seconds; UI reads and validation can extend it: sidebar story
lines and the working tool/timer, a move into Needs attention, two answers using
parsed option buttons through the native queue, collapsed Briefing summaries,
then checkout review beside the agent. The
changed-file list is hidden for readability. The stand-ins accept single-key
answers like real terminal agents; the api-service stand-in’s `r` key triggers
its scripted permission request.
Input targets the demo process; reserve the chosen desktop for the duration.
Each scene checks the foreground app and expected view, and queue stages require
an enabled **Option 1: Yes** button scoped to the answer panel for each session,
then a closed panel after the second answer. **More keys** stays collapsed. Scene screenshots and accessibility trees are saved for review. A failed
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

Before replacing either artifact, save its SHA-256 for the review record:

```sh
shasum -a 256 docs/media/desktop-demo.gif docs/media/desktop-demo.mp4
```

After reviewing the captured frames, encode on the selected recording guest or Mac:

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
before considering a frame-rate change; retain all seven review scenes below.
The MP4 must be 30 fps. The stream records real motion during transitions;
constant-rate encoding repeats stationary frames when the display does not change.

Review both final encoded files themselves with playback and decoded frames at
each scene boundary. Source screenshots and accessibility checks alone do not
establish that the encoded crop, timing, and text are legible. Confirm the story
lines, Needs attention, real option buttons for **api-service** and
**documentation**, advancement after the first answer, closure and **All caught
up** after the second, Briefing's collapsed session summaries with no expanded
event rows, and checkout review. Keep **More keys** collapsed. Re-encode and
repeat the review if any scene is hidden or unreadable.

Copy accepted files to `docs/media/` and commit `desktop-demo-review.json` there.
The schema is version 1 with these required fields:

- `schema_version`: `1`; `reviewer`: the person or executing agent that inspected
  both encoded files; `reviewed_at`: ISO 8601 timestamp including timezone;
  `app_source_revision`: the full 40-character Git revision used to build the app.
- `artifacts`: exactly two objects, with `path` equal to
  `docs/media/desktop-demo.gif` or `docs/media/desktop-demo.mp4`, `old_sha256`
  saved before replacement, `new_sha256` of the reviewed encoding, integer
  `byte_size`, and `frame_rate` as a rational string such as `10/1` or `30/1`.
- Each artifact's `scenes` contains these IDs in order: `story-lines`,
  `needs-attention`, `first-option`, `second-option`, `all-caught-up`,
  `briefing-collapsed`, `checkout-review`. Each has `timestamp_seconds`, a
  zero-based decoded `frame_index`, `result: "pass"`, and nonempty `observations`
  describing what was actually seen. Identify the agents and their option
  labels, queue advancement/closure, and the visible collapsed Briefing summaries.
  Timestamps and frame indices must increase and identify the same encoded frame.

Use ffprobe to locate decoded frame timestamps, then extract that exact frame:

```sh
ffprobe -v error -select_streams v:0 -show_frames \
  -show_entries frame=best_effort_timestamp_time -of csv=p=0 docs/media/desktop-demo.gif
# Replace FRAME_INDEX with the zero-based frame number selected above.
ffmpeg -i docs/media/desktop-demo.gif -vf 'select=eq(n,FRAME_INDEX)' \
  -frames:v 1 /tmp/desktop-review-frame.png
python3 scripts/demo/check-desktop-review.py docs/media/desktop-demo-review.json
python3 -m unittest discover -s scripts/demo/tests -v
```

Repeat frame inspection for the MP4; its indices differ from the GIF's. The
validator uses Python's standard library and ffprobe to check schema, changed
hashes, current sizes/rates, scene ordering and bounds. It checks freshness and
completeness, not visual truth. Re-encoding invalidates the review until visual
inspection and the evidence hashes are refreshed. Link the evidence and summarize
the measured recording facts in `docs/media/README.md`. Keep agent output labeled
as scripted demo data; never fabricate a pass when media cannot be inspected.
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
