#!/usr/bin/env python3
"""Orchestration helpers for config validation."""

from __future__ import annotations

import os

from data_loader import load_hook_events, load_palette_colors, load_sort_modes

from config_validator_accounts import validate_accounts
from config_validator_defaults import validate_defaults
from config_validator_hooks import validate_hooks
from config_validator_sessions import validate_sessions
from config_validator_shared import Validator


def resolve_config_paths(config_dir: str) -> tuple[str, str, str, str]:
    """Resolve config file paths honoring override environment variables."""
    accounts_path = os.environ.get(
        "CC_MANAGER_ACCOUNTS", os.path.join(config_dir, "accounts.toml")
    )
    sessions_path = os.environ.get(
        "CC_MANAGER_CONFIG", os.path.join(config_dir, "sessions.toml")
    )
    defaults_path = os.environ.get(
        "CC_MANAGER_DEFAULTS", os.path.join(config_dir, "defaults.toml")
    )
    hooks_path = os.path.join(config_dir, "hooks.json")
    return accounts_path, sessions_path, defaults_path, hooks_path


def collect_validators(config_dir: str, data_dir: str) -> list[Validator]:
    """Run all validators and return them in display order."""
    palette_colors = load_palette_colors(data_dir)
    valid_events = load_hook_events(data_dir)
    valid_sort = load_sort_modes(data_dir)
    accounts_path, sessions_path, defaults_path, hooks_path = resolve_config_paths(
        config_dir
    )

    accounts_validator, account_names = validate_accounts(accounts_path, palette_colors)
    sessions_validator = validate_sessions(sessions_path, account_names)
    defaults_validator = validate_defaults(defaults_path, valid_sort)
    hooks_validator = validate_hooks(hooks_path, valid_events, account_names)

    return [
        accounts_validator,
        sessions_validator,
        defaults_validator,
        hooks_validator,
    ]


def quick_validate(config_dir: str, data_dir: str) -> list[tuple[str, str]]:
    """Run validation and return a list of (level, message) tuples."""
    issues: list[tuple[str, str]] = []
    for validator in collect_validators(config_dir, data_dir):
        for error in validator.errors:
            issues.append(("error", f"{validator.label}: {error}"))
        for warning in validator.warnings:
            issues.append(("warning", f"{validator.label}: {warning}"))
    return issues
