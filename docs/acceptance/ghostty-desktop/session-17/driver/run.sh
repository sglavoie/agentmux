#!/bin/bash
# Runs the installation acceptance on the MacBook and writes what it observed
# to OUTDIR/install-acceptance.log.
# usage: run.sh OUTDIR [CASE...]
#   cases, in order: install bundle signed diagnostics update server uninstall down
# `setup.sh REPO` must have built the world first.
here=$(cd "$(dirname "$0")" && pwd)
out=$1; shift
. "$here/env.sh"
mkdir -p "$out"
LOG=$out/install-acceptance.log
. "$here/lib.sh"
$SSH "$H" "cat > $R/remote-lib.sh" < "$here/remote-lib.sh"
[ $# -gt 0 ] || set -- install bundle signed diagnostics update server uninstall down
V=$out/.values; touch "$V"
# keep NAME VALUE / kept NAME: values one case hands to a later one.
keep() { grep -v "^$1=" "$V" > "$V.new"; printf '%s=%s\n' "$1" "$2" >> "$V.new"; mv "$V.new" "$V"; }
kept() { sed -n "s/^$1=//p" "$V"; }

for c in "$@"; do case $c in

install)
  note "clean-supported-mac-install: a Mac with no tmux and no Agent Mux.app; the Release bundle is installed with the installer and used"
  facts "the Mac" 'scutil --get ComputerName; sw_vers -productVersion; uname -m; echo "PATH inside the world: $PATH"'
  facts "tmux anywhere on this Mac" 'echo "the user'"'"'s login shell: $(HOME=$REAL_HOME /bin/zsh -lic "command -v tmux" 2>/dev/null || echo none)"; echo "the world: $(command -v tmux || echo none)"; ls -d /opt/homebrew/bin/tmux /usr/local/bin/tmux /opt/local/bin/tmux 2>&1'
  rcheck "no tmux is installed: not on the user's login PATH, not in the world, not from a package manager" '[ -z "$(HOME=$REAL_HOME /bin/zsh -lic "command -v tmux" 2>/dev/null)" ] && ! command -v tmux && [ ! -e /opt/homebrew/bin/tmux ] && [ ! -e /usr/local/bin/tmux ]'
  rcheck "no Agent Mux.app is installed, in /Applications or in the install root" '[ ! -e "/Applications/Agent Mux.app" ] && [ ! -e "$DEST" ]'
  rcheck "the app has never run for this user: it has no preferences" '! defaults read $BUNDLE >/dev/null 2>&1'
  rcheck "no session server, runtime, or registry exists yet" '[ ! -e "$SRV" ] && [ ! -e "$DATA/runtime" ] && [ ! -e "$DATA/runtime-sessions.json" ]'
  keep realstate "$(rq realstate)"
  facts "the bundle under test" 'echo "$SRC"; bundle_version "$SRC"; "$SRC/Contents/Resources/bin/amx" --version; cat "$SRC/Contents/Resources/tmux/VERSION"'
  run "install-app.sh (first install)" 'install_app "$SRC"'
  check "the installer succeeded" [ "$status" = 0 ]
  check "it reports a first install, not an update" has "Installed $R/Applications/Agent Mux.app"
  rcheck "the app is in the install root as a whole bundle" '[ -x "$DEST/Contents/MacOS/Agent Mux" ] && [ -x "$AMX" ] && [ -x "$DEST/Contents/Resources/tmux/bin/tmux" ]'
  rcheck "nothing was left staged beside it" '[ -z "$(ls -A "$INSTALL" | grep -v "^Agent Mux.app$")" ]'
  rcheck "the installer made the tmux runtime ready before the app ever ran" '[ "$(runtimes | wc -l | tr -d " ")" = 1 ] && [ -x "$(runtimes)/bin/tmux" ]'
  rq 'app_defaults; app_start' >> "$LOG" 2>&1
  wcheck "the installed app runs, from the install root" 10 '[ -n "$(apppid)" ]'
  wcheck "it chose the amx inside its own bundle" 30 'applog | grep -q "running .*$DEST/Contents/Resources/bin/amx (bundled)"'
  wcheck "it found its own tmux: bundled, verified" 30 'applog | grep -q "backend: tmux 3.7c (bundled with Agent Mux, verified)"'
  rq 'daemon_start' >> "$LOG" 2>&1
  wcheck "a daemon started by the bundled amx is up and the app's feed is live" 20 'snapshot >/dev/null && applog | grep -q "feed: live"'
  run "amx project launch alpha --backend tmux (the bundled amx)" 'launch alpha; cat $R/logs/launch-alpha.json'
  check "an agent launched on the tmux backend" [ "$status" = 0 ]
  check "the launch reports the tmux backend and a runtime session" ev 'has "\"backend\":\"tmux\"" && has "\"runtime_session_id\":\"rs-"'
  wcheck "the agent is running and counting" 15 '[ "$(lasttick alpha | cut -d" " -f1)" -ge 2 ]'
  wcheck "the daemon lists it, as the app's feed carries it" 15 'snapshot | grep -q "alpha .*runtime=tmux"'
  facts "registry" registry
  facts "amx backend status" '"$AMX" backend status'
  facts "the app's log" applog
  ;;

