# Shortcuts, notifications, and troubleshooting

[Back to the companion guide](../companion-app.md)

## Review-window shortcuts

| View | Keys |
| --- | --- |
| History | ⌘F search; Down from search reveals results; ⌘G / ⌘⇧G next/previous match; ⌘R refresh transcript; ⌘Return resume/focus; ⌘⇧Return fork; ⌘⌥S toggle results |
| Changes | ⌘G / ⌘⇧G next/previous hunk; ⌘R refresh comparison; ⌘E open file; ⌘⌥F reveal/focus files; ⌘⇧E edit base; ⌘⌥S toggle files |
| Runs | ⌘R refresh; ⌘⌥F reveal/focus runs; ⌘Return open session; ⌘⇧I inspect failed check; ⌘⌥S toggle runs |
| Activity | ⌘R refresh; ⌘⌥F reveal/focus events; ⌘Return focus live session; ⌘⇧Return open recorded conversation; ⌘⌥S toggle events |

**⌘W** closes a standalone review window and returns to the workspace when open.
The four review windows share one minimum size and remember their placement. Each
reads again when it is shown; Checkout Changes also compares again when you come
back to the app, since an agent may have edited files meanwhile, and Runs polls only
while its window can be seen.
In a split workspace, review shortcuts belong to the focused review pane.
Refreshes retain selection and focus; toggling a sidebar retains reading position.

## Global shortcuts

| Default | Does |
| --- | --- |
| ⌃⌥⌘A | Shows the workspace's Dashboard. |
| ⌃⌥⌘N | Focuses the next session needing attention in sidebar order. Beeps when nothing waits. |

Change or clear them in Preferences. They are Carbon hotkeys and need no
Accessibility permission. Preferences warns when a shortcut is taken by the
other one, by another app, or by macOS itself in System Settings › Keyboard ›
Keyboard Shortcuts, which Carbon cannot detect. A shortcut that cannot act
beeps and leaves the reason in the menu.

## Notifications

With the app running and allowed, the daemon still applies every gate (quiet
hours, mute, snooze, cooldown) and then hands each notification to the app,
which shows it through Notification Center. Clicking it shows the tmux session in the workspace,
opening the window if it is closed; the banner's actions mute the session, snooze for an
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
- **Paused ASL runs** appear in Waiting for answering only when their pause left
  an open supervisor question. **View › ASL Runs…** also lists pauses without one.
- **Codex completion visibility.** Managed interactive launches select `--no-daemon`
  when the launched CLI advertises support, preserving the terminal's hook environment
  across resume and `/new`. Explicit remote transport choices and older CLIs retain
  their behavior. Shared-server hooks with ambiguous or unbound owners are rejected;
  they are not borrowed from another terminal. The mitigation requires an updated
  installation and a newly launched managed client. Existing clients are not rebound.
  The operator confirmed the installed-build resume, `/new`, app-reopen, and physical
  sleep/wake checks on 2026-10-18.

### Waiting controls and update times

Waiting context distinguishes loading, failed reads, and unavailable questions.
**Refresh** keeps the current content and scroll position; **Retry** retries only
the read. Failed refreshes retain the previous content and its original read
time. ASL questions use the same Markdown reader as History, including code-copy
controls; terminal captures remain monospace. A replaced question clears its
context immediately and cannot be answered through the old card.

The Session inspector describes prompt reasons in ordinary language and shows
the recorded evidence supporting the displayed state: screen or transcript
inference, harness/process lifecycle, subagent, or ASL. **Observed** is that
fact's timestamp, independent of the latest event from another source. Missing
or older-daemon evidence stays explicitly unavailable; it is not proof of idle.

While using Find in an embedded agent, **Search Full Conversation…** opens
History on that exact conversation and carries the active terminal query.
Terminal Find still labels whether it covers the visible screen or terminal
history. History searches saved prose using words and quoted phrases, not
terminal regular expressions. An unavailable query read is explained. Closing
History returns focus to the original visible terminal. Utility views keep
their own terminal Find; they have no conversation to search.

Expanded Waiting cards put the question directly below the session title and
Answer controls, with a compact refresh icon beside Hide context. Read time and
retained-read failures appear below the question; failures keep an explicit Retry
button. The collapsed card keeps its reason text, and accessible labels retain
session context. Lighter card backgrounds keep the prose prominent.

Waiting keeps category and Oldest first on one row. Next Waiting stays available;
Update List appears only when changes are held while you read. The keyboard hint
appears while the Waiting reader has focus and disappears when focus leaves.
Changed questions immediately lose their answer actions, even before you apply
an updated list.

Review toolbars show **Updated 12s ago** (then minutes, hours, or days), using
the oldest successful read of the displayed content. Refresh failures and
warnings take priority; slower reads show Refreshing. Click the status for each
read's exact timestamp, missing reads, and retry actions. Updating a run list
alone cannot make older plan details appear new. **Show in Finder** reveals the
selected plan session file; **Stop After Session…** requests a stop after the
current session finishes.

### Resolved action feedback

Retained warnings and errors show **Needs attention** until matching evidence
resolves them. A successful readiness check resolves only the same plan and options;
a successful resume resolves only that conversation and operation; a new
worktree review resolves a changed-review warning; a successful landing resolves
that worktree's landing failure. A matching final receipt resolves an unknown
receipt outcome, including when the receipt reports rejection (the rejection remains
a separate warning). Unrelated successes and ordinary refreshes do not clear action
failures. A later fork can create a different conversation, so it does not resolve
an earlier fork's failure or unknown outcome. Resolved notices retain their original message, resolution evidence, and
time across restarts, subject to the existing 30-entry bound. Repeated failures
reopen the notice. Older notices without an exact issue identity remain dismissible.

### Accounted-for sessions and notes

Press **⌘⇧K** to mark the current session accounted for, or clear its acknowledgement
if it is already marked. This works while its terminal has focus. The **Session**
menu shows the current action; customize the shortcut in Settings.

Right-click a session or project heading and choose **Mark Accounted For** to
record that you have checked its current work. The action keeps your terminal
selection and focus. A project action covers its current sessions across profiles,
including collapsed and pinned sessions; a later arrival remains unacknowledged.
The sidebar shows **Accounted for** or the number of sessions needing review.

Choose **Edit Session Note…** or **Edit Project Note…** in the same menu to open a
small note editor. Save a one-line note of up to 500 characters, or save an empty
note to remove it. Notes appear in the Session inspector and on sidebar hover.
They persist after acknowledgement expires. **Clear Acknowledgement** preserves
notes; **Undo Annotation** restores the prior annotation without hiding new events.

Acknowledgement expires when meaningful new attention arrives: a turn completes,
a question or permission request arrives, a warning/error occurs, or an agent
exits unexpectedly. Working/compacting/subagent activity does not expire it.
Questions and errors remain actionable even when acknowledged. Notification mute
and snooze settings remain independent. The app and dashboard share these records
through the daemon; offline controls are disabled. See
[session annotations](../usage/filtering.md#accounted-for-sessions-and-notes) for CLI and storage.
