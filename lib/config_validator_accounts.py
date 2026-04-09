#!/usr/bin/env python3
"""Validation logic for accounts.toml."""

from __future__ import annotations

import os
import tomllib

from config_validator_shared import Validator


def validate_accounts(
    path: str, palette_colors: frozenset[str] | set[str] | None
) -> tuple[Validator, set[str]]:
    """Validate accounts.toml. Returns (Validator, set of account names)."""
    validator = Validator("accounts.toml")
    account_names: set[str] = set()

    if not os.path.isfile(path):
        validator.warn("file not found (run 'ccm config init' to create it)")
        return validator, account_names

    try:
        with open(path, "rb") as file_obj:
            data = tomllib.load(file_obj)
    except tomllib.TOMLDecodeError as exc:
        validator.error(f"invalid TOML syntax: {exc}")
        return validator, account_names

    accounts = data.get("accounts")
    if accounts is None:
        validator.warn("no [[accounts]] entries found")
        return validator, account_names

    if not isinstance(accounts, list):
        validator.error("'accounts' must be an array of tables ([[accounts]])")
        return validator, account_names

    known_account_keys = {"name", "config_dir", "color"}

    for index, entry in enumerate(accounts, start=1):
        unknown = set(entry.keys()) - known_account_keys
        if unknown:
            label = entry.get("name", f"entry {index}")
            validator.warn(
                f"account '{label}': unknown key(s): {', '.join(sorted(unknown))}"
            )

        name = entry.get("name", "")
        if not name:
            validator.error(f"entry {index}: missing or empty 'name'")
            continue
        if not isinstance(name, str):
            validator.error(f"entry {index}: 'name' must be a string")
            continue

        config_dir = entry.get("config_dir", "")
        if not isinstance(config_dir, str):
            validator.error(f"account '{name}': 'config_dir' must be a string")
        elif config_dir and not os.path.isdir(os.path.expanduser(config_dir)):
            validator.warn(
                f"account '{name}': config_dir '{config_dir}' does not exist"
            )

        color = entry.get("color", "gray")
        if not isinstance(color, str):
            validator.error(f"account '{name}': 'color' must be a string")
        elif palette_colors is not None and color not in palette_colors:
            validator.error(
                f"account '{name}': unknown color '{color}' "
                f"(valid: {', '.join(sorted(palette_colors))})"
            )

        if name in account_names:
            validator.error(f"duplicate account name '{name}'")
        account_names.add(name)

    return validator, account_names
