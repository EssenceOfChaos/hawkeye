import { useEffect, useId, useRef, useState, type ComponentType, type SVGProps } from 'react'
import { animate, createTimeline, svg } from 'animejs'
import {
  BoltIcon,
  ChatBubbleLeftRightIcon,
  ClockIcon,
  CodeBracketIcon,
  EnvelopeIcon,
  GlobeAltIcon,
  LockClosedIcon,
  ServerStackIcon,
} from '@heroicons/react/24/outline'
import { inScope, prefersReducedMotion } from '../motion'

/*
 * The landing page's "how it works" animation: what HawkEye watches, how it confirms an outage before alerting, and where the alerts go.
 *
 * It is an illustration, not live data. One timeline (12 s, looping) sets data-state on the SVG, and CSS (index.css, .flow) shows each state; pulses
 * move along the wires with anime.js motion paths. With reduced motion, or before the script runs, a single frame shows an outage confirmed and alerts
 * sent, so the story is there without motion. The animation pauses while the figure is off screen.
 *
 * Two layouts share every class name: wide (three columns, left to right) and narrow (top to bottom) for phones, where the wide one's text would be
 * too small to read.
 */

type Icon = ComponentType<SVGProps<SVGSVGElement>>
/**
 * The story, in order: all well; the API goes down (HawkEye has not checked yet); one failed check; a second failed check confirms the outage; the
 * alerts arrive; the API comes back; HawkEye sees it recover; the recovery alerts arrive. CSS keys every visual change off these names.
 */
type State = 'healthy' | 'down' | 'failing' | 'confirmed' | 'alerted' | 'resolving' | 'recovered' | 'closed'

const INPUTS: { key: string; label: string; short: string; icon: Icon }[] = [
  { key: 'website', label: 'Website', short: 'Website', icon: GlobeAltIcon },
  { key: 'api', label: 'API', short: 'API', icon: CodeBracketIcon },
  { key: 'cron', label: 'Cron job', short: 'Cron', icon: ClockIcon },
  { key: 'dns', label: 'DNS record', short: 'DNS', icon: ServerStackIcon },
  { key: 'ssl', label: 'SSL certificate', short: 'SSL', icon: LockClosedIcon },
]
const FAILING = 1 // the API

const OUTPUTS: { key: string; title: string; sub: string; shortSub: string; icon: Icon }[] = [
  { key: 'email', title: 'Email', sub: 'To you and your team', shortSub: 'Your team', icon: EnvelopeIcon },
  { key: 'chat', title: 'Chat apps', sub: 'Slack, Teams, Discord, Telegram', shortSub: 'Slack and more', icon: ChatBubbleLeftRightIcon },
  { key: 'webhook', title: 'Webhook', sub: 'Your own systems', shortSub: 'Your systems', icon: BoltIcon },
]

const CHECKS = 10 // squares in the "recent checks" strip; the last two fail

interface Box { x: number; y: number; w: number; h: number }
interface Layout {
  name: 'wide' | 'narrow'
  width: number
  height: number
  inputs: Box[]
  inWires: string[] // from each input to the engine, through the junction
  junction: [number, number]
  engine: Box
  wordmark: [number, number, number] // x, y, font size
  status: Box
  checksLabel: [number, number]
  checks: [number, number, number, number] // x, y, size, gap
  divider: [number, number, number] // x1, x2, y
  steps: [number, number][] // check, confirm, alert
  stepSize: number
  outputs: Box[]
  outWires: string[]
  pill: [number, number] // center x, y
  headers?: [string, number, number][]
}

