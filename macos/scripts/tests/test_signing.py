"""Exercise signing decisions without a real keychain or installed application."""

import os
from pathlib import Path
import subprocess
import tempfile
import unittest


LIBRARY = Path(__file__).resolve().parents[1] / "signing-lib.sh"
IDENTITY = "A" * 40
NAME = "Apple Development: Example Developer (ABCDEFGHIJ)"


class SigningTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.old = self.root / "Old App.app"
        self.new = self.root / "New App.app"
        self.old.mkdir()
        self.new.mkdir()
        self.env = {k: v for k, v in os.environ.items() if not k.startswith("AMX_")}
        self.env.update(
            PATH=f"{self.root}:{os.environ['PATH']}",
            SIGNING_LIBRARY=str(LIBRARY),
            OLD_APP=str(self.old),
            NEW_APP=str(self.new),
            IDENTITIES=f'  1) {IDENTITY} "{NAME}"\n     1 valid identities found\n',
        )
        self.tool("security", 'printf "%s" "$IDENTITIES"\nexit "${SECURITY_STATUS:-0}"\n')
        # Model requirement evaluation independently of signature extraction.
        self.tool("codesign", '''case "$1" in
    -d)
        if [ "${NO_REQUIREMENT:-0}" != 1 ]; then
            echo 'designated => identifier "example" and anchor apple generic' >&2
        fi
        ;;
    --verify)
        printf '%s\\n' "$*" >> "$VERIFY_LOG"
        case "$*" in
            *"$NEW_APP") exit "${NEW_STATUS:-0}" ;;
            *"$OLD_APP") exit "${OLD_STATUS:-0}" ;;
        esac
        exit 1
        ;;
    *) exit 99 ;;
esac
''')
        self.env["VERIFY_LOG"] = str(self.root / "verify.log")

    def tool(self, name, body):
        path = self.root / name
        path.write_text("#!/bin/sh\n" + body)
        path.chmod(0o755)

    def run_shell(self, command, **env):
        return subprocess.run(
            ["sh", "-eu", "-c", '. "$SIGNING_LIBRARY"\n' + command],
            env=self.env | env, text=True, capture_output=True,
        )

    def test_default_identity(self):
        result = self.run_shell("signing_identity")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout.strip(), IDENTITY)

    def test_identity_name_with_spaces_resolves_to_fingerprint(self):
        result = self.run_shell("signing_identity", AMX_CODESIGN_IDENTITY=NAME)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout.strip(), IDENTITY)

    def test_missing_explicit_identity_fails(self):
        result = self.run_shell("signing_identity", AMX_CODESIGN_IDENTITY="Missing")
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(result.stdout, "")

    def test_keychain_failure_does_not_become_adhoc(self):
        result = self.run_shell("signing_identity", SECURITY_STATUS="1")
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(result.stdout, "")

    def test_no_certificate_warns_about_permissions(self):
        result = self.run_shell("signing_identity", IDENTITIES="0 valid identities found")
        self.assertEqual(result.stdout.strip(), "-")
        self.assertIn("privacy grants", result.stderr)

    def test_explicit_adhoc_overrides_generated_certificate(self):
        result = self.run_shell("xcode_signing_settings", AMX_CODESIGN_IDENTITY="-")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, "CODE_SIGN_IDENTITY=-\nDEVELOPMENT_TEAM=\n")

    def test_compatible_update_evaluates_both_requirements(self):
        result = self.run_shell('check_signing_continuity "$OLD_APP" "$NEW_APP"')
        self.assertEqual(result.returncode, 0, result.stderr)
        calls = Path(self.env["VERIFY_LOG"]).read_text().splitlines()
        self.assertEqual(len(calls), 2)
        self.assertIn('-R =identifier "example"', calls[0])

    def test_incompatible_update_is_refused_in_either_direction(self):
        for flag in ("OLD_STATUS", "NEW_STATUS"):
            with self.subTest(flag=flag):
                result = self.run_shell(
                    'check_signing_continuity "$OLD_APP" "$NEW_APP"', **{flag: "1"}
                )
                self.assertNotEqual(result.returncode, 0)
                self.assertIn("AMX_CODESIGN_IDENTITY", result.stderr)

    def test_unreadable_requirement_is_refused(self):
        result = self.run_shell(
            'check_signing_continuity "$OLD_APP" "$NEW_APP"', NO_REQUIREMENT="1"
        )
        self.assertNotEqual(result.returncode, 0)

    def test_explicit_migration_warns_and_continues(self):
        result = self.run_shell(
            'check_signing_continuity "$OLD_APP" "$NEW_APP"',
            NEW_STATUS="1", AMX_ALLOW_SIGNING_CHANGE="1",
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("granted again once", result.stderr)

    def test_first_install_needs_no_previous_requirement(self):
        self.old.rmdir()
        result = self.run_shell('check_signing_continuity "$OLD_APP" "$NEW_APP"')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertFalse(Path(self.env["VERIFY_LOG"]).exists())


if __name__ == "__main__":
    unittest.main()
