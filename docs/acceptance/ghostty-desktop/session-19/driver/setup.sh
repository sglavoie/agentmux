#!/bin/bash
# Runs on the MacBook. Builds the world under /tmp/amxs19 around the packaged
# Release app: the bundle `just mac-app Release` left in the checkout copy is
# installed with the repository's own installer into the world's install
# root, and its amx and its tmux are the only ones the world has.
# usage: setup.sh CHECKOUT_COPY   (the copy remote-ui.sh keeps, absolute)
set -euo pipefail
copy=$1
R=/tmp/amxs19
[ ! -e $R/home ] || { echo "$R already holds a world; run down.sh first"; exit 1; }
mkdir -p $R/bin $R/home/.config/agent-mux $R/home/plans/demo-plan $R/tmp $R/out $R/logs $R/Applications $R/proj/alpha $R/proj/beta $R/zdot
chmod 700 $R/tmp
echo "$copy" > $R/copy
. $R/driver/remote-env.sh
realstate > $R/realstate.before
test -d "$SRC/Contents" || { echo "no Release bundle at $SRC: run just mac-app Release"; exit 1; }
! defaults read $BUNDLE >/dev/null 2>&1 || { echo "the app already has preferences on this Mac; they would be changed"; exit 1; }
[ ! -e "/Applications/Agent Mux.app" ] || { echo "Agent Mux.app is installed on this Mac; the run would quit it"; exit 1; }

AMX_APP_SOURCE="$SRC" AMX_APP_INSTALL_DIR="$INSTALL" AMX_APP_NO_LAUNCH=1 "$COPY/macos/scripts/install-app.sh" </dev/null

# The world's amx is the packaged one. A wrapper, not a link: amx finds its
# tmux runtime beside its own executable.
printf '#!/bin/sh\nexec "%s" "$@"\n' "$AMX" > $R/bin/amx; chmod +x $R/bin/amx
install -m 0755 $R/driver/standin-claude.sh $R/bin/claude
install -m 0755 $R/driver/standin-editor.sh $R/bin/edit-standin
ln -sf $KITTY/kitty $R/bin/kitty; ln -sf $KITTY/kitten $R/bin/kitten
cp $R/driver/config.toml $HOME/.config/agent-mux/config.toml
cp $R/driver/zshenv $R/zdot/.zshenv; cp $R/driver/zshrc $R/zdot/.zshrc; cp $R/driver/zshrc $HOME/.zshrc
for d in $R/proj/alpha $R/proj/beta; do
  git -C $d init -q . && git -C $d -c user.email=s19@example.invalid -c user.name=s19 commit -q --allow-empty -m init
done
printf '# Context\n\nAgent Mux acceptance fixture.\n' > $HOME/plans/demo-plan/context.md
printf '# Manifest\n\n- session-1\n' > $HOME/plans/demo-plan/manifest.md
printf '# Session 1: demo\n\nFixture session.\n\n## Handoff\n\n- **Status**: pending\n- **Notes**:\n' > $HOME/plans/demo-plan/session-1.md
# One closed Claude conversation of project alpha, for Past Sessions.
conv=11111111-1111-4111-8111-111111111111
tdir=$HOME/.claude-a/projects/-tmp-amxs19-proj-alpha
mkdir -p "$tdir"
cat > "$tdir/$conv.jsonl" <<JSON
{"type":"user","entrypoint":"cli","sessionId":"$conv","cwd":"$R/proj/alpha","timestamp":"2026-10-09T12:00:00.000Z","uuid":"aaaaaaaa-0000-4000-8000-000000000001","parentUuid":null,"message":{"role":"user","content":"acceptance fixture prompt"}}
{"type":"assistant","sessionId":"$conv","cwd":"$R/proj/alpha","timestamp":"2026-10-09T12:00:05.000Z","uuid":"aaaaaaaa-0000-4000-8000-000000000002","parentUuid":"aaaaaaaa-0000-4000-8000-000000000001","message":{"role":"assistant","content":[{"type":"text","text":"acceptance fixture answer"}]}}
JSON
echo "world ready on $(scutil --get ComputerName) ($(sw_vers -productVersion), $(uname -m))"
echo "bundle: $(/usr/libexec/PlistBuddy -c 'Print :CFBundleShortVersionString' "$DEST/Contents/Info.plist"), installed in $INSTALL"
amx --version
"$TM" -V
$KITTY/kitty --version
amx backend status
