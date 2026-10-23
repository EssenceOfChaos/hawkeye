#!/usr/bin/env python3
"""Turn the secret manager (Infisical) into the files a HawkEye server reads, and check them before anything is replaced.

    scripts/fetch-secrets.py write --app-out .env.production --host-out ~/.hawkeye-host.env
    scripts/fetch-secrets.py check                      # fetch and validate, write nothing
    scripts/fetch-secrets.py host-env --out FILE        # only the host-only values, as a mode-600 .env file (for scripts on a laptop)
    scripts/fetch-secrets.py dump --encrypt-to AGE_PUBLIC_KEY --out FILE.age   # everything, as JSON encrypted with age (the nightly copy)
    scripts/fetch-secrets.py prepare-import FILE...     # one-time: merge the old .env files into one file to paste into Infisical
    scripts/fetch-secrets.py compare FILE...            # one-time: after importing, show which names differ from the old files (names only)

Settings come from a mode-600 file holding the machine identity that may READ the production environment, and nothing else. It is looked for in
this order: --creds, $HAWKEYE_INFISICAL_ENV, ~/.hawkeye-infisical.env (where a server keeps it), ~/.hawkeye-secrets/.hawkeye-infisical.env (where a
laptop keeps it; $HAWKEYE_SECRETS_DIR changes the folder):

    INFISICAL_CLIENT_ID=...
    INFISICAL_CLIENT_SECRET=...
    INFISICAL_PROJECT_ID=...
    INFISICAL_ENV=prod                          # the environment slug (default prod)
    INFISICAL_API_URL=https://app.infisical.com # EU cloud: https://eu.infisical.com (default: the US cloud)

deploy/env.manifest lists which names go to the app file (.env.production, read by the containers) and which to the host file (backups and
heartbeat scripts only), and which are required: nothing is written if a required value is missing. Values are never printed, only names.

Exit codes (scripts/sync-secrets.sh acts on them):  0 ok | 2 bad usage or settings | 10 the secret manager could not be reached
(deploys may carry on with the last files) | 11 it rejected our credentials | 12 project or environment not found | 13 the values are not
acceptable (a required one is missing, a placeholder, or cannot be written safely).

No value is ever written to the terminal: it goes to files (mode 600), or, for the nightly copy, straight into age. Only names and counts are printed.
Standard library only (plus the age program for `dump`), so it runs on a fresh Ubuntu server and a laptop without installing anything.
"""
import argparse
import json
import os
import re
import socket
import stat
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
from datetime import datetime, timezone
from pathlib import Path

HERE = Path(__file__).resolve().parent
MANIFEST = HERE.parent / "deploy" / "env.manifest"
DEFAULT_API = "https://app.infisical.com"
NAME_RE = re.compile(r"^[A-Za-z_][A-Za-z0-9_]*$")
# What can be written without quotes and still read identically by docker compose and by bash. Anything else is single-quoted.
SAFE_RE = re.compile(r"^[A-Za-z0-9_@%+=:,./~-]*$")
PLACEHOLDERS = ("change_me", "changeme", "replace_me")  # compared in lower case
TIMEOUT = 15
BACKOFF = (1, 3)  # seconds to wait before the 2nd and 3rd attempt
ATTEMPTS = len(BACKOFF) + 1

OK, USAGE, UNREACHABLE, REJECTED, NOT_FOUND, INVALID = 0, 2, 10, 11, 12, 13


class Failure(Exception):
    """A problem to report to the operator. `code` is the exit status."""

    def __init__(self, code, message):
        super().__init__(message)
        self.code = code


# ---- reading .env style files ---------------------------------------------------------------------------------------------------------------

def parse_env_text(text):
    """NAME=VALUE lines as docker compose reads them: '#' starts a comment (after a space, for unquoted values), quotes are removed."""
    values = {}
    for raw in text.splitlines():
        line = raw.strip()
        if not line or line.startswith("#"):
            continue
        if line.startswith("export "):
            line = line[len("export "):].lstrip()
        name, sep, rest = line.partition("=")
        name = name.strip()
        if not sep or not NAME_RE.match(name):
            continue
        rest = rest.strip()
        if rest[:1] in ("'", '"'):
            quote = rest[0]
            end = rest.find(quote, 1)
            value = rest[1:end] if end > 0 else rest[1:]
        else:
            m = re.search(r"\s#", rest)
            value = (rest[:m.start()] if m else rest).strip()
        values[name] = value
    return values


