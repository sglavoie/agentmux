#!/usr/bin/env bash

[[ -n "${_CC_COMMON_SOCKETS_LOADED:-}" ]] && return 0
_CC_COMMON_SOCKETS_LOADED=1

discover_socket() {
	if [[ -n "${KITTY_LISTEN_ON:-}" ]]; then
		ccm_debug "Socket from KITTY_LISTEN_ON: $KITTY_LISTEN_ON"
		echo "$KITTY_LISTEN_ON"
		return
	fi

	local latest=""
	local candidate mtime newest_mtime=0
	for candidate in "${TMPDIR:-/tmp}"/kitty-*; do
		[[ -e "$candidate" ]] || continue
		if ! mtime="$(_file_mtime "$candidate")"; then
			continue
		fi
		if ((mtime > newest_mtime)); then
			newest_mtime=$mtime
			latest="$candidate"
		fi
	done

	if [[ -n "$latest" ]]; then
		ccm_debug "Socket from file: unix:$latest"
		echo "unix:$latest"
		return
	fi

	err "No kitty socket found."
	echo "  Possible fixes:" >&2
	echo "    1. Check that Kitty is running (pgrep -x kitty)" >&2
	echo "    2. Add 'allow_remote_control socket-only' to kitty.conf" >&2
	echo "    3. Add 'listen_on unix:/tmp/kitty-{kitty_pid}' to kitty.conf" >&2
	echo "    4. Or set the KITTY_LISTEN_ON env var to point to an existing socket" >&2
	return 1
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

	# Collect socket paths with their modification times, then sort by most recent.
	# Uses a simple glob + array sort instead of a fragile multi-stage pipe chain.
	local -a sock_entries=()
	local sock_path
	for sock_path in "${TMPDIR:-/tmp}"/kitty-*; do
		[[ -e "$sock_path" ]] || continue
		local mtime
		mtime="$(_file_mtime "$sock_path")" || continue
		sock_entries+=("$mtime"$'\t'"$sock_path")
	done

	while IFS=$'\t' read -r _ sock_path; do
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
	done < <(printf '%s\n' ${sock_entries[@]+"${sock_entries[@]}"} | sort -rn)

	local candidate
	for candidate in "${candidates[@]+"${candidates[@]}"}"; do
		local probe_err
		if probe_err="$(kitty @ --to "$candidate" ls 2>&1 >/dev/null)"; then
			_INSTANCE_SOCKETS+=("$candidate")
			local bare="${candidate#unix:}"
			local base
			base="$(basename "$bare")"
			local label="$candidate"
			if [[ "$base" =~ ^kitty-([0-9]+) ]]; then
				label="K:${BASH_REMATCH[1]}"
			fi
			_INSTANCE_LABELS+=("$label")
		else
			ccm_debug "socket probe failed for $candidate: $probe_err"
		fi
	done

	if [[ ${#_INSTANCE_SOCKETS[@]} -eq 0 ]]; then
		if [[ ${#candidates[@]} -gt 0 ]]; then
			warn "Found ${#candidates[@]} kitty socket(s) but none responded."
			echo "  Check that 'allow_remote_control socket-only' is set in kitty.conf." >&2
			echo "  If Kitty was recently restarted, stale sockets may linger in ${TMPDIR:-/tmp}/." >&2
		else
			warn "No kitty sockets found."
			echo "  Check that Kitty is running (pgrep -x kitty) and that" >&2
			echo "  'listen_on unix:/tmp/kitty-{kitty_pid}' is set in kitty.conf." >&2
		fi
	fi
	ccm_debug "Discovered ${#_INSTANCE_SOCKETS[@]} active socket(s)"
}
