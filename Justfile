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

# Fail when any TRACKED Go file is not gofmt-clean.
#
# gofmt exits 0 whether or not it found anything, so the check is on its
# output: any listed path is a file the formatter would rewrite. The paths are
# printed before failing, because "run gofmt -w" is only actionable if the
# operator knows on what.
#
# The file list comes from git rather than from a directory walk. An operator's
# checkout carries agent worktrees and other untracked Go sources under
# .claude/, and a gate that failed on a stale copy of a file nobody is editing
# would be a gate people learn to ignore.
fmt-check:
    #!/usr/bin/env bash
    set -euo pipefail
    unformatted="$(git ls-files -z '*.go' | xargs -0 gofmt -l)"
    if [ -n "$unformatted" ]; then
        echo "gofmt would rewrite these files; run 'gofmt -w' on them:" >&2
        echo "$unformatted" >&2
        exit 1
    fi

# Run the concurrency-sensitive filesystem persistence, continuity, Kitty
# read-coordination, daemon, and TUI suites under the race detector.
#
# `./tui` does not include its subpackages. `./tui/commands` owns the live
# topology reads and the package-level seams tests swap, and `./tui/overview`
# runs its own Bubble Tea program with reads in flight, so both are named here
# rather than inherited. `./tui/sessionactions` is named for a different
# reason: it is one declaration read by two surfaces, and it owes each caller an
# independent value, so a later attempt to hand out a cached slice or map has to
# fail here rather than in whichever goroutine happened to write to it.
# `./internal/daemon/ipc` is likewise not inherited from `./internal/daemon`:
# it owns the connection goroutines, their deadlines, and the ingress queue.
race-core:
    go test -race ./internal/transcript ./internal/plans ./internal/handoff ./internal/worktree ./internal/kitty ./internal/daemon ./internal/daemon/ipc ./tui ./tui/commands ./tui/overview ./tui/sessionactions

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

# Compare the vendored compatibility corpus against the canonical one in an ASL
# checkout. `just test` already proves the vendored bytes and the pinned
# ContractSetID agree with each other; what it cannot see is whether that
# agreed-upon copy is still the one ASL publishes. This recipe closes that gap
# offline, without needing an installed `asl` the way `asl-integration` does.
#
# The byte comparison is the whole check, and it is enough: ContractSetID is
# derived from the vendored bytes by contract_test.go, so identical bytes imply
# an identical ID. manifest.json is compared too -- it is excluded from the
# digest, so a manifest that drifted while the schemas did not is invisible to
# every digest check and visible only here.
#
# `just` passes everything after the recipe name positionally, so a caller who
# writes the documented `asl_root=/path/to/scripts` form hands the recipe that
# whole string. The prefix is stripped here rather than refused, so the named
# and bare forms both work and neither silently checks the wrong directory.
asl-contract-sync asl_root:
    #!/usr/bin/env bash
    set -euo pipefail

    asl_root_arg="{{ asl_root }}"
    asl_root="${asl_root_arg#asl_root=}"
    canonical="$asl_root/tests/fixtures/agent_mux_contract"
    vendored="{{ root }}/internal/aslcontract/corpus"

    if [ ! -d "$canonical" ]; then
        echo "no ASL corpus at $canonical" >&2
        echo "pass the ASL checkout: just asl-contract-sync asl_root=/path/to/scripts" >&2
        exit 1
    fi


    # README.md is Agent Mux's own note about why the copy exists; it is not
    # part of the contract and ASL does not publish one.
    if ! diff -ru --exclude=README.md "$canonical" "$vendored"; then
        echo >&2
        echo "vendored corpus has drifted from ASL's canonical corpus" >&2
        echo "re-vendor it, then rerun: just test" >&2
        exit 1
    fi

    echo "asl-contract-sync: vendored corpus matches $canonical"

