#!/bin/bash
# Runs on the MacBook. Ends everything the world started and removes it, then
# shows that the MacBook user's own amx, AgentMux directories, Kitty
# configuration, and app preferences are what they were at setup.
R=/tmp/amxs19
. $R/driver/remote-env.sh
out=${1:-$R/out}/teardown.log; : > "$out"
say() { echo "$*" | tee -a "$out"; }
if [ -f $R/kitty.pid ]; then kill "$(cat $R/kitty.pid)" 2>/dev/null; fi
osascript -e "tell application id \"$BUNDLE\" to quit" >/dev/null 2>&1
pkill -f "$DEST/Contents/MacOS/Agent Mux" 2>/dev/null
amx daemon stop >/dev/null 2>&1
priv kill-server 2>/dev/null; obs kill-server 2>/dev/null
sleep 2
# Anything else that still runs from the world, but not this script or the
# shell SSH started it from, whose command lines name the world too.
for p in $(pgrep -f "$R/"); do [ "$p" = $$ ] || [ "$p" = $PPID ] || kill "$p" 2>/dev/null; done; sleep 1
defaults delete $BUNDLE >/dev/null 2>&1; rm -f "$REAL_HOME/Library/Preferences/$BUNDLE.plist"
left=$(pgrep -fl "$R/" | grep -v "driver/down.sh")
if [ -z "$left" ]; then say "PASS: no process of the world is left"; else say "FAIL: processes left: $left"; fi
realstate > $R/realstate.after
if diff $R/realstate.before $R/realstate.after >> "$out"; then say "PASS: the MacBook user's own amx, AgentMux directories, Kitty configuration, app preferences, and Kitty processes are what they were before the run"; else say "FAIL: the MacBook user's own state changed (diff above)"; fi
cat $R/realstate.after >> "$out"
cp "$out" /tmp/amxs19-teardown.log
# The Go module cache the cockpit rehearsal filled is read-only by design.
chmod -R u+w $R 2>/dev/null
rm -rf $R
# The rehearsal's `just install` built an amx in the checkout copy.
[ -z "$COPY" ] || rm -f "$COPY/amx"
if [ ! -e $R ]; then echo "PASS: $R is removed" | tee -a /tmp/amxs19-teardown.log; else echo "FAIL: $R is still there" | tee -a /tmp/amxs19-teardown.log; fi
