# Session 15 acceptance driver

The scripts that produced `../interaction-acceptance.log` and the pictures
beside it. They run on one Mac (the Studio) and act only on another (the
MacBook Pro): no window is opened, no input is synthesized, and no clipboard,
focus, or input source is touched on the machine the driver runs on.

## Two machines

| | Studio (driver) | MacBook Pro (everything seen and driven) |
| --- | --- | --- |
| The app under test | | a copy of this checkout's Debug build, bundle id `io.github.sglavoie.agentmux.acceptance15`, built there |
| tmux | | a relocated copy of tmux 3.7c inside the world; nothing installed |
| The world's `amx` daemon, registry, config | | `/tmp/amxs15` (own `HOME`, `TMPDIR`) |
| Keys, pointer, pictures | sent by `vnc.py`, a VNC client with no viewer | received by the MacBook's screen sharing, as real input to its console |
| State read for the checks | asked over SSH | `tmux`, the registry, the daemon, the app's unified log, the link target's log |
| The three real harnesses | the **processes** run here, headless, signed in as usual | each one's **terminal** is a tmux pane there |

The harnesses are signed in on the Studio only, and no credential is copied.
`bridge.py` joins the two halves: in each pane on the MacBook, `agent.sh` runs
`bridge.py pane`, which relays that pane's terminal over SSH to
`bridge.py serve` on the Studio, where the real harness runs in a
pseudo-terminal of the pane's size and with the pane's terminal identity
(`TERM`, `TMUX`, and the rest: a harness decides from them whether to ask for
extended keys). So a session has two processes whose identity matters, and
the checks name both: the pane process on the MacBook, which is what AgentMux
registered and what survives viewers, and the harness process on the Studio.

## Files

- `env.sh` (Studio) and `remote-env.sh` (MacBook) define the two sides.
  `rr` runs a script on the MacBook inside the world.
- `setup.sh REPO` builds `amx` on the Studio, has the MacBook build the Debug
  app in the isolated copy that `macos/scripts/remote-ui.sh` keeps there, and
  installs the world under `/tmp/amxs15` on the MacBook.
- `world.sh up|down` starts or stops the world: daemon, link target, and four
  sessions on the MacBook; the three harnesses on the Studio.
- `agent.sh` is the world's `claude`, `codex`, and `copilot` on the MacBook:
  one managed `SessionStart` hook, so the session is listed, then the pane end
  of the bridge. In the `history` project it is a plain line printer that
  stays on the main screen (3000 lines and a link).
- `amx-shim.sh` is the `amx` the app copy runs: it puts the world's `HOME`
  back (libghostty starts commands through `login(1)`) and hosts utility
  views in an observer tmux (`obs.sh`) so their screens can be read.
- `remote-lib.sh` (MacBook) and `lib.sh` (Studio) hold the helpers.
- `vnc.py` is the input and picture tool. `drive.sh OUTDIR CASE...` runs the
  cases: `fresh claude codex copilot history link ime priority reattach`.
- `inputsource.swift` selects a keyboard input source on the MacBook, and
  `hid.swift` posts real wheel and key events there (see below).

## What the MacBook had to allow, and what was changed there

- **Screen sharing joins the console only by preference.** A viewer with the
  shared VNC password is otherwise given a login-window session of its own
  (`screensharingd`: `userLoggedInFlag 1 ... createLoginWindow 1`), which
  cannot join a desktop that is in use. For the run,
  `/Library/Preferences/com.apple.RemoteManagement VNCAlwaysStartOnConsole`
  was set to true with the operator's approval; it was absent before and is
  removed again afterwards (`../session-15.json` records the restoration).
- **Real HID events need an Accessibility grant.** Three things do not
  survive the trip through screen sharing: the wheel (dropped from this
  viewer), keys meant for an input method (the server types the characters
  itself, past any input method), and a global shortcut. For those,
  `hid.swift` posts real events on the MacBook over SSH, which macOS allows
  only once `/usr/libexec/sshd-keygen-wrapper` is in System Settings >
  Privacy & Security > Accessibility. It was added there for the run, with the
  operator's approval, through the viewer, and removed again afterwards
  (`../session-15.json` records both). Without it, System Events refuses
  keystrokes from SSH and `CGEvent` posting is not permitted. Screen capture
  is never permitted from SSH: every picture is the viewer's.
- **An input source can only be enabled and selected from inside the GUI
  session.** `gui` in `lib.sh` runs `inputsource` there as a child of System
  Events, which SSH may ask things of.
- The MacBook's clipboard is overwritten by the copy and paste checks.
- The link check hands a link to the MacBook's default browser. If the
  browser was not running it is started, may stop at its own profile chooser,
  and is closed again by the driver; one that was running is left alone.
- The Japanese input method is enabled for the input-method case and
  disabled again, with the list of enabled sources compared before and after.
- macOS may ask on the MacBook whether "Python" may find devices on local
  networks (the link target is Python's `http.server`). The run answered
  Don't Allow.

## Things that mislead

- A press with Shift, or on a surface that already holds a selection,
  extends that selection: one drag per freshly attached view, started in the
  left part of the first cell.
- Under a mouse-tracking program, which tmux always is, a link opens only
  with Command+Shift+click and only for modifiers pressed as keys. `vnc.py`
  presses them as keys.
- A check that reads a log or a port can be blind: the link target fetches a
  marker path from itself and finds it in its log before any click counts.
- Codex quits on Control+C in an empty composer; under tmux its new-line key
  is Control+J, and Shift+Return submits.
- The sidebar rows are clicked at fixed points, so a session that ends
  mid-run moves every row below it.
