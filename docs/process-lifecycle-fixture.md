# Process lifecycle acceptance

The daemon observes a harness process when its terminal hook is missing. Its
process identity includes harness, PID, and an opaque start token, so a reused
PID cannot settle a newer session. The inspector runs off the state lock and
an unknown inspection preserves the current state for a later pass.

Run the package tests with:

```bash
go test ./internal/daemon -run '^TestProcessLifecycle' -count=1
```

`process_lifecycle_test.go` covers live, missing, replaced, and unknown
processes; parent and pipe child isolation; native terminal hooks racing an
inspection; reset and ASL exclusions; notification suppression; and the
settlement's diagnostic event. Checkpoint and export tests in the same package
cover persistence and schema compatibility.

For the full gate, run `just acceptance`. Installed behavior is checked by
`just cockpit-smoke` and the private tmux scenarios in `just crash-restart`.
Those checks do not require a model provider. A provider-specific live check
may confirm the actual hook delivery and process exit path on the installed
build; preserve its daemon event export and diagnostic journal when reporting
that result.
