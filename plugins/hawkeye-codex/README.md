# HawkEye plugin for Codex

Set up [HawkEye](https://hawkeye.observer) monitoring from your codebase. Ask Codex to "set up monitoring for this project" and it:

1. reads the repository for your production domain, your cron jobs and scheduled tasks (crontab, GitHub Actions schedules, Kubernetes CronJobs,
   platform crons, in-app schedulers) and your health endpoints;
2. asks HawkEye to look at your live site and suggest monitors (home page, health endpoint, API subdomain, email records);
3. shows you one plan and waits for your approval;
4. creates the monitors, including a heartbeat for each job;
5. adds a ping to the end of each job, reading the private ping address from an environment variable or CI secret (never committed), and tells you
   which ones to set.

Nothing is created and no file is changed before you approve. Your code stays on your machine: HawkEye only receives the monitors you approve.

## Install

```bash
codex plugin marketplace add EssenceOfChaos/hawkeye
codex plugin add hawkeye@hawkeye
codex mcp login hawkeye
```

The last command opens HawkEye's sign-in page in your browser. Choose the organization and **Read and make changes** (read and write access can add
monitors on every plan, including Free), and confirm with your two-factor code if you use one. Then start a new Codex session. `codex mcp list` shows
whether you're signed in. You can disconnect Codex at any time from your HawkEye account.

Signing in uses port 47831 on your machine for the browser to hand the sign-in back to Codex, so it must be free while you sign in.

### With an API key instead

To connect without signing in (for example on a server), add HawkEye's MCP server to `~/.codex/config.toml` with a key from
https://hawkeye.observer/api-keys in an environment variable:

```toml
[mcp_servers.hawkeye]
url = "https://hawkeye.observer/mcp"
bearer_token_env_var = "HAWKEYE_API_KEY"
```

## What's included

- **MCP server:** HawkEye's tools: account overview, monitors, uptime, incidents, status pages, maintenance windows, monitor suggestions and
  creating monitors. What Codex can change depends on the access you chose and your plan; see https://hawkeye.observer/docs/claude.
- **Skill `set-up-monitoring`:** the setup described above.

## License

This plugin (the files in this folder) is available under the [MIT License](LICENSE). The license covers the plugin only, not the HawkEye
application or the rest of this repository, and it does not cover the HawkEye name or logo.
