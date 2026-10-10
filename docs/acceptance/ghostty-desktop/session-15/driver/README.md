# Session 15 acceptance tools

**There is no recorded acceptance run for session 15.** No `drive.sh`, no
`session-15.json`, and no log or screenshot was published. What is here is
the isolated world and the input tools, built and used interactively while
exploring the live app, and left for whoever performs the recorded run. The
session stopped, with its status pending, on an unanswered question about
continuing to drive the operator's desktop.

The world is rooted at `/tmp/amxs15`: its own `HOME`, `TMPDIR`, config, daemon,
runtime-session registry, private tmux server, and app preferences domain
(`io.github.sglavoie.agentmux.acceptance15`). It has no Kitty.

- `env.sh` is sourced by everything. It points the environment at the world,
  unsets every Kitty, tmux, ASL, harness, and AgentMux variable, and keeps the
  operator's own home as `REAL_HOME`.
- `setup.sh REPO` (after `just mac-app`) builds this checkout's `amx`, installs
  `agent.sh` as the world's `claude`, `codex`, and `copilot`, records where the
  real harnesses are and the environment they need, compiles the Swift tools,
  and copies the Debug app under the acceptance bundle identifier.
- `agent.sh` is what AgentMux launches in a pane. It sends one managed
  `SessionStart` hook to the world's daemon, so the session is listed in the
  sidebar, then becomes the operator's **real** harness with the operator's
  home and credentials and with the AgentMux session identity removed. In the
  `history` project it is instead a plain line printer on the main screen
  (3000 numbered lines and a link), whose output scrolls into tmux's history.
- `world.sh up` starts the world's daemon, the link target, and the four
  sessions. `world.sh down` stops all of it. Verify afterwards that nothing is
  left: an earlier version recorded the wrong process for the link target and
  left it running. The preferences cleanup in `down` was rewritten after its
  last use and has **not been run**.
- `amx-shim.sh` is the `amx` the app copy runs: it restores the world's `HOME`
  (libghostty starts commands through `login(1)`) and hosts utility views in
  the observer tmux (`obs.sh`) so their screens can be read.
- `lib.sh` holds the helpers: the private server's panes and screens, the
  registry, the daemon snapshot, the app's unified log, the window and menus
  through System Events accessibility, cell-to-screen-point geometry, the
  clipboard, the link target, and a borrowed input method.

## The input tools, and what they can do to the machine

These post real input to the operator's desktop. Each hazard below was found
by causing it.

- `mouse.swift` posts pointer events. Events are created from a **private**
  event source. From the HID-system source, one Shift-flagged mouse event made
  the whole machine report Shift held, at both the HID and the session level,
  until the tool's next unflagged event: for minutes when such an event ended
  a command. Every action now ends by releasing what it pressed and posting an
  unflagged move, exits 9 if the system still reports a modifier held, and
  does the same when interrupted. `mouse state` prints what the system
  believes is held; read it before claiming the keyboard is clean.
- A click that must act on a link needs more than modifier flags on the mouse
  events. Under a mouse-tracking program, which tmux always is, Ghostty takes
  a click back from the program only for modifiers it saw pressed as **keys**.
  Flags on mouse events work only on a surface the pointer has never crossed
  without modifiers (`GhosttyLinkTests` pins both halves). The tool has no
  verb that presses Command and Shift as keys around a click. One has to be
  written, and measured the way the mouse flags were, before a live link
  check means anything.
- A press with Shift, or on a surface that already holds a selection,
  **extends** that selection. Trials that reuse a surface are not
  independent: use a freshly attached view for each, and start a drag in the
  left quarter of the first cell (Ghostty starts at the next cell for a press
  well past the middle).
- `inputsource.swift` selects a keyboard input source, enabling it first.
  Selecting the Japanese input method also enables its kana palette. Use it
  only through `ime_begin` / `ime_end` in `lib.sh`, which restore the selected
  source and disable everything that was not enabled before, on any exit.
- System Events key presses with modifiers (`key CODE "shift down"`) were
  measured to leave nothing held. `key down` / `key up` were not, and must not
  be used: a step that dies between them leaves the modifiers down.
- `winid.swift` gives the app's window number for `screencapture -l`, which
  captures that window alone. A capture of a screen region includes whatever
  the operator has in front of it.
- A check that reads a log, a port, or a picture can be blind. Prove the
  instrument first: the link target fetches a marker path from itself and
  finds it in the log before any click is counted, and a crop must contain a
  known landmark.

## What was observed with these tools

Live, in the real app copy, with real key events and the three real harnesses
running in the private tmux server (Claude Code 2.1.296, Codex 0.162.0,
Copilot 1.0.95):

- Claude: typing, Option+Left, Control+A, Shift+Return (a new line, not a
  submit), Control+U, a two-line paste with Command+V (not submitted), a
  150-line answer, the wheel and Page Up through its own transcript, and
  Shift+Page Up / Shift+Page Down through the menu path once that was fixed.
- The main-screen session: Shift+Page Up/Down, Command+Home/End, Command+F
  with a typed needle landing on its line out of 2964, Command+G and
  Shift+Command+G, Escape; a plain drag copied through tmux to the clipboard
  with no prompt; a right click did nothing.
- A real Japanese input method composed in the terminal with the candidate
  window at the cursor, and committed text reached Claude.
- The app copy was replaced three times while the same four agent processes
  kept running.
- One link opened with Command+Shift+click, logged by the app and fetched from
  the link target in the same second.

Codex and Copilot were started and seen idle, and nothing was exercised on
them. Focus-dependent shortcut priority, detach and reattach, and reopening
the window were not exercised at all. None of the above is a recorded,
repeatable run. Two kinds of observation are not listed because the pointer
tool was still leaving modifiers held when they were made: copying a
Shift+drag selection with Command+C (the clipboard text was exact, but the
chord the system saw is uncertain), and every link click after the first.
