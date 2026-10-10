#!/bin/bash
# Brings the two-machine world up or down. usage: world.sh up|down
#   up:   on the MacBook, the world's daemon (no Kitty), the link target, and
#         one tmux session per project; on the Studio, the three real
#         harnesses, headless, each joined to its pane by bridge.py.
#   down: quits the app copy, ends every session, stops the daemon, the
#         private tmux server, the observer and the link target on the
#         MacBook, and the bridges and harnesses on the Studio.
here=$(cd "$(dirname "$0")" && pwd)
. "$here/env.sh"
real() { case $1 in claude) echo /Users/sglavoie/.local/bin/claude;; codex) echo /opt/homebrew/bin/codex;; copilot) echo /opt/homebrew/bin/copilot;; esac; }
case "$1" in
up)
  $SSH "$H" "cat > $R/remote-lib.sh" < "$here/remote-lib.sh"
  rr <<'REMOTE' || exit 1
. $R/remote-lib.sh
nohup amx daemon start >> $R/logs/daemon.out 2>&1 </dev/null &
n=40; while [ $n -gt 0 ] && [ ! -S $HOME/.local/share/agent-mux/daemon.sock ]; do sleep 0.25; n=$((n-1)); done
[ -S $HOME/.local/share/agent-mux/daemon.sock ] || { echo "daemon did not start"; tail -5 $R/logs/daemon.out; exit 1; }
link_start || { echo "the link target did not start"; exit 1; }
for p in claude codex copilot history; do
  profile=$p; [ $p = history ] && profile=plain
  amx project launch $p --profile $profile --backend tmux --json > $R/logs/launch-$p.json 2>&1 || { echo "launch $p failed"; cat $R/logs/launch-$p.json; exit 1; }
done
n=60; while [ $n -gt 0 ] && [ "$(ls $R/bridge/*.sock 2>/dev/null | wc -l | tr -d ' ')" != 3 ]; do sleep 0.25; n=$((n-1)); done
ls $R/bridge/
REMOTE
  # The harnesses: on the Studio, with no AgentMux, Kitty, ASL, or askpass
  # variable, and none of this run's guard directories on PATH.
  clean=$(env | grep -E '^(ASL_|AMX_|KITTY_|CLAUDE_CODE|CLAUDECODE|CLAUDE_PID|CLAUDE_EFFORT|SSH_ASKPASS|DISPLAY|TMUX)' | cut -d= -f1 | sed 's/^/-u /' | tr '\n' ' ')
  path=$(printf '%s' "$PATH" | tr ':' '\n' | grep -v -e agent-session-loop -e '/codex/.codex/' | paste -sd: -)
  for h in claude codex copilot; do
    # Detached with every stream redirected, so nothing keeps this script's
    # caller waiting on it.
    nohup /bin/sh -c "cd $L/proj/$h && exec python3 '$here/bridge.py' serve '$H' $R/bridge.py $R/bridge/$h.sock -- env $clean PATH='$path' '$(real $h)'" > $L/logs/serve-$h.log 2>&1 </dev/null &
  done
  sleep 6
  rr <<'REMOTE'
. $R/remote-lib.sh
n=80; while [ $n -gt 0 ] && [ "$(snapshot | grep -c 'runtime=tmux')" != 4 ]; do sleep 0.25; n=$((n-1)); done
snapshot; cat $R/logs/hooks.log
REMOTE
  for h in claude codex copilot; do echo "$h on the Studio: $(cat $L/logs/serve-$h.log | tr '\n' ' ')"; done
  ;;
down)
  rr <<'REMOTE'
. $R/remote-lib.sh
app_quit
amx daemon stop >/dev/null 2>&1
priv kill-server 2>/dev/null
obs kill-server 2>/dev/null
link_stop
sleep 1
rm -rf $R/home/.local $R/tmp/* $R/logs/* $R/bridge/* $R/app.started $R/logmark $R/obs.sock
defaults delete $BUNDLE >/dev/null 2>&1
prefs="$REAL_HOME/Library/Preferences/$BUNDLE.plist"
if [ -f "$prefs" ] && [ "$(plutil -p "$prefs" 2>/dev/null | tr -d ' \n')" = "{}" ]; then rm -- "$prefs"; fi
mkdir -p $R/tmp $R/logs
echo "left on the MacBook: $(pgrep -fl "$R/" | cut -c1-80 | tr '\n' ';')"
REMOTE
  sleep 1
  pkill -f "bridge.py serve $H" 2>/dev/null
  echo "left on the Studio: $(pgrep -fl 'bridge.py serve' | tr '\n' ';')"
  ;;
*) echo "usage: world.sh up|down"; exit 2;;
esac
