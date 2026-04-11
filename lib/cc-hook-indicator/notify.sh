#!/usr/bin/env bash

# shellcheck disable=SC2154

[[ -n "${_CC_HOOK_INDICATOR_NOTIFY_LOADED:-}" ]] && return 0
_CC_HOOK_INDICATOR_NOTIFY_LOADED=1

_NOTIFY_COOLDOWN=10

cchi_maybe_notify() {
    local enabled="$1" icon="$2" subtitle="$3" session_label="$4"
    [[ "$enabled" == "on" ]] || return 0
    command -v alerter >/dev/null 2>&1 || return 0

    local cooldown_file="${_CCM_TMPDIR}/ccm-notify-${ACTION}-${tab_id}"
    if [[ -f "$cooldown_file" ]]; then
        local now last_time
        now=$(date +%s)
        last_time=$(_file_mtime "$cooldown_file" || echo 0)
        if ((now - last_time < _NOTIFY_COOLDOWN)); then
            return 0
        fi
    fi
    touch "$cooldown_file"

    (
        result=$(alerter --title "${session_label}" --subtitle "${icon} ${subtitle}" \
            --message "needs attention" \
            --group "ccm-${ACTION}-${tab_id}" --timeout 10 2>/dev/null)
        if [[ "$result" != "@TIMEOUT" && "$result" != "@CLOSED" ]]; then
            kitty @ ${TO_ARGS[@]+"${TO_ARGS[@]}"} focus-tab --match "id:${tab_id}" 2>/dev/null
        fi
    ) &
}

cchi_is_post_permission() {
    [[ "$ACTION" == "stop" ]] || return 1
    [[ "$_prev_last_type" == "permission" ]] || return 1
    [[ "$_prev_last_ts" -gt 0 ]] || return 1
    local now elapsed
    now=$(date +%s)
    elapsed=$((now - _prev_last_ts))
    ((elapsed < 5))
}

cchi_is_too_recent() {
    local min_secs="${_DEFAULT_NOTIFY_MIN_SECS:-0}"
    [[ "$min_secs" -gt 0 ]] || return 1
    [[ "$_prev_last_ts" -gt 0 ]] || return 1

    local now elapsed
    now=$(date +%s)
    elapsed=$((now - _prev_last_ts))
    ((elapsed < min_secs))
}

cchi_matches_ignore_pattern() {
    local patterns="${_DEFAULT_NOTIFY_IGNORE_PATTERNS:-}"
    [[ -n "$patterns" ]] || return 1

    local attempt=0
    local max_attempts=5
    local initial_delay=0.3
    local retry_delay=0.5
    local screen_text
    local IFS='|'

    while [[ $attempt -lt $max_attempts ]]; do
        if [[ $attempt -eq 0 ]]; then
            sleep "$initial_delay"
        else
            sleep "$retry_delay"
        fi

        screen_text=$(kitten @ ${TO_ARGS[@]+"${TO_ARGS[@]}"} get-text --extent screen --match "id:$KITTY_WINDOW_ID" 2>/dev/null) || {
            attempt=$((attempt + 1))
            continue
        }

        local pattern
        for pattern in $patterns; do
            [[ -n "$pattern" ]] || continue
            if echo "$screen_text" | grep -qE "$pattern"; then
                return 0
            fi
        done

        attempt=$((attempt + 1))
    done

    return 1
}

cchi_should_suppress_notify() {
    [[ "$_PIPE_MODE" == true ]] && return 0
    [[ "$_MUTED" == true ]] && return 0
    cchi_is_post_permission && return 0
    cchi_is_too_recent && return 0
    cchi_matches_ignore_pattern && return 0
    return 1
}

cchi_is_pipe_mode() {
    local pid="$PPID"
    local depth=0
    while [[ "$depth" -lt 4 && "$pid" -gt 1 ]]; do
        local cmd_line
        cmd_line=$(ps -o args= -p "$pid" 2>/dev/null) || return 1
        if echo "$cmd_line" | grep -qE '(^|/)claude[[:space:]]' &&
            echo "$cmd_line" | grep -qE '(^|[[:space:]])(-p|--print)([[:space:]]|$)'; then
            return 0
        fi
        pid=$(ps -o ppid= -p "$pid" 2>/dev/null | tr -d ' ') || return 1
        depth=$((depth + 1))
    done
    return 1
}

cchi_send_action_notification() {
    case "$ACTION" in
    failure)
        cchi_should_suppress_notify || cchi_maybe_notify "$_DEFAULT_NOTIFY_ERROR" "$_ICON_ERROR" "Error" "$remaining"
        ;;
    permission)
        cchi_should_suppress_notify || cchi_maybe_notify "$_DEFAULT_NOTIFY_PERMISSION" "$_ICON_KEY" "Permission Required" "$remaining"
        ;;
    stop)
        cchi_should_suppress_notify || cchi_maybe_notify "$_DEFAULT_NOTIFY_STOP" "$_ICON_STOP" "Stopped" "$remaining"
        ;;
    esac
}
