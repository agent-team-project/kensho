#!/usr/bin/env python3
"""Run the gate-tier validator shipped with the bundled verifier skill."""

from __future__ import annotations

import runpy
import sys
from pathlib import Path


VALIDATOR = (
    Path(__file__).resolve().parents[2]
    / "template"
    / "skills"
    / "verify"
    / "scripts"
    / "validate_gate_tiers.py"
)


if not VALIDATOR.is_file():
    raise SystemExit(f"missing shipped gate-tier validator: {VALIDATOR}")

sys.argv[0] = str(VALIDATOR)
runpy.run_path(str(VALIDATOR), run_name="__main__")
