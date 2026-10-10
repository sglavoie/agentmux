#!/bin/sh
# Observer tmux on the MacBook: a screen recorder for the utility views, on
# its own socket. It is not AgentMux's private server and hosts no agent.
exec /tmp/amxs16/tmux/bin/tmux -S /tmp/amxs16/obs.sock -f /dev/null "$@"
