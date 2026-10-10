#!/bin/sh
# Installed as the world's `claude`, `codex`, and `copilot`: what AgentMux
# launches in a tmux pane for a session of that harness.
#
# It first reports the session to the world's daemon with one managed
# SessionStart hook, so the session is listed in the app's sidebar. Then it
# becomes the operator's REAL harness, with the operator's own home,
# credentials, and PATH (recorded by setup.sh), and with the AgentMux session
# identity removed: the real harness's own hooks therefore reach no daemon,
# neither the world's nor the operator's. Everything typed, pasted, clicked,
# and scrolled in the acceptance run reaches that real program.
#
# In the `history` project it is instead a plain line printer that stays on
# the main screen, so its output scrolls into tmux's history.
R=/tmp/amxs15
name=$(basename "$0")
real=$(cat "$R/real/$name")
case "$1" in --version|-V|-v|--help|-h) exec "$real" "$@" ;; esac
sid="s15-$name-$$"
hook() {
  printf '{"session_id":"%s","hook_event_name":"%s","cwd":"%s"%s}' "$sid" "$1" "$(pwd)" "$2" \
    | HOME=$R/home "$R/bin/amx" hook-indicator --harness "$name" "$1" >> "$R/logs/hooks.log" 2>&1
  echo "$name $1 runtime=${AMX_RUNTIME_SESSION_ID:-none} exit=$?" >> "$R/logs/hooks.log"
}
hook SessionStart ',"source":"startup"'
case "$(pwd -P)" in
*/proj/history)
  i=1
  while [ $i -le 3000 ]; do printf 'LINE %04d needle-%04d\n' $i $i; i=$((i+1)); done
  echo "link: http://127.0.0.1:$(cat $R/linkport)/s15-link"
  echo "history stand-in ready (pid $$)"
  n=0
  while IFS= read -r line; do
    n=$((n+1))
    case "$line" in
      ask) hook PermissionRequest ',"tool_name":"Bash"'; echo "asked ($n)" ;;
      quit) exit 0 ;;
      *) echo "echo[$n]: $line" ;;
    esac
  done
  exit 0 ;;
esac
for v in $(env | grep -E '^(AMX_|KITTY_|CLAUDE_|CODEX_|COPILOT_|TMUX)' | cut -d= -f1); do
  case "$v" in TMUX|TMUX_PANE) ;; *) unset "$v" ;; esac
done
. "$R/real/env"
exec "$real" "$@"
