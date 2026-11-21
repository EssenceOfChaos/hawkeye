# HawkEye plugin for Cursor

Set up [HawkEye](https://hawkeye.observer) monitoring from your codebase. Ask Cursor's agent to "set up monitoring for this project" and it:

1. reads the repository for your production domain, your cron jobs and scheduled tasks (crontab, GitHub Actions schedules, Kubernetes CronJobs,
   platform crons, in-app schedulers) and your health endpoints;
2. asks HawkEye to look at your live site and suggest monitors (home page, health endpoint, API subdomain, email records);
3. shows you one plan and waits for your approval;
4. creates the monitors, including a heartbeat for each job;
5. adds a ping to the end of each job, reading the private ping address from an environment variable or CI secret (never committed), and tells you
   which ones to set.

Nothing is created and no file is changed before you approve. Your code stays on your machine: HawkEye only receives the monitors you approve.

## Install

Create an API key with **Read and write** access at https://hawkeye.observer/api-keys. Read and write keys can add monitors on every plan, including
Free.

Install the HawkEye plugin from Cursor's plugin marketplace, then in Cursor open **Customize** > **Plugins** > **HawkEye** > **Configure** and enter
the key as its API key. Start a new chat.

To try it before it is in the marketplace, copy this folder to `~/.cursor/plugins/local/hawkeye` and reload the window.

The plugin connects Cursor to HawkEye's MCP server (`https://hawkeye.observer/mcp`) with that key, and adds the `set-up-monitoring` skill. The key
is sent only to HawkEye.

### Without the plugin

Add HawkEye's MCP server to `~/.cursor/mcp.json` (or a project's `.cursor/mcp.json`), with the key in an environment variable:

```json
{
  "mcpServers": {
    "hawkeye": {
      "url": "https://hawkeye.observer/mcp",
      "headers": {
        "Authorization": "Bearer ${env:HAWKEYE_API_KEY}"
      }
    }
  }
}
```

## What's included

- **MCP server:** HawkEye's tools: account overview, monitors, uptime, incidents, status pages, maintenance windows, monitor suggestions and
  creating monitors. What a key can change depends on its access and your plan; see https://hawkeye.observer/docs/claude.
- **Skill `set-up-monitoring`:** the setup described above.

## License

This plugin (the files in this folder) is available under the [MIT License](LICENSE). The license covers the plugin only, not the HawkEye
application or the rest of this repository, and it does not cover the HawkEye name or logo.
