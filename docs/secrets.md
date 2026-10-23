# Secrets and settings

Every setting a server needs (database URL, session secret, Stripe and SMTP keys, the encryption key, the backup bucket, the heartbeat URL) lives in one place: the
**production environment of an Infisical project**. Nothing is copied by hand, and nothing secret is in git or in the infrastructure code.

```
 Infisical  (project "hawkeye", environment "prod")           you edit values here, in a web page
     │  read-only machine identity
     ▼
 ~/.hawkeye-infisical.env      the ONE file a server is given: who it is, not what it holds
     │  scripts/sync-secrets.sh  (every deploy; also at stage and cutover)
     ├─▶ .env.production          for the containers (the API, worker and web server read it)
     └─▶ ~/.hawkeye-host.env      for scripts on the host only: backups and the heartbeat. Never given to a container.
```

`deploy/env.manifest` lists every setting, whether it is `app` (to the containers) or `host`, and whether it is required. `scripts/fetch-secrets.py` refuses to write
anything if a required value is missing, empty or still a `CHANGE_ME` placeholder, so a server can never be started with an incomplete set. The files are replaced
atomically; a failed run leaves the server exactly as it was. `APP_VERSION` is not a secret: the deploy script sets it to the commit being released.

## Day to day

| I want to... | Do this |
|---|---|
| Change a value | Edit it in Infisical (prod). Then apply it: **GitHub → Actions → Deploy → Run workflow → tick "Only apply changed settings"**, or `ssh deploy@<ip> 'cd hawkeye && scripts/deploy.sh secrets-only'`, or just wait for the next release. Containers restart only if something changed, and if the site is unhealthy afterwards the previous settings are put back. |
| Release new code | Push to `main`. The deploy refreshes the settings first, then builds and starts. Nothing else to do. |
| Add a new setting | Add it to Infisical, add a line to `deploy/env.manifest` and a documented line to `deploy/.env.production.example` (`scripts/test_fetch_secrets.py` fails if they differ). |
| Set up a new server | `scripts/rebuild.sh create`, `stage`, `cutover` (`docs/rebuild.md`). `create` gives the new server the credentials file; it fetches everything else itself. |
| Turn the heartbeat on | Add `HEALTHCHECK_URL=https://hc-ping.com/<uuid>` in Infisical. It reaches `~/.hawkeye-host.env` at the next deploy or settings refresh. |
| Rehearse a rebuild | `scripts/rebuild.sh create --workspace drill`, then `stage --workspace drill`, then `destroy --workspace drill`. The rehearsal fetches the same settings. |

## What happens when something is wrong

| Situation | What the deploy does |
|---|---|
| Infisical cannot be reached | Carries on with the settings already on the server and prints a warning (an outage at the secret manager never blocks a release). `secrets-only` fails instead, because it has nothing else to do. |
| Infisical rejects the credentials (rotated or revoked) | Stops, does not start the release, and says so. Fix the credentials file (below). |
| A required value is missing or a placeholder | Stops with the names that are wrong (never the values). The old settings and the running site are untouched. |
| New settings make the site unhealthy | Rolls back the code **and** the settings that were live. |
| A secret contains a single quote and other special characters | Stops with the name. It cannot be written to a `.env` file safely: generate a value without a `'` (they are rare in generated secrets). |

## One-time setup

Done once, by hand, because it creates the accounts and the first copy of the values. About half an hour. The steps follow Infisical's documentation; the labels in
their web page may differ a little.

1. **Account and project.** Sign up at <https://app.infisical.com> (the US cloud; for the EU cloud use <https://eu.infisical.com> and put it in `INFISICAL_API_URL` below).
   Create a project named `hawkeye` and note its **Project ID** (project settings). Use the environment with the slug `prod`.
2. **Check the free plan** at <https://infisical.com/pricing> before relying on it. It was described as including 5 identities, which covers you plus one machine
   identity. The settings are kept off the server too (see *Recovery*), so a pricing change is an inconvenience and not an emergency.
3. **Prepare the values.** On your laptop, from the repo:
   ```
   python3 scripts/fetch-secrets.py prepare-import ~/.hawkeye-secrets/.env.production ~/.hawkeye-secrets/.hawkeye-backup.env ~/.hawkeye-secrets/.hawkeye-heartbeat.env
   ```
   (leave out files you do not have). It merges them into `~/.hawkeye-secrets/infisical-import.env` (mode 600), drops `APP_VERSION`, refuses two files that disagree
   about a value, and lists any placeholder still in there. Fix those at the source first.
4. **Import** that file into the `prod` environment (Secrets overview → Add / Import → paste or upload a `.env` file), then **delete** it:
   `rm ~/.hawkeye-secrets/infisical-import.env`.
5. **Create the machine identity.** Organization → Access Control → Machine Identities → Create (give it no organization permissions). Under Universal Auth, create a
   **client secret** and note the **client ID**. Then open the project → Access Control → Machine Identities → add it with a role that can **read secrets and their
   values in `prod` and nothing else** (create a custom read-only role if there is no built-in one). It must not be able to edit.
