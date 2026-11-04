# Product acceptance

The local product gate is:

```bash
just check
```

It checks Go formatting, runs the Go suite with the TUI scenario harness,
exercises the race-sensitive packages, runs `go vet`, and lints both the release
and scenario builds. `just acceptance` runs the same gate without the optional
linter. `just test` is the shorter Go and TUI scenario gate. The scenarios use
isolated homes and temporary state; see [Build and test](testing.md) for the
individual recipes and CI lanes.

The macOS app has its own suite:

```bash
just mac-test
```

The paired release gate also requires installed `amx`, `asl`, and tmux. It
installs the pair, restarts the daemon, exercises their integration, and writes
an evidence record. Run it only as part of a release; see
[Paired releases](paired-releases.md).

## Runtime coverage

| Area | Automated coverage |
| --- | --- |
| Session lifecycle | `internal/session`, `cmd/session`, and `internal/runtimesession` cover launch, focus, close, ownership, and registry persistence for tmux sessions. |
| Terminal backend | `internal/tmux` exercises a private server, pane capture, session switching, status rendering, popups, and function keys. |
| Daemon state | `internal/daemon` covers hook routing by runtime session ID, reducer state, process exit, stop confirmation, stale observations, notification suppression, and checkpoint restore. |
| Screen probes | `internal/screenstate` classifies tmux captures for Claude, Codex, and Copilot. Daemon probe tests check pane and server generation before applying observations. |
| TUI | `tui`, `tui/overview`, and `cmd/tuitest` exercise dashboard and overview behavior, including narrow and wide scenario fixtures under `docs/tui-testing/scenarios`. |
| Native app | `macos/AgentMuxTests` covers the embedded Ghostty surface, workspace navigation, app menus, notifications, and session recovery. Run with `just mac-test`. |
| ASL integration | `internal/agentloop`, `internal/aslbridge`, and daemon integration tests cover the vendored contract and runtime session claims. `just asl-integration` checks an installed ASL against a private tmux server. |

## Diagnostic regression acceptance

`status`, `tail`, `explain`, and `bundle` share immutable journal snapshots.
Tests in `internal/diagnostics`, `internal/debugger`, `internal/indicator`,
`internal/hooks`, and `cmd/debug` cover event-time ordering, bounded causal
traversal, lossless compaction, source isolation for `json_compact` hook
payloads, and a strict exit 2 for oversized payloads. The 2,000-to-1 segment
fixture measures scan behavior; its timings are informational.

## Session actions across surfaces

The dashboard and overview use the shared action catalog in
`tui/sessionactions`. Tests in that package and the two surfaces cover action
availability, confirmation, launch, focus, close, restart, and undo. Managed
sessions are addressed by runtime session ID; a hosted Ghostty workspace may
handle a focus request directly, while a plain terminal switches its tmux
client.

## Live session overview

The overview previews registered tmux panes. Its capture scheduler bounds
concurrent reads and rejects stale replies. `tui/overview` tests keyboard and
mouse navigation, resizing, paging, action menus, and disappearing sessions.
The scenario suite covers rendered layouts; a live terminal check uses
`amx overview` from a session on the private tmux server.

## Installed and live evidence

Hermetic tests establish behavior at process boundaries. These checks cover
installed components and a real terminal:

```bash
just cockpit-smoke
just asl-integration
just crash-restart
just reliability-stress
```

`cockpit-smoke` checks the installed daemon and a private tmux round trip.
`asl-integration` requires an installed ASL. `crash-restart` and
`reliability-stress` use private tmux servers. None is part of `just check`.
The app's installed build is checked through `just mac-release-gate`.

The current commands above are the acceptance procedure for this checkout.
