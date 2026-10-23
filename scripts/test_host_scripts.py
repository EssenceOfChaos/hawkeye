#!/usr/bin/env python3
"""Tests for the scripts that read ~/.hawkeye-host.env: scripts/heartbeat.sh and scripts/backup-offsite.sh (the part that copies the settings).

curl, docker, age and rclone are stubs that record what they were asked to do.

    python3 -m unittest discover -s scripts -p 'test_*.py' -v
"""
import json
import os
import shutil
import stat
import subprocess
import tempfile
import unittest
from pathlib import Path

from test_fetch_secrets import Base, REQUIRED

HERE = Path(__file__).resolve().parent


class HostScriptBase(Base):
    def setUp(self):
        super().setUp()
        self.home = self.tmp / "home"
        self.home.mkdir()
        self.bin = self.tmp / "bin"
        self.bin.mkdir()
        self.calls = self.tmp / "calls.log"

    def stub(self, name, body):
        p = self.bin / name
        p.write_text("#!/bin/sh\n" + body)
        p.chmod(0o755)

    def env(self, **extra):
        e = {k: v for k, v in os.environ.items() if not k.startswith(("HAWKEYE_", "HEARTBEAT", "BACKUP", "AGE_", "HEALTHCHECK"))}
        e.update(HOME=str(self.home), PATH=f"{self.bin}:{os.environ['PATH']}")
        e.update(extra)
        return e

    def calls_text(self):
        return self.calls.read_text() if self.calls.exists() else ""


class HeartbeatTests(HostScriptBase):
    def setUp(self):
        super().setUp()
        self.stub("curl", f'echo "curl $*" >> "{self.calls}"\nexit 0\n')
        self.repo = self.tmp / "repo"
        (self.repo / "scripts").mkdir(parents=True)
        shutil.copy(HERE / "heartbeat.sh", self.repo / "scripts/heartbeat.sh")
        (self.repo / ".env.production").write_text("SITE_ADDRESS=hawkeye.test\n")

    def run_it(self):
        return subprocess.run(["bash", str(self.repo / "scripts/heartbeat.sh")], capture_output=True, text=True, env=self.env())

    def test_it_does_nothing_until_configured(self):
        self.assertEqual(self.run_it().returncode, 0)
        self.assertEqual(self.calls_text(), "")

    def test_the_host_file_from_the_secret_manager_is_enough(self):
        (self.home / ".hawkeye-host.env").write_text("BACKUP_BUCKET=b\nHEALTHCHECK_URL=https://hc-ping.com/abc\n")
        r = self.run_it()
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("https://hawkeye.test/readyz", self.calls_text())
        self.assertIn("curl -fsS -m 10 --retry 3 -o /dev/null https://hc-ping.com/abc", self.calls_text())

    def test_a_host_file_without_a_url_is_quiet_because_the_check_is_optional(self):
        (self.home / ".hawkeye-host.env").write_text("BACKUP_BUCKET=b\n")
        r = self.run_it()
        self.assertEqual((r.returncode, r.stderr, self.calls_text()), (0, "", ""))

    def test_the_older_heartbeat_file_still_works(self):
        (self.home / ".hawkeye-heartbeat.env").write_text("HEALTHCHECK_URL=https://hc-ping.com/old\n")
        self.assertEqual(self.run_it().returncode, 0)
        self.assertIn("https://hc-ping.com/old", self.calls_text())

    def test_the_older_file_without_a_url_is_still_an_error(self):
        (self.home / ".hawkeye-heartbeat.env").write_text("# empty\n")
        r = self.run_it()
        self.assertEqual(r.returncode, 1)
        self.assertIn("HEALTHCHECK_URL is not set", r.stderr)

    def test_the_secret_managers_value_wins_over_the_older_file(self):
        (self.home / ".hawkeye-heartbeat.env").write_text("HEALTHCHECK_URL=https://hc-ping.com/old\n")
        (self.home / ".hawkeye-host.env").write_text("HEALTHCHECK_URL=https://hc-ping.com/new\n")
        self.run_it()
        self.assertIn("https://hc-ping.com/new", self.calls_text())
        self.assertNotIn("hc-ping.com/old", self.calls_text())


