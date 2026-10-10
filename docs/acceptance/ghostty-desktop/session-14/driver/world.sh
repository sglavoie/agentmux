#!/bin/bash
# Brings the isolated world up or down. usage: world.sh up|down
#   up:   the world's daemon, started with no Kitty variable, and one tmux
#         session of project beta running the stand-in agent. No Kitty is
#         started and none is referenced.
#   down: quits the app under test, the daemon, the private tmux server and
#         the observer, and removes the world's state and the app's preferences.
here=$(cd "$(dirname "$0")" && pwd)
. "$here/env.sh"; . "$here/lib.sh"
case "$1" in
up)
  daemon_start || { echo "daemon did not start"; exit 1; }
  amx transcripts rebuild > $R/logs/transcripts.out 2>&1
  amx project launch beta --profile work --backend tmux --json \
    | python3 -c "import json,sys; print(json.load(sys.stdin)['data']['runtime_session_id'])" > $R/rs-beta
  waitfor 20 sh -c ". $here/env.sh; . $here/lib.sh; snapshot | grep -q 'beta'" || { echo "session not listed"; snapshot; exit 1; }
  snapshot
  ;;
down)
  [ -f $R/app.pid ] && app_quit
  amx daemon stop >/dev/null 2>&1
  priv kill-server 2>/dev/null
  obs kill-server 2>/dev/null
  sleep 1
  git -C $R/proj/alpha worktree prune 2>/dev/null
  for b in $(git -C $R/proj/alpha branch --list 'amx/*' --format='%(refname:short)' 2>/dev/null); do git -C $R/proj/alpha branch -q -D "$b"; done
  perl -pi -e 's/\*\*Status\*\*: complete/**Status**: pending/' $R/plans/demo/session-1.md 2>/dev/null
  rm -rf $R/home/.local $R/home/.claude-b $R/tmp/* $R/logs/* $R/rs-* $R/app.pid $R/app.started $R/logmark $R/obs.sock
  find $R/home/.claude-a/projects -name '*.jsonl' ! -name '11111111-*' -delete 2>/dev/null
  defaults delete $BUNDLE >/dev/null 2>&1
  mkdir -p $R/tmp $R/logs
  ;;
*) echo "usage: world.sh up|down"; exit 2;;
esac
