#!/bin/sh
# Observer tmux: a screen recorder and keyboard for the utility views, on its
# own socket. It is not AgentMux's private server and hosts no agent.
exec /opt/homebrew/bin/tmux -S /tmp/amxs14/obs.sock -f /dev/null "$@"
