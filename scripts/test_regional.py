#!/usr/bin/env python3
"""Tests for the regional workers' automation: scripts/regions.py, regional.sh, regional-sync.sh, regional-receive.sh, regional-heartbeat.sh, check_migrations.py
and deploy/wireguard.sh. ssh, docker, sudo, curl and tofu are stubs that record what they were asked to do. The hooks in scripts/deploy.sh are in test_deploy_script.py.

    python3 -m unittest discover -s scripts -p 'test_*.py' -v
"""
import gzip
import http.server
import io
import json
import os
import re
import shutil
import stat
import subprocess
import tarfile
import tempfile
import threading
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
HUB = "203.0.113.1"
EU = "203.0.113.2"
AU = "203.0.113.3"
KEY_HUB = "H" * 43 + "="
KEY_EU = "E" * 43 + "="
KEY_AU = "A" * 43 + "="
RELEASE_KEY = "ssh-ed25519 AAAAC3NzaC1 hawkeye-regional"
WORKERS = {"eu-central": {"ip": EU, "tunnel_ip": "10.66.0.2", "vultr_region": "fra"}, "ap-southeast": {"ip": AU, "tunnel_ip": "10.66.0.3", "vultr_region": "syd"}}
REGIONS = {"primary": {"name": "US East"}, "workers": {
    "eu-central": {"name": "Europe", "vultr_region": "fra", "tunnel_ip": "10.66.0.2", "launched": True},
    "ap-southeast": {"name": "Australia", "vultr_region": "syd", "tunnel_ip": "10.66.0.3", "launched": False}}}
ENV_PROD = """SITE_ADDRESS=hawkeye.test
DATABASE_URL=postgres://hawkeye:s3cret@db:5432/monitoring?sslmode=disable
SECRETS_ENCRYPTION_KEY=the-key
SESSION_SECRET=do-not-send-this
SMTP_PASSWORD=do-not-send-this-either
STRIPE_SECRET_KEY=or-this
LOG_LEVEL=info
APP_VERSION=abc123def456
"""


def stub(path, body):
    path.write_text(body)
    path.chmod(path.stat().st_mode | stat.S_IEXEC)


SSH_STUB = r"""#!/usr/bin/env python3
import json, os, sys
args = sys.argv[1:]
i = next(n for n, a in enumerate(args) if a.startswith("deploy@"))
host, command = args[i].split("@")[1], " ".join(args[i + 1:])
raw = b"" if sys.stdin.isatty() else sys.stdin.buffer.read()
with open(os.environ["SSH_LOG"], "a") as f:
    f.write(json.dumps({"host": host, "command": command, "args": args[:i], "stdin": raw[:300].decode("latin-1")}) + chr(10))
if os.environ.get("SSH_BODY_DIR") and raw[:0] == b"" and len(raw) > 512:
    open(os.path.join(os.environ["SSH_BODY_DIR"], host + ".tar"), "wb").write(raw)
keys = {"203.0.113.1": os.environ["KEY_HUB"], "203.0.113.2": os.environ["KEY_EU"], "203.0.113.3": os.environ["KEY_AU"]}
if command.endswith("-key"): print(keys[host])
elif "hawkeye-regional.pub" in command: print(os.environ["RELEASE_KEY"])
elif "s/^APP_VERSION=//p" in command: print(os.environ.get("WORKER_VERSION" if host != "203.0.113.1" else "HUB_VERSION", "abc123def456"))
elif "State.Health.Status" in command: print(os.environ.get("HEALTH", "healthy"))
if host in os.environ.get("SSH_FAIL_HOSTS", "").split(): sys.exit(255)
sys.exit(int(os.environ.get("FAIL_PING", "0")) if "ping" in command else 0)
"""

TOFU_STUB = r"""#!/usr/bin/env python3
import os, sys
a = sys.argv[1:]
if a[:2] == ["workspace", "show"]: print(os.environ.get("TF_WS", "default"))
elif a[:2] == ["output", "-raw"]: sys.stdout.write({"public_ip": "203.0.113.1"}[a[2]])
elif a[:2] == ["output", "-json"]: print(os.environ["WORKERS_JSON"])
"""


