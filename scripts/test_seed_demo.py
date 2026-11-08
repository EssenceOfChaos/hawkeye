import datetime
import unittest

import seed_demo


class FakeAPI:
    """Records requests and answers like the HawkEye API, keeping just enough state for the seed to make sense."""

    def __init__(self, plan_writes=True):
        self.base = "https://hawkeye.test"
        self.calls = []
        self.pings = []
        self.tags, self.monitors, self.pages, self.windows = [], [], [], []
        self.incident = None
        self.plan_writes = plan_writes

    def request(self, method, path, body=None):
        self.calls.append((method, path, body))
        if path == "/api/v1/usage":
            return {"usage": {"plan": {"name": "Pro" if self.plan_writes else "Free", "api_write_enabled": self.plan_writes}}}
        if path == "/api/v1/tags":
            if method == "POST":
                t = {"id": f"t{len(self.tags)}", "name": body["name"]}
                self.tags.append(t)
                return {"tag": t}
            return {"tags": self.tags}
        if path.startswith("/api/v1/monitors?"):
            return {"monitors": self.monitors, "total": len(self.monitors)}
        if path == "/api/v1/monitors" and method == "POST":
            m = dict(body, id=f"m{len(self.monitors)}", type=body.get("type", "http"))
            if m["type"] == "heartbeat":
                m["heartbeat"] = {"ping_token": f"tok{len(self.monitors)}"}
            self.monitors.append(m)
            return {"monitor": m}
        if path.endswith("/pause"):
            return {"monitor": next(m for m in self.monitors if path.startswith(f"/api/v1/monitors/{m['id']}/"))}
        if path == "/api/v1/status-pages":
            if method == "POST":
                self.pages.append(body)
                return {"status_page": body}
            return {"status_pages": self.pages}
        if path == "/api/v1/maintenance-windows":
            if method == "POST":
                self.windows.append(body)
                return {"maintenance_window": body}
            return {"maintenance_windows": self.windows}
        if "/incidents" in path and path.startswith("/api/v1/monitors/"):
            if "status=open" in path:
                return {"incidents": [self.incident] if self.incident and self.incident["status"] == "open" else []}
            return {"incidents": [self.incident] if self.incident else []}
        if path.startswith("/api/v1/incidents/") and path.endswith("/updates"):
            self.incident["update_count"] += 1
            return {"update": body}
        raise AssertionError(f"unexpected {method} {path}")

    def ping(self, url):
        self.pings.append(url)
        if url.endswith("/fail"):
            self.incident = {"id": "i1", "status": "open", "update_count": 0}
        elif self.incident and url.endswith(self.incident_token()):
            self.incident["status"] = "resolved"

    def incident_token(self):
        return next(m for m in self.monitors if m["name"] == seed_demo.INCIDENT_MONITOR)["heartbeat"]["ping_token"]


NOW = datetime.datetime(2026, 11, 11, 15, 0, tzinfo=datetime.timezone.utc)  # a Wednesday


class SeedTest(unittest.TestCase):
    def test_creates_everything_once(self):
        api = FakeAPI()
        seed_demo.seed(api, "demo", now=NOW)
        self.assertEqual([t["name"] for t in api.tags], seed_demo.TAGS)
        self.assertEqual(len(api.monitors), len(seed_demo.MONITORS))
        self.assertTrue(all(m["tag_ids"] for m in api.monitors))
        self.assertEqual(api.pages[0]["slug"], "demo")
        self.assertTrue(api.pages[0]["published"])
        self.assertEqual(api.windows[0]["starts_at"], "2026-11-15T02:00:00Z")  # the next Sunday
        self.assertEqual(api.incident["status"], "resolved")
        self.assertEqual(api.incident["update_count"], 3)
        self.assertIn(("POST", "/api/v1/monitors/m3/pause", None), api.calls)

        writes = len([c for c in api.calls if c[0] == "POST"])
        seed_demo.seed(api, "demo", now=NOW)
        self.assertEqual(len([c for c in api.calls if c[0] == "POST"]), writes, "a second run creates nothing new")

    def test_refuses_a_plan_without_api_writes(self):
        with self.assertRaises(seed_demo.APIError):
            seed_demo.seed(FakeAPI(plan_writes=False), "demo", now=NOW)

    def test_next_sunday(self):
        self.assertEqual(seed_demo.next_sunday_2am(NOW).isoformat(), "2026-11-15T02:00:00+00:00")
        sunday = datetime.datetime(2026, 11, 15, 1, 0, tzinfo=datetime.timezone.utc)
        self.assertEqual(seed_demo.next_sunday_2am(sunday).date().isoformat(), "2026-11-22")

    def test_key_must_come_from_the_environment(self):
        import os
        old = os.environ.pop("HAWKEYE_API_KEY", None)
        try:
            self.assertEqual(seed_demo.main(["--base-url", "https://hawkeye.test"]), 2)
        finally:
            if old is not None:
                os.environ["HAWKEYE_API_KEY"] = old


if __name__ == "__main__":
    unittest.main()
