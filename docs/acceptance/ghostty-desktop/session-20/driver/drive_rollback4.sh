#!/bin/bash
# Runs on the MacBook, in the Session 19 world. Rehearses step 4 of the
# rollback in docs/daily-driver-trial.md: going back to the build that was
# installed before the candidate (amx and Agent Mux.app at the earlier
# revision, built by run.sh's `old` step into /tmp/amxs20-old), over a world
# the candidate has been used in, and then forward again.
#
# What `just update` does in the earlier checkout is done here by hand, with
# the world's paths: the earlier amx is put at ~/.local/bin/amx, the earlier
# app is installed by the earlier checkout's own install-app.sh into the
# world's install root, and the earlier amx restarts the daemon. Nothing of
# the MacBook user's own is installed, replaced, or restarted.
# usage: drive_rollback4.sh LOG
R=/tmp/amxs19; . $R/driver/remote-env.sh
OLD=/tmp/amxs20-old
LOG=$1; : > "$LOG"
CFG=$HOME/.config/agent-mux/config.toml
note() { printf '\n##### %s [%s]\n' "$*" "$(date -u +%H:%M:%SZ)" >> "$LOG"; echo "## $*"; }
fail=0; passed=0
check() { local what=$1; shift; if "$@" >/dev/null 2>&1; then printf 'PASS: %s\n' "$what" >> "$LOG"; passed=$((passed+1)); else printf 'FAIL: %s\n' "$what" >> "$LOG"; echo "FAIL: $what"; fail=$((fail+1)); fi; }
wcheck() { local what=$1 n=$(( $2 * 4 )); shift 2; while [ $n -gt 0 ]; do if eval "$1" >/dev/null 2>&1; then printf 'PASS: %s\n' "$what" >> "$LOG"; passed=$((passed+1)); return 0; fi; sleep 0.25; n=$((n-1)); done; printf 'FAIL: %s (not within the wait)\n' "$what" >> "$LOG"; echo "FAIL: $what"; fail=$((fail+1)); }
ev() { eval "$1"; }
facts() { printf -- '--- %s ---\n' "$1" >> "$LOG"; shift; "$@" >> "$LOG" 2>&1; }
run() { printf -- '--- $ %s ---\n' "$1" >> "$LOG"; eval "$2" >> "$LOG" 2>&1; printf '(exit %s)\n' "$?" >> "$LOG"; }

