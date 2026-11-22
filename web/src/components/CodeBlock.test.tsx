import { act, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { CodeBlock } from './CodeBlock'

afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

describe('CodeBlock', () => {
  it('copies the code, says so, and goes back after two seconds', async () => {
    const writeText = vi.fn(() => Promise.resolve())
    vi.stubGlobal('navigator', { ...navigator, clipboard: { writeText } })
    vi.useFakeTimers()
    render(<CodeBlock>{'curl -X POST https://hawkeye.observer/api/v1/deploys'}</CodeBlock>)
    expect(screen.getByText('curl -X POST https://hawkeye.observer/api/v1/deploys')).toBeInTheDocument()

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Copy code' }))
    })
    expect(writeText).toHaveBeenCalledWith('curl -X POST https://hawkeye.observer/api/v1/deploys')
    expect(screen.getByRole('button', { name: 'Copied' })).toBeInTheDocument()
    expect(screen.getByText('Copied to the clipboard')).toBeInTheDocument()

    act(() => vi.advanceTimersByTime(2000))
    expect(screen.getByRole('button', { name: 'Copy code' })).toBeInTheDocument()
  })

  it('does nothing where the clipboard is unavailable', async () => {
    vi.stubGlobal('navigator', { ...navigator, clipboard: undefined })
    render(<CodeBlock>{'echo hi'}</CodeBlock>)
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Copy code' }))
    })
    expect(screen.getByRole('button', { name: 'Copy code' })).toBeInTheDocument()
  })
})