class BackupSettingsCopyTests(HostScriptBase):
    """The encrypted copy of the settings that backup-offsite.sh uploads beside the database dump."""

    def setUp(self):
        super().setUp()
        self.out = self.tmp / "backups"
        self.out.mkdir()
        self.repo = self.tmp / "repo"
        (self.repo / "scripts").mkdir(parents=True)
        (self.repo / "deploy").mkdir()
        for name in ("backup-offsite.sh", "fetch-secrets.py"):
            shutil.copy(HERE / name, self.repo / "scripts" / name)
        shutil.copy(HERE.parent / "deploy/env.manifest", self.repo / "deploy/env.manifest")
        # backup.sh (the dump) is a stub that makes a dump file and its checksum.
        (self.repo / "scripts/backup.sh").write_text(
            f'#!/bin/sh\nwhile [ $# -gt 0 ]; do [ "$1" = --out ] && OUT="$2"; shift; done\n'
            f'echo dump > "$OUT/hawkeye-20260101T000000Z.dump.age"; echo sum > "$OUT/hawkeye-20260101T000000Z.dump.age.sha256"\n')
        (self.repo / "scripts/backup.sh").chmod(0o755)
        self.stub("docker", "exit 0\n")
        # age: with -o FILE it writes stdin there, marked so the test can tell it was "encrypted"; it also records who it was for.
        self.stub("age", f'echo "age $*" >> "{self.calls}"\nwhile [ $# -gt 0 ]; do [ "$1" = -o ] && O="$2"; shift; done\n{{ echo AGE-ENCRYPTED; cat; }} > "$O"\n')
        # rclone: records its arguments; `size` answers with the real size of the file in the staging directory.
        self.stub("rclone", f'echo "rclone $*" >> "{self.calls}"\n'
                            f'if [ "$1" = size ]; then n=$(wc -c < "{self.out}/$(basename "$2")" | tr -d " "); echo "{{\\"count\\":1,\\"bytes\\":$n}}"; fi\nexit 0\n')
        (self.home / ".hawkeye-host.env").write_text(
            "AGE_RECIPIENT=age1recipient\nBACKUP_ENDPOINT=ewr1.example\nBACKUP_BUCKET=bucket\nBACKUP_ACCESS_KEY=ak\nBACKUP_SECRET_KEY=sk\n")

    def run_it(self, **env):
        return subprocess.run(["bash", str(self.repo / "scripts/backup-offsite.sh")], capture_output=True, text=True,
                              env=self.env(BACKUP_DIR=str(self.out), **env))

    def test_the_settings_are_encrypted_uploaded_and_not_kept_on_the_server(self):
        r = self.run_it(HAWKEYE_INFISICAL_ENV=str(self.creds))
        self.assertEqual(r.returncode, 0, r.stderr + r.stdout)
        calls = self.calls_text()
        self.assertIn("age -r age1recipient -o", calls)
        self.assertRegex(calls, r"rclone copy \S+hawkeye-secrets-\d{8}T\d{6}Z\.json\.age offsite:bucket/secrets/ --checksum")
        self.assertIn("rclone delete offsite:bucket/secrets/ --min-age 30d --include hawkeye-secrets-*", calls)
        self.assertIn("offsite settings copy ok", r.stdout)
        self.assertEqual(list(self.out.glob("hawkeye-secrets-*")), [], "the settings must not stay on this server")

    def test_what_gets_encrypted_is_the_whole_set_as_json(self):
        saved = self.tmp / "captured"
        self.stub("age", f'while [ $# -gt 0 ]; do [ "$1" = -o ] && O="$2"; shift; done\ncat > "{saved}"; echo x > "$O"\n')
        self.assertEqual(self.run_it(HAWKEYE_INFISICAL_ENV=str(self.creds)).returncode, 0)
        data = json.loads(saved.read_text())
        self.assertEqual(data["SESSION_SECRET"], REQUIRED["SESSION_SECRET"])
        self.assertIn("BACKUP_SECRET_KEY", data)

    def test_no_copy_is_made_without_credentials_for_the_secret_manager(self):
        r = self.run_it(HAWKEYE_INFISICAL_ENV=str(self.tmp / "missing.env"))
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertNotIn("secrets/", self.calls_text())
        self.assertIn("offsite backup ok", r.stdout)

    def test_a_failed_copy_is_reported_but_the_database_backup_still_succeeds(self):
        self.write_creds(INFISICAL_API_URL="http://127.0.0.1:9")
        r = self.run_it(HAWKEYE_INFISICAL_ENV=str(self.creds))
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("offsite backup ok", r.stdout)
        self.assertIn("WARNING: the encrypted copy of the settings was not made", r.stderr)
        self.assertNotIn("secrets/", self.calls_text())
        self.assertEqual(list(self.out.glob("hawkeye-secrets-*")), [])

    def test_the_host_file_supplies_the_backup_settings(self):
        # No older ~/.hawkeye-backup.env exists in this test: the settings come from the secret manager's file alone.
        r = self.run_it(HAWKEYE_INFISICAL_ENV=str(self.tmp / "missing.env"))
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("offsite:bucket/db/", self.calls_text())