bundle)
  note "bundled-tmux-and-libraries: the tmux that runs is the pinned one from the bundle, complete in itself, verified, and outside the app"
  facts "the runtime in the bundle" 'cd "$DEST/Contents/Resources/tmux" && ls -R . && cat VERSION && cat SHA256SUMS'
  run "the bundle's checksums, checked" 'cd "$DEST/Contents/Resources/tmux" && shasum -a 256 -c SHA256SUMS'
  check "every file of the bundled runtime matches its recorded checksum" ev '[ "$status" = 0 ] && has "bin/tmux: OK"'
  run "the libraries the bundled tmux loads" 'otool -L "$DEST/Contents/Resources/tmux/bin/tmux"'
  rcheck "it loads system libraries only: its own are linked into it" '[ -z "$(otool -L "$DEST/Contents/Resources/tmux/bin/tmux" | tail -n +2 | awk "{print \$1}" | grep -v "^/usr/lib/")" ]'
  run "what it reports" '"$DEST/Contents/Resources/tmux/bin/tmux" -V; lipo -archs "$DEST/Contents/Resources/tmux/bin/tmux"; vtool -show-build "$DEST/Contents/Resources/tmux/bin/tmux" | grep -E "minos|platform"'
  check "it is the pinned release, built for this architecture and macOS 14" ev 'has "tmux 3.7c" && has arm64 && has "minos 14.0"'
  rcheck "libevent, ncurses, utf8proc, and jemalloc are in it, with their licenses beside it" 'cd "$DEST/Contents/Resources/tmux" && grep -q "^libevent=" VERSION && grep -q "^ncurses=" VERSION && grep -q "^utf8proc=" VERSION && grep -q "^jemalloc=" VERSION && [ "$(ls licenses | wc -l | tr -d " ")" = 5 ] && strings bin/tmux | grep -q "libevent" && strings bin/tmux | grep -qi "jemalloc"'
  facts "the installed copy" 'runtimes; ls -la "$(runtimes)" "$(runtimes)/bin"; cat $DATA/runtime/current'
  run "the installed copy's checksums, checked" 'cd "$(runtimes)" && shasum -a 256 -c SHA256SUMS'
  check "the copy amx installed matches the same checksums" ev '[ "$status" = 0 ] && has "bin/tmux: OK"'
  rcheck "amx reports the bundled runtime as the tmux in use, verified" '[ "$(sfield tmux.source)" = bundled ] && [ "$(sfield tmux.verified)" = true ] && [ "$(sfield tmux.version)" = 3.7c ] && [ "$(sfield tmux.path)" = "$(runtimes)/bin/tmux" ]'
  facts "the session server process" 'serverproc; serverexe; echo "recorded: $(pin)"'
  rcheck "the session server runs the installed copy, not a file inside the app" '[ "$(serverexe)" = "$(runtimes)/bin/tmux" ] && case "$(serverexe)" in "$DEST"*) false;; *) true;; esac'
  rcheck "the executable it was started with is recorded beside its socket" '[ "$(pin)" = "$(runtimes)/bin/tmux" ]'
  run "the allocator in the running server (vmmap names the process's malloc zones)" 'vmmap "$(serverpid)" 2>&1 | grep -m1 -o "malloc zone jemalloc_zone[_0-9a-fx]*"'
  check "the server's allocator is the jemalloc linked into it" has "malloc zone jemalloc_zone"
  rcheck "the agent's terminal is described by the system's terminfo (TERM=tmux-256color resolves)" '[ "$(srv display-message -p -t "$(regline alpha | cut -d" " -f4)" "#{pane_dead}")" = 0 ] && infocmp tmux-256color >/dev/null 2>&1'
  ;;

