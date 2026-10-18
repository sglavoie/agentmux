# Daily-use friction register

Status: one operator-reported interruption, investigated below. No complete
workday or recurrence rate is inferred from this report, tests, or screenshots.

Copy recurring IDs from [the daily log](trial-log.md). Combine occurrences only
when they describe the same interruption; keep build changes visible.

| ID | Workflow and interruption | Workdays affected / observed | Occurrences | Recovery time | Severity | Evidence | Fix / verification / recurrence after fix |
| --- | --- | --- | --- | --- | --- | --- | --- |
| F-001 | Codex completes but the indicator eventually becomes blank; operator misses completion (`tt`) | Unknown | One named example; broader recurrence reported, not counted | Unknown | Completion visibility | [Observation](trial-log.md#reported-interruption-2026-10-17) | Managed Codex launches select supported `--no-daemon`; shell execution tests cover compatibility and explicit remote choices. Not yet installed or verified in real use. |

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

| Rank | Friction ID | Evidence and reason | Next action | Outcome |
| --- | --- | --- | --- | --- |

F-001 is the first concrete fix candidate; no comparative frequency ranking is
available from one report. The implemented event navigation,
Runs evidence, status toolbar, filter hints, and launch summaries are candidates
for real-use evaluation, not recorded trial successes.
