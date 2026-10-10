#!/bin/bash
# Builds the isolated world under /tmp/amxs13 from this checkout.
# usage: setup.sh REPO_ROOT   (after `just mac-app`)
set -euo pipefail
repo=$1; here=$(cd "$(dirname "$0")" && pwd)
# The installed asl, found on the caller's PATH before the world replaces it.
asl_bin=$(command -v asl)
. "$here/env.sh"
mkdir -p $R/bin $R/home/.config/agent-mux $R/tmp $R/proj/alpha $R/proj/beta $R/zdot $R/logs $R/plans/demo $R/aslrepo $R/app
(cd "$repo" && go build -o $R/bin/amx .)
install -m 0755 "$here/standin-claude.sh" $R/bin/claude
mkdir -p $R/shim && install -m 0755 "$here/amx-shim.sh" $R/shim/amx
swiftc -O -o $R/bin/click "$here/click.swift"
rm -f $R/bin/asl && ln -s "$asl_bin" $R/bin/asl
cp "$here/config.toml" $R/home/.config/agent-mux/config.toml
cp "$here/kitty-zshenv" $R/zdot/.zshenv
for d in $R/proj/alpha $R/proj/beta $R/aslrepo; do
  [ -d $d/.git ] || { git -C $d init -q . && git -C $d -c user.email=s13@example.invalid -c user.name=s13 commit -q --allow-empty -m init; }
done
printf '# Context\n\nAgent Mux acceptance fixture.\n' > $R/plans/demo/context.md
printf '# Manifest\n\n- session-1\n' > $R/plans/demo/manifest.md
printf '# Session 1: demo\n\nFixture session.\n\n## Handoff\n\n- **Status**: pending\n- **Notes**:\n' > $R/plans/demo/session-1.md
# The app under test: this checkout's Debug build under its own bundle
# identifier, so it has its own preferences and runs beside the installed app.
rm -rf "$APP"; ditto "$repo/macos/build/Agent Mux.app" "$APP"
/usr/libexec/PlistBuddy -c "Set :CFBundleIdentifier $BUNDLE" "$APP/Contents/Info.plist"
codesign --force --deep --sign - "$APP" 2>&1 | tail -1