signed)
  note "signed-bundle: the installed bundle and both executables in it verify, and a bundle that does not is not installed"
  run "codesign --verify --strict --deep (installed bundle)" 'codesign --verify --strict --deep --verbose=2 "$DEST"'
  check "the installed bundle passes strict, deep verification" ev '[ "$status" = 0 ] && has "valid on disk" && has "satisfies its Designated Requirement"'
  run "the three signatures" 'for f in "$DEST" "$AMX" "$DEST/Contents/Resources/tmux/bin/tmux"; do codesign -dv "$f" 2>&1 | grep -E "^(Identifier|Signature|TeamIdentifier|CodeDirectory)"; codesign --verify --strict "$f" && echo "verifies: $f"; done; security find-identity -v -p codesigning 2>&1 | tail -1'
  check "the app, its amx, and its tmux each carry their own identifier" ev 'has "Identifier=io.github.sglavoie.agentmux" && has "Identifier=io.github.sglavoie.agentmux.cli" && has "Identifier=io.github.sglavoie.agentmux.tmux"'
  check "each of the three verifies on its own" ev '[ "$(printf "%s" "$output" | grep -c "^verifies: ")" = 3 ]'
  say "NOTE: this Mac has no signing certificate (0 valid identities), so the signature is ad-hoc: it proves the bundle is intact, not who built it."
  rcheck "the runtime's checksums are under the app's seal" 'grep -q "Resources/tmux/SHA256SUMS" "$DEST/Contents/_CodeSignature/CodeResources"'
  rq 'rm -rf $R/tampered $R/alt; mkdir -p $R/tampered; ditto "$SRC" "$R/tampered/Agent Mux.app"; printf x >> "$R/tampered/Agent Mux.app/Contents/Resources/tmux/bin/tmux"'
  run "install-app.sh with a bundle whose tmux was altered" 'install_app "$R/tampered/Agent Mux.app" $R/alt'
  check "the installer refuses it" ev '[ "$status" != 0 ] && has "does not match its checksums"'
  rcheck "and installed nothing" '[ ! -e "$R/alt/Agent Mux.app" ] && [ -z "$(ls -A $R/alt 2>/dev/null)" ]'
  rq 'rm -rf $R/tampered; mkdir -p $R/tampered; ditto "$SRC" "$R/tampered/Agent Mux.app"; echo "tampered" >> "$R/tampered/Agent Mux.app/Contents/Resources/NOTICE-ghostty"'
  run "install-app.sh with a bundle whose sealed resource was altered" 'install_app "$R/tampered/Agent Mux.app" $R/alt'
  check "the installer refuses that too" ev '[ "$status" != 0 ] && has "does not pass signature verification"'
  rcheck "and installed nothing" '[ ! -e "$R/alt/Agent Mux.app" ]'
  rq 'rm -rf $R/tampered; mkdir -p $R/tampered $R/home3; ditto "$SRC" "$R/tampered/Agent Mux.app"; cd "$R/tampered/Agent Mux.app/Contents/Resources/tmux"; printf x >> bin/tmux; shasum -a 256 VERSION bin/tmux licenses/* > SHA256SUMS'
  run "amx given a runtime that was altered and its checksums rewritten to match (the app's seal no longer holds)" 'install_app "$R/tampered/Agent Mux.app" $R/alt'
  check "the installer refuses it by the seal" ev '[ "$status" != 0 ] && has "does not pass signature verification"'
  rq 'rm -rf $R/tampered; mkdir -p $R/tampered; ditto "$SRC" "$R/tampered/Agent Mux.app"; printf x >> "$R/tampered/Agent Mux.app/Contents/Resources/tmux/bin/tmux"'
  run "amx itself, pointed at a runtime that does not match its checksums" 'HOME=$R/home3 AMX_TMUX_RUNTIME="$R/tampered/Agent Mux.app/Contents/Resources/tmux" "$AMX" backend status; ls -A $R/home3/.local/share/agent-mux/runtime 2>&1'
  check "amx refuses to run or install it, and says which file" ev 'has "bin/tmux does not match its checksum" && has "tmux: not available"'
  rq 'rm -rf $R/tampered $R/alt $R/home3'
  ;;

