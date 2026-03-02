#!/usr/bin/env bash
# shellcheck shell=bash
# shellcheck disable=SC2034

[[ -n "${_CC_AUDIT_CORE_LOADED:-}" ]] && return 0
_CC_AUDIT_CORE_LOADED=1

# ---------------------------------------------------------------------------
# Path resolution
# ---------------------------------------------------------------------------
resolve_repo_dir() {
    local source_path source_dir
    source_path="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
    source_dir="$source_path/../.."
    cd "$source_dir" && pwd
}

REPO_DIR="$(resolve_repo_dir)"
MANIFEST="$REPO_DIR/data/audit-manifest.json"
CACHE_DIR="/tmp/ccm-audit-cache"
AI_RESULTS_DIR="$CACHE_DIR/ai-results"
AI_TIMEOUT=300
REPORT_FILE="$REPO_DIR/audit-report.txt"

# ---------------------------------------------------------------------------
# Color helpers
# ---------------------------------------------------------------------------
if [[ -t 1 ]]; then
    _C_RESET=$'\033[0m'
    _C_BOLD=$'\033[1m'
    _C_GREEN=$'\033[32m'
    _C_YELLOW=$'\033[33m'
    _C_RED=$'\033[31m'
    _C_CYAN=$'\033[36m'
    _C_DIM=$'\033[2m'
else
    _C_RESET="" _C_BOLD="" _C_GREEN="" _C_YELLOW="" _C_RED="" _C_CYAN="" _C_DIM=""
fi

# ---------------------------------------------------------------------------
# Defaults
# ---------------------------------------------------------------------------
opt_no_cache=false
opt_skip_ai=false
opt_section=""
opt_json=false
opt_quiet=false
has_claude=false
has_lynx=false

# ---------------------------------------------------------------------------
# Usage
# ---------------------------------------------------------------------------
usage() {
    cat <<'HELP'
Usage: cc-audit [OPTIONS]

Check upstream feature compatibility by comparing cc-manager's known features
against current Claude Code and Kitty documentation.

Options:
    --no-cache          Force re-fetch of upstream docs (ignore 24h cache)
    --skip-ai           Skip AI analysis, show manifest summary only
    --section NAME      Limit to a section (claude-hooks, claude-cli,
                        kitty-remote, kitty-launch, kitty-match)
    --json              Output as JSON instead of formatted text
    --quiet             Suppress "up to date" sections, show only findings
    --update-manifest   Open the manifest in $EDITOR for manual editing
    --help              Show this help message
HELP
}

# ---------------------------------------------------------------------------
# Validate prerequisites
# ---------------------------------------------------------------------------
validate() {
    local missing=false

    if ! command -v jq >/dev/null 2>&1; then
        echo "${_C_RED}Error: jq is required but not found.${_C_RESET}" >&2
        missing=true
    fi

    if ! command -v curl >/dev/null 2>&1; then
        echo "${_C_RED}Error: curl is required but not found.${_C_RESET}" >&2
        missing=true
    fi

    if [[ "$missing" == "true" ]]; then
        exit 1
    fi

    if command -v claude >/dev/null 2>&1; then
        has_claude=true
    fi

    if command -v lynx >/dev/null 2>&1; then
        has_lynx=true
    fi

    if [[ ! -f "$MANIFEST" ]]; then
        echo "${_C_RED}Error: Manifest not found at $MANIFEST${_C_RESET}" >&2
        exit 1
    fi
}

# Strip ANSI escape codes from stdin.
_strip_ansi() {
    sed $'s/\033\[[0-9;]*m//g'
}

# Extract JSON from claude -p output, stripping markdown fences if present.
_extract_json() {
    local raw
    raw="$(cat)"
    raw="$(printf '%s\n' "$raw" | sed '/^```.*$/d')"
    raw="$(printf '%s\n' "$raw" | sed '/^[[:space:]]*$/d')"
    printf '%s\n' "$raw" | jq '.' 2>/dev/null
}
