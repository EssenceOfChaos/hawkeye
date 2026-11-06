#!/usr/bin/env python3
"""Runs govulncheck and fails on any vulnerability our code reaches, except the ones accepted below.

govulncheck has no way to ignore a finding, so CI (and `make vuln`) run it through this script. An exception here means someone read the advisory and showed
that HawkEye is not affected. It is narrow on purpose: it names one advisory and one module, needs at least the version that fixes the problem, and lapses on
its review date, so it cannot quietly outlive its reason. Everything else fails as it would without this script, and so does a scan that did not finish.

Accepted findings are recorded in docs/security-review.md. Remove an entry when the script reports that it is no longer needed.
"""
import datetime
import json
import re
import subprocess
import sys
from dataclasses import dataclass

GOVULNCHECK = ["go", "run", "golang.org/x/vuln/cmd/govulncheck@latest", "-json", "./..."]


@dataclass(frozen=True)
class Accepted:
    module: str
    minimum_version: str  # the finding is excused only from this version on
    review_by: datetime.date  # the exception stops applying after this day
    reason: str


# Nothing is accepted at the moment. An entry looks like this:
#
#   "GO-2026-1234": Accepted("example.com/module", "v1.2.3", datetime.date(2027, 1, 31), "Why HawkEye is not affected."),
ACCEPTED = {}


def parse_stream(text):
    """govulncheck -json prints a stream of JSON objects, not one document."""
    decoder, position, messages = json.JSONDecoder(), 0, []
    while True:
        while position < len(text) and text[position].isspace():
            position += 1
        if position >= len(text):
            return messages
        message, position = decoder.raw_decode(text, position)
        messages.append(message)


def version_at_least(version, minimum):
    """Only plain releases count: a pre-release or a pseudo-version cannot be shown to contain the fix."""
    parts = [re.fullmatch(r"v(\d+)\.(\d+)\.(\d+)", v) for v in (version or "", minimum)]
    if not all(parts):
        return False
    have, need = ([int(n) for n in p.groups()] for p in parts)
    return have >= need


def evaluate(messages, accepted, today):
    """Sorts what the scan found into (failures, excused, unused), as sorted lists of advisory ids.

    A finding only matters when its trace reaches a function: that is what govulncheck itself reports as affecting our code. A vulnerability in a module we
    require but never call is not one.
    """
    if not any("config" in m for m in messages):
        raise ValueError("govulncheck produced no scan results")
    reached = {}
    for message in messages:
        finding = message.get("finding")
        if finding and finding["trace"][0].get("function"):
            reached.setdefault(finding["osv"], finding["trace"][0])

    failures, excused = [], []
    for osv, frame in sorted(reached.items()):
        rule = accepted.get(osv)
        allowed = (rule is not None and frame.get("module") == rule.module and today <= rule.review_by
                   and version_at_least(frame.get("version"), rule.minimum_version))
        (excused if allowed else failures).append(osv)
    return failures, excused, sorted(set(accepted) - set(reached))


def describe(osv, messages):
    for message in messages:
        entry = message.get("osv")
        if entry and entry.get("id") == osv:
            return entry.get("summary") or entry.get("details", "")[:120]
    return ""


def main():
    scan = subprocess.run(GOVULNCHECK, capture_output=True, text=True)
    if scan.returncode != 0:
        sys.stderr.write(scan.stderr)
        print(f"govulncheck did not finish (exit status {scan.returncode})", file=sys.stderr)
        return 1
    try:
        messages = parse_stream(scan.stdout)
        failures, excused, unused = evaluate(messages, ACCEPTED, datetime.date.today())
    except ValueError as err:
        print(f"could not read the govulncheck results: {err}", file=sys.stderr)
        return 1

    for osv in excused:
        rule = ACCEPTED[osv]
        print(f"accepted until {rule.review_by}: {osv}: {rule.reason}")
    for osv in unused:
        print(f"no longer needed: remove the exception for {osv} from scripts/vulncheck.py")
    for osv in failures:
        print(f"VULNERABLE: {osv}: {describe(osv, messages)}\n  https://pkg.go.dev/vuln/{osv}", file=sys.stderr)
        if osv in ACCEPTED:
            rule = ACCEPTED[osv]
            print(f"  An exception exists but does not apply: it needs {rule.module} {rule.minimum_version} or later and ends {rule.review_by}.", file=sys.stderr)
    if failures:
        print(f"{len(failures)} vulnerabilities reach our code", file=sys.stderr)
        return 1
    print("No unaccepted vulnerabilities reach our code")
    return 0


if __name__ == "__main__":
    sys.exit(main())