diagnostics)
  note "dependency-failure-diagnostics: with no usable tmux, every surface says what was tried and what to do"
  rq 'mkdir -p $R/standalone $R/home2/.config/agent-mux $R/old $R/tmp2; chmod 700 $R/tmp2; cp "$AMX" $R/standalone/amx; cp $HOME/.config/agent-mux/config.toml $R/home2/.config/agent-mux/config.toml; printf "#!/bin/sh\necho tmux 3.3a\n" > $R/old/tmux; chmod +x $R/old/tmux; printf "export PATH=$R/bin:\$PATH\n" > $R/home2/.zshrc'
  say "a standalone amx (a copy outside any bundle), a HOME with no runtime, a TMPDIR with no session server, and no tmux on PATH:"
  run "amx backend status" 'HOME=$R/home2 TMPDIR=$R/tmp2 $R/standalone/amx backend status'
  check "status succeeds and explains: tmux is not available, each place tried, and the fix" ev '[ "$status" = 0 ] && has "tmux: not available (need 3.5 or newer)" && has "bundled runtime: not present" && has "PATH: tmux not found" && has "brew install tmux" && has "[defaults.tmux] path" && has "Agent Mux.app, which bundles one"'
  run "amx backend status --check --json" 'HOME=$R/home2 TMPDIR=$R/tmp2 $R/standalone/amx backend status --check --json'
  check "--check exits 1 and the report names the problem by code" ev '[ "$status" = 1 ] && has "\"code\":\"tmux_unavailable\"" && has "\"available\":false"'
  run "amx project launch alpha --backend tmux --json" 'HOME=$R/home2 TMPDIR=$R/tmp2 $R/standalone/amx project launch alpha --profile work --backend tmux --json'
  check "a tmux launch fails as backend_unavailable with the same explanation" ev '[ "$status" != 0 ] && has "\"code\":\"backend_unavailable\"" && has "PATH: tmux not found" && has "brew install tmux"'
  rcheck "the failed launch started nothing and registered nothing" '[ ! -e $R/home2/.local/share/agent-mux/runtime-sessions.json ] || ! grep -q "\"live\"\|\"pending\"" $R/home2/.local/share/agent-mux/runtime-sessions.json'
  run "amx project launch (text)" 'HOME=$R/home2 TMPDIR=$R/tmp2 $R/standalone/amx project launch alpha --profile work --backend tmux'
  check "the text form says the same" ev '[ "$status" != 0 ] && has "tmux backend unavailable" && has "Install tmux 3.5 or newer"'
  say "a tmux on PATH that is too old:"
  run "amx backend status (tmux 3.3a on PATH)" 'HOME=$R/home2 TMPDIR=$R/tmp2 PATH=$R/old:$PATH $R/standalone/amx backend status'
  check "it is refused by version, not run" ev 'has "tmux 3.3a is older than 3.5" && has "tmux: not available"'
  say "a configured tmux that does not exist:"
  rq 'printf "\n[defaults.tmux]\npath = \"/nonexistent/tmux\"\n" >> $R/home2/.config/agent-mux/config.toml'
  run "amx backend status ([defaults.tmux] path = /nonexistent/tmux)" 'HOME=$R/home2 TMPDIR=$R/tmp2 AMX_TMUX_RUNTIME="$DEST/Contents/Resources/tmux" $R/standalone/amx backend status'
  check "the configured path is the only one tried, and the bundled runtime is not silently used instead" ev 'has "[defaults.tmux] path (/nonexistent/tmux): not an executable file" && has "tmux: not available" && ! has "bundled runtime"'
  say "a configuration that names an unknown backend:"
  rq 'cp $HOME/.config/agent-mux/config.toml $R/home2/.config/agent-mux/config.toml; sed -i "" "s/^\[defaults\]/[defaults]\nlaunch_backend = \"ghostty\"/" $R/home2/.config/agent-mux/config.toml'
  run "amx backend status (launch_backend = ghostty)" 'HOME=$R/home2 TMPDIR=$R/tmp2 $R/standalone/amx backend status'
  check "the configuration error is reported with the value and how to see every error" ev 'has "defaults.launch_backend \"ghostty\"" && has "amx config validate"'
  say "the app, given a tmux it cannot use (AMX_TMUX in its environment preference):"
  rq 'app_quit; defaults write $BUNDLE amxEnvironment -dict AMX_TMUX /nonexistent/tmux; app_start' >> "$LOG" 2>&1
  wcheck "the app records the problem and its fix where an operator looks" 30 'applog | grep -q "backend: error tmux_unavailable\|backend: warning tmux_unavailable" && applog | grep -q "AMX_TMUX (/nonexistent/tmux): not an executable file" && applog | grep -q "brew install tmux"'
  facts "the app's log" 'applog | grep backend'
  rq 'app_quit; defaults delete $BUNDLE amxEnvironment; app_start' >> "$LOG" 2>&1
  wcheck "with the preference removed the app is back on its own tmux" 30 'applog | grep -q "backend: tmux 3.7c (bundled with Agent Mux, verified)"'
  rcheck "the agent launched earlier was not disturbed by any of this" '[ "$(tickgaps alpha)" = 0 ]'
  rcheck "nothing was started in the standalone world" '[ ! -e $R/tmp2/agent-mux-$(id -u)/tmux.sock ]'
  rq 'rm -rf $R/standalone $R/home2 $R/old $R/tmp2'
  ;;

