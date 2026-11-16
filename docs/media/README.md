# README demo media

The README embeds the desktop animation and links to the full-resolution video.

## Desktop recording

Recorded on 2026-11-16 inside the Parallels macOS guest using the real app,
built from `d526b878b7b5eb88e4fc939c137b482dd61f377a`. The guest CLI was built
at checkpoint `b91552e45cbf`; production app/CLI code is unchanged from session 8
(`1fa93124`). Agent responses, saved transcripts and project changes are
synthetic and labeled. The UI, daemon, private tmux sessions, hook events,
activity journal and checkout diff are real. No agent credentials or model
requests were used.

- `desktop-demo.gif`: 38.2-second looping preview, 1120 × 700, **10 fps**,
  382 frames, 2,443,639 bytes (2.44 MB; below the 8 MB limit).
- `desktop-demo.mp4`: 38.2 seconds, 1280 × 800, **30 fps**, 1,146 frames,
  723,247 bytes, H.264 without audio.
- `desktop-demo.png`: the encoded MP4's split checkout review at 32 seconds.

The tour shows, in order:

1. Each waiting/completed row's story line: the scripted `make check` command
   for documentation and “Release page ready for review” for release-page,
   beside Claude's working tool and timer.
2. api-service moving into **Needs attention**, with its command story line.
3. Real **option buttons** labeled **1 Yes** and **2 No** for api-service
   (Claude), then automatic advancement to documentation (Copilot), with
   **More keys** collapsed throughout.
4. The answer panel closing after the second answer and **All caught up**.
   This receipt clears the prompt queue; an already completed turn can still
   appear in the separate Waiting inspector.
5. **Briefing**, scoped to All projects: three collapsed session rows, no
   expanded event children. api-service and documentation each summarize one
   permission request; release-page summarizes one finished turn.
6. Checkout review beside api-service, showing the `server.py` health endpoint
   diff with the changed-file list collapsed.

ScreenCaptureKit captured 535 actual display updates over 32.21 seconds at up
to 30 fps. Encoding preserves source timing, repeats stationary frames at the
output rate, adds a two-second receipt hold at source time 19.4 seconds and a
four-second final checkout hold.

Both final encodings were played side by side in guest Safari, through the
MP4's end and the GIF's loop. Seven frames decoded from **each encoded file**
were separately inspected at 2, 6, 10, 14.5, 20.4, 25 and 32 seconds. Story
lines, both agents' Yes/No controls, queue advance/closure, the receipt,
collapsed Briefing summaries and checkout diff were legible in both formats.
The [hash-bound visual review](desktop-demo-review.json) records those exact
frames, observations, measured rates/sizes and old/new SHA-256 digests.
`scripts/demo/check-desktop-review.py` checks evidence freshness and completeness;
it does not substitute for visual inspection.

The recorder checked foreground ownership, enabled panel-scoped option controls,
queue advance and closure. The daemon subsequently reported all three sessions
at `stop`, with no permission or question remaining. The stand-ins emit hooks
directly; this is not evidence of real CLI authentication or hook trust.
The isolated demo app, daemon and tmux sessions were stopped, and their private
preferences removed. The host's remote-only guard remains in place. Raw captures,
scene trees, temporary worlds and diagnostic logs are not committed.

See [the reproduction instructions](../../scripts/demo/README.md). Capture,
encoding and validation run in the selected guest, never in GitHub Actions.

## Terminal dashboard animation

`demo.svg` is the former README's 21.2-second dashboard tour: eight real TUI
frames with synthetic Claude, Codex, and Copilot sessions. CSS switches between
the frames, preserving sharp text at any scale. It uses no JavaScript, external
fonts, or embedded screenshots. Reduced-motion viewers see the opening frame.
Frames share their line-clipping definitions to keep the generated SVG small.

The offline `TestReadmeDemo` fixture drives the dashboard with keyboard input
and checks each frame for layout issues and expected content. The renderer
adapts the animated SVG approach from Terminal Tasks (`tt`). Regenerate with
`just readme-media`; verify with `just readme-media-check`. Both require `uv`.
These commands only update or check the terminal animation, not the desktop
recording. See [the animation instructions](../../scripts/demo/README.md#terminal-dashboard-animation).
