# README demo media

`demo.svg` is the README's 21.2-second dashboard tour: eight real TUI frames
with synthetic Claude, Codex, and Copilot sessions. CSS switches between the
frames, preserving sharp text at any scale. It uses no JavaScript, external
fonts, or embedded screenshots. Reduced-motion viewers see the opening frame.

The offline `TestReadmeDemo` fixture drives the dashboard with keyboard input
and checks each frame for layout issues and expected content. The renderer
adapts the animated SVG approach from Terminal Tasks (`tt`). Regenerate with
`just readme-media`; verify with `just readme-media-check`. Both require `uv`.
See [the animation instructions](../../scripts/demo/README.md#readme-animation).

## Desktop recording

Recorded on 2026-10-21 using the real macOS app on a MacBook Pro, driven over
SSH. The application build is based on `3bb5637a`, with the MIT license added
to its resources. Agent responses and project changes are synthetic and
clearly labeled; the UI, daemon, tmux sessions, hook events, and diff are real.

- `desktop-demo.gif`: previous 46-second looping preview, 1120 × 700, 2 fps.
- `desktop-demo.mp4`: the full 1280 × 800 window capture at 5 fps, without audio.
- `desktop-demo.png`: the split checkout diff at 42 seconds, at capture resolution.

The recorder checked the foreground app and expected view at nine scene
transitions. Verification also checked the approval state transition, the
managed hook configuration, and shutdown. Scene frames were visually reviewed
and all 230 source frames were screened with OCR before encoding. Raw frames,
temporary demo worlds, and diagnostic reports are not included.

See [the reproduction instructions](../../scripts/demo/README.md). Recording
runs only on the chosen physical Mac, never in GitHub Actions.
