# Notifications and answer panels

[Back to the macOS development guide](../README.md)

## Notifications

While it runs, the app can be the daemon's notification backend
(`Shell/NotificationPresenter.swift`). The daemon still applies every gate
(quiet hours, mute, snooze, cooldown). It then sends each notification that
passes them as a `notification` frame to the subscriber holding the role, and
the app shows it through `UNUserNotificationCenter`.

**The role.** The app claims the role in `subscribe` while
`amxUseAppNotifications` is on and macOS allows its notifications. It also
claims the role while it has never been asked, because the daemon sends frames
only to a claimant. It asks for permission when the first frame arrives, not
at launch. Turning the preference off, a denied prompt, or permission revoked
in System Settings drops the claim, and the app subscribes again without it.
The app reads the permission again whenever it comes to the front, whenever
the menu opens, and when Settings opens, because macOS gives no notice of a
change.
While it is denied, Settings says so under Use app notifications, with an
Open System Settings button for System Settings › Notifications.

**Fallback.** The app reports `delivered` once `add` succeeds. A failed `add`,
a denied permission, or the preference turned off reports `failed`. The daemon
then sends that notification through Alerter, as it does when no app holds the
role, or when `delivered` takes longer than 3 seconds. That 3-second limit
covers the first permission prompt, so the notification that raised the prompt
usually ends up on Alerter.

**Banners.** Each banner's identifier is the daemon's `notification_id`, and
its thread is the session's runtime session ID, so one session's banners group together.
Permission and question (`elicitation`) banners play the default sound, also
while the app is in the foreground (its menu open); other banners are silent. The
category depends on the actions the daemon offered, and a banner never shows
an action that was not offered:

| Category | Actions | Used for |
| --- | --- | --- |
| `amx.session` | Mute Session, Snooze All for 1 Hour | A runtime session notification |
| `amx.session.snooze` | Snooze All for 1 Hour | One with no session to mute |
| `amx.asl` / `amx.asl.snooze` | Answer…, then the offered session actions | An ASL question |
| `amx.prompt` | Answer… | A permission prompt of a tmux session, when the daemon offered `respond` |
| `amx.plain` | none | Every other permission prompt, and any notification offered none |

**Responses.** A click reports `clicked`, and the daemon focuses the pane
exactly as it does for an Alerter click. A dismissal reports `dismissed` (every category sets
`customDismissAction`). An action reports `action_mute`, `action_snooze`,
`action_answer`, or `action_respond`, and the app carries it out:
- Mute runs `amx mute-toggle` on that session, unless the session is already
  muted: `--runtime-session` identifies the target.
- Snooze sets the same 1-hour snooze of every notification as the
  Notifications menu.