def read_env_file(path):
    try:
        return parse_env_text(Path(path).read_text(encoding="utf-8"))
    except OSError as e:
        raise Failure(USAGE, f"cannot read {path}: {e.strerror}")


def load_manifest(path=MANIFEST):
    """{name: (scope, required)} in file order."""
    entries = {}
    try:
        lines = Path(path).read_text(encoding="utf-8").splitlines()
    except OSError as e:
        raise Failure(USAGE, f"cannot read {path}: {e.strerror}")
    for n, raw in enumerate(lines, 1):
        line = raw.split("#", 1)[0].strip()
        if not line:
            continue
        parts = line.split()
        if len(parts) != 3 or not NAME_RE.match(parts[0]) or parts[1] not in ("app", "host") or parts[2] not in ("required", "optional"):
            raise Failure(USAGE, f"{path}:{n}: expected 'NAME app|host required|optional', got {raw.strip()!r}")
        if parts[0] in entries:
            raise Failure(USAGE, f"{path}:{n}: {parts[0]} is listed twice")
        entries[parts[0]] = (parts[1], parts[2] == "required")
    return entries


# ---- settings --------------------------------------------------------------------------------------------------------------------------------

class Settings:
    def __init__(self, client_id, client_secret, project_id, environment, api_url):
        self.client_id, self.client_secret, self.project_id = client_id, client_secret, project_id
        self.environment, self.api_url = environment, api_url.rstrip("/")


def find_creds(explicit=None):
    """The settings file to use: the one named, else the first that exists of the usual places (see the module help)."""
    if explicit:
        return Path(explicit).expanduser()
    secrets_dir = Path(os.environ.get("HAWKEYE_SECRETS_DIR", "~/.hawkeye-secrets")).expanduser()
    candidates = [Path("~/.hawkeye-infisical.env").expanduser(), secrets_dir / ".hawkeye-infisical.env"]
    named = os.environ.get("HAWKEYE_INFISICAL_ENV")
    if named:
        return Path(named).expanduser()
    return next((c for c in candidates if c.exists()), candidates[0])


def load_settings(path):
    path = find_creds(path)
    if not path.exists():
        raise Failure(USAGE, f"{path} not found: this machine has no credentials for the secret manager (docs/secrets.md). "
                             "On a laptop the file belongs in ~/.hawkeye-secrets/.hawkeye-infisical.env")
    mode = stat.S_IMODE(path.stat().st_mode)
    if mode & 0o077:
        raise Failure(USAGE, f"{path} is readable by other users (mode {mode:o}); run: chmod 600 {path}")
    env = read_env_file(path)
    missing = [k for k in ("INFISICAL_CLIENT_ID", "INFISICAL_CLIENT_SECRET", "INFISICAL_PROJECT_ID") if not env.get(k)]
    if missing:
        raise Failure(USAGE, f"{path} is missing {', '.join(missing)}")
    api = env.get("INFISICAL_API_URL") or DEFAULT_API
    if not (api.startswith("https://") or re.match(r"^http://(127\.0\.0\.1|localhost)(:\d+)?(/|$)", api)):
        raise Failure(USAGE, "INFISICAL_API_URL must start with https:// (plain http is accepted only for localhost)")
    return Settings(env["INFISICAL_CLIENT_ID"], env["INFISICAL_CLIENT_SECRET"], env["INFISICAL_PROJECT_ID"], env.get("INFISICAL_ENV") or "prod", api)


# ---- the Infisical API -----------------------------------------------------------------------------------------------------------------------

def _request(req, what):
    """One HTTP call. Network trouble and 429/5xx are retried, then reported as UNREACHABLE; other errors are final."""
    last = None
    for attempt in range(ATTEMPTS):
        if attempt:
            time.sleep(BACKOFF[attempt - 1])
        try:
            with urllib.request.urlopen(req, timeout=TIMEOUT) as resp:
                return json.loads(resp.read().decode("utf-8"))
        except urllib.error.HTTPError as e:
            body = e.read().decode("utf-8", "replace")
            detail = _message(body)
            if e.code in (401, 403):
                raise Failure(REJECTED, f"{what}: the secret manager rejected our credentials (HTTP {e.code}{detail}); "
                                        "check the client id and secret, and that the identity can read this project and environment")
            if e.code == 404:
                raise Failure(NOT_FOUND, f"{what}: not found (HTTP 404{detail}); check INFISICAL_PROJECT_ID, INFISICAL_ENV and INFISICAL_API_URL")
            if e.code == 429 or e.code >= 500:
                last = f"HTTP {e.code}{detail}"
                continue
            raise Failure(REJECTED, f"{what}: HTTP {e.code}{detail}")
        except (urllib.error.URLError, socket.timeout, TimeoutError, ConnectionError) as e:
            last = str(getattr(e, "reason", e))
        except (ValueError, UnicodeDecodeError):
            last = "the answer was not JSON"
    raise Failure(UNREACHABLE, f"{what}: could not reach the secret manager ({last}) after {ATTEMPTS} attempts")


