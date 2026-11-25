import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { useState } from 'react'
import { describe, expect, it } from 'vitest'
import { useConfirm } from './useConfirm'

/** A button that asks before "deleting", and shows what the dialog answered. */
function Harness({ danger = true }: { danger?: boolean }) {
  const { confirm, dialog } = useConfirm()
  const [answer, setAnswer] = useState('none')
  return (
    <>
      <button type="button" onClick={async () => setAnswer(String(await confirm({ title: 'Delete “Website”?', message: 'Its monitoring stops.', confirmLabel: 'Delete monitor', danger })))}>
        Delete
      </button>
      <output>{answer}</output>
      {dialog}
    </>
  )
}

const open = () => fireEvent.click(screen.getByRole('button', { name: 'Delete' }))
const answered = async (value: string) => {
  await waitFor(() => expect(screen.getByRole('status')).toHaveTextContent(value))
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
}

describe('useConfirm', () => {
  it('shows the question and what happens, with Cancel focused for a destructive action', () => {
    render(<Harness />)
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    open()
    const dialog = screen.getByRole('dialog', { name: 'Delete “Website”?' })
    expect(dialog).toHaveAccessibleDescription('Its monitoring stops.')
    expect(dialog).toHaveClass('d-modal')
    expect(screen.getByRole('button', { name: 'Cancel' })).toHaveFocus()
    expect(screen.getByRole('button', { name: 'Delete monitor' })).toHaveClass('danger')
  })

  it('goes ahead only from the confirm button', async () => {
    render(<Harness />)
    open()
    fireEvent.click(screen.getByRole('button', { name: 'Delete monitor' }))
    await answered('true')
  })

  it('cancels from Cancel', async () => {
    render(<Harness />)
    open()
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    await answered('false')
  })

  it('cancels from the close button', async () => {
    render(<Harness />)
    open()
    fireEvent.click(screen.getByRole('button', { name: 'Close' }))
    await answered('false')
  })

  it('cancels from a click outside the box', async () => {
    render(<Harness />)
    open()
    fireEvent.click(document.querySelector('.d-modal-backdrop')!)
    await answered('false')
  })

  it('cancels when the browser closes the dialog (Esc)', async () => {
    render(<Harness />)
    open()
    fireEvent(screen.getByRole('dialog'), new Event('close'))
    await answered('false')
  })

  it('asks a second question straight after the first is answered, in a dialog of its own', async () => {
    function TwoInARow() {
      const { confirm, dialog } = useConfirm()
      const [log, setLog] = useState<string[]>([])
      const ask = async () => {
        const first = await confirm({ title: 'First?', message: 'One.', confirmLabel: 'Yes, first' })
        const second = await confirm({ title: 'Second?', message: 'Two.', confirmLabel: 'Yes, second' })
        setLog([String(first), String(second)])
      }
      return <><button type="button" onClick={() => void ask()}>Ask</button><output>{log.join(',')}</output>{dialog}</>
    }
    render(<TwoInARow />)
    fireEvent.click(screen.getByRole('button', { name: 'Ask' }))
    fireEvent.click(await screen.findByRole('button', { name: 'Yes, first' }))
    // The second dialog is a fresh one: shown, with working buttons.
    expect(await screen.findByRole('dialog', { name: 'Second?' })).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Yes, second' }))
    await waitFor(() => expect(screen.getByRole('status')).toHaveTextContent('true,true'))
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('cancels an open question when another is asked, so its caller never waits forever', async () => {
    function Overlapping() {
      const { confirm, dialog } = useConfirm()
      const [log, setLog] = useState<string[]>([])
      const ask = (name: string) => async () => {
        const ok = await confirm({ title: `${name}?`, message: name, confirmLabel: `Delete ${name}` })
        setLog((l) => [...l, `${name}:${ok}`])
      }
      return (
        <>
          <button type="button" onClick={() => void ask('A')()}>Ask A</button>
          <button type="button" onClick={() => void ask('B')()}>Ask B</button>
          <output>{log.join(',')}</output>
          {dialog}
        </>
      )
    }
    render(<Overlapping />)
    fireEvent.click(screen.getByRole('button', { name: 'Ask A' }))
    await screen.findByRole('dialog', { name: 'A?' })
    // jsdom has no showModal, so the page behind stays clickable; a real browser makes it inert.
    fireEvent.click(screen.getByRole('button', { name: 'Ask B' }))
    await waitFor(() => expect(screen.getByRole('status')).toHaveTextContent('A:false'))
    expect(screen.getByRole('dialog', { name: 'B?' })).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Delete B' }))
    await waitFor(() => expect(screen.getByRole('status')).toHaveTextContent('A:false,B:true'))
  })

  it('focuses the confirm button when the action is not destructive', () => {
    render(<Harness danger={false} />)
    open()
    expect(screen.getByRole('button', { name: 'Delete monitor' })).toHaveFocus()
    expect(screen.getByRole('button', { name: 'Delete monitor' })).not.toHaveClass('danger')
  })
})
