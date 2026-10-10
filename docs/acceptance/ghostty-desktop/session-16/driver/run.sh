#!/bin/bash
# Runs the recovery and ownership acceptance on the MacBook and writes what
# it observed to OUTDIR/recovery-acceptance.log.
# usage: run.sh OUTDIR [CASE...]
#   cases, in order: up crash daemon transfer passive detached sleep exited absent lost down
#   and `purge`, which removes the world from the MacBook altogether.
# `setup.sh REPO` must have built the world first.
here=$(cd "$(dirname "$0")" && pwd)
out=$1; shift
. "$here/env.sh"
mkdir -p "$out" $L/logs
LOG=$out/recovery-acceptance.log
. "$here/lib.sh"
$SSH "$H" "cat > $R/remote-lib.sh" < "$here/remote-lib.sh"
[ $# -gt 0 ] || set -- up crash daemon transfer passive detached sleep exited absent lost down

ids() { A=$(rq 'rsof alpha'); B=$(rq 'rsof beta'); C=$(rq 'rsof gamma'); }
# on SESSION: asks the daemon to have the workspace show a session, as the
# Dashboard does, and waits until the app has a terminal attached to it.
on() { rq "dview session $2" >> "$LOG" 2>&1; waitfor 15 rtest "[ \$(attached $1) = 1 ]"; }
world() {
  facts "registry" registry
  facts "panes on the private tmux server" panes
  facts "terminals attached to the private tmux server" clients
  facts "what the daemon lists" snapshot
  facts "the app's saved workspace" prefs
}

for c in "$@"; do case $c in

up)
  note "A fresh world on the MacBook: daemon, three stand-in agents on the private tmux server, the app"
  rr >> "$LOG" 2>&1 <<'REMOTE'
scutil --get ComputerName; sw_vers -productVersion; amx --version; tmux -V
daemon_start || { echo "daemon did not start"; tail -5 $R/logs/daemon.out; exit 1; }
for p in alpha beta gamma; do
  amx project launch $p --profile work --backend tmux --json > $R/logs/launch-$p.json 2>&1 || { echo "launch $p failed"; cat $R/logs/launch-$p.json; exit 1; }
done
n=40; while [ $n -gt 0 ] && [ "$(snapshot | grep -c 'runtime=tmux')" != 3 ]; do sleep 0.25; n=$((n-1)); done
app_defaults; rm -f $R/app.started
app_start || echo "the app did not come up"
REMOTE
  ids
  wcheck "the daemon lists the three sessions" 10 '[ "$(snapshot | grep -c runtime=tmux)" = 3 ]'
  wcheck "the app is running with its feed live" 10 '[ -n "$(apppid)" ] && applog | grep -q "feed: live"'
  check "no terminal is attached to any agent yet" ev '[ -z "$(rq clients)" ]'
  world
  ;;

