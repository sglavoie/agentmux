"""Miscellaneous action helpers: hooks sync and editor launcher."""

import os
import shlex
import subprocess


def run_hooks_sync(account_name=None):
    """Run install-hooks.sh to sync hooks into settings.json files."""
    this_dir = os.path.dirname(os.path.realpath(__file__))
    repo_lib = os.path.join(this_dir, "..", "..", "..", "lib", "install-hooks.sh")
    installed_lib = os.path.join(
        os.path.expanduser("~"), ".local", "lib", "cc-manager", "install-hooks.sh"
    )
    if os.path.isfile(repo_lib):
        script_path = os.path.realpath(repo_lib)
    elif os.path.isfile(installed_lib):
        script_path = installed_lib
    else:
        return False, "install-hooks.sh not found"

    repo_example = os.path.join(
        this_dir, "..", "..", "..", "config", "hooks.example.json"
    )
    installed_example = os.path.join(
        os.path.expanduser("~"), ".local", "lib", "cc-manager", "hooks.example.json"
    )
    if os.path.isfile(repo_example):
        example_path = os.path.realpath(repo_example)
    elif os.path.isfile(installed_example):
        example_path = installed_example
    else:
        return False, "hooks.example.json not found"

    cmd = ["bash", script_path]
    if account_name:
        cmd += ["--account", account_name]
    cmd.append(example_path)

    try:
        result = subprocess.run(cmd, capture_output=True, text=True, timeout=10)
    except subprocess.TimeoutExpired:
        return False, "hooks sync timed out after 10 seconds"
    output = (result.stdout + result.stderr).strip()
    return result.returncode == 0, output


def do_open_editor(socket, filepath):
    """Open a file in the user's editor in a new Kitty OS window.

    Returns True on success, False if the launch fails.
    """
    editor = os.environ.get("VISUAL") or os.environ.get("EDITOR") or "vi"
    editor_parts = shlex.split(editor)
    try:
        subprocess.Popen(
            [
                "kitty",
                "@",
                "--to",
                socket,
                "launch",
                "--type=os-window",
                *editor_parts,
                filepath,
            ],
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
        )
    except OSError:
        return False
    return True
