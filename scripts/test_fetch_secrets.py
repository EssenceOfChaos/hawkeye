#!/usr/bin/env python3
"""Tests for scripts/fetch-secrets.py and scripts/sync-secrets.sh, against a fake Infisical on localhost.

    python3 -m unittest discover -s scripts -p 'test_*.py' -v
"""
import importlib.util
import json
import os
import re
import shutil
import stat
import subprocess
import sys
import tempfile
import threading
import unittest
import urllib.parse
from contextlib import redirect_stderr, redirect_stdout
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from io import StringIO
from pathlib import Path

HERE = Path(__file__).resolve().parent
ROOT = HERE.parent
spec = importlib.util.spec_from_file_location("fetch_secrets", HERE / "fetch-secrets.py")
fs = importlib.util.module_from_spec(spec)
spec.loader.exec_module(fs)
fs.BACKOFF = (0, 0)  # no waiting between attempts in tests

REQUIRED = {n: f"value-of-{n.lower()}" for n, (scope, req) in fs.load_manifest().items() if req}
REQUIRED.update({"SITE_ADDRESS": "hawkeye.test", "DATABASE_URL": "postgres://hawkeye:p%40ss@db:5432/monitoring?sslmode=disable"})


class FakeInfisical:
    """Answers the two calls fetch-secrets makes, and records them."""

    def __init__(self):
        self.secrets = dict(REQUIRED)
        self.login_status = 200
        self.list_status = 200
        self.hidden = False
        self.login_wants = None   # "json": answer a form-encoded login with 415
        self.older_api = False    # only /api/v3/secrets/raw exists (workspaceId)
        self.requests = []
        outer = self

        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *a):
                pass

            def _send(self, status, body):
                data = json.dumps(body).encode()
                self.send_response(status)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(data)))
                self.end_headers()
                self.wfile.write(data)

            def do_POST(self):
                raw = self.rfile.read(int(self.headers["Content-Length"])).decode()
                ctype = self.headers.get("Content-Type")
                form = json.loads(raw) and {k: [v] for k, v in json.loads(raw).items()} if ctype == "application/json" else urllib.parse.parse_qs(raw)
                outer.requests.append(("POST", self.path, form, ctype))
                if outer.login_wants == "json" and ctype != "application/json":
                    return self._send(415, {"message": "Unsupported Media Type"})
                if outer.login_status != 200:
                    return self._send(outer.login_status, {"message": "Invalid credentials"})
                self._send(200, {"accessToken": "tok-123", "expiresIn": 7200, "tokenType": "Bearer"})

            def do_GET(self):
                url = urllib.parse.urlparse(self.path)
                outer.requests.append(("GET", url.path, urllib.parse.parse_qs(url.query), self.headers.get("Authorization")))
                if outer.older_api != (url.path == "/api/v3/secrets/raw"):
                    return self._send(404, {"message": "Route not found"})
                if outer.list_status != 200:
                    return self._send(outer.list_status, {"message": "nope"})
                items = [{"secretKey": k, "secretValue": "" if outer.hidden else v, "secretValueHidden": outer.hidden} for k, v in outer.secrets.items()]
                self._send(200, {"secrets": items, "imports": []})

        self.server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        self.url = f"http://127.0.0.1:{self.server.server_address[1]}"
        threading.Thread(target=self.server.serve_forever, daemon=True).start()

    def close(self):
        self.server.shutdown()
        self.server.server_close()