crash)
  note "app-crash-same-agent: the app is killed outright while its terminal is on alpha, then started again"
  ids; mark
  on alpha $A
  wcheck "the app's terminal is the one terminal on alpha" 10 '[ "$(attached alpha)" = 1 ]'
  wcheck "the app itself saved where it is" 10 'prefs | grep -q "window_open=1 place=session:'$A'"'
  before=$(rq 'agentproc alpha'); size=$(rq 'size alpha'); client=$(rq clients)
  say "alpha's agent before the crash (pid, start time UTC): $before; pane size $size; $client"
  facts "registry before the crash" 'regline alpha'
  apppid=$(rq apppid)
  rq "kill -9 $apppid"
  say "killed the app (pid $apppid) with SIGKILL at $(date -u +%H:%M:%SZ)"
  wcheck "the app is gone" 10 '[ -z "$(apppid)" ]'
  wcheck "its terminal left the server" 10 '[ "$(attached alpha)" = 0 ]'
  wcheck "the ownership it held was released without it (amx outlived the hangup)" 6 'regline alpha | grep -q owner=none'
  check "alpha's agent is the same process" ev '[ "$(rq "agentproc alpha")" = "$before" ]'
  check "alpha's pane kept the size its owner last gave it" ev '[ "$(rq "size alpha")" = "$size" ]'
  t1=$(rq 'lasttick alpha'); sleep 3; t2=$(rq 'lasttick alpha')
  say "alpha's own count with no app: $t1 -> $t2"
  check "the agent kept counting while no app ran" ev '[ ${t2%% *} -gt ${t1%% *} ]'
  check "the saved workspace still says: open, on alpha" ev 'rq prefs | grep -q "window_open=1 place=session:$A"'
  rq 'app_start' >> "$LOG" 2>&1; rq 'echo 0 > $R/logmark'
  logcheck "the new app reopens its window at launch" 15 "workspace: reopening at launch (was open: true, place: session:$A)"
  logcheck "it asks amx whether alpha still runs and is free" 15 "session $A: live(clients: 0)"
  logcheck "it reattaches alpha's terminal" 15 "workspace: restored $A; reattaching its terminal"
  wcheck "one terminal is on alpha again" 10 '[ "$(attached alpha)" = 1 ]'
  check "it is a new terminal of a new app process" ev '[ "$(rq apppid)" != "$apppid" ] && [ "$(rq clients)" != "$client" ]'
  check "alpha's agent is still the same process" ev '[ "$(rq "agentproc alpha")" = "$before" ]'
  check "its count never started over or skipped" ev '[ "$(rq "tickgaps alpha")" = 0 ]'
  check "no session was launched: three registrations, three panes" ev '[ "$(rq regcount)" = 3 ] && [ "$(rq panes | wc -l | tr -d " ")" = 3 ]'
  say "alpha's agent after the restart: $(rq 'agentproc alpha'); pane size $(rq 'size alpha'); $(rq clients)"
  applog_since; world
  ;;

daemon)
  note "daemon-restart-same-agent: the daemon is killed outright while the app shows alpha, then started again"
  ids
  rq 'priv send-keys -t $(pane beta) ask Enter'
  wcheck "beta asked for permission and the daemon shows it waiting" 10 'snapshot | grep -q "beta .*icon=key .*attention=True"'
  sleep 4   # one maintenance pass, so the state is in the daemon's checkpoint
  before=$(rq 'for p in alpha beta gamma; do agentproc $p; done'); client=$(rq clients); dpid=$(rq daemonpid)
  say "agents before (pid, start time UTC): $(echo $before)"
  say "terminals before: $client; daemon pid $dpid"
  mark
  rq "kill -9 $dpid"
  say "killed the daemon (pid $dpid) with SIGKILL at $(date -u +%H:%M:%SZ)"
  logcheck "the app notices the feed is gone" 20 "feed: "
  logcheck "it keeps its rows from the last snapshot" 20 "workspace: rows held from the last snapshot (3 session(s), selection $A)"
  check "with no daemon the app's terminal is still the one on alpha" ev '[ "$(rq clients)" = "$client" ]'
  check "with no daemon every agent is the same process" ev '[ "$(rq "for p in alpha beta gamma; do agentproc \$p; done")" = "$before" ]'
  facts "the app's log with the daemon down" since
  rq 'daemon_start' >> "$LOG" 2>&1
  ndpid=$(rq daemonpid)
  say "started a new daemon: pid $ndpid"
  check "it is another daemon process" ev '[ -n "$ndpid" ] && [ "$ndpid" != "$dpid" ]'
  logcheck "the app's feed is live again" 30 "feed: live"
  logcheck "its rows are current again, on the same selection" 30 "workspace: rows current (3 session(s), selection $A)"
  wcheck "the new daemon lists the same three runtime sessions" 10 '[ "$(snapshot | grep -c runtime=tmux)" = 3 ] && [ "$(listed '$A')" = 1 ] && [ "$(listed '$B')" = 1 ] && [ "$(listed '$C')" = 1 ]'
  wcheck "beta's waiting state came back from the checkpoint" 10 'snapshot | grep -q "beta .*icon=key .*attention=True"'
  check "no hook arrived after the restart: the state was restored, not re-reported" ev '[ "$(rq "grep -c PermissionRequest \$R/logs/hooks.log")" = 1 ]'
  check "the app made no new terminal and let none go" ev 'since_lacks "attached a terminal" && since_lacks "detaching" && since_lacks "ended; detached"'
  check "the same terminal is still the one on alpha" ev '[ "$(rq clients)" = "$client" ]'
  check "every agent is still the same process" ev '[ "$(rq "for p in alpha beta gamma; do agentproc \$p; done")" = "$before" ]'
  check "no count started over or skipped" ev '[ "$(rq "tickgaps alpha; tickgaps beta; tickgaps gamma" | tr -d "\n")" = 000 ]'
  check "no session was launched" ev '[ "$(rq regcount)" = 3 ]'
  applog_since; world
  ;;

