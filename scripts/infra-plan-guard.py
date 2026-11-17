#!/usr/bin/env python3
"""Refuses a saved OpenTofu plan that deletes or replaces anything, unless that address is allowed for this run.

Usage: tofu show -json plan.bin | scripts/infra-plan-guard.py [--allow ADDRESS ...]

Every infrastructure change is applied by CI (.github/workflows/infra.yml). Creating and updating in place go ahead after review; deleting or
replacing a resource (a server, an address, a DNS record) is the kind of change that causes an outage, so it needs a run started on purpose with
that address named (workflow_dispatch input allow_replace). Writes a Markdown summary to $GITHUB_STEP_SUMMARY when it is set.
"""
import argparse
import json
import os
import sys


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--allow", action="append", default=[], help="a resource address that may be deleted or replaced in this run")
    args = ap.parse_args()
    plan = json.load(sys.stdin)

    rows, blocked = [], []
    for rc in plan.get("resource_changes", []):
        actions = rc["change"]["actions"]
        imported = bool(rc["change"].get("importing"))
        if actions == ["no-op"] and not imported:
            continue
        what = "import" if imported and actions == ["no-op"] else "+".join(actions)
        if imported and actions != ["no-op"]:
            what = "import, then " + what
        rows.append((rc["address"], what))
        if "delete" in actions and rc["address"] not in args.allow:
            blocked.append(rc["address"])

    lines = ["### Infrastructure plan", ""]
    if rows:
        lines += ["| Resource | Change |", "|---|---|"] + [f"| `{a}` | {w} |" for a, w in rows]
    else:
        lines.append("No changes.")
    if blocked:
        lines += ["", "**Refused:** this plan deletes or replaces " + ", ".join(f"`{a}`" for a in blocked) + ". If that is intended, run the "
                  "Infrastructure workflow by hand with those addresses in *allow_replace*."]
    text = "\n".join(lines) + "\n"
    print(text)
    if os.environ.get("GITHUB_STEP_SUMMARY"):
        with open(os.environ["GITHUB_STEP_SUMMARY"], "a") as f:
            f.write(text)
    return 1 if blocked else 0


if __name__ == "__main__":
    sys.exit(main())
