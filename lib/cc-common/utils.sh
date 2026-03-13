#!/usr/bin/env bash

[[ -n "${_CC_COMMON_UTILS_LOADED:-}" ]] && return 0
_CC_COMMON_UTILS_LOADED=1

# Return the modification time of a file as a Unix epoch timestamp.
# Uses the appropriate stat syntax for macOS (Darwin) vs Linux.
_file_mtime() {
	local file="$1"
	case "$(uname -s)" in
	Darwin) stat -f '%m' "$file" 2>/dev/null ;;
	*) stat -c '%Y' "$file" 2>/dev/null ;;
	esac
}

# Escape regex metacharacters in a string for use in kitty --match patterns.
escape_regex() {
	local s="$1"
	# Escape: . [ ] * + ? { } ( ) | ^ $ backslash
	s="${s//\\/\\\\}"
	s="${s//./\\.}"
	s="${s//\[/\\[}"
	s="${s//\]/\\]}"
	s="${s//\*/\\*}"
	s="${s//+/\\+}"
	s="${s//\?/\\?}"
	s="${s//\{/\\{}"
	s="${s//\}/\\}}"
	s="${s//\(/\\(}"
	s="${s//\)/\\)}"
	s="${s//|/\\|}"
	s="${s//^/\\^}"
	s="${s//\$/\\\$}"
	printf '%s' "$s"
}

_trim() {
	local s="$1"
	s="${s#"${s%%[![:space:]]*}"}"
	s="${s%"${s##*[![:space:]]}"}"
	printf '%s' "$s"
}
