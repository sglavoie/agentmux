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
