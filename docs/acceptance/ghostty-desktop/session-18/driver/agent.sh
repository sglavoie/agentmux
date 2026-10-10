#!/bin/bash
# Installed in the world as its `claude`: what AgentMux launches in a tmux
# pane for a session. A stand-in agent, not a harness: the cost of watching a
# fleet is about how many panes there are and how much they print, whatever
# prints it.
#
# It reports itself with two managed hooks, SessionStart and UserPromptSubmit,
# the second carrying a prompt and a credential nobody may ever find in a
# diagnostic (the canaries run.sh greps for), shows the same canaries on its
# screen, and then does what the world's workload file says: nothing, or
# print as fast as the terminal takes it.
R=/tmp/amxs18
case "$1" in --version|-V|-v) echo "2.1.0 (Claude Code)"; exit 0 ;; esac
project=$(basename "$(pwd -P)")
sid="s18-$project-$$"
PROMPT='PROMPT-CANARY-s18 rewrite the billing service'
SECRET='sk-s18canary0123456789abcdefghij'
hook() { printf '%s' "$2" | "$R/app/bin/amx" hook-indicator --harness claude "$1" >> "$R/logs/hooks.log" 2>&1; echo "$project $1 exit=$?" >> "$R/logs/hooks.log"; }
hook SessionStart "{\"session_id\":\"$sid\",\"hook_event_name\":\"SessionStart\",\"cwd\":\"$(pwd)\",\"source\":\"startup\"}"
hook UserPromptSubmit "{\"session_id\":\"$sid\",\"hook_event_name\":\"UserPromptSubmit\",\"cwd\":\"$(pwd)\",\"prompt\":\"$PROMPT with key $SECRET\"}"
echo "stand-in agent $project pid $$ runtime ${AMX_RUNTIME_SESSION_ID:-none}"
echo "> $PROMPT"
echo "export ANTHROPIC_API_KEY=$SECRET"
if [ "$(cat $R/workload 2>/dev/null)" = output ]; then
  # The canaries are in every line, so they are on the screen whenever it is read.
  exec yes "agent output: $PROMPT; ANTHROPIC_API_KEY=$SECRET"
fi
exec sleep 100000