update)
  note "update-preserves-agent: the app is updated by the installer while an agent runs detached; the agent never notices"
  keep agent "$(rq 'agentproc alpha')"; keep rs "$(rq 'rsof alpha')"; keep reg "$(rq 'regline alpha')"
  keep server "$(rq serverproc)"; keep serverexe "$(rq serverexe)"; keep generation "$(rq 'sfield server.generation')"
  keep runtime1 "$(rq runtimes)"; keep daemon "$(rq daemonpid)"; keep apppid "$(rq apppid)"
  say "before: agent (pid, start UTC) $(kept agent); runtime session $(kept rs)"
  say "before: session server (pid, start UTC) $(kept server); generation $(kept generation); executable $(kept serverexe)"
  say "before: app pid $(kept apppid), version $(rq 'bundle_version "$DEST"'); daemon pid $(kept daemon); last count $(rq 'lasttick alpha')"
  rcheck "no terminal is attached to the agent: it is detached" '[ -z "$(srv list-clients)" ]'
  run "the update is built: another version, and a tmux runtime with other checksums, signed by sign.sh" 'make_v2 2>&1 | tail -3; bundle_version "$V2"; cat "$V2/Contents/Resources/tmux/VERSION" | tail -1; shasum -a 256 "$SRC/Contents/Resources/tmux/SHA256SUMS" "$V2/Contents/Resources/tmux/SHA256SUMS"'
  check "the update is version 0.1.1" has "0.1.1"
  run "install-app.sh (update, with the app running)" 'install_app "$V2"'
  check "the installer succeeded" [ "$status" = 0 ]
  check "it saw the running server and said it would leave it" has "A session server is running with 1 agent(s); it is left running."
  check "it quit the running app" has "Quitting the running Agent Mux.app"
  check "it reports an update" has "Updated $R/Applications/Agent Mux.app"
  check "it confirmed the server is the one that was running" has "The session server is unchanged ($(kept generation)); 1 agent(s) running."
  rcheck "the installed bundle is the new version, whole and verified" '[ "$(bundle_version "$DEST")" = 0.1.1 ] && codesign --verify --strict --deep "$DEST" && [ -z "$(ls -A "$INSTALL" | grep -v "^Agent Mux.app$")" ]'
  rcheck "the old app process is gone" '! kill -0 '"$(kept apppid)"' 2>/dev/null'
  check "the agent is the same process" ev '[ "$(rq "agentproc alpha")" = "$(kept agent)" ]'
  check "its registry record is unchanged: live, same pane, same pid, same server generation" ev '[ "$(rq "regline alpha")" = "$(kept reg)" ]'
  sleep 3
  rcheck "its count never started over or skipped through the update" '[ "$(tickgaps alpha)" = 0 ]'
  rcheck "and never paused: no two counts are more than 3 seconds apart" '[ "$(ticklate alpha)" = 0 ]'
  say "after: agent $(rq 'agentproc alpha'); last count $(rq 'lasttick alpha')"
  rq 'app_start' >> "$LOG" 2>&1
  wcheck "the updated app runs, on its own bundled amx" 30 '[ -n "$(apppid)" ] && applog | grep -q "running .*$DEST/Contents/Resources/bin/amx (bundled)"'
  wcheck "its feed is live and carries the same session" 30 'applog | grep -q "feed: live" && snapshot | grep -q "alpha .*runtime=tmux .* '"$(kept rs)"'"'
  run "amx attach --json from the updated bundle (what the app runs before it shows a session)" '"$AMX" attach '"$(kept rs)"' --json'
  check "the updated amx finds the agent live, same pid, with no terminal attached" ev '[ "$status" = 0 ] && has "\"state\":\"live\"" && has "\"pid\":$(kept agent | cut -d" " -f1)" && has "\"clients\":0"'
  facts "the daemon through the update" 'echo "pid before '"$(kept daemon)"', now $(daemonpid)"; ps -o pid=,lstart=,comm= -p "$(daemonpid)"'
  facts "the app's log" applog
  ;;