transfer)
  note "ownership-transfer: a second terminal attaches to alpha while the app's terminal is on it"
  ids; mark
  before=$(rq 'agentproc alpha'); mine=$(rq clients); size=$(rq 'size alpha')
  say "before: $mine; pane size $size; agent $before"
  rq "ext_open $A 90 25"
  wcheck "the second terminal (90x25) is the one terminal on alpha" 15 '[ "$(attached alpha)" = 1 ] && clients | grep -q "pane=$(pane alpha) size=90x25"'
  check "it is not the app's terminal" ev '[ "$(rq clients)" != "$mine" ]'
  check "alpha's pane took the new owner's size" ev '[ "$(rq "size alpha")" = 90x25 ]'
  logcheck "the app's terminal ended" 10 "workspace: the terminal for $A ended; detached"
  logcheck "the app shows alpha as detached and does not take it back" 10 "workspace: showing detached(runtimeSessionID: \"$A\")"
  facts "registry: the owner is the second terminal's amx attach" 'regline alpha'
  sleep 5
  check "five seconds on, still exactly one terminal, the second one" ev '[ "$(rq "attached alpha")" = 1 ] && rq clients | grep -q "size=90x25"'
  check "the app attached nothing meanwhile" ev 'since_lacks "attached a terminal"'
  check "alpha's agent is the same process" ev '[ "$(rq "agentproc alpha")" = "$before" ]'
  applog_since

  say ""
  say "The app is quit and started again while the second terminal owns alpha."
  rq 'app_quit; app_start' >> "$LOG" 2>&1; rq 'echo 0 > $R/logmark'
  logcheck "the new app finds alpha attached elsewhere" 20 "session $A: live(clients: 1)"
  logcheck "it restores the selection without taking the session" 20 "workspace: $A is attached elsewhere (1 terminal(s)); restored detached"
  check "the second terminal is still the only one, at its size" ev '[ "$(rq "attached alpha")" = 1 ] && [ "$(rq "size alpha")" = 90x25 ]'
  applog_since

  say ""
  say "The second terminal is closed."
  rq ext_close
  wcheck "no terminal is on alpha" 10 '[ "$(attached alpha)" = 0 ]'
  wcheck "its ownership was released" 15 'regline alpha | grep -q owner=none'
  check "with nobody attached the pane keeps the last owner's size" ev '[ "$(rq "size alpha")" = 90x25 ]'
  check "alpha's agent is the same process" ev '[ "$(rq "agentproc alpha")" = "$before" ]'

  say ""
  say "The app is quit and started again with alpha free."
  rq 'app_quit; app_start' >> "$LOG" 2>&1; rq 'echo 0 > $R/logmark'
  logcheck "the new app finds alpha free and reattaches" 20 "workspace: restored $A; reattaching its terminal"
  wcheck "the app's terminal is the one terminal on alpha, at the app's size" 10 '[ "$(attached alpha)" = 1 ] && [ "$(size alpha)" = '$size' ]'
  check "alpha's agent is the same process after two changes of owner" ev '[ "$(rq "agentproc alpha")" = "$before" ]'
  check "its count never started over or skipped" ev '[ "$(rq "tickgaps alpha")" = 0 ]'
  applog_since; world
  ;;