apppid() { pgrep -f "$DEST/Contents/MacOS/Agent Mux" | head -1; }
app_start() { date '+%Y-%m-%d %H:%M:%S' > $R/app.started; open -n "$DEST" --env HOME="$HOME" --env TMPDIR="$TMPDIR" --env PATH="$PATH"; }
app_quit() { osascript -e "tell application id \"$BUNDLE\" to quit" >/dev/null 2>&1; n=40; while [ $n -gt 0 ] && [ -n "$(apppid)" ]; do sleep 0.25; n=$((n-1)); done; }
applog() { /usr/bin/log show --start "$(cat $R/app.started)" --style compact --predicate "subsystem == \"io.github.sglavoie.agentmux\" AND processIdentifier == $(apppid)" 2>/dev/null | sed -E -e '1d' -e 's/^[0-9-]+ ([0-9:.]+) +[A-Za-z]+ +Agent Mux\[[0-9:a-f]+\] /\1 /'; }
applines() { applog | grep -E 'backend:|feed:|running ' | head -12; }
panes() { priv list-panes -a -F '#{session_name} #{pane_id} pid=#{pane_pid} dead=#{pane_dead} #{pane_current_path}' 2>&1; }
rsof() { registry | awk -v p="project=$1" '$2=="live" && $6==p {print $1}' | tail -1; }
pidof() { registry | awk -v p="project=$1" '$2=="live" && $6==p {print $7}' | tail -1 | cut -d= -f2; }
proc() { LC_ALL=C TZ=UTC ps -o pid=,lstart= -p "$1" 2>/dev/null | sed -e 's/^ *//' -e 's/  */ /g'; }
titles() { K ls | python3 -c "
import json,sys
for o in json.load(sys.stdin):
  for t in o['tabs']: print(t['title'])"; }
kwin() { K ls | python3 -c "
import json,sys
for o in json.load(sys.stdin):
  for t in o['tabs']:
    for w in t['windows']:
      if w.get('user_vars',{}).get('amx_project')=='$1': print(w['id'], w.get('pid'))"; }
ktext() { K get-text --match id:$1 2>/dev/null; }
klaunch() { KITTY_LISTEN_ON=$KSOCK amx project launch "$@"; }
# The operator types these inside Kitty; here Kitty is named for them.
inkitty() { KITTY_LISTEN_ON=$KSOCK "$@"; }
daemon_ready() { amx daemon status 2>/dev/null | grep -q '^ready: true'; }
# What must survive: the files neither build may rewrite, and the state the
# candidate wrote that the earlier build does not know.
SOURCES="$CFG $HOME/.claude-a/projects/-tmp-amxs19-proj-alpha/11111111-1111-4111-8111-111111111111.jsonl $DATA/handoffs/rehearsal-handoff.md $DATA/worktrees/rehearsal/README $HOME/plans/demo-plan/session-1.md $DATA/launch_overrides.json"
CANDIDATE_STATE="$DATA/runtime-sessions.json $DATA/runtime-state.json"
sums() { for f in "$@"; do if [ -e "$f" ]; then printf '%s  %s\n' "$(shasum -a 256 < "$f" | cut -c1-16)" "${f#$R/}"; else printf '%-16s  %s\n' absent "${f#$R/}"; fi; done; }
listing() { (cd $DATA && find . -maxdepth 2 -not -name '*.sock' -not -name '*.lock' | sort); }

test -x $OLD/amx && test -d "$OLD/macos/build/Agent Mux.app/Contents" || { echo "no earlier build in $OLD: run the old step"; printf 'FAIL: no earlier build in %s\n' $OLD >> "$LOG"; exit 1; }
mkdir -p $HOME/.local/bin $R/keep
cp "$AMX" $R/keep/amx-candidate   # the candidate's amx, kept outside the app for the way forward
cp $R/keep/amx-candidate $HOME/.local/bin/amx
printf '#!/bin/sh\nexec "%s" "$@"\n' "$HOME/.local/bin/amx" > $R/bin/amx; chmod +x $R/bin/amx
TM=$(ls -d $DATA/runtime/tmux-*/bin/tmux | head -1)

rm -f $R/kitty.sock
nohup env -i HOME=$HOME TMPDIR=$TMPDIR PATH=$PATH LC_ALL=en_US.UTF-8 KITTY_CONFIG_DIRECTORY=$R/kittycfg \
  $KITTY/kitty --config NONE -o allow_remote_control=yes -o env=ZDOTDIR=$R/zdot -o confirm_os_window_close=0 \
  -o shell=/bin/zsh -o remember_window_size=no -o initial_window_width=150c -o initial_window_height=42c \
  --listen-on $KSOCK --instance-group amxs19 --directory $R > $R/logs/kitty.out 2>&1 </dev/null &
echo $! > $R/kitty.pid
n=80; while [ $n -gt 0 ] && ! K ls >/dev/null 2>&1; do sleep 0.25; n=$((n-1)); done
K ls >/dev/null 2>&1 || { printf 'FAIL: Kitty did not start\n%s\n' "$(cat $R/logs/kitty.out)" >> "$LOG"; echo "Kitty did not start"; exit 1; }
sleep 2
amx daemon stop >/dev/null 2>&1; sleep 1
KITTY_LISTEN_ON=$KSOCK nohup amx daemon start >> $R/logs/daemon.out 2>&1 </dev/null &
sleep 2

note "0 the two builds"
facts "the candidate (installed: ~/.local/bin/amx and the app)" sh -c "amx --version; /usr/libexec/PlistBuddy -c 'Print :CFBundleShortVersionString' '$DEST/Contents/Info.plist'"
facts "the earlier build (in $OLD, not installed yet)" sh -c "$OLD/amx --version; /usr/libexec/PlistBuddy -c 'Print :CFBundleShortVersionString' '$OLD/macos/build/Agent Mux.app/Contents/Info.plist'; codesign -dv '$OLD/macos/build/Agent Mux.app' 2>&1 | grep -E 'Signature|TeamIdentifier'"
OLDREV=$($OLD/amx --version | sed -E 's/.*revision ([0-9a-f]+).*/\1/')
check "the earlier amx is a different revision from the candidate's" ev '[ -n "$OLDREV" ] && ! amx --version | grep -q "revision $OLDREV"'
check "the earlier app bundle passes strict signature verification" codesign --verify --strict "$OLD/macos/build/Agent Mux.app"
check "the earlier app has no tmux runtime: it is the menu bar companion" ev '[ ! -e "$OLD/macos/build/Agent Mux.app/Contents/Resources/tmux" ]'

note "1 the candidate in use: a tmux agent, a Kitty session, and the files a rollback must leave alone"
perl -0pi -e 's/^\[defaults\]\n/[defaults]\nlaunch_backend = "tmux"\n/m' "$CFG"
mkdir -p $DATA/handoffs $DATA/worktrees/rehearsal
printf '# Handoff\n\nA handoff pack the rollback must not touch.\n' > $DATA/handoffs/rehearsal-handoff.md
printf 'a managed worktree the rollback must not touch\n' > $DATA/worktrees/rehearsal/README
run "amx project launch beta --profile work --json   (launch_backend = \"tmux\")" 'amx project launch beta --profile work --json'
wcheck "beta runs on the private tmux server and is registered live" 15 '[ "$(panes | grep -c "dead=0.*proj/beta")" = 1 ] && [ "$(registry | grep -c " live .* tmux .*project=beta")" = 1 ]'
B=$(rsof beta); BP=$(pidof beta); BPANE=$(registry | awk '$2=="live" && $6=="project=beta" {print $5}'); beta_before=$(proc "$BP"); server_before=$(priv display-message -p '#{pid}')
echo "beta: runtime session $B, process $beta_before; server pid $server_before" >> "$LOG"
priv send-keys -t "$BPANE" 'ask' Enter
wcheck "the daemon wrote its runtime checkpoint" 20 '[ -s $DATA/runtime-state.json ]'
run "amx session list --json   (builds the transcript index)" 'amx session list --json | cut -c1-400'
wcheck "the transcript index exists and lists the fixture conversation" 15 '[ -s $DATA/transcripts.json ] && amx session list --json | grep -q 11111111-1111-4111-8111-111111111111'
[ -e $DATA/launch_overrides.json ] || printf '{}\n' > $DATA/launch_overrides.json
run "amx project launch alpha --profile work --backend kitty --json" 'klaunch alpha --profile work --backend kitty --json'
wcheck "alpha runs in a Kitty tab" 15 '[ "$(kwin alpha | wc -l | tr -d " ")" = 1 ]'
AW=$(kwin alpha | cut -d' ' -f1); AP=$(kwin alpha | cut -d' ' -f2); alpha_before=$(proc "$AP")
wcheck "the agent in that tab is up" 15 'ktext $AW | grep -q "stand-in claude pid"'

note "2 the earlier amx reads a configuration that still says launch_backend = \"tmux\" (why step 1 comes first)"
run "OLD amx config validate" '$OLD/amx config validate'
run "OLD amx config validate --strict" '$OLD/amx config validate --strict'
check "the earlier amx names launch_backend as a key it does not know" ev '$OLD/amx config validate 2>&1 | grep -q launch_backend'

note "3 rollback step 1, then the state before step 4"
perl -ni -e 'print unless /^launch_backend = "tmux"$/' "$CFG"
check "config.toml validates with both builds" ev 'amx config validate && $OLD/amx config validate'
facts "data directory" listing
sleep 4
sums $SOURCES > $R/keep/sources.before; sums $CANDIDATE_STATE > $R/keep/state.before; cp $DATA/runtime-state.json $R/keep/runtime-state.before.json; sums $DATA/transcripts.json > $R/keep/cache.before
facts "files neither build may rewrite" cat $R/keep/sources.before
facts "state only the candidate knows" cat $R/keep/state.before
facts "transcript index (a disposable cache)" cat $R/keep/cache.before

note "4 rollback step 4: the earlier amx, the earlier app, the daemon started by the earlier amx; the tmux agent left running on purpose"
# `just update` restarts the daemon in one command. It is done in two here so
# that what the candidate's daemon writes as it stops can be told apart from
# what the earlier build does afterwards.
run "amx daemon stop   (the candidate's daemon)" 'amx daemon stop'
sleep 1
sums $CANDIDATE_STATE > $R/keep/state.stopped; cp $DATA/runtime-state.json $R/keep/runtime-state.stopped.json
facts "state only the candidate knows, once its daemon has stopped" cat $R/keep/state.stopped
facts "what the candidate's daemon changed in its checkpoint between the first listing and stopping (report only)" diff $R/keep/runtime-state.before.json $R/keep/runtime-state.stopped.json
cp $OLD/amx $HOME/.local/bin/.amx.staging && mv -f $HOME/.local/bin/.amx.staging $HOME/.local/bin/amx
run "the earlier checkout's install-app.sh" 'AMX_APP_INSTALL_DIR="$INSTALL" AMX_APP_NO_LAUNCH=1 $OLD/macos/scripts/install-app.sh </dev/null'
run "amx daemon restart   (the earlier amx)" 'inkitty amx daemon restart'
wcheck "the daemon is ready" 15 'daemon_ready'
run "amx --version" 'amx --version'
run "amx daemon status" 'amx daemon status'
check "the installed amx is the earlier revision" ev 'amx --version | grep -q "revision $OLDREV"'
check "the daemon runs the earlier revision too" ev 'amx daemon status | grep -q "$OLDREV"'
check "the installed app is the earlier one, with the earlier amx inside" ev '"$DEST/Contents/Resources/bin/amx" --version | grep -q "revision $OLDREV" && [ ! -e "$DEST/Contents/Resources/tmux" ]'
run "amx doctor --strict   (recorded, not judged: the world has no hooks installed, no notifier, and no asl)" 'inkitty amx doctor --strict 2>&1 | tail -22'

note "5 the earlier build works: Kitty launch, hooks, the conversation index, the app"
check "the Kitty session from before the rollback is the same process in the same tab" ev '[ "$(proc "$AP")" = "$alpha_before" ] && [ "$(kwin alpha | cut -d" " -f1)" = "$AW" ]'
K send-text --match id:$AW $'ask\r'
wcheck "its hook reaches the earlier daemon: the tab is marked for permission" 15 'titles | grep -q "🔑🟠alpha"'
run "amx project launch beta --profile work --json   (the earlier amx)" 'klaunch beta --profile work --json'
wcheck "the earlier amx launches beta in a Kitty tab" 15 '[ "$(kwin beta | wc -l | tr -d " ")" = 1 ]'
KB=$(kwin beta | cut -d' ' -f1)
wcheck "the agent in that tab is up" 15 'ktext $KB | grep -q "stand-in claude pid"'
K send-text --match id:$KB $'work\r'
wcheck "and its hook marks the tab working" 15 'titles | grep -q "⚡🟠beta"'
run "amx session list --json   (the earlier amx)" 'amx session list --json | cut -c1-400'
check "the earlier amx lists the fixture conversation" ev 'amx session list --json | grep -q 11111111-1111-4111-8111-111111111111'
run "amx project launch beta --backend tmux   (the earlier amx has no such flag)" 'amx project launch beta --profile work --backend tmux --json'
check "the earlier amx refuses --backend" ev '! amx project launch beta --profile work --backend tmux --json'
run "amx attach $B --json   (the earlier amx has no such command)" 'amx attach "$B" --json'
defaults delete $BUNDLE amxBinaryPath >/dev/null 2>&1
defaults write $BUNDLE amxUseAppNotifications -bool false
app_start
wcheck "the earlier app runs from the install root" 20 '[ -n "$(apppid)" ]'
wcheck "its feed from the earlier daemon is live" 40 'applog | grep -q "feed: live"'
facts "the earlier app's log" applines
app_quit

note "6 what the earlier build did to what it does not know"
check "the tmux agent is the same process on the same server, through the downgrade" ev '[ "$(proc "$BP")" = "$beta_before" ] && [ "$(priv display-message -p "#{pid}")" = "$server_before" ]'
priv send-keys -t "$BPANE" 'still here' Enter
wcheck "it still answers its terminal" 10 'priv capture-pane -p -t "$BPANE" | grep -q "echo\[[0-9]*\]: still here"'
check "the earlier amx's session listing does not show it" ev '! amx session list --json | grep -q "$B"'
sleep 5
sums $SOURCES > $R/keep/sources.after; sums $CANDIDATE_STATE > $R/keep/state.after; sums $DATA/transcripts.json > $R/keep/cache.after
facts "files neither build may rewrite, after" cat $R/keep/sources.after
check "config.toml, the transcript, the handoff, the worktree, the plan, and launch_overrides.json are byte for byte what they were" diff $R/keep/sources.before $R/keep/sources.after
facts "state only the candidate knows, after" cat $R/keep/state.after
check "the runtime-session registry and the runtime checkpoint are byte for byte what the candidate's daemon left" diff $R/keep/state.stopped $R/keep/state.after
facts "transcript index, after (report only: it is a cache either build may refresh)" cat $R/keep/cache.after
facts "data directory" listing
check "the tmux runtime the candidate's app installed is still there" ev '[ -x "$TM" ]'

note "7 forward again: the candidate's amx back, the daemon restarted; the tmux agent is found as it was"
cp $R/keep/amx-candidate $HOME/.local/bin/.amx.staging && mv -f $HOME/.local/bin/.amx.staging $HOME/.local/bin/amx
run "amx daemon restart   (the candidate's amx)" 'inkitty amx daemon restart'
wcheck "the daemon is ready" 15 'daemon_ready'
check "the installed amx is the candidate again" ev '! amx --version | grep -q "revision $OLDREV"'
run "amx attach $B --json" 'amx attach "$B" --json'
check "the candidate finds the tmux session live, the same process" ev 'amx attach "$B" --json | grep -q "\"state\": *\"live\"" && [ "$(proc "$BP")" = "$beta_before" ]'
run "amx backend status" 'amx backend status'
check "the Kitty sessions launched by either build are still there" ev '[ "$(kwin alpha | cut -d" " -f1)" = "$AW" ] && [ "$(kwin beta | cut -d" " -f1)" = "$KB" ]'
K send-text --match id:$KB $'ask\r'
wcheck "a Kitty session launched by the earlier build reports to the candidate's daemon" 15 'titles | grep -q "🔑🟠beta"'
run "amx backend stop --yes" 'amx backend stop --yes'
wcheck "the private server and its agent are gone" 15 '! priv list-panes -a && ! kill -0 "$BP"'
K send-text --match id:$AW $'quit\r'; K send-text --match id:$KB $'quit\r'; sleep 2

printf '\n##### RESULT: %d passed, %d failed\n' "$passed" "$fail" >> "$LOG"; echo "rollback step 4: $passed passed, $fail failed"