server)
  note "live-server-not-replaced: the update shipped another tmux runtime; the running server is the same process on the same executable"
  check "the session server is the same process: same pid, same start time" ev '[ "$(rq serverproc)" = "$(kept server)" ]'
  check "its generation, which every session's target is bound to, is unchanged" ev '[ "$(rq "sfield server.generation")" = "$(kept generation)" ]'
  check "it still runs the executable it was started with" ev '[ "$(rq serverexe)" = "$(kept serverexe)" ]'
  rcheck "that executable is still there, untouched, and still matches its checksums" 'cd "'"$(kept runtime1)"'" && shasum -a 256 -c SHA256SUMS >/dev/null'
  facts "runtimes installed now" 'runtimes; echo "current: $(cat $DATA/runtime/current)"; echo "recorded for the server: $(pin)"'
  rcheck "the update's runtime was installed beside it, not over it" '[ "$(runtimes | wc -l | tr -d " ")" = 2 ] && [ "$(sfield tmux.path)" != "'"$(kept serverexe)"'" ] && [ -x "$(sfield tmux.path)" ]'
  rcheck "the record beside the socket still names the server's own executable" '[ "$(pin)" = "'"$(kept serverexe)"'" ]'
  run "amx backend status (updated bundle)" '"$AMX" backend status'
  check "amx says so: the server keeps its tmux, and the new one waits for the last agent to end" ev 'has "server tmux: $(kept serverexe)" && has "info: The running server keeps the tmux it was started with"'
  rcheck "the updated app says the same in its log" 'applog | grep -q "backend: info server_keeps_previous_tmux"'
  run "a second agent launched after the update" 'launch beta; cat $R/logs/launch-beta.json'
  check "launching still works against the running server" ev '[ "$status" = 0 ] && has "\"runtime_session_id\":\"rs-"'
  wcheck "the new agent runs in the same server, beside the first" 15 '[ "$(srv list-panes -a -F "#{pane_dead}" | grep -c "^0$")" = 2 ] && [ "$(serverproc)" = "'"$(kept server)"'" ] && [ "$(lasttick beta | cut -d" " -f1)" -ge 1 ]'
  rcheck "and the first agent still counts without a gap" '[ "$(tickgaps alpha)" = 0 ] && [ "$(ticklate alpha)" = 0 ]'
  facts "registry" registry
  ;;

