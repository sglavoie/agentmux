# Adding a harness

The checklist for adding a harness to Agent Mux. `CLAUDE.md` points here.

1. Implement `harness.Impl` and invocation capabilities in `internal/harness`.
2. Register the adapter and hook contract.
3. Add transcript reader fixtures and config coverage.
4. Extend fresh/resume/fork cross-harness tests, explicitly representing
   unsupported native fork capability.
5. Add the screen adapter in `internal/screenstate`, capture-first. This step is
   not optional for a harness whose transcript cannot report that a pane is
   waiting on a human — a permission prompt is not a transcript event, so on
   Copilot the screen is the only evidence such a pane exists at all.

   Take the captures *before* writing a single pattern, with
   `kitten @ get-text --extent=screen` against a live pane of the harness, and
   commit them byte-for-byte under `internal/screenstate/testdata/`. They are
   never hand-edited and never adapted from another harness's file. Cover at
   minimum a working screen, a finished turn, a bare composer, a blocking
   prompt, an interrupted turn, and a composer holding unsent input, plus a
   `_t0`/`_t1` pair each for working and idle so the Layer B fingerprint claim
   is tested rather than assumed. Record the harness build in the test file's
   header comment: the version identifies which capture must be re-taken when a
   release moves the copy.

   Take the same screens on tmux as well, with
   `internal/screenstate/testdata/tmux/capture.sh` against a live pane on a
   private server running the production configuration. It writes the plain
   capture the tmux backend returns, the styled (`-e`) capture, and the pane
   geometry and screen mode tmux reported, under
   `internal/screenstate/testdata/tmux/`. Include one capture after a live
   resize to a narrow pane. A probe that reads more than the screen needs that
   captured too: `capture.sh title` writes the pane title the harness set, and
   `capture.sh all` the screen with its history, each beside the capture it
   belongs to. Register each in `tmux_fixture_test.go` and add a
   row per behaviour to its Kitty/tmux parity table: the two sets must read
   alike, and a difference is a finding about the adapter, not about tmux.

   Do not borrow patterns from an existing set. All three provisional Copilot
   clauses, written by analogy to Claude and Codex, were wrong in kind rather
   than mis-tuned — the composer had no prompt indicator at all, the status line
   had neither the assumed ellipsis nor the assumed timer, and the interrupt
   hint was spelled without its preposition. Each would have failed silently.
   Expect the discriminator to be a *shape* rather than a glyph or a word:
   Copilot's `●` is simultaneously a spinner frame and the bullet it prefixes
   ordinary output with, so only the footer's column geometry separates a live
   turn from finished prose. Write down the accepted cost of every such choice
   in the TOML, and check that it falls on the fail-closed side — a withheld
   reading, never a granted one.

   Then assert every committed capture. Nothing in this package discovers
   fixtures: a file dropped into `testdata/` that no test names is read by zero
   tests and passes the suite silently, which is the one failure mode the
   capture rule exists to prevent. Give each capture a row stating its state,
   tier, and veto set, and add a named test for each trap a capture was taken to
   pose — including a guard that the capture still *contains* that trap, so a
   later re-capture cannot quietly turn the assertion vacuous.

   Finally, list every clause that is a sole veto source in the adapter's
   `Requirements` so an override that drops it is refused at install time rather
   than degrading to "no match" on a live pane.
6. Run `just check`.

