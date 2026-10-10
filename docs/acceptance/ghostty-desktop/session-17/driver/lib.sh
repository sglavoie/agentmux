# Helpers for the driver, which runs on the Studio and acts only on the
# MacBook. Source after env.sh. State is read over SSH (remote-lib.sh).
LOG=${LOG:?the acceptance log}
note() { printf '\n##### %s [%s]\n' "$*" "$(date -u +%H:%M:%SZ)" >> "$LOG"; echo "## $*"; }
say() { printf '%s\n' "$*" >> "$LOG"; echo "$*"; }
# facts TITLE 'REMOTE COMMAND': what the MacBook reports, copied into the log.
facts() { printf -- '--- %s ---\n' "$1" >> "$LOG"; rq "$2" >> "$LOG" 2>&1; }
fail=0; passed=0
# check WHAT CONDITION...: CONDITION is evaluated here, on values read earlier.
check() { local what=$1; shift; if "$@" >/dev/null 2>&1; then say "PASS: $what"; passed=$((passed+1)); else say "FAIL: $what"; fail=$((fail+1)); fi; }
ev() { eval "$1"; }
waitfor() { local n=$(( $1 * 2 )); shift; while [ $n -gt 0 ]; do "$@" >/dev/null 2>&1 && return 0; sleep 0.5; n=$((n-1)); done; return 1; }
# rtest 'REMOTE CONDITION': a condition evaluated on the MacBook.
rtest() { rq "$1" >/dev/null 2>&1; }
# rcheck WHAT 'REMOTE CONDITION': evaluated on the MacBook, once.
rcheck() { if rtest "$2"; then say "PASS: $1"; passed=$((passed+1)); else say "FAIL: $1"; fail=$((fail+1)); fi; }
# wcheck WHAT SECONDS 'REMOTE CONDITION': waits for it, then records it.
wcheck() { local what=$1 secs=$2; shift 2; if waitfor "$secs" rtest "$1"; then say "PASS: $what"; passed=$((passed+1)); else say "FAIL: $what (not within ${secs}s)"; fail=$((fail+1)); fi; }
# run TITLE 'REMOTE COMMAND': runs it, copies its output into the log, and
# leaves its exit status in $status and its output in $output.
run() { printf -- '--- %s ---\n' "$1" >> "$LOG"; output=$(rq "$2" 2>&1); status=$?; printf '%s\n(exit %s)\n' "$output" "$status" >> "$LOG"; }
has() { printf '%s' "$output" | grep -qF -- "$1"; }