const wideInputs = INPUTS.map((_, i) => ({ x: 45, y: 78 + i * 72, w: 274, h: 54 }))
const WIDE: Layout = {
  name: 'wide',
  width: 1200,
  height: 470,
  inputs: wideInputs,
  junction: [438, 249],
  inWires: wideInputs.map((b) => {
    const cy = b.y + b.h / 2
    return cy === 249 ? 'M319 249 H497' : `M319 ${cy} C372 ${cy} 400 ${249 + (cy - 249) * 0.3} 438 249 H497`
  }),
  engine: { x: 497, y: 68, w: 333, h: 362 },
  wordmark: [521, 108, 26],
  status: { x: 519, y: 128, w: 290, h: 92 },
  checksLabel: [521, 254],
  checks: [521, 266, 22, 7],
  divider: [521, 807, 316],
  steps: [[521, 352], [622, 352], [744, 352]],
  stepSize: 14,
  outputs: OUTPUTS.map((_, i) => ({ x: 905, y: 149 + i * 68, w: 250, h: 64 })),
  outWires: OUTPUTS.map((_, i) => {
    const cy = 181 + i * 68
    return cy === 249 ? 'M830 249 H905' : `M830 249 H866 C888 249 884 ${cy} 905 ${cy}`
  }),
  pill: [1030, 118],
  headers: [
    ['01', 45, 38],
    ['02', 497, 38],
    ['03', 905, 38],
  ],
}

const narrowInputs = INPUTS.map((_, i) => ({ x: 8 + i * 70, y: 12, w: 64, h: 66 }))
const NARROW: Layout = {
  name: 'narrow',
  width: 360,
  height: 672,
  inputs: narrowInputs,
  junction: [180, 128],
  inWires: narrowInputs.map((b) => {
    const cx = b.x + b.w / 2
    return cx === 180 ? 'M180 78 V158' : `M${cx} 78 C${cx} 104 ${180 + (cx - 180) * 0.25} 116 180 128 V158`
  }),
  engine: { x: 14, y: 158, w: 332, h: 290 },
  wordmark: [34, 196, 22],
  status: { x: 32, y: 212, w: 296, h: 84 },
  checksLabel: [34, 326],
  checks: [34, 338, 22, 7.5],
  divider: [34, 326, 384],
  steps: [[34, 418], [132, 418], [246, 418]],
  stepSize: 12.5,
  outputs: OUTPUTS.map((_, i) => ({ x: 8 + i * 118, y: 508, w: 108, h: 112 })),
  outWires: OUTPUTS.map((_, i) => {
    const cx = 62 + i * 118
    return cx === 180 ? 'M180 448 V508' : `M180 448 V466 C180 486 ${cx} 484 ${cx} 508`
  }),
  pill: [180, 648],
}

const HEADER_TEXT: Record<string, string> = { '01': 'What HawkEye watches', '02': 'Confirms the outage', '03': 'Tells you and your team' }

/** The status panel's text for each state: [title, detail]. */
const STATUS: { state: State | State[]; tone: 'up' | 'warn' | 'down'; title: string; detail: string; short: string }[] = [
  { state: ['healthy', 'down'], tone: 'up', title: 'All checks passing', detail: 'Every check answered as expected', short: 'Every check answered' },
  { state: 'failing', tone: 'warn', title: 'API check failed', detail: 'Checking again before alerting', short: 'Checking again first' },
  { state: ['confirmed', 'alerted', 'resolving'], tone: 'down', title: 'Outage confirmed', detail: 'Failed checks in a row, not a blip', short: 'Failed checks in a row' },
  { state: ['recovered', 'closed'], tone: 'up', title: 'API back online', detail: 'Recovered, incident closed', short: 'Incident closed' },
]

function useNarrow(): boolean {
  const query = '(max-width: 640px)'
  const [narrow, setNarrow] = useState(() => typeof window !== 'undefined' && typeof window.matchMedia === 'function' && window.matchMedia(query).matches)
  useEffect(() => {
    if (typeof window.matchMedia !== 'function') return
    const mq = window.matchMedia(query)
    const on = () => setNarrow(mq.matches)
    mq.addEventListener('change', on)
    return () => mq.removeEventListener('change', on)
  }, [])
  return narrow
}

function StatusIcon({ cx, cy, tone }: { cx: number; cy: number; tone: 'up' | 'warn' | 'down' }) {
  return (
    <g className={`flow-status-icon ${tone}`}>
      <circle cx={cx} cy={cy} r={12} />
      {tone === 'up' ? (
        <path d={`M${cx - 6} ${cy} l4 5 l8 -10`} />
      ) : (
        <path d={`M${cx} ${cy - 6} v7 M${cx} ${cy + 5.5} v0.5`} />
      )}
    </g>
  )
}