uninstall)
  note "uninstall-preserves-data: removing the app removes the app; ending agents and removing data are separate, explicit choices"
  keep userdata "$(rq userdata)"; keep agent "$(rq 'agentproc alpha')"; keep server "$(rq serverproc)"
  say "before: user data digest $(kept userdata); agent alpha $(kept agent); server $(kept server)"
  facts "the operator's files" 'cd $HOME && find .config/agent-mux .local/share/agent-mux -maxdepth 2 | LC_ALL=C sort'

  say "1. asking to remove the app's data while agents run on its runtime, without asking to end them:"
  run "uninstall-app.sh --remove-app-data --yes" 'uninstall_app --remove-app-data --yes'
  check "it is refused, and says what to choose" ev '[ "$status" != 0 ] && has "A session server is running with 2 agent(s)" && has "--end-sessions"'
  rcheck "nothing was removed: the app, its preferences, and the runtime are all there" '[ -x "$AMX" ] && defaults read $BUNDLE >/dev/null 2>&1 && [ "$(runtimes | wc -l | tr -d " ")" = 2 ]'

  say "2. the default: remove the app, nothing else:"
  run "uninstall-app.sh" 'uninstall_app'
  check "the uninstaller succeeded" [ "$status" = 0 ]
  check "it says what it removed and what it left" ev 'has "Removed $R/Applications/Agent Mux.app" && has "Left running: the session server and its 2 agent(s)." && has "Kept the app'"'"'s preferences" && has "Kept: your configuration"'
  check "it prints the one command that ends the agents later without amx" has "-S '$R/tmp/agent-mux-"
  rcheck "the app is gone and no longer running" '[ ! -e "$DEST" ] && [ -z "$(pgrep -f "Agent Mux.app/Contents/MacOS/Agent Mux")" ]'
  check "the agents were not ended: alpha is the same process" ev '[ "$(rq "agentproc alpha")" = "$(kept agent)" ]'
  check "the session server is the same process" ev '[ "$(rq serverproc)" = "$(kept server)" ]'
  sleep 3
  rcheck "both agents kept counting through the uninstall" '[ "$(tickgaps alpha)" = 0 ] && [ "$(ticklate alpha)" = 0 ] && [ "$(tickgaps beta)" = 0 ]'
  check "the operator's files are byte for byte what they were" ev '[ "$(rq userdata)" = "$(kept userdata)" ]'
  rcheck "the session registry is still there, with both sessions live" '[ "$(registry | grep -c " live ")" = 2 ]'
  rcheck "the app's preferences and both runtimes were kept" 'defaults read $BUNDLE amxUseAppNotifications >/dev/null 2>&1 && [ "$(runtimes | wc -l | tr -d " ")" = 2 ]'

  say "3. ending the agents is its own choice, and asks:"
  run "uninstall-app.sh --end-sessions (no --yes, no terminal to ask on)" 'uninstall_app --end-sessions'
  check "without confirmation nothing is ended" ev '[ "$status" != 0 ] && has "needs confirmation: rerun with --yes"'
  check "alpha is still the same process" ev '[ "$(rq "agentproc alpha")" = "$(kept agent)" ]'
  run "uninstall-app.sh --end-sessions --yes (the app is already gone: the server is asked with its own recorded tmux)" 'uninstall_app --end-sessions --yes'
  check "it ended the server and said how many agents" ev '[ "$status" = 0 ] && has "Stopped the session server and its 2 running agent(s)."'
  wcheck "the server and both agents are gone" 10 '! srv list-sessions >/dev/null 2>&1 && ! kill -0 '"$(kept agent | cut -d" " -f1)"' 2>/dev/null'
  check "the operator's files are still what they were" ev '[ "$(rq userdata)" = "$(kept userdata)" ]'
  rcheck "the preferences and runtimes are still kept: only the agents were asked for" 'defaults read $BUNDLE >/dev/null 2>&1 && [ "$(runtimes | wc -l | tr -d " ")" = 2 ]'

  say "4. installed again, a new server starts on the update's runtime; then everything the app owns is removed on request:"
  run "install-app.sh (the update again, as a first install)" 'install_app "$V2"'
  check "it installs, with no server to preserve" ev '[ "$status" = 0 ] && has "Installed" && ! has "session server"'
  run "amx project launch beta --backend tmux" 'launch beta; cat $R/logs/launch-beta.json'
  wcheck "a new server is running" 15 '[ -n "$(serverpid)" ]'
  keep server2 "$(rq serverexe)"
  check "the new server runs the update's runtime: the earlier one was kept only for the server that had started on it" ev '[ "$(kept server2)" != "$(kept serverexe)" ] && [ "$(kept server2)" = "$(rq "sfield tmux.path")" ]'
  keep agent2 "$(rq 'agentpid beta')"
  run "uninstall-app.sh --end-sessions --remove-app-data --yes" 'uninstall_app --end-sessions --remove-app-data --yes'
  check "it did all three and said so" ev '[ "$status" = 0 ] && has "Stopped the private tmux server and 1 running agent" && has "Removed $R/Applications/Agent Mux.app" && has "Removed the app'"'"'s preferences"'
  facts "after removing everything the app owns" 'ls -d "$DEST" "$DATA/runtime" 2>&1; defaults read $BUNDLE 2>&1 | head -3; ls -la "$REAL_HOME/Library/Preferences/$BUNDLE.plist" 2>&1'
  rcheck "the app, every preference it had saved, and its runtimes are gone" '[ ! -e "$DEST" ] && [ -z "$(defaults read $BUNDLE 2>/dev/null | grep -v "^[{}]$")" ] && [ ! -e "$DATA/runtime" ]'
  rcheck "the agent it was asked to end is gone" '! kill -0 '"$(kept agent2)"' 2>/dev/null'
  check "the operator's files are still byte for byte what they were" ev '[ "$(rq userdata)" = "$(kept userdata)" ]'
  rcheck "the registry file is kept as well" '[ -f "$DATA/runtime-sessions.json" ]'
  facts "what is left" 'cd $HOME && find .config/agent-mux .local/share/agent-mux -maxdepth 2 | LC_ALL=C sort; ls -A "$INSTALL"'
  ;;

