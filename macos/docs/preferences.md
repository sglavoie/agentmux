# Shortcuts and preferences

[Back to the macOS development guide](../README.md)

## Workspace shortcuts

Settings › Shortcuts › Workspace shortcuts records keys for the new View commands and Command
Palette. `WorkspaceShortcut` owns stable IDs, default bindings, and action
selectors. Changes update existing menu items immediately; launch restores them
from `AppPreferences`. These are local menu equivalents and are never registered
with Carbon. Command-only combinations are accepted without weakening the global
shortcut recorder's restrictions. Existing menu keys (including hidden aliases),
other configurable commands, and the two global bindings are checked for conflicts.
Conflicting stored bindings remain visible in Settings but are inactive.

The terminal checks configured equivalents before encoding keys, including
shifted punctuation and Control-Return. It retains Ghostty key sequences and IME
composition handling, and validates the menu action before invoking it. Settings
stops recording when another recorder starts, the selected command changes, the
window closes, or the window loses focus.

## Global shortcuts

Two Carbon hotkeys (`Shell/HotKey.swift`), which need no Accessibility
permission:

| Default | Does |
| --- | --- |
| ⌃⌥⌘A | Shows the workspace's Dashboard. |
| ⌃⌥⌘N | Focuses the next session needing attention (`Model/AttentionCycler.swift`) and reveals a workspace session in the sidebar, as ⌘J does. |

The next-attention shortcut visits the attention rows (stop, key, error,
question) in the order the workspace sidebar lists every session
(`WorkspaceSidebar.ordered`): promoted attention sessions, then pinned sessions
and projects in their saved order. How long a session has waited moves
nothing, and the menu's Needs attention section lists them in the same order.
Each press focuses the one after the session the last
press focused, and wraps after the last one. A session is identified by its
runtime session ID (`AttentionCycler.Key.embedded`). When the session it
last focused no longer waits, or is gone, the cycle carries on from where
that session stood instead of starting over. With nothing waiting, it beeps.

