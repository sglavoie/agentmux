#!/usr/bin/env bash

[[ -n "${_CC_COMMON_UTILS_LOADED:-}" ]] && return 0
_CC_COMMON_UTILS_LOADED=1

_trim() {
	local s="$1"
	s="${s#"${s%%[![:space:]]*}"}"
	s="${s%"${s##*[![:space:]]}"}"
	printf '%s' "$s"
}