class Base(unittest.TestCase):
    def setUp(self):
        self.tmp = Path(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, self.tmp, True)
        self.api = FakeInfisical()
        self.addCleanup(self.api.close)
        self.creds = self.tmp / "creds.env"
        self.write_creds()

    def write_creds(self, **over):
        values = {"INFISICAL_CLIENT_ID": "cid", "INFISICAL_CLIENT_SECRET": "csecret", "INFISICAL_PROJECT_ID": "proj-1", "INFISICAL_ENV": "prod",
                  "INFISICAL_API_URL": self.api.url}
        values.update(over)
        self.creds.write_text("".join(f"{k}={v}\n" for k, v in values.items()))
        self.creds.chmod(0o600)

    def run_cli(self, *args):
        out, err = StringIO(), StringIO()
        with redirect_stdout(out), redirect_stderr(err):
            code = fs.main(["--creds", str(self.creds), *args])
        return code, out.getvalue(), err.getvalue()


class ParsingTests(unittest.TestCase):
    def test_env_text_is_read_as_docker_compose_reads_it(self):
        got = fs.parse_env_text("""
# comment
SITE=hawkeye.test          # inline comment
export TOKEN="a b # c"
QUOTED='it $stays literal'
EMPTY=
NOEQ
1BAD=x
URL=postgres://u:p@h/db?x=1#frag
""")
        self.assertEqual(got, {"SITE": "hawkeye.test", "TOKEN": "a b # c", "QUOTED": "it $stays literal", "EMPTY": "", "URL": "postgres://u:p@h/db?x=1#frag"})

    def test_manifest_is_valid_and_matches_the_documented_example(self):
        manifest = fs.load_manifest()
        example = (ROOT / "deploy/.env.production.example").read_text()
        documented = set(re.findall(r"^#?\s*([A-Z][A-Z0-9_]+)=", example, re.M))
        active = set(re.findall(r"^([A-Z][A-Z0-9_]+)=", example, re.M)) - {"APP_VERSION"}
        self.assertEqual(sorted(set(manifest) - documented), [], "listed in deploy/env.manifest but not documented in deploy/.env.production.example")
        self.assertEqual(sorted(active - set(manifest)), [], "set in deploy/.env.production.example but missing from deploy/env.manifest")
        self.assertNotIn("APP_VERSION", manifest)
        self.assertIn("SECRETS_ENCRYPTION_KEY", manifest)
        self.assertEqual(manifest["HEALTHCHECK_URL"], ("host", False))
        self.assertEqual(manifest["BACKUP_SECRET_KEY"], ("host", True))

    def test_a_bad_manifest_line_is_refused(self):
        p = Path(tempfile.mkdtemp()) / "m"
        p.write_text("SITE_ADDRESS app maybe\n")
        with self.assertRaises(fs.Failure):
            fs.load_manifest(p)
        p.write_text("A app required\nA host optional\n")
        with self.assertRaises(fs.Failure):
            fs.load_manifest(p)


