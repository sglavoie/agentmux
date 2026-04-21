#!/usr/bin/env python3
"""Validation logic for sessions.toml."""

from __future__ import annotations

import os
import tomllib

from config_validator_shared import Validator


def validate_sessions(path: str, account_names: set[str]) -> Validator:
    """Validate sessions.toml."""
    validator = Validator("sessions.toml")

    if not os.path.isfile(path):
        validator.warn("file not found (run 'ccm config init' to create it)")
        return validator

    try:
        with open(path, "rb") as file_obj:
            data = tomllib.load(file_obj)
    except tomllib.TOMLDecodeError as exc:
        validator.error(f"invalid TOML syntax: {exc}")
        return validator

    sessions = data.get("sessions")
    if sessions is None:
        validator.warn("no [[sessions]] entries found")
        return validator

    if not isinstance(sessions, list):
        validator.error("'sessions' must be an array of tables ([[sessions]])")
        return validator

    valid_project_window = {"", "-", "stack", "vsplit", "hsplit", "split"}
    known_session_keys = {
        "title",
        "directory",
        "account",
        "flags",
        "project_window",
        "env",
        "groups",
        "mute",
        "commands",
    }
    seen_titles: set[str] = set()

    for index, entry in enumerate(sessions, start=1):
        unknown = set(entry.keys()) - known_session_keys
        if unknown:
            label = entry.get("title", f"entry {index}")
            validator.warn(
                f"session '{label}': unknown key(s): {', '.join(sorted(unknown))}"
            )

        title = entry.get("title", "")
        directory = entry.get("directory", "")

        if not title:
            validator.error(f"entry {index}: missing or empty 'title'")
        if not directory:
            validator.error(f"entry {index}: missing or empty 'directory'")
        elif not os.path.isdir(os.path.expanduser(directory)):
            label = title or f"entry {index}"
            validator.warn(f"session '{label}': directory '{directory}' does not exist")
        if not title:
            continue

        if title in seen_titles:
            validator.warn(f"duplicate session title '{title}'")
        seen_titles.add(title)

        account = entry.get("account", "")
        if account and account_names and account not in account_names:
            validator.error(
                f"session '{title}': references unknown account '{account}'"
            )

        project_window = entry.get("project_window", "")
        if project_window and project_window not in valid_project_window:
            validator.error(
                f"session '{title}': invalid project_window '{project_window}' "
                f"(valid: -, stack, vsplit, hsplit, split)"
            )

        mute = entry.get("mute")
        if mute is not None and not isinstance(mute, bool) and mute != "-":
            validator.warn(
                f"session '{title}': 'mute' should be "
                f'true, false, or "-", got {mute!r}'
            )

        commands = entry.get("commands")
        if commands is not None:
            if not isinstance(commands, list):
                validator.warn(
                    f"session '{title}': 'commands' must be an array, "
                    f"got {type(commands).__name__} — ignoring"
                )
            else:
                for command_index, command in enumerate(commands):
                    if not isinstance(command, str):
                        validator.warn(
                            f"session '{title}': 'commands' entry "
                            f"{command_index} must be a string, "
                            f"got {type(command).__name__} — skipping"
                        )
                    elif "\t" in command or "\n" in command or "\r" in command:
                        validator.warn(
                            f"session '{title}': 'commands' entry "
                            f"{command_index} contains a tab or newline, "
                            "which is not supported by the current "
                            "transport — ignoring"
                        )
                project_window_raw = entry.get("project_window", "")
                if commands and project_window_raw == "-":
                    validator.warn(
                        f"session '{title}': 'commands' is set but "
                        "'project_window = \"-\"' disables the project "
                        "window — commands will be ignored"
                    )

        env = entry.get("env", "")
        if env and env != "-":
            for pair in env.split():
                if "=" not in pair:
                    label = title or f"entry {index}"
                    validator.warn(
                        f"session '{label}': env token '{pair}' is not a KEY=VALUE pair"
                    )

    group_counts: dict[str, int] = {}
    for entry in sessions:
        groups = entry.get("groups", [])
        if isinstance(groups, list):
            for group in groups:
                if isinstance(group, str) and group:
                    group_counts[group] = group_counts.get(group, 0) + 1

    if len(group_counts) >= 2:
        for group, count in group_counts.items():
            if count == 1:
                validator.warn(
                    f"group '{group}' is used by only 1 session — possible typo?"
                )

    return validator
