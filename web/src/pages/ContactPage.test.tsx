import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { AuthProvider } from '../auth'
import { ContactPage } from './ContactPage'

afterEach(() => vi.unstubAllGlobals())
beforeEach(() => {
  document.cookie = 'csrf_token=t; path=/'
})

const json = (status: number, body: unknown) =>
  Promise.resolve(new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }))

const categories = [
  { code: 'technical', label: "Something isn't working", hint: 'A monitor misbehaves.', priority: 'High' },
  { code: 'billing', label: 'Billing or subscription', hint: 'Payments.', priority: 'High' },
  { code: 'feature', label: 'Suggest a feature', hint: 'Ideas.', priority: 'Low' },
]

function mount(opts: { user?: boolean; submit?: () => Promise<Response> } = {}) {
  const posts: any[] = []
  vi.stubGlobal('fetch', vi.fn((url: string, init?: RequestInit) => {
    if (url.includes('/auth/me')) {
      return opts.user
        ? json(200, { user: { id: 'u', email: 'me@example.org', email_verified_at: '2026-01-01T00:00:00Z' }, organizations: [] })
        : json(401, { error: { code: 'UNAUTHENTICATED', message: 'x' } })
    }
    if (url.includes('/contact/categories')) return json(200, { categories })
    if (url.endsWith('/contact') && init?.method === 'POST') {
      posts.push(JSON.parse(init.body as string))
      return opts.submit ? opts.submit() : json(202, { reference: 'HK-ABC123' })
    }
    return json(404, {})
  }))
  render(
    <MemoryRouter>
      <AuthProvider>
        <ContactPage />
      </AuthProvider>
    </MemoryRouter>,
  )
  return posts
}

async function fillIn() {
  await userEvent.click(await screen.findByLabelText(/Billing or subscription/))
  await userEvent.type(screen.getByLabelText('Your name'), 'Ada')
  await userEvent.type(screen.getByLabelText('Email address'), 'ada@example.org')
  await userEvent.type(screen.getByLabelText('Subject'), 'Charged twice')
  await userEvent.type(screen.getByLabelText('Message'), 'I was charged twice this month.')
}

describe('ContactPage', () => {
  it('offers the support topics from the API and sends the chosen one', async () => {
    const posts = mount()
    await fillIn()
    await userEvent.click(screen.getByRole('button', { name: 'Send message' }))
    expect(await screen.findByText('HK-ABC123')).toBeInTheDocument()
    expect(posts[0]).toMatchObject({ category: 'billing', name: 'Ada', email: 'ada@example.org', subject: 'Charged twice', company: '' })
    expect(screen.getByText(/confirmation to/)).toHaveTextContent('ada@example.org')
  })

  it('shows field errors from the server next to the fields', async () => {
    mount({ submit: () => json(400, { error: { code: 'VALIDATION_ERROR', message: 'x', details: { fields: { email: 'Enter a valid email address so we can reply.' } } } }) })
    await fillIn()
    await userEvent.click(screen.getByRole('button', { name: 'Send message' }))
    expect(await screen.findByText('Enter a valid email address so we can reply.')).toBeInTheDocument()
    expect(screen.queryByText('HK-ABC123')).not.toBeInTheDocument()
  })

  it('tells the visitor when delivery fails instead of pretending it worked', async () => {
    mount({ submit: () => json(503, { error: { code: 'CONTACT_UNAVAILABLE', message: "We couldn't send your message just now. Please try again in a few minutes." } }) })
    await fillIn()
    await userEvent.click(screen.getByRole('button', { name: 'Send message' }))
    expect(await screen.findByRole('alert')).toHaveTextContent("couldn't send your message")
  })

  it('asks for the website only for topics where it helps', async () => {
    mount()
    await userEvent.click(await screen.findByLabelText(/Something isn't working/))
    expect(screen.getByLabelText(/Website concerned/)).toBeInTheDocument()
    await userEvent.click(screen.getByLabelText(/Suggest a feature/))
    expect(screen.queryByLabelText(/Website concerned/)).not.toBeInTheDocument()
  })

  it('prefills the address of a signed-in visitor', async () => {
    mount({ user: true })
    await screen.findByLabelText(/Billing or subscription/)
    expect(await screen.findByDisplayValue('me@example.org')).toBeInTheDocument()
  })

  it('hides the spam trap from people and assistive technology', async () => {
    mount()
    await screen.findByLabelText(/Billing or subscription/)
    const trap = document.querySelector('.hp')!
    expect(trap).toHaveAttribute('aria-hidden', 'true')
    expect(trap.querySelector('input')).toHaveAttribute('tabindex', '-1')
  })
})
