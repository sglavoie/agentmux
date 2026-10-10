#!/bin/sh
# Observer tmux: a screen recorder for the surface under test, on its own socket.
exec /opt/homebrew/bin/tmux -S /tmp/amxs11/obs.sock -f /dev/null "$@"
