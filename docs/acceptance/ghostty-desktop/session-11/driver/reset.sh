#!/bin/sh
# Return the isolated world to empty: no daemon, no private tmux server, no state.
R=/tmp/amxs11; . $R/env.sh
tmux -S $R/tmp/agent-mux-$(id -u)/tmux.sock kill-server 2>/dev/null
$R/obs.sh kill-server 2>/dev/null
pkill -f "$R/bin/amx" 2>/dev/null
sleep 1
rm -rf $R/home/.local $R/tmp/* $R/home/.claude-a/projects/-private-tmp-amxs11-home* 
git -C $R/proj/alpha worktree prune 2>/dev/null
for b in $(git -C $R/proj/alpha branch --list 'amx/*' --format='%(refname:short)'); do git -C $R/proj/alpha branch -q -D "$b"; done
perl -pi -e 's/\*\*Status\*\*: complete/**Status**: pending/' $R/aslplan/session-1.md
rm -f $R/aslrun.log
mkdir -p $R/tmp