passive)
  note "passive-preview-no-resize: the Overview and the Dashboard read every pane while the app's terminal owns alpha"
  ids; mark
  sizes=$(rq 'for p in alpha beta gamma; do size $p; done'); clients=$(rq clients); owners=$(rq 'registry | cut -d" " -f1,6')
  say "before: pane sizes $(echo $sizes); terminals: $clients"
  rq 'dview overview' >> "$LOG" 2>&1
  logcheck "the app opened its Overview" 15 "workspace: opening overview view overview"
  # The Overview starts in its compact layout, which lists without previews.
  # One `c` written to the Overview's own terminal (through the observer that
  # hosts it, not through the desktop) switches it to the layout with them.
  waitfor 10 rtest 'vscreen overview | grep -q "Live sessions"'
  rq 'vscreen overview | grep -q "c normal" && obs send-keys -t "=overview:" c'
  wcheck "the Overview shows what each of the three panes has on its screen" 30 '[ "$(vscreen overview | grep -c "tick [0-9]* pid")" -ge 3 ]'
  first=$(rq 'vscreen overview | grep -o "tick [0-9]* pid [0-9]*" | sort -u | tr "\n" ";"')
  sleep 6
  second=$(rq 'vscreen overview | grep -o "tick [0-9]* pid [0-9]*" | sort -u | tr "\n" ";"')
  say "previews read at first: $first"
  say "previews six seconds later: $second"
  check "the previews are live: they were read again" ev '[ "$first" != "$second" ]'
  printf -- '--- the Overview as the app shows it (read from the observer) ---\n' >> "$LOG"; rq 'vscreen overview | head -40' >> "$LOG"
  rq 'dview dashboard' >> "$LOG" 2>&1
  wcheck "the Dashboard's preview shows a pane's screen too" 20 'vscreen dashboard | grep -q "tick [0-9]* pid"'
  check "every pane has the size it had" ev '[ "$(rq "for p in alpha beta gamma; do size \$p; done")" = "$sizes" ]'
  check "the only terminal is still the app's one on alpha: reading attached nothing" ev '[ "$(rq clients)" = "$clients" ]'
  check "beta and gamma, which nobody ever attached to, are still 80x24 with no terminal" ev '[ "$(rq "size beta; size gamma; attached beta; attached gamma" | tr "\n" " ")" = "80x24 80x24 0 0 " ]'
  check "no ownership was recorded or changed" ev '[ "$(rq "registry | cut -d\" \" -f1,6")" = "$owners" ]'
  check "nothing typed reached an agent: no pane shows an echo" ev '! rq "screen alpha; screen beta; screen gamma" | grep -q "echo:"'
  rq "dview session $A" >> "$LOG" 2>&1
  applog_since; world
  ;;

detached)
  note "detached-operation: the app is quit; the agents and the daemon carry on with no window and no terminal"
  ids; mark
  before=$(rq 'for p in alpha beta gamma; do agentproc $p; done'); size=$(rq 'size alpha')
  rq 'perl -e "alarm 15; exec @ARGV" osascript -e "tell application id \"$BUNDLE\" to quit"'
  wcheck "the app quit" 15 '[ -z "$(apppid)" ]'
  wcheck "no terminal is attached to anything" 10 '[ -z "$(clients)" ]'
  wcheck "alpha has no owner on record" 10 'regline alpha | grep -q owner=none'
  check "quitting left the workspace saved as open, on alpha" ev 'rq prefs | grep -q "window_open=1 place=session:$A"'
  t1=$(rq 'lasttick alpha')
  rq 'priv send-keys -t $(pane alpha) ask Enter'
  wcheck "alpha asked for permission with nothing attached, and the daemon shows it waiting" 10 'snapshot | grep -q "alpha .*icon=key .*attention=True"'
  sleep 8
  t2=$(rq 'lasttick alpha')
  say "alpha's own count with no app and no terminal: $t1 -> $t2"
  check "alpha kept working" ev '[ $(( ${t2%% *} - ${t1%% *} )) -ge 7 ]'
  check "every agent is the same process" ev '[ "$(rq "for p in alpha beta gamma; do agentproc \$p; done")" = "$before" ]'
  check "alpha's pane kept its size" ev '[ "$(rq "size alpha")" = "$size" ]'
  facts "what the daemon lists with no app running" snapshot
  facts "alpha's screen, read with nothing attached" 'screen alpha | tail -6'
  rq 'app_start' >> "$LOG" 2>&1; rq 'echo 0 > $R/logmark'
  logcheck "started again, the app returns to alpha" 20 "workspace: restored $A; reattaching its terminal"
  wcheck "its terminal is the one terminal on alpha" 10 '[ "$(attached alpha)" = 1 ]'
  check "every agent is still the same process" ev '[ "$(rq "for p in alpha beta gamma; do agentproc \$p; done")" = "$before" ]'
  check "no count started over or skipped" ev '[ "$(rq "tickgaps alpha; tickgaps beta; tickgaps gamma" | tr -d "\n")" = 000 ]'
  applog_since; world
  ;;

