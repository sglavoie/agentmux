# Show available recipes by default
[private]
default:
    @just --list

prefix_bin := home_directory() / ".local/bin"
prefix_kitty := home_directory() / ".config/kitty"
prefix_config := home_directory() / ".config/agent-mux"
root := justfile_directory()

# The TUI scenario harness (tui's uitest_*.go files and cmd/tuitest) builds
# only with this tag, so a release amx carries none of it. Every recipe that
# tests, vets, races, lints, or runs scenarios passes it; `build` and
# `install` never do. Plain `go test ./...` still passes but skips the harness
# and the tests that drive the dashboard through it, so the gates never run it
# that way. cmd/release's TestGatesBuildTheScenarioHarness holds the recipes
# to this.
harness_tags := "tuitest"

# Build the amx binary (release build: no scenario harness)
build:
    go build -o amx .

# Regenerate the terminal demo SVG from the offline TUI tour (requires uv)
readme-media:
    sh scripts/demo/readme-media.sh

# Check that the terminal demo SVG matches the dashboard's current rendering
readme-media-check:
    sh scripts/demo/readme-media.sh --check

# The suite runs once with the harness tag, which is a superset of the
# untagged suite everywhere except root's command assembly: there the tag
# swaps the hidden tui-test stand-in for the real harness, so `./cmd` runs once
# more without the tag to keep the release build's tui-test covered.
#
# Run all tests, including the TUI scenario harness
test:
    go test -tags {{ harness_tags }} ./...
    go test ./cmd

# Vetting both builds is what keeps each side of a build constraint compiled:
# the tagged harness files on one side, the release build's tui-test stand-in
# on the other.
#
# Run go vet over the release and the harness builds
vet:
    go vet ./...
    go vet -tags {{ harness_tags }} ./...

# gofmt exits 0 whether or not it found anything, so the check is on its
# output: any listed path is a file the formatter would rewrite. The paths are
# printed before failing, because "run gofmt -w" is only actionable if the
# operator knows on what.
#
# The file list comes from git rather than from a directory walk. An operator's
# checkout carries agent worktrees and other untracked Go sources under
# .claude/, and a gate that failed on a stale copy of a file nobody is editing
# would be a gate people learn to ignore.
#
# Fail when any tracked Go file is not gofmt-clean
fmt-check:
    #!/usr/bin/env bash
    set -euo pipefail
    unformatted="$(git ls-files -z '*.go' | xargs -0 gofmt -l)"
    if [ -n "$unformatted" ]; then
        echo "gofmt would rewrite these files; run 'gofmt -w' on them:" >&2
        echo "$unformatted" >&2
        exit 1
    fi

# The concurrency-sensitive filesystem persistence, continuity, Kitty
# read-coordination, daemon, and TUI suites.
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
#
# Run the concurrency-sensitive suites under the race detector
race-core:
    go test -race -tags {{ harness_tags }} ./internal/transcript ./internal/plans ./internal/handoff ./internal/worktree ./internal/kitty ./internal/daemon ./internal/daemon/ipc ./tui ./tui/commands ./tui/overview ./tui/sessionactions

