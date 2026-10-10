#!/bin/sh
# Runs inside the stock Ghostty window with nothing in between: the dashboard
# draws straight to Ghostty, and script(1) keeps a byte record of what it drew.
GHOSTTY_TERM="$TERM"
. /tmp/amxs11/env.sh
export TERM="$GHOSTTY_TERM"
cd /tmp/amxs11
tty > /tmp/amxs11/ghostty_direct_tty.txt
exec script -q /tmp/amxs11/ghostty-direct.typescript amx
