#!/bin/bash
# Session 18 acceptance driver: the real daemon and the packaged tmux, with
# fleets of 1, 10, and 30 stand-in agents, idle and printing, in a world of
# its own. See README.md.
#
#   run.sh REPO OUTDIR
#
# Headless: it shows nothing and sends no input. Everything it starts runs
# with HOME and TMPDIR under /tmp/amxs18 and a PATH of system directories, so
# it reads and writes nothing of the user's AgentMux, and is ended at exit.
set -u
REPO=$(cd "$1" && pwd -P); OUT=$(mkdir -p "$2" && cd "$2" && pwd -P)
HERE=$(cd "$(dirname "$0")" && pwd -P)
R=/tmp/amxs18
WINDOW=${AMX_S18_WINDOW:-30}
BUNDLED="$REPO/macos/build/Agent Mux.app/Contents/Resources/tmux"
PROMPT_CANARY='PROMPT-CANARY-s18'
SECRET_CANARY='sk-s18canary0123456789abcdefghij'

checks=0; failed=0
say()   { printf '%s\n' "$*"; }
head1() { printf '\n== %s ==\n' "$*"; }
check() { local what=$1; shift; checks=$((checks+1)); if "$@" >/dev/null 2>&1; then say "ok   - $what"; else failed=$((failed+1)); say "FAIL - $what"; fi; }
# Everything of the world runs through w: its own HOME and TMPDIR, system PATH.
w() { env -i HOME=$R/home TMPDIR=$R/tmp PATH=$R/bin:/usr/bin:/bin:/usr/sbin:/sbin TERM=xterm-256color LC_ALL=en_US.UTF-8 USER="$USER" "$@"; }
amx() { w $R/app/bin/amx "$@"; }
py() { python3 -I -c "$@"; }
jq1() { py 'import json,sys
d=json.load(sys.stdin)
for k in sys.argv[1].split("."):
    d=d[int(k)] if isinstance(d,list) else d.get(k) if isinstance(d,dict) else None
    if d is None: break
print("" if d is None else json.dumps(d) if isinstance(d,(dict,list)) else d)' "$1"; }
cpu_ms() { ps -o cputime= -p "$1" | py 'import sys
t=sys.stdin.read().strip() or "0"
d,_,t=t.rpartition("-")
p=[float(x) for x in t.split(":")]
s=sum(v*60**i for i,v in enumerate(reversed(p)))+(int(d)*86400 if d else 0)
print(int(s*1000))'; }
rss_mib() { ps -o rss= -p "$1" | awk '{printf "%.1f", $1/1024}'; }
daemon_pid() { amx daemon status 2>/dev/null | sed -n 's/.*pid \([0-9][0-9]*\).*/\1/p' | head -1; }
server_pid() { amx backend status --json 2>/dev/null | jq1 data.server.generation | cut -d- -f1; }
live() { { grep -o '"state": *"live"' $R/home/.local/share/agent-mux/runtime-sessions.json 2>/dev/null || true; } | wc -l | tr -d ' '; }
# t evaluates a condition in this shell, where the helpers above are defined.
t() { eval "$1"; }
doctor_json() { amx doctor --json 2>/dev/null; }
findings() { py 'import json,sys
for f in json.load(sys.stdin)["findings"]: print(f["severity"], f["check"])'; }

down() {
  amx backend stop --yes >/dev/null 2>&1
  amx daemon stop >/dev/null 2>&1
  pkill -f "^$R/" 2>/dev/null; pkill -f "$R/tmp/agent-mux" 2>/dev/null; pkill -f "$R/home/.local/share/agent-mux/runtime" 2>/dev/null
  sleep 1
  rm -rf $R
}
trap down EXIT

# --- the world -------------------------------------------------------------------
head1 "world"
[ ! -e $R ] || { say "$R exists; a previous run did not finish. Remove it first."; trap - EXIT; exit 1; }
mkdir -p $R/app/bin $R/bin $R/home/.config/agent-mux $R/home/.claude-w $R/tmp $R/logs $R/proj || exit 1
chmod 700 $R/tmp
(cd "$REPO" && go build -o $R/app/bin/amx .) || { say "FAIL - go build"; exit 1; }
cp -R "$BUNDLED" $R/app/tmux || { say "FAIL - no packaged tmux at $BUNDLED (run: just mac-app Release)"; exit 1; }
install -m 755 "$HERE/agent.sh" $R/bin/claude
{
  printf '[defaults]\nsort = "alpha"\nlaunch_backend = "tmux"\n\n'
  # Nothing here may ever show a notification on this machine.
  printf '[defaults.notifications]\nalerter_path = "/nonexistent/alerter"\nerror = false\npermission = false\nelicitation = false\nstop = false\nstop_failure = false\nstale_active = false\nhook_silence = false\n\n'
  printf '[defaults.asl]\nenabled = false\n\n'
  printf '[[profiles]]\nname = "work"\nharness = "claude"\nhome_dir = "%s/home/.claude-w"\ncolor = "blue"\n\n' $R
  for i in $(seq 1 30 | awk '{printf "%02d\n", $1}'); do mkdir -p $R/proj/fleet-$i; printf '[[projects]]\ntitle = "fleet-%s"\ndirectory = "%s/proj/fleet-%s"\nprofile = "work"\n\n' $i $R $i; done
  printf '[harness_emojis]\nclaude = "🟠"\n'
} > $R/home/.config/agent-mux/config.toml
say "repo: $(cd "$REPO" && git rev-parse --short HEAD)$(cd "$REPO" && git diff --quiet || echo ' (with uncommitted changes)')"
say "machine: $(sysctl -n machdep.cpu.brand_string), $(sysctl -n hw.ncpu) cores, macOS $(sw_vers -productVersion) $(uname -m)"
say "amx: $(amx --version 2>&1 | head -1)"
say "packaged tmux: $(cat "$BUNDLED/VERSION" | head -1) sha256 $(shasum -a 256 "$BUNDLED/bin/tmux" | cut -c1-16)"
check "the world's amx validates its configuration" amx config validate
check "the profile's hooks are installed" amx config hooks-sync --this-binary
nohup env -i HOME=$R/home TMPDIR=$R/tmp PATH=$R/bin:/usr/bin:/bin:/usr/sbin:/sbin TERM=xterm-256color LC_ALL=en_US.UTF-8 USER="$USER" $R/app/bin/amx daemon start >> $R/logs/daemon.out 2>&1 </dev/null &
n=60; while [ $n -gt 0 ] && [ -z "$(daemon_pid)" ]; do sleep 0.25; n=$((n-1)); done
DPID=$(daemon_pid)
check "the world's daemon is up (pid ${DPID:-none})" [ -n "$DPID" ]
check "it is not the operator's daemon: its executable is the world's" t "ps -o command= -p $DPID | grep -q '^$R/app/bin/amx daemon start'"
status=$(amx backend status --json)
say "backend: tmux $(echo "$status" | jq1 data.tmux.version) from $(echo "$status" | jq1 data.tmux.source), verified=$(echo "$status" | jq1 data.tmux.verified), at $(echo "$status" | jq1 data.tmux.path)"
check "the tmux in use is the packaged runtime, verified, installed outside the app" t "echo '$status' | grep -q '\"source\":\"bundled\"' && echo '$status' | grep -q '\"verified\":true' && echo '$status' | grep -q '$R/home/.local/share/agent-mux/runtime/'"
[ "$failed" = 0 ] || { say "the world did not come up; nothing was measured"; exit 1; }

# --- fleets -----------------------------------------------------------------------
: > "$OUT/production-fleet.tsv"
printf 'sessions\tworkload\tdaemon_cpu_pct\tdaemon_rss_mib\tserver_cpu_pct\tserver_rss_mib\tpasses\tpass_last_ms\tpass_max_ms\ttmux_clients\tclients_at_once\n' >> "$OUT/production-fleet.tsv"
fleet() {
  local n=$1 workload=$2 i
  head1 "$n sessions, $workload"
  echo "$workload" > $R/workload
  for i in $(seq 1 "$n" | awk '{printf "%02d\n", $1}'); do amx project launch "fleet-$i" --profile work --backend tmux --json > $R/logs/launch-$i.json 2>&1 || say "launch fleet-$i failed: $(cat $R/logs/launch-$i.json)"; done
  i=40; while [ $i -gt 0 ] && [ "$(live)" != "$n" ]; do sleep 0.25; i=$((i-1)); done
  check "$n agents launched on the tmux backend and registered live" [ "$(live)" = "$n" ]
  sleep 6   # labels read, screens filled, history full
  local spid; spid=$(server_pid)
  local before; before=$(doctor_json)
  local d0 s0 t0; d0=$(cpu_ms "$DPID"); s0=$(cpu_ms "$spid"); t0=$(py 'import time;print(int(time.time()*1000))')
  sleep "$WINDOW"
  local d1 s1 t1; d1=$(cpu_ms "$DPID"); s1=$(cpu_ms "$spid"); t1=$(py 'import time;print(int(time.time()*1000))')
  local after; after=$(doctor_json)
  local dp sp; dp=$(py 'import sys;a=[int(x) for x in sys.argv[1:]];print("%.2f"%(100*(a[1]-a[0])/(a[3]-a[2])))' $d0 $d1 $t0 $t1); sp=$(py 'import sys;a=[int(x) for x in sys.argv[1:]];print("%.2f"%(100*(a[1]-a[0])/(a[3]-a[2])))' $s0 $s1 $t0 $t1)
  local p0 p1 c0 c1 fl; p0=$(echo "$before" | jq1 daemon.runtime.reconcile.passes); p1=$(echo "$after" | jq1 daemon.runtime.reconcile.passes)
  c0=$(echo "$before" | jq1 daemon.runtime.tmux_client.commands); c1=$(echo "$after" | jq1 daemon.runtime.tmux_client.commands)
  fl=$(echo "$after" | jq1 daemon.runtime.reconcile.failures)
  local passes=$((p1-p0)) clients=$((c1-c0))
  local last max once; last=$(echo "$after" | jq1 daemon.runtime.reconcile.last_duration_ms); max=$(echo "$after" | jq1 daemon.runtime.reconcile.max_duration_ms); once=$(echo "$after" | jq1 daemon.runtime.tmux_client.max_in_flight)
  say "over $(( (t1-t0)/1000 ))s: daemon $dp% CPU ($((d1-d0))ms), $(rss_mib "$DPID") MiB; tmux server $sp% CPU ($((s1-s0))ms), $(rss_mib "$spid") MiB"
  say "reconciliation: $passes passes, last ${last}ms, slowest since start ${max}ms, $fl failed; $clients tmux clients started by the daemon, at most $once at once"
  say "daemon sees: $(echo "$after" | jq1 daemon.runtime.sessions)"
  printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' "$n" "$workload" "$dp" "$(rss_mib "$DPID")" "$sp" "$(rss_mib "$spid")" "$passes" "$last" "$max" "$clients" "$once" >> "$OUT/production-fleet.tsv"
  check "the daemon counts $n live tmux sessions on one server" [ "$(echo "$after" | jq1 daemon.runtime.sessions.tmux)/$(echo "$after" | jq1 daemon.runtime.sessions.servers)" = "$n/1" ]
  check "reconciliation kept its two-second cadence: $passes passes in ${WINDOW}s" [ "$passes" -ge $((WINDOW/2-2)) -a "$passes" -le $((WINDOW/2+2)) ]
  check "no pass failed" [ "$fl" = 0 ]
  check "a pass is one tmux client however many agents: $clients clients for $passes passes" [ "$clients" -le $((passes+1)) ]
  check "never more than 4 tmux clients at once (saw $once)" [ "$once" -le 4 ]
  check "the daemon used under 2% of one core ($dp%)" py "import sys;sys.exit(0 if $dp < 2.0 else 1)"
  LAST_AFTER=$after
}
end_fleet() {
  amx backend stop --yes >/dev/null 2>&1
  local i=40; while [ $i -gt 0 ] && [ "$(live)" != 0 ]; do sleep 0.5; i=$((i-1)); done
  check "the fleet was ended and the daemon recorded every agent as stopped" [ "$(live)" = 0 ]
}

for n in 1 10 30; do
  for workload in idle output; do
    fleet $n $workload
    if [ "$n/$workload" != 30/output ]; then end_fleet; fi
  done
done

# --- diagnostics, with 30 agents printing ---------------------------------------
head1 "diagnostics (30 sessions, output)"
amx doctor > "$OUT/doctor.txt" 2>&1; echo "exit $?" >> "$OUT/doctor.txt"
amx doctor --json > "$OUT/doctor.json" 2>/dev/null
amx debug status > "$OUT/debug-status.txt" 2>&1
amx debug status --json > "$OUT/debug-status.json" 2>/dev/null
amx debug bundle --mode share --output $R/bundle.tar.gz --json > "$OUT/debug-bundle-result.json" 2>&1
mkdir -p $R/bundle && tar -xzf $R/bundle.tar.gz -C $R/bundle 2>/dev/null
grep -E '^(daemon|kitty|backend|runtime sessions|screen captures|versions|overall)' "$OUT/doctor.txt"
check "doctor --strict passes with 30 tmux agents printing and no Kitty: $(tail -1 "$OUT/doctor.txt")" t "amx doctor --strict"
check "no tmux session is reported as a renderer that fell behind" t "! grep -q render_generation_drift '$OUT/doctor.txt'"
check "doctor reports the backend, the sessions and their attachment, captures, and versions" t "grep -q '^backend: launches go to tmux; tmux .* (bundled, verified); server running with 30 agents in 30 sessions' '$OUT/doctor.txt' && grep -q '^runtime sessions: 30 live (30 on tmux, 1 server), 0 with a terminal attached, 30 unattached; reconciled' '$OUT/doctor.txt' && grep -q '^screen captures: ' '$OUT/doctor.txt' && grep -q '^versions: amx .*tmux 3' '$OUT/doctor.txt'"
check "doctor --json carries backend, versions, and daemon.runtime at schema 7" [ "$(jq1 schema_version < "$OUT/doctor.json")/$(jq1 backend.server.agents < "$OUT/doctor.json")/$(jq1 versions.tmux_source < "$OUT/doctor.json")/$(jq1 daemon.runtime.sessions.live < "$OUT/doctor.json")" = "7/30/bundled/30" ]
check "no Kitty in this world is a note, not a failure" t "findings < '$OUT/doctor.json' | grep -q '^info kitty$'"
check "debug status prints the same backend and runtime lines" t "grep -q '^backend: launches go to tmux' '$OUT/debug-status.txt' && grep -q '^runtime sessions: 30 live' '$OUT/debug-status.txt'"
check "the share bundle carries the runtime block and the backend setup" t "grep -q '\"unattached\": 30' $R/bundle/status.json && grep -q '\"launch_backend\": \"tmux\"' $R/bundle/doctor.json && grep -q '\"tmux_source\": \"bundled\"' $R/bundle/doctor.json"
cp $R/bundle/manifest.json "$OUT/debug-bundle-manifest.json" 2>/dev/null
cp $R/bundle/status.json "$OUT/debug-bundle-status.json" 2>/dev/null

head1 "privacy"
# The canaries are in every pane's screen and history, and in every agent's
# UserPromptSubmit hook. They must be nowhere a diagnostic is kept or shared.
TMUXBIN=$(echo "$status" | jq1 data.tmux.path); SOCK=$R/tmp/agent-mux-$(id -u)/tmux.sock
pane=$(w "$TMUXBIN" -S $SOCK list-panes -a -F '#{pane_id}' | head -1)
w "$TMUXBIN" -S $SOCK capture-pane -p -J -t "$pane" > $R/screen.txt 2> $R/screen.err
say "read pane $pane: $(wc -l < $R/screen.txt | tr -d ' ') lines $(head -c 200 $R/screen.err)"
check "the check is not vacuous: a pane's screen holds the prompt and the credential" t "grep -q '$PROMPT_CANARY' $R/screen.txt && grep -q '$SECRET_CANARY' $R/screen.txt"
check "and every agent sent them through the hook path (30 UserPromptSubmit hooks delivered)" [ "$(grep -c 'UserPromptSubmit .*exit=0' $R/logs/hooks.log)" -ge 30 ]
leak() { grep -rl -e "$PROMPT_CANARY" -e "$SECRET_CANARY" "$@" 2>/dev/null; }
check "no canary in doctor, debug status, or the bundle result" t "! grep -l -e '$PROMPT_CANARY' -e '$SECRET_CANARY' '$OUT'/doctor.txt '$OUT'/doctor.json '$OUT'/debug-status.txt '$OUT'/debug-status.json '$OUT'/debug-bundle-result.json"
check "no canary in any member of the share bundle" [ -z "$(leak $R/bundle)" ]
check "no canary in the journals, the daemon log, or anything under the state directory" [ -z "$(leak $R/home/.local/state $R/logs/daemon.out)" ]
check "no canary in the registry, the checkpoint, or anything under the data directory" [ -z "$(leak $R/home/.local/share/agent-mux)" ]
say "state directory: $(cd $R/home/.local/state && find . -type f | wc -l | tr -d ' ') files; bundle members: $(ls $R/bundle | tr '\n' ' ')"
check "the bundle names no home directory or socket path" t "! grep -rl -e '$R' -e '/Users/' $R/bundle"

# --- a server that stops answering ----------------------------------------------
head1 "backend failure: the server stops answering"
SPID=$(server_pid)
kill -STOP "$SPID"
say "tmux server $SPID stopped (SIGSTOP) at $(date '+%H:%M:%S'); every client now times out after 5s"
sleep 40   # three passes that each wait out a client timeout, spaced 4s and 8s apart
amx doctor --strict > "$OUT/doctor-server-stopped.txt" 2>&1; rc=$?
echo "exit $rc" >> "$OUT/doctor-server-stopped.txt"
amx doctor --json > "$OUT/doctor-server-stopped.json" 2>/dev/null
grep -E '^(backend|runtime sessions|screen captures|overall)|backend_|runtime_|tmux_client' "$OUT/doctor-server-stopped.txt"
check "doctor says the server is running but not answering, not that it is empty" grep -q '^backend: .*server running but not answering' "$OUT/doctor-server-stopped.txt"
check "doctor --strict fails (exit $rc) and names the server that does not answer" t "[ $rc -ne 0 ] && findings < '$OUT/doctor-server-stopped.json' | grep -q '^failure backend_server_unreadable$'"
check "the daemon's side is named too: passes failing by timeout, and clients that timed out" t "findings < '$OUT/doctor-server-stopped.json' | grep -q '^warning runtime_reconcile_failing$' && findings < '$OUT/doctor-server-stopped.json' | grep -q '^info tmux_client_timeouts$' && [ \"\$(jq1 daemon.runtime.reconcile.failure_class < '$OUT/doctor-server-stopped.json')\" = timeout ]"
check "no agent was given up on while the server could not be read" [ "$(live)" = 30 ]
check "the daemon itself still answers and is not degraded by it" t "amx daemon status | grep -q 'pid $DPID'"
kill -CONT "$SPID"
say "tmux server continued at $(date '+%H:%M:%S')"
sleep 34   # the next pass is due within the longest wait between failing ones
amx doctor --strict > "$OUT/doctor-server-recovered.txt" 2>&1; rc=$?
echo "exit $rc" >> "$OUT/doctor-server-recovered.txt"
check "after it answers again doctor --strict passes (exit $rc) with the same 30 agents" t "[ $rc -eq 0 ] && grep -q '^runtime sessions: 30 live' '$OUT/doctor-server-recovered.txt'"
amx debug tail --stage daemon.runtime_reconcile --json -n 20 > "$OUT/journal-runtime-reconcile.json" 2>&1
edges=$(py 'import json,sys
def walk(d):
    if isinstance(d,dict):
        if d.get("stage")=="daemon.runtime_reconcile": yield d
        for v in d.values(): yield from walk(v)
    elif isinstance(d,list):
        for v in d: yield from walk(v)
print(" ".join(r.get("reason","")+":"+(r.get("evidence") or {}).get("failure_class","") for r in walk(json.load(open(sys.argv[1])))))' "$OUT/journal-runtime-reconcile.json")
say "journal daemon.runtime_reconcile records: $edges"
check "the journal holds one failure edge (timeout) and one recovery for the whole outage" t "echo '$edges' | grep -q 'reconcile_failed:timeout' && echo '$edges' | grep -q 'reconcile_recovered:' && [ \$(echo '$edges' | tr ' ' '\n' | grep -c reconcile_failed) = 1 ]"

# --- a server that dies -----------------------------------------------------------
head1 "backend failure: the server is killed"
kill -KILL "$SPID"
i=40; while [ $i -gt 0 ] && [ "$(live)" != 0 ]; do sleep 0.5; i=$((i-1)); done
amx doctor > "$OUT/doctor-server-killed.txt" 2>&1; echo "exit $?" >> "$OUT/doctor-server-killed.txt"
grep -E '^(backend|runtime sessions)' "$OUT/doctor-server-killed.txt"
check "every agent is recorded as stopped, none relaunched" [ "$(live)" = 0 ]
check "doctor says no server is running and no session is live" t "grep -q '^backend: .*no server running' '$OUT/doctor-server-killed.txt' && grep -q '^runtime sessions: 0 live' '$OUT/doctor-server-killed.txt'"
amx debug tail --stage daemon.runtime_reconcile --json -n 20 > "$OUT/journal-runtime-reconcile-killed.json" 2>&1
check "the journal records the pass that found 30 agents gone" grep -q '"ended": *"30"' "$OUT/journal-runtime-reconcile-killed.json"
check "no canary in the journal or in any recorded output after the failures either" [ -z "$(leak $R/home/.local/state "$OUT"/*.txt "$OUT"/*.json "$OUT"/*.tsv)" ]

# --- no tmux at all, and no daemon --------------------------------------------------
head1 "backend failure: no usable tmux, daemon down"
mkdir -p $R/home2/.config/agent-mux $R/tmp2 $R/standalone && chmod 700 $R/tmp2
cp $R/app/bin/amx $R/standalone/amx
cp $R/home/.config/agent-mux/config.toml $R/home2/.config/agent-mux/config.toml
env -i HOME=$R/home2 TMPDIR=$R/tmp2 PATH=/usr/bin:/bin:/usr/sbin:/sbin TERM=xterm-256color LC_ALL=en_US.UTF-8 $R/standalone/amx doctor --strict > "$OUT/doctor-no-tmux.txt" 2>&1; rc=$?
echo "exit $rc" >> "$OUT/doctor-no-tmux.txt"
env -i HOME=$R/home2 TMPDIR=$R/tmp2 PATH=/usr/bin:/bin:/usr/sbin:/sbin TERM=xterm-256color LC_ALL=en_US.UTF-8 $R/standalone/amx doctor --json > "$OUT/doctor-no-tmux.json" 2>/dev/null
grep -E '^(daemon|backend|versions)|backend_tmux' "$OUT/doctor-no-tmux.txt"
check "with no daemon and no tmux, doctor --strict fails (exit $rc) on backend_tmux_unavailable and says how to get one" t "[ $rc -ne 0 ] && findings < '$OUT/doctor-no-tmux.json' | grep -q '^failure backend_tmux_unavailable$' && grep -q 'brew install tmux' '$OUT/doctor-no-tmux.txt' && grep -q '^versions: .*tmux not found' '$OUT/doctor-no-tmux.txt'"

head1 "result"
say "$checks checks, $failed failed"
[ "$failed" = 0 ]
