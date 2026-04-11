#!/usr/bin/env python3
"""Integration tests for lib/install-hooks.sh.

Tests the hook installer against temporary directories to verify:
- Fresh install (no existing settings.json)
- Idempotency (running twice produces same result)
- Existing user hooks are preserved
- Backup file (.bak) is created on changes
- Malformed hooks.json produces clear error

Run: python3 tests/test_hooks_install.py
"""

import json
import os
import shutil
import subprocess
import sys
import tempfile

PASS = 0
FAIL = 0

REPO_DIR = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
INSTALL_SCRIPT = os.path.join(REPO_DIR, "lib", "install-hooks.sh")
HOOKS_EXAMPLE = os.path.join(REPO_DIR, "config", "hooks.example.json")
ICONS_JSON = os.path.join(REPO_DIR, "data", "icons.json")


def check(label, condition):
    global PASS, FAIL
    if condition:
        PASS += 1
        print(f"  PASS  {label}")
    else:
        FAIL += 1
        print(f"  FAIL  {label}")


def _has_deps():
    """Check if jq and python3 are available."""
    return all(shutil.which(cmd) is not None for cmd in ("jq", "python3"))


def _make_accounts_toml(tmp_dir, accounts):
    """Create a minimal accounts.toml in tmp_dir.

    accounts: list of (name, config_dir) tuples.
    """
    config_dir = os.path.join(tmp_dir, "config")
    os.makedirs(config_dir, exist_ok=True)
    path = os.path.join(config_dir, "accounts.toml")
    lines = []
    for name, cdir in accounts:
        lines.append("[[accounts]]")
        lines.append(f'name = "{name}"')
        lines.append(f'config_dir = "{cdir}"')
        lines.append('color = "blue"')
        lines.append("")
    with open(path, "w") as f:
        f.write("\n".join(lines))
    return config_dir


def _run_installer(_tmp_dir, config_dir, extra_env=None, expect_fail=False):
    """Run install-hooks.sh with env pointing to tmp dirs."""
    env = os.environ.copy()
    env["CC_MANAGER_ACCOUNTS"] = os.path.join(config_dir, "accounts.toml")
    env["CC_MANAGER_CONFIG"] = config_dir
    if extra_env:
        env.update(extra_env)

    result = subprocess.run(
        ["bash", INSTALL_SCRIPT, HOOKS_EXAMPLE],
        capture_output=True,
        text=True,
        env=env,
        cwd=REPO_DIR,
    )
    if expect_fail:
        return result
    if result.returncode != 0:
        print(f"    STDERR: {result.stderr.strip()}")
    return result


def _load_settings(config_dir):
    """Load settings.json from a config directory."""
    path = os.path.join(config_dir, "settings.json")
    with open(path) as f:
        return json.load(f)


# ── tests ────────────────────────────────────────────────────────────


def test_fresh_install():
    """Fresh install creates settings.json with hooks."""
    print("\n--- fresh install ---")
    with tempfile.TemporaryDirectory() as tmp:
        acct_dir = os.path.join(tmp, "claude-config")
        os.makedirs(acct_dir)
        config_dir = _make_accounts_toml(tmp, [("test", acct_dir)])
        result = _run_installer(tmp, config_dir)

        check("exit code 0", result.returncode == 0)

        settings_path = os.path.join(acct_dir, "settings.json")
        check("settings.json created", os.path.isfile(settings_path))

        settings = _load_settings(acct_dir)
        check("has hooks key", "hooks" in settings)
        check("hooks is dict", isinstance(settings["hooks"], dict))
        check("has SessionStart", "SessionStart" in settings["hooks"])

        # Verify cc-hook-indicator is in the hooks
        session_start = settings["hooks"]["SessionStart"]
        commands = []
        for entry in session_start:
            for hook in entry.get("hooks", []):
                commands.append(hook.get("command", ""))
        check(
            "contains cc-hook-indicator",
            any("cc-hook-indicator" in c for c in commands),
        )


def test_idempotency():
    """Running installer twice produces identical settings.json."""
    print("\n--- idempotency ---")
    with tempfile.TemporaryDirectory() as tmp:
        acct_dir = os.path.join(tmp, "claude-config")
        os.makedirs(acct_dir)
        config_dir = _make_accounts_toml(tmp, [("test", acct_dir)])

        # First run
        _run_installer(tmp, config_dir)
        first = _load_settings(acct_dir)

        # Second run
        result = _run_installer(tmp, config_dir)
        check("exit code 0", result.returncode == 0)
        check(
            "reports skipped",
            "Skipped" in result.stdout or "already up to date" in result.stdout,
        )

        second = _load_settings(acct_dir)
        check("identical output", first == second)


def test_preserves_user_hooks():
    """Existing user-defined hooks (non cc-hook-indicator) are preserved."""
    print("\n--- preserves user hooks ---")
    with tempfile.TemporaryDirectory() as tmp:
        acct_dir = os.path.join(tmp, "claude-config")
        os.makedirs(acct_dir)
        config_dir = _make_accounts_toml(tmp, [("test", acct_dir)])

        # Pre-populate settings.json with a user hook
        user_hook = {
            "hooks": {
                "SessionStart": [
                    {"hooks": [{"type": "command", "command": "my-custom-hook start"}]}
                ]
            }
        }
        with open(os.path.join(acct_dir, "settings.json"), "w") as f:
            json.dump(user_hook, f)

        result = _run_installer(tmp, config_dir)
        check("exit code 0", result.returncode == 0)

        settings = _load_settings(acct_dir)
        session_hooks = settings["hooks"]["SessionStart"]
        all_commands = []
        for entry in session_hooks:
            for hook in entry.get("hooks", []):
                all_commands.append(hook.get("command", ""))

        check("user hook preserved", "my-custom-hook start" in all_commands)
        check(
            "ccm hook also present", any("cc-hook-indicator" in c for c in all_commands)
        )