# Exercise the overseer against the *installed* pair: the amx in ~/.local/bin
# and the asl wrapper on PATH. It drives policy, history, watch, checkpoint and
# resume through ASL's own leaves, and checks the compatibility both ways --
# that the installed binary answers the contracts ASL speaks, and that a
# component which does not is refused rather than read past.
#
# This is the one gate that judges what an operator actually has. `just check`
# judges these sources; `asl-integration` judges the installed ASL against
# *these* sources; `asl-contract-sync` compares two checkouts offline. A version
# skew between the two installed programs is invisible to all three, and it is
# the skew an operator meets, because the two are released separately.
#
# Everything it touches is a throwaway world the suite builds and deletes:
# its own Git repositories, its own state root, its own config.toml reached
# through AMX_CONFIG, its own profile home and transcripts, and fake harness
# processes standing in for the ones a lock names. It never reads the
# operator's configuration and never touches a real plan.
#
# Like asl-integration, it fails rather than skips: AMX_ASL_OVERSEER_SMOKE
# turns the suite's "no installed pair here" skip into a failure, because a
# silent skip would report a compatible pair that was never actually checked.
#
# As with asl-contract-sync, `just` passes everything after the recipe name
# positionally, so the documented `asl_root=/path/to/scripts` prefix is stripped
# here rather than refused.
overseer-integration asl_root:
    #!/usr/bin/env bash
    set -euo pipefail

    asl_root_arg="{{ asl_root }}"
    asl_root="${asl_root_arg#asl_root=}"
    amx="{{ prefix_bin }}/amx"
    suite="tests.test_asl_overseer_paired_smoke"

    if [ ! -x "$amx" ]; then
        echo "no installed amx at $amx; run just install-restart-smoke first" >&2
        exit 1
    fi

    if [ ! -d "$asl_root/agent_session_loop" ]; then
        echo "no ASL checkout at $asl_root" >&2
        echo "pass the ASL checkout: just overseer-integration asl_root=/path/to/scripts" >&2
        exit 1
    fi

    # The installed wrapper has to be the one this checkout publishes, or the
    # run would judge a pair nobody deployed. The suite asserts this too; it is
    # repeated here so the operator hears which checkout is wrong, immediately,
    # rather than as a failing test.
    if ! wrapper="$(command -v asl)"; then
        echo "no asl on PATH; install it from $asl_root: just all" >&2
        exit 1
    fi
    resolved="$(cd "$(dirname "$(readlink -f "$wrapper")")" && pwd -P)"
    canonical="$(cd "$asl_root" && pwd -P)"
    case "$resolved/" in
        "$canonical"/*) ;;
        *)
            echo "installed asl resolves to $resolved, outside $canonical" >&2
            echo "install this checkout first: cd $asl_root && just all" >&2
            exit 1
            ;;
    esac

    echo "== installed pair =="
    "$amx" --version
    echo "asl: $wrapper -> $(readlink -f "$wrapper")"

    echo "== overseer smoke =="
    cd "$asl_root"
    AMX_ASL_OVERSEER_SMOKE=1 AMX_INSTALLED_BINARY="$amx" \
        python3 -m unittest "$suite" -v

    echo "overseer-integration: ok"

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

# The daemon scenarios that assert state survives a crash, a restart, or a
# degraded start. TestFault also covers the companion stream: a daemon stop
# says bye{shutdown} to every subscriber, and a subscriber killed mid-write
# leaves the daemon healthy. Kept separate from stress_scenarios: those repeat healthy
# lifecycle paths to shake out ordering races, while these inject a fault and
# assert the observable state afterwards.
crash_restart_scenarios := "TestFault|TestFuzzReducerRestartEquivalence|TestTraceReplayRestartEquivalence|TestShadowReducerSurvivesRestart|TestInteractiveOwnershipSurvivesRestart"

# Crash, restart, and degraded-start gate. Runs the fault-injection and
# restart-equivalence scenarios under the race detector.
#
# The count guard is the point of the shell wrapper. `go test -run PATTERN`
# exits 0 when the pattern matches nothing, so a renamed test silently converts
# this gate into a green step that checked nothing -- the exact failure mode a
# release gate must not have. The floor is deliberately below the current count
# so deleting one scenario does not break the build, and far enough above zero
# that a pattern which stopped matching fails here.
crash-restart:
    #!/usr/bin/env bash
    set -euo pipefail

    log="$(mktemp)"
    trap 'rm -f "$log"' EXIT

    go test -race -count=1 -v -run '{{ crash_restart_scenarios }}' ./internal/daemon 2>&1 | tee "$log"

    if grep -q "no tests to run" "$log"; then
        echo >&2
        echo "crash-restart matched no tests: the -run pattern is stale" >&2
        exit 1
    fi

    # Subtest lines are indented, so this counts top-level scenarios only.
    passed=$(grep -c '^--- PASS' "$log" || true)
    if [ "$passed" -lt 13 ]; then
        echo >&2
        echo "crash-restart ran only $passed top-level scenarios (expected at least 13)" >&2
        echo "update crash_restart_scenarios if a scenario was intentionally renamed" >&2
        exit 1
    fi

    echo "crash-restart: ok ($passed scenarios)"

# Real-Kitty cockpit smoke over the *installed* pair. Read-only: it observes,
# it never mutates a session, a plan, or config.toml.
#
# Everything else in this repository simulates the terminal. This recipe is the
# one place a real `kitten @` round trip, a real daemon, and a real ASL pairing
# preflight are all required at once, which is why it belongs to the release
# gate rather than to `just check`. It fails rather than skips: a cockpit smoke
# that quietly passes because Kitty was absent is worse than no smoke.
#
# Scope note, so the evidence is not read as more than it is: this half proves
# the cockpit observes a live Kitty window through the daemon. The intervention
# half -- fetching a question body, refusing a stale token, accepting a mutation
# and getting a body-free receipt -- is proven against the installed ASL wrapper
# by `just asl-integration`, which the release gate runs separately.
cockpit-smoke:
    #!/usr/bin/env bash
    set -euo pipefail

    amx="{{ prefix_bin }}/amx"

    if [ ! -x "$amx" ]; then
        echo "no installed amx at $amx; run just install-restart-smoke first" >&2
        exit 1
    fi

    if [ -z "${KITTY_WINDOW_ID:-}" ]; then
        echo "KITTY_WINDOW_ID is unset: run the cockpit smoke from a Kitty window" >&2
        exit 1
    fi

    echo "== kitty =="
    kitty --version
    kitten @ ls > /dev/null
    echo "kitten @ ls: ok"

    echo "== daemon =="
    "$amx" daemon status | grep -q '^ready: true' || {
        echo "daemon is not ready" >&2
        "$amx" daemon status >&2
        exit 1
    }
    "$amx" daemon status

    # Proves the daemon is reducing facts for this real window, not merely
    # answering its socket.
    echo "== live window events =="
    "$amx" daemon export-events --current-window --last 50 > /dev/null
    echo "export-events --current-window: ok"

    echo "== invariants =="
    "$amx" doctor --strict

    echo "== asl pairing preflight =="
    "$amx" config asl-sync --check

    echo "cockpit-smoke: ok"

# Run the paired agent-session-loop / Agent Mux release gate.
#
#   just paired-release-gate asl_root=~/scripts signing_key=~/.ssh/amx_release
#   just paired-release-gate asl_root=~/scripts signing_key=~/.ssh/amx_release tag=yes
#
# The gate itself lives in internal/release and is documented there; this recipe
# only translates the `key=value` invocation into flags. As with
# asl-contract-sync, `just` hands everything after the recipe name to the recipe
# positionally, so the prefixes are stripped here rather than refused.
#
# This has real side effects. It installs both programs, restarts the running
# daemon, and (with tag=yes) writes annotated tags into both repositories.
#
# tag=yes requires signing_key. An unsigned run is a rehearsal, and the pair
# tags are the durable claim that a release happened -- a rehearsal that could
# write them would leave release provenance nobody can verify. The gate refuses
# the combination itself; the recipe refuses it first so the operator hears it
# immediately rather than after the toolchain starts.
paired-release-gate asl_root signing_key="" tag="no":
    #!/usr/bin/env bash
    set -euo pipefail

    asl_root="{{ asl_root }}"
    signing_key="{{ signing_key }}"
    tag="{{ tag }}"

    # A caller who omits asl_root and passes only signing_key would otherwise
    # have the key silently interpreted as a checkout path.
    case "$asl_root" in
        signing_key=*|tag=*)
            echo "first argument must be the ASL checkout: asl_root=/path/to/scripts" >&2
            exit 1
            ;;
    esac
    # And a caller who omits signing_key and passes only tag would otherwise
    # have "tag=yes" read as a key path -- which the gate then refuses for the
    # wrong reason, after silently dropping the tag request the operator made.
    case "$signing_key" in
        asl_root=*|tag=*)
            echo "second argument must be the signing key: signing_key=/path/to/key" >&2
            echo "to rehearse without one, pass an empty signing_key= before tag=" >&2
            exit 1
            ;;
    esac
    asl_root="${asl_root#asl_root=}"
    signing_key="${signing_key#signing_key=}"
    tag="${tag#tag=}"

    tag_requested=no
    case "$tag" in
        yes|true|1) tag_requested=yes ;;
    esac

    args=(release-gate --asl-root "$asl_root" --amx-root "{{ root }}")
    if [ -n "$signing_key" ]; then
        args+=(--signing-key "$signing_key")
    elif [ "$tag_requested" = yes ]; then
        echo "refusing: tag=yes requires signing_key=/path/to/key" >&2
        echo "an unsigned record is a rehearsal, not a release, and a rehearsal does not tag" >&2
        exit 1
    else
        echo "warning: no signing_key given; the evidence record will be unsigned" >&2
        echo "warning: an unsigned record is a rehearsal, not a release" >&2
    fi
    if [ "$tag_requested" = yes ]; then
        args+=(--tag)
    fi

    go run . "${args[@]}"

# Product acceptance without the optional linter dependency.
#
# fmt-check leads because it is the cheapest gate here and the one whose
# failure says the least about the change: a formatting drift found after a
# twenty-minute race suite is a twenty-minute wait for a one-command fix.
acceptance: fmt-check test race-core vet tui-scenarios

# Run linter.
# golangci-lint's default lock is machine-wide, not repository-scoped, so a
# linter running in an unrelated checkout would otherwise fail this gate.
lint:
    golangci-lint run --allow-parallel-runners

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

# Build macos/build/Agent Mux.app, the menu bar companion. Debug bundles amx
# only when AMX_BINARY names one and otherwise finds ~/.local/bin/amx at run
# time; Release builds amx with `just build` and bundles that. Like mac-test and
# mac-install it needs Xcode, so none of the three is part of check, acceptance,
# or paired-release-gate. See macos/README.md.
mac-app configuration="Debug":
    #!/usr/bin/env bash
    set -euo pipefail
    if [ "{{ configuration }}" = Release ]; then
        just build
        export AMX_BINARY="{{ root }}/amx"
    fi
    ./macos/scripts/build-app.sh {{ configuration }}

# Run the Agent Mux.app XCTest suite with xcodebuild test (outside check)
mac-test:
    ./macos/scripts/test-app.sh

# Build the Release Agent Mux.app, install it to /Applications, and relaunch it
mac-install: (mac-app "Release")
    ./macos/scripts/install-app.sh

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
