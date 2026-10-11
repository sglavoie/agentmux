#!/usr/bin/env bash
# The isolated installed pair, on the Mac Studio, headless.
#
#   pair.sh AGENTMUX_CHECKOUT ASL_SOURCE LOG_DIR
#
# It builds a world under /tmp with its own HOME and TMPDIR, installs this
# checkout's amx into it (`just install` follows HOME), clones ASL_SOURCE into
# it (the source itself is only read: by `git clone --no-hardlinks`, and by
# the contract comparison),
# links the clone's wrapper as the world's `asl`, and runs the three
# cross-repository recipes against that pair. Nothing of the operator's is
# installed over, restarted, or read: the operator's amx, asl, daemon, config
# and state are compared before and after.
#
# The world and every process started in it are removed at the end.
set -uo pipefail

checkout="$(cd "$1" && pwd -P)"
source_root="$(cd "$2" && pwd -P)"
logs="$(mkdir -p "$3" && cd "$3" && pwd -P)"

world="$(mktemp -d /tmp/amxs19-pair.XXXXXX)"
world="$(cd "$world" && pwd -P)"
home="$world/home"
mkdir -p "$home/.local/bin" "$world/tmp"
status=0

operator_state() {
    for f in "$HOME/.local/bin/amx" "$HOME/.local/bin/asl" "$HOME/.config/agent-mux/config.toml"; do
        if [ -e "$f" ]; then
            printf '%s %s %s\n' "$(shasum -a 256 "$(readlink -f "$f")" | cut -c1-16)" "$(readlink -f "$f")" "$f"
        else
            printf 'absent %s\n' "$f"
        fi
    done
    pgrep -fl 'amx daemon' | sed 's/^/process /' || true
}
operator_state > "$logs/operator-before.txt"

# Go's caches stay where they are (read and written as any build does); only
# what the pair installs and runs is confined to the world.
gopath="$(go env GOPATH)"; gomodcache="$(go env GOMODCACHE)"; gocache="$(go env GOCACHE)"
in_world() {
    env HOME="$home" TMPDIR="$world/tmp" XDG_STATE_HOME="$home/.local/state" \
        GOPATH="$gopath" GOMODCACHE="$gomodcache" GOCACHE="$gocache" \
        PATH="$home/.local/bin:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin" \
        PYTHONDONTWRITEBYTECODE=1 \
        "$@"
}

step() {
    local name="$1"; shift
    echo "== $name: $* ==" | tee -a "$logs/pair.log"
    if in_world "$@" > "$logs/$name.log" 2>&1; then
        echo "$name: exit 0" | tee -a "$logs/pair.log"
    else
        local code=$?
        echo "$name: exit $code" | tee -a "$logs/pair.log"
        status=1
    fi
}

{
    echo "world: $world"
    echo "agentmux: $checkout at $(git -C "$checkout" rev-parse HEAD) ($(git -C "$checkout" status --short | wc -l | tr -d ' ') uncommitted paths)"
    echo "asl source: $source_root at $(git -C "$source_root" rev-parse HEAD) ($(git -C "$source_root" status --short --ignored | wc -l | tr -d ' ') changed or ignored paths)"
} | tee "$logs/pair.log"

git clone --quiet --no-hardlinks "$source_root" "$world/asl"
echo "asl clone: $world/asl at $(git -C "$world/asl" rev-parse HEAD)" | tee -a "$logs/pair.log"
# A clean clone of a clean checkout at the same commit is the same tree; the
# tree hashes say so without walking either one.
source_tree="$(git -C "$source_root" rev-parse 'HEAD^{tree}')"
clone_tree="$(git -C "$world/asl" rev-parse 'HEAD^{tree}')"
if [ "$source_tree" = "$clone_tree" ] && [ -z "$(git -C "$world/asl" status --short)" ]; then
    echo "asl clone: same tree as the source ($clone_tree), clean" | tee -a "$logs/pair.log"
else
    echo "asl clone: DIFFERS from the source tree ($clone_tree vs $source_tree)" | tee -a "$logs/pair.log"
    status=1
fi

cd "$checkout"
step install just install
ln -s "$world/asl/bin/.local/bin/asl" "$home/.local/bin/asl"
{
    echo "installed amx: $(in_world "$home/.local/bin/amx" --version 2>&1)"
    echo "installed asl: $(in_world sh -c 'command -v asl') -> $(readlink -f "$home/.local/bin/asl")"
    echo "asl harness-compat: $(in_world asl harness-compat --json 2>&1 | head -c 400)"
} | tee -a "$logs/pair.log"

# The vendored corpus against the corpus the pinned source publishes.
step asl-contract-sync just asl-contract-sync "asl_root=$source_root"
# This checkout's bridge and daemon against the installed asl: the clone.
step asl-integration just asl-integration
# The installed amx and the installed asl against each other.
step overseer-integration just overseer-integration "asl_root=$world/asl"

# Everything the world started is stopped, then the world goes.
leftovers="$(pgrep -fl "$world" || true)"
if [ -n "$leftovers" ]; then
    echo "stopping world processes:" | tee -a "$logs/pair.log"
    echo "$leftovers" | tee -a "$logs/pair.log"
    pkill -f "$world" || true
    sleep 1
fi
if [ -n "$(git -C "$source_root" status --short --ignored)" ]; then
    echo "asl source: CHANGED by the run" | tee -a "$logs/pair.log"
    git -C "$source_root" status --short --ignored | tee -a "$logs/pair.log"
    status=1
else
    echo "asl source: unchanged (no modified, untracked or ignored path)" | tee -a "$logs/pair.log"
fi
chmod -R u+w "$world" 2>/dev/null || true
rm -rf "$world"
[ -e "$world" ] && { echo "world: NOT removed" | tee -a "$logs/pair.log"; status=1; } || echo "world: removed" | tee -a "$logs/pair.log"
operator_state > "$logs/operator-after.txt"
if diff "$logs/operator-before.txt" "$logs/operator-after.txt" > /dev/null; then
    echo "operator's amx, asl, config and daemon: unchanged" | tee -a "$logs/pair.log"
else
    echo "operator state DIFFERS (operator-before.txt, operator-after.txt)" | tee -a "$logs/pair.log"
    status=1
fi
echo "pair: $([ $status = 0 ] && echo ok || echo FAILED)" | tee -a "$logs/pair.log"
exit $status