class WritingTests(unittest.TestCase):
    TRICKY = ["plain", "with space", "dollar $HOME and ${X}", "hash # not comment", "star * and ? and !", "back\\slash", "unicode é✓", "a=b=c",
              "https://u:p%40@h:5432/db?sslmode=disable&x=1", "  leading and trailing  ", "", "semi;colon `tick` (paren)", "double \"quote\""]

    def test_quote_only_when_needed(self):
        self.assertEqual(fs.quote("hawkeye.example.org"), "hawkeye.example.org")
        self.assertEqual(fs.quote("AbC123+/=="), "AbC123+/==")
        self.assertEqual(fs.quote("a b"), "'a b'")
        self.assertEqual(fs.quote(""), "")

    def test_what_is_written_reads_back_identically_in_bash(self):
        values = {f"V{i}": v for i, v in enumerate(self.TRICKY)}
        f = Path(tempfile.mkdtemp()) / "out.env"
        f.write_text(fs.render(values, "t"))
        script = "set -a; . \"$1\"; " + "".join(f'printf "%s\\0" "${{V{i}}}"; ' for i in range(len(values)))
        out = subprocess.run(["bash", "-c", script, "bash", str(f)], capture_output=True, check=True).stdout.decode().split("\0")[:-1]
        self.assertEqual(out, self.TRICKY)

    def test_what_is_written_reads_back_identically_in_docker_compose(self):
        if not shutil.which("docker") or subprocess.run(["docker", "compose", "version"], capture_output=True).returncode != 0:
            self.skipTest("docker compose is not available")
        names = [f"V{i}" for i in range(len(self.TRICKY))]
        d = Path(tempfile.mkdtemp())
        (d / "app.env").write_text(fs.render(dict(zip(names, self.TRICKY)), "t"))
        (d / "compose.yml").write_text("services:\n  x:\n    image: scratch\n    environment:\n" + "".join(f"      {n}: ${{{n}-UNSET}}\n" for n in names))
        out = subprocess.run(["docker", "compose", "--env-file", str(d / "app.env"), "-f", str(d / "compose.yml"), "config", "--format", "json"],
                             capture_output=True, text=True)
        if out.returncode != 0:
            self.skipTest(f"this docker compose cannot print JSON config: {out.stderr[:100]}")
        env = json.loads(out.stdout)["services"]["x"]["environment"]
        # `compose config` prints a literal dollar as $$ so its output can be fed back to compose; undo that to compare.
        self.assertEqual([env[n].replace("$$", "$") for n in names], self.TRICKY)

    def test_files_are_private_and_replaced_atomically(self):
        d = Path(tempfile.mkdtemp())
        target = d / ".env.production"
        target.write_text("OLD=1\n")
        fs.write_private(target, "NEW=2\n")
        self.assertEqual(target.read_text(), "NEW=2\n")
        self.assertEqual(stat.S_IMODE(target.stat().st_mode), 0o600)
        self.assertEqual([p.name for p in d.iterdir()], [".env.production"], "no temporary file may be left behind")

    def test_validation(self):
        manifest = {"A": ("app", True), "B": ("app", False), "H": ("host", True)}
        self.assertEqual(fs.validate({"A": "x"}, {"H": "y"}, manifest), [])
        self.assertIn("A is required", "".join(fs.validate({}, {"H": "y"}, manifest)))
        self.assertIn("H is required", "".join(fs.validate({"A": "x"}, {}, manifest)))
        self.assertIn("A is required", "".join(fs.validate({"A": ""}, {"H": "y"}, manifest)))
        self.assertIn("placeholder", "".join(fs.validate({"A": "CHANGE_ME_to_32_chars"}, {"H": "y"}, manifest)))
        self.assertIn("placeholder", "".join(fs.validate({"A": "x", "B": "changeme"}, {"H": "y"}, manifest)))
        self.assertIn("line break", "".join(fs.validate({"A": "x\ny"}, {"H": "y"}, manifest)))
        self.assertIn("single quote", "".join(fs.validate({"A": "it's a secret"}, {"H": "y"}, manifest)))
        self.assertIn("not a valid variable name", "".join(fs.validate({"A": "x", "bad name": "1"}, {"H": "y"}, manifest)))

    def test_split_routes_by_manifest_and_applies_overrides(self):
        manifest = {"A": ("app", True), "H": ("host", True), "W": ("app", False)}
        app, host, warnings = fs.split({"A": "1", "H": "2", "W": "3", "NEW": "4"}, manifest, {"A": "override"}, ["W"])
        self.assertEqual(app, {"A": "override", "NEW": "4"})
        self.assertEqual(host, {"H": "2"})
        self.assertEqual(len(warnings), 1)
        self.assertIn("1 setting(s)", warnings[0])
        self.assertNotIn("NEW", warnings[0])


