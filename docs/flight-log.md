# Flight Log

Flight Log keeps local session statistics and a collection of milestones.
Open **View → Flight Log…** (⌘⇧F) in the macOS app, or choose **Show Flight Log**
from the terminal dashboard's command palette (`:`). Search the palette for
`stats`, `achievements`, or `heatmap`. The app's shortcut can be assigned in
Settings.

The overview offers Today, Week, and All time, with project, harness, and
profile filters. Weeks start on Monday in the machine's local timezone. The
calendar covers the last year; choose launches, turns finished, or observed
working time for its intensity. In the app, selecting a day opens its recorded
sessions, including days outside the currently selected week.

## Session scorecards

Each launch has its own scorecard, including resumes, forks, and restarts of
the same conversation. A scorecard shows its launch type, state changes,
turns finished, questions, permission requests, observed working and waiting
time, a state breakdown, and a timeline. Gray timeline intervals are unknown.

Select a question or permission counter to inspect matching recorded events.
**Conversation at this event** opens Conversation History at the event's time.
**Open Retained Activity** opens Since Last Looked for that session and event
kind; its diagnostic journal may retain less history than Flight Log.

The terminal view uses `1`/`2`/`3` for Overview, Sessions, and Milestones;
`p` cycles the period, `f` the project, `h` the harness, and `a` the profile.
Use `j`/`k` to move, Enter for a scorecard, `e` to filter its events, `s` to
find its conversation in Past Sessions, `r` to refresh, and Escape to go back.
The CLI provides the same reports:

```sh
amx session flight-log
amx session flight-log --period today
amx session flight-log --period all --project API --harness codex
amx session flight-log --session rs-example --period all --json
```

`amx session stats` is an alias. `--session` accepts an exact Flight Log key,
runtime-session ID, or launch ID. All JSON commands use the standard
`{"schema_version":1,"data":...}` envelope. Reports include period totals,
global lifetime totals, calendar buckets, scorecards, milestones, weekly
observations, settings, and coverage warnings.

## What counts

| Statistic | Definition |
| --- | --- |
| Launches | Successfully launched sessions; failed requests, deduplication, attaching, and reopening the app add nothing |
| Launch types | Fresh, resume, fork, or restart, recorded at the launch boundary |
| Conversations | Distinct harness-qualified conversation identities actually known to the recorder |
| State changes | Changes to a session's effective icon or loop marker; repeated hooks, redraws, unseen markers, and hidden source changes add nothing |
| Turns finished | Entries into the effective “Turn finished” state; loop-only changes do not add another turn |
| Questions / permissions / errors | Entries into the corresponding effective state |
| Working time | Observed time in Working, Subagent working, or Compacting context |
| Waiting for you | Observed time in Question or Permission requested |
| Peak working | Maximum overlap of observed working intervals |
| ASL runs finished | Distinct run IDs with a recorded finished lifecycle event |

Time is counted between observations from the same daemon process, at most
45 seconds apart. The daemon samples its effective state every 30 seconds;
transitions also provide observations. Sleep, daemon restarts, recording
interruptions, and missed observations leave unknown intervals. Nothing is
extrapolated after the last observation. These measure reported agent states,
not CPU use or verified task completion.

Global time sums each session's observed time, including overlapping agents;
it can exceed wall-clock time. Turns and activity counts do not measure the
quality of the work or imply that a task succeeded.

## Milestones and recaps

The collection includes First Flight, Frequent Flyer (100 launches), A
Thousand Signals (1,000 state changes), Picking Up the Thread (25 resumes),
Branching Out (first fork), Across the Fleet (finish a turn with each
configured harness), and Loop Closed (first finished ASL run). Unlock dates
link to the session that earned them. Milestones use the whole history and
remain unlocked when dashboard filters change. Fleet membership is recorded
with launches so later configuration changes preserve an earned unlock.

The app can show a dismissible banner for a milestone unlocked while Flight
Log is open. Opening historical data does not replay celebrations. There are
no required daily streaks, XP penalties, or rewards for errors or approvals.

Weekly recaps describe launches, resumes, turns, the project with the most
permission requests, observed concurrency, and finished ASL runs. They use
the selected project/harness/profile filters and the current calendar week.

## Local history and controls

The private ledger lives at
`$XDG_STATE_HOME/agent-mux/flight-log.jsonl`, falling back to
`~/.local/state/agent-mux/flight-log.jsonl`. It stores compact identities,
labels, timestamps, and state/lifecycle events. It stores no prompts,
transcript text, terminal output, commands, or environment values. Nothing is
sent to a service. Exported labels and identities remain your local data.

Tracking starts with the updated daemon or the first successful launch.
The app and CLI show **Tracking since**; they never invent lifetime history
from the sessions currently running. Collection is independent of whether
diagnostic logging is enabled. Restart the daemon after installing an updated
build to begin recording its state transitions.

**Manage** in the app provides tracking and celebration preferences, export,
partial history import, and reset. CLI equivalents are:

```sh
amx session flight-log settings --enabled=false
amx session flight-log settings --enabled=true --celebrations=false
amx session flight-log export --json > flight-log.json
amx session flight-log import --json
amx session flight-log reset --yes
```

Pausing tracking preserves existing history. Activity during the pause is
not recorded. Reset permanently deletes Flight Log history and milestones,
preserves valid preferences, and starts a new tracking period. The app asks
for confirmation; the CLI requires `--yes`. Other session and conversation
data are unaffected.

Import is explicit and reads only retained journal events from before tracking
started. Imported history is labeled partial, has no reconstructed working
time, and can lack session identities or launch types. Repeated imports
deduplicate recorded event identities. Export includes the complete compact
ledger and settings, including observation spans.

The ledger survives journal retention and daemon restarts. Writes use a
process-shared lock, private file permissions, and atomic replacement for
reset, settings, and compaction. The daemon compacts at startup and daily,
merging repetitive observations while retaining counters, transitions,
milestone sessions, and unknown intervals. Recording is asynchronous on the
hook path; recording failures are logged and leave a gap marker on recovery.
Unreadable records are reported as incomplete history and preserved for
inspection. A damaged ledger is never rebuilt automatically.
