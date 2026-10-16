/**
 * AcceptInvitationScreen behaviour: the screen reads the invitation a token
 * points to through the host-bound operations, accepts it, and gives the
 * contract's two read failures -- a bare 404 and a 409 -- their own text.
 * Text expectations read the bundle values, never inline language.
 */

import { describe, expect, it, vi } from 'vitest'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { AcceptInvitationScreen } from './AcceptInvitationScreen.js'
import type { InvitationOperations } from './AcceptInvitationScreen.js'
import { renderWithProviders } from '../test-utils/render.js'
import { expectNoAxeViolations } from '../test-utils/axe.js'
import zhCN from './locales/zh-CN.json' with { type: 'json' }
import enUS from './locales/en-US.json' with { type: 'json' }

/** The invitation read's success answer for these suites. */
const INVITATION = {
  tenant: 'Acme',
  email: 'invitee@example.com',
  status: 'pending',
  expiresAt: '2026-10-20T12:00:00Z',
}

function operations(
  overrides: Partial<InvitationOperations> = {},
): InvitationOperations {
  return {
    readInvitation: () => Promise.resolve(INVITATION),
    acceptInvitation: () => Promise.resolve(),
    ...overrides,
  }
}

describe('AcceptInvitationScreen', () => {
  it('renders the invitation and an accept action', async () => {
    renderWithProviders(
      <AcceptInvitationScreen token="tok-1" invitations={operations()} />,
    )
    expect(
      await screen.findByText(
        zhCN.acceptInvitation.invited.replace('{{tenant}}', 'Acme'),
      ),
    ).toBeInTheDocument()
    expect(
      screen.getByText(
        zhCN.acceptInvitation.email.replace('{{email}}', 'invitee@example.com'),
      ),
    ).toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: zhCN.acceptInvitation.accept }),
    ).toBeInTheDocument()
  })

  it('accepts the invitation and fires onAccepted once', async () => {
    const acceptInvitation = vi.fn(() => Promise.resolve())
    const onAccepted = vi.fn()
    const user = userEvent.setup()
    renderWithProviders(
      <AcceptInvitationScreen
        token="tok-1"
        invitations={operations({ acceptInvitation })}
        onAccepted={onAccepted}
      />,
    )
    await user.click(
      await screen.findByRole('button', {
        name: zhCN.acceptInvitation.accept,
      }),
    )
    expect(acceptInvitation).toHaveBeenCalledWith('tok-1', {})
    expect(
      await screen.findByText(zhCN.acceptInvitation.accepted.title),
    ).toBeInTheDocument()
    expect(onAccepted).toHaveBeenCalledTimes(1)
  })

  it('passes a display name and password for an address with no account yet', async () => {
    const acceptInvitation = vi.fn(() => Promise.resolve())
    const user = userEvent.setup()
    renderWithProviders(
      <AcceptInvitationScreen
        token="tok-1"
        invitations={operations({ acceptInvitation })}
      />,
    )
    await user.type(
      await screen.findByLabelText(zhCN.acceptInvitation.displayNameLabel),
      'Ada Lovelace',
    )
    await user.type(
      screen.getByLabelText(zhCN.acceptInvitation.passwordLabel),
      's3cret-pass',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.acceptInvitation.accept }),
    )
    expect(acceptInvitation).toHaveBeenCalledWith('tok-1', {
      displayName: 'Ada Lovelace',
      password: 's3cret-pass',
    })
  })

  it('reports a refused acceptance with the answer code and stays retryable', async () => {
    const acceptInvitation = vi.fn(() =>
      Promise.reject({ status: 409, code: 'authn.conflict' }),
    )
    const user = userEvent.setup()
    renderWithProviders(
      <AcceptInvitationScreen
        token="tok-1"
        invitations={operations({ acceptInvitation })}
      />,
    )
    await user.click(
      await screen.findByRole('button', {
        name: zhCN.acceptInvitation.accept,
      }),
    )
    // authn.conflict is a whitelisted code: its own text, not the fallback.
    expect(
      await screen.findByText(zhCN.errors.authn.conflict),
    ).toBeInTheDocument()
    // The accept action remains: a refused acceptance is retryable.
    expect(
      screen.getByRole('button', { name: zhCN.acceptInvitation.accept }),
    ).toBeEnabled()
  })

  it('renders the not-found text for a bare 404 read answer', async () => {
    renderWithProviders(
      <AcceptInvitationScreen
        token="tok-1"
        invitations={operations({
          readInvitation: () =>
            Promise.reject({ status: 404, code: 'client.http.404' }),
        })}
      />,
    )
    expect(
      await screen.findByText(zhCN.acceptInvitation.missing.title),
    ).toBeInTheDocument()
    expect(
      screen.getByText(zhCN.acceptInvitation.missing.description),
    ).toBeInTheDocument()
  })

  it('renders the expired text for a 409 read answer', async () => {
    renderWithProviders(
      <AcceptInvitationScreen
        token="tok-1"
        invitations={operations({
          readInvitation: () =>
            Promise.reject({ status: 409, code: 'authn.conflict' }),
        })}
      />,
    )
    expect(
      await screen.findByText(zhCN.acceptInvitation.unavailable.title),
    ).toBeInTheDocument()
  })

  it('renders the code banner and a retry for any other read failure', async () => {
    const readInvitation = vi.fn(() =>
      Promise.reject({ status: 503, code: 'authn.unavailable' }),
    )
    const user = userEvent.setup()
    renderWithProviders(
      <AcceptInvitationScreen
        token="tok-1"
        invitations={operations({ readInvitation })}
      />,
    )
    expect(
      await screen.findByText(zhCN.acceptInvitation.error.title),
    ).toBeInTheDocument()
    // authn.unavailable is a whitelisted code: its own text, not the fallback.
    expect(
      screen.getByText(zhCN.errors.authn.unavailable),
    ).toBeInTheDocument()

    await user.click(
      screen.getByRole('button', { name: zhCN.acceptInvitation.retry }),
    )
    expect(readInvitation).toHaveBeenCalledTimes(2)
  })

  it('renders in the en-US bundle on an English-starting instance', async () => {
    renderWithProviders(
      <AcceptInvitationScreen token="tok-1" invitations={operations()} />,
      { language: 'en-US' },
    )
    expect(
      await screen.findByText(
        enUS.acceptInvitation.invited.replace('{{tenant}}', 'Acme'),
      ),
    ).toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: enUS.acceptInvitation.accept }),
    ).toBeInTheDocument()
  })

  it('has no axe violations', async () => {
    renderWithProviders(
      <AcceptInvitationScreen token="tok-1" invitations={operations()} />,
    )
    await screen.findByRole('button', {
      name: zhCN.acceptInvitation.accept,
    })
    await expectNoAxeViolations()
  })
})
