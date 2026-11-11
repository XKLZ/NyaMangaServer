#!/usr/bin/env python3
"""Identical source identity for native server/restore CLI and container builds."""
from pathlib import Path
import subprocess

root = Path(__file__).resolve().parents[2]
result = subprocess.run(['git', 'rev-parse', 'HEAD'], cwd=root, text=True, capture_output=True)
if result.returncode:
    print('unknown')
else:
    dirty = subprocess.check_output(['git', 'status', '--porcelain', '--untracked-files=normal'], cwd=root, text=True)
    print(result.stdout.strip() + ('-dirty' if dirty else ''))
