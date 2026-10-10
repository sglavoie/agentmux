# Ghostty + tmux feasibility harness

Session 2 of the desktop migration: one embedded Ghostty surface attached,
through `tmux attach`, to a session on a private tmux server. It exists to prove
the terminal stack before production code depends on it, and is not part of
Agent Mux.app, `just mac-test`, or any gate.

```bash
macos/GhosttyHarness/scripts/build.sh            # vendor the pinned GhosttyKit (macos/Vendor), build the app
python3 -I macos/GhosttyHarness/scripts/run_matrix.py --tmux "$(command -v tmux)"
python3 -I macos/GhosttyHarness/scripts/run_matrix.py --skip-probe --agents claude
```

- GhosttyKit is the one Agent Mux.app links: `macos/scripts/build-ghosttykit.sh`
  builds it from the pins in `macos/GHOSTTY_VERSION`. The Session 2 evidence
  was recorded against a copy of sglavoie/tt's build of the same tag, Zig, and
  patches; no checkout outside this repository is read any more.
- `Sources/Ghostty/` is the wrapper as first ported. The production copy,
  generalized for several surfaces, is `macos/AgentMux/Ghostty/`; the license
  notice for both is `macos/NOTICE-ghostty`.
- `scripts/run_matrix.py` runs the capture, resize, modified-keys, paste, and
  scrolling cases against `scripts/probe.py`, then asks Claude, Codex, and
  Copilot a question, SIGKILLs the viewer, reattaches a new one, and checks the
  agent is the same process. Results and captures go to
  `macos/.cache/ghostty-harness/runs/<UTC stamp>/`.
- The tmux server uses `tmux.conf` and a socket under `/tmp`; agents start with
  `KITTY_*`, `CLAUDECODE`/`CLAUDE_CODE_*`, `ASL_*`, and `AMX_*` removed.
- Running the live cases sends two short prompts to each agent account.

Recorded evidence: `docs/acceptance/ghostty-desktop/session-2.json`, with the
copied run artifacts under `docs/acceptance/ghostty-desktop/session-2/`.
