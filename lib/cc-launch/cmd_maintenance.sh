#!/usr/bin/env bash

[[ -n "${_CC_LAUNCH_CMD_MAINTENANCE_LOADED:-}" ]] && return 0
_CC_LAUNCH_CMD_MAINTENANCE_LOADED=1

_CC_LAUNCH_CMD_MAINTENANCE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

source "$_CC_LAUNCH_CMD_MAINTENANCE_DIR/cmd_maintenance_list.sh"
source "$_CC_LAUNCH_CMD_MAINTENANCE_DIR/cmd_maintenance_cleanup.sh"
source "$_CC_LAUNCH_CMD_MAINTENANCE_DIR/cmd_maintenance_save.sh"
