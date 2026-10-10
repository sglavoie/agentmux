#!/bin/bash
# Brings the isolated world up or down. usage: world.sh up|down
#   up:   the world's daemon (no Kitty variable), the link target, and one
#         tmux session per project: the three real harnesses and the
#         main-screen history stand-in.
#   down: quits the app under test, ends every session, stops the daemon, the
#         private tmux server, the observer and the link target, and removes
#         the world's state and the app copy's preferences.
here=$(cd "$(dirname "$0")" && pwd)
. "$here/env.sh"; . "$here/lib.sh"
case "$1" in
up)
  daemon_start || { echo "daemon did not start"; exit 1; }
  link_start || { echo "the link target did not start on port $LINKPORT"; cat $R/logs/link.log; exit 1; }
  for p in claude codex copilot history; do
    profile=$p; [ $p = history ] && profile=plain
    amx project launch $p --profile $profile --backend tmux --json > $R/logs/launch-$p.json 2>&1 || { echo "launch $p failed"; cat $R/logs/launch-$p.json; exit 1; }
  done
  waitfor 30 sh -c ". $here/env.sh; . $here/lib.sh; [ \$(snapshot | grep -c 'runtime=tmux') -eq 4 ]" || { echo "sessions not listed"; snapshot; cat $R/logs/hooks.log; exit 1; }
  snapshot
  ;;
down)
  [ -f $R/app.pid ] && app_quit
  amx daemon stop >/dev/null 2>&1
  priv kill-server 2>/dev/null
  obs kill-server 2>/dev/null
  link_stop || echo "the link target could not be stopped; see above"
  sleep 1
  rm -rf $R/home/.local $R/tmp/* $R/logs/* $R/app.pid $R/app.started $R/logmark $R/obs.sock
  # The copy's preferences live with the real user whatever HOME says. When
  # deleting leaves an empty domain behind, its one file is removed. The
  # preferences daemon is the operator's and is never restarted for this.
  defaults delete $BUNDLE >/dev/null 2>&1
  prefs="$REAL_HOME/Library/Preferences/$BUNDLE.plist"
  if [ -f "$prefs" ] && [ "$(plutil -p "$prefs" 2>/dev/null | tr -d ' \n')" = "{}" ]; then rm -- "$prefs"; fi
  [ -f "$prefs" ] && echo "the copy's preferences are still at $prefs"
  mkdir -p $R/tmp $R/logs
  ;;
*) echo "usage: world.sh up|down"; exit 2;;
esac
