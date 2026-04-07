"""Shared helpers for config validator tests."""

from __future__ import annotations

import os
import sys

LIB_DIR = os.path.join(os.path.dirname(__file__), "..", "..", "lib")
if LIB_DIR not in sys.path:
    sys.path.insert(0, LIB_DIR)

import config_validator as cv

PASS = 0
FAIL = 0


def check(label: str, condition: bool) -> None:
    global PASS, FAIL
    if condition:
        PASS += 1
        print(f"  PASS  {label}")
    else:
        FAIL += 1
        print(f"  FAIL  {label}")


def write_file(tmp_dir: str, filename: str, content: str) -> str:
    path = os.path.join(tmp_dir, filename)
    with open(path, "w") as handle:
        handle.write(content)
    return path