6. **Write the credentials file** on your laptop, `~/.hawkeye-secrets/.hawkeye-infisical.env`, mode 600:
   ```
   INFISICAL_CLIENT_ID=...
   INFISICAL_CLIENT_SECRET=...
   INFISICAL_PROJECT_ID=...
   INFISICAL_ENV=prod
   # INFISICAL_API_URL=https://eu.infisical.com     (only for the EU cloud)
   ```
   `chmod 600` it; the scripts refuse a file other users can read. Keep a copy in your password manager. On a laptop `fetch-secrets.py` finds it in `~/.hawkeye-secrets/` by itself; on a server it is `~/.hawkeye-infisical.env`.
7. **Prove the import was faithful.** Names only, no values are printed:
   ```
   python3 scripts/fetch-secrets.py compare ~/.hawkeye-secrets/.env.production ~/.hawkeye-secrets/.hawkeye-backup.env ~/.hawkeye-secrets/.hawkeye-heartbeat.env
   ```
   It must end with `identical`. Then `scripts/rebuild.sh preflight` should say the secret manager is reachable and all required values are present.
8. **Rehearse on a throwaway server** (costs cents): `scripts/rebuild.sh create --workspace drill`, `stage --workspace drill`, then `destroy --workspace drill`. This is the
   test that a brand-new server gets everything with no manual copying.
9. **Switch the live server.** Push this release so the server has the new scripts, and let it deploy (it keeps using its existing `.env.production`). Then
   `scripts/rebuild.sh credentials --host <server ip>` gives it the credentials file and checks it can fetch. Then run **Deploy → Run workflow → "Only apply changed
   settings"**. The log should say `refreshed from the secret manager`.
10. **Clean up** once a normal release has gone through on the new path: on the server delete `~/.hawkeye-backup.env` and `~/.hawkeye-heartbeat.env` (the
    secret manager's `~/.hawkeye-host.env` supersedes them); on your laptop move the old files in `~/.hawkeye-secrets` into the password manager as an archive.

## Rotating things

- **The machine identity's client secret:** create a new one in Infisical, put it in `~/.hawkeye-secrets/.hawkeye-infisical.env`, run
  `scripts/rebuild.sh credentials --host <ip>` for each server, then delete the old client secret in Infisical.
- **A setting:** change it in Infisical and apply it (see *Day to day*).
- **`SECRETS_ENCRYPTION_KEY`:** never change it casually. Credentials customers saved under the old key cannot be opened with a new one
  (`docs/operations.md#stored-credentials-cannot-be-opened`). Keep it in the password manager as well.

## Recovery

- **A server is rebuilt while Infisical is unreachable:** nothing to fetch from. Restore the settings from the encrypted copy below, put them in
  `.env.production` and `~/.hawkeye-host.env` by hand, and carry on; or wait for Infisical.
- **Infisical is lost or closed:** every night `scripts/backup-offsite.sh` uploads an encrypted copy of *all* the settings to `secrets/` in the backup bucket (age, the
  same key as the database dumps; kept 30 days; never stored on the server). To read the newest:
  ```
  # the bucket's keys are in Vultr's console (Object Storage); the endpoint is e.g. ewr1.vultrobjects.com
  export RCLONE_CONFIG_OFFSITE_TYPE=s3 RCLONE_CONFIG_OFFSITE_PROVIDER=Other RCLONE_CONFIG_OFFSITE_ENDPOINT=https://<endpoint> \
         RCLONE_CONFIG_OFFSITE_ACCESS_KEY_ID=<key> RCLONE_CONFIG_OFFSITE_SECRET_ACCESS_KEY=<secret>
  rclone lsf offsite:<bucket>/secrets/ | sort | tail -1
  rclone cat offsite:<bucket>/secrets/<file> | age -d -i ~/hawkeye-backup-key.txt > settings.json
  ```
  `settings.json` holds every name and value. Import it into a new project (or write the files by hand), then delete it.
- **A server keeps working without Infisical.** The settings are files on its disk; Infisical is only asked at deploy time.

## What this adds to the trust you place in a third party

Infisical stores your production settings (Stripe live keys, the database password, `SECRETS_ENCRYPTION_KEY`, SMTP password). It holds no customer data, but a compromise of
your Infisical account would be a compromise of all of those, so protect the account with a strong password and two-factor authentication. The credentials file on each
server can only **read** `prod`; a stolen one cannot change anything, but it can read everything, so treat it like the settings themselves. Infisical is open source and can be
self-hosted later if you want the settings back under your own control; only `INFISICAL_API_URL` would change. The privacy policy lists sub-processors of customer data;
this is not one, but mention it to counsel with the other open items (`docs/legal-placeholders.md`).

## Tests

`python3 -m unittest discover -s scripts -p 'test_*.py' -v` (runs in CI). They cover the fetch and validation against a fake Infisical, that what is written reads back
identically in bash and in docker compose, the deploy script's behaviour in a throwaway git repository (including every rollback), the heartbeat and backup scripts, and the
`credentials` command. The tests that need `flock` run on Linux only. **They cannot prove the real Infisical API answers as the fake does**: the one-time rehearsal in
step 8 is that proof. If a call is rejected there, `scripts/fetch-secrets.py` prints the endpoint and status, and the two calls it makes (login, list secrets) are all that
would need adjusting.