sleep)
  note "sleep-wake: the MacBook is put to sleep and woken while the app shows alpha"
  ids; mark
  before=$(rq 'for p in alpha beta gamma; do agentproc $p; done'); client=$(rq clients); size=$(rq 'size alpha'); dpid=$(rq daemonpid)
  mac=$(rq 'ifconfig en0 | awk "/ether/{print \$2}"'); ip=$(rq 'ipconfig getifaddr en0')
  t1=$(rq 'lasttick alpha')
  say "before: agents $(echo $before); $client; daemon pid $dpid; alpha's count $t1"
  asked=$(rq 'date "+%Y-%m-%d %H:%M:%S"')
  rq 'nohup /bin/sh -c "sleep 2; pmset sleepnow" >/dev/null 2>&1 </dev/null &'
  say "asked the MacBook to sleep at $asked, its time (pmset sleepnow); it is left alone for 60 s"
  sleep 60
  # Wake it: a wake-on-LAN packet for its network interface and an SSH
  # connection, either of which brings it to a dark wake; then, once it
  # answers, a declaration of user activity so the wake is a full one.
  woke=0; tries=0
  while [ $tries -lt 400 ]; do
    tries=$((tries+1))
    python3 "$here/wake.py" "$mac" "${ip%.*}.255" "$ip" 2>/dev/null
    if $SSH -o ConnectTimeout=6 "$H" 'caffeinate -u -t 5 >/dev/null 2>&1 & true' </dev/null 2>/dev/null; then woke=1; break; fi
    sleep 6
  done
  say "the MacBook answered at $(rq 'date "+%Y-%m-%d %H:%M:%S"'), its time, on attempt $tries"
  check "the MacBook answers again" ev '[ $woke = 1 ]'
  sleep 8
  printf -- '--- the MacBook'"'"'s power log since the request: its sleeps and wakes (pmset -g log) ---\n' >> "$LOG"
  rq "pmset -g log | awk -v since='$asked' '\$3 ~ /^[+-][0-9]+\$/ && (\$1\" \"\$2) >= since && (\$4==\"Sleep\" || \$4==\"Wake\" || \$4==\"DarkWake\")' | cut -c1-200" >> "$LOG"
  check "the power log has a sleep after the request" ev 'sed -n "/power log since the request/,\$p" "$LOG" | grep -q "^20.* Sleep  *.*Entering Sleep state"'
  check "and a full wake after it" ev 'sed -n "/power log since the request/,\$p" "$LOG" | grep -q "^20.* Wake  *.*\(DarkWake to FullWake\|Wake from\)"'
  slept=$(rq 'awk "NR>1 && \$3-prev>d {d=\$3-prev} {prev=\$3} END {print d+0}" $R/logs/ticks-alpha.log')
  say "the longest pause between two of alpha's counts: ${slept}s (a count is a second apart while the machine is awake)"
  check "alpha's clock jumped while its count did not: the process was suspended with the machine" ev '[ "$slept" -ge 8 ]'
  check "its count did not start over or skip: it was not replaced" ev '[ "$(rq "tickgaps alpha; tickgaps beta; tickgaps gamma" | tr -d "\n")" = 000 ]'
  logcheck "the app noticed the wake and reconnected its feed" 30 "woke from sleep; reconnecting the feed"
  logcheck "its feed came back live" 30 "feed: live"
  wcheck "the daemon lists the three sessions" 20 '[ "$(snapshot | grep -c runtime=tmux)" = 3 ]'
  check "it is the same daemon" ev '[ "$(rq daemonpid)" = "$dpid" ]'
  check "every agent is the same process" ev '[ "$(rq "for p in alpha beta gamma; do agentproc \$p; done")" = "$before" ]'
  check "the same terminal is the one on alpha, at the same size" ev '[ "$(rq clients)" = "$client" ] && [ "$(rq "size alpha")" = "$size" ]'
  check "the app made no new terminal and let none go" ev 'since_lacks "attached a terminal" && since_lacks "ended; detached"'
  t2=$(rq 'lasttick alpha'); sleep 3; t3=$(rq 'lasttick alpha')
  say "alpha's count after the wake: $t2 -> $t3"
  check "alpha is counting again" ev '[ ${t3%% *} -gt ${t2%% *} ]'
  check "no session was launched" ev '[ "$(rq regcount)" = 3 ]'
  applog_since; world
  ;;

