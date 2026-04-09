#!/usr/bin/env python3
"""Validation logic for hooks.json."""

from __future__ import annotations

import json
import os

from config_validator_shared import Validator


def validate_hooks(
    path: str, valid_events: set[str], account_names: set[str]
) -> Validator:
    """Validate hooks.json."""
    validator = Validator("hooks.json")

    if not os.path.isfile(path):
        validator.warn("file not found (run 'ccm config init' to create it)")
        return validator

    try:
        with open(path) as file_obj:
            data = json.load(file_obj)
    except json.JSONDecodeError as exc:
        validator.error(f"invalid JSON syntax: {exc}")
        return validator

    if not isinstance(data, dict):
        validator.error("top-level value must be a JSON object")
        return validator

    hooks = data.get("hooks")
    if hooks is None:
        validator.error("missing required 'hooks' key")
    elif not isinstance(hooks, dict):
        validator.error("'hooks' must be an object")
    elif valid_events:
        for event_name in hooks:
            if event_name not in valid_events:
                validator.warn(
                    f"unknown hook event '{event_name}' (not in icons.json hook_events)"
                )

    overrides = data.get("account_overrides")
    if overrides is None:
        return validator
    if not isinstance(overrides, dict):
        validator.error("'account_overrides' must be an object")
        return validator

    for account_name, override in overrides.items():
        if account_names and account_name not in account_names:
            validator.warn(f"account_overrides: unknown account '{account_name}'")
        if not isinstance(override, dict):
            validator.error(f"account_overrides.{account_name} must be an object")
            continue

        disabled_events = override.get("disabled_events")
        if disabled_events is not None:
            if not isinstance(disabled_events, list):
                validator.error(
                    f"account_overrides.{account_name}.disabled_events must be an array"
                )
            elif valid_events:
                for event_name in disabled_events:
                    if event_name not in valid_events:
                        validator.warn(
                            f"account_overrides.{account_name}: unknown disabled event '{event_name}'"
                        )

        extra_commands = override.get("extra_commands") or override.get("extra_hooks")
        if extra_commands is None:
            continue
        if not isinstance(extra_commands, dict):
            validator.error(
                f"account_overrides.{account_name}.extra_commands must be an object"
            )
            continue
        if valid_events:
            for event_name in extra_commands:
                if event_name not in valid_events:
                    validator.warn(
                        f"account_overrides.{account_name}: unknown extra_commands event '{event_name}'"
                    )

    return validator
