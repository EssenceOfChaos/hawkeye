import { ArrowsRightLeftIcon, BellAlertIcon, ChatBubbleLeftRightIcon, ClockIcon, CodeBracketIcon, CommandLineIcon, GlobeAltIcon, LinkIcon, RocketLaunchIcon } from '@heroicons/react/24/outline'
import type { ComponentType, ReactNode, SVGProps } from 'react'
import { Alerts, ApiDocs, ApiWorkflows, ClaudeDocs, CodexDocs, DnsMonitoring, GettingStarted, HeartbeatMonitoring, StatusPageDomains } from './docsArticles'

/** A docs article. KEEP IN STEP with the routes in App.tsx, the pages in seo.ts and the @app pattern in deploy/Caddyfile. */
export interface Doc {
  path: string
  title: string
  summary: string
  icon: ComponentType<SVGProps<SVGSVGElement>>
  body: () => ReactNode
}

export const DOCS: readonly Doc[] = [
  { path: '/docs/getting-started', title: 'Getting started', summary: 'From a new account to your first monitor and alert in a few minutes.', icon: RocketLaunchIcon, body: GettingStarted },
  { path: '/docs/cron-job-monitoring', title: 'Heartbeat (cron job) monitoring', summary: 'Get alerted when a backup, scheduled task or queue worker stops running or fails.', icon: ClockIcon, body: HeartbeatMonitoring },
  { path: '/docs/api-workflows', title: 'Multi-step API checks', summary: 'Check that a sequence of requests works, such as signing in and fetching an account, and see which step failed.', icon: ArrowsRightLeftIcon, body: ApiWorkflows },
  { path: '/docs/dns-monitoring', title: 'DNS monitoring', summary: 'Get alerted when a DNS record disappears or stops matching the value you expect.', icon: GlobeAltIcon, body: DnsMonitoring },
  { path: '/docs/status-page-domains', title: 'Status pages on your own domain', summary: 'Serve a status page at an address such as status.yourclient.com: the DNS records to add, and what happens if they change.', icon: LinkIcon, body: StatusPageDomains },
  { path: '/docs/alerts', title: 'Alerts', summary: 'Alert channels, routing alerts by client, alert delays, quiet hours and maintenance windows.', icon: BellAlertIcon, body: Alerts },
  { path: '/docs/api', title: 'API and API keys', summary: 'Use HawkEye from your own scripts and tools: create a key, send requests, and keep keys safe.', icon: CodeBracketIcon, body: ApiDocs },
  { path: '/docs/claude', title: 'Use HawkEye with Claude', summary: 'Connect Claude from its connector directory, or Claude Code with an API key, to check what is down, look at uptime, pause monitors and set up monitoring from your code by asking.', icon: ChatBubbleLeftRightIcon, body: ClaudeDocs },
  { path: '/docs/codex', title: 'Use HawkEye with Codex', summary: 'Install the Codex plugin to check your monitoring and set up monitors and cron job heartbeats from your code by asking.', icon: CommandLineIcon, body: CodexDocs },
]