class Base(unittest.TestCase):
    def setUp(self):
        self.tmp = Path(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, self.tmp)
        self.bin = self.tmp / "bin"
        self.bin.mkdir()
        stub(self.bin / "ssh", SSH_STUB)
        stub(self.bin / "tofu", TOFU_STUB)
        self.log = self.tmp / "ssh.log"
        self.log.write_text("")
        self.env = {**os.environ, "PATH": f"{self.bin}:{os.environ['PATH']}", "SSH_LOG": str(self.log), "KEY_HUB": KEY_HUB, "KEY_EU": KEY_EU, "KEY_AU": KEY_AU,
                    "RELEASE_KEY": RELEASE_KEY, "WORKERS_JSON": json.dumps(WORKERS), "HAWKEYE_REGIONAL_KNOWN_HOSTS": str(self.tmp / "known"),
                    "REGIONAL_TRIES": "2", "REGIONAL_WAIT": "0"}

    def calls(self, host=None):
        rows = [json.loads(line) for line in self.log.read_text().splitlines()]
        return [r for r in rows if host is None or r["host"] == host]


class Link(Base):
    def run_script(self, *args, **env):
        return subprocess.run([str(ROOT / "scripts/regional.sh"), *args], stdin=subprocess.DEVNULL, capture_output=True, text=True, timeout=60, env={**self.env, **env})

    def test_every_worker_becomes_a_peer_of_the_hub_and_learns_the_hubs_key(self):
        r = self.run_script("link")
        self.assertEqual(r.returncode, 0, r.stderr)
        apply_hub = next(c for c in self.calls(HUB) if "hub-apply" in c["command"])
        self.assertIn(f"{KEY_EU}=10.66.0.2", apply_hub["command"])
        self.assertIn(f"{KEY_AU}=10.66.0.3", apply_hub["command"])
        self.assertTrue(any(f"worker-apply {KEY_HUB} {HUB} 10.66.0.2" in c["command"] for c in self.calls(EU)))

    def test_the_release_key_can_only_run_the_receiver_and_only_from_the_tunnel(self):
        self.assertEqual(self.run_script("link").returncode, 0)
        line = next(c["stdin"] for c in self.calls(EU) if "authorized_keys" in c["command"])
        self.assertEqual(line.strip(), f'from="10.66.0.1",restrict,command="/home/deploy/hawkeye/scripts/regional-receive.sh" {RELEASE_KEY}')

    def test_a_replaced_hubs_key_replaces_the_old_one_instead_of_adding_to_it(self):
        self.run_script("link")
        command = next(c["command"] for c in self.calls(EU) if "authorized_keys" in c["command"])
        self.assertIn('sed -i "/ hawkeye-regional$/d" ~/.ssh/authorized_keys', command)

    def test_a_broken_tunnel_stops_the_run_and_says_where_to_look(self):
        r = self.run_script("link", FAIL_PING="1")
        self.assertNotEqual(r.returncode, 0)
        self.assertIn("cannot reach the hub", r.stderr)

    def test_it_refuses_a_rehearsal_workspace(self):
        r = self.run_script("link", TF_WS="drill")
        self.assertNotEqual(r.returncode, 0)
        self.assertEqual(self.calls(), [])

    def test_with_no_workers_it_stops_before_touching_anything(self):
        r = self.run_script("link", WORKERS_JSON="{}")
        self.assertIn("no workers", r.stderr)
        self.assertEqual(self.calls(), [])

    def test_a_healthy_worker_on_productions_release_goes_on_to_the_egress_check(self):
        r = self.run_script("verify", "eu-central")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertTrue(any("verify-egress.sh" in c["command"] for c in self.calls(EU)))

    def test_an_unhealthy_worker_fails_before_the_egress_check(self):
        r = self.run_script("verify", "eu-central", HEALTH="starting")
        self.assertNotEqual(r.returncode, 0)
        self.assertFalse(any("verify-egress.sh" in c["command"] for c in self.calls(EU)))

    def test_a_worker_on_an_older_release_fails_verification(self):
        r = self.run_script("verify", "eu-central", WORKER_VERSION="000000000000")
        self.assertIn("production is on", r.stderr)

    def test_an_unknown_region_is_refused(self):
        self.assertIn("no worker called", self.run_script("verify", "mars").stderr)