class CommandTests(Base):
    def test_write_makes_both_files_from_the_fake_service(self):
        app, host = self.tmp / "app.env", self.tmp / "host.env"
        code, out, err = self.run_cli("write", "--app-out", str(app), "--host-out", str(host))
        self.assertEqual(code, 0, err)
        app_values, host_values = fs.parse_env_text(app.read_text()), fs.parse_env_text(host.read_text())
        self.assertEqual(app_values["SITE_ADDRESS"], "hawkeye.test")
        self.assertEqual(app_values["DATABASE_URL"], REQUIRED["DATABASE_URL"])
        self.assertIn("BACKUP_BUCKET", host_values)
        self.assertNotIn("BACKUP_BUCKET", app_values, "host-only values must never reach the containers' file")
        self.assertNotIn("SESSION_SECRET", host_values)
        self.assertEqual(stat.S_IMODE(app.stat().st_mode), 0o600)
        self.assertEqual(stat.S_IMODE(host.stat().st_mode), 0o600)
        self.assertNotIn("value-of-session_secret", out + err, "values must never be printed")

    def test_the_requests_follow_the_infisical_api(self):
        self.run_cli("check")
        login, listing = self.api.requests
        self.assertEqual(login[:2], ("POST", "/api/v1/auth/universal-auth/login"))
        self.assertEqual(login[2], {"clientId": ["cid"], "clientSecret": ["csecret"]})
        self.assertEqual(listing[:2], ("GET", "/api/v4/secrets"))
        self.assertEqual(listing[2]["projectId"], ["proj-1"])
        self.assertEqual(listing[2]["environment"], ["prod"])
        self.assertEqual(listing[2]["secretPath"], ["/"])
        self.assertEqual(listing[3], "Bearer tok-123")

    def test_a_server_that_wants_a_json_login_is_asked_that_way(self):
        self.api.login_wants = "json"
        code, _, err = self.run_cli("check")
        self.assertEqual(code, 0, err)
        logins = [r for r in self.api.requests if r[0] == "POST"]
        self.assertEqual([r[3] for r in logins], ["application/x-www-form-urlencoded", "application/json"])
        self.assertEqual(logins[1][2], {"clientId": ["cid"], "clientSecret": ["csecret"]})

    def test_rejected_credentials_are_not_retried_in_another_format(self):
        self.api.login_status = 401
        self.run_cli("check")
        self.assertEqual(len([r for r in self.api.requests if r[0] == "POST"]), 1)

    def test_an_older_server_with_the_v3_list_is_read_too(self):
        self.api.older_api = True
        code, _, err = self.run_cli("check")
        self.assertEqual(code, 0, err)
        listing = [r for r in self.api.requests if r[0] == "GET"]
        self.assertEqual([r[1] for r in listing], ["/api/v4/secrets", "/api/v3/secrets/raw"])
        self.assertEqual(listing[1][2]["workspaceId"], ["proj-1"])

    def test_a_missing_project_is_still_reported_as_not_found(self):
        self.api.list_status = 404
        self.assertEqual(self.run_cli("check")[0], fs.NOT_FOUND)

    def test_overrides_and_drops_make_a_staging_copy(self):
        self.api.secrets["WWW_ADDRESS"] = "www.hawkeye.test"
        app = self.tmp / "app.env"
        code, _, err = self.run_cli("write", "--app-out", str(app), "--override", "SITE_ADDRESS=staging.hawkeye.test", "--drop", "WWW_ADDRESS")
        self.assertEqual(code, 0, err)
        values = fs.parse_env_text(app.read_text())
        self.assertEqual(values["SITE_ADDRESS"], "staging.hawkeye.test")
        self.assertNotIn("WWW_ADDRESS", values)

    def test_a_missing_required_value_stops_everything_and_touches_nothing(self):
        del self.api.secrets["SESSION_SECRET"]
        app = self.tmp / "app.env"
        app.write_text("KEEP=me\n")
        code, _, err = self.run_cli("write", "--app-out", str(app))
        self.assertEqual(code, fs.INVALID)
        self.assertIn("SESSION_SECRET is required", err)
        self.assertEqual(app.read_text(), "KEEP=me\n")

    def test_a_placeholder_value_stops_the_run(self):
        self.api.secrets["SMTP_PASSWORD"] = "CHANGE_ME"
        code, _, err = self.run_cli("check")
        self.assertEqual((code, "SMTP_PASSWORD still holds a placeholder" in err), (fs.INVALID, True))

    def test_exit_codes_say_what_went_wrong(self):
        self.api.login_status = 401
        self.assertEqual(self.run_cli("check")[0], fs.REJECTED)
        self.api.login_status, self.api.list_status = 200, 403
        self.assertEqual(self.run_cli("check")[0], fs.REJECTED)
        self.api.list_status = 404
        code, _, err = self.run_cli("check")
        self.assertEqual(code, fs.NOT_FOUND)
        self.assertIn("INFISICAL_PROJECT_ID", err)
        self.api.list_status = 500
        self.assertEqual(self.run_cli("check")[0], fs.UNREACHABLE)
        self.api.list_status = 429
        self.assertEqual(self.run_cli("check")[0], fs.UNREACHABLE)

    def test_a_service_that_is_not_there_is_unreachable(self):
        self.write_creds(INFISICAL_API_URL="http://127.0.0.1:9")
        code, _, err = self.run_cli("check")
        self.assertEqual(code, fs.UNREACHABLE)
        self.assertIn("could not reach", err)

    def test_an_identity_that_cannot_read_values_is_told_so(self):
        self.api.hidden = True
        code, _, err = self.run_cli("check")
        self.assertEqual(code, fs.REJECTED)
        self.assertIn("read access to secret values", err)

    def test_settings_problems(self):
        self.creds.chmod(0o644)
        code, _, err = self.run_cli("check")
        self.assertEqual(code, fs.USAGE)
        self.assertIn("chmod 600", err)
        self.write_creds(INFISICAL_CLIENT_SECRET="")
        self.assertEqual(self.run_cli("check")[0], fs.USAGE)
        self.write_creds(INFISICAL_API_URL="http://evil.example.com")
        code, _, err = self.run_cli("check")
        self.assertEqual(code, fs.USAGE)
        self.assertIn("https://", err)
        self.creds.unlink()
        self.assertEqual(self.run_cli("check")[0], fs.USAGE)

    def test_the_settings_file_is_found_where_a_laptop_or_a_server_keeps_it(self):
        home = self.tmp / "home"
        (home / ".hawkeye-secrets").mkdir(parents=True)
        laptop = home / ".hawkeye-secrets" / ".hawkeye-infisical.env"
        self.creds.rename(laptop)
        old_home, old_env = os.environ.get("HOME"), {k: os.environ.pop(k, None) for k in ("HAWKEYE_INFISICAL_ENV", "HAWKEYE_SECRETS_DIR")}
        os.environ["HOME"] = str(home)
        try:
            self.assertEqual(fs.find_creds(), laptop)
            out, err = StringIO(), StringIO()
            with redirect_stdout(out), redirect_stderr(err):
                self.assertEqual(fs.main(["check"]), 0, err.getvalue())   # no --creds: found in ~/.hawkeye-secrets
            server = home / ".hawkeye-infisical.env"
            laptop.rename(server)
            self.assertEqual(fs.find_creds(), server)
            server.rename(laptop)
            server.write_text("x")
            laptop.rename(self.tmp / "keep.env")
            server.unlink()
            with redirect_stdout(StringIO()), redirect_stderr(err):
                self.assertEqual(fs.main(["check"]), fs.USAGE)
            self.assertIn("~/.hawkeye-secrets/.hawkeye-infisical.env", err.getvalue())
            os.environ["HAWKEYE_INFISICAL_ENV"] = str(self.tmp / "named.env")
            self.assertEqual(fs.find_creds(), self.tmp / "named.env", "an explicit environment variable wins")
            self.assertEqual(fs.find_creds("/x/y"), Path("/x/y"), "--creds wins over everything")
        finally:
            os.environ.pop("HAWKEYE_INFISICAL_ENV", None)
            if old_home is not None:
                os.environ["HOME"] = old_home
            for k, v in old_env.items():
                if v is not None:
                    os.environ[k] = v

    def test_the_environment_defaults_to_prod(self):
        self.write_creds(INFISICAL_ENV="")
        self.run_cli("check")
        self.assertEqual(self.api.requests[1][2]["environment"], ["prod"])

    def test_host_env_writes_only_the_host_values_to_a_private_file(self):
        out = self.tmp / "host.env"
        code, text, err = self.run_cli("host-env", "--out", str(out))
        self.assertEqual(code, 0, err)
        values = fs.parse_env_text(out.read_text())
        self.assertIn("BACKUP_BUCKET", values)
        self.assertNotIn("SESSION_SECRET", values)
        self.assertEqual(stat.S_IMODE(out.stat().st_mode), 0o600)
        self.assertNotIn("value-of", text + err, "no value may be printed")

    def test_there_is_no_command_that_prints_a_value(self):
        with self.assertRaises(SystemExit):
            with redirect_stderr(StringIO()):
                fs.main(["--creds", str(self.creds), "get", "SITE_ADDRESS"])
        for cmd in (("check",), ("write", "--app-out", str(self.tmp / "a.env")), ("host-env", "--out", str(self.tmp / "h.env"))):
            _, out, err = self.run_cli(*cmd)
            for v in REQUIRED.values():
                self.assertNotIn(v, out + err)

    def test_dump_is_encrypted_by_age_and_never_printed(self):
        stub = self.tmp / "bin"
        stub.mkdir()
        saved = self.tmp / "plain.json"
        (stub / "age").write_text(f'#!/bin/sh\nwhile [ $# -gt 0 ]; do [ "$1" = -o ] && O="$2"; shift; done\ncat > "{saved}"; echo ENCRYPTED > "$O"\n')
        (stub / "age").chmod(0o755)
        out = self.tmp / "settings.json.age"
        old_path = os.environ["PATH"]
        os.environ["PATH"] = f"{stub}:{old_path}"
        try:
            code, text, err = self.run_cli("dump", "--encrypt-to", "age1abc", "--out", str(out))
        finally:
            os.environ["PATH"] = old_path
        self.assertEqual(code, 0, err)
        self.assertEqual(out.read_text(), "ENCRYPTED\n")
        self.assertEqual(stat.S_IMODE(out.stat().st_mode), 0o600)
        dumped = json.loads(saved.read_text())  # what age was given
        self.assertEqual(dumped["SESSION_SECRET"], REQUIRED["SESSION_SECRET"])
        self.assertIn("BACKUP_BUCKET", dumped)
        self.assertNotIn("value-of", text + err)

    def test_dump_fails_cleanly_without_age_or_with_a_bad_key(self):
        old_path = os.environ["PATH"]
        os.environ["PATH"] = str(self.tmp)  # no age here
        try:
            code, _, err = self.run_cli("dump", "--encrypt-to", "age1abc", "--out", str(self.tmp / "x.age"))
        finally:
            os.environ["PATH"] = old_path
        self.assertEqual(code, fs.USAGE)
        self.assertIn("age program is not installed", err)

    def test_check_reports_optional_values_that_are_not_set(self):
        code, out, _ = self.run_cli("check")
        self.assertEqual(code, 0)
        self.assertIn("ok:", out)
        self.assertIn("HEALTHCHECK_URL", out)

    def test_unlisted_names_are_noted_but_allowed(self):
        self.api.secrets["SOMETHING_NEW"] = "1"
        app = self.tmp / "app.env"
        code, _, err = self.run_cli("write", "--app-out", str(app))
        self.assertEqual(code, 0)
        self.assertIn("1 setting(s) in the secret manager are not listed", err)
        self.assertNotIn("SOMETHING_NEW", err, "only names the manifest knows are ever printed")
        self.assertIn("SOMETHING_NEW", fs.parse_env_text(app.read_text()))


