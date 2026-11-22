import { Link } from 'react-router'
import type { Deploy } from '../api'
import { formatShortDateTime } from '../format'
import { useApi } from '../hooks'

/** What a deploy quieted, in words. */
function target(d: Deploy): string {
  if (d.tag) return `Tag “${d.tag}”`
  if (d.all_monitors) return 'Every monitor'
  return `${d.monitors} monitor${d.monitors === 1 ? '' : 's'}`
}

function Outcome({ d }: { d: Deploy }) {
  if (d.verified === true) return <span className="badge up"><span aria-hidden="true">✔</span> Passed</span>
  if (d.verified === false) return <span className="badge down"><span aria-hidden="true">✖</span> Failed</span>
  return <span className="hint">{d.active ? 'In progress' : 'Not checked'}</span>
}

/**
 * Deploys a CI pipeline reported (POST /deploys): when, for which monitors, how long alerts were quiet, and whether the check afterwards passed.
 * They're kept apart from scheduled windows, and listed for 30 days.
 */
export function RecentDeploys() {
  const deploys = useApi<{ deploys: Deploy[] }>('/deploys')
  return (
    <section aria-labelledby="deploys-h">
      <h2 id="deploys-h">Recent deploys</h2>
      <p className="hint">
        Your CI pipeline can quiet alerts while it deploys and check your sites afterwards. <Link to="/docs/deploys">Set it up</Link>
      </p>
      {deploys.data?.deploys.length === 0 && <p className="hint">No deploys in the last 30 days.</p>}
      {deploys.data && deploys.data.deploys.length > 0 && (
        <div className="table-wrap">
          <table>
            <caption className="sr-only">Deploys in the last 30 days, newest first</caption>
            <thead>
              <tr><th>Started</th><th>For</th><th>Note</th><th>Alerts quiet until</th><th>Check</th></tr>
            </thead>
            <tbody>
              {deploys.data.deploys.map((d) => (
                <tr key={d.id}>
                  <td className="nowrap"><time dateTime={d.started_at}>{formatShortDateTime(d.started_at)}</time></td>
                  <td>{target(d)}{d.started_by_key && <div className="hint">Key: {d.started_by_key}</div>}</td>
                  <td>{d.note ?? <span className="hint">—</span>}</td>
                  <td className="nowrap">
                    <time dateTime={d.ends_at}>{formatShortDateTime(d.ends_at)}</time>
                    {d.active ? <div className="hint">Quiet now</div> : d.finished_at && <div className="hint">Finished by the pipeline</div>}
                  </td>
                  <td><Outcome d={d} /></td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  )
}
