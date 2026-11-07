#!/usr/bin/env python3
"""The check locations, from their one source: infra/regions.json. OpenTofu reads the same file, and nothing else defines them.

    {"primary": {"name": "US East"},
     "workers": {"eu-central": {"name": "Europe", "vultr_region": "fra", "tunnel_ip": "10.66.0.2", "launched": false}}}

A worker exists as soon as it is listed. "launched" is the switch customers see: only launched regions reach CHECK_REGIONS, so a new worker can run
and be proven first, and going live is a reviewed pull request that flips one word. The primary's code is always "primary": results are stored under it.

    scripts/regions.py validate
    scripts/regions.py check-regions              CHECK_REGIONS for the production server (nothing when no region is launched)
    scripts/regions.py worker-check-regions CODE  the same, plus that worker's own region: it has to be valid there before launch
    scripts/regions.py workers                    one "CODE TUNNEL_IP" per line
    scripts/regions.py apply-env FILE             make CHECK_REGIONS in an .env file match
    scripts/regions.py healthcheck NAME TIMEOUT GRACE
                                                  create or find a healthchecks.io check and print its ping URL (needs HEALTHCHECKS_API_KEY)
"""
import json
import os
import re
import sys
import tempfile
import urllib.error
import urllib.request
from pathlib import Path

FILE = Path(os.environ.get("HAWKEYE_REGIONS_FILE") or Path(__file__).resolve().parent.parent / "infra" / "regions.json")
HEALTHCHECKS_API = os.environ.get("HEALTHCHECKS_API", "https://healthchecks.io/api/v3/checks/")
CODE_RE = re.compile(r"^[a-z0-9][a-z0-9-]{0,18}[a-z0-9]$")  # the same rule as internal/regions
TUNNEL_RE = re.compile(r"^10\.66\.0\.([2-9]|[1-9][0-9]|1[0-9][0-9]|2[0-4][0-9]|25[0-4])$")
BAD_NAME = re.compile(r"[,:'\"\n]")  # CHECK_REGIONS is separated by commas and colons, and written in single quotes


def load():
    if not FILE.exists():  # a release from before this file existed
        return {"primary": {"name": "Primary"}, "workers": {}}
    data = json.loads(FILE.read_text())
    problems = problems_in(data)
    if problems:
        sys.exit("regions.json: " + "; ".join(problems))
    return data


def problems_in(data):
    out = []
    if BAD_NAME.search(data.get("primary", {}).get("name", "")) or not data.get("primary", {}).get("name"):
        out.append("primary needs a name, without commas, colons or quotes")
    seen = set()
    for code, w in data.get("workers", {}).items():
        if not CODE_RE.match(code) or code == "primary":
            out.append(f"{code}: use 2 to 20 lowercase letters, digits or hyphens, and not \"primary\"")
        if not w.get("name") or BAD_NAME.search(w["name"]):
            out.append(f"{code}: needs a name, without commas, colons or quotes")
        if not w.get("vultr_region"):
            out.append(f"{code}: needs a vultr_region")
        if not isinstance(w.get("launched"), bool):
            out.append(f"{code}: launched must be true or false")
        ip = w.get("tunnel_ip", "")
        if not TUNNEL_RE.match(ip):
            out.append(f"{code}: tunnel_ip must be 10.66.0.2 to 10.66.0.254")
        if ip in seen:
            out.append(f"{code}: tunnel_ip {ip} is already taken")
        seen.add(ip)
    return out


def region_list(data, include=None):
    codes = sorted(c for c, w in data["workers"].items() if w["launched"] or c == include)
    if not codes:
        return ""
    return ",".join([f"primary:{data['primary']['name']}"] + [f"{c}:{data['workers'][c]['name']}" for c in codes])


def apply_env(path, value):
    path = Path(path)
    # Replaced where it stands: scripts/deploy.sh compares the settings as text, and a line that moved would look like a change and restart production.
    new = f"CHECK_REGIONS='{value}'"
    lines, found = [], False
    for line in path.read_text().splitlines():
        if not line.startswith("CHECK_REGIONS="):
            lines.append(line)
        elif value and not found:
            lines.append(new)
            found = True
    if value and not found:
        lines.append(new)
    fd, tmp = tempfile.mkstemp(dir=path.parent, prefix=path.name + ".")
    with os.fdopen(fd, "w") as f:
        f.write("\n".join(lines) + "\n")
    os.chmod(tmp, path.stat().st_mode & 0o777)
    os.replace(tmp, path)


def healthcheck(name, timeout, grace):
    key = os.environ.get("HEALTHCHECKS_API_KEY", "")
    if not key:
        return 3
    # "unique" makes this an upsert, so running it on every deploy is safe. The key goes in the body: on a command line, it would be visible in `ps`.
    body = json.dumps({"api_key": key, "name": name, "timeout": int(timeout), "grace": int(grace), "channels": "*", "unique": ["name"]}).encode()
    req = urllib.request.Request(HEALTHCHECKS_API, data=body, headers={"Content-Type": "application/json"}, method="POST")
    try:
        with urllib.request.urlopen(req, timeout=15) as r:
            print(json.load(r)["ping_url"])
        return 0
    except (urllib.error.URLError, KeyError, ValueError) as e:
        print(f"healthchecks.io: {e}", file=sys.stderr)
        return 1


def main(argv):
    cmd = argv[0] if argv else ""
    if cmd == "healthcheck" and len(argv) == 4:
        return healthcheck(*argv[1:])
    data = load()
    if cmd == "validate":
        return 0
    if cmd == "check-regions":
        print(region_list(data))
    elif cmd == "worker-check-regions" and len(argv) == 2 and argv[1] in data["workers"]:
        print(region_list(data, include=argv[1]))
    elif cmd == "workers":
        for code, w in sorted(data["workers"].items()):
            print(code, w["tunnel_ip"])
    elif cmd == "apply-env" and len(argv) == 2:
        apply_env(argv[1], region_list(data))
    else:
        print(__doc__, file=sys.stderr)
        return 2
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
