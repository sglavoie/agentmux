#!/bin/sh
# The amx the packaged app is told to run (its amxBinaryPath preference), in
# the world only. libghostty starts a terminal's command through login(1),
# which resets HOME to the account's real one, so an embedded terminal would
# otherwise attach to the MacBook user's own AgentMux. This puts the world
# back and runs the amx inside the installed bundle, which is the amx under
# test. An app installed for real needs no shim: its HOME is the real one.
export HOME=/tmp/amxs19/home TMPDIR=/tmp/amxs19/tmp PATH=/tmp/amxs19/bin:/usr/bin:/bin:/usr/sbin:/sbin
exec "/tmp/amxs19/Applications/Agent Mux.app/Contents/Resources/bin/amx" "$@"
