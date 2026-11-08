#!/usr/bin/env python3
"""Fill a HawkEye organization with realistic data for a demo or a reviewer's test account (the Connectors Directory asks for "a fully populated
account").

    HAWKEYE_API_KEY=hk_... scripts/seed_demo.py --base-url https://hawkeye.observer [--status-page-slug hawkeye-demo]

It talks to the public API with an API key, so it runs against any environment and creates data exactly as a person would. Make the key in the
organization to fill, as its owner, with **read and write** access; the organization must be on Pro or Agency (writes through the API, maintenance
windows). The key is read from the environment, never from the command line, so it stays out of shell history.

What it creates (and skips if something with the same name already exists, so it can be run again):

- tags for two made-up clients, "Acme Bakery" and "Northwind Clinic"
- website monitors (example.com, example.org, iana.org), one of them paused, a DNS monitor and two heartbeat (cron job) monitors
- a published status page showing the clients' sites
- a weekly maintenance window
- a past incident with updates: one heartbeat reports a failure, the script posts "investigating" and "identified" updates, the job recovers, and
  a "resolved" update closes the story

Standard library only.
"""

import argparse
import datetime
import json
import os
import sys
import time
import urllib.error
import urllib.request


class APIError(Exception):
    pass


class Client:
    """A tiny HawkEye API client authenticated with an API key."""

    def __init__(self, base_url, key):
        self.base = base_url.rstrip("/")
        self.key = key

    def request(self, method, path, body=None):
        data = json.dumps(body).encode() if body is not None else None
        req = urllib.request.Request(self.base + path, data=data, method=method)
        req.add_header("Authorization", "Bearer " + self.key)
        req.add_header("User-Agent", "hawkeye-seed-demo/1")
        if data is not None:
            req.add_header("Content-Type", "application/json")
        try:
            with urllib.request.urlopen(req, timeout=30) as res:
                raw = res.read()
                return json.loads(raw) if raw else {}
        except urllib.error.HTTPError as e:
            try:
                err = json.loads(e.read()).get("error", {})
            except Exception:  # noqa: BLE001 - any unreadable body
                err = {}
            raise APIError(f"{method} {path}: {e.code} {err.get('code', '')} {err.get('message', '')}".strip()) from None

    def ping(self, url):
        req = urllib.request.Request(url, method="POST", headers={"User-Agent": "hawkeye-seed-demo/1"})
        with urllib.request.urlopen(req, timeout=30):
            pass


TAGS = ["Acme Bakery", "Northwind Clinic"]

MONITORS = [
    {"name": "Acme Bakery website", "url": "https://example.com", "check_interval_seconds": 300, "tags": ["Acme Bakery"]},
    {"name": "Acme Bakery online shop", "url": "https://example.org", "check_interval_seconds": 300, "tags": ["Acme Bakery"]},
    {"name": "Northwind Clinic website", "url": "https://www.iana.org", "check_interval_seconds": 300, "tags": ["Northwind Clinic"]},
    {"name": "Old Acme landing page", "url": "https://example.net", "check_interval_seconds": 3600, "tags": ["Acme Bakery"], "paused": True},
    {"type": "dns", "name": "Acme Bakery mail (MX)", "url": "example.com", "dns_record_type": "MX", "check_interval_seconds": 3600, "tags": ["Acme Bakery"]},
    {"type": "heartbeat", "name": "Nightly database backup", "check_interval_seconds": 86400, "grace_seconds": 3600, "tags": ["Northwind Clinic"]},
    {"type": "heartbeat", "name": "Hourly invoice export", "check_interval_seconds": 3600, "grace_seconds": 600, "tags": ["Acme Bakery"]},
]

INCIDENT_MONITOR = "Hourly invoice export"
INCIDENT_UPDATES = [
    ("investigating", "The hourly invoice export reported a failure. We're looking into it."),
    ("identified", "The export job ran out of disk space on the reporting server. Space has been freed and the job is being re-run."),
]
RESOLVED_UPDATE = ("resolved", "The export ran successfully and invoices are up to date. Nothing was lost.")


def log(msg):
    print(msg, flush=True)


def all_monitors(api):
    out, offset = [], 0
    while True:
        page = api.request("GET", f"/api/v1/monitors?limit=100&offset={offset}")
        out += page["monitors"]
        offset += len(page["monitors"])
        if not page["monitors"] or offset >= page["total"]:
            return out


def ensure_tags(api):
    existing = {t["name"]: t["id"] for t in api.request("GET", "/api/v1/tags")["tags"]}
    for name in TAGS:
        if name not in existing:
            existing[name] = api.request("POST", "/api/v1/tags", {"name": name})["tag"]["id"]
            log(f"tag: created {name}")
    return existing


