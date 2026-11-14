/** Copy-and-paste ways for a scheduled job to ping its heartbeat monitor. */
/** Ready-to-paste ways of pinging, for the common kinds of scheduled job. */
export function pingExamples(url: string): { label: string; code: string }[] {
  return [
    { label: 'Crontab line', code: `0 2 * * * /usr/local/bin/backup.sh && curl -fsS -m 10 --retry 5 -o /dev/null ${url}` },
    { label: 'Report failures too (exit status)', code: `0 2 * * * /usr/local/bin/backup.sh; curl -fsS -m 10 --retry 5 -o /dev/null ${url}/$?` },
    { label: 'End of a shell script', code: `curl -fsS -m 10 --retry 5 -o /dev/null ${url}` },
    { label: 'Windows PowerShell', code: `Invoke-RestMethod -Uri "${url}" -TimeoutSec 10` },
  ]
}