exited)
  note "lost-versus-exited, the exit: gamma's agent ends with status 3 while the app shows it"
  ids
  on gamma $C; mark
  wcheck "the app's terminal is on gamma" 10 '[ "$(attached gamma)" = 1 ]'
  agents=$(rq 'pgrep -f "^/bin/bash $R/bin/claude" | wc -l | tr -d " "')
  say "stand-in agent processes before: $agents; gamma's is $(rq 'agentproc gamma')"
  rq 'priv send-keys -t $(pane gamma) "exit 3" Enter'
  say "gamma's agent was told to exit with status 3 at $(date -u +%H:%M:%SZ)"
  wcheck "the server keeps gamma's pane, dead, with status 3" 10 'panes | grep -q "^$(pane gamma) .*dead=1 status=3"'
  wcheck "the registry records it as exited, with the status" 15 'regline gamma | grep -q "stale .*reason=agent_exited_(status_3)"'
  wcheck "the daemon no longer lists gamma" 15 '[ "$(listed '$C')" = 0 ]'
  logcheck "the app is told it exited" 20 "session $C: exited("
  logcheck "the app says so in the pane and relaunches nothing" 20 "workspace: $C is not running: exited("
  logcheck "the pane shows the session as gone" 20 "workspace: showing gone(runtimeSessionID: \"$C\""
  facts "what amx answers about gamma" "check_json $C"
  check "amx attach refuses it as exited" ev 'rq "check_json $C" | grep -q "^error runtime_session_exited: .*agent exited (status 3)"'
  sleep 6
  check "nothing was started in its place: one stand-in fewer, three registrations, three panes" ev '[ "$(rq "pgrep -f \"^/bin/bash \$R/bin/claude\" | wc -l | tr -d \" \"")" = $((agents-1)) ] && [ "$(rq regcount)" = 3 ] && [ "$(rq panes | wc -l | tr -d " ")" = 3 ]'
  check "the other agents were not touched" ev '[ "$(rq "tickgaps alpha; tickgaps beta" | tr -d "\n")" = 00 ]'
  check "the app no longer saves gamma as the place to return to" ev '! rq prefs | grep -q "$C"'
  applog_since; world
  ;;

absent)
  note "absent-agent-not-relaunched: beta's agent dies while the app is not running; the app is then started"
  ids
  on beta $B
  wcheck "the app saved beta as where it is" 10 'prefs | grep -q "window_open=1 place=session:'$B'"'
  rq 'perl -e "alarm 15; exec @ARGV" osascript -e "tell application id \"$BUNDLE\" to quit"'
  wcheck "the app quit" 15 '[ -z "$(apppid)" ]'
  agents=$(rq 'pgrep -f "^/bin/bash $R/bin/claude" | wc -l | tr -d " "'); bpid=$(rq 'agentpid beta')
  rq "kill -9 $bpid"
  say "killed beta's agent (pid $bpid) with SIGKILL at $(date -u +%H:%M:%SZ), with no app running; stand-in agents before: $agents"
  wcheck "the daemon, alone, records beta as exited by that signal" 15 'regline beta | grep -q "stale .*reason=agent_exited_(signal_kill)"'
  wcheck "and stops listing it" 15 '[ "$(listed '$B')" = 0 ]'
  check "the saved workspace still points at beta" ev 'rq prefs | grep -q "window_open=1 place=session:$B"'
  rq 'app_start' >> "$LOG" 2>&1; rq 'echo 0 > $R/logmark'
  logcheck "the app reopens, heading for beta" 20 "workspace: reopening at launch (was open: true, place: session:$B)"
  logcheck "it finds beta is not listed and asks what became of it" 20 "workspace: $B is not listed after the restart; asking what became of it"
  logcheck "it reports the agent as not running and relaunches nothing" 20 "workspace: $B is not running: exited("
  sleep 8
  check "the app made no terminal for it" ev 'since_lacks "attached a terminal"'
  check "no terminal is attached to beta's pane" ev '[ "$(rq "attached beta")" = 0 ]'
  check "nothing was started: one stand-in fewer, three registrations, three panes" ev '[ "$(rq "pgrep -f \"^/bin/bash \$R/bin/claude\" | wc -l | tr -d \" \"")" = $((agents-1)) ] && [ "$(rq regcount)" = 3 ] && [ "$(rq panes | wc -l | tr -d " ")" = 3 ]'
  check "beta's process ID names no process" ev '! rq "kill -0 $bpid"'
  say "asking for it by name changes nothing either:"
  rq "dview session $B" >> "$LOG" 2>&1
  facts "what amx answers about beta" "check_json $B"
  check "amx attach refuses it and starts nothing" ev 'rq "check_json $B" | grep -q "^error runtime_session_exited:" && [ "$(rq regcount)" = 3 ]'
  check "the app no longer saves beta as the place to return to" ev '! rq prefs | grep -q "$B"'
  applog_since; world
  ;;

