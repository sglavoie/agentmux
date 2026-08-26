# Show available recipes by default
[private]
default:
    @just --list

prefix_bin := home_directory() / ".local/bin"
prefix_kitty := home_directory() / ".config/kitty"
prefix_config := home_directory() / ".config/agent-mux"
root := justfile_directory()

# Build the amx binary
build:
    go build -o amx .

# Run all tests
test:
    go test ./...

# Run go vet
vet:
    go vet ./...

# Run the concurrency-sensitive filesystem persistence, continuity, daemon,
# and TUI suites under the race detector.
race-core:
    go test -race ./internal/transcript ./internal/plans ./internal/handoff ./internal/worktree ./internal/daemon ./tui

# Run every committed narrow/wide TUI acceptance scenario in isolation.
tui-scenarios:
    go run . tui-test run docs/tui-testing/scenarios/*.json

# Exercise the installed agent-session-loop bridge end to end against a
# throwaway plan and state root. Every other recipe stays hermetic and needs no
# ASL; this one is opt-in because it needs a real `asl` on PATH. When it is
# asked for, an ASL that is missing or speaks a different protocol fails here
# rather than skipping: a silent skip would report a compatible pair that was
# never actually checked.
asl-integration:
    AMX_ASL_INTEGRATION=1 go test -count=1 -run TestInstalledBridge ./internal/aslbridge

# Daemon lifecycle scenarios whose determinism this repository stresses:
# startup publication, owned-loop shutdown drain, and the concurrent handler
# state paths. Extend this pattern as new fault scenarios land.
stress_scenarios := "TestRun_|TestServer_|TestHandler_Concurrent"

# Reliability stress gate. Repeats the daemon lifecycle scenarios in isolation,
# then again as concurrent shards while the full suite loads the machine, then
# measures the latency budgets on a quiet machine.
#
# The correctness scenarios synchronize on signals the daemon publishes, so
# contention must not change their outcome; the latency budgets are measured
# separately and deliberately, because contention legitimately changes those.
reliability-stress reps="20" shards="4":
    #!/usr/bin/env bash
    set -euo pipefail

    log_dir="$(mktemp -d)"
    trap 'rm -rf "$log_dir"' EXIT

    echo "== isolated repetitions (x{{ reps }}) =="
    go test -count={{ reps }} -run '{{ stress_scenarios }}' ./internal/daemon

    echo "== isolated repetitions under -race (x{{ reps }}) =="
    go test -race -count={{ reps }} -run '{{ stress_scenarios }}' ./internal/daemon

    # The full suite runs here to saturate the machine, and this gate judges only
    # the daemon scenarios racing against it. `just test` is the suite's own gate
    # and runs it unloaded. Its outcome is still reported rather than swallowed: a
    # regression that only the loaded run reveals should be visible here even
    # though it does not fail this recipe.
    echo "== {{ shards }} concurrent shards (x{{ reps }}) under full-suite load =="
    go test -count=1 ./... > "$log_dir/suite.log" 2>&1 &
    suite_pid=$!
    shard_pids=()
    for shard in $(seq 1 {{ shards }}); do
        go test -count={{ reps }} -run '{{ stress_scenarios }}' ./internal/daemon \
            > "$log_dir/shard-$shard.log" 2>&1 &
        shard_pids+=("$!")
    done
    failed=0
    for pid in "${shard_pids[@]}"; do
        wait "$pid" || failed=1
    done
    suite_status=0
    wait "$suite_pid" || suite_status=$?
    if [ "$failed" -ne 0 ]; then
        for log in "$log_dir"/*.log; do
            echo "--- $log"
            cat "$log"
        done
        exit 1
    fi
    if [ "$suite_status" -ne 0 ]; then
        echo "-- background load suite exited $suite_status (reported, not gated):"
        tail -n 40 "$log_dir/suite.log"
        echo "-- gate on it with: just test"
    fi

    # Budgets last and alone: everything above has finished, so the machine is
    # quiet and a measured overrun means the daemon got slower, not that the
    # stress run was still competing with itself.
    echo "== latency budgets (quiet machine) =="
    AMX_PERF_TESTS=1 go test -count=1 -run 'TestPerformance_' ./internal/daemon

    echo "reliability-stress: ok"

# Product acceptance without the optional linter dependency.
acceptance: test race-core vet tui-scenarios

# Run linter
lint:
    golangci-lint run

# Run the full repository gate.
check: acceptance lint

# Install binary, Kitty config, and a starter app config if missing.
# The binary is staged beside the destination and renamed into place so the
# replacement is atomic: no window where amx is absent or half-copied, and a
# running daemon keeps its already-open executable.
install: build
    @mkdir -p "{{prefix_bin}}"
    cp amx "{{prefix_bin}}/.amx.staging"
    mv -f "{{prefix_bin}}/.amx.staging" "{{prefix_bin}}/amx"
    @mkdir -p "{{prefix_kitty}}"
    ln -sf "{{root}}/agent-mux.conf" "{{prefix_kitty}}/agent-mux.conf"
    @if [ ! -f "{{prefix_config}}/config.toml" ]; then "{{prefix_bin}}/amx" config init >/dev/null; echo "Seeded {{prefix_config}}/config.toml"; fi
    @echo "Installed {{prefix_bin}}/amx"
    @echo "Symlinked {{prefix_kitty}}/agent-mux.conf"

# Build, install atomically, restart the daemon, wait for it to report ready,
# then gate on strict doctor (which verifies the CLI and daemon are the same
# artifact by hash/revision and that no invariant is violated).
install-restart-smoke: install
    "{{prefix_bin}}/amx" daemon restart
    @for i in $(seq 1 50); do \
        if "{{prefix_bin}}/amx" daemon status | grep -q '^ready: true'; then break; fi; \
        if [ "$i" = "50" ]; then echo "daemon never became ready" >&2; "{{prefix_bin}}/amx" daemon status >&2; exit 1; fi; \
        sleep 0.2; \
    done
    "{{prefix_bin}}/amx" daemon status
    "{{prefix_bin}}/amx" doctor --strict
    @echo "install-restart-smoke: ok"

# Remove binary and config
uninstall:
    rm -f "{{prefix_bin}}/amx"
    rm -f "{{prefix_kitty}}/agent-mux.conf"
    @echo "Removed {{prefix_bin}}/amx"
    @echo "Removed {{prefix_kitty}}/agent-mux.conf"

# Bump version in cmd/root.go (e.g. just bump 0.4.0)
bump version:
    @sed -i '' 's/const version = ".*"/const version = "{{version}}"/' cmd/root.go
    @grep 'const version' cmd/root.go