class Sync(Base):
    """scripts/regional-sync.sh in a throwaway copy of the repo, as the production server runs it."""

    def setUp(self):
        super().setUp()
        self.repo = self.tmp / "repo"
        (self.repo / "scripts").mkdir(parents=True)
        (self.repo / "infra").mkdir()
        for name in ("regional-sync.sh", "regions.py"):
            shutil.copy(ROOT / "scripts" / name, self.repo / "scripts" / name)
        (self.repo / "infra/regions.json").write_text(json.dumps(REGIONS))
        (self.repo / ".env.production").write_text(ENV_PROD)
        self.home = self.tmp / "home"
        (self.home / ".ssh").mkdir(parents=True)
        (self.home / ".ssh/hawkeye-regional").write_text("key")
        self.bodies = self.tmp / "bodies"
        self.bodies.mkdir()
        self.curl_log = self.tmp / "curl.log"
        stub(self.bin / "docker", "#!/bin/sh\n[ \"$1\" = save ] || exit 1\n[ -z \"$NO_IMAGE\" ] || exit 1\nhead -c 4000 /dev/urandom\n")
        stub(self.bin / "sudo", "#!/bin/sh\nprintf 'E\\t%s\\n' \"$LINKED\"\n")
        stub(self.bin / "curl", f"#!/bin/sh\necho \"$*\" >> {self.curl_log}\n")
        self.env.update(HOME=str(self.home), SSH_BODY_DIR=str(self.bodies), LINKED="10.66.0.2/32 10.66.0.3/32", HAWKEYE_HOST_ENV=str(self.home / "host.env"))

    def sync(self, **env):
        return subprocess.run(["bash", str(self.repo / "scripts/regional-sync.sh")], cwd=self.repo, stdin=subprocess.DEVNULL, capture_output=True, text=True, timeout=60, env={**self.env, **env})

    def bundle(self, host):
        with tarfile.open(self.bodies / f"{host}.tar") as t:
            return {m.name.lstrip("./"): t.extractfile(m).read() for m in t.getmembers() if m.isfile()}

    def test_each_worker_gets_its_settings_through_the_tunnel(self):
        r = self.sync()
        self.assertEqual(r.returncode, 0, r.stderr + r.stdout)
        env = self.bundle("10.66.0.2")["worker.env"].decode()
        self.assertIn("DATABASE_URL=postgres://hawkeye:s3cret@10.66.0.1:5432/monitoring?sslmode=disable", env)
        self.assertIn("WORKER_REGION=eu-central", env)
        self.assertIn("ALLOW_INSECURE_DATABASE=true", env)
        self.assertIn("WORKER_REGION=ap-southeast", self.bundle("10.66.0.3")["worker.env"].decode())

    def test_only_what_a_worker_needs_leaves_the_server(self):
        self.sync()
        env = self.bundle("10.66.0.2")["worker.env"].decode()
        self.assertNotIn("do-not-send", env)
        self.assertNotIn("or-this", env)

    def test_a_worker_not_yet_launched_still_knows_its_own_region_but_not_as_customers_would(self):
        self.sync()
        self.assertIn("CHECK_REGIONS='primary:US East,eu-central:Europe'", self.bundle("10.66.0.2")["worker.env"].decode())
        self.assertIn("CHECK_REGIONS='primary:US East,ap-southeast:Australia,eu-central:Europe'", self.bundle("10.66.0.3")["worker.env"].decode())

    def test_the_commit_is_the_only_thing_on_the_command_line(self):
        self.sync()
        for call in self.calls():
            self.assertEqual(call["command"], "abc123def456")
            self.assertIn(str(self.home / ".ssh/hawkeye-regional"), call["args"])

    def test_no_workers_means_nothing_happens(self):
        (self.repo / "infra/regions.json").write_text('{"primary": {"name": "US East"}, "workers": {}}')
        r = self.sync()
        self.assertEqual((r.returncode, r.stdout), (0, ""))
        self.assertEqual(self.calls(), [])

    def test_a_listed_worker_that_is_not_linked_yet_is_skipped_without_failing(self):
        r = self.sync(LINKED="10.66.0.2/32")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("ap-southeast is listed but not linked", r.stdout)
        self.assertEqual([c["host"] for c in self.calls()], ["10.66.0.2"])

    def test_one_failed_worker_does_not_stop_the_others_and_is_reported(self):
        r = self.sync(SSH_FAIL_HOSTS="10.66.0.3")
        self.assertEqual(r.returncode, 1)
        self.assertIn("not updated: ap-southeast", r.stderr)
        self.assertEqual({c["host"] for c in self.calls()}, {"10.66.0.2", "10.66.0.3"})

    def test_a_database_the_tunnel_cannot_reach_is_refused(self):
        (self.repo / ".env.production").write_text("APP_VERSION=abc123def456\nDATABASE_URL=postgres://u:p@managed.example.org:5432/m?sslmode=verify-full\n")
        r = self.sync()
        self.assertEqual(r.returncode, 1)
        self.assertIn("db:5432", r.stderr)
        self.assertEqual(self.calls(), [])

    def test_a_version_that_is_not_a_commit_is_refused(self):
        (self.repo / ".env.production").write_text(ENV_PROD.replace("APP_VERSION=abc123def456", "APP_VERSION=local"))
        self.assertEqual(self.sync().returncode, 1)
        self.assertEqual(self.calls(), [])

    def test_no_copy_of_the_secrets_is_left_behind(self):
        self.sync()
        self.assertEqual([p.name for p in self.home.iterdir() if p.name.startswith(".regional")], [])


