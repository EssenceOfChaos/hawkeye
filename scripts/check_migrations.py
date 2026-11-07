#!/usr/bin/env python3
"""Stops a migration that could break workers running the previous release. Runs in CI.

A deploy migrates first and updates the regional workers just after, so for a moment workers run the old code against the new schema. A migration therefore
has to work with the release before it: add now, remove in a later release. Removing or renaming something in the same release as the code that stops using
it breaks those workers. When a migration like that really is safe, say why on a line of its own and this lets it through:

    -- worker-safe: the worker has not read this column since release abc123

    scripts/check_migrations.py BASE_COMMIT
"""
import re
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
RISKY = re.compile(r"\bDROP\s+(COLUMN|TABLE|TYPE|VIEW)\b|\bRENAME\s+(COLUMN\s+)?\S+\s+TO\b|\bRENAME\s+TO\b|\bALTER\s+COLUMN\s+\S+\s+(SET\s+DATA\s+)?TYPE\b|\bSET\s+NOT\s+NULL\b", re.I)
SAFE = re.compile(r"^[ \t]*--[ \t]*worker-safe:[ \t]*\S", re.M)  # a reason on the same line: \s would run on into the next one


def problems(sql):
    if SAFE.search(sql):
        return []
    code = re.sub(r"--.*", "", sql)
    return sorted({m.group(0).upper() for m in RISKY.finditer(code)})


def new_migrations(base):
    out = subprocess.run(["git", "diff", "--name-only", "--diff-filter=A", f"{base}...HEAD", "--", "migrations"], cwd=ROOT, capture_output=True, text=True, check=True).stdout
    return [f for f in out.split() if f.endswith(".up.sql")]


def main(argv):
    if len(argv) != 1:
        print(__doc__, file=sys.stderr)
        return 2
    if not argv[0].strip("0"):   # the first push of a branch has no base to compare with
        return 0
    bad = [(f, p) for f in new_migrations(argv[0]) if (p := problems((ROOT / f).read_text()))]
    for f, p in bad:
        print(f"{f}: {', '.join(p)} can break workers still on the previous release. Do it in a later release, or add a '-- worker-safe: reason' line.", file=sys.stderr)
    return 1 if bad else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
