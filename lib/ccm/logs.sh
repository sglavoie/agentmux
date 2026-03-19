#!/usr/bin/env bash

[[ -n "${_CCM_LOGS_LOADED:-}" ]] && return 0
_CCM_LOGS_LOADED=1

_resolve_log_dir() {
	echo "${CC_MANAGER_LOGS:-${XDG_STATE_HOME:-$HOME/.local/state}/cc-manager/activity}"
}

cmd_logs_list() {
	local log_dir
	log_dir="$(_resolve_log_dir)"

	if [[ ! -d "$log_dir" ]]; then
		echo "No log directory found at $log_dir"
		return 0
	fi

	local files
	files=("$log_dir"/*.jsonl)
	# Handle no-match: glob expands literally when nothing matches.
	if [[ ${#files[@]} -eq 0 || ! -e "${files[0]}" ]]; then
		echo "No log files found in $log_dir"
		return 0
	fi

	# Sort by last-modified descending.
	local sorted
	sorted="$(ls -1t "$log_dir"/*.jsonl 2>/dev/null)" || true
	if [[ -z "$sorted" ]]; then
		echo "No log files found in $log_dir"
		return 0
	fi

	printf "%-40s %8s %7s  %s\n" "Session ID" "Size" "Events" "Working Directory"
	printf "%-40s %8s %7s  %s\n" "----------" "----" "------" "-----------------"

	while IFS= read -r f; do
		local sid size events cwd_val
		sid="$(basename "$f" .jsonl)"
		size="$(wc -c <"$f" | tr -d ' ')"
		events="$(wc -l <"$f" | tr -d ' ')"
		# Extract cwd from first entry.
		cwd_val="$(head -1 "$f" | jq -r '.cwd // "-"' 2>/dev/null)" || cwd_val="-"

		# Human-readable size.
		local hr_size
		if [[ $size -ge 1048576 ]]; then
			hr_size="$(awk "BEGIN {printf \"%.1fM\", $size/1048576}")"
		elif [[ $size -ge 1024 ]]; then
			hr_size="$(awk "BEGIN {printf \"%.1fK\", $size/1024}")"
		else
			hr_size="${size}B"
		fi

		printf "%-40s %8s %7s  %s\n" "$sid" "$hr_size" "$events" "$cwd_val"
	done <<<"$sorted"
}

cmd_logs_show() {
	local session_id="" pretty=false tail_n=""

	while [[ $# -gt 0 ]]; do
		case "$1" in
		--pretty)
			pretty=true
			shift
			;;
		--tail)
			tail_n="${2:?--tail requires a number}"
			shift 2
			;;
		-*)
			err "Unknown flag '$1'. Run 'ccm help logs' for usage."
			return 1
			;;
		*)
			session_id="$1"
			shift
			;;
		esac
	done

	if [[ -z "$session_id" ]]; then
		echo "Usage: ccm logs show SESSION_ID [--pretty] [--tail N]" >&2
		return 1
	fi

	local log_dir log_file
	log_dir="$(_resolve_log_dir)"
	log_file="$log_dir/$session_id.jsonl"

	if [[ ! -f "$log_file" ]]; then
		err "Log file not found: '$log_file'."
		return 1
	fi

	local content
	if [[ -n "$tail_n" ]]; then
		content="$(tail -n "$tail_n" "$log_file")"
	else
		content="$(cat "$log_file")"
	fi

	if [[ "$pretty" == true ]]; then
		echo "$content" | jq .
	else
		echo "$content"
	fi
}

cmd_logs_summary() {
	local session_id="${1:-}"
	if [[ -z "$session_id" ]]; then
		echo "Usage: ccm logs summary SESSION_ID" >&2
		return 1
	fi

	local log_dir log_file
	log_dir="$(_resolve_log_dir)"
	log_file="$log_dir/$session_id.jsonl"

	if [[ ! -f "$log_file" ]]; then
		err "Log file not found: '$log_file'."
		return 1
	fi

	local first_ts
	first_ts="$(head -1 "$log_file" | jq -r '.ts' 2>/dev/null)" || first_ts=""
	if [[ -n "$first_ts" ]]; then
		local start_date
		start_date="$(date -r "$first_ts" '+%Y-%m-%d %H:%M' 2>/dev/null || date -d "@$first_ts" '+%Y-%m-%d %H:%M' 2>/dev/null || echo "$first_ts")"
		printf "Session:   %s (started %s)\n" "$session_id" "$start_date"
	else
		printf "Session:   %s\n" "$session_id"
	fi

	# Compute the rest of the summary in a single jq invocation.
	jq -rs '
		(map(select(.event == "tool")) | length) as $tool_count |
		(map(select(.event == "stop")) | length) as $stop_count |
		(map(select(.event == "session_end")) | length) as $end_count |
		length as $total |
		(map(.ts) | min) as $first_ts |
		(map(.ts) | max) as $last_ts |
		($last_ts - $first_ts) as $duration_secs |
		(map(select(.event == "tool") | .tool) | group_by(.) | map({tool: .[0], count: length}) | sort_by(-.count) | .[:10]) as $top_tools |
		"Events:    \($total) total (\($tool_count) tool, \($stop_count) stop, \($end_count) session_end)\n" +
		"Duration:  \($duration_secs / 3600 | floor)h \(($duration_secs % 3600) / 60 | floor)m \($duration_secs % 60)s\n" +
		"Ended:     \(if $end_count > 0 then "yes" else "no" end)\n" +
		"\nTop tools:\n" +
		($top_tools | map("    \(.count)x  \(.tool)") | join("\n"))
	' "$log_file"
}

cmd_logs_clean() {
	local days=30 dry_run=false

	while [[ $# -gt 0 ]]; do
		case "$1" in
		--older-than)
			days="${2:?--older-than requires a number}"
			shift 2
			;;
		--dry-run)
			dry_run=true
			shift
			;;
		-*)
			err "Unknown flag '$1'. Run 'ccm help logs' for usage."
			return 1
			;;
		*)
			err "Unexpected argument '$1'. Run 'ccm help logs' for usage."
			return 1
			;;
		esac
	done

	local log_dir
	log_dir="$(_resolve_log_dir)"

	if [[ ! -d "$log_dir" ]]; then
		echo "No log directory found at $log_dir"
		return 0
	fi

	local count=0
	local total_size=0
	while IFS= read -r f; do
		local size
		size="$(wc -c <"$f" | tr -d ' ')"
		total_size=$((total_size + size))
		count=$((count + 1))
		if [[ "$dry_run" == true ]]; then
			echo "Would delete: $(basename "$f" .jsonl) ($(wc -l <"$f" | tr -d ' ') events)"
		else
			rm "$f"
		fi
	done < <(find "$log_dir" -name "*.jsonl" -mtime "+$days" -type f 2>/dev/null)

	if [[ $count -eq 0 ]]; then
		echo "No log files older than $days days."
	elif [[ "$dry_run" == true ]]; then
		echo "Would delete $count file(s)."
	else
		echo "Deleted $count file(s)."
	fi
}

cmd_logs() {
	case "${1:-}" in
	list | ls)
		cmd_logs_list
		;;
	show)
		shift
		cmd_logs_show "$@"
		;;
	summary)
		shift
		cmd_logs_summary "$@"
		;;
	clean)
		shift
		cmd_logs_clean "$@"
		;;
	dir)
		_resolve_log_dir
		;;
	"" | --help | -h | help)
		echo "Usage: ccm logs <list|show|summary|clean|dir>" >&2
		echo "" >&2
		echo "Subcommands:" >&2
		echo "    list              List all session log files" >&2
		echo "    show SESSION_ID   Show log entries for a session" >&2
		echo "    summary SESSION_ID  Print an aggregate summary" >&2
		echo "    clean             Delete old log files" >&2
		echo "    dir               Print the log directory path" >&2
		echo "" >&2
		echo "Run 'ccm help logs' for detailed usage." >&2
		return 1
		;;
	*)
		err "Unknown logs subcommand '$1'. Use: list, show, summary, clean, dir."
		return 1
		;;
	esac
}
