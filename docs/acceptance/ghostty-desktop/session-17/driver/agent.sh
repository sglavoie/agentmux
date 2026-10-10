#!/bin/bash
# Installed on the MacBook as the world's `claude`: what AgentMux launches in
# a tmux pane for a session. It is a stand-in agent, not a harness: surviving
# an update is about the process a pane holds, whatever that process is.
#
# It reports itself to the world's daemon with one managed SessionStart hook,
# so its session is listed, and then counts: one line a second on its screen
# and in a log of its own, each with the count, its PID, and the clock. A
# count that carries on without starting over or skipping is the same process
# still running, undisturbed.
R=/tmp/amxs17
case "$1" in --version|-V|-v) echo "stand-in agent"; exit 0 ;; esac
project=$(basename "$(pwd -P)")
sid="s17-$project-$$"
printf '{"session_id":"%s","hook_event_name":"SessionStart","cwd":"%s","source":"startup"}' "$sid" "$(pwd)" \
  | HOME=$R/home "$R/Applications/Agent Mux.app/Contents/Resources/bin/amx" hook-indicator --harness claude SessionStart >> "$R/logs/hooks.log" 2>&1
echo "$project SessionStart runtime=${AMX_RUNTIME_SESSION_ID:-none} exit=$?" >> "$R/logs/hooks.log"
echo "stand-in agent $project pid $$ runtime ${AMX_RUNTIME_SESSION_ID:-none}"
n=0
while :; do
  sleep 1
  n=$((n+1))
  printf 'tick %d pid %d at %s\n' "$n" "$$" "$(date '+%H:%M:%S')"
  printf '%d %d %s\n' "$n" "$$" "$(date '+%s %H:%M:%S')" >> "$R/logs/ticks-$project.log"
done
