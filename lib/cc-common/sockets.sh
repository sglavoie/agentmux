#!/usr/bin/env bash

[[ -n "${_CC_COMMON_SOCKETS_LOADED:-}" ]] && return 0
_CC_COMMON_SOCKETS_LOADED=1

discover_socket() {
    if [[ -n "${KITTY_LISTEN_ON:-}" ]]; then
        echo "$KITTY_LISTEN_ON"
        return
    fi

    local latest=""
    latest="$(ls -t /tmp/kitty-* 2>/dev/null | head -n1)" || true
    if [[ -n "$latest" ]]; then
        echo "unix:$latest"
        return
    fi

    echo "Error: No kitty socket found. Set KITTY_LISTEN_ON or ensure kitty is running with remote control enabled." >&2
    exit 1
}

_INSTANCE_SOCKETS=()
_INSTANCE_LABELS=()

discover_all_sockets() {
    _INSTANCE_SOCKETS=()
    _INSTANCE_LABELS=()
    local -a candidates=()
    local -a seen=()

    if [[ -n "${KITTY_LISTEN_ON:-}" ]]; then
        candidates+=("$KITTY_LISTEN_ON")
        seen+=("$KITTY_LISTEN_ON")
    fi

    local sock_path
    # shellcheck disable=SC2012,SC2045 # intentional: need newest socket by mtime
    for sock_path in $(ls -t /tmp/kitty-* 2>/dev/null); do
        local sock="unix:$sock_path"
        local already=false
        local seen_sock
        for seen_sock in "${seen[@]+"${seen[@]}"}"; do
            if [[ "$seen_sock" == "$sock" ]]; then
                already=true
                break
            fi
        done
        if [[ "$already" == "false" ]]; then
            candidates+=("$sock")
            seen+=("$sock")
        fi
    done

    local candidate
    for candidate in "${candidates[@]+"${candidates[@]}"}"; do
        if kitty @ --to "$candidate" ls >/dev/null 2>&1; then
            _INSTANCE_SOCKETS+=("$candidate")
            local bare="${candidate#unix:}"
            local base
            base="$(basename "$bare")"
            local label="$candidate"
            if [[ "$base" =~ ^kitty-([0-9]+) ]]; then
                label="K:${BASH_REMATCH[1]}"
            fi
            _INSTANCE_LABELS+=("$label")
        fi
    done
}