class FakeHealthchecks(http.server.BaseHTTPRequestHandler):
    seen = []

    def do_POST(self):
        body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
        FakeHealthchecks.seen.append(body)
        out = json.dumps({"ping_url": "https://hc-ping.com/" + body["name"].replace(" ", "-")}).encode()
        self.send_response(201)
        self.send_header("Content-Length", str(len(out)))
        self.end_headers()
        self.wfile.write(out)

    def log_message(self, *a):
        pass


class Alerts(Sync):
    def setUp(self):
        super().setUp()
        FakeHealthchecks.seen = []
        self.server = http.server.HTTPServer(("127.0.0.1", 0), FakeHealthchecks)
        threading.Thread(target=self.server.serve_forever, daemon=True).start()
        self.addCleanup(self.server.shutdown)
        (self.home / "host.env").write_text("HEALTHCHECKS_API_KEY=hc-key\n")
        self.env["HEALTHCHECKS_API"] = f"http://127.0.0.1:{self.server.server_port}/"

    def test_each_worker_gets_its_own_check_with_a_short_period(self):
        self.assertEqual(self.sync().returncode, 0)
        check = next(c for c in FakeHealthchecks.seen if c["name"] == "HawkEye worker eu-central")
        self.assertEqual((check["timeout"], check["grace"], check["unique"], check["channels"]), (60, 300, ["name"], "*"))
        self.assertEqual(self.bundle("10.66.0.2")["heartbeat.url"].decode().strip(), "https://hc-ping.com/HawkEye-worker-eu-central")

    def test_the_api_key_is_never_on_a_command_line(self):
        self.sync()
        self.assertNotIn("hc-key", self.curl_log.read_text())
        self.assertEqual({c["api_key"] for c in FakeHealthchecks.seen}, {"hc-key"})

    def test_a_clean_run_reports_success_and_a_failed_one_alerts(self):
        self.sync()
        self.assertRegex(self.curl_log.read_text(), r"hc-ping.com/HawkEye-regional-deploys\s*$")
        self.curl_log.unlink()
        self.sync(SSH_FAIL_HOSTS="10.66.0.2")
        log = self.curl_log.read_text()
        self.assertIn("HawkEye-regional-deploys/fail", log)
        self.assertIn("eu-central", log)


