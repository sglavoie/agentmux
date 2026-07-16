# Process-Lifecycle Acceptance

AgentMux has a hermetic release fixture for harness processes that exit without
emitting `Stop`, `SessionEnd`, or another native terminal hook:

```bash
go test ./cmd -run '^TestAcceptanceHarnessProcessLifecyclePipeline$' -count=1
```

`cmd.TestAcceptanceHarnessProcessLifecyclePipeline` starts the production daemon
in-process and crosses its real Unix socket. A PATH-local `ps` returns
controllable PID plus opaque `lstart` snapshots, while a fake Kitty executable
records the real `amx_state` and title commands. The fixture makes no model
calls, sends no desktop notifications, performs no Git operations, and does not
contact a live Kitty instance.

The test covers:

- Claude `-p`, Codex `exec`, and Copilot prompt-mode pipe children;
- an interactive missing-process fallback for Claude, Codex, and Copilot;
- an interactive parent surviving a pipe child, pipe-only work, and two
  concurrent pipe children where only one exits;
- preservation of pipe failure and question facts until acknowledgement;
- both missing and PID-replaced process observations;
- native terminal hooks arriving before and during an off-lock inspection;
- reset-owned clear-only settlement and a live ASL lease coexisting with an
  exiting ordinary harness process;
- daemon restart after the last active hook, followed by settlement from the
  exact schema-v4 process fingerprint restored from the recorded Kitty user var;
- a schema-v10 `HarnessProcessExited` event joined to its successful renderer
  generation and a sanitized `daemon.process_lifecycle` journal row.

The sanitized production-hook trace corpus independently carries a complete
pipe-process fingerprint through every-prefix `amx_state` restart equivalence.
Schema compatibility tests retain explicit schema 9 and schema 8 projections;
both must omit schema-v10 process fields.

The release gates remain:

```bash
just acceptance
just lint
git diff --check
```

## Policy under test

Native terminal hooks are authoritative when present. Process observation is a
fail-safe for their absence, and an unknown or failed inspection preserves all
state for a later pass. A confirmed pipe exit withdraws only disposable facts
owned by the exact harness/PID/start-token fingerprint; attention and failure
facts remain visible. A confirmed interactive exit synthesizes a clean stop
only when no native terminal outcome already owns the source, clears that
session's remaining subagents, and never sends a desktop notification. Reset
flows clear without synthesizing stop, and process observation never releases
an ASL lease. Identity-less and legacy facts retain the existing watchdog as
their fallback.

## Optional installed smoke

Automated acceptance does not claim that a live provider or installed Kitty
drill ran. On a Kitty-enabled development machine, install and restart the
candidate artifact, start a scoped diagnostic capture, and run a non-committing
`spec commit` dry-run or an equivalent Claude pipe invocation. Confirm that:

1. `hook.process_detection` records pipe mode and a complete safe fingerprint.
2. With no terminal hook, `daemon.process_lifecycle` and
   `HarnessProcessExited` appear within roughly two seconds of process exit.
3. The event has reason `pipe_process_exited`, acknowledges the pipe fact,
   reveals the parent or clears the icon, links to a successful renderer
   generation, and has `terminal_hook_evidence: "absent"`.
4. No desktop notification is sent.
5. Exiting one interactive session for each installed harness synthesizes a
   silent clean stop only when its native terminal hook is absent.
6. `amx doctor --strict`, `amx debug status`, the lifecycle tail, explanation,
   and schema-v10 export are clean. Retain schema 9 and schema 8 exports proving
   the process fields are excluded.

Use the commands in [Debugging event delivery](debugging.md#tracing-a-harness-process-exit)
to preserve the exact settlement and renderer trace. Do not modify the `spec`
repository or incur provider/model usage merely to satisfy the automated gate.

The production inspector supports macOS and Linux `ps` implementations that
provide PID and five-field C-locale `lstart` output. The automated fixture
models that shared contract; the installed smoke is the evidence for a
particular host, Kitty version, and provider build.