lost)
  note "lost-versus-exited, the loss: the private tmux server is killed outright while the app shows alpha"
  ids
  on alpha $A; mark
  wcheck "the app's terminal is on alpha" 10 '[ "$(attached alpha)" = 1 ]'
  spid=$(rq 'priv display-message -p "#{pid}"'); apid=$(rq 'agentpid alpha')
  rq "kill -9 $spid"
  say "killed the private tmux server (pid $spid) with SIGKILL at $(date -u +%H:%M:%SZ); alpha's agent was pid $apid"
  logcheck "the app's terminal ended with the server" 15 "workspace: the terminal for $A ended; detached"
  wcheck "the registry records alpha as lost, not exited" 15 'regline alpha | grep -q "stale .*reason=backend_generation_changed"'
  wcheck "the daemon no longer lists alpha" 15 '[ "$(listed '$A')" = 0 ]'
  logcheck "the app is told it was lost" 20 "session $A: lost("
  logcheck "the app says so in the pane and relaunches nothing" 20 "workspace: $A is not running: lost("
  facts "what amx answers about alpha (lost) and gamma (exited)" "check_json $A; check_json $C"
  check "amx attach refuses alpha as lost" ev 'rq "check_json $A" | grep -q "^error runtime_session_lost:"'
  check "and still refuses gamma as exited: the two are told apart" ev 'rq "check_json $C" | grep -q "^error runtime_session_exited: .*agent exited (status 3)"'
  sleep 8
  check "no tmux server was started in its place" ev 'rq "priv list-sessions 2>&1" | grep -q -e "no server running" -e "No such file" -e "error connecting"'
  check "no agent was started: no stand-in runs, and the registry still has three registrations" ev '[ "$(rq "pgrep -f \"^/bin/bash \$R/bin/claude\" | wc -l | tr -d \" \"")" = 0 ] && [ "$(rq regcount)" = 3 ]'
  check "the app attached nothing after the loss" ev 'since_lacks "attached a terminal"'
  facts "the registry at the end: one exited by status, one exited by signal, one lost" registry
  applog_since
  facts "what the daemon lists" snapshot
  facts "the daemon's own log of what it found" 'daemonlog'
  ;;

down)
  note "Taking the world down"
  rr >> "$LOG" 2>&1 <<'REMOTE'
ext_close
app_quit
amx daemon stop >/dev/null 2>&1
priv kill-server 2>/dev/null
obs kill-server 2>/dev/null
sleep 1
rm -rf $R/home/.local $R/tmp/* $R/logs/* $R/app.started $R/logmark $R/obs.sock $R/ext.pid
defaults delete $BUNDLE >/dev/null 2>&1
prefs="$REAL_HOME/Library/Preferences/$BUNDLE.plist"
if [ -f "$prefs" ] && [ "$(plutil -p "$prefs" 2>/dev/null | tr -d ' \n')" = "{}" ]; then rm -- "$prefs"; fi
mkdir -p $R/tmp $R/logs
echo "left running on the MacBook: [$(pgrep -fl "$R/" | cut -c1-80 | tr '\n' ';')]; the app's preferences file exists: $([ -f "$prefs" ] && echo yes || echo no)"
REMOTE
  ;;

purge)
  note "Removing the world from the MacBook"
  { echo ". $R/remote-env.sh"; echo 'cd /; rm -rf "$R"; echo "the world directory exists: $([ -e $R ] && echo yes || echo no)"'; } | $SSH "$H" /bin/sh -s >> "$LOG" 2>&1
  rm -rf "$L"
  ;;

*) echo "unknown case $c"; exit 2;;
esac; done
say ""
say "checks passed: $passed, failed: $fail"
[ $fail = 0 ]