class Receive(unittest.TestCase):
    """scripts/regional-receive.sh on a throwaway clone, with docker reporting whatever health the test asks for."""

    def setUp(self):
        self.tmp = Path(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, self.tmp)
        self.bin = self.tmp / "bin"
        self.bin.mkdir()
        self.home = self.tmp / "home"
        self.home.mkdir()
        self.docker_log = self.tmp / "docker.log"
        self.health = self.tmp / "health"
        self.health.write_text("healthy\n")
        stub(self.bin / "flock", "#!/bin/sh\nexit 0\n")
        stub(self.bin / "docker", f"""#!/bin/sh
echo "docker $*" >> {self.docker_log}
case "$1" in
  inspect) cat {self.health};;
  images) printf 'aaaa11112222\\nbbbb11112222\\ncccc11112222\\n';;
  load) cat >/dev/null;;
esac
exit 0
""")
        origin, self.repo = self.tmp / "origin.git", self.tmp / "repo"
        self.git(self.tmp, "init", "-q", "--bare", "-b", "main", str(origin))
        self.git(self.tmp, "clone", "-q", str(origin), str(self.repo))
        (self.repo / "scripts").mkdir()
        (self.repo / "deploy").mkdir()
        shutil.copy(ROOT / "scripts/regional-receive.sh", self.repo / "scripts/regional-receive.sh")
        (self.repo / "deploy/docker-compose.worker.yml").write_text("services: {}\n")
        (self.repo / ".gitignore").write_text(".env.*\n")
        self.commit("one")
        (self.repo / "marker").write_text("two\n")
        self.commit("two")
        self.git(self.repo, "push", "-q", "origin", "main")
        self.one = self.git(self.repo, "rev-parse", "HEAD~1").strip()
        self.two = self.git(self.repo, "rev-parse", "HEAD").strip()
        self.git(self.repo, "reset", "-q", "--hard", self.one)
        (self.repo / ".env.worker").write_text(f"OLD=1\nAPP_VERSION=aaaa11112222\n")

    def git(self, cwd, *args):
        env = dict(os.environ, GIT_AUTHOR_NAME="t", GIT_AUTHOR_EMAIL="t@t", GIT_COMMITTER_NAME="t", GIT_COMMITTER_EMAIL="t@t")
        return subprocess.run(["git", *args], cwd=cwd, env=env, capture_output=True, text=True, check=True).stdout

    def commit(self, msg):
        self.git(self.repo, "add", "-A")
        self.git(self.repo, "commit", "-q", "-m", msg)

    def receive(self, version, body=None):
        if body is None:
            buf = io.BytesIO()
            with tarfile.open(fileobj=buf, mode="w") as t:
                for name, data in (("worker.env", b"NEW=1\n"), ("heartbeat.url", b"https://hc-ping.com/x\n"), ("image.tar.gz", gzip.compress(b"image"))):
                    info = tarfile.TarInfo(name)
                    info.size = len(data)
                    t.addfile(info, io.BytesIO(data))
            body = buf.getvalue()
        env = {**os.environ, "PATH": f"{self.bin}:{os.environ['PATH']}", "HOME": str(self.home), "HAWKEYE_ROOT": str(self.repo), "SSH_ORIGINAL_COMMAND": version, "RECEIVE_TRIES": "1", "RECEIVE_WAIT": "0"}
        return subprocess.run(["bash", str(self.repo / "scripts/regional-receive.sh")], input=body, capture_output=True, timeout=60, env=env)

    def head(self):
        return self.git(self.repo, "rev-parse", "HEAD").strip()

    def test_a_good_release_is_started_and_its_settings_replace_the_old_ones(self):
        r = self.receive(self.two)
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(self.head(), self.two)
        self.assertEqual((self.repo / ".env.worker").read_text(), f"NEW=1\nAPP_VERSION={self.two}\n")
        self.assertIn("HEALTHCHECK_URL=https://hc-ping.com/x", (self.home / ".hawkeye-region-heartbeat").read_text())
        self.assertIn("docker compose --env-file .env.worker -f deploy/docker-compose.worker.yml up -d --remove-orphans", self.docker_log.read_text())

    def test_settings_files_are_private(self):
        self.receive(self.two)
        self.assertEqual((self.repo / ".env.worker").stat().st_mode & 0o777, 0o600)

    def test_a_release_that_does_not_become_healthy_is_undone(self):
        self.health.write_text("unhealthy\n")
        r = self.receive(self.two)
        self.assertNotEqual(r.returncode, 0)
        self.assertEqual(self.head(), self.one)
        self.assertEqual((self.repo / ".env.worker").read_text(), "OLD=1\nAPP_VERSION=aaaa11112222\n")
        self.assertEqual(self.docker_log.read_text().count("up -d --remove-orphans"), 2)

    def test_old_images_go_but_the_previous_one_stays_for_going_back(self):
        self.receive(self.two)
        log = self.docker_log.read_text()
        self.assertIn("docker rmi hawkeye-worker:bbbb11112222", log)
        self.assertNotIn("docker rmi hawkeye-worker:aaaa11112222", log)

    def test_anything_that_is_not_a_commit_is_refused(self):
        for bad in ("", "main", "abc; rm -rf /", "../../x"):
            self.assertEqual(self.receive(bad, body=b"").returncode, 2, bad)
        self.assertFalse(self.docker_log.exists())

    def test_it_leaves_no_copy_of_the_settings_behind(self):
        self.receive(self.two)
        self.assertEqual([p.name for p in self.home.iterdir() if p.name.startswith(".regional-receive")], [])


