#!/bin/bash
# Brings the isolated world up or down. usage: world.sh up|down
#   up:   a separate Kitty instance, the world's daemon, two tmux sessions of
#         project alpha (profiles work and personal) and one Kitty session of
#         project beta, each running the stand-in agent.
#   down: quits the app under test, the daemon, the private tmux server and
#         that Kitty, and removes the world's state and the app's preferences.
here=$(cd "$(dirname "$0")" && pwd)
. "$here/env.sh"; . "$here/lib.sh"
case "$1" in
up)
  /Applications/kitty.app/Contents/MacOS/kitty --config NONE -o allow_remote_control=yes -o env=ZDOTDIR=$R/zdot \
    -o confirm_os_window_close=0 --listen-on "unix:$R/kitty-{kitty_pid}" --directory $R /bin/zsh > $R/logs/kitty.out 2>&1 &
  disown
  waitfor 15 sh -c "ls $R/kitty-* >/dev/null 2>&1" || { echo "kitty did not start"; exit 1; }
  echo "unix:$(ls $R/kitty-* | head -1)" > $R/kitty.sockname
  daemon_start || { echo "daemon did not start"; exit 1; }
  for p in work personal; do
    amx project launch alpha --profile $p --backend tmux --json \
      | python3 -c "import json,sys; print(json.load(sys.stdin)['data']['runtime_session_id'])" > $R/rs-$p
  done
  KITTY_LISTEN_ON=$(ksock) amx project launch beta --profile work --json > /dev/null
  waitfor 20 sh -c ". $here/env.sh; . $here/lib.sh; test \$(snapshot | grep -c 'claude') = 3" || { echo "sessions not listed"; snapshot; exit 1; }
  snapshot
  ;;
down)
  [ -f $R/app.pid ] && app_quit
  amx daemon stop >/dev/null 2>&1
  priv kill-server 2>/dev/null
  [ -f $R/kitty.sockname ] && kill "$(kittypid)" 2>/dev/null
  sleep 1
  rm -rf $R/home/.local $R/home/.claude-a $R/home/.claude-b $R/tmp/* $R/logs/* $R/kitty-* $R/kitty.sockname $R/rs-* $R/app.pid $R/app.started $R/logmark $R/menu.txt
  defaults delete $BUNDLE >/dev/null 2>&1
  mkdir -p $R/tmp $R/logs
  ;;
*) echo "usage: world.sh up|down"; exit 2;;
esac
