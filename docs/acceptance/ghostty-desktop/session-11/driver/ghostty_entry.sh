#!/bin/sh
# Runs inside the stock Ghostty window: the AgentMux dashboard, hosted by the
# observer tmux so its screen can be read and its keyboard driven.
GHOSTTY_TERM="$TERM"
. /tmp/amxs11/env.sh
export TERM="${GHOSTTY_TERM:-xterm-ghostty}"
env | grep -E '^(TERM|TERM_PROGRAM|TERM_PROGRAM_VERSION|GHOSTTY_[A-Z_]*|KITTY_[A-Z_]*)=' > /tmp/amxs11/ghostty_env.txt
cd /tmp/amxs11
exec /tmp/amxs11/obs.sh new-session -s dash "amx; echo SURFACE_EXIT=\$?; sleep 3600"