def _message(body):
    try:
        msg = json.loads(body).get("message") or ""
    except (ValueError, AttributeError):
        msg = ""
    msg = str(msg).replace("\n", " ")[:160]
    return f": {msg}" if msg else ""


def fetch_secrets(s):
    """{name: value} for the environment, from the root folder only."""
    token = _login(s)
    headers = {"Authorization": f"Bearer {token}", "Accept": "application/json"}
    current = urllib.parse.urlencode({"projectId": s.project_id, "environment": s.environment, "secretPath": "/", "recursive": "false",
                                      "includeImports": "false", "expandSecretReferences": "true", "viewSecretValue": "true"})
    try:
        body = _request(urllib.request.Request(f"{s.api_url}/api/v4/secrets?{current}", headers=headers), "reading secrets")
    except Failure as e:
        if e.code != NOT_FOUND:
            raise
        # An older Infisical (a self-hosted one) has the same list under another path and name for the project.
        older = urllib.parse.urlencode({"workspaceId": s.project_id, "environment": s.environment, "secretPath": "/", "recursive": "false",
                                        "include_imports": "false", "expandSecretReferences": "true"})
        body = _request(urllib.request.Request(f"{s.api_url}/api/v3/secrets/raw?{older}", headers=headers), "reading secrets")
    secrets = body.get("secrets")
    if not isinstance(secrets, list):
        raise Failure(UNREACHABLE, "reading secrets: unexpected answer from the secret manager")
    out = {}
    for item in secrets:
        name, value = item.get("secretKey"), item.get("secretValue")
        if item.get("secretValueHidden"):
            raise Failure(REJECTED, f"the identity may list {name} but not read its value: give it read access to secret values")
        if isinstance(name, str) and isinstance(value, str):
            out[name] = value
    return out


def _login(s):
    """An access token. The documented request is form-encoded; a server that insists on JSON is asked again that way."""
    fields = {"clientId": s.client_id, "clientSecret": s.client_secret}
    encodings = (("application/x-www-form-urlencoded", urllib.parse.urlencode(fields).encode()), ("application/json", json.dumps(fields).encode()))
    failure = None
    for content_type, data in encodings:
        req = urllib.request.Request(f"{s.api_url}/api/v1/auth/universal-auth/login", data=data, method="POST",
                                     headers={"Content-Type": content_type, "Accept": "application/json"})
        try:
            token = _request(req, "login").get("accessToken")
        except Failure as e:
            if e.code == REJECTED and re.search(r"HTTP (400|415)\b", str(e)):
                failure = e
                continue
            raise
        if not token:
            raise Failure(REJECTED, "login: the secret manager answered without an access token")
        return token
    raise failure


# ---- validating and writing -----------------------------------------------------------------------------------------------------------------

def split(values, manifest, overrides=None, drops=()):
    """(app, host, warnings): route names by the manifest, applying --override and --drop to the app values."""
    app, host, unknown = {}, {}, []
    for name, value in values.items():
        scope = manifest.get(name, ("app", False))[0]
        (host if scope == "host" else app)[name] = value
        if name not in manifest:
            unknown.append(name)
    for name in drops:
        app.pop(name, None)
    app.update(overrides or {})
    warnings = []
    if unknown:
        # Counted, not named: only names from the manifest are ever printed. The unlisted ones are visible in the secret manager itself.
        warnings.append(f"{len(unknown)} setting(s) in the secret manager are not listed in deploy/env.manifest; they went to .env.production as "
                        "optional. Add them to the manifest (and deploy/.env.production.example) so they are checked")
    return app, host, warnings