class Heartbeat(unittest.TestCase):
    def setUp(self):
        self.tmp = Path(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, self.tmp)
        self.curl = self.tmp / "curl.log"
        for name, body in (("curl", f'#!/bin/sh\necho "$*" >> {self.curl}\n'), ("docker", '#!/bin/sh\necho "$STATE"\n[ "$STATE" != gone ]\n')):
            stub(self.tmp / name, body)
        self.settings = self.tmp / "settings"

    def beat(self, state):
        env = {**os.environ, "PATH": f"{self.tmp}:{os.environ['PATH']}", "STATE": state, "HAWKEYE_REGION_HEARTBEAT": str(self.settings)}
        return subprocess.run(["bash", str(ROOT / "scripts/regional-heartbeat.sh")], capture_output=True, text=True, env=env)

    def test_it_is_silent_until_it_has_a_ping_address(self):
        self.assertEqual(self.beat("healthy").returncode, 0)
        self.assertFalse(self.curl.exists())

    def test_a_healthy_worker_pings_and_an_unhealthy_one_reports_it(self):
        self.settings.write_text("HEALTHCHECK_URL=https://hc-ping.com/x\n")
        self.assertEqual(self.beat("healthy").returncode, 0)
        self.assertNotIn("/fail", self.curl.read_text())
        self.assertEqual(self.beat("unhealthy").returncode, 1)
        self.assertIn("hc-ping.com/x/fail", self.curl.read_text())


class Regions(unittest.TestCase):
    def setUp(self):
        self.tmp = Path(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, self.tmp)
        self.file = self.tmp / "regions.json"
        self.file.write_text(json.dumps(REGIONS))

    def regions(self, *args):
        return subprocess.run(["python3", str(ROOT / "scripts/regions.py"), *args], capture_output=True, text=True, env={**os.environ, "HAWKEYE_REGIONS_FILE": str(self.file)})

    def test_the_committed_file_is_valid_and_lists_nothing_customers_see_by_accident(self):
        r = subprocess.run(["python3", str(ROOT / "scripts/regions.py"), "validate"], capture_output=True, text=True)
        self.assertEqual(r.returncode, 0, r.stderr)

    def test_only_launched_regions_reach_customers_and_the_primary_stays_first(self):
        self.assertEqual(self.regions("check-regions").stdout.strip(), "primary:US East,eu-central:Europe")

    def test_nothing_launched_means_nothing_is_set(self):
        data = json.loads(json.dumps(REGIONS))
        data["workers"]["eu-central"]["launched"] = False
        self.file.write_text(json.dumps(data))
        self.assertEqual(self.regions("check-regions").stdout.strip(), "")

    def test_env_files_are_updated_in_place_and_keep_their_permissions(self):
        env = self.tmp / ".env"
        env.write_text("A=1\nCHECK_REGIONS=old\nB=2\n")
        env.chmod(0o600)
        self.assertEqual(self.regions("apply-env", str(env)).returncode, 0)
        self.assertEqual(env.read_text(), "A=1\nCHECK_REGIONS='primary:US East,eu-central:Europe'\nB=2\n")
        self.assertEqual(env.stat().st_mode & 0o777, 0o600)

    def test_a_region_can_be_taken_away_again(self):
        self.file.write_text('{"primary": {"name": "US East"}, "workers": {}}')
        env = self.tmp / ".env"
        env.write_text("A=1\nCHECK_REGIONS='primary:US East,eu-central:Europe'\n")
        self.regions("apply-env", str(env))
        self.assertEqual(env.read_text(), "A=1\n")

    def test_mistakes_are_refused(self):
        for change, why in (({"tunnel_ip": "10.66.0.1"}, "tunnel_ip"), ({"tunnel_ip": "10.66.0.3"}, "already taken"), ({"name": "Europe, West"}, "name"), ({"launched": "yes"}, "launched"), ({"vultr_region": ""}, "vultr_region")):
            data = json.loads(json.dumps(REGIONS))
            data["workers"]["eu-central"].update(change)
            self.file.write_text(json.dumps(data))
            r = self.regions("validate")
            self.assertNotEqual(r.returncode, 0, change)
            self.assertIn(why, r.stderr)

    def test_the_primary_code_is_reserved(self):
        data = json.loads(json.dumps(REGIONS))
        data["workers"]["primary"] = data["workers"].pop("eu-central")
        self.file.write_text(json.dumps(data))
        self.assertIn("primary", self.regions("validate").stderr)


