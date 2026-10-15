# shellcheck shell=sh
# Sourced by sign.sh, build-app.sh, and test-app.sh: which identity signs
# Agent Mux.app, so every copy of the app on this Mac is signed alike.
#
# Privacy grants (Files and Folders, Automation, notifications) are recorded
# against the bundle identifier and the signature's designated requirement.
# Every copy of the app shares the bundle identifier: /Applications, the
# DerivedData products, and the host the hosted tests run in. An ad-hoc copy's
# requirement is its cdhash, so a grant answered in one copy fails to match the
# next build or the installed app, and macOS asks again. A copy signed with a
# certificate shares one requirement across every build. xcodebuild therefore
# signs its own products with the identity sign.sh uses, rather than ad-hoc.

# signing_identity: AMX_CODESIGN_IDENTITY when set, else the SHA-1 of this
# Mac's first valid Apple Development identity (the name can be shared by an
# expired twin), else "-" for ad-hoc.
signing_identity() {
	if [ -n "${AMX_CODESIGN_IDENTITY:-}" ]; then
		printf '%s\n' "$AMX_CODESIGN_IDENTITY"
		return
	fi
	found=$(security find-identity -v -p codesigning 2>/dev/null |
		sed -n 's/^ *[0-9][0-9]*) \([0-9A-F]\{40\}\) "Apple Development: .*/\1/p' | head -n 1)
	printf '%s\n' "${found:--}"
}

# signing_team IDENTITY: the team (the certificate's OU) the identity belongs
# to, empty when ad-hoc or when it cannot be read.
signing_team() {
	if [ "$1" = - ]; then
		return
	fi
	security find-identity -v -p codesigning 2>/dev/null |
		sed -n "s/^ *[0-9][0-9]*) \([0-9A-F]\{40\}\) \"\(.*\)\"$/\1 \2/p" |
		while IFS= read -r line; do
			hash=${line%% *}
			name=${line#* }
			if [ "$hash" = "$1" ] || [ "$name" = "$1" ]; then
				security find-certificate -a -Z -c "$name" -p 2>/dev/null |
					openssl x509 -noout -subject -nameopt multiline 2>/dev/null |
					sed -n 's/^ *organizationalUnitName *= *//p' | head -n 1
				return
			fi
		done
}

# xcode_signing_settings: the build settings that make xcodebuild sign with
# signing_identity, printed one per line for `set --`. Nothing for ad-hoc,
# which project.yml already asks for.
xcode_signing_settings() {
	identity=$(signing_identity)
	if [ "$identity" = - ]; then
		return
	fi
	printf '%s\n' "CODE_SIGN_IDENTITY=$identity"
	team=$(signing_team "$identity")
	if [ -n "$team" ]; then
		printf '%s\n' "DEVELOPMENT_TEAM=$team"
	fi
}
