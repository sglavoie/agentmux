#!/bin/bash
# Installed on the MacBook as the world's `claude`: what AgentMux launches in
# a tmux pane for a session. It is a stand-in agent, not a harness: recovery
# and ownership are about the process a pane holds, whatever that process is.
#
# It reports itself to the world's daemon with one managed SessionStart hook,
# so its session is listed, and then counts: one line a second on its screen
# and in a log of its own, each with its PID, the count, and the clock. A
# count that carries on without starting over is the same process still
# running; a gap in the clock with no gap in the count is a machine that
# slept. It takes three commands on its terminal: `ask` sends a
# PermissionRequest hook, `exit N` ends it with status N, and anything else
# is echoed.
R=/tmp/amxs16
case "$1" in --version|-V|-v) echo "stand-in agent"; exit 0 ;; esac
project=$(basename "$(pwd -P)")
sid="s16-$project-$$"
hook() {
  printf '{"session_id":"%s","hook_event_name":"%s","cwd":"%s"%s}' "$sid" "$1" "$(pwd)" "$2" \
    | HOME=$R/home "$R/bin/amx" hook-indicator --harness claude "$1" >> "$R/logs/hooks.log" 2>&1
  echo "$project $1 runtime=${AMX_RUNTIME_SESSION_ID:-none} exit=$?" >> "$R/logs/hooks.log"
}
hook SessionStart ',"source":"startup"'
echo "stand-in agent $project pid $$ runtime ${AMX_RUNTIME_SESSION_ID:-none}"
n=0
while :; do
  if IFS= read -r -t 1 line; then
    case "$line" in
      ask) hook PermissionRequest ',"tool_name":"Bash"'; echo "asked" ;;
      "exit "*) echo "exiting ${line#exit }"; exit "${line#exit }" ;;
      *) echo "echo: $line" ;;
    esac
  else
    n=$((n+1))
    printf 'tick %d pid %d at %s\n' "$n" "$$" "$(date '+%H:%M:%S')"
    printf '%d %d %s\n' "$n" "$$" "$(date '+%s %H:%M:%S')" >> "$R/logs/ticks-$project.log"
  fi
done