class Migrations(unittest.TestCase):
    def problems(self, sql):
        import importlib.util
        spec = importlib.util.spec_from_file_location("check_migrations", ROOT / "scripts/check_migrations.py")
        m = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(m)
        return m.problems(sql)

    def test_adding_things_is_fine(self):
        self.assertEqual(self.problems("ALTER TABLE monitors ADD COLUMN x integer;\nCREATE INDEX i ON monitors (x);"), [])

    def test_removing_or_renaming_is_flagged(self):
        for sql in ("ALTER TABLE t DROP COLUMN x;", "DROP TABLE t;", "ALTER TABLE t RENAME COLUMN a TO b;", "ALTER TABLE t RENAME TO u;", "ALTER TABLE t ALTER COLUMN x TYPE text;", "ALTER TABLE t ALTER COLUMN x SET NOT NULL;"):
            self.assertNotEqual(self.problems(sql), [], sql)

    def test_a_comment_does_not_count_as_a_change(self):
        self.assertEqual(self.problems("-- we used to DROP COLUMN x here\nSELECT 1;"), [])

    def test_a_stated_reason_lets_it_through(self):
        self.assertEqual(self.problems("-- worker-safe: unused since release abc\nALTER TABLE t DROP COLUMN x;"), [])
        self.assertNotEqual(self.problems("-- worker-safe:\nALTER TABLE t DROP COLUMN x;"), [])