# `just test` already runs the same set through the same CLI path (see the
# acceptance recipe), so this is for running them by hand.
#
# Run every committed narrow/wide TUI acceptance scenario in isolation
tui-scenarios:
    go run -tags {{ harness_tags }} . tui-test run docs/tui-testing/scenarios/*.json

# Exercise the installed agent-session-loop bridge end to end against a
# throwaway plan and state root. Every other recipe stays hermetic and needs no
# ASL; this one is opt-in because it needs a real `asl` on PATH. When it is
# asked for, an ASL that is missing or speaks a different protocol fails here
# rather than skipping: a silent skip would report a compatible pair that was
# never actually checked.
#
# Two packages hold a half each. internal/aslbridge checks the bridge protocol
# against a state corpus. internal/daemon drives real `asl run`s — one inside a
# private tmux session, one inside a Kitty runtime session — through this
# checkout's hook client and daemon handler, so it additionally needs tmux.
#
# Check the installed agent-session-loop bridge end to end (opt-in; needs asl and tmux)
asl-integration:
    AMX_ASL_INTEGRATION=1 go test -count=1 -run TestInstalledBridge ./internal/aslbridge ./internal/daemon

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
# writes the documented `asl_root=/path/to/agent-session-loop` form hands the
# recipe that whole string. scripts/named-arg strips the prefix (and expands a
# leading ~, which zsh, unlike bash, leaves unexpanded after =) rather than
# refusing it, so the named and bare forms both work and neither silently
# checks the wrong directory.
#
# Compare the vendored ASL contract corpus with an ASL checkout's (offline)
asl-contract-sync asl_root:
    #!/usr/bin/env bash
    set -euo pipefail

    asl_root="$("{{ root }}/scripts/named-arg" asl_root "{{ asl_root }}")"
    canonical="$asl_root/tests/fixtures/agent_mux_contract"
    vendored="{{ root }}/internal/aslcontract/corpus"

    if [ ! -d "$canonical" ]; then
        echo "no ASL corpus at $canonical" >&2
        echo "pass the ASL checkout: just asl-contract-sync asl_root=/path/to/agent-session-loop" >&2
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
# As with asl-contract-sync, the documented
# `asl_root=/path/to/agent-session-loop` prefix is stripped by scripts/named-arg
# rather than refused.
#
# Exercise the overseer against the installed amx and asl pair, in a throwaway world
overseer-integration asl_root: installed-amx
    #!/usr/bin/env bash
    set -euo pipefail

    asl_root="$("{{ root }}/scripts/named-arg" asl_root "{{ asl_root }}")"
    amx="{{ prefix_bin }}/amx"
    suite="tests.test_asl_overseer_paired_smoke"

    if [ ! -d "$asl_root/agent_session_loop" ]; then
        echo "no ASL checkout at $asl_root" >&2
        echo "pass the ASL checkout: just overseer-integration asl_root=/path/to/agent-session-loop" >&2
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
# state paths, which include health asked while maintenance reconciles a real
# private tmux server and the probes read its panes. Extend this pattern as new
# fault scenarios land.
stress_scenarios := "TestRun_|TestServer_|TestHandler_Concurrent"

# Repeats the daemon lifecycle scenarios in isolation, then again as concurrent
# shards while the full suite loads the machine (both the shards and the loaded
# suite must pass), then measures the latency budgets on a quiet machine.
#
# The correctness scenarios synchronize on signals the daemon publishes, so
# contention must not change their outcome; the latency budgets are measured
# separately and deliberately, because contention legitimately changes those.
#
# Run the reliability stress gate: lifecycle scenarios under contention, then latency budgets
reliability-stress reps="20" shards="4":
    #!/usr/bin/env bash
    set -euo pipefail

    log_dir="$(mktemp -d)"
    trap 'rm -rf "$log_dir"' EXIT

    echo "== isolated repetitions (x{{ reps }}) =="
    go test -count={{ reps }} -run '{{ stress_scenarios }}' ./internal/daemon

    echo "== isolated repetitions under -race (x{{ reps }}) =="
    go test -race -count={{ reps }} -run '{{ stress_scenarios }}' ./internal/daemon

    # The full suite runs here to saturate the machine while the daemon shards
    # race against it, and it is gated too: a test that fails only when the
    # machine is loaded is a reliability failure, which is what this recipe is
    # for. Both outcomes are printed before either fails the recipe, so a
    # failure says which half broke.
    echo "== {{ shards }} concurrent shards (x{{ reps }}) under full-suite load =="
    go test -count=1 -tags {{ harness_tags }} ./... > "$log_dir/suite.log" 2>&1 &
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
        echo "-- daemon shards under load: FAILED"
        for log in "$log_dir"/shard-*.log; do
            echo "--- $log"
            cat "$log"
        done
    else
        echo "-- daemon shards under load: ok"
    fi
    if [ "$suite_status" -ne 0 ]; then
        echo "-- full suite under load: FAILED (exit $suite_status)"
        grep -E '^(--- FAIL|FAIL|panic:)' "$log_dir/suite.log" || true
        echo "--- last 40 lines of the suite log"
        tail -n 40 "$log_dir/suite.log"
    else
        echo "-- full suite under load: ok"
    fi
    if [ "$failed" -ne 0 ] || [ "$suite_status" -ne 0 ]; then
        exit 1
    fi

    # Budgets last and alone: everything above has finished, so the machine is
    # quiet and a measured overrun means the daemon got slower, not that the
    # stress run was still competing with itself.
    #
    # The fleet budgets (1, 10, and 30 tmux agents, idle and printing) are
    # measured against the tmux the app ships when one has been built
    # (`just mac-tmux`), so the numbers are of the packaged runtime; without
    # one they use whatever tmux the runtime resolves, and skip without any.
    echo "== latency budgets (quiet machine) =="
    packaged_tmux="{{ justfile_directory() }}/macos/Vendor/tmux/bin/tmux"
    if [ -z "${AMX_TMUX:-}" ] && [ -x "$packaged_tmux" ]; then
        export AMX_TMUX="$packaged_tmux"
        echo "-- fleet budgets against the packaged $("$packaged_tmux" -V)"
    fi
    AMX_PERF_TESTS=1 go test -count=1 -v -run 'TestPerformance_' ./internal/daemon

    echo "reliability-stress: ok"

# The daemon scenarios that assert state survives a crash, a restart, or a
# degraded start. TestFault also covers the companion stream: a daemon stop
# says bye{shutdown} to every subscriber, and a subscriber killed mid-write
# leaves the daemon healthy. It also covers tmux-hosted agents against a real
# private tmux server: the same process after a daemon restart, a killed
# viewer, or a change of owner; exited or lost, and never relaunched, after
# the agent or its server dies. Kept separate from stress_scenarios: those repeat healthy
# lifecycle paths to shake out ordering races, while these inject a fault and
# assert the observable state afterwards.
crash_restart_scenarios := "TestFault|TestFuzzReducerRestartEquivalence|TestTraceReplayRestartEquivalence|TestShadowReducerSurvivesRestart|TestInteractiveOwnershipSurvivesRestart"

# The count guard is the point of the shell wrapper. `go test -run PATTERN`
# exits 0 when the pattern matches nothing, so a renamed test silently converts
# this gate into a green step that checked nothing -- the exact failure mode a
# release gate must not have. The expected count is not written down: it is
# however many top-level tests `go test -list` finds for the same pattern, so
# adding or removing a scenario needs no edit here, and every one it lists has
# to pass rather than skip.
#
# Run the crash, restart, and degraded-start scenarios under the race detector
crash-restart:
    #!/usr/bin/env bash
    set -euo pipefail

    log="$(mktemp)"
    trap 'rm -f "$log"' EXIT

    expected=$(go test -list '{{ crash_restart_scenarios }}' ./internal/daemon | grep -c '^Test' || true)
    if [ "$expected" -eq 0 ]; then
        echo "crash-restart's pattern lists no tests: the -run pattern is stale" >&2
        exit 1
    fi

    go test -race -count=1 -v -run '{{ crash_restart_scenarios }}' ./internal/daemon 2>&1 | tee "$log"

    if grep -q "no tests to run" "$log"; then
        echo >&2
        echo "crash-restart matched no tests: the -run pattern is stale" >&2
        exit 1
    fi

    # Subtest lines are indented, so this counts top-level scenarios only.
    passed=$(grep -c '^--- PASS' "$log" || true)
    # The tmux scenarios skip when tmux is not installed, which leaves the
    # count short: a fault gate that passes without them proves nothing about
    # the agents the desktop app keeps alive.
    if [ "$passed" -ne "$expected" ]; then
        echo >&2
        echo "crash-restart passed $passed of the $expected top-level scenarios its pattern lists (the tmux scenarios need tmux)" >&2
        grep '^--- SKIP' "$log" >&2 || true
        exit 1
    fi

    echo "crash-restart: ok ($passed scenarios)"

# Read-only: it observes, it never mutates a session, a plan, or config.toml.
#
# Everything else in this repository runs tmux in a world of its own. This
# recipe is the one place the installed amx, the tmux it would start agents
# on, a real daemon, and a real ASL pairing preflight are all required at
# once, which is why it belongs to the release gate rather than to
# `just check`. It runs from any terminal and needs no Kitty. A missing or
# unusable tmux is a failure: a cockpit smoke that quietly passes because
# there was no backend to observe is worse than no smoke.
#
# The tmux round trip runs the executable `amx backend status` resolved, in a
# server of its own on a socket in a temporary directory, so the operator's
# agents and the private server that holds them are never touched.
#
# Scope note, so the evidence is not read as more than it is: this proves the
# installed amx resolves a tmux that runs, that the daemon is ready and
# answers for the sessions it holds, and that its invariants hold. Reducing a
# real tmux session's hooks is proven by `just asl-integration`, and the
# intervention half -- fetching a question body, refusing a stale token,
# accepting a mutation and getting a body-free receipt -- by the same recipe
# against the installed ASL wrapper; the release gate runs it separately.
#
# Smoke the installed pair against a real tmux and daemon, from any terminal (read-only)
cockpit-smoke: installed-amx daemon-ready
    #!/usr/bin/env bash
    set -euo pipefail

    amx="{{ prefix_bin }}/amx"

    echo "== tmux backend =="
    "$amx" backend status
    "$amx" backend status --check || {
        echo "the tmux backend cannot start agents on this machine" >&2
        exit 1
    }
    tmux_bin="$("$amx" backend status | sed -n 's/^tmux: \(.*\) ([^()]*)$/\1/p')"
    if [ -z "$tmux_bin" ] || [ ! -x "$tmux_bin" ]; then
        echo "amx backend status named no tmux executable" >&2
        exit 1
    fi

    echo "== tmux round trip =="
    world="$(mktemp -d "${TMPDIR:-/tmp}/amx-smoke.XXXXXX")"
    socket="$world/t.sock"
    cleanup() {
        "$tmux_bin" -S "$socket" kill-server > /dev/null 2>&1 || true
        rm -rf "$world"
    }
    trap cleanup EXIT
    "$tmux_bin" -S "$socket" -f /dev/null new-session -d -s smoke -x 80 -y 24 'sleep 120'
    pane="$("$tmux_bin" -S "$socket" list-panes -a -F '#{pane_id} #{pane_dead}')"
    case "$pane" in
        "%"*" 0") echo "$tmux_bin: one live pane ($pane)" ;;
        *)
            echo "$tmux_bin started no live pane: '$pane'" >&2
            exit 1
            ;;
    esac
    "$tmux_bin" -S "$socket" kill-server

    # daemon-ready, a dependency, has already required `ready: true`.
    echo "== daemon =="
    "$amx" daemon status

    # Proves the daemon answers for the sessions it holds, not merely that
    # its socket is reachable.
    echo "== session events =="
    "$amx" daemon export-events --last 50 > /dev/null
    echo "export-events: ok"

    echo "== invariants =="
    "$amx" doctor --strict

    echo "== asl pairing preflight =="
    "$amx" config asl-sync --check

    echo "cockpit-smoke: ok"

# For an operator who launches into Kitty. Opt-in: no gate runs it, since
# Kitty is an optional backend. Like the cockpit smoke it observes and never
# mutates, and it fails when there is no Kitty to observe.
#
# Smoke the installed amx against a real Kitty, from a Kitty window (opt-in)
kitty-smoke: installed-amx daemon-ready
    #!/usr/bin/env bash
    set -euo pipefail

    amx="{{ prefix_bin }}/amx"

    if [ -z "${KITTY_WINDOW_ID:-}" ]; then
        echo "KITTY_WINDOW_ID is unset: run the Kitty smoke from a Kitty window" >&2
        exit 1
    fi

    echo "== kitty =="
    kitty --version
    kitten @ ls > /dev/null
    echo "kitten @ ls: ok"

    # daemon-ready, a dependency, has already required `ready: true`.
    echo "== daemon =="
    "$amx" daemon status

    # Proves the daemon is reducing facts for this real window, not merely
    # answering its socket.
    echo "== live window events =="
    "$amx" daemon export-events --current-window --last 50 > /dev/null
    echo "export-events --current-window: ok"

    echo "== invariants =="
    "$amx" doctor --strict

    echo "kitty-smoke: ok"

#   just paired-release-gate asl_root=~/1_dev_projects/sglavoie_agent-session-loop signing_key=~/.ssh/amx_release
#   just paired-release-gate asl_root=~/1_dev_projects/sglavoie_agent-session-loop signing_key=~/.ssh/amx_release tag=yes
#
# The gate itself lives in internal/release and is documented there; this recipe
# only translates the `key=value` invocation into flags. As with
# asl-contract-sync, `just` hands everything after the recipe name to the recipe
# positionally, so scripts/named-arg strips the prefixes rather than refusing
# them, after the checks below have used them to catch arguments out of order.
#
# This has real side effects. It installs both programs, restarts the running
# daemon, and (with tag=yes) writes annotated tags into both repositories.
#
# tag=yes requires signing_key. An unsigned run is a rehearsal, and the pair
# tags are the durable claim that a release happened -- a rehearsal that could
# write them would leave release provenance nobody can verify. The gate refuses
# the combination itself; the recipe refuses it first so the operator hears it
# immediately rather than after the toolchain starts.
#
# Run the paired agent-session-loop / Agent Mux release gate (installs both, restarts the daemon)
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
            echo "first argument must be the ASL checkout: asl_root=/path/to/agent-session-loop" >&2
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
    named_arg="{{ root }}/scripts/named-arg"
    asl_root="$("$named_arg" asl_root "$asl_root")"
    signing_key="$("$named_arg" signing_key "$signing_key")"
    tag="$("$named_arg" tag "$tag")"

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

# The one-step release: everything paired-release-gate does with tag=yes, then
# the pair tag pushed to both repositories' origin and a GitHub release for
# Agent Mux with the binary, the evidence record, and its signature. A signing
# key is required, never optional: an unsigned run is a rehearsal and has
# nothing to publish. scripts/publish-release preflights first, so an unpushed
# main or an already-released version fails in seconds rather than after the
# gate.
#
# Release the pair: gate, tag, push both tags, and publish the GitHub release
release asl_root signing_key:
    #!/usr/bin/env bash
    set -euo pipefail
    named_arg="{{ root }}/scripts/named-arg"
    asl_root="$("$named_arg" asl_root "{{ asl_root }}")"
    signing_key="$("$named_arg" signing_key "{{ signing_key }}")"
    publish="{{ root }}/scripts/publish-release"

    "$publish" preflight "{{ root }}" "$asl_root" "$signing_key"

    summary="$(mktemp)"
    trap 'rm -f "$summary"' EXIT
    "{{ just_executable() }}" --justfile "{{ justfile() }}" \
        paired-release-gate "$asl_root" "$signing_key" yes | tee "$summary"
    record="$(sed -n 's/^  evidence //p' "$summary")"
    [ -n "$record" ] || { echo "release: the gate named no evidence record" >&2; exit 1; }

    "$publish" publish "{{ root }}" "$record"

# Publish a pair the gate already passed and tagged (re-runnable): push both tags, create the GitHub release
publish-release record:
    "{{ root }}/scripts/publish-release" publish "{{ root }}" "$("{{ root }}/scripts/named-arg" record "{{ record }}")"

# fmt-check leads because it is the cheapest gate here and the one whose
# failure says the least about the change: a formatting drift found after a
# twenty-minute race suite is a twenty-minute wait for a one-command fix.
#
# Every committed TUI scenario runs inside `test` (which passes the harness
# tag; an untagged `go test` would build neither runner), so tui-scenarios is
# not repeated here: cmd/tuitest's TestTUITestRunCommittedScenariosEndToEnd runs
# the same files through the same CLI path that recipe does (in parallel
# shards, each under a sentinel HOME that must come back untouched), and tui's
# TestExampleScenariosParseAndPass runs them in-package against the README
# inventory. The tui-scenarios recipe stays for running them by hand.
#
# Run product acceptance: gofmt, tests, race suites, vet, TUI scenarios (no linter)
acceptance: fmt-check test race-core vet

# golangci-lint's default lock is machine-wide, not repository-scoped, so a
# linter running in an unrelated checkout would otherwise fail this gate.
#
# A build constraint hides a file from every run that does not satisfy it, so
# one run per build is what lints both the harness files and the release
# build's tui-test stand-in.
#
# Run golangci-lint over the release and the harness builds
lint:
    golangci-lint run --allow-parallel-runners
    golangci-lint run --allow-parallel-runners --build-tags {{ harness_tags }}

# Run the full repository gate: acceptance plus golangci-lint
check: acceptance lint

# Nothing of Kitty's is created or linked; `just install-kitty` does that for
# the Kitty backend. The binary is staged beside the destination and renamed into place so the
# replacement is atomic: no window where amx is absent or half-copied, and a
# running daemon keeps its already-open executable.
#
# Install amx atomically into ~/.local/bin, plus a starter config if missing
install: build
    @mkdir -p "{{prefix_bin}}"
    cp amx "{{prefix_bin}}/.amx.staging"
    mv -f "{{prefix_bin}}/.amx.staging" "{{prefix_bin}}/amx"
    @if [ ! -f "{{prefix_config}}/config.toml" ]; then "{{prefix_bin}}/amx" config init >/dev/null; echo "Seeded {{prefix_config}}/config.toml"; fi
    @echo "Installed {{prefix_bin}}/amx"

# Link the Kitty snippet for the optional Kitty backend (include it from kitty.conf)
install-kitty:
    @mkdir -p "{{prefix_kitty}}"
    ln -sf "{{root}}/agent-mux.conf" "{{prefix_kitty}}/agent-mux.conf"
    @echo "Symlinked {{prefix_kitty}}/agent-mux.conf"

# Strict doctor verifies the CLI and daemon are the same artifact by
# hash/revision and that no invariant is violated.
#
# Install amx, restart the daemon onto it, and gate on strict doctor
install-restart-smoke: install restart-verify
    @echo "install-restart-smoke: ok"

# The app goes in before the daemon restart so doctor judges the pair as it
# will run, and a bundled amx from another revision shows up as
# companion_build_mismatch. Needs Xcode; without it, use install-restart-smoke.
#
# It runs from any terminal: outside Kitty the restarted daemon keeps the
# Kitty its predecessor read, and with none running it is ready without one.
# The Kitty snippet is a link into this checkout, so one made by
# `just install-kitty` already follows it.
#
# Update everything from this checkout: amx, hooks, menu bar app, daemon
update: install sync-hooks mac-install restart-verify
    @echo "update: ok ($("{{prefix_bin}}/amx" --version))"

# Hook files follow the installed amx's templates, so a new amx can leave an
# installed one drifted, which strict doctor fails on.
[private]
sync-hooks:
    "{{prefix_bin}}/amx" config hooks-sync

# Restart the daemon onto the installed binary, wait for it to report ready,
# and gate on strict doctor. The waiting and the judging are subsequent
# dependencies, so they run after the restart, in order.
[private]
restart-verify: && (daemon-ready "50") verify-daemon
    "{{prefix_bin}}/amx" daemon restart

# Fail unless the installed amx exists. Every recipe that judges the installed
# pair rather than these sources starts here.
[private]
installed-amx:
    @if [ ! -x "{{prefix_bin}}/amx" ]; then \
        echo "no installed amx at {{prefix_bin}}/amx; run just install-restart-smoke first" >&2; \
        exit 1; \
    fi

# Fail unless the installed daemon reports `ready: true`, asking up to `tries`
# times 0.2s apart: once for a daemon that should already be up, fifty after a
# restart. The status is read whole before it is matched, so a reader that
# stops early cannot fail the pipeline under pipefail.
[private]
daemon-ready tries="1": installed-amx
    #!/usr/bin/env bash
    set -euo pipefail
    amx="{{ prefix_bin }}/amx"
    ready=no
    for i in $(seq 1 {{ tries }}); do
        status="$("$amx" daemon status 2>&1 || true)"
        if grep -q '^ready: true' <<< "$status"; then
            ready=yes
            break
        fi
        if [ "$i" -lt {{ tries }} ]; then
            sleep 0.2
        fi
    done
    if [ "$ready" != yes ]; then
        echo "daemon is not ready after {{ tries }} check(s)" >&2
        "$amx" daemon status >&2 || true
        exit 1
    fi

# Judge a daemon that has just reported ready: give a required ASL bridge the
# same bounded window to handshake (it does so after readiness), then strict
# doctor. A bridge that never connects is doctor's to report, not this wait's.
[private]
verify-daemon:
    #!/usr/bin/env bash
    set -euo pipefail
    amx="{{ prefix_bin }}/amx"
    for _ in $(seq 1 50); do
        status="$("$amx" daemon status 2>&1 || true)"
        if ! grep -Eq '^asl: (unavailable|read-only) \(mode required' <<< "$status"; then
            break
        fi
        sleep 0.2
    done
    "$amx" daemon status
    "$amx" doctor --strict

# Debug bundles amx only when AMX_BINARY names one and otherwise finds ~/.local/bin/amx at run
# time; Release builds amx with `just build` and bundles that. Both bundle the
# pinned tmux runtime. Like mac-test, mac-install, mac-ghosttykit, and
# mac-release-gate it needs Xcode, so none of them is part of check,
# acceptance, or paired-release-gate. See macos/README.md.
#
# Build macos/build/Agent Mux.app (Debug, or Release with a freshly built amx)
mac-app configuration="Debug":
    #!/usr/bin/env bash
    set -euo pipefail
    if [ "{{ configuration }}" = Release ]; then
        just build
        export AMX_BINARY="{{ root }}/amx"
    fi
    ./macos/scripts/build-app.sh {{ configuration }}

# Run the Agent Mux.app XCTest suite with xcodebuild test (outside check); `just mac-test Release` runs it against the optimized app
mac-test configuration="Debug":
    ./macos/scripts/test-app.sh {{ configuration }}

# Build the pinned GhosttyKit into macos/Vendor (a no-op when current; mac-app and mac-test run it themselves)
mac-ghosttykit:
    ./macos/scripts/build-ghosttykit.sh

# Build the pinned tmux runtime the app bundles into macos/Vendor/tmux (a no-op when current; mac-app runs it itself)
mac-tmux:
    ./macos/scripts/build-tmux.sh

# Running agents are not touched: they live in a tmux server that outlives the
# app, and the installer fails if it finds that server changed.
#
# Build the Release Agent Mux.app, install or update it in /Applications, and relaunch it
mac-install: (mac-app "Release")
    ./macos/scripts/install-app.sh

# It builds the Release app (on the Mac AMX_MAC_TEST_HOST names, when one is
# named), checks the bundle's seal, its tmux runtime, and that its amx is the
# one this checkout built, runs the hosted suite against the Debug and the
# Release app, and writes its own evidence record. It installs nothing and
# restarts nothing. Like every mac-* recipe it needs Xcode and stays outside
# check, acceptance, and paired-release-gate; when there is no Xcode to be had
# the record says pending and the recipe fails. See docs/desktop-releases.md.
#
#   just mac-release-gate --signing-key ~/.ssh/amx_release
#   just mac-release-gate --notarized --signing-key ~/.ssh/amx_release
#   just mac-release-gate --rehearsal --evidence-dir /tmp/evidence
#
# Run the Agent Mux.app release gate and write its evidence record (outside check)
mac-release-gate *flags:
    go run . desktop-release-gate --amx-root "{{ root }}" {{ flags }}

# Remove Agent Mux.app; agents keep running and data stays unless asked: --end-sessions, --remove-app-data, --yes
mac-uninstall *flags:
    ./macos/scripts/uninstall-app.sh {{ flags }}

# Stop the daemon, then remove the binary and the Kitty snippet if linked (config.toml and data are kept)
uninstall:
    -"{{prefix_bin}}/amx" daemon stop
    rm -f "{{prefix_bin}}/amx"
    rm -f "{{prefix_kitty}}/agent-mux.conf"
    @echo "Removed {{prefix_bin}}/amx"
    @echo "Removed {{prefix_kitty}}/agent-mux.conf"

# Bump version in cmd/root.go (e.g. just bump 0.4.0)
bump version:
    @perl -pi -e 's/const version = ".*"/const version = "{{version}}"/' cmd/root.go
    @grep 'const version' cmd/root.go