A shortcut that cannot act beeps and leaves the reason as the menu's notice.
Preferences records shortcuts, and a change is registered at once. The
recorder refuses, saying why in its button, a key with no ⌃, ⌥, or ⌘ (other
than F12 to F20), ⌘ alone or with ⇧ (every app's own menu keys), and a bare F1
to F11 (the workspace's keys); `HotKeyCombo.refusal` is the rule. It warns
when a shortcut is already used by the other one (the dashboard's wins), is
held by another app (Carbon's `eventHotKeyExistsErr`), is one macOS owns in
System Settings › Keyboard › Keyboard Shortcuts, which Carbon cannot see, or is
also a key of the app's own main menu. While a recorder listens, both
shortcuts are unregistered so they can be recorded; the recorded one is saved
before the recorder stops, so registering again happens once, with the new
shortcut. Shortcuts are shown with the key the current keyboard layout puts
there (`UCKeyTranslate`; ⌃⌥⌘Q on AZERTY for the stored `ctrl+opt+cmd+a`), and
the status menu's Open Dashboard and Next Waiting Session show their
shortcuts as their keys. Settings and the conflict warnings name each
shortcut by its menu item (`HotKeyPreference.label`).

## Preferences

Preferences (`UI/PreferencesWindow.swift`, SwiftUI in an NSWindow titled
Settings, opened by Settings… in the status menu and the app menu)
saves each change as it is made, in the `io.github.sglavoie.agentmux` defaults
domain. The amx binary path is saved when its field is submitted or left (or
the window closes), not per keystroke, so a half-typed path never replaces the
amx the app runs.
Settings are organized into General, Shortcuts, Appearance, Workspace, and
Advanced tabs. The tabs stay visible while each pane scrolls independently.
Launch Settings opens the Workspace tab at the launch destination. Switching
tabs commits edited binary/environment fields and ends shortcut recording.
An absent, mistyped, or out-of-range value reads as its default.

| Key | Default | Meaning |
| --- | --- | --- |
| `amxBinaryPath` | empty | The amx to run; empty is automatic. Preferences shows the running path and version. |
| `amxLaunchAtLogin` | false | Mirrors the `SMAppService.mainApp` login item, which survives a logout. |
| `amxHotKeyDashboard` | `ctrl+opt+cmd+a` | Empty turns it off. |
| `amxHotKeyNextAttention` | `ctrl+opt+cmd+n` | Empty turns it off. |
| `amxWorkspaceShortcuts` | `{}` | Command ID → combo string; absent uses the command default, empty clears it. |
| `amxStatusStyle` | `glyphs` | `glyphs` (`⚡2 🔑1`), `compact` (the attention total), or `iconOnly`. |
| `amxShowWorkingCount` | true | Includes `⚡N` in the glyph title. |
| `amxRecentDays` | 7 | How far back the Recent submenu looks (1–90). |
| `amxRecentLimit` | 15 | How many conversations it lists (1–50). |
| `amxUseAppNotifications` | true | Offers the app as the daemon's notification backend. |
| `amxSnoozeUntil` | none | The last snooze the daemon reported, re-applied after a daemon restart (see Menu actions). The daemon owns the snooze, and Preferences shows the daemon's while the feed is live. |
| `amxEnvironment` | empty | Values amx runs with instead of the login shell's, for `PATH`, `XDG_*`, and `AMX_*`. |
| `amxLaunchBackend` | `workspace` | `workspace` uses tmux; `follow-cli` reads the CLI configuration before project launches and Recent conversation resumes. A stored legacy `automatic` migrates to `workspace`. Unknown or missing values use `workspace`. |
| `amxOpenWorkspaceAtLaunch` | false | Shows the workspace window when the app starts, even if it was closed last time. |
| `amxConfirmationPolicy` | `follow-cli` | Whether to ask before closing a running utility view (⌘W) or ending a session (⌘⇧W): `follow-cli` asks what `config.toml` says (`[defaults] confirm_destructive`, read through `amx config confirmation --json`) right before the action and asks when that cannot be read; `always` and `never` override it. Changes apply immediately to shortcuts, menus, sidebar close actions, and the embedded dashboards; app quit confirmation is separate. A stored legacy `amxConfirmBeforeClosing` Boolean is read only when this key is absent (true → `always`, false → `never`) and is removed on the next save. |
| `amxReadingProseSize` | 13 | Prose size in History, Activity and Waiting (11–24 points), independent of code/diff/terminal-preview size. |
| `amxDefaultFontSize` | none | Default terminal zoom in points (6–72); unset uses the Ghostty config. Changes apply to all open terminals; ⌘⇧- resets to this size. |
| `amxSidebarZoomPercent` | 100 | Zoom of the workspace's sidebar and inspector in percent (70–200, in 10% steps from ⌥+ and ⌥−). Changes apply to the open window at once; out-of-range stored values read as 100. |
| `amxSidebarGrouping` | `stateThenProject` | `stateThenProject`, `projectThenState`, `project`, `compact`, or `projectThenBranch`; changes apply immediately. |
| `amxSidebarSort` | `manual` | `manual` or `recentlyVisited`, independent of grouping. Flat drag order and visit history use `amxWorkspaceFlatSessionOrder` and `amxWorkspaceVisitedSessions`; existing per-project drag order is preserved. |
| `amxTerminalTheme` | none | The Ghostty `theme` every terminal uses, as Ghostty writes it: `Name` or `light:Name,dark:Name`; unset uses the Ghostty config. Changes recolor all open terminals. |

Preferences › Workspace also shows the backend setup, read from
`amx backend status --json` each time amx is resolved
(`Model/BackendSetup.swift`): which tmux runs and where it came from, whether
the session server is running and with how many agents, and every problem amx
reports with what to do about it. A missing tmux is shown there before a
launch fails on it; when a launch does fail, its message carries the same
places tried and the same fix. The app names the bundle's runtime to every
amx it runs through `AMX_TMUX_RUNTIME`, so `~/.local/bin/amx`, when the
resolver prefers it, still runs the pinned tmux.

A change to the binary override or an environment value resolves amx again
once typing pauses; the login shell is not asked twice. The login item is
SMAppService's to keep. At launch the stored choice is set to match it, so a
login item removed in System Settings is not added back.
