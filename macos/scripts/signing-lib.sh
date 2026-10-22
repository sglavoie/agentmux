# shellcheck shell=sh
# Sourced by sign.sh, build-app.sh, and test-app.sh: which identity signs
# Agent Mux.app, so every copy of the app on this Mac is signed alike.
#
# Privacy grants (Screen Recording, Accessibility, Files and Folders) are recorded
# against the bundle identifier and the signature's designated requirement.
# Every copy of the app shares the bundle identifier: /Applications, the
# DerivedData products, and the host the hosted tests run in. An ad-hoc copy's
# requirement is its cdhash, so a grant answered in one copy fails to match the
# next build or the installed app, and macOS asks again. A copy signed with a
# certificate shares one requirement across every build. xcodebuild therefore
# signs its own products with the identity sign.sh uses, rather than ad-hoc.

# signing_identity: resolve AMX_CODESIGN_IDENTITY when set, else the SHA-1 of this
# Mac's first valid Apple Development identity (the name can be shared by an
# expired twin), else "-" for ad-hoc.
signing_identity() {
	if [ "${AMX_CODESIGN_IDENTITY:-}" = - ]; then
		printf '%s\n' -
		return
	fi
	identities=$(security find-identity -v -p codesigning 2>/dev/null) || {
		echo "Cannot read signing identities; refusing to silently switch to ad-hoc signing." >&2
		return 1
	}
	if [ -n "${AMX_CODESIGN_IDENTITY:-}" ]; then
		# Return a fingerprint even for a name containing spaces: callers pass
		# these settings to xcodebuild as separate shell words.
		found=$(printf '%s\n' "$identities" | awk -v wanted="$AMX_CODESIGN_IDENTITY" '
			/^[[:space:]]*[0-9]+\)/ {
				hash = $2
				name = $0
				sub(/^[^"]*"/, "", name); sub(/".*$/, "", name)
				if (hash == toupper(wanted) || name == wanted) { print hash; exit }
			}')
		if [ -z "$found" ]; then
			echo "AMX_CODESIGN_IDENTITY is not a valid signing identity on this Mac: $AMX_CODESIGN_IDENTITY" >&2
			return 1
		fi
	else
		found=$(printf '%s\n' "$identities" |
			sed -n 's/^ *[0-9][0-9]*) \([0-9A-F]\{40\}\) "Apple Development: .*/\1/p' | head -n 1)
	fi
	if [ -z "$found" ]; then
		echo "No Apple Development identity found: signing ad-hoc; privacy grants will not survive changed builds. Set AMX_CODESIGN_IDENTITY to a certificate to preserve them." >&2
	fi
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
# signing_identity, printed one per line for `set --`.
xcode_signing_settings() {
	identity=$(signing_identity) || return 1
	printf '%s\n' "CODE_SIGN_IDENTITY=$identity"
	team=$(signing_team "$identity")
	printf '%s\n' "DEVELOPMENT_TEAM=$team"
}

# check_signing_continuity OLD NEW: validate the signatures against each
# other's designated requirements (not cdhashes or certificate fingerprints).
# Certificate renewals with compatible requirements are valid updates.
# Run before quitting the app or changing anything in /Applications.
check_signing_continuity() (
	[ -d "$1" ] || return 0
	old_requirement=$(codesign -d -r- "$1" 2>&1 | sed -n 's/^designated => //p')
	new_requirement=$(codesign -d -r- "$2" 2>&1 | sed -n 's/^designated => //p')
	if [ -n "$old_requirement" ] && [ -n "$new_requirement" ] &&
		codesign --verify --strict -R "=$old_requirement" "$2" >/dev/null 2>&1 &&
		codesign --verify --strict -R "=$new_requirement" "$1" >/dev/null 2>&1; then
		echo "Signing identity is compatible with the installed app; privacy grants can carry forward."
		return 0
	fi
	echo "The new app's signing requirement is incompatible with $1; replacing it can invalidate macOS permissions." >&2
	if [ "${AMX_ALLOW_SIGNING_CHANGE:-0}" = 1 ]; then
		echo "Continuing because AMX_ALLOW_SIGNING_CHANGE=1; permissions may need to be granted again once." >&2
		return 0
	fi
	echo "Rebuild with the installed app's certificate (AMX_CODESIGN_IDENTITY)." >&2
	echo "For an intentional identity migration only, rerun with AMX_ALLOW_SIGNING_CHANGE=1." >&2
	return 1
)