class MigrationTests(Base):
    def old_files(self):
        app = self.tmp / "old.env"
        host = self.tmp / "backup.env"
        app.write_text("SITE_ADDRESS=hawkeye.test   # comment\nAPP_VERSION=abc123\n" + "".join(f"{k}={v}\n" for k, v in REQUIRED.items() if fs.load_manifest()[k][0] == "app" and k != "SITE_ADDRESS"))
        host.write_text("".join(f"{k}={v}\n" for k, v in REQUIRED.items() if fs.load_manifest()[k][0] == "host"))
        return app, host

    def test_prepare_import_merges_the_old_files(self):
        app, host = self.old_files()
        out = self.tmp / "import.env"
        code, text, err = self.run_cli("prepare-import", str(app), str(host), "--out", str(out))
        self.assertEqual(code, 0, err)
        merged = fs.parse_env_text(out.read_text())
        self.assertEqual(merged["SITE_ADDRESS"], "hawkeye.test")
        self.assertNotIn("APP_VERSION", merged)
        self.assertIn("BACKUP_BUCKET", merged)
        self.assertEqual(stat.S_IMODE(out.stat().st_mode), 0o600)
        self.assertNotIn("value-of", text)

    def test_prepare_import_refuses_conflicting_duplicates_and_warns_about_placeholders(self):
        app, host = self.old_files()
        host.write_text(host.read_text() + "SITE_ADDRESS=other.test\n")
        self.assertEqual(self.run_cli("prepare-import", str(app), str(host), "--out", str(self.tmp / "i.env"))[0], fs.INVALID)
        app.write_text(app.read_text().replace("value-of-session_secret", "CHANGE_ME"))
        host.write_text(host.read_text().replace("SITE_ADDRESS=other.test\n", ""))
        code, _, err = self.run_cli("prepare-import", str(app), str(host), "--out", str(self.tmp / "i.env"))
        self.assertEqual(code, 0)
        self.assertIn("placeholder", err)

    def test_compare_names_the_differences_without_showing_values(self):
        app, host = self.old_files()
        code, out, _ = self.run_cli("compare", str(app), str(host))
        self.assertEqual(code, 0)
        self.assertIn("identical", out)
        del self.api.secrets["SMTP_HOST"]
        self.api.secrets["SESSION_SECRET"] = "changed"
        self.api.secrets["STRIPE_SECRET_KEY"] = "only-remote"   # listed in the manifest, absent from the old files
        self.api.secrets["EXTRA"] = "x"                           # not listed anywhere: counted, not named
        code, out, err = self.run_cli("compare", str(app), str(host))
        self.assertEqual(code, fs.INVALID)
        self.assertIn("missing from the secret manager: SMTP_HOST", out)
        self.assertIn("different value in the secret manager: SESSION_SECRET", out)
        self.assertIn("only in the secret manager: STRIPE_SECRET_KEY", out)
        self.assertIn("1 setting(s) not listed in deploy/env.manifest are only in the secret manager", out)
        self.assertNotIn("EXTRA", out + err)
        self.assertNotIn("changed", out + err)
        self.assertNotIn("only-remote", out + err)