def validate(app, host, manifest):
    problems = []
    for name, (scope, required) in manifest.items():
        value = (app if scope == "app" else host).get(name)
        if required and not value:
            problems.append(f"{name} is required ({'.env.production' if scope == 'app' else 'host file'}) but is missing or empty")
    for group in (app, host):
        for name, value in group.items():
            if not NAME_RE.match(name):
                problems.append(f"{name!r} is not a valid variable name")
            elif any(p in value.lower() for p in PLACEHOLDERS):
                problems.append(f"{name} still holds a placeholder value")
            elif any(c in value for c in "\n\r\0"):
                problems.append(f"{name} contains a line break or NUL, which a .env file cannot hold")
            elif not SAFE_RE.match(value) and "'" in value:
                problems.append(f"{name} contains a single quote together with other special characters, which cannot be written safely: choose a value without a single quote")
    return problems


def quote(value):
    return value if SAFE_RE.match(value) else f"'{value}'"


def render(values, title):
    stamp = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
    head = f"# {title}\n# Generated by scripts/fetch-secrets.py from the secret manager at {stamp}. Do not edit: the next deploy overwrites it.\n"
    return head + "".join(f"{name}={quote(value)}\n" for name, value in sorted(values.items()))


def write_private(path, text):
    """Write atomically with mode 600: the old file is replaced only when the new one is complete."""
    path = Path(path).expanduser()
    fd, tmp = tempfile.mkstemp(dir=path.parent, prefix=path.name + ".", suffix=".tmp")
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as f:
            f.write(text)
            f.flush()
            os.fsync(f.fileno())
        os.chmod(tmp, 0o600)
        os.replace(tmp, path)
    except BaseException:
        try:
            os.unlink(tmp)
        except OSError:
            pass
        raise


# ---- commands --------------------------------------------------------------------------------------------------------------------------------

def parse_pairs(items):
    out = {}
    for item in items or []:
        name, sep, value = item.partition("=")
        if not sep or not NAME_RE.match(name):
            raise Failure(USAGE, f"--override expects NAME=VALUE, got {item!r}")
        out[name] = value
    return out


def prepared(args):
    manifest = load_manifest()
    values = fetch_secrets(load_settings(args.creds))
    app, host, warnings = split(values, manifest, parse_pairs(getattr(args, "override", None)), getattr(args, "drop", None) or ())
    problems = validate(app, host, manifest)
    if problems:
        raise Failure(INVALID, "the secrets are not acceptable:\n  - " + "\n  - ".join(problems))
    return app, host, warnings, values


def cmd_write(args):
    if not args.app_out and not args.host_out:
        raise Failure(USAGE, "write needs --app-out and/or --host-out")
    app, host, warnings, _ = prepared(args)
    # Both are written only after both validated, and each replace is atomic.
    if args.app_out:
        write_private(args.app_out, render(app, "HawkEye production settings (read by the containers)"))
    if args.host_out:
        write_private(args.host_out, render(host, "HawkEye host-only settings (read by scripts on the server, never by the containers)"))
    for w in warnings:
        print(f"note: {w}", file=sys.stderr)
    print(f"secrets written: {len(app)} app values, {len(host)} host values")


def cmd_check(args):
    app, host, warnings, _ = prepared(args)
    for w in warnings:
        print(f"note: {w}", file=sys.stderr)
    manifest = load_manifest()
    absent = sorted(n for n, (scope, req) in manifest.items() if not req and n not in (app if scope == "app" else host))
    print(f"ok: {len(app)} app values and {len(host)} host values, all required ones present")
    if absent:
        print("optional and not set: " + ", ".join(absent))


def cmd_host_env(args):
    _, host, _, _ = prepared(args)
    write_private(args.out, render(host, "HawkEye host-only settings"))
    print(f"host-only settings written: {len(host)} values")


def cmd_dump(args):
    """Every setting, as JSON, encrypted with age straight into a file: the plaintext exists only in the pipe to age."""
    values = fetch_secrets(load_settings(args.creds))
    payload = (json.dumps(values, indent=1, sort_keys=True) + "\n").encode("utf-8")
    out = Path(args.out).expanduser()
    try:
        subprocess.run(["age", "-r", args.encrypt_to, "-o", str(out)], input=payload, check=True)
    except FileNotFoundError:
        raise Failure(USAGE, "the age program is not installed (sudo apt install age)")
    except subprocess.CalledProcessError:
        out.unlink(missing_ok=True)
        raise Failure(USAGE, "age could not encrypt the settings (is --encrypt-to a public key starting with age1?)")
    os.chmod(out, 0o600)
    print(f"encrypted copy of {len(values)} settings written")