def test_backup_created():
    """Backup .bak file is created when settings.json is modified."""
    print("\n--- backup created ---")
    with tempfile.TemporaryDirectory() as tmp:
        acct_dir = os.path.join(tmp, "claude-config")
        os.makedirs(acct_dir)
        config_dir = _make_accounts_toml(tmp, [("test", acct_dir)])

        # Create initial settings.json with something different
        with open(os.path.join(acct_dir, "settings.json"), "w") as f:
            json.dump({"hooks": {}}, f)

        _run_installer(tmp, config_dir)

        bak_path = os.path.join(acct_dir, "settings.json.bak")
        check("backup file exists", os.path.isfile(bak_path))

        # Verify backup contains the original content
        with open(bak_path) as f:
            backup = json.load(f)
        check("backup has original content", backup == {"hooks": {}})


def test_remove_mode():
    """--remove strips ccm hooks and preserves user hooks."""
    print("\n--- remove mode ---")
    with tempfile.TemporaryDirectory() as tmp:
        acct_dir = os.path.join(tmp, "claude-config")
        os.makedirs(acct_dir)
        config_dir = _make_accounts_toml(tmp, [("test", acct_dir)])

        # First install hooks
        _run_installer(tmp, config_dir)

        # Add a user hook manually
        settings = _load_settings(acct_dir)
        settings["hooks"].setdefault("SessionStart", []).append(
            {"hooks": [{"type": "command", "command": "my-user-hook"}]}
        )
        with open(os.path.join(acct_dir, "settings.json"), "w") as f:
            json.dump(settings, f)

        # Run in remove mode
        env = os.environ.copy()
        env["CC_MANAGER_ACCOUNTS"] = os.path.join(config_dir, "accounts.toml")
        env["CC_MANAGER_CONFIG"] = config_dir
        result = subprocess.run(
            ["bash", INSTALL_SCRIPT, "--remove"],
            capture_output=True,
            text=True,
            env=env,
            cwd=REPO_DIR,
        )
        check("remove exit code 0", result.returncode == 0)

        settings = _load_settings(acct_dir)
        all_commands = []
        for event_hooks in settings.get("hooks", {}).values():
            for entry in event_hooks:
                for hook in entry.get("hooks", []):
                    all_commands.append(hook.get("command", ""))

        check(
            "cc-hook-indicator removed",
            not any("cc-hook-indicator" in c for c in all_commands),
        )
        check("user hook preserved after remove", "my-user-hook" in all_commands)


def test_malformed_hooks_json():
    """Malformed hooks.json produces a non-zero exit and clear error."""
    print("\n--- malformed hooks.json ---")
    with tempfile.TemporaryDirectory() as tmp:
        acct_dir = os.path.join(tmp, "claude-config")
        os.makedirs(acct_dir)
        config_dir = _make_accounts_toml(tmp, [("test", acct_dir)])

        # Write an invalid JSON file to use as the hooks source
        bad_hooks = os.path.join(tmp, "bad_hooks.json")
        with open(bad_hooks, "w") as f:
            f.write("{this is not valid json!!")

        env = os.environ.copy()
        env["CC_MANAGER_ACCOUNTS"] = os.path.join(config_dir, "accounts.toml")
        env["CC_MANAGER_CONFIG"] = config_dir

        result = subprocess.run(
            ["bash", INSTALL_SCRIPT, bad_hooks],
            capture_output=True,
            text=True,
            env=env,
            cwd=REPO_DIR,
        )

        check("non-zero exit code", result.returncode != 0)
        # Should produce some error output (stderr or stdout)
        combined = result.stderr + result.stdout
        check("error message present", len(combined.strip()) > 0)
        # settings.json should NOT have been created/modified
        settings_path = os.path.join(acct_dir, "settings.json")
        check("settings.json not created", not os.path.isfile(settings_path))


def test_invalid_settings_json_exit_code():
    """Installer exits non-zero when an account has invalid settings.json."""
    print("\n--- invalid settings.json exit code ---")
    with tempfile.TemporaryDirectory() as tmp:
        acct_dir = os.path.join(tmp, "claude-config")
        os.makedirs(acct_dir)
        config_dir = _make_accounts_toml(tmp, [("badacct", acct_dir)])

        # Pre-populate settings.json with invalid JSON
        with open(os.path.join(acct_dir, "settings.json"), "w") as f:
            f.write("{this is not valid json!!")

        result = _run_installer(tmp, config_dir, expect_fail=True)

        check("non-zero exit for invalid settings.json", result.returncode != 0)
        check(
            "error mentions account name",
            "badacct" in result.stderr or "badacct" in result.stdout,
        )
        check(
            "error mentions invalid JSON",
            "invalid JSON" in result.stderr or "invalid JSON" in result.stdout,
        )


# ── run all ──────────────────────────────────────────────────────────


if __name__ == "__main__":
    print("install-hooks.sh integration tests")

    if not _has_deps():
        print("\nSkipped: jq or python3 not available")
        sys.exit(0)

    if not os.path.isfile(INSTALL_SCRIPT):
        print(f"\nSkipped: {INSTALL_SCRIPT} not found")
        sys.exit(0)

    test_fresh_install()
    test_idempotency()
    test_preserves_user_hooks()
    test_backup_created()
    test_remove_mode()
    test_malformed_hooks_json()
    test_invalid_settings_json_exit_code()

    print(f"\n{PASS} passed, {FAIL} failed")
    if FAIL:
        sys.exit(1)
