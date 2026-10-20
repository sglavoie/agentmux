# Daily-use friction register

Status: one operator-reported interruption, investigated below. No complete
workday or recurrence rate is inferred from this report, tests, or screenshots.

Copy recurring IDs from [the daily log](trial-log.md). Combine occurrences only
when they describe the same interruption; keep build changes visible.

| ID | Workflow and interruption | Workdays affected / observed | Occurrences | Recovery time | Severity | Evidence | Fix / verification / recurrence after fix |
| --- | --- | --- | --- | --- | --- | --- | --- |
| F-001 | Codex completes but the indicator eventually becomes blank; operator misses completion (`tt`) | Unknown | One named example; broader recurrence reported, not counted | Unknown | Completion visibility | [Observation](trial-log.md#reported-interruption-2026-10-17) | Managed Codex launches select supported `--no-daemon`; shell execution tests cover compatibility and explicit remote choices. Additional [lifecycle coverage](../../desktop-improvements.md#status-review-and-refresh-polish-on-2026-10-18) verifies new/resumed conversations, simulated observation gaps, and checkpoint restoration. Resolved on 2026-10-18: operator confirmed resume, /new reset and recompletion, app reopening, and physical sleep/wake on the installed fix. [Build and confirmation](../../workspace-consistency/observations.json). |

## Ranking at the end of each workday

1. Process loss, wrong-session routing, unrecoverable sessions, and blocked input
   remain trial blockers regardless of frequency. Follow the trial's reset rules.
2. Rank other interruptions by affected-workday frequency, then total recovery
   effort. Keep estimated time labeled; unknown effort stays unknown.
3. Prefer a recurring lost-context or repeated-navigation problem over an isolated
   cosmetic preference. Record the reason when severity overrides frequency.
4. Link each chosen fix to its exact log occurrences, implementation commit,
   affected checks, installed build, and subsequent real-use observations.
   Passing fixtures alone do not prove a daily-use interruption has stopped.

## Next polish decisions

The [workspace seams follow-up](../../workspace-seams-polish/README.md) adds
specific observation tasks for Waiting recovery, applied settings, status
evidence, terminal-to-History search, and large-diff responsiveness. VoiceOver
acceptance was waived by the user. The real-work tasks remain candidate checks
until the operator records actual use; they do not add occurrences or completed workdays to this register.

| Rank | Friction ID | Evidence and reason | Next action | Outcome |
| --- | --- | --- | --- | --- |

F-001 is the first operator-confirmed fix; no comparative frequency ranking is
available from one report. The implemented event navigation,
Runs evidence, status toolbar, filter hints, and launch summaries are candidates
for real-use evaluation, not recorded trial successes.