class Render(unittest.TestCase):
    def wg(self, *args):
        return subprocess.run(["bash", str(ROOT / "deploy/wireguard.sh"), *args], capture_output=True, text=True)

    def test_a_worker_may_reach_only_the_hub(self):
        conf = self.wg("render-worker", KEY_HUB, HUB, "10.66.0.2").stdout
        self.assertIn("AllowedIPs = 10.66.0.1/32", conf)
        self.assertIn(f"Endpoint = {HUB}:51820", conf)
        self.assertIn("Address = 10.66.0.2/32", conf)

    def test_the_hub_routes_each_peer_to_one_address(self):
        conf = self.wg("render-hub", f"{KEY_EU}=10.66.0.2", f"{KEY_AU}=10.66.0.3").stdout
        self.assertIn(f"PublicKey = {KEY_EU}\nAllowedIPs = 10.66.0.2/32", conf)
        self.assertIn(f"PublicKey = {KEY_AU}\nAllowedIPs = 10.66.0.3/32", conf)

    def test_the_tunnel_forwards_the_database_port_and_drops_everything_else(self):
        conf = self.wg("render-hub").stdout
        self.assertIn("--dport 5432 -j DNAT --to-destination 172.30.10.5:5432", conf)
        up = [line for line in conf.splitlines() if line.startswith("PostUp") and "DOCKER-USER" in line]
        # Each rule goes in at position 1, so the ACCEPT must be inserted last to end up above the DROP.
        self.assertIn("-j DROP", up[0])
        self.assertIn("-j ACCEPT", up[1])

    def test_every_rule_added_is_removed_again(self):
        conf = self.wg("render-hub").stdout
        rule = lambda line: re.sub(r"-[ADI] (\S+)( 1)? ", r"\1 ", line.split("iptables -w ")[1])
        ups = sorted(rule(l) for l in conf.splitlines() if l.startswith("PostUp = iptables"))
        downs = sorted(rule(l) for l in conf.splitlines() if l.startswith("PostDown = iptables"))
        self.assertEqual(ups, downs)

    def test_rendered_output_has_no_private_key(self):
        self.assertNotIn("PrivateKey", self.wg("render-hub", f"{KEY_EU}=10.66.0.2").stdout)
        self.assertNotIn("PrivateKey", self.wg("render-worker", KEY_HUB, HUB, "10.66.0.2").stdout)

    def test_the_installed_config_carries_the_key_so_syncconf_cannot_wipe_it(self):
        # `wg syncconf` makes the interface match the config exactly: a config with no key removes it and drops every tunnel.
        script = (ROOT / "deploy/wireguard.sh").read_text()
        self.assertIn('(umask 077; with_key > "$DIR/$IFACE.conf.new")', script)
        self.assertNotIn("private-key", script.split("ACTION=")[0].split("render_hub()")[1].split("with_key()")[0])
        with tempfile.TemporaryDirectory() as d:
            (Path(d) / "private.key").write_text("the-private-key\n")
            sed = (ROOT / "deploy/wireguard.sh").read_text().replace("DIR=/etc/wireguard", f"DIR={d}")
            (Path(d) / "wg.sh").write_text(sed)
            out = subprocess.run(["bash", f"{d}/wg.sh", "with-key"], input="[Interface]\nAddress = 10.66.0.1/24\n\n[Peer]\nPublicKey = x\n", capture_output=True, text=True).stdout
        self.assertEqual(out, "[Interface]\nPrivateKey = the-private-key\nAddress = 10.66.0.1/24\n\n[Peer]\nPublicKey = x\n")

    def test_malformed_values_are_refused(self):
        self.assertNotEqual(self.wg("render-hub", "not-a-key=10.66.0.2").returncode, 0)
        self.assertNotEqual(self.wg("render-worker", KEY_HUB, "bad host", "10.66.0.2").returncode, 0)


class Compose(unittest.TestCase):
    def test_the_worker_matches_what_the_egress_filter_expects(self):
        worker = (ROOT / "deploy/docker-compose.worker.yml").read_text()
        prod = (ROOT / "deploy/docker-compose.prod.yml").read_text()
        for shared in ("name: hawkeye", "subnet: 172.30.10.0/24", "cap_drop: [ALL]", "read_only: true"):
            self.assertIn(shared, worker)
            self.assertIn(shared, prod)
        self.assertIn('WORKER_CHECKS_ONLY: "true"', worker)
        self.assertNotIn("WORKER_CHECKS_ONLY", prod)
        self.assertNotIn("ports:", worker)
        self.assertNotIn("build:", worker)

    @unittest.skipUnless(shutil.which("docker"), "needs docker")
    def test_compose_can_parse_it_and_requires_a_version(self):
        env_file = ROOT / ".env.worker"
        created = not env_file.exists()
        if created:
            env_file.write_text("")
            self.addCleanup(env_file.unlink)
        base = ["docker", "compose", "--env-file", "/dev/null", "-f", str(ROOT / "deploy/docker-compose.worker.yml"), "config", "--quiet"]
        ok = subprocess.run(base, capture_output=True, text=True, env={**os.environ, "APP_VERSION": "abc123"})
        self.assertEqual(ok.returncode, 0, ok.stderr)
        env = {k: v for k, v in os.environ.items() if k != "APP_VERSION"}
        missing = subprocess.run(base, capture_output=True, text=True, env=env)
        self.assertNotEqual(missing.returncode, 0)
        self.assertIn("APP_VERSION is missing", missing.stderr)


if __name__ == "__main__":
    unittest.main()