def ensure_monitors(api, tags):
    existing = {m["name"]: m for m in all_monitors(api)}
    for spec in MONITORS:
        if spec["name"] in existing:
            continue
        body = {k: v for k, v in spec.items() if k not in ("tags", "paused")}
        body["tag_ids"] = [tags[t] for t in spec["tags"]]
        m = api.request("POST", "/api/v1/monitors", body)["monitor"]
        log(f"monitor: created {m['name']}")
        if spec.get("paused"):
            m = api.request("POST", f"/api/v1/monitors/{m['id']}/pause")["monitor"]
        existing[m["name"]] = m
    return existing


def ensure_status_page(api, monitors, slug):
    if any(p["slug"] == slug for p in api.request("GET", "/api/v1/status-pages")["status_pages"]):
        return
    shown = ["Acme Bakery website", "Acme Bakery online shop", "Northwind Clinic website", "Hourly invoice export"]
    api.request("POST", "/api/v1/status-pages", {
        "slug": slug, "title": "Acme Bakery & Northwind Clinic status", "description": "Live status of our clients' websites and services.",
        "published": True, "monitors": [{"monitor_id": monitors[n]["id"]} for n in shown if n in monitors],
    })
    log(f"status page: created /status/{slug}")


def next_sunday_2am(now):
    days = (6 - now.weekday()) % 7 or 7
    d = (now + datetime.timedelta(days=days)).date()
    return datetime.datetime(d.year, d.month, d.day, 2, 0, tzinfo=datetime.timezone.utc)


def ensure_maintenance(api, now):
    if any(w["name"] == "Weekly deploys" for w in api.request("GET", "/api/v1/maintenance-windows")["maintenance_windows"]):
        return
    api.request("POST", "/api/v1/maintenance-windows", {
        "name": "Weekly deploys", "starts_at": next_sunday_2am(now).isoformat().replace("+00:00", "Z"), "duration_minutes": 60,
        "repeat": "weekly", "all_monitors": True,
    })
    log("maintenance window: created Weekly deploys")


def ensure_incident(api, monitors, wait=time.sleep, timeout=120):
    """A past incident with updates, made the way a real one happens: the job reports a failure, the team posts updates, the job recovers."""
    m = monitors.get(INCIDENT_MONITOR)
    if m is None:
        return
    past = api.request("GET", f"/api/v1/monitors/{m['id']}/incidents?limit=5")["incidents"]
    if any(i["update_count"] > 0 for i in past):
        return
    ping = api.base + "/ping/" + m["heartbeat"]["ping_token"]
    api.ping(ping + "/fail")
    incident = None
    deadline = time.monotonic() + timeout
    while incident is None:
        open_ = api.request("GET", f"/api/v1/monitors/{m['id']}/incidents?status=open")["incidents"]
        incident = open_[0] if open_ else None
        if incident is None:
            if time.monotonic() > deadline:
                raise APIError("the failure ping did not open an incident in time; is the worker running?")
            wait(2)
    for status, message in INCIDENT_UPDATES:
        api.request("POST", f"/api/v1/incidents/{incident['id']}/updates", {"status": status, "message": message})
    api.ping(ping)
    api.request("POST", f"/api/v1/incidents/{incident['id']}/updates", {"status": RESOLVED_UPDATE[0], "message": RESOLVED_UPDATE[1]})
    log(f"incident: {INCIDENT_MONITOR} failed, was updated three times and recovered")


def keep_heartbeats_fresh(api, monitors):
    """Ping the heartbeats once so they show as up (the incident one was already pinged when it recovered)."""
    for name, m in monitors.items():
        if m.get("type") == "heartbeat" and name != INCIDENT_MONITOR:
            api.ping(api.base + "/ping/" + m["heartbeat"]["ping_token"])


def seed(api, slug, now=None):
    now = now or datetime.datetime.now(datetime.timezone.utc)
    overview = api.request("GET", "/api/v1/usage")["usage"]
    if not overview["plan"].get("api_write_enabled"):
        raise APIError(f"the organization is on {overview['plan']['name']}, which can't make changes through the API; use Pro or Agency")
    tags = ensure_tags(api)
    monitors = ensure_monitors(api, tags)
    ensure_status_page(api, monitors, slug)
    ensure_maintenance(api, now)
    ensure_incident(api, monitors)
    keep_heartbeats_fresh(api, monitors)


def main(argv=None):
    p = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    p.add_argument("--base-url", required=True, help="e.g. https://hawkeye.observer or http://localhost:8080")
    p.add_argument("--status-page-slug", default="hawkeye-demo", help="address of the demo status page (/status/<slug>); must be free")
    args = p.parse_args(argv)
    key = os.environ.get("HAWKEYE_API_KEY", "")
    if not key.startswith("hk_"):
        print("seed_demo: set HAWKEYE_API_KEY to a read and write API key (hk_…) of the organization to fill", file=sys.stderr)
        return 2
    try:
        seed(Client(args.base_url, key), args.status_page_slug)
    except APIError as e:
        print(f"seed_demo: {e}", file=sys.stderr)
        return 1
    log("done")
    return 0


if __name__ == "__main__":
    sys.exit(main())
