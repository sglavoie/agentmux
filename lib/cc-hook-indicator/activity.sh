#!/usr/bin/env bash

# shellcheck disable=SC2154

[[ -n "${_CC_HOOK_INDICATOR_ACTIVITY_LOADED:-}" ]] && return 0
_CC_HOOK_INDICATOR_ACTIVITY_LOADED=1

# Activity counts file format (12 lines, one value per line):
#   1: first_event_ts   — timestamp of first event in this session
#   2: last_event_ts    — timestamp of most recent event
#   3: last_event_type  — type of most recent event (prompt/tool/etc.)
#   4: prompt_count     — number of prompt events
#   5: tool_use_count   — number of tool use events
#   6: tool_failure_count — number of tool failure events
#   7: permission_count — number of permission events
#   8: stop_count       — number of stop events
#   9: last_attention_ts — timestamp of last attention-requiring event
#  10: task_completes   — number of task completion events
#  11: active_subagents — currently active subagent count
#  12: total_subagents  — total subagents spawned this session

cchi_activity_file() {
	printf '%s/ccm-activity-%s.counts' "$_CCM_TMPDIR" "$tab_id"
}

cchi_ensure_int() {
	[[ "${1:-}" =~ ^[0-9]+$ ]] && echo "$1" || echo "${2:-0}"
}

cchi_activity_event_kind() {
	if [[ -n "$EVENT_KIND" ]]; then
		echo "$EVENT_KIND"
		return 0
	fi

	case "$ACTION" in
	active) echo "prompt" ;;
	failure) echo "failure" ;;
	permission) echo "permission" ;;
	stop) echo "stop" ;;
	*) echo "" ;;
	esac
}

cchi_update_activity() {
	local activity_file now event_kind
	activity_file="$(cchi_activity_file)"
	now=$(date +%s)
	event_kind="$(cchi_activity_event_kind)"

	[[ -n "$event_kind" ]] || return 0

	local first_ts="$now" last_ts="$now" last_type="$event_kind"
	local prompts=0 tools=0 failures=0 permissions=0 stops=0 attention_ts=0 task_completes=0
	local active_subagents=0 total_subagents=0

	if [[ -f "$activity_file" ]]; then
		local line_count
		line_count=$(wc -l <"$activity_file" 2>/dev/null || echo 0)
		line_count="${line_count// /}"
		if [[ "$line_count" -ne 12 ]]; then
			ccm_debug "Corrupt counts file ($activity_file): expected 12 lines, got $line_count. Resetting."
			rm -f "$activity_file"
		else
			local i=0 line
			while IFS= read -r line; do
				case $i in
				0) first_ts="$line" ;;
				1) last_ts="$line" ;;
				2) last_type="$line" ;;
				3) prompts="$line" ;;
				4) tools="$line" ;;
				5) failures="$line" ;;
				6) permissions="$line" ;;
				7) stops="$line" ;;
				8) attention_ts="$line" ;;
				9) task_completes="$line" ;;
				10) active_subagents="$line" ;;
				11) total_subagents="$line" ;;
				esac
				i=$((i + 1))
			done <"$activity_file"
		fi

		first_ts="$(cchi_ensure_int "$first_ts" "$now")"
		last_ts="$(cchi_ensure_int "$last_ts" "$now")"
		prompts="$(cchi_ensure_int "$prompts")"
		tools="$(cchi_ensure_int "$tools")"
		failures="$(cchi_ensure_int "$failures")"
		permissions="$(cchi_ensure_int "$permissions")"
		stops="$(cchi_ensure_int "$stops")"
		attention_ts="$(cchi_ensure_int "$attention_ts")"
		task_completes="$(cchi_ensure_int "$task_completes")"
		active_subagents="$(cchi_ensure_int "$active_subagents")"
		total_subagents="$(cchi_ensure_int "$total_subagents")"
	fi

	_prev_last_ts="$last_ts"
	_prev_last_type="$last_type"

	last_ts="$now"
	last_type="$event_kind"

	case "$event_kind" in
	prompt) prompts=$((prompts + 1)) ;;
	tool) tools=$((tools + 1)) ;;
	failure)
		failures=$((failures + 1))
		attention_ts="$now"
		;;
	permission)
		permissions=$((permissions + 1))
		attention_ts="$now"
		;;
	stop) stops=$((stops + 1)) ;;
	task_complete) task_completes=$((task_completes + 1)) ;;
	subagent_start)
		active_subagents=$((active_subagents + 1))
		total_subagents=$((total_subagents + 1))
		;;
	subagent_stop) active_subagents=$((active_subagents > 0 ? active_subagents - 1 : 0)) ;;
	esac

	local tmpfile="${activity_file}.tmp.$$"
	printf '%s\n%s\n%s\n%s\n%s\n%s\n%s\n%s\n%s\n%s\n%s\n%s\n' \
		"$first_ts" "$last_ts" "$last_type" \
		"$prompts" "$tools" "$failures" "$permissions" "$stops" "$attention_ts" \
		"$task_completes" "$active_subagents" "$total_subagents" \
		>"$tmpfile" && mv "$tmpfile" "$activity_file"
}
