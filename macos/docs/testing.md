# Tests, logs, and shared conversations

[Back to the macOS development guide](../README.md)

## Single instance and tests

A second copy of the app exits at launch if one is already running, but first
asks the running one to come forward (a distributed notification,
`SingleInstance.reopenRequest`, answered as a reopen is: the workspace
opens) and activates it, so a double launch is never silently nothing. The tests
run hosted inside the app. When the app sees XCTest in its environment, it
skips the single-instance check and the status item, and it never spawns
`amx`. This means `just mac-test` works while the installed app is running.

`WorkspaceWorkflowPreviewTests` captures fixture windows in light and dark
appearances using ScreenCaptureKit's current-process window API (macOS 14.4 or
newer). It verifies the requested window dimensions and recognizes visible
labels with Vision, so a blank or unexpectedly enlarged window fails. Captures
cover History, Changes, Activity, and Runs in populated, empty, loading, and
failed states, plus search highlights, diff navigation, Waiting, and the command
palette. Fixtures use injected services and never read real conversations or
operate a real run. PNGs are attached to the XCTest result and written to
`/tmp/agentmux-polish-previews`; older macOS versions skip only these capture
tests. The XCTest result contains the reviewed reference images.

`WorkspaceSessionContextTests` checks the session header's fixed height, split-pane
identity, stale question actions, and offline wording. It writes light/dark/narrow
workspace previews and `responsiveness.json` to `/tmp/agentmux-context-previews`.
The timing fixture warms 30 synthetic terminal views and samples 100 session
switches and palette openings, including layout. `WorkspaceStreamTests` also
writes `ghostty-responsiveness.json` for 100 switches among four real Ghostty
surfaces running isolated shell stand-ins. Both report median, p95, and maximum;
these are navigation measurements, not agent startup or GPU presentation times.
Run `just mac-test Release` for timings from optimized code, and avoid concurrent
builds when collecting comparison measurements. Timings are observations rather
than machine-dependent pass/fail thresholds.

For a live trace, select Agent Mux in Instruments and record Points of Interest
from `io.github.sglavoie.agentmux`, category `Responsiveness`. `Session switch` and
`Command palette` measure controller work through focus assignment, excluding
deferred layout and GPU presentation. `History search` covers background work and
result application; its 120 ms typing debounce is additional. `Recap read` covers
background work and result application, including generation for surfaces that
request it. The context strip requests only saved recaps. Interval metadata never
includes queries, paths, session IDs, or conversation text.

### Native agent handoff

`WorkspaceHandoffModel` loads all configured profiles and a read-only
`session handoff --preview` snapshot. `WorkspaceHandoffPanel` presents editable
context, the source checkout, an optional instruction, and explicit launch.
It submits private temporary context through `--content-file`, removes the
file after the CLI returns, and opens the reported receiving runtime session.
Unconfirmed outcomes retain the preview and disable repeated launch.

Session menu, sidebar context menu, command palette, History and Project
Briefing share this panel. `WorkspaceHandoffOrigins` persists source links by
runtime identity and, once observed, by harness-qualified conversation identity.
The Session inspector and History expose the source conversation. Generated
handoff packs remain durable user assets owned by the Go handoff workflow.

`WorkspaceHandoffTests` cover preview identity, exact reviewed text, private
file permissions and cleanup, launch receipts, stale reads and saved links.
Workflow previews exercise light/dark, narrow/wide and unavailable states.

### Project Briefing

View › Project Briefing… and the command palette open `WorkspaceBriefingWindow`.
`WorkspaceBriefingModel` joins the companion snapshot with `session list --all
--project <title> --limit 10`; project membership follows the same CLI filter as
History, across profiles and harnesses. All live conversation identities remain
included even outside the latest ten. The model deduplicates on harness plus
conversation ID, and late replies cannot replace a newly selected project.

Each card owns a `WorkspaceRecapModel` with `automaticallyGenerate: false`.
Opening, refreshing, missing/stale results, and pending-request polls only read
saved recaps. Generation is an explicit per-conversation action, gated on a live
feed and no working instance of that conversation. Focus and resume reuse the
existing exact-identity actions; source links open History at the cited message.
The View menu shortcut is assignable and has no default. Closing cancels pending
UI work, while a model request already started may finish into the shared cache.

`WorkspaceBriefingTests` covers scope, the history bound, live/history merging,
cross-harness identity collisions, stale responses, cached-only reads, generation
eligibility, retained failures, and focus/resume. `WorkspaceWorkflowPreviewTests`
captures populated light/dark, empty, and offline views at narrow and wide sizes.

### Running the UI on another Mac

The hosted tests launch the app, so they show windows on the Mac that runs
them. To keep that off the machine you are working at:

```bash
AMX_MAC_TEST_HOST=user@other-mac just mac-test
```

`scripts/remote-ui.sh` copies this checkout to a directory of its own under
that user's `Library/Caches/agent-mux-remote-ui/` on the other Mac, runs the
same `test-app.sh` there, and reports its result and which Mac ran it. It
refuses a host that cannot be reached or that turns out to be this Mac. With
`AMX_UI_TEST_REMOTE_ONLY=1` the suite never runs locally: no host, or a host
that fails, is an error rather than a fallback. SSH takes its credentials from
your agent or askpass helper and is given no terminal. The other Mac needs
Xcode; GhosttyKit and XcodeGen travel with the copy.

## Logs

The app logs to the unified log under the subsystem
`io.github.sglavoie.agentmux`:

```sh
log show --last 5m --predicate 'subsystem == "io.github.sglavoie.agentmux"'
```

### Recorded activity

View › Since Last Looked uses `amx session activity --json --days N --limit 1000`.
The command projects actual daemon journal transitions, settled process exits,
and accepted ASL lifecycle records; unchanged, suppressed, and duplicate reducer
records are omitted before limiting. Stable record IDs survive journal rereads.
Read IDs and the last marking time persist atomically in
`~/Library/Application Support/AgentMux/activity-read.json` (0600), without output
or transcript text. IDs expire after 91 days, beyond the maximum 90-day query.
Marking read is explicit: the Mark read menu offers the selected record or all
loaded filtered results, with an exact count. Undo restores the last acknowledgement
and its prior selection; neither action includes records omitted by a truncated query. Late-spooled events remain unread regardless of their timestamps.

Supporting excerpts use the exact harness-qualified conversation and the latest
assistant message timestamp at or before the recorded event. Missing timestamps
or transcripts are explicit, and earlier messages can be loaded. Stale requests
cannot replace a newer selection. Event navigation rechecks the live session
feed and never uses a recycled terminal window ID to identify an agent.

## Shared conversation threads

The app reads Go-owned handoff relationships from `session list/show --json`
and `session threads --json`. History and the inspector expose both directions;
Project Briefing groups connected conversations and reads cached recaps for
older indexed members returned as `related_conversations`. These reads launch
no agents or model requests. `WorkspaceThreadsModel` discards replies after a
selection change or close. Source links in old `workspace.handoffOrigins.v1`
preferences remain readable for compatibility; new handoffs are recorded by
the Go workflow instead of app preferences. See [usage](../../docs/usage.md) for
storage and CLI details.
