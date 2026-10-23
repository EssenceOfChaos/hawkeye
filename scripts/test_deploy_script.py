#!/usr/bin/env python3
"""Tests for scripts/deploy.sh: how a release treats the settings, and what a failure puts back.

It runs the real script in a throwaway git repository, with stubs for docker and the smoke test and the fake Infisical from
test_fetch_secrets.py. Needs flock (Linux): the tests are skipped where it is missing (macOS); CI runs them.

    python3 -m unittest discover -s scripts -p 'test_*.py' -v
"""
import os
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path

from test_fetch_secrets import Base, REQUIRED, fs

HERE = Path(__file__).resolve().parent


@unittest.skipUnless(shutil.which("flock") and shutil.which("git"), "needs flock and git (Linux)")
class DeployScriptTests(Base):
    def setUp(self):
        super().setUp()
        t = self.tmp
        self.home = t / "home"
        self.home.mkdir()
        self.bin = t / "bin"
        self.bin.mkdir()
        self.log = t / "docker.log"
        # docker is a stub that records what it was asked to do.
        (self.bin / "docker").write_text(f'#!/bin/sh\necho "docker $*" >> "{self.log}"\nexit 0\n')
        (self.bin / "docker").chmod(0o755)
        # A bare "origin" and a working copy with two commits, holding the real deploy and settings scripts and stubs for the rest.
        origin, self.repo = t / "origin.git", t / "repo"
        self.git(t, "init", "-q", "--bare", "-b", "main", str(origin))
        self.git(t, "clone", "-q", str(origin), str(self.repo))
        for d in ("scripts", "deploy"):
            (self.repo / d).mkdir()
        for name in ("deploy.sh", "sync-secrets.sh", "fetch-secrets.py"):
            shutil.copy(HERE / name, self.repo / "scripts" / name)
        shutil.copy(HERE.parent / "deploy" / "env.manifest", self.repo / "deploy" / "env.manifest")
        (self.repo / "scripts/backup-offsite.sh").write_text("#!/bin/sh\necho backup-ran >> \"$HOME/events\"\n")
        # The smoke test fails while the settings contain SMOKE=fail.
        (self.repo / "scripts/smoke.sh").write_text('#!/bin/sh\ngrep -q "^SMOKE=fail" "$(dirname "$0")/../.env.production" && exit 1\nexit 0\n')
        for f in (self.repo / "scripts").glob("*.sh"):
            f.chmod(0o755)
        (self.repo / ".gitignore").write_text(".env.*\n.deploy.lock\n")
        (self.repo / "deploy/docker-compose.prod.yml").write_text("services: {}\n")
        (self.repo / "deploy/docker-compose.local-db.yml").write_text("services: {}\n")
        self.commit("one")
        (self.repo / "deploy/marker").write_text("two\n")
        self.commit("two")
        self.git(self.repo, "push", "-q", "origin", "main")
        self.sha1, self.sha2 = self.git(self.repo, "rev-parse", "HEAD~1").strip(), self.git(self.repo, "rev-parse", "HEAD").strip()
        self.git(self.repo, "reset", "-q", "--hard", self.sha1)  # the server is running the first commit
        self.env_file = self.repo / ".env.production"
        self.env_file.write_text("SITE_ADDRESS=hawkeye.test\nOLD=1\nAPP_VERSION=" + self.sha1[:12] + "\n")
        self.creds.rename(self.home / ".hawkeye-infisical.env")
        self.creds = self.home / ".hawkeye-infisical.env"

    def git(self, cwd, *args):
        env = dict(os.environ, GIT_AUTHOR_NAME="t", GIT_AUTHOR_EMAIL="t@t", GIT_COMMITTER_NAME="t", GIT_COMMITTER_EMAIL="t@t")
        return subprocess.run(["git", *args], cwd=cwd, env=env, capture_output=True, text=True, check=True).stdout

    def commit(self, msg):
        self.git(self.repo, "add", "-A")
        self.git(self.repo, "commit", "-q", "-m", msg)

    def deploy(self, *args, ssh_command=None, no_creds=False):
        env = dict(os.environ, HOME=str(self.home), PATH=f"{self.bin}:{os.environ['PATH']}", HEALTH_TRIES="1", HEALTH_WAIT="0")
        env.pop("SSH_ORIGINAL_COMMAND", None)
        if ssh_command:
            env["SSH_ORIGINAL_COMMAND"] = ssh_command
        if no_creds and self.creds.exists():
            self.creds.unlink()
        return subprocess.run(["bash", str(self.repo / "scripts/deploy.sh"), *args], cwd=self.repo, capture_output=True, text=True, env=env)

    def head(self):
        return self.git(self.repo, "rev-parse", "HEAD").strip()

    def settings(self):
        return fs.parse_env_text(self.env_file.read_text())

    def docker_calls(self):
        return self.log.read_text().splitlines() if self.log.exists() else []

    # ---- a normal release ----

    def test_without_credentials_the_existing_settings_are_used_and_only_the_version_changes(self):
        r = self.deploy(self.sha2, no_creds=True)
        self.assertEqual(r.returncode, 0, r.stderr + r.stdout)
        self.assertIn("no secret manager configured", r.stdout)
        self.assertEqual(self.head(), self.sha2)
        s = self.settings()
        self.assertEqual((s["OLD"], s["APP_VERSION"]), ("1", self.sha2[:12]))
        self.assertTrue(any("up -d --build" in c for c in self.docker_calls()))

    def test_with_credentials_the_release_runs_on_freshly_fetched_settings(self):
        r = self.deploy(self.sha2)
        self.assertEqual(r.returncode, 0, r.stderr + r.stdout)
        s = self.settings()
        self.assertNotIn("OLD", s, "the old file's own values must not linger")
        self.assertEqual(s["SESSION_SECRET"], REQUIRED["SESSION_SECRET"])
        self.assertEqual(s["APP_VERSION"], self.sha2[:12])
        self.assertEqual(s["APP_VERSION"], self.sha2[:12])
        self.assertIn("BACKUP_BUCKET", fs.parse_env_text((self.home / ".hawkeye-host.env").read_text()))
        self.assertEqual(os.stat(self.env_file).st_mode & 0o777, 0o600)

    def test_the_default_target_is_the_tip_of_origin(self):
        r = self.deploy()
        self.assertEqual(r.returncode, 0, r.stderr + r.stdout)
        self.assertEqual(self.head(), self.sha2)

    def test_an_unreachable_secret_manager_does_not_block_a_release(self):
        self.write_creds(INFISICAL_API_URL="http://127.0.0.1:9")
        r = self.deploy(self.sha2)
        self.assertEqual(r.returncode, 0, r.stderr + r.stdout)
        self.assertIn("unreachable", r.stderr)
        self.assertEqual(self.settings()["OLD"], "1", "the settings already on the server stay in use")
        self.assertEqual(self.head(), self.sha2)

    def test_unacceptable_settings_stop_the_release_and_the_old_version_stays_live(self):
        del self.api.secrets["SESSION_SECRET"]
        before = self.env_file.read_text()
        r = self.deploy(self.sha2)
        self.assertEqual(r.returncode, 1)
        self.assertIn("SESSION_SECRET is required", r.stderr)
        self.assertEqual(self.head(), self.sha1, "rolled back to the running code")
        self.assertEqual(self.settings()["OLD"], "1")
        self.assertEqual(fs.parse_env_text(before)["SITE_ADDRESS"], self.settings()["SITE_ADDRESS"])

    def test_rejected_credentials_stop_the_release(self):
        self.api.login_status = 401
        r = self.deploy(self.sha2)
        self.assertEqual((r.returncode, self.head()), (1, self.sha1))
        self.assertIn("rejected our credentials", r.stderr)

    def test_a_release_whose_new_settings_make_the_site_unhealthy_rolls_back_code_and_settings(self):
        self.api.secrets["SMOKE"] = "fail"
        r = self.deploy(self.sha2)
        self.assertEqual(r.returncode, 1)
        self.assertEqual(self.head(), self.sha1)
        s = self.settings()
        self.assertNotIn("SMOKE", s, "the settings that were live come back with the code that was live")
        self.assertEqual((s["OLD"], s["APP_VERSION"]), ("1", self.sha1[:12]))
        self.assertIn("rolled back", r.stderr)

    # ---- secrets-only ----

    def test_secrets_only_applies_changed_settings_without_touching_the_code(self):
        r = self.deploy("secrets-only")
        self.assertEqual(r.returncode, 0, r.stderr + r.stdout)
        self.assertEqual(self.head(), self.sha1)
        self.assertEqual(self.settings()["SESSION_SECRET"], REQUIRED["SESSION_SECRET"])
        self.assertEqual(self.settings()["APP_VERSION"], self.sha1[:12])
        self.assertTrue(any("up -d" in c and "--build" not in c for c in self.docker_calls()), "restart without a rebuild")

    def test_secrets_only_does_nothing_when_nothing_changed(self):
        self.assertEqual(self.deploy("secrets-only").returncode, 0)
        self.log.unlink()
        r = self.deploy("secrets-only")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("have not changed", r.stdout)
        self.assertEqual(self.docker_calls(), [])

    def test_secrets_only_can_be_asked_for_over_the_restricted_ssh_key(self):
        r = self.deploy(ssh_command="secrets-only")
        self.assertEqual(r.returncode, 0, r.stderr + r.stdout)
        self.assertIn("refreshing settings", r.stdout)

    def test_secrets_only_puts_the_previous_settings_back_if_the_site_goes_unhealthy(self):
        self.assertEqual(self.deploy("secrets-only").returncode, 0)
        good = self.env_file.read_text()
        self.api.secrets["SMOKE"] = "fail"
        r = self.deploy("secrets-only")
        self.assertEqual(r.returncode, 1)
        self.assertEqual(self.env_file.read_text(), good)
        self.assertIn("rolled back", r.stderr)

    def test_secrets_only_fails_loudly_when_it_cannot_refresh(self):
        before = self.env_file.read_text()
        self.write_creds(INFISICAL_API_URL="http://127.0.0.1:9")
        r = self.deploy("secrets-only")
        self.assertEqual(r.returncode, 1)
        self.assertIn("not refreshed", r.stderr)
        self.assertEqual(self.env_file.read_text(), before)

    # ---- input ----

    def test_it_refuses_anything_that_is_not_a_full_sha_or_secrets_only(self):
        for bad in ("main", "abc123", "secrets-only; rm -rf /", self.sha2[:39]):
            r = self.deploy(bad)
            self.assertEqual(r.returncode, 2, bad)
        self.assertEqual(self.deploy(ssh_command="rm -rf /").returncode, 2)

    def test_the_backup_runs_when_the_host_settings_exist(self):
        (self.home / ".hawkeye-host.env").write_text("BACKUP_BUCKET=x\n")
        r = self.deploy(self.sha2)
        self.assertEqual(r.returncode, 0, r.stderr + r.stdout)
        self.assertIn("backup-ran", (self.home / "events").read_text())

    def test_without_any_backup_settings_it_warns_but_deploys(self):
        r = self.deploy(self.sha2, no_creds=True)
        self.assertEqual(r.returncode, 0)
        self.assertIn("WITHOUT a backup", r.stderr)


if __name__ == "__main__":
    unittest.main()
