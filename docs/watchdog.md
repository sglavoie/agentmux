# Watchdog and tab state

## Watchdog

The daemon includes a watchdog that turns three independent signals into a
"can I trust my icons?" guarantee:

1. **Per-state stale thresholds.** Each active-class icon (⚡ active, 🤖
   subagent, 🌀 compact) has its own grace period. When a session displays
   one of those icons but the daemon stops receiving fresh hook events for
   longer than the threshold, the daemon clears the icon and (optionally)
   fires a one-shot notification.
2. **Glanceable run timer in the dashboard.** The list shows how long the
   current busy stretch has been running for each active-class row, and
   clears the cell to `—` the moment the row leaves that class. As the
   most recent hook event ages toward the configured threshold the cell
   shifts into a warning style with a ⏳ beside the running timer, so a
   stuck session is visible before it becomes stale.
3. **Account-level hook silence.** A separate sweep watches whether each
   profile is producing _any_ hook events at all. If an entire profile
   goes silent for longer than the hook-silence threshold (default 4
   hours), the watchdog fires a single notification — useful for catching
   a misconfigured `settings.json` or a daemon-down window where every
   session simply looks idle.

Configure the four thresholds under `[defaults.watchdog]`:

```toml
[defaults.watchdog]
active = 600           # bolt icon; bursty tool loops settle within ~10 min
subagent = 1800        # robot icon; agentic workflows can legitimately last ~30 min
compact = 180          # compaction icon; should always finish within minutes
hook_silence = 14400   # account-level silence; default 4 hours
```

Each field independently falls back to the value above when set to `0`.
The legacy `stale_active_threshold = N` key still loads and is mirrored
into `[defaults.watchdog].active` for backward compatibility.

Per-profile overrides live on the profile itself:

```toml
[[profiles]]
name = "claude-work"
harness = "claude"
home_dir = "~/.config/claude-work"
color = "blue"
watchdog = { active = 300, subagent = 2400, hook_silence = 0 }
```

`color` is required on every profile and sets the project-title color in
the dashboard, picker, detail header, and account chips, as well as the
inactive Kitty tab-bar accent on every session launched under that profile.
Active tab colors come from `kitty.conf` so the focused tab remains prominent.
Harness identity uses `[harness_emojis]` for the tab-title prefix and
`[harness_colors]` for harness-name cells in the dashboard and launch picker
(see the starter `config.toml`).

`hook_silence = 0` on a per-profile override disables the silence sweep
for that profile only.

The notifications themselves are gated by `[defaults.notifications]`:

```toml
[defaults.notifications]
# alerter_path = "/custom/path/to/alerter"
stale_active = true
hook_silence = true
```

AgentMux requires Alerter 26 or newer and does not fall back to AppleScript.
At daemon startup it resolves `alerter_path` when set; otherwise it searches
`PATH`, `/opt/homebrew/bin`, `/usr/local/bin`, and `/opt/local/bin`. An explicit
path is authoritative, so an invalid value is reported instead of silently
selecting another installation. Run `amx daemon status` or `amx doctor` to see
the selected executable and capability, and restart the daemon after installing
Alerter or changing this setting.

Clicking a session notification activates Kitty and focuses the exact originating
OS window, tab, and pane through that session's Kitty socket. If the daemon did
not receive a reusable socket or the target no longer exists, it records a
diagnostic suppression and does not guess another target. Profile-wide hook
silence notifications remain informational and do not navigate on click.

## Tab icons

The tab title is prefixed with an icon based on the latest hook event:

| Icon | Meaning |
| --- | --- |
| ⚡ | active |
| 🤖 | subagent running |
| 🌀 | compaction running |
| 🛑 | stopped |
| 🔑 | permission needed |
| ⚠️ | tool failed (recoverable) |
| ❗ | stopped with failure |

Claude pane-title work markers — a `✳` rune, any Braille-pattern rune, or any
quadrant-spinner rune — are stale-tolerant evidence, never proof of active work.
Claude reverts neither the marker frame nor the task description reliably when a
turn ends, so an idle pane can hold `✳ Fix the parser` indefinitely. The screen
scrape is authoritative for whether a turn is running; a marker title only
lengthens stabilization, requiring a four-sample run before a stuck ⚡ settles
instead of the usual two. A description body additionally gates the positive
half: it can restore a hook-missed empty tab to ⚡ once, keyed on the description
alone so a rotating marker frame cannot re-arm it.

### Stop confirmation

A harness that ends a turn and then keeps working on its own makes the tab flip
between ⚡ and 🛑 (and fire a notification) on every false ending. The daemon
therefore withholds a native terminal hook from the reducer instead of painting
it: the tab keeps showing ⚡ and 🛑 arrives only when the session has been quiet
for `delay` seconds **and** the harness's own idle probe confirms the pane is
really idle, with `timeout` as the hard deadline that commits an unconfirmed
stop so every window still converges.

```toml
[defaults.stop_confirm]
delay = 6      # quiet seconds required before a held stop may paint 🛑
timeout = 15   # hard deadline; must be >= delay
# enabled = true
```

Both fields fall back to their defaults when set to `0`. The values are read
once at daemon startup, so run `amx daemon restart` after changing them.
`enabled = false` restores the previous behaviour, where 🛑 paints the instant
the hook lands.

The hold is bypassed at arm time for pipe-mode panes, harnesses without an idle
probe, windows holding a live ASL lease, a daemon with no Kitty client, and
`enabled = false`. A bypass sends the native terminal to the reducer without the
confirmed-idle wait, which paints 🛑 immediately in every case but one: under a
live ASL lease the reducer goes on masking the stop icon for as long as the
lease is live, so those windows still show 🛑 only once the loop finishes. A
verified harness-process exit paints 🛑 instantly even if a hold is already
armed, and a new prompt or any other sign the harness is still working drops
the held stop rather than painting it. `amx daemon export-events` records each
hold as a `TerminalStopHold` event with its `stop_hold_outcome`, and a released
stop carries `stop_hold_waited_ms` plus the `stop_confirmed_by` evidence.
