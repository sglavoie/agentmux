#!/bin/bash
# Builds the isolated world under /tmp/amxs15 from this checkout.
# usage: setup.sh REPO_ROOT   (after `just mac-app`)
set -euo pipefail
repo=$1; here=$(cd "$(dirname "$0")" && pwd)
# The operator's real harnesses and the environment they need, recorded
# before the world replaces PATH and HOME.
real_home=$HOME; real_claude_dir=${CLAUDE_CONFIG_DIR:-}
# Without the entries an ASL run adds for its own worker.
real_path=$(printf '%s' "$PATH" | tr ':' '\n' | grep -v -e agent-session-loop -e '/codex/.codex/' | paste -sd: -)
real_claude=$(command -v claude); real_codex=$(command -v codex); real_copilot=$(command -v copilot)
. "$here/env.sh"
mkdir -p $R/bin $R/real $R/home/.config/agent-mux $R/tmp $R/logs $R/app $R/shim \
  $R/proj/claude $R/proj/codex $R/proj/copilot $R/proj/history
(cd "$repo" && go build -o $R/bin/amx .)
for h in claude codex copilot; do install -m 0755 "$here/agent.sh" $R/bin/$h; done
echo "$real_claude" > $R/real/claude; echo "$real_codex" > $R/real/codex; echo "$real_copilot" > $R/real/copilot
{
  echo "export HOME='$real_home'"
  echo "export PATH='$real_path'"
  [ -n "$real_claude_dir" ] && echo "export CLAUDE_CONFIG_DIR='$real_claude_dir'"
  echo "unset XDG_STATE_HOME XDG_CONFIG_HOME XDG_DATA_HOME TMPDIR"
} > $R/real/env
install -m 0755 "$here/amx-shim.sh" $R/shim/amx
install -m 0755 "$here/obs.sh" $R/obs.sh
swiftc -O -o $R/bin/mouse "$here/mouse.swift"
cp "$here/config.toml" $R/home/.config/agent-mux/config.toml
echo $LINKPORT > $R/linkport
# AgentMux starts a harness through a login shell, whose system profile puts
# the system directories first: the world's bin goes back in front, so the
# harness names resolve to agent.sh.
printf "export PATH=$R/bin:\$PATH\nPS1='view %%1~ %%# '\n" > $R/home/.zshrc
# The app under test: this checkout's Debug build under its own bundle
# identifier, so it has its own preferences and runs beside the installed app.
rm -rf "$APP"; ditto "$repo/macos/build/Agent Mux.app" "$APP"
/usr/libexec/PlistBuddy -c "Set :CFBundleIdentifier $BUNDLE" "$APP/Contents/Info.plist"
codesign --force --deep --sign - "$APP" 2>&1 | tail -1
