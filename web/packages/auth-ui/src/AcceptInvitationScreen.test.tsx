/**
 * AcceptInvitationScreen behaviour: the screen reads the invitation a token
 * points to through the host-bound operations, accepts it for the signed-in
 * account, gives the contract's two read failures -- a bare 404 and a 409 --
 * their own text, and turns the unproved-email refusal into the proof step.
 * Text expectations read the bundle values, never inline language.
 */

import { describe, expect, it, vi } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { AcceptInvitationScreen } from './AcceptInvitationScreen.js'
import type { InvitationOperations } from './AcceptInvitationScreen.js'
import { renderWithProviders } from '../test-utils/render.js'
import { expectNoAxeViolations } from '../test-utils/axe.js'
import { DisplayTimeZoneProvider } from '@speed/ui-kit'
import zhCN from './locales/zh-CN.json' with { type: 'json' }
import enUS from './locales/en-US.json' with { type: 'json' }

/** The invitation read's success answer for these suites: pending and still
 * ahead of its deadline, so the screen reaches its accept action. */
const INVITATION = {
  tenant: 'Acme',
  email: 'invitee@example.com',
  status: 'pending',
  expiresAt: new Date(Date.now() + 7 * 24 * 60 * 60 * 1000).toISOString(),
}

const UNPROVED = {
  status: 403,
  code: 'authn.invitation_email_unproved',
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
  it('renders the invitation and an accept action from the public token read', async () => {
    // The token read is public: the screen shows what the link points to
    // without any session of its own. Acceptance is what needs the account.
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

  it('renders the unavailable state for an invitation already accepted', async () => {
    renderWithProviders(
      <AcceptInvitationScreen
        token="tok-1"
        invitations={operations({
          readInvitation: () =>
            Promise.resolve({ ...INVITATION, status: 'accepted' }),
        })}
      />,
    )
    expect(
      await screen.findByText(zhCN.acceptInvitation.unavailable.title),
    ).toBeInTheDocument()
    expect(
      screen.getByText(zhCN.acceptInvitation.unavailable.description),
    ).toBeInTheDocument()
    // A link that can no longer be redeemed shows neither the summary nor the
    // accept action.
    expect(
      screen.queryByText(
        zhCN.acceptInvitation.invited.replace('{{tenant}}', 'Acme'),
      ),
    ).not.toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: zhCN.acceptInvitation.accept }),
    ).not.toBeInTheDocument()
  })

  it('renders the unavailable state for a revoked invitation', async () => {
    renderWithProviders(
      <AcceptInvitationScreen
        token="tok-1"
        invitations={operations({
          readInvitation: () =>
            Promise.resolve({ ...INVITATION, status: 'revoked' }),
        })}
      />,
    )
    expect(
      await screen.findByText(zhCN.acceptInvitation.unavailable.title),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: zhCN.acceptInvitation.accept }),
    ).not.toBeInTheDocument()
  })

  it('renders the unavailable state for a pending invitation whose expiry has passed', async () => {
    const expired = new Date(Date.now() - 60 * 1000).toISOString()
    renderWithProviders(
      <AcceptInvitationScreen
        token="tok-1"
        invitations={operations({
          readInvitation: () =>
            Promise.resolve({ ...INVITATION, expiresAt: expired }),
        })}
      />,
    )
    expect(
      await screen.findByText(zhCN.acceptInvitation.unavailable.title),
    ).toBeInTheDocument()
    expect(
      screen.queryByText(
        zhCN.acceptInvitation.invited.replace('{{tenant}}', 'Acme'),
      ),
    ).not.toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: zhCN.acceptInvitation.accept }),
    ).not.toBeInTheDocument()
  })

  it('renders the unavailable state from a state-only read', async () => {
    // A terminal or expired read answers the state alone: the screen must not
    // need the tenant, the address or the deadline to render it.
    renderWithProviders(
      <AcceptInvitationScreen
        token="tok-1"
        invitations={operations({
          readInvitation: () =>
            Promise.resolve({ tenant: '', email: '', status: 'expired', expiresAt: '' }),
        })}
      />,
    )
    expect(
      await screen.findByText(zhCN.acceptInvitation.unavailable.title),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: zhCN.acceptInvitation.accept }),
    ).not.toBeInTheDocument()
  })

  it('still offers the accept action for a pending invitation whose expiry has not passed', async () => {
    const unexpired = new Date(Date.now() + 60 * 1000).toISOString()
    renderWithProviders(
      <AcceptInvitationScreen
        token="tok-1"
        invitations={operations({
          readInvitation: () =>
            Promise.resolve({ ...INVITATION, expiresAt: unexpired }),
        })}
      />,
    )
    expect(
      await screen.findByRole('button', { name: zhCN.acceptInvitation.accept }),
    ).toBeInTheDocument()
    expect(
      screen.queryByText(zhCN.acceptInvitation.unavailable.title),
    ).not.toBeInTheDocument()
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
    // The operation takes the token alone: the caller is the request's
    // signed-in identity, and acceptance never opens an account.
    expect(acceptInvitation).toHaveBeenCalledWith('tok-1')
    expect(
      await screen.findByText(zhCN.acceptInvitation.accepted.title),
    ).toBeInTheDocument()
    expect(onAccepted).toHaveBeenCalledTimes(1)
  })

  it('fills a status region that stood empty with the acceptance confirmation', async () => {
    // The confirmation's role="status" region must already exist and be
    // empty when the acceptance lands: a live region announces only
    // content changes that follow its own insertion, so a confirmation
    // born together with its region would be silent. The region stands
    // for the whole ready state, and the same node receives the text.
    const acceptInvitation = vi.fn(() => Promise.resolve())
    const user = userEvent.setup()
    renderWithProviders(
      <AcceptInvitationScreen
        token="tok-1"
        invitations={operations({ acceptInvitation })}
      />,
    )
    await screen.findByRole('button', { name: zhCN.acceptInvitation.accept })
    const region = screen.getByRole('status')
    expect(region).toBeEmptyDOMElement()
    await user.click(
      screen.getByRole('button', { name: zhCN.acceptInvitation.accept }),
    )
    await waitFor(() =>
      expect(region).toHaveTextContent(zhCN.acceptInvitation.accepted.title),
    )
    expect(region).toHaveTextContent(
      zhCN.acceptInvitation.accepted.description,
    )
    // The same node carried both frames: it was not replaced by one that
    // mounted together with the text.
    expect(screen.getByRole('status')).toBe(region)
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

  it('shows the proof step when the caller has not proved the invited email', async () => {
    const acceptInvitation = vi.fn(() => Promise.reject(UNPROVED))
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
    // The refusal is the action's own: the screen explains that the invited
    // address has to be proved, naming the address, and offers a new
    // acceptance for after the proof.
    expect(
      await screen.findByText(zhCN.acceptInvitation.unproved.title),
    ).toBeInTheDocument()
    expect(
      screen.getByText(
        zhCN.acceptInvitation.unproved.description.replace(
          '{{email}}',
          INVITATION.email,
        ),
      ),
    ).toBeInTheDocument()
    expect(
      screen.getByRole('button', {
        name: zhCN.acceptInvitation.unproved.retry,
      }),
    ).toBeInTheDocument()
    // The plain accept action is replaced, not stacked beside the step.
    expect(
      screen.queryByRole('button', { name: zhCN.acceptInvitation.accept }),
    ).not.toBeInTheDocument()
    // The refusal is not a whole-attempt error banner.
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    // The invitation stays readable: the same token can be tried again.
    expect(
      screen.getByText(
        zhCN.acceptInvitation.email.replace('{{email}}', INVITATION.email),
      ),
    ).toBeInTheDocument()
  })

  it('points at the host account surface from the proof step', async () => {
    const acceptInvitation = vi.fn(() => Promise.reject(UNPROVED))
    const onProveEmail = vi.fn()
    const user = userEvent.setup()
    renderWithProviders(
      <AcceptInvitationScreen
        token="tok-1"
        invitations={operations({ acceptInvitation })}
        onProveEmail={onProveEmail}
      />,
    )
    await user.click(
      await screen.findByRole('button', {
        name: zhCN.acceptInvitation.accept,
      }),
    )
    await user.click(
      await screen.findByRole('button', {
        name: zhCN.acceptInvitation.unproved.manageAccount,
      }),
    )
    expect(onProveEmail).toHaveBeenCalledTimes(1)
  })

  it('accepts again after the address is proved', async () => {
    const acceptInvitation = vi
      .fn()
      .mockRejectedValueOnce(UNPROVED)
      .mockResolvedValueOnce(undefined)
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
    await user.click(
      await screen.findByRole('button', {
        name: zhCN.acceptInvitation.unproved.retry,
      }),
    )
    expect(acceptInvitation).toHaveBeenCalledTimes(2)
    expect(
      await screen.findByText(zhCN.acceptInvitation.accepted.title),
    ).toBeInTheDocument()
    expect(onAccepted).toHaveBeenCalledTimes(1)
  })

  it('leaves a refusal that is not the unproved email to the banner', async () => {
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
    expect(await screen.findByRole('alert')).toHaveTextContent(
      zhCN.errors.authn.conflict,
    )
    expect(
      screen.queryByText(zhCN.acceptInvitation.unproved.title),
    ).not.toBeInTheDocument()
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

  it('has no axe violations in the proof step', async () => {
    const acceptInvitation = vi.fn(() => Promise.reject(UNPROVED))
    const user = userEvent.setup()
    renderWithProviders(
      <AcceptInvitationScreen
        token="tok-1"
        invitations={operations({ acceptInvitation })}
        onProveEmail={() => undefined}
      />,
    )
    await user.click(
      await screen.findByRole('button', {
        name: zhCN.acceptInvitation.accept,
      }),
    )
    await screen.findByText(zhCN.acceptInvitation.unproved.title)
    await expectNoAxeViolations()
  })

  it('renders the titles at the host’s heading level, the confirmation one below', async () => {
    const user = userEvent.setup()
    renderWithProviders(
      <AcceptInvitationScreen
        token="tok-1"
        invitations={operations()}
        headingLevel="h2"
      />,
    )
    expect(
      await screen.findByRole('heading', {
        level: 2,
        name: zhCN.acceptInvitation.title,
      }),
    ).toBeInTheDocument()
    await user.click(
      screen.getByRole('button', { name: zhCN.acceptInvitation.accept }),
    )
    // The confirmation continues the outline beneath the screen's title.
    expect(
      await screen.findByRole('heading', {
        level: 3,
        name: zhCN.acceptInvitation.accepted.title,
      }),
    ).toBeInTheDocument()
  })

  it('renders the proof step one heading level below the screen title', async () => {
    const acceptInvitation = vi.fn(() => Promise.reject(UNPROVED))
    const user = userEvent.setup()
    renderWithProviders(
      <AcceptInvitationScreen
        token="tok-1"
        invitations={operations({ acceptInvitation })}
        headingLevel="h2"
      />,
    )
    await user.click(
      await screen.findByRole('button', {
        name: zhCN.acceptInvitation.accept,
      }),
    )
    expect(
      await screen.findByRole('heading', {
        level: 3,
        name: zhCN.acceptInvitation.unproved.title,
      }),
    ).toBeInTheDocument()
  })

  it('renders the expiry in the host’s display time zone', async () => {
    const browserZone = Intl.DateTimeFormat().resolvedOptions().timeZone
    renderWithProviders(
      <DisplayTimeZoneProvider timeZone="Asia/Tokyo">
        <AcceptInvitationScreen token="tok-1" invitations={operations()} />
      </DisplayTimeZoneProvider>,
    )
    const formatter = (timeZone: string): string =>
      new Intl.DateTimeFormat('zh-CN', {
        dateStyle: 'medium',
        timeStyle: 'short',
        timeZone,
      }).format(new Date(INVITATION.expiresAt))
    const tokyo = formatter('Asia/Tokyo')
    const browser = formatter(browserZone)
    // The host's zone differs from the browser's, so the assertions below
    // discriminate the provider's effect: an implementation that ignored
    // it and fell back to the browser zone would render the other string.
    expect(tokyo).not.toBe(browser)
    expect(
      await screen.findByText(
        zhCN.acceptInvitation.expires.replace('{{expires}}', tokyo),
      ),
    ).toBeInTheDocument()
    expect(
      screen.queryByText(
        zhCN.acceptInvitation.expires.replace('{{expires}}', browser),
      ),
    ).not.toBeInTheDocument()
  })

  it('falls back to the browser zone and keeps the expiry line when the host’s display time zone is invalid', async () => {
    // The host's zone is unvalidated: a stale or misspelled IANA name
    // resolves through the shared chain to the browser's zone, so the
    // line still renders rather than disappearing. The suite's vitest
    // config pins a non-UTC browser zone: on a UTC runner this case would
    // also hold for an implementation that fell straight through to UTC.
    const browserZone = Intl.DateTimeFormat().resolvedOptions().timeZone
    expect(browserZone).not.toBe('UTC')
    const browserExpiry = new Intl.DateTimeFormat('zh-CN', {
      dateStyle: 'medium',
      timeStyle: 'short',
      timeZone: browserZone,
    }).format(new Date(INVITATION.expiresAt))
    renderWithProviders(
      <DisplayTimeZoneProvider timeZone="Asia/Shangai">
        <AcceptInvitationScreen token="tok-1" invitations={operations()} />
      </DisplayTimeZoneProvider>,
    )
    expect(
      await screen.findByText(
        zhCN.acceptInvitation.invited.replace('{{tenant}}', 'Acme'),
      ),
    ).toBeInTheDocument()
    expect(
      screen.getByText(
        zhCN.acceptInvitation.expires.replace('{{expires}}', browserExpiry),
      ),
    ).toBeInTheDocument()
  })

  it('omits the expiry line when the deadline carries no instant', async () => {
    renderWithProviders(
      <AcceptInvitationScreen
        token="tok-1"
        invitations={operations({
          readInvitation: () =>
            Promise.resolve({ ...INVITATION, expiresAt: 'not-a-timestamp' }),
        })}
      />,
    )
    expect(
      await screen.findByText(
        zhCN.acceptInvitation.invited.replace('{{tenant}}', 'Acme'),
      ),
    ).toBeInTheDocument()
    // The prefix is the whole line when the interpolated value is empty:
    // its presence means the line rendered without a deadline.
    expect(
      screen.queryByText(zhCN.acceptInvitation.expires.replace('{{expires}}', '')),
    ).not.toBeInTheDocument()
  })
})
