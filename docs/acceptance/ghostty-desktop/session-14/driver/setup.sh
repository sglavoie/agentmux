#!/bin/bash
# Builds the isolated world under /tmp/amxs14 from this checkout.
# usage: setup.sh REPO_ROOT   (after `just mac-app`)
set -euo pipefail
repo=$1; here=$(cd "$(dirname "$0")" && pwd)
# The installed asl, found on the caller's PATH before the world replaces it.
asl_bin=$(command -v asl)
. "$here/env.sh"
mkdir -p $R/bin $R/home/.config/agent-mux $R/tmp $R/proj/alpha $R/proj/beta $R/logs $R/plans/demo $R/aslrepo $R/app $R/shim
(cd "$repo" && go build -o $R/bin/amx .)
install -m 0755 "$here/standin-claude.sh" $R/bin/claude
install -m 0755 "$here/standin-editor.sh" $R/bin/edit-standin
install -m 0755 "$here/amx-shim.sh" $R/shim/amx
install -m 0755 "$here/obs.sh" $R/obs.sh
swiftc -O -o $R/bin/click "$here/click.swift"
rm -f $R/bin/asl && ln -s "$asl_bin" $R/bin/asl
cp "$here/config.toml" $R/home/.config/agent-mux/config.toml
# The shell view runs the operator's login shell with the world's HOME: give
# it a prompt that names nobody.
printf "PS1='view %%1~ %%# '\n" > $R/home/.zshrc
for d in $R/proj/alpha $R/proj/beta $R/aslrepo; do
  [ -d $d/.git ] || { git -C $d init -q . && git -C $d -c user.email=s14@example.invalid -c user.name=s14 commit -q --allow-empty -m init; }
done
printf '# Context\n\nAgent Mux acceptance fixture.\n' > $R/plans/demo/context.md
printf '# Manifest\n\n- session-1\n' > $R/plans/demo/manifest.md
printf '# Session 1: demo\n\nFixture session.\n\n## Handoff\n\n- **Status**: pending\n- **Notes**:\n' > $R/plans/demo/session-1.md
# One closed Claude conversation of project alpha, for Past Sessions.
conv=11111111-1111-4111-8111-111111111111
tdir=$R/home/.claude-a/projects/-tmp-amxs14-proj-alpha
mkdir -p "$tdir"
cat > "$tdir/$conv.jsonl" <<JSON
{"type":"user","entrypoint":"cli","sessionId":"$conv","cwd":"$R/proj/alpha","timestamp":"2026-10-09T12:00:00.000Z","uuid":"aaaaaaaa-0000-4000-8000-000000000001","parentUuid":null,"message":{"role":"user","content":"acceptance fixture prompt"}}
{"type":"assistant","sessionId":"$conv","cwd":"$R/proj/alpha","timestamp":"2026-10-09T12:00:05.000Z","uuid":"aaaaaaaa-0000-4000-8000-000000000002","parentUuid":"aaaaaaaa-0000-4000-8000-000000000001","message":{"role":"assistant","content":[{"type":"text","text":"acceptance fixture answer"}]}}
JSON
# The app under test: this checkout's Debug build under its own bundle
# identifier, so it has its own preferences and runs beside the installed app.
rm -rf "$APP"; ditto "$repo/macos/build/Agent Mux.app" "$APP"
/usr/libexec/PlistBuddy -c "Set :CFBundleIdentifier $BUNDLE" "$APP/Contents/Info.plist"
codesign --force --deep --sign - "$APP" 2>&1 | tail -1