down)
  note "the world is taken down and the MacBook is as it was"
  rr >> "$LOG" 2>&1 <<'REMOTE'
p=$(daemonpid); [ -n "$p" ] && kill "$p"
pkill -f "$R/" 2>/dev/null
sleep 1
defaults delete $BUNDLE >/dev/null 2>&1
rm -f "$REAL_HOME/Library/Preferences/$BUNDLE.plist"
REMOTE
  rcheck "no process of the world is left" '[ -z "$(worldprocs)" ]'
  rcheck "the app left no preference behind" '[ -z "$(defaults read $BUNDLE 2>/dev/null | grep -v "^[{}]$")" ] && [ ! -e "$REAL_HOME/Library/Preferences/$BUNDLE.plist" ]'
  rcheck "nothing was installed in /Applications" '[ ! -e "/Applications/Agent Mux.app" ]'
  check "the MacBook user's own amx, configuration, and data were never touched" ev '[ "$(rq realstate)" = "$(kept realstate)" ]'
  $SSH "$H" "rm -rf $R" </dev/null
  check "the world is removed" ev '! $SSH "$H" "test -e $R" </dev/null'
  ;;

*) echo "unknown case $c"; exit 2 ;;
esac; done

say ""
say "RESULT: $passed passed, $fail failed"
[ "$fail" = 0 ]
