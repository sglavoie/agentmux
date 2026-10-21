#!/bin/sh
# A named, signed local app gives macOS privacy grants a stable identity.
set -eu
repo=$(CDPATH="" cd -- "$(dirname -- "$0")/../.." && pwd)
app="$repo/macos/build/Agent Mux Demo Recorder.app"
mkdir -p "$app/Contents/MacOS"
swiftc -parse-as-library "$repo/scripts/demo/record.swift" -o "$app/Contents/MacOS/demo-recorder"
cat > "$app/Contents/Info.plist" <<'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleIdentifier</key><string>io.github.sglavoie.agentmux.demo-recorder</string>
<key>CFBundleName</key><string>Agent Mux Demo Recorder</string>
<key>CFBundleExecutable</key><string>demo-recorder</string>
<key>CFBundlePackageType</key><string>APPL</string>
<key>CFBundleVersion</key><string>1</string>
<key>LSMinimumSystemVersion</key><string>14.0</string>
<key>LSUIElement</key><true/>
</dict></plist>
PLIST
. "$repo/macos/scripts/signing-lib.sh"
codesign --force --sign "$(signing_identity)" "$app"
codesign --verify --strict "$app"
printf '%s\n' "$app"
