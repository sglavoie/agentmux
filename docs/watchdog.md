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

All three apply to a tmux session as they do to a Kitty window, and need no
Kitty: a daemon with none running sweeps its tmux sessions every second.

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

`hook_silence = 0` on a per-profile override does not disable the silence
sweep for that profile: zero (like an omitted key) inherits the global
`[defaults.watchdog] hook_silence`, and a negative value is rejected. There is
no per-profile switch to turn the sweep off. A larger per-profile value makes
that profile's notification rarer, and `hook_silence = false` under
`[defaults.notifications]` suppresses the notification for every profile.

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

## Screen classifier patterns

The idle probe that confirms a held stop reads the pane's screen and decides
whether the harness is still working. That decision is made by matching the
harness's own UI copy — status lines, interrupt hints, composer placeholders,
the context-limit banner. Harnesses redesign those strings without warning, and
when they do the classifier stops matching and the affected tab holds ⚡ until
the watchdog's stale threshold clears it.

The UI copy is therefore data, not compiled-in constants. Each pattern set
ships embedded in the binary and can be replaced from:

```text
~/.config/agent-mux/screenstate/<name>.toml
```

`<name>` is the pattern set, which is usually a harness (`claude.toml`,
`codex.toml`) but not always: `contextlimit.toml` holds the copy for the
context-limit banner, which is shared rather than owned by one adapter. Each
file must declare its own name at the top, e.g. `harness = "claude"`.

An override **replaces** the embedded set; it is never merged into it. Half one
release's UI copy and half another's is a classifier that vetoes nothing and
settles a live pane, which is a worse failure than either set alone. So copy the
shipped file, edit it, and keep the whole thing. The shipped files live in the
source tree as `internal/screenstate/patterns_<name>.toml` and
`internal/contextlimit/patterns_contextlimit.toml`, and each documents its
clauses at the definition site.

Patterns are loaded once, on the startup goroutine, before any probe runs. There
is no file watcher and no config-reload path, so **reloading means
`amx daemon restart`**.

A clause is consumed in one of four ways, and the difference matters when
editing:

| Section | How it matches |
| --- | --- |
| `[regex]` | Compiled and matched per line. Write the source as a TOML literal string (single quotes) so backslashes reach the compiler unmangled. |
| `[phrases]` | Substring search against the case-folded screen. Entries must be lowercase; the loader rejects a file that mixes case. |
| `[group]` | `all` (every ref matches) plus `any` (at least one ref matches) over other clause names. Either half may be used alone. This is how the two Codex prompt conjunctions are expressed. |
| Special cases | `composer_placeholder` is compared for **equality** against the composer's captured remainder, so adding a substring to it does nothing. The `contextlimit` `chrome_*` lists are matched **case-sensitively** against the unfolded line, and `chrome_prompt_rune` is a rune set, so an entry longer than one rune can never match. |

Nothing bad happens if a load fails: every failure degrades to the embedded
default and is reported rather than crashing the daemon. A daemon that refused
to start over a typo in a pattern file would be worse than one classifying with
the shipped copy.

### Required clauses

A clause the installed set does not define simply never matches. For most
clauses that is a safe way to degrade — it costs the classifier one piece of
positive evidence, and the pane holds ⚡ a little longer than it needed to.

For some clauses it is the opposite. The classifier settles a pane only when
**no veto fired**, so a clause that is the sole source of a veto is a *refusal*:
drop it and the veto silently cannot fire, the screen looks quiet, and a running
turn settles to a false stop. `claude.toml`'s `working_status_line` is the
sharpest case — without it, a live turn on a framed empty composer reads as
quiescent.

So an override must define every clause that carries a refusal, and the loader
checks that before installing the file. This is separate from parsing: a file
can be perfectly well-formed TOML with a perfectly valid regex and still be
rejected here. The check covers three things:

- **presence** — the clause is defined at all;
- **section** — it is defined where the classifier reads it. A clause moved from
  `[regex]` to `[phrases]` resolves to nothing, so `working_status_line` as a
  phrase list is rejected exactly like a missing one;
- **capture groups** — for the one clause read positionally. `codex.toml`'s
  `composer_line` must keep its capture group, because the classifier reads
  submatch 1 as the composer's remainder; without the group every matching line
  hands it an empty remainder and any chevron-prefixed transcript line reads as
  an idle composer.

The required set is deliberately a *subset* of the clauses each adapter reads,
not all of them. Clauses that only grade a settle that already survived every
veto — Claude's `completed_status_line` and `interrupted_status_line`, Codex's
`worked_for_line`, `completed_marker_line`, `interrupted_line` and
`composer_placeholder`, and `contextlimit`'s `chrome_*` lists — stay optional,
because losing one makes the probe demand *more* stabilization evidence rather
than less. An override written against an older release therefore still loads,
as long as it kept the refusals.

Rejection is reported as `screenstate_override_invalid`, and the detail names
each missing clause and what its absence would cost. The embedded set stays in
force, so the daemon comes up classifying with the shipped copy.

The shipped files are held to the same contract, in tests and at load time, so a
clause renamed on only one side of the pair fails the build rather than shipping
a classifier that quietly cannot refuse.

### What doctor reports

`amx doctor` prints the active source for every installed pattern set:

```text
screen patterns:
- claude: override (/Users/you/.config/agent-mux/screenstate/claude.toml)
- codex: embedded
- contextlimit: embedded
```

A valid override is the supported configuration and raises nothing — the source
line is the report. Two findings can appear:

- **`screenstate_override_invalid`** (warning). An override file exists but was
  not used: unreadable, undecodable, missing a required clause (above), or the
  config directory could not be resolved at all. The daemon kept the embedded
  default, so this is degraded-but-working and does not fail `--strict` on its
  own. The finding names the file and the exact parse or validation error; fix
  or remove the file and run `amx daemon restart`.
- **`screenstate_persistent_decline`** (failure). The classifier has refused the
  *same unchanging screen* on a live window 60 consecutive probe ticks in a row
  — two minutes at the 2-second probe cadence. This one does fail `--strict`.

### Responding to a persistent decline

A persistent decline is the failure this whole mechanism exists to make visible:
the patterns no longer match the harness's UI, so the pane will hold ⚡
indefinitely and nothing else in the system will say why.

The conjunction is what makes it trustworthy. A pane doing real work repaints
its elapsed-time counter on every frame, which changes the screen fingerprint
and restarts the streak at one, so no amount of legitimate activity can reach
the threshold. A long streak against a *stable* fingerprint means the screen
went quiet and the classifier vetoed it anyway.

The finding carries the probe source, the streak, the veto set, and the status
line the classifier saw. Those last two are the diagnosis: diff the reported
status line against the corresponding clause in the shipped pattern file and the
copy that changed is usually obvious. Then:

1. Copy the shipped `patterns_<harness>.toml` to
   `~/.config/agent-mux/screenstate/<harness>.toml`.
2. Edit the clause the reported status line no longer matches.
3. `amx daemon restart`, and confirm `amx doctor` now reports `override` as that
   set's active source.
4. Confirm the tab settles. The streak clears on a real settle and on window
   closure; it deliberately survives a reducer-driven idle-evidence reset,
   because that proves nothing about whether the classifier works.

An override is a stopgap that keeps one machine working; the fix belongs in the
shipped pattern file. `amx debug bundle` embeds the doctor report, so a bundle
carries both findings without any extra step.