function Diagram({ l }: { l: Layout }) {
  const narrow = l.name === 'narrow'
  const [jx, jy] = l.junction
  const [checkX, checkY, size, gap] = l.checks
  const { engine: e, status: s } = l
  return (
    <>
      {l.headers?.map(([n, x, y]) => (
        <text key={n} x={x} y={y} className="flow-header">
          {n} <tspan className="flow-header-label">{HEADER_TEXT[n]}</tspan>
        </text>
      ))}

      {/* Wires in, the junction, and the failing wire's red overlay. */}
      {l.inWires.map((d, i) => <path key={i} className="flow-wire" d={d} />)}
      <path className="flow-wire flow-wire-bad" d={l.inWires[FAILING]} />
      <path className="flow-wire flow-wire-good" d={l.inWires[FAILING]} />
      <circle className="flow-junction" cx={jx} cy={jy} r={narrow ? 8 : 11} />
      <circle className="flow-junction-core" cx={jx} cy={jy} r={narrow ? 3.2 : 4.4} />

      {/* What HawkEye watches. */}
      {INPUTS.map((input, i) => {
        const b = l.inputs[i]
        const Ico = input.icon
        const failing = i === FAILING
        return (
          <g key={input.key} className={`flow-input${failing ? ' flow-input-failing' : ''}`}>
            <rect className="flow-card" x={b.x} y={b.y} width={b.w} height={b.h} rx={12} />
            {narrow ? (
              <>
                <Ico className="flow-icon" x={b.x + b.w / 2 - 11} y={b.y + 10} width={22} height={22} aria-hidden="true" />
                <text className="flow-input-label" x={b.x + b.w / 2} y={b.y + 52} textAnchor="middle">{input.short}</text>
              </>
            ) : (
              <>
                <Ico className="flow-icon" x={b.x + 17} y={b.y + 15} width={24} height={24} aria-hidden="true" />
                <text className="flow-input-label" x={b.x + 55} y={b.y + 33}>{input.label}</text>
                {failing && <text className="flow-input-error" x={b.x + b.w - 18} y={b.y + 32} textAnchor="end">503 error</text>}
              </>
            )}
          </g>
        )
      })}

      {/* Outgoing wires, with red and green overlays for the alert pulses. */}
      {l.outWires.map((d, i) => (
        <g key={i}>
          <path className="flow-wire flow-wire-out" d={d} />
          <path className="flow-wire flow-wire-bad flow-out-bad" d={d} />
          <path className="flow-wire flow-wire-good flow-out-good" d={d} />
        </g>
      ))}

      {/* The engine. */}
      <rect className="flow-engine" x={e.x} y={e.y} width={e.w} height={e.h} rx={20} />
      <text className="flow-wordmark" x={l.wordmark[0]} y={l.wordmark[1]} fontSize={l.wordmark[2]}>
        Hawk<tspan className="flow-gold">Eye</tspan>
      </text>
      <rect className="flow-status-well" x={s.x} y={s.y} width={s.w} height={s.h} rx={12} />
      {STATUS.map((st) => {
        const states = Array.isArray(st.state) ? st.state : [st.state]
        return (
          <g key={st.title} className={`flow-status ${states.map((x) => `on-${x}`).join(' ')}`}>
            <rect className={`flow-status-fill ${st.tone}`} x={s.x + 6} y={s.y + 6} width={s.w - 12} height={s.h - 12} rx={9} />
            <StatusIcon cx={s.x + 30} cy={s.y + s.h / 2} tone={st.tone} />
            <text className="flow-status-title" x={s.x + 52} y={s.y + s.h / 2 - 4}>{st.title}</text>
            <text className="flow-status-detail" x={s.x + 52} y={s.y + s.h / 2 + 17}>{narrow ? st.short : st.detail}</text>
          </g>
        )
      })}
      <text className="flow-muted" x={l.checksLabel[0]} y={l.checksLabel[1]}>Recent checks of the API</text>
      {Array.from({ length: CHECKS }, (_, i) => (
        <rect key={i} className={`flow-check${i >= CHECKS - 2 ? ` flow-check-${CHECKS - i === 2 ? 'first' : 'second'}` : ''}`}
          x={checkX + i * (size + gap)} y={checkY} width={size} height={size} rx={5} />
      ))}
      <path className="flow-divider" d={`M${l.divider[0]} ${l.divider[2]} H${l.divider[1]}`} />
      {(['check', 'confirm', 'alert'] as const).map((step, i) => (
        <text key={step} className={`flow-step flow-step-${step}`} x={l.steps[i][0]} y={l.steps[i][1]} fontSize={l.stepSize}>
          {step.toUpperCase()}
        </text>
      ))}
      <rect className="flow-engine-alarm" x={e.x} y={e.y} width={e.w} height={e.h} rx={20} />

      {/* Where the alerts go. */}
      {OUTPUTS.map((out, i) => {
        const b = l.outputs[i]
        const Ico = out.icon
        return (
          <g key={out.key} className="flow-output">
            <rect className="flow-card flow-output-card" x={b.x} y={b.y} width={b.w} height={b.h} rx={13} />
            {narrow ? (
              <>
                <Ico className="flow-icon" x={b.x + b.w / 2 - 12} y={b.y + 14} width={24} height={24} aria-hidden="true" />
                <text className="flow-output-title" x={b.x + b.w / 2} y={b.y + 64} textAnchor="middle">{out.title}</text>
                <text className="flow-output-sub" x={b.x + b.w / 2} y={b.y + 84} textAnchor="middle">{out.shortSub}</text>
              </>
            ) : (
              <>
                <Ico className="flow-icon" x={b.x + 18} y={b.y + 20} width={24} height={24} aria-hidden="true" />
                <text className="flow-output-title" x={b.x + 56} y={b.y + 28}>{out.title}</text>
                <text className="flow-output-sub" x={b.x + 56} y={b.y + 47}>{out.sub}</text>
              </>
            )}
          </g>
        )
      })}
      <g className="flow-pill flow-pill-bad">
        <rect x={l.pill[0] - 82} y={l.pill[1] - 14} width={164} height={28} rx={14} />
        <text x={l.pill[0]} y={l.pill[1] + 4.5} textAnchor="middle">OUTAGE ALERT SENT</text>
      </g>
      <g className="flow-pill flow-pill-good">
        <rect x={l.pill[0] - 90} y={l.pill[1] - 14} width={180} height={28} rx={14} />
        <text x={l.pill[0]} y={l.pill[1] + 4.5} textAnchor="middle">RECOVERY ALERT SENT</text>
      </g>

      {/* Moving dots: steady checks on every input, and the pulses the timeline sends. */}
      {l.inWires.map((_, i) => <circle key={i} className="flow-dot" r={narrow ? 3 : 3.7} data-wire={i} />)}
      <circle className="flow-pulse flow-pulse-bad" data-pulse="in-bad-1" r={6.3} />
      <circle className="flow-pulse flow-pulse-bad" data-pulse="in-bad-2" r={6.3} />
      <circle className="flow-pulse flow-pulse-good" data-pulse="in-good" r={6.3} />
      {OUTPUTS.map((_, i) => <circle key={i} className="flow-pulse flow-pulse-bad" data-pulse={`out-bad-${i}`} r={6.3} />)}
      {OUTPUTS.map((_, i) => <circle key={i} className="flow-pulse flow-pulse-good" data-pulse={`out-good-${i}`} r={6.3} />)}

      {/* Paths for the motion, never drawn. */}
      <g className="flow-tracks" aria-hidden="true">
        {l.inWires.map((d, i) => <path key={i} d={d} data-track={`in-${i}`} />)}
        {l.outWires.map((d, i) => <path key={i} d={d} data-track={`out-${i}`} />)}
      </g>
    </>
  )
}

