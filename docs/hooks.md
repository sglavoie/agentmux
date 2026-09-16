# Hooks

Sync hooks for every configured profile:

```bash
amx config hooks-sync
amx config hooks-status
```

Managed hook files embed the absolute `amx` path returned by the binary that ran
`hooks-sync`. Re-run `amx config hooks-sync` when that path changes (for example,
when switching from a repo-local binary to `~/.local/bin/amx`). If you rebuilt
or reinstalled the same binary path and only changed Go logic, hook sync is not
needed.

Hook installation is harness-aware:

- **Claude**: merges Agent Mux hook commands into that profile's
  `settings.json`
- **Copilot**: writes `$COPILOT_HOME/hooks/agent-mux.json`
- **Codex**: writes `$CODEX_HOME/hooks.json`

The hook command is
`amx hook-indicator <event> --harness=<claude|codex|copilot>`. Generated hooks
always include `--harness` so the daemon knows which harness produced the
event, even when the event is routed to a tab owned by another harness. The
flag remains optional for legacy or manual invocations; an omitted or unknown
producer stays explicitly unidentified instead of being guessed as Claude.
`amx config hooks-status` detects older unstamped generated hooks; run
`amx config hooks-sync` to resynchronize them.

`agent-session-loop` / `asl` uses that same hook endpoint when `amx` is on
`PATH`. It emits `AgentSessionLoopStarted`, `AgentSessionLoopTurnStarted`,
`AgentSessionLoopTurnHeartbeat`, `AgentSessionLoopTurnHangSuspected`,
`AgentSessionLoopTurnHangCleared`, `AgentSessionLoopTurnFinished`,
`AgentSessionLoopVerificationStarted`, `AgentSessionLoopVerificationHeartbeat`,
`AgentSessionLoopVerificationFinished`, `AgentSessionLoopWaitStarted`,
`AgentSessionLoopWaitEnded`, `AgentSessionLoopMailboxQuestion`,
`AgentSessionLoopMailboxReply`, `AgentSessionLoopMailboxResolution`,
`AgentSessionLoopPaused`, and
`AgentSessionLoopFinished` with JSON that includes `source`, `reason`, `run_id`,
`plan_dir`, `cwd`, `state_path`, and `harness`. Wait events additionally carry
`question_id`, `question_preview`, and `wait_reason`. Hang events additionally
carry the bounded scalars `hang_reason` (`result_received_process_alive` or
`zero_cpu_idle`), `hang_idle_seconds`, and `harness_pid`; unknown values are
omitted from the payload rather than sent as null. Both hang events stay in
the turn phase — a suspicion episode is still an executing turn — so they
re-assert the active loop lease like a heartbeat instead of releasing it, and
the tab keeps rendering ⚡🔁. Lifecycle events carry the run's `run_id` and a
persisted monotonic source sequence; the mailbox-reply marker uses the physical
mailbox reply sequence. The daemon keys one lease per run: overlapping runs in
one workspace hold independent facts, a run's turn/verification phases render
⚡🔁, a worker blocked on a supervisor mailbox reply and a between-turns run
render bare 🔁, paused states release 🔁 and retain their human-attention icon, and one run
finishing never changes another run's state (verified live with two overlapping
runs in a single window). Turn and verification heartbeats re-assert the lease
every 30 seconds. A worker wait has no stale threshold: it keeps 🔁 until the
run answers, pauses, or finishes. When the explicit `asl outbox wait` command
surfaces a still-open set, ASL sends a body-free
`AgentSessionLoopMailboxQuestion` handoff with the run, latest question
identity/timestamp, open count, and physical plus projection sequences. Agent
Mux validates and routes that handoff to the managed title owner immediately,
showing ❓🔁 and sending “User input requested” without waiting for a harness
`Stop`. A duplicate handoff is idempotent; F8 dismisses the current set until a
different latest question arrives, and that new question is allowed to alert
even inside the ordinary notification cooldown. The question icon ties
permission priority, remains below errors, and outranks warnings and activity.

ASL supervisor lease schema v2 remains the fixed-size, text-free compatibility
path (`run_id`, mailbox projection sequence, count, latest question ID, and
timestamp). Agent Mux refreshes registered live runs on its two-second
maintenance cycle. For an older producer or a lost push, it raises ❓🔁 only
when the lease has an unanswered question and the newest interactive supervisor
state is a trusted identified harness `Stop` (or daemon-authoritative
screen/transcript stop) no older than the question. Anonymous legacy and
pipe-worker terminal events cannot perform that fallback handoff. The probe
survives ordinary heartbeats and transient lease failures and persists across
daemon restarts. V1, stale, malformed, and foreign-run evidence cannot raise it.

After ASL's post-append snapshot proves no open questions remain for the run,
either the text-free `AgentSessionLoopMailboxReply` or
`AgentSessionLoopMailboxResolution` marker retracts the aggregate probe
immediately. Sequence cutoffs prevent an in-flight stale projection from
restoring it while preserving any genuinely newer question; Kitty and the
dashboard return to the underlying live loop state. A fresh zero count or run
completion remains the compatibility fallback. No question, reply, resolution
note, or notice body crosses this notification path.
When the final run finishes, that terminal boundary also retires an older
interactive `bolt`/`compact` fact, so a lost Codex/Claude `Stop` or Copilot
`agentStop` cannot leave `⚡` masking the run's `🛑`; newer activity, pipe-mode
children, subagents, and attention/error states remain protected.
`ASL_AGENT_MUX_NOTIFY=off`
disables the emission entirely; the ASL test suite forces it off so nested
test runs never leak events into a live daemon (also verified live). Start payloads use the run's
top-level harness; turn-start payloads use `active_turn.harness`; finish
payloads use the last completed turn harness when present. A mid-run harness
switch moves and clears the loop marker on the matching project tab. Agent Mux
also reads `state_path` when a lifecycle payload needs to recover `cwd` or
harness, using `active_turn.harness` before top-level `harness`.

