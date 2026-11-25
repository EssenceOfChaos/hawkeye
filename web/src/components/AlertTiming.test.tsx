import { fireEvent, render, screen } from '@testing-library/react'
import { useState } from 'react'
import { describe, expect, it } from 'vitest'
import type { QuietHours } from '../api'
import { localTimeZone } from '../format'
import { AlertTiming } from './AlertTiming'

function Harness({ initialDelay = 0, initialQuiet = null as QuietHours | null, onChange = (_d: number, _q: QuietHours | null) => {} }) {
  const [delay, setDelay] = useState(initialDelay)
  const [quiet, setQuiet] = useState<QuietHours | null>(initialQuiet)
  return (
    <AlertTiming delay={delay} quiet={quiet} errors={{}}
      onDelay={(d) => { setDelay(d); onChange(d, quiet) }} onQuiet={(q) => { setQuiet(q); onChange(delay, q) }} />
  )
}

describe('AlertTiming', () => {
  it('offers alert delays in words and keeps an unusual saved value', () => {
    render(<Harness initialDelay={45} />)
    const select = screen.getByLabelText('Alert me') as HTMLSelectElement
    expect(select.value).toBe('45')
    expect(screen.getByRole('option', { name: 'As soon as an outage is confirmed' })).toBeInTheDocument()
    expect(screen.getByRole('option', { name: 'After 2 hours down' })).toBeInTheDocument()
  })

  it('turns quiet hours on at 22:00 to 07:00 in the visitor’s own time zone, and off again', () => {
    let last: QuietHours | null = null
    render(<Harness onChange={(_d, q) => { last = q }} />)
    expect(screen.queryByLabelText('Quiet hours start')).not.toBeInTheDocument()
    fireEvent.click(screen.getByLabelText('Quiet hours for this monitor'))
    expect(last).toEqual({ start: '22:00', end: '07:00', timezone: localTimeZone() })
    fireEvent.change(screen.getByLabelText('Quiet hours end'), { target: { value: '06:30' } })
    expect(last).toMatchObject({ end: '06:30' })
    fireEvent.click(screen.getByLabelText('Quiet hours for this monitor'))
    expect(last).toBeNull()
  })

  it('shows a saved time zone even if the browser does not list it', () => {
    render(<Harness initialQuiet={{ start: '01:00', end: '02:00', timezone: 'Etc/GMT+5' }} />)
    expect((screen.getByLabelText('Time zone') as HTMLSelectElement).value).toBe('Etc/GMT+5')
  })

  it('holds back alerts while flapping by default, and says what turning it off means', () => {
    function Flap() {
      const [on, setOn] = useState(true)
      return <AlertTiming delay={0} quiet={null} errors={{}} onDelay={() => {}} onQuiet={() => {}} flap={{ value: on, onChange: setOn }} />
    }
    render(<Flap />)
    const toggle = screen.getByLabelText('Hold back alerts while it’s flapping') as HTMLInputElement
    expect(toggle.checked).toBe(true)
    expect(screen.getByText(/After 3 outages in an hour, you get one “flapping” alert/)).toBeInTheDocument()
    expect(screen.getByText(/If it stays down, you’re alerted at once/)).toBeInTheDocument()
    fireEvent.click(toggle)
    expect(toggle.checked).toBe(false)
    expect(screen.getByText(/Every outage and recovery is alerted/)).toBeInTheDocument()
  })

  it('has no flapping setting unless the form offers one', () => {
    render(<Harness />)
    expect(screen.queryByLabelText('Hold back alerts while it’s flapping')).not.toBeInTheDocument()
  })
})
