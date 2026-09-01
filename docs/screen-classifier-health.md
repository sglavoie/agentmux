# Screen classifier health

The screen classifier decides whether a pane is still working by matching the
harness's own UI copy. That copy is data — embedded in the binary, overridable
per pattern set from `~/.config/agent-mux/screenstate/<name>.toml`, installed
once at daemon startup, reloaded only by `amx daemon restart`. The file format,
the four ways a clause is consumed, and the editing procedure are documented in
[Screen classifier patterns](watchdog.md#screen-classifier-patterns); this
document is the operator's half.

`amx doctor` always prints which set each pattern came from, because a stale
override that still parses is the most likely explanation for a classifier that
silently stopped matching and is invisible everywhere else:

```text
screen patterns:
- claude: override (/Users/you/.config/agent-mux/screenstate/claude.toml)
- codex: embedded
- contextlimit: embedded
```

A daemon that predates the block prints
`screen patterns: unreported (older daemon; restart required)` and raises
nothing, rather than claiming the embedded default it cannot actually confirm.
A valid override raises nothing either: it is the supported configuration, and
the source line above is the whole report. Two finding identifiers exist, and
they answer different questions:

| Finding | Severity | What it means | What fixes it |
| --- | --- | --- | --- |
| `screenstate_override_invalid` | warning | An override file exists but the daemon declined it — unreadable, undecodable, missing a clause the classifier requires, or no absolute config directory could be resolved. The embedded default is in use. | Read the finding's parse or validation detail, correct or delete the named file, then `amx daemon restart`. |
| `screenstate_persistent_decline` | failure | The classifier refused the same unchanging screen 60 consecutive probe ticks — two minutes at the 2-second cadence — on a live window that is still showing an active icon. | Diff the reported status line against the shipped pattern file, install a corrected override, `amx daemon restart`. |

The severities are deliberately different, for the same reason the six ASL
dimensions are reported separately: they are not two grades of one problem.
`screenstate_override_invalid` is degraded-but-working — the daemon kept
classifying with the shipped copy, nothing is stuck, and failing `--strict` over
a typo in an optional file would make the strict gate useless. It fires once per
declined file, so four broken overrides produce four findings. The most valuable
case it covers is the quiet one: an override that parses cleanly but has dropped
a clause the classifier needs in order to refuse. That file would otherwise
install silently and settle live panes, so the loader rejects it and this warning
is the only thing that says why — see
[Required clauses](watchdog.md#required-clauses) for which clauses are mandatory
and which an older override may safely omit.
`screenstate_persistent_decline` is the opposite: a window is stuck right now
and no other signal in the system says so, which is precisely what `--strict`
exists to catch. It exits 2.

The two are also independent. A persistent decline with every set reporting
`embedded` means the *shipped* patterns no longer match the harness — an upgrade
of the harness, not a mistake by the operator — and the override is the stopgap
while the shipped file is corrected.

Trusting the failure finding rests on the conjunction behind it: a pane doing
real work repaints its elapsed-time counter, the screen fingerprint changes, and
the streak restarts at one. Legitimate activity cannot reach the threshold at
any duration. The finding carries the probe source, the streak, the veto set,
and the status line the classifier saw; the last two are the diagnosis. Tombstoned
windows are exempt, the streak clears on a real settle and on window closure,
and it deliberately survives a reducer-driven idle-evidence reset, which proves
nothing about whether the classifier works.

The finding is a claim about the window *right now*, so it retracts as soon as
that claim stops holding. The probe only classifies a ⚡ window, so anything
that moves the icon — a late native stop, a terminal error, a blocking prompt,
`/clear` or F8 — ends the streak the moment the new state is published, and
doctor independently withholds the finding for a window that is no longer a live
⚡ so that a snapshot captured just before the retraction, or emitted by an older
daemon, cannot fail `--strict` for a pane that already recovered. Historical
decline evidence in the per-window event ring is unaffected either way.

Both findings reach `amx debug bundle` with no extra step, because the bundle
re-runs doctor and embeds `doctor.json`.

## Drilling the decline signal

No automated test can prove the signal fires against a real harness, so this is
the operator drill. Run it on a scratch session, never on live work.

1. Copy `internal/screenstate/patterns_claude.toml` to
   `~/.config/agent-mux/screenstate/claude.toml` and replace `composer_line`
   with a valid regex that cannot match, e.g.
   `'^AMX_NEVER_MATCHES_\x{0000}$'`. Run `amx daemon restart` and confirm
   `amx doctor` reports `claude: override (…)`.
2. Launch one scratch Claude session, send it a prompt long enough to still be
   running after a few seconds, then press Esc to interrupt it. Claude fires no
   `Stop` hook after a user interruption, so the tab is left holding ⚡ with a
   quiet screen — which is the exact stuck-bolt case the probe exists to
   recover, and the only way to hold ⚡ long enough to reach the threshold.
   Clear the composer so the pane would genuinely settle under the shipped
   patterns.
3. Wait about two minutes, then read
   `amx daemon export-events --window <id>`. The window's
   `watchdog.screen_declines` entry should show a streak past 60,
   `stable_fingerprint: true`, and `vetoes: "composer_busy"`.
4. `amx doctor --strict` now raises `screenstate_persistent_decline` naming that
   window, and exits 2.
5. Delete the override, `amx daemon restart`, and the pane settles to 🛑 on the
   next probe tick with both findings gone. Do this even if the drill failed
   earlier: a broken override left installed degrades every Claude pane.

**Break `composer_line`, not `working_status_line`.** Breaking the working
status line produces the opposite failure and cannot raise this finding.
`working_status_line` is the only source of `VetoLiveStatusLine`, so an
unmatchable one leaves a live turn on a framed empty composer with no veto at
all: the classifier reads it as quiescent and *settles a running turn* — a false
🛑, not a held ⚡. And where some other veto does fire during a live turn, the
decline is recorded against an unstable fingerprint, because the screen
fingerprint hashes the elapsed timer the pane repaints every tick. The finding
requires a stable fingerprint, so a working pane can never raise it at any
duration. That is deliberate: it is precisely what makes a 60-tick threshold
safe rather than merely long. `composer_line` is the clause whose failure
produces a permanently vetoed *quiet* screen, which is the fault this finding
names.

Note that this drill *keeps* `working_status_line` and makes `composer_line`
unmatchable, which is why it still works: the loader's required-clause check
tests that a clause is present and in the right section, not that it matches
anything. Deleting `working_status_line` outright is refused at load and the
override never installs, but an unmatchable one is indistinguishable from copy
the harness stopped emitting — and that is the failure mode this drill and the
decline finding exist to expose.
