# Menu bar companion

`Agent Mux.app` is an optional native macOS menu bar app for Agent Mux. It
shows the live managed sessions with attention counts in the menu bar, focuses
a session's Kitty tab from a menu row or a global shortcut, shows the daemon's
notifications as native banners, and answers ASL supervisor questions in a
native panel. Kitty stays the terminal for Kitty sessions; sessions launched
on the tmux backend open in the app's own [workspace window](#workspace). The
daemon stays the source of truth: the app subscribes to the daemon's companion
stream and runs the `amx` CLI for actions. Nothing in Agent Mux depends on it,
and quitting it returns every behavior to what it was without it; agents on
tmux keep running and `amx attach` reaches them from any terminal.

This page is the operator's guide. How the app is built, signed, and laid out,
and how each feature works inside, is in [`macos/README.md`](../macos/README.md).

## Install

Requirements: macOS 14 or later, Xcode command line tools, and the
repository's usual toolchain (Go, `just`). The first build also builds the
embedded terminal engine from source, which needs `git`, network access,
several minutes, and about 5 GB under `macos/.cache/`; later builds reuse it.
See [GhosttyKit](../macos/README.md#ghosttykit).

```bash
just install        # amx and the daemon first: the app needs a daemon that streams
just mac-install    # Release build, then install or update /Applications/Agent Mux.app and relaunch it
```

Install `amx` first. The app talks to whichever daemon is running, and a
daemon older than the companion protocol leaves the app in degraded mode (see
[Troubleshooting](#troubleshooting)). After an upgrade of the pair, reinstall
the app too, so its bundled `amx` and the installed one stay close.

The app must live in `/Applications` for the login item to work, which is
where `just mac-install` puts it (`AMX_APP_INSTALL_DIR` overrides it, at the
cost of the login item).

**Nothing else needs installing.** The bundle carries its own `amx` and its
own tmux: a pinned release, built from checksummed sources and linked
statically, so the app runs on a Mac with no Homebrew and no tmux. Before
`amx` runs that tmux it checks every file of it against the checksums sealed
into the bundle and copies it to `~/.local/share/agent-mux/runtime/`. The copy
is what runs, so the tmux a session server was started from is never inside
the app.

### Updating

`just mac-install` again. The installer checks the new bundle (signature,
`amx`, tmux against its checksums), quits the app, stages the new copy beside
the old one, and renames it into place.

**Agents keep running through an update.** They live in the session server,
not in the app, and the installer never stops, restarts, or replaces that
server: it reads the server's identity before and after and fails if it
changed. When the new app opens, the same agents are in its sidebar. If the
update ships a newer tmux, the running server keeps the tmux it was started
with until its last agent has ended; the next server starts with the new one.
`amx backend status` shows which is in use.

### Removing

```bash
just mac-uninstall                                # the app only
just mac-uninstall --end-sessions                 # and end every running agent
just mac-uninstall --end-sessions --remove-app-data --yes
```

Removing the app removes the app. What else happens is yours to choose:

- **Running agents keep running** unless you pass `--end-sessions`. Without
  it the uninstaller says how many it left, and prints the one command that
  ends them later without `amx`.
- **Your data stays.** `config.toml`, conversations and their indexes,
  handoffs, worktrees, and the session registry are never touched.
  `--remove-app-data` removes only what is the app's alone: its preferences
  (the `io.github.sglavoie.agentmux` defaults domain), its saved window
  state, and the tmux runtime it installed. It is refused while agents run on
  that runtime unless you end them too.

Both choices ask before acting; `--yes` answers for a script.

## Permissions

The app needs no Accessibility, Automation, or Screen Recording permission.
Two grants matter:

- **Notifications.** The app asks the first time the daemon sends it a
  notification, not at launch. While it has never been asked, or while it is
  allowed, it claims the daemon's notification backend role; when it is denied
  or turned off in Preferences, the daemon uses Alerter as before. Change it
  later in System Settings › Notifications › Agent Mux. The app rereads the
  permission every 10 seconds and whenever its menu opens.
- **Login item.** Preferences › Launch at Login registers the app with
  `SMAppService`. It shows in System Settings › General › Login Items, and
  removing it there is respected: the app does not add it back.

macOS ties both grants to the app's code signature. A build signed with an
Apple Development or Developer ID certificate keeps them across rebuilds. An
ad-hoc build (no certificate in the keychain, or `AMX_CODESIGN_IDENTITY=-`)
looks like a new app every time, so both grants are asked for again after each
install. See [Signing](../macos/README.md#signing).

## Kitty requirements

Focus, the dashboard shortcut, Launch Project, Recent, and Mute all reach Kitty
through its remote-control socket, so `kitty.conf` needs:

```conf
allow_remote_control yes
listen_on unix:/tmp/kitty
```

Kitty appends its PID, so each instance listens on `unix:/tmp/kitty-<pid>`;
that is how the app tells two Kitty instances apart and brings the right one
forward. When the app cannot find any socket to try, its menu says so in place
of acting: "Kitty has no remote-control socket: add `allow_remote_control yes`
and `listen_on unix:/tmp/kitty` to kitty.conf, then restart Kitty." A changed
`listen_on` needs a Kitty restart, not a reload. If you use another socket
path, set it in Preferences › Kitty socket.

The daemon accepts a `focus_window` only for a socket it learned itself, so a
Kitty instance that has never run a managed session is not focusable until it
has.

## Workspace

**Open Workspace** in the menu shows a window with every live session in a
sidebar, grouped by project and labeled by profile, with the same state glyphs
the tab titles use and a count of waiting sessions beside each project.

- A click on a **tmux** session shows its terminal in the window and puts the
  keyboard in it. The terminal is a view of the agent, not the agent: closing
  the window, quitting the app, or attaching to the session somewhere else
  (`amx attach`, the dashboard) leaves the agent running. A view that lost
  its session to another terminal says **Detached** and offers **Reattach**.
  Each session has one owner at a time: the terminal that attached last has
  the keyboard and sets the size, and the agent keeps that size when nobody
  is attached. Previews and the Overview only read the screen.
- The window comes back where it was. If it was open when the app last ran
  (it quit, crashed, or was upgraded), it reopens on the same session or view.
  The agents were running in tmux all along and are simply shown again. A
  session that another terminal attached to meanwhile is shown **Detached**
  rather than taken back.
- An agent that is no longer running is never started again by the app. The
  pane says what happened instead: **Agent exited** when the agent ended on
  its own (with its exit status), **Agent lost** when the terminal it ran in
  is gone, as after a reboot. Starting or resuming is done from the Dashboard.
- A click on a **Kitty** session brings Kitty forward on its window, as the
  menu does. Its row is marked `Kitty`. Moving through the sidebar with the
  arrow keys only selects; it never switches apps.
- If the daemon stops, the sidebar keeps its sessions, dimmed, and says so at
  the bottom; the terminals keep working. Everything is current again when
  the daemon is back.

While the window is open the app has a Dock icon and a menu bar; closing the
window makes it a menu bar app again.

### Dashboard and utility views

Above the projects the sidebar lists the workspace's own views. They are
terminals of their own beside the agents, and none of them needs Kitty.

- **Dashboard** (⌘D) is the `amx` dashboard, embedded. The workspace opens on
  it. Everything the dashboard does in a terminal it does here: launching,
  Past Sessions, Plans, Worktrees, Runs, the action menu. What changes is
  where things show. Launching an agent, resuming or forking a conversation,
  restarting a session, or pressing Enter on a row takes you to that session's
  own view in the workspace, and the Dashboard stays where it was behind it.
  New agents launch on tmux.
- **Overview** (⌘O) is the live session overview. Enter on a tile goes to the
  session's view the same way.
- **Shell**, **editor**, and **scrollback** views open when the Dashboard asks
  for them (*Open shell*, *Open editor*, *Peek scrollback*, the plan and
  worktree editors) and from **View › New Shell** (⌘T). Each is listed while
  its program runs and goes away when it exits; **View › Close** (⌘W)
  ends the one you are on, and so does the × in the top right corner of its row.
- The same × on a session's row **ends that session's agent**, as closing it
  from the Dashboard does. It asks first when the agent is working or waiting
  on you; an idle one closes at once. The conversation stays on disk and can
  be resumed. ⌘W on a session does the same. A session that is a Kitty window
  has no ×: close its tab.
- **Session › New Terminal Below** (⌘↩) opens a shell where the session you
  are on is working and lists it right under that session; pressed again, it
  adds another. Click one to go back to it, or step to it: ⌘] and ⌘[ stop
  at every session and every terminal under one, in sidebar order; ⌘⇧] and
  ⌘⇧[ go from session to session, passing over the terminals. The digits
  (⌘1 to ⌘0) count sessions only. It applies to sessions whose terminal
  is in the workspace, not to Kitty sessions. If the session ends first, the
  shell keeps running and moves up to the **Workspace** section.

A view is not a session. It is never counted as waiting, never shown in the
status menu, and skipped by the next-attention shortcut. Its program runs
without any session's identity, so a harness you start by hand in a shell
view is not mistaken for a managed agent. Closing the window detaches the
session terminals but leaves a running shell, editor, or the Dashboard alone;
they end when they exit, when their view is closed, or when the app quits.

The global dashboard shortcut shows the workspace's Dashboard. Preferences ›
Show the dashboard in can send it to Kitty instead: it then focuses the
dashboard there, or opens one, and shows the workspace's only when there is no
Kitty to ask. **Launch Project** in the menu launches on tmux and
shows the new session in the workspace.

### Keys, history, and the mouse in a terminal

Whatever has the keyboard decides where a key goes.

- In a **session's terminal** every key without ⌘ is the agent's: Return,
  Shift+Return, Escape, Tab, the arrows, Page Up, the function keys, and
  every Control and Option chord. The one exception is **F12**, which
  detaches the view (the agent keeps running and never sees that key).
- In the **Dashboard** or any other utility view the same keys go to the
  program running there.
- In the **session list** the arrow keys move the selection and Return goes
  into the selected row. Nothing typed there reaches a program.
  **Window › Session List** (⌘⇧L) puts the keyboard there.
- The button in the title bar, or **View › Toggle Sidebar** (⌘B), hides the
  sidebar so the terminal has the whole window, and brings it back; ⌘⇧L
  shows it too. The choice is kept across launches.
- The button at the other end of the title bar, or **View › Toggle
  Inspector** (⌘⇧B), shows a second sidebar on the right, hidden until it is
  asked for. Its **Waiting** tab lists every session waiting on you, in the
  order the session list has them (the order **F4** steps through), with why
  and for how long; a click goes to the session as a click on its line in
  the session list does. Its **Session** tab says what the daemon knows
  about the session the pane is on (state, reason, times, flags, profile,
  conversation and runtime-session IDs, branch), copies an identifier when
  its line is clicked, and has buttons for what ⌘R and F5, F6, F8, F10, and
  F11 do. Nothing in it takes the keyboard, and whether it is shown and
  which tab it is on are kept across launches.
- **⌘1** to **⌘0** go to the first ten sessions in the order the sidebar
  lists them, top to bottom: ⌘1 is the first session and ⌘0 the tenth.
  Shells and other opened views are not counted. **⌘]** and **⌘[** step to
  the next and the previous session or terminal under one, wrapping; **⌘⇧]**
  and **⌘⇧[** step to the next and the previous session, skipping the
  terminals. From a terminal, the previous session is the one it is under.
- Without Shift those keys never leave the app: a session that lives in
  Kitty is selected and its pane says where it is. Add Shift (**⌘⇧1** to **⌘⇧0**, **⌘⇧]**,
  **⌘⇧[**) and such a session comes forward in Kitty instead, as a click on
  its row does. A session embedded here is focused here either way. macOS
  takes ⌘⇧3, ⌘⇧4, and ⌘⇧5 for screenshots before any app sees them, unless
  those are turned off in System Settings › Keyboard › Keyboard Shortcuts.
- **⌘L** goes back to the session or view shown before this one, and again
  returns: the two most recently shown.
- **⌘R** names the session shown (**Session › Name Session…**), and so does
  **Name Session…** in the menu a right-click on a session's line opens. A
  named session has the name on a first line of its own, bold and dark blue,
  above its profile; one with no name looks as it always did. An empty name
  removes it. The name is kept with the session (`amx name-session`), so it
  is still there after the app was quit, and shows about two seconds after
  it was given. Only a session embedded here can be named.
- Every shortcut of the menu bar holds ⌘, so none is taken from a program,
  with two exceptions: Shift+Page Up and Shift+Page Down, and the function
  keys below. The global shortcuts work whatever has the keyboard.

**Function keys.** F1 to F11 do here what they do in Kitty, on the
workspace's sessions instead of tabs (**Session › Function Keys**). The ones
about a session act on the one shown, or on the one a terminal beside it, a
pager, or a picker is about. F12 stays the key that detaches.

| Key | What it does |
| --- | --- |
| F1 | Shows this list in the pane; `q` or Escape goes back |
| F2 | The Overview |
| F3 | Marks errors, failures, warnings, and deprecations in a session's history; again to return (see History) |
| F4 | The next session needing attention |
| F5 / F6 | Picks the session's harness icon, or its color; the pane goes back when the pick is made |
| F7 / F9 | The previous or next session, passing over terminals and staying in the app |
| F8 | Clears the session's state icon |
| F10 | Mutes or unmutes the session's notifications |
| F11 | Marks the session active (⚡) until its next real event |

F5 and F6 are for sessions embedded here; a session that lives in Kitty has
its pickers in Kitty. F8, F10, and F11 work for both.

**History** belongs to the session, not to the window showing it, so it is
all still there after the window was closed, the app was quit, or another
terminal attached. Nothing here needs a tmux key or prefix.

| Menu | Key | In a session's terminal |
| --- | --- | --- |
| View › Page Up / Page Down | ⇧Page Up / ⇧Page Down | One page through the history |
| View › Scroll to Top | ⌘Home | The oldest line tmux holds |
| View › Scroll to Bottom | ⌘End | Back to the live screen |
| Edit › Find… | ⌘F | Asks for text at the bottom of the terminal and jumps to the nearest match above |
| Edit › Find Next / Find Previous | ⌘G / ⇧⌘G | The match before, or back toward the newest |
| Session › Function Keys › Toggle Error and Warning Markers | F3 | Highlights every `error`, `failed`, `warning`, and `deprecated`, in any case, from the last one up; ⌘G steps to the one before, and F3 again returns to the live screen |

Escape, `q`, or scrolling back down to the bottom also returns to the live
screen. The wheel scrolls the same history.

An agent that draws its own full screen, as Claude Code, Codex, and Copilot
do, keeps its transcript itself. There, paging and the wheel are handed to
the agent, which scrolls its own transcript, and **Find** covers only what is
on screen: use the agent's own search for the rest (in Claude Code, Control+O
and then `/`; in Codex, F3). F3 is handed to such an agent as its own key
rather than marking anything.

In a utility view the paging items scroll that terminal's own buffer, Find is
not offered, and F3 is the program's.

**Selecting and copying.** Hold Shift and drag to select text in any
terminal, then **Edit › Copy** (⌘C). In a session whose program does not use
the mouse, a plain drag selects and copies on release. An agent that uses the
mouse receives plain clicks and drags itself. **Edit › Paste** (⌘V) pastes as one block: a pasted line break is not
a Return. A right click never opens a tmux menu.

**Links.** ⌘-click opens a link where the program is not using the mouse,
such as a shell view. Where a program does use the mouse it receives plain
and ⌘ clicks itself, and a link opens with ⌘⇧-click: that is every session's
terminal, and any utility view whose program tracks the mouse.

**Input methods** compose at the cursor; the agent receives the text when it
is committed.

## Global shortcuts

| Default | Does |
| --- | --- |
| ⌃⌥⌘A | Shows the workspace's Dashboard. Sent to Kitty in Preferences, it brings the dashboard running there forward, or opens one (`amx open-dashboard`). |
| ⌃⌥⌘N | Focuses the next session needing attention, in the order the workspace lists sessions (projects by name, then each project's sessions as arranged there), across Kitty instances and the workspace: a tmux session opens in the workspace, a Kitty session in Kitty. Beeps when nothing waits. |

Change or clear them in Preferences. They are Carbon hotkeys and need no
Accessibility permission. Preferences warns when a shortcut is taken by the
other one, by another app, or by macOS itself in System Settings › Keyboard ›
Keyboard Shortcuts, which Carbon cannot detect. A shortcut that cannot act
beeps and leaves the reason in the menu.

## Notifications

With the app running and allowed, the daemon still applies every gate (quiet
hours, mute, snooze, cooldown) and then hands each notification to the app,
which shows it through Notification Center. Clicking it focuses the pane the
way an Alerter click does; the banner's actions mute the session, snooze for an
hour, or open the ASL answer panel. A permission prompt's banner offers no
approval. For a session in the workspace it offers **Answer…**, which opens a
small panel showing the session's screen: you read the prompt there and press
the key that answers it (`1`–`9`, `y`, `n`, arrows, Return, or the `esc`
button), without bringing the workspace forward. If the app does not confirm delivery
within 3 seconds, or reports a failure, the daemon withdraws it and Alerter
shows it instead. Quitting the app falls back to Alerter at once.

```bash
amx notifications test   # "confirmed via app" when the app showed it
```

The first notification after a fresh install usually goes to Alerter: the
permission prompt it raises outlasts the 3-second limit.

## Checking the pairing

```bash
amx daemon status        # the companion line names each subscriber and its version
amx doctor --strict      # companion_app (info) per connected app
amx debug subscribe --frames 5   # the same stream the app reads
```

`amx doctor` raises `companion_build_mismatch` (warning) when the app reported
an `amx` build that is not the daemon's. The app reports the `amx` it resolved
in `subscribe.amx_build`, with the full sha256 of the executable rather than
the prefix `amx --version` prints, and subscribes again whenever a new
resolution selects a different binary; until one is resolved it reports none
and nothing is compared. Preferences shows which `amx` binary the app resolved
and why.

## Troubleshooting

**"daemon too old — run `amx daemon restart`".** The running daemon predates
the companion stream (it answered `subscribe` as an unknown request). The app
falls back to polling `health` every 10 seconds and retries `subscribe`; menu
items that need the daemon are greyed out with the same advice. Restart the
daemon after installing a current `amx`.

**`not running` in the menu.** No daemon owns the socket. The app never starts
one on its own; choose **Start daemon** or run `amx daemon start`.

**`reconnecting` or `unresponsive`.** The stream ended or went quiet. The app
redials with backoff, reconnects within 0.5s after a `bye` (as after
`amx daemon restart`), and treats 30 seconds without a frame as a half-open
socket. A wake from sleep reconnects at once.

**Build mismatch.** `companion_build_mismatch` means the app runs an `amx`
that is not the daemon's build, usually after `just install` without
`just mac-install`, or the reverse. Reinstall the side that is behind. The app
prefers `~/.local/bin/amx` over its bundled copy when the local one matches
the daemon, is newer, or is the same clean build; to pin one, set
Preferences › amx binary.

**"no supported tmux found".** A workspace launch needs tmux, and none could
be used. The app's own tmux is used whenever the bundle has one, so this
means a build without it (a hand-made Debug build), a bundle whose tmux no
longer matches its checksums (reinstall the app), or a
`[defaults.tmux] path` in `config.toml` that names something that is not a
tmux 3.5 or newer: an explicit path is the only one tried. Preferences ›
Workspace shows which tmux is in use and, when none is, each place that was
tried and why it was refused; `amx backend status` prints the same.

**Grayed-out menu items.** The resolved `amx` lacks the command
(`project`, `open-dashboard`, `mute-toggle --json`); the tooltip says which.
Install a current `amx`.

**Kitty does not come forward, or shows the wrong Space.** The app is an
accessory app and activates Kitty cooperatively: it yields activation to the
Kitty process that owns the socket and asks it to activate. macOS 14 and later
may decline a cooperative activation when the app itself was not active, and
when only one Kitty runs the app also opens it through Launch Services, which
macOS does honor. With several Kitty instances that fallback cannot name one,
so it is skipped. Kitty focuses the tab and window either way; whether macOS
switches to the Space holding that OS window is governed by System Settings ›
Desktop & Dock › "When switching to an application, switch to a Space with
open windows for the application". The app deliberately does not use
AppleScript or Accessibility to force a Space switch.

**Notifications never arrive in the app.** Check System Settings ›
Notifications › Agent Mux and Preferences › Use app notifications. After an
ad-hoc install the permission resets; allow it again. `amx debug tail` names
the backend each dispatch used (`app` or `alerter`).

**Logs.**

```bash
log show --last 5m --predicate 'subsystem == "io.github.sglavoie.agentmux"'
```

## Known limits

- **Find in a full-screen agent** reaches only the visible screen; the rest
  of the conversation is in the agent's own transcript and search.
- **Mute lag.** A mute made from the app (or `amx mute-toggle`) is written to
  Kitty at once, but the daemon learns it on its next Kitty listing, at most
  one render cadence (2 seconds) later. A notification raised inside that
  window is not yet suppressed. The menu row shows a mute made from the app
  at once and keeps showing it until a snapshot agrees, for up to 5 seconds,
  so reopening the menu right away does not offer the toggle again. See
  [Daemon › Mute lag](daemon.md#mute-lag) for why there is no `mute_set`
  request.
- **Paused ASL runs** are listed for answering only when their pause left an
  open supervisor question.
- **One Kitty per window ID.** Window IDs mean something only inside the Kitty
  that issued them, so Mute never tries another instance's socket.
- **Other Kitty instances.** The daemon lists the instance whose hook it heard
  last and, on the same cadence, each other instance that has a session, so
  those sessions are named from their own tabs. Each is kept until its own
  instance stops reporting the window or that Kitty exits, never because a
  listing of another instance could not see it. Window IDs are still bare, so
  two instances' sessions that share a window number share one daemon state.
- **Codex hooks run in a shared process.** Codex 0.160 runs hooks inside its
  managed `app-server`, which carries the Kitty window of the pane that started
  it, so every Codex session's hooks are attributed to that one window.