const CYCLE = 12000

/** Builds the looping timeline on `root` (the SVG). Returns its cleanup. */
function run(root: SVGSVGElement): () => void {
  const set = (state: State) => () => root.setAttribute('data-state', state)
  const track = (name: string) => root.querySelector<SVGPathElement>(`[data-track="${name}"]`)!
  const pulse = (name: string) => root.querySelector<SVGCircleElement>(`[data-pulse="${name}"]`)!
  const flight = (_dot: Element, path: SVGPathElement, duration: number) => ({ ...svg.createMotionPath(path), opacity: [{ to: 1, duration: 120 }, { to: 1, duration: duration - 240 }, { to: 0, duration: 120 }], duration, ease: 'inOutSine' })

  // Steady checks: a dot runs along each input wire, at slightly different speeds so they never line up.
  const dots = Array.from(root.querySelectorAll<SVGCircleElement>('.flow-dot')).map((dot, i) =>
    // Started part of the way along (seek) rather than delayed, so no dot waits at the corner of the figure.
    animate(dot, { ...svg.createMotionPath(track(`in-${i}`)), duration: 2600 + i * 310, loop: true, ease: 'linear' }).seek(i * 700),
  )
  root.setAttribute('data-running', '')

  // Each pulse is one event reaching the next stage: two failed checks, the outage alerts, the recovery and its alerts.
  const tl = createTimeline({ loop: true })
  const send = (name: string, path: SVGPathElement, at: number, duration = 1100) => tl.add(pulse(name), flight(pulse(name), path, duration), at)
  tl.call(set('healthy'), 0).call(set('down'), 3000)
  send('in-bad-1', track(`in-${FAILING}`), 3000)
  tl.call(set('failing'), 4100)
  send('in-bad-2', track(`in-${FAILING}`), 4500)
  tl.call(set('confirmed'), 5600)
  OUTPUTS.forEach((_, i) => send(`out-bad-${i}`, track(`out-${i}`), 5700 + i * 180, 1000))
  tl.call(set('alerted'), 6900).call(set('resolving'), 8400)
  send('in-good', track(`in-${FAILING}`), 8400)
  tl.call(set('recovered'), 9500)
  OUTPUTS.forEach((_, i) => send(`out-good-${i}`, track(`out-${i}`), 9600 + i * 180, 900))
  tl.call(set('closed'), 10700).call(() => {}, CYCLE) // the cycle's length: "closed" holds until here

  // Run only while the figure is on screen.
  const all = [tl, ...dots]
  const seen = new IntersectionObserver(([entry]) => all.forEach((a) => (entry.isIntersecting ? a.play() : a.pause())), { threshold: 0.15 })
  seen.observe(root)
  return () => {
    seen.disconnect()
    all.forEach((a) => a.revert())
    root.removeAttribute('data-running')
    root.setAttribute('data-state', 'alerted')
  }
}

