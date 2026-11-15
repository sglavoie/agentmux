# README demo media

The README embeds the desktop animation and links to the full-resolution video.

## Desktop recording

Recorded on 2026-11-15 using the real macOS app on the separate MacBook Pro,
driven over SSH. The app and CLI were built from `aec5857b` (sessions 1–12);
the recording tools were updated in session 13. Agent responses, saved transcripts,
and project changes are synthetic and clearly labeled. The UI, daemon, private
tmux sessions, hook events, activity journal, and checkout diff are real.

- `desktop-demo.gif`: 42-second looping preview, 1120 × 700, **10 fps**,
  420 frames, 2,191,378 bytes (2.19 MB; below the 8 MB limit).
- `desktop-demo.mp4`: 42 seconds, 1280 × 800, **30 fps**, 1,260 frames,
  647,030 bytes, H.264 without audio.
- `desktop-demo.png`: the final split checkout review, 1280 × 800.

The tour shows, in order:

1. Claude’s working sidebar row with `Bash`, a live elapsed timer, and its branch.
2. The api-service session moving into **Needs attention**.
3. Its native answer panel advancing automatically to Copilot’s documentation
   prompt, then closing after the second answer with **All caught up**.
4. **Briefing**, scoped to All projects, with retained activity and labeled
   synthetic supporting output.
5. Checkout review beside the agent, with the changed-file list collapsed.

ScreenCaptureKit captured 609 actual display updates over 37.1 seconds at an
up-to-30-fps capture rate. Encoding uses their original timing, repeats stationary
frames at the output rate, and holds the final review to reach 42 seconds.
This replaces the previous 5-fps source recording and 2-fps README GIF.

All seven scene screenshots were visually reviewed, as was a contact sheet from
the encoded GIF. The recorder checked foreground ownership, expected workspace
and Briefing titles, enabled prompt controls for each queued session, and panel
closure after the second answer. The daemon subsequently reported all three
sessions at `stop`, with no permission or question remaining. The three synthetic
transcripts were indexed; managed hook files reported `in_sync`. The stand-ins
emit hooks directly, so this is not evidence of real CLI authentication or hook
activation/trust. No real agent credentials or model requests were used.

`ffprobe` verified dimensions, duration, frame counts, frame rates, and sizes;
the MP4 also passed a full decode. The demo app, sessions, and daemon were stopped
after review. Raw capture, scene trees, temporary worlds, and logs are not committed.

See [the reproduction instructions](../../scripts/demo/README.md). Recording
runs only on the chosen physical Mac, never in GitHub Actions.

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
