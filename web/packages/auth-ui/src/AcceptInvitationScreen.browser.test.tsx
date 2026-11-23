/**
 * AcceptInvitationScreen focus in a real browser: an acceptance in flight
 * keeps focus on the control that started it.
 *
 * jsdom does not apply the spec's focus fixup when a focused control
 * becomes unfocusable, so a natively disabled button can keep
 * `document.activeElement` there and hide the defect. Chromium moves focus
 * to the document body the moment the button drops out of the tab order,
 * which is exactly what a keyboard user loses.
 */
import { describe, expect, it, vi } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import { userEvent } from 'vitest/browser'
import { AcceptInvitationScreen } from './AcceptInvitationScreen.js'
import type { InvitationOperations } from './AcceptInvitationScreen.js'
import { renderWithProviders } from '../test-utils/render.js'
import zhCN from './locales/zh-CN.json' with { type: 'json' }

const INVITATION = {
  tenant: 'Acme',
  tenantDisplayName: 'Acme',
  email: 'invitee@example.com',
  status: 'pending',
  expiresAt: new Date(Date.now() + 7 * 24 * 60 * 60 * 1000).toISOString(),
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

/** A promise the test settles by hand, for observing an in-flight write. */
function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((settle) => {
    resolve = settle
  })
  return { promise, resolve }
}

describe('AcceptInvitationScreen focus in Chromium', () => {
  it('keeps focus on the accept control while the acceptance is in flight', async () => {
    const pending = deferred<void>()
    const acceptInvitation = vi.fn(() => pending.promise)
    renderWithProviders(
      <AcceptInvitationScreen
        token="tok-1"
        invitations={operations({ acceptInvitation })}
        signedIn={true}
      />,
    )
    const accept = await screen.findByRole('button', {
      name: zhCN.acceptInvitation.accept,
    })
    await userEvent.click(accept)
    expect(accept).toHaveFocus()
    expect(accept).toHaveAttribute('aria-disabled', 'true')
    expect(accept).toHaveAttribute('aria-busy', 'true')

    pending.resolve()
    await waitFor(() => {
      expect(
        screen.getByText(zhCN.acceptInvitation.accepted.title),
      ).toBeInTheDocument()
    })
  })

  it('moves focus to the completion heading when the acceptance lands', async () => {
    // The action area unmounts with the control that started the
    // acceptance, so Chromium drops focus to the body. The confirmation
    // heading is the new content's starting point and takes it instead.
    const pending = deferred<void>()
    const acceptInvitation = vi.fn(() => pending.promise)
    renderWithProviders(
      <AcceptInvitationScreen
        token="tok-1"
        invitations={operations({ acceptInvitation })}
        signedIn={true}
      />,
    )
    const accept = await screen.findByRole('button', {
      name: zhCN.acceptInvitation.accept,
    })
    await userEvent.click(accept)
    expect(accept).toHaveFocus()

    pending.resolve()
    const heading = await screen.findByRole('heading', {
      name: zhCN.acceptInvitation.accepted.title,
    })
    await waitFor(() => expect(heading).toHaveFocus())
  })
})
