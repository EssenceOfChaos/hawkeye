# HawkEye deploy action

Quiet [HawkEye](https://hawkeye.observer)'s alerts while you deploy, then check your sites and fail the job if one is down.

```yaml
- uses: EssenceOfChaos/hawkeye/actions/deploy@deploy-action-v1
  with:
    key: ${{ secrets.HAWKEYE_DEPLOY_KEY }}
    step: start
    tag: production        # leave out for every monitor

- run: ./deploy.sh

- uses: EssenceOfChaos/hawkeye/actions/deploy@deploy-action-v1
  with:
    key: ${{ secrets.HAWKEYE_DEPLOY_KEY }}
    step: finish           # ends the quiet period and checks; fails the job if a monitor is down
```

Create the key in HawkEye under **API keys**, with **Deploys only** access (the organization's owner can), and store it as the
`HAWKEYE_DEPLOY_KEY` secret. A deploy key can only start and finish deploys.

| Input | Default | |
|---|---|---|
| `key` | | a deploy key (required) |
| `step` | | `start`, `finish`, or `check` (a check on its own, on every plan) |
| `tag` | every monitor | only the monitors with this tag (`start` and `check`) |
| `minutes` | `15` | alerts come back on by themselves after this long if `finish` never runs (1 to 120) |
| `note` | the commit SHA | shown with the deploy in HawkEye |
| `verify` | `true` | on `finish`, `false` only ends the quiet period |
| `wait-seconds` | `60` | how long to keep retrying a failing check, every 10 seconds |

Guide: https://hawkeye.observer/docs/deploys. The same two calls work from any CI with `curl`; the guide shows how.

`hawkeye-deploy.sh` is the whole action, and runs on its own with the `HAWKEYE_*` variables it lists.