- Answer opens the [ASL answer panel](#asl-answer-panel) on the notice's run.
- Answer on a permission prompt opens the [prompt answer
  panel](#prompt-answer-panel) on the notice's session.

A `notification_retract` frame removes the banner. A retracted notification
that is still waiting on the permission prompt, or on `add`, is never shown.
When the feed's connection ends the daemon ends every delivery it made on it
and can never retract one, so the app removes the banners it showed on that
connection then (`NotificationPresenter.connectionEnded`); the app's own
terminal notices stay. Banners left by an earlier launch are removed at
launch.
The `amx debug tail` dispositions name the `app` backend.

**Terminal notices.** A program's own desktop notification (OSC 9 or 777) in
an embedded terminal is shown as a plain banner titled by its session or view
(`NotificationPresenter.presentTerminal`), only while app notifications are on
and allowed: it never asks for permission, and the daemon never hears of it.
It obeys the snooze (the daemon's, or the mirrored one while the feed is
down) and its session's mute, and is not shown while its session or view is
in the pane of the key workspace window. Its thread is its session or view,
and the app withdraws that thread's notices when the pane moves to it or the
workspace window becomes key on it. Clicking one from a session shows that
session; from a utility view, it opens the workspace on that view, if it is
still open (navigation only: a view never counts toward attention).

## ASL answer panel

An ASL run waiting on its supervisor shows ❓ on its session row, and an
indented **Answer…** item follows that row (`Answer… (3 questions)` when more
than one is open). A run binds to the runtime session that names it; a run
no listed session claims is listed under **Supervisor questions** instead.
The `amx.asl` banner's Answer… opens the same panel.

The panel (`UI/ASLQuestionPanel.swift`) floats above other windows and does not
activate the app until the reply editor takes focus. It reads the daemon, never
`amx`:

1. `asl_snapshot` finds the run's row, its permitted actions, and its action
   token. The menu's waiting list carries neither a body nor a token.
2. `asl_question` fetches one question body at a time, as you move between the
   run's open questions.
3. `asl_action` sends each submission once. It is never resent automatically.

The rules are ported from `tui/asl` into `Model/ASLResponseState.swift`, so
the panel and the TUI's Runs view agree:

- A live wait offers **Reply**, **Continue**, and **Pause**. A paused run
  offers only **Reply and Resume**. Reply needs text; ⌘↩ sends it.
- The form opens only when the daemon reports ASL reachable, the snapshot is
  not stale, mutations are enabled, the row carries a token, and the row
  permits the method. Otherwise the panel shows the same fixed sentence the TUI
  shows, including ASL's own `disabled_reasons`.
- A failed submission keeps its operation ID, so retrying the same button after
  an unknown outcome replays the bridge's receipt instead of applying it twice.
  Another button mints a new ID.
- After an accepted closure the panel moves on to the run's next open
  question. That question's fetch re-mints the token, and nothing is sent
  until it has. The last question, or a resume, closes the panel.
- A refusal is shown as a fixed sentence chosen by its bridge code, never the
  bridge's message. A refusal meaning the run moved (a stale token, a closed
  question, a stale snapshot) closes the panel, shows a notice in the menu,
  and rebuilds the menu.
- A question body that cannot be read says "could not load the question",
  never an action's wording, and nothing can be sent until **Load Again**
  has read it. That includes the re-authorizing fetch after a closure.
- A disabled button says why in its tooltip, and the caption under the reply
  says why Reply is: "Type a reply", "Loading…", "Re-authorizing…".
- While a submission is on its way, Close (and Escape, and the title bar's
  close button) wait for it. One whose panel went anyway, because another run
  was opened, still reports how it went as a notice in the menu.

The question body is held only while the panel is open. Drafts not yet sent
are kept, in memory only, per run: closing the panel or opening another run
keeps them, and reopening that run puts them back for the questions it still
has open. They go when the run's questions move: answered, a moved run, or a
run no longer waiting. `SecretText` and the state's `description`, `dump`
mirror, and request descriptions are all redacted, so the logs (category
`asl`) name only methods, run, question, and operation IDs, and refusal codes.

## Prompt answer panel

A permission banner never approves anything: the operator has to see the
prompt before answering it. For a session on the private tmux server the
banner can offer to show it. Its **Answer…** opens a floating panel
(`UI/PromptAnswerPanel.swift`) with the tail of the session's screen and a
strip of keys, and the workspace does not have to come forward or attach.

The panel decides nothing about whether a session may be written to. It runs
two `amx send` invocations and shows what they answer:

1. `amx send <runtime-session-id> --look --json` reads the screen. It runs
   when the panel opens, on **Look Again**, and 0.6 seconds after every key.
2. `amx send <runtime-session-id> --key <name> --json` presses one key.

The rules are in `Model/PromptAnswer.swift`:

- No key can be pressed before a look has answered, or while a key or a look
  is on its way.
- Keys are enabled only while the look says the screen is asking something.
  `amx send` checks the same thing again when it writes, so a prompt that
  went away in between is a refusal and not a stray key.
- The strip is `1`–`9`, `↑` `↓` `↩`, and `y` `n` `esc`. Typing a digit, `y`,
  `n`, or Return in the panel presses that key; Escape closes the panel, so
  the session's Escape is the `esc` button.
- A session running an agent-session-loop run answers the first press with
  "press the key again to confirm"; the second carries `--yes`. A different
  key is asked about afresh.
- A refusal is shown as a sentence chosen by its code, not `amx send`'s own
  message, which names command-line flags.
- A key held with ⌘, ⌃, or ⌥ is left to the menus and never reaches the
  session.
- A first look that fails can be tried again with **Look Again**, and
  reopening the panel on that session starts over. A look you ask for clears
  the notice an earlier key or look left (the look 0.6 seconds after a key
  keeps that key's), and a confirmation asked before it is asked afresh.

The daemon offers `respond` only on a `permission` notice whose target is a
tmux runtime session, and the app shows Answer… only when that was offered
and the notice names such a session. The logs (category `prompt`) name
sessions, key names, and refusal codes, never screen text.