class SyncScriptTests(Base):
    """scripts/sync-secrets.sh around the fetch: what lands on the server, and what a failure leaves alone."""

    def run_sync(self, *args, creds=True):
        env = dict(os.environ, HAWKEYE_INFISICAL_ENV=str(self.creds if creds else self.tmp / "nope.env"),
                   HAWKEYE_APP_ENV_OUT=str(self.tmp / ".env.production"), HAWKEYE_HOST_ENV_OUT=str(self.tmp / "host.env"), HOME=str(self.tmp))
        return subprocess.run(["bash", str(HERE / "sync-secrets.sh"), *args], capture_output=True, text=True, env=env)

    @property
    def app(self):
        return self.tmp / ".env.production"

    def test_without_credentials_the_existing_file_stays_in_use(self):
        self.app.write_text("LEGACY=1\n")
        r = self.run_sync(creds=False)
        self.assertEqual(r.returncode, 0)
        self.assertIn("no secret manager configured", r.stdout)
        self.assertEqual(self.app.read_text(), "LEGACY=1\n")

    def test_a_refresh_replaces_both_files_keeps_the_previous_and_the_version(self):
        self.app.write_text("OLD=1\nAPP_VERSION=abc123def456\n")
        r = self.run_sync()
        self.assertEqual(r.returncode, 0, r.stderr)
        new = fs.parse_env_text(self.app.read_text())
        self.assertEqual(new["SITE_ADDRESS"], "hawkeye.test")
        self.assertEqual(new["APP_VERSION"], "abc123def456", "the deploy script's version must survive a refresh")
        self.assertNotIn("OLD", new)
        self.assertEqual((self.tmp / ".env.production.previous").read_text(), "OLD=1\nAPP_VERSION=abc123def456\n")
        self.assertIn("BACKUP_BUCKET", fs.parse_env_text((self.tmp / "host.env").read_text()))
        self.assertEqual(stat.S_IMODE(self.app.stat().st_mode), 0o600)
        self.assertEqual(sorted(p.name for p in self.tmp.iterdir() if p.name.endswith(".new")), [], "no half-written file may remain")

    def test_a_first_run_on_a_new_server_has_nothing_to_preserve(self):
        r = self.run_sync()
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertNotIn("APP_VERSION", fs.parse_env_text(self.app.read_text()))
        self.assertFalse((self.tmp / ".env.production.previous").exists())

    def test_unacceptable_secrets_leave_the_server_as_it_was(self):
        self.app.write_text("OLD=1\n")
        del self.api.secrets["SESSION_SECRET"]
        r = self.run_sync()
        self.assertEqual(r.returncode, fs.INVALID)
        self.assertIn("SESSION_SECRET is required", r.stderr)
        self.assertEqual(self.app.read_text(), "OLD=1\n")
        self.assertFalse((self.tmp / "host.env").exists())
        self.assertEqual(sorted(p.name for p in self.tmp.iterdir() if p.name.endswith(".new")), [])

    def test_rejected_credentials_stop_the_run(self):
        self.app.write_text("OLD=1\n")
        self.api.login_status = 401
        r = self.run_sync()
        self.assertEqual(r.returncode, fs.REJECTED)
        self.assertEqual(self.app.read_text(), "OLD=1\n")

    def test_an_unreachable_service_exits_10_and_changes_nothing(self):
        self.app.write_text("OLD=1\n")
        self.write_creds(INFISICAL_API_URL="http://127.0.0.1:9")
        r = self.run_sync()
        self.assertEqual(r.returncode, fs.UNREACHABLE)
        self.assertIn("WARNING", r.stderr)
        self.assertEqual(self.app.read_text(), "OLD=1\n")

    def test_overrides_pass_through_and_check_writes_nothing(self):
        r = self.run_sync("--override", "SITE_ADDRESS=staging.hawkeye.test", "--drop", "WWW_ADDRESS")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(fs.parse_env_text(self.app.read_text())["SITE_ADDRESS"], "staging.hawkeye.test")
        self.app.unlink()
        r = self.run_sync("--check")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertFalse(self.app.exists())
        self.assertIn("ok:", r.stdout)

    def test_the_written_file_is_what_deploy_sh_and_heartbeat_sh_can_read(self):
        # Both scripts pull SITE_ADDRESS out with sed: it must be written unquoted.
        self.run_sync()
        out = subprocess.run(["sed", "-n", r"s/^SITE_ADDRESS=\([^ #]*\).*/\1/p", str(self.app)], capture_output=True, text=True).stdout.strip()
        self.assertEqual(out, "hawkeye.test")


if __name__ == "__main__":
    unittest.main()
