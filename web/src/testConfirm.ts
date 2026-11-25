// Tests only: answers the confirmation dialog (components/ConfirmDialog.tsx), which replaced window.confirm.
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect } from 'vitest'

/** Waits for the confirmation dialog, presses one of its buttons, waits for it to close and returns what it said. */
export async function answerConfirm(button: string | RegExp): Promise<string> {
  const dialog = await screen.findByRole('dialog')
  const text = dialog.textContent ?? ''
  await userEvent.click(within(dialog).getByRole('button', { name: button }))
  await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
  return text
}
