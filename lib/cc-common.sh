#!/usr/bin/env bash
# cc-common.sh — shared infrastructure for cc-manager shell scripts.
# Sourced by cc-launch and cc-status. Do not execute directly.

[[ -n "${_CC_COMMON_LOADED:-}" ]] && return 0
_CC_COMMON_LOADED=1

CCM_DEBUG="${CCM_DEBUG:-0}"
ccm_debug() { if [[ "$CCM_DEBUG" == "1" ]]; then echo "[DEBUG] $*" >&2; fi; }

_CC_COMMON_SCRIPT="$(readlink -f "${BASH_SOURCE[0]}")"
_CC_COMMON_DIR="$(cd "$(dirname "$_CC_COMMON_SCRIPT")" && pwd)"

_require_python3() {
	if ! command -v python3 >/dev/null 2>&1; then
		echo "Error: python3 is required but not found." >&2
		exit 1
	fi
}

source "$_CC_COMMON_DIR/cc-common/utils.sh"
source "$_CC_COMMON_DIR/cc-common/data.sh"
source "$_CC_COMMON_DIR/cc-common/sockets.sh"
source "$_CC_COMMON_DIR/cc-common/ui.sh"
source "$_CC_COMMON_DIR/cc-common/ansi.sh"
source "$_CC_COMMON_DIR/cc-common/config.sh"
source "$_CC_COMMON_DIR/cc-common/accounts.sh"
source "$_CC_COMMON_DIR/cc-common/defaults.sh"
source "$_CC_COMMON_DIR/cc-common/palette.sh"
source "$_CC_COMMON_DIR/cc-common/sorting.sh"