const DESCRIPTION =
  'HawkEye checks your website, API, cron jobs, DNS records and SSL certificates. In this example the API fails one check, HawkEye checks again before ' +
  'alerting, confirms the outage after consecutive failed checks, and sends alerts by email, chat apps and webhook. When the API recovers, HawkEye ' +
  'sends a recovery alert and closes the incident.'

/** The animated "how it works" figure. Illustration only. */
export function MonitoringFlow() {
  const narrow = useNarrow()
  const ref = useRef<SVGSVGElement>(null)
  const id = useId()
  const l = narrow ? NARROW : WIDE

  useEffect(() => {
    const root = ref.current
    if (!root || prefersReducedMotion() || typeof IntersectionObserver === 'undefined') return
    return inScope(root as unknown as HTMLElement, (add) => add(run(root)))
  }, [narrow])

  return (
    <figure className="flow-figure">
      <svg ref={ref} className={`flow flow-${l.name}`} viewBox={`0 0 ${l.width} ${l.height}`} data-state="alerted" role="img"
        aria-labelledby={`${id}-title`} aria-describedby={`${id}-desc`}>
        <title id={`${id}-title`}>How HawkEye confirms an outage and alerts you</title>
        <desc id={`${id}-desc`}>{DESCRIPTION}</desc>
        <Diagram l={l} />
      </svg>
      <figcaption className="hint">Illustration, not live data.</figcaption>
    </figure>
  )
}
