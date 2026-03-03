#!/usr/bin/env bash
# ccm.sh — umbrella library for ccm CLI modules.
# Sourced by bin/ccm. Do not execute directly.

[[ -n "${_CCM_LOADED:-}" ]] && return 0
_CCM_LOADED=1

_CCM_SCRIPT="$(readlink -f "${BASH_SOURCE[0]}")"
_CCM_DIR="$(cd "$(dirname "$_CCM_SCRIPT")" && pwd)"

# shellcheck disable=SC2034 # used by sourced modules
REPO_DIR="$(cd "$_CCM_DIR/.." && pwd)"

source "$_CCM_DIR/ccm/utils.sh"
source "$_CCM_DIR/ccm/config.sh"
source "$_CCM_DIR/ccm/notify.sh"
source "$_CCM_DIR/ccm/hooks.sh"
source "$_CCM_DIR/ccm/logs.sh"
source "$_CCM_DIR/ccm/help.sh"
source "$_CCM_DIR/ccm/main.sh"