if __name__ == "__main__":
    unittest.main()


class RebuildCredentialsTests(HostScriptBase):
    """scripts/rebuild.sh credentials: giving an existing server the one file it needs (ssh and scp are stubs)."""

    def setUp(self):
        super().setUp()
        self.repo = self.tmp / "repo"
        for d in ("scripts", "deploy", "infra"):
            (self.repo / d).mkdir(parents=True)
        shutil.copy(HERE / "rebuild.sh", self.repo / "scripts/rebuild.sh")
        shutil.copy(HERE / "fetch-secrets.py", self.repo / "scripts/fetch-secrets.py")
        shutil.copy(HERE.parent / "deploy/env.manifest", self.repo / "deploy/env.manifest")
        self.stub("scp", f'echo "scp $*" >> "{self.calls}"\nexit 0\n')
        self.stub("ssh", f'echo "ssh $*" >> "{self.calls}"\nexit 0\n')
        self.secrets = self.tmp / "secrets"
        self.secrets.mkdir()
        self.creds.rename(self.secrets / ".hawkeye-infisical.env")
        self.creds = self.secrets / ".hawkeye-infisical.env"

    def run_it(self, *args):
        return subprocess.run(["bash", str(self.repo / "scripts/rebuild.sh"), *args], capture_output=True, text=True,
                              env=self.env(HAWKEYE_SECRETS_DIR=str(self.secrets)))

    def test_the_credentials_go_to_the_server_after_they_are_proven_to_work_here(self):
        r = self.run_it("credentials", "--host", "203.0.113.7")
        self.assertEqual(r.returncode, 0, r.stderr + r.stdout)
        calls = self.calls_text()
        self.assertIn(f"{self.creds} deploy@203.0.113.7:.hawkeye-infisical.env", calls)
        self.assertIn("chmod 600 ~/.hawkeye-infisical.env", calls)
        self.assertIn("scripts/sync-secrets.sh --check", calls)
        self.assertNotIn("hawkeye.test", r.stdout + r.stderr, "settings must never be printed")

    def test_credentials_that_do_not_work_are_not_copied_anywhere(self):
        self.api.login_status = 401
        r = self.run_it("credentials", "--host", "203.0.113.7")
        self.assertNotEqual(r.returncode, 0)
        self.assertIn("do not work from here", r.stderr)
        self.assertEqual(self.calls_text(), "")

    def test_it_needs_a_host_and_a_credentials_file(self):
        self.assertIn("which server", self.run_it("credentials").stderr)
        self.creds.unlink()
        self.assertIn("no credentials file", self.run_it("credentials", "--host", "203.0.113.7").stderr)

    def test_dry_run_copies_nothing(self):
        r = self.run_it("credentials", "--host", "203.0.113.7", "--dry-run")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("[dry-run]", r.stdout)
        self.assertEqual(self.calls_text(), "")