def legacy_values(paths):
    merged, source = {}, {}
    for p in paths:
        for name, value in read_env_file(p).items():
            if name in merged and merged[name] != value:
                raise Failure(INVALID, f"{name} has different values in {source[name]} and {p}: decide which is right and fix the old file first")
            merged[name], source[name] = value, p
    merged.pop("APP_VERSION", None)  # set by the deploy script for each release
    return merged


def cmd_prepare_import(args):
    values = legacy_values(args.files)
    manifest = load_manifest()
    app, host, _ = split(values, manifest)
    problems = validate(app, host, manifest)
    out = Path(args.out).expanduser()
    out.parent.mkdir(parents=True, exist_ok=True)
    # Plain NAME=VALUE with no quoting rules of ours: Infisical's own importer reads this.
    write_private(out, "".join(f"{n}={v}\n" for n, v in sorted(values.items())))
    print(f"wrote {out} with {len(values)} values ({len(app)} app, {len(host)} host); mode 600. Import it, then DELETE it.")
    if problems:
        print("fix these before relying on it:\n  - " + "\n  - ".join(problems), file=sys.stderr)


def cmd_compare(args):
    legacy = legacy_values(args.files)
    remote = fetch_secrets(load_settings(args.creds))
    manifest = load_manifest()
    # Names are printed only when the manifest knows them; anything else is counted.
    missing = sorted(n for n in manifest if n in legacy and n not in remote)
    different = sorted(n for n in manifest if n in legacy and n in remote and remote[n] != legacy[n])
    extra = sorted(n for n in manifest if n in remote and n not in legacy)
    unlisted_wrong = sum(1 for n in legacy if n not in manifest and remote.get(n) != legacy[n])
    unlisted_extra = sum(1 for n in remote if n not in manifest and n not in legacy)
    print(f"{len(legacy)} values in the old files, {len(remote)} in the secret manager")
    for label, names in (("missing from the secret manager", missing), ("different value in the secret manager", different), ("only in the secret manager", extra)):
        if names:
            print(f"  {label}: {', '.join(names)}")
    if unlisted_wrong:
        print(f"  {unlisted_wrong} setting(s) not listed in deploy/env.manifest are missing or different in the secret manager")
    if unlisted_extra:
        print(f"  {unlisted_extra} setting(s) not listed in deploy/env.manifest are only in the secret manager")
    if missing or different or unlisted_wrong:
        raise Failure(INVALID, "the secret manager does not match the old files yet")
    print("identical: every old value is in the secret manager unchanged")


def main(argv=None):
    p = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    p.add_argument("--creds", default=None, help="settings file (default: $HAWKEYE_INFISICAL_ENV, ~/.hawkeye-infisical.env, then ~/.hawkeye-secrets/.hawkeye-infisical.env)")
    sub = p.add_subparsers(dest="cmd", required=True)
    w = sub.add_parser("write", help="fetch, validate, then write the files")
    w.add_argument("--app-out", help="where to write the app settings (.env.production)")
    w.add_argument("--host-out", help="where to write the host-only settings")
    w.add_argument("--override", action="append", metavar="NAME=VALUE", help="replace an app value (a staging copy uses its own address)")
    w.add_argument("--drop", action="append", metavar="NAME", help="leave an app value out")
    w.set_defaults(run=cmd_write)
    sub.add_parser("check", help="fetch and validate; write nothing").set_defaults(run=cmd_check)
    h = sub.add_parser("host-env", help="write the host-only values to a file")
    h.add_argument("--out", required=True)
    h.set_defaults(run=cmd_host_env)
    d = sub.add_parser("dump", help="write every value as JSON, encrypted with age")
    d.add_argument("--encrypt-to", required=True, metavar="AGE_PUBLIC_KEY")
    d.add_argument("--out", required=True)
    d.set_defaults(run=cmd_dump)
    i = sub.add_parser("prepare-import", help="merge old .env files into one file to import")
    i.add_argument("files", nargs="+")
    i.add_argument("--out", default="~/.hawkeye-secrets/infisical-import.env")
    i.set_defaults(run=cmd_prepare_import)
    c = sub.add_parser("compare", help="compare the secret manager with old .env files (names only)")
    c.add_argument("files", nargs="+")
    c.set_defaults(run=cmd_compare)
    args = p.parse_args(argv)
    try:
        args.run(args)
    except Failure as e:
        print(f"fetch-secrets: {e}", file=sys.stderr)
        return e.code
    return OK


if __name__ == "__main__":
    sys.exit(main())
