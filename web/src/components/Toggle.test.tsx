import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { shake } from '../motion'
import { Toggle } from './Toggle'

vi.mock('../motion', () => ({ shake: vi.fn() }))

describe('Toggle', () => {
  beforeEach(() => vi.mocked(shake).mockClear())

  it('is a daisyUI switch named by its label, and reports the new state', async () => {
    const onToggle = vi.fn()
    render(<Toggle checked={false} onToggle={onToggle}>Weekly summary</Toggle>)
    const box = screen.getByLabelText('Weekly summary')
    expect(box).toHaveClass('d-toggle', 'd-toggle-primary')
    await userEvent.click(box)
    expect(onToggle).toHaveBeenCalledWith(true)
    expect(shake).not.toHaveBeenCalled()
  })

  it('shakes when the change did not stick', async () => {
    render(<Toggle checked={false} onToggle={() => Promise.resolve(false)}>Weekly summary</Toggle>)
    const box = screen.getByLabelText('Weekly summary')
    await userEvent.click(box)
    await waitFor(() => expect(shake).toHaveBeenCalledWith(box))
  })

  it('shows a spinner and cannot be changed while busy', () => {
    const { container } = render(<Toggle checked busy onToggle={() => undefined}>Public uptime badge</Toggle>)
    expect(screen.getByLabelText('Public uptime badge')).toBeDisabled()
    expect(container.querySelector('.d-loading')).toBeInTheDocument()
  })
})
