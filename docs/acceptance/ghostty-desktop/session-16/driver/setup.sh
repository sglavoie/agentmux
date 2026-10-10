#!/bin/bash
# Builds the isolated world on the MacBook from this checkout.
# usage: setup.sh REPO_ROOT
# amx is built here on the Studio (headless) and copied over. The Debug app is
# built on the MacBook, in the isolated copy of this checkout that
# macos/scripts/remote-ui.sh keeps there: nothing of Xcode's runs on the
# Studio. Nothing is installed on the MacBook outside /tmp/amxs16 and that
# copy: tmux is a relocated copy of the Studio's Homebrew tmux with its four
# libraries beside it. The agents are stand-ins (agent.sh): no harness runs.
set -euo pipefail
repo=$1; here=$(cd "$(dirname "$0")" && pwd)
. "$here/env.sh"
RS="rsync -a -e '$SSH'"
mkdir -p $L/bin $L/logs
(cd "$repo" && go build -o $L/bin/amx .)
"$repo/macos/scripts/remote-ui.sh" run macos/scripts/build-app.sh Debug > $L/logs/build-app.log 2>&1 || { tail -20 $L/logs/build-app.log; exit 1; }
copy=$("$repo/macos/scripts/remote-ui.sh" dir)
# tmux, relocated.
B=$L/tmux; rm -rf $B; mkdir -p $B/bin $B/lib $B/share
src=$(python3 -c "import os;print(os.path.realpath('/opt/homebrew/bin/tmux'))")
cp "$src" $B/bin/tmux; chmod u+w $B/bin/tmux
for l in utf8proc/lib/libutf8proc.3.dylib ncurses/lib/libncursesw.6.dylib libevent/lib/libevent_core-2.1.7.dylib jemalloc/lib/libjemalloc.2.dylib; do
  n=$(basename $l); cp -L /opt/homebrew/opt/$l $B/lib/$n; chmod u+w $B/lib/$n
  install_name_tool -id "@rpath/$n" $B/lib/$n 2>/dev/null
  install_name_tool -change /opt/homebrew/opt/$l "@executable_path/../lib/$n" $B/bin/tmux 2>/dev/null
  codesign -f -s - $B/lib/$n 2>/dev/null
done
codesign -f -s - $B/bin/tmux 2>/dev/null
cp -RL /opt/homebrew/opt/ncurses/share/terminfo $B/share/terminfo
$SSH "$H" "mkdir -p $R/bin $R/home/.config/agent-mux $R/tmp $R/logs $R/app $R/shim $R/proj/alpha $R/proj/beta $R/proj/gamma" </dev/null
eval "$RS $B/ '$H:$R/tmux/'" </dev/null
eval "$RS $L/bin/amx '$H:$R/bin/amx'" </dev/null
$SSH "$H" "rm -rf '$R/app/Agent Mux.app' && ditto \"\$HOME/$copy/macos/build/Agent Mux.app\" '$R/app/Agent Mux.app'" </dev/null
eval "$RS '$here/remote-env.sh' '$here/obs.sh' '$here/agent.sh' '$here/amx-shim.sh' '$here/config.toml' '$here/dview.py' '$here/remote-lib.sh' '$H:$R/'" </dev/null
{ echo ". $R/remote-env.sh"; cat; } <<'REMOTE' | $SSH "$H" /bin/sh -s
set -e
install -m 0755 $R/agent.sh $R/bin/claude
install -m 0755 $R/amx-shim.sh $R/shim/amx; chmod +x $R/obs.sh
cp $R/config.toml $R/home/.config/agent-mux/config.toml
# AgentMux starts a harness through a login shell, whose system profile puts
# the system directories first: the world's bin goes back in front.
printf "export PATH=$R/bin:$R/tmux/bin:\$PATH\nexport TERMINFO_DIRS=$TERMINFO_DIRS\nPS1='view %%1~ %%# '\n" > $R/home/.zshrc
/usr/libexec/PlistBuddy -c "Set :CFBundleIdentifier $BUNDLE" "$APP/Contents/Info.plist"
codesign --force --deep --sign - "$APP" 2>&1 | tail -1
amx --version; tmux -V; scutil --get ComputerName
REMOTE
