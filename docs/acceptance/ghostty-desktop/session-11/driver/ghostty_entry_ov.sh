#!/bin/sh
GHOSTTY_TERM="$TERM"
. /tmp/amxs11/env.sh
export TERM="$GHOSTTY_TERM"
cd /tmp/amxs11
exec /tmp/amxs11/obs.sh new-session -s dash "amx overview; echo SURFACE_EXIT=\$?; sleep 3600"