Copilot and Codex hook files are user-level for a given harness home, so any CLI
surface that reuses that home — including VS Code or another non-Kitty session
— will run the same hook commands. Agent Mux treats those non-Kitty invocations
as silent no-ops; only Kitty-backed sessions update tab state.

Copilot hook entries use the documented `{"bash": ..., "powershell": ...}`
schema. If you installed hooks with an older Agent Mux build that wrote a
`command` field instead, re-run `amx config hooks-sync` once; the drift
detector backs up the old file as `agent-mux.json.bak`.

Codex hook entries use Codex's grouped `{"hooks": { ... }}` JSON shape. Agent
Mux preserves non-Agent-Mux hooks already present in `hooks.json`, replaces only
its prior hook entries, writes synchronous command hooks (no `async` field), and
sets `timeout: 5` so `hook-indicator` does not inherit Codex's long default
hook timeout.

Current Codex events mapped by Agent Mux include:

- `SessionStart`
- `UserPromptSubmit`
- `PreToolUse`
- `PermissionRequest`
- `PostToolUse`
- `PreCompact`
- `PostCompact`
- `SubagentStart`
- `SubagentStop`
- `Stop`

Codex's `PostCompact` hook is classified from its `trigger` payload. Automatic
mid-turn compaction restores the active bolt (⚡) because Codex continues the
same turn; manual `/compact` lands on the stop icon (🛑). Missing or unknown
trigger values keep the conservative terminal behavior for compatibility.

Current Copilot events mapped by Agent Mux include:

- `sessionStart`
- `sessionEnd`
- `userPromptSubmitted`
- `preToolUse`
- `postToolUse`
- `postToolUseFailure`
- `permissionRequest`
- `agentStop`
- `subagentStart`
- `subagentStop`
- `errorOccurred`
- `preCompact`
- `notification`

`agentStop` is the terminal Copilot turn-end hook — the direct analogue of
Claude's `Stop` event — and resolves to the stop action (🛑 icon).
`notification` payloads with `notification_type = agent_idle` are treated as
**background-agent** notifications for Copilot and are ignored for main-session
state: they no longer resolve to the stop action, so a background agent going
idle can never falsely stop the interactive turn (the tab keeps its current
`⚡`). Subagent lifecycle hooks (`subagentStart`/`subagentStop`) remain
responsible for the 🤖 subagent icon. This is harness-specific: Claude
`agent_idle` still resolves to stop. Copilot `permissionRequest` fires earlier,
before the
permission service decides whether a tool call is auto-allowed, denied by rules,
or escalated to a real user prompt, so Agent Mux treats that hook as
pre-permission activity and preserves the current icon until the outcome is
clearer. `notification_type = permission_prompt` and
`notification_type = elicitation_dialog` are the explicit user-blocking signals
that resolve to different sticky icons: `permission_prompt` uses the
permission-needed key icon (🔑), while `elicitation_dialog` uses the question
icon (❓) for plan approvals and similar user-input prompts. Because the managed
Kitty snippet disables tab bell markers and audible bells globally, these icons
become the visible attention signal instead of Kitty's bell state. If Copilot
has already continued the turn after a speculative `permissionRequest`, a later
trailing `notification.permission_prompt` is treated as stale noise and does
not replace the current icon.
`sessionEnd` remains the session-level terminal hook. Copilot still does not
document a `postCompact` hook; if a compaction run ends with only an
idle/cleanup follow-up after `preCompact`, Agent Mux promotes the compact state
to 🛑 instead of silently clearing the tab icon.

Claude hook support remains in place alongside Copilot; `Stop` and
`SessionEnd` are terminal. `TeammateIdle` is the lone non-terminal
child-complete signal that preserves the parent session's current icon.

Agent Mux classifies reset flows from the harness lifecycle it actually sees at
runtime instead of from prompt-only heuristics. Claude `/clear` is detected from
`SessionEnd.reason = clear` and the follow-up `SessionStart.source = clear`.
Copilot reset commands are still detected from `userPromptSubmitted.prompt`
when the first token is `/clear`, `/reset`, or `/new`, plus the documented
`sessionStart.source = new` lifecycle hint, but interactive Copilot sessions can
also emit `/clear` first as `sessionEnd.reason = user_exit` and only start the
next session on the next prompt. Agent Mux treats that user-driven session end
as reset cleanup too. The daemon defers reset-sensitive terminal stops so late
reset markers can win, then rewrites the
follow-up stop/idle cleanup path to `user_clear` so reset flows clear the tab
without surfacing a stop icon or stop notification. If runtime behavior still
diverges, inspect `amx daemon export-events --current-window --last 50` from
the live tab before changing the delay heuristic.

If the managed hook binary is newer than the running daemon, the next hook event
from that binary automatically restarts the daemon so new indicator/daemon logic
takes effect without a manual restart. Use `amx daemon restart` when you want to
force that refresh immediately, or when debugging a hook path mismatch.
