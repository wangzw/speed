import { describe, expect, it, vi } from 'vitest'
import { act, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { AcceptInvitationScreen } from './AcceptInvitationScreen.js'
import type { InvitationOperations } from './AcceptInvitationScreen.js'
import { renderWithProviders } from '../test-utils/render.js'
import { expectNoAxeViolations } from '../test-utils/axe.js'
import { DisplayTimeZoneProvider } from '@speed/ui-kit'
import zhCN from './locales/zh-CN.json' with { type: 'json' }
import enUS from './locales/en-US.json' with { type: 'json' }

const INVITATION = {
  tenant: 'Acme',
  tenantDisplayName: 'Acme',
  email: 'invitee@example.com',
  status: 'pending',
  expiresAt: new Date(Date.now() + 7 * 24 * 60 * 60 * 1000).toISOString(),
}

const UNPROVED = {
  status: 403,
  code: 'authn.invitation_email_unproved',
}

const UNAUTHORIZED = {
  status: 401,
  code: 'client.http.401',
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

const anonymousActions = {
  onSignIn: vi.fn(),
  onRegister: vi.fn(),
}

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((settle) => {
    resolve = settle
  })
  return { promise, resolve }
}

function invitedSentence(
  bundle: typeof zhCN | typeof enUS,
  tenant: string,
): string {
  return bundle.acceptInvitation.invited.replace('{{tenant}}', tenant)
}

describe('AcceptInvitationScreen', () => {
  it('renders the invitation and the host actions to an anonymous viewer', async () => {
    renderWithProviders(
      <AcceptInvitationScreen
        token="tok-1"
        invitations={operations()}
        {...anonymousActions}
      />,
    )
    expect(
      await screen.findByRole('heading', {
        name: zhCN.acceptInvitation.title,
      }),
    ).toBeInTheDocument()
    expect(
      screen.getByText(invitedSentence(zhCN, 'Acme')),
    ).toBeInTheDocument()
    expect(
      screen.getByText(
        zhCN.acceptInvitation.email.replace('{{email}}', 'invitee@example.com'),
      ),
    ).toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: zhCN.acceptInvitation.signIn }),
    ).toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: zhCN.acceptInvitation.createAccount }),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: zhCN.acceptInvitation.accept }),
    ).not.toBeInTheDocument()
  })

  it('reports the two host actions', async () => {
    const onSignIn = vi.fn()
    const onRegister = vi.fn()
    const user = userEvent.setup()
    renderWithProviders(
      <AcceptInvitationScreen
        token="tok-1"
        invitations={operations()}
        onSignIn={onSignIn}
        onRegister={onRegister}
      />,
    )
    await user.click(
      await screen.findByRole('button', { name: zhCN.acceptInvitation.signIn }),
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.acceptInvitation.createAccount }),
    )
    expect(onSignIn).toHaveBeenCalledTimes(1)
    expect(onRegister).toHaveBeenCalledTimes(1)
  })

  it('names the inviter and the invited role when the invitation carries them', async () => {
    renderWithProviders(
      <AcceptInvitationScreen
        token="tok-1"
        invitations={operations({
          readInvitation: () =>
            Promise.resolve({
              ...INVITATION,
              invitedByName: 'Ada Lovelace',
              role: 'tenant-admin',
            }),
        })}
      />,
    )
    expect(
      await screen.findByText(
        zhCN.acceptInvitation.invitedByWithRole
          .replace('{{inviter}}', 'Ada Lovelace')
          .replace('{{role}}', 'tenant-admin')
          .replace('{{tenant}}', 'Acme'),
      ),
    ).toBeInTheDocument()
  })

  it('omits the role when the invitation carries none', async () => {
    renderWithProviders(
      <AcceptInvitationScreen
        token="tok-1"
        invitations={operations({
          readInvitation: () =>
            Promise.resolve({ ...INVITATION, invitedByName: 'Ada Lovelace' }),
        })}
      />,
    )
    expect(
      await screen.findByText(
        zhCN.acceptInvitation.invitedBy
          .replace('{{inviter}}', 'Ada Lovelace')
          .replace('{{tenant}}', 'Acme'),
      ),
    ).toBeInTheDocument()
  })

  it('shows the proof step on arrival when the signed-in caller has not proved the address', async () => {
    renderWithProviders(
      <AcceptInvitationScreen
        token="tok-1"
        invitations={operations({
          readEligibility: () =>
            Promise.resolve({ state: 'pending', emailProved: false }),
        })}
        signedIn={true}
      />,
    )
    expect(
      await screen.findByText(zhCN.acceptInvitation.unproved.title),
    ).toBeInTheDocument()
    expect(
      screen.getByText(invitedSentence(zhCN, 'Acme')),
    ).toBeInTheDocument()
    expect(
      screen.getByText(
        zhCN.acceptInvitation.email.replace('{{email}}', INVITATION.email),
      ),
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
      screen.queryByRole('button', { name: zhCN.acceptInvitation.accept }),
    ).not.toBeInTheDocument()
  })

  it('offers the accept action when the signed-in caller proved the address', async () => {
    renderWithProviders(
      <AcceptInvitationScreen
        token="tok-1"
        invitations={operations({
          readEligibility: () =>
            Promise.resolve({ state: 'pending', emailProved: true }),
        })}
        signedIn={true}
      />,
    )
    expect(
      await screen.findByRole('button', { name: zhCN.acceptInvitation.accept }),
    ).toBeInTheDocument()
    expect(
      screen.queryByText(zhCN.acceptInvitation.unproved.title),
    ).not.toBeInTheDocument()
  })

  it('treats a 401 from the eligibility read as an anonymous visitor', async () => {
    renderWithProviders(
      <AcceptInvitationScreen
        token="tok-1"
        invitations={operations({
          readEligibility: () => Promise.reject(UNAUTHORIZED),
        })}
        signedIn={true}
        {...anonymousActions}
      />,
    )
    expect(
      await screen.findByRole('button', { name: zhCN.acceptInvitation.signIn }),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: zhCN.acceptInvitation.accept }),
    ).not.toBeInTheDocument()
  })

  it('renders the terminal state the eligibility read judges for a pending link', async () => {
    renderWithProviders(
      <AcceptInvitationScreen
        token="tok-1"
        invitations={operations({
          readEligibility: () =>
            Promise.resolve({ state: 'revoked', emailProved: true }),
        })}
        signedIn={true}
      />,
    )
    expect(
      await screen.findByText(zhCN.acceptInvitation.revoked.title),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: zhCN.acceptInvitation.accept }),
    ).not.toBeInTheDocument()
  })

  it('renders the expired state with the inviter remedy', async () => {
    renderWithProviders(
      <AcceptInvitationScreen
        token="tok-1"
        invitations={operations({
          readInvitation: () =>
            Promise.resolve({ ...INVITATION, status: 'expired' }),
        })}
      />,
    )
    expect(
      await screen.findByText(zhCN.acceptInvitation.expired.title),
    ).toBeInTheDocument()
    expect(
      screen.getByText(zhCN.acceptInvitation.expired.description),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: zhCN.acceptInvitation.accept }),
    ).not.toBeInTheDocument()
  })

  it('renders the revoked state', async () => {
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
      await screen.findByText(zhCN.acceptInvitation.revoked.title),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: zhCN.acceptInvitation.accept }),
    ).not.toBeInTheDocument()
  })

  it('trusts the server state instead of judging expiry from the local clock', async () => {
    const past = new Date(Date.now() - 60 * 1000).toISOString()
    renderWithProviders(
      <AcceptInvitationScreen
        token="tok-1"
        invitations={operations({
          readInvitation: () =>
            Promise.resolve({ ...INVITATION, expiresAt: past }),
        })}
        signedIn={true}
      />,
    )
    expect(
      await screen.findByRole('button', { name: zhCN.acceptInvitation.accept }),
    ).toBeInTheDocument()
    expect(
      screen.queryByText(zhCN.acceptInvitation.expired.title),
    ).not.toBeInTheDocument()
  })

  it('offers the organization to a member who opens an accepted link', async () => {
    const onOpenTenant = vi.fn()
    const user = userEvent.setup()
    renderWithProviders(
      <AcceptInvitationScreen
        token="tok-1"
        invitations={operations({
          readInvitation: () =>
            Promise.resolve({ ...INVITATION, status: 'accepted' }),
        })}
        signedIn={true}
        isMemberOf={(tenant) => tenant === INVITATION.tenant}
        onOpenTenant={onOpenTenant}
      />,
    )
    expect(
      await screen.findByText(
        zhCN.acceptInvitation.member.title.replace('{{tenant}}', 'Acme'),
      ),
    ).toBeInTheDocument()
    await user.click(
      screen.getByRole('button', { name: zhCN.acceptInvitation.member.open }),
    )
    expect(onOpenTenant).toHaveBeenCalledWith(INVITATION.tenant)
  })

  it('falls back to the organization name when it set no display name', async () => {
    renderWithProviders(
      <AcceptInvitationScreen
        token="tok-1"
        invitations={operations({
          readInvitation: () =>
            Promise.resolve({
              ...INVITATION,
              tenant: 'acme',
              tenantDisplayName: '',
              status: 'accepted',
            }),
        })}
        signedIn={true}
        isMemberOf={() => true}
        onOpenTenant={() => undefined}
      />,
    )
    expect(
      await screen.findByText(
        zhCN.acceptInvitation.member.title.replace('{{tenant}}', 'acme'),
      ),
    ).toBeInTheDocument()
  })

  it('waits for the membership fact instead of saying an accepted link was used', async () => {
    let resolveRead: ((value: typeof INVITATION) => void) | undefined
    const readInvitation = vi.fn(
      () =>
        new Promise<typeof INVITATION>((resolve) => {
          resolveRead = resolve
        }),
    )
    renderWithProviders(
      <AcceptInvitationScreen
        token="tok-1"
        invitations={operations({ readInvitation })}
        signedIn={true}
        isMemberOf={() => undefined}
        onOpenTenant={() => undefined}
      />,
    )
    await waitFor(() => expect(resolveRead).toBeDefined())
    await act(() => {
      resolveRead?.({ ...INVITATION, status: 'accepted' })
    })
    expect(
      await screen.findByText(zhCN.acceptInvitation.loading),
    ).toBeInTheDocument()
    expect(
      screen.queryByText(zhCN.acceptInvitation.used.title),
    ).not.toBeInTheDocument()
  })

  it('says an accepted link was used to an anonymous viewer without waiting', async () => {
    renderWithProviders(
      <AcceptInvitationScreen
        token="tok-1"
        invitations={operations({
          readInvitation: () =>
            Promise.resolve({ ...INVITATION, status: 'accepted' }),
        })}
        isMemberOf={() => undefined}
      />,
    )
    expect(
      await screen.findByText(zhCN.acceptInvitation.used.title),
    ).toBeInTheDocument()
  })

  it('says the invitation was used when the accepted link is not this session’s organization', async () => {
    renderWithProviders(
      <AcceptInvitationScreen
        token="tok-1"
        invitations={operations({
          readInvitation: () =>
            Promise.resolve({ ...INVITATION, status: 'accepted' }),
        })}
        signedIn={true}
        isMemberOf={() => false}
      />,
    )
    expect(
      await screen.findByText(zhCN.acceptInvitation.used.title),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: zhCN.acceptInvitation.member.open }),
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
        signedIn={true}
        onAccepted={onAccepted}
      />,
    )
    await user.click(
      await screen.findByRole('button', {
        name: zhCN.acceptInvitation.accept,
      }),
    )
    expect(acceptInvitation).toHaveBeenCalledWith('tok-1')
    expect(
      await screen.findByText(zhCN.acceptInvitation.accepted.title),
    ).toBeInTheDocument()
    expect(onAccepted).toHaveBeenCalledTimes(1)
  })

  it('keeps the accept control focused and marked busy while the acceptance runs', async () => {
    const pending = deferred<void>()
    const acceptInvitation = vi.fn(() => pending.promise)
    const user = userEvent.setup()
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
    await user.click(accept)
    expect(accept).toHaveFocus()
    expect(accept).toHaveAttribute('aria-disabled', 'true')
    expect(accept).toHaveAttribute('aria-busy', 'true')
    expect(acceptInvitation).toHaveBeenCalledTimes(1)
    await user.click(accept)
    await user.keyboard('{Enter}')
    await user.keyboard(' ')
    expect(acceptInvitation).toHaveBeenCalledTimes(1)
    await act(async () => {
      pending.resolve()
    })
    expect(
      await screen.findByText(zhCN.acceptInvitation.accepted.title),
    ).toBeInTheDocument()
  })

  it('fills a status region that stood empty with the acceptance confirmation', async () => {
    const acceptInvitation = vi.fn(() => Promise.resolve())
    const user = userEvent.setup()
    renderWithProviders(
      <AcceptInvitationScreen
        token="tok-1"
        invitations={operations({ acceptInvitation })}
        signedIn={true}
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
    expect(screen.getByRole('status')).toBe(region)
  })

  it('moves focus to the completion heading when the acceptance lands', async () => {
    const pending = deferred<void>()
    const acceptInvitation = vi.fn(() => pending.promise)
    const user = userEvent.setup()
    renderWithProviders(
      <AcceptInvitationScreen
        token="tok-1"
        invitations={operations({ acceptInvitation })}
        signedIn={true}
      />,
    )
    await user.click(
      await screen.findByRole('button', {
        name: zhCN.acceptInvitation.accept,
      }),
    )
    await act(async () => {
      pending.resolve()
    })
    const heading = await screen.findByRole('heading', {
      name: zhCN.acceptInvitation.accepted.title,
    })
    await waitFor(() => expect(heading).toHaveFocus())
    expect(heading).toHaveAttribute('tabindex', '-1')
  })

  it('leaves focus where the operator moved it when the acceptance lands', async () => {
    const pending = deferred<void>()
    const acceptInvitation = vi.fn(() => pending.promise)
    const user = userEvent.setup()
    renderWithProviders(
      <>
        <AcceptInvitationScreen
          token="tok-1"
          invitations={operations({ acceptInvitation })}
          signedIn={true}
        />
        <button type="button">Elsewhere</button>
      </>,
    )
    await user.click(
      await screen.findByRole('button', {
        name: zhCN.acceptInvitation.accept,
      }),
    )
    const elsewhere = screen.getByRole('button', { name: 'Elsewhere' })
    elsewhere.focus()
    expect(elsewhere).toHaveFocus()
    await act(async () => {
      pending.resolve()
    })
    const heading = await screen.findByRole('heading', {
      name: zhCN.acceptInvitation.accepted.title,
    })
    expect(elsewhere).toHaveFocus()
    expect(heading).not.toHaveFocus()
  })

  it('re-reads a 409 refusal and renders the terminal state instead of the refusal sentence', async () => {
    let reads = 0
    const readInvitation = vi.fn(() => {
      reads += 1
      return Promise.resolve(
        reads === 1
          ? INVITATION
          : { ...INVITATION, status: 'accepted' as const },
      )
    })
    const acceptInvitation = vi.fn(() =>
      Promise.reject({ status: 409, code: 'authn.invitation_terminal' }),
    )
    const user = userEvent.setup()
    renderWithProviders(
      <AcceptInvitationScreen
        token="tok-1"
        invitations={operations({ readInvitation, acceptInvitation })}
        signedIn={true}
        isMemberOf={() => false}
      />,
    )
    await user.click(
      await screen.findByRole('button', {
        name: zhCN.acceptInvitation.accept,
      }),
    )
    expect(
      await screen.findByText(zhCN.acceptInvitation.used.title),
    ).toBeInTheDocument()
    expect(readInvitation).toHaveBeenCalledTimes(2)
    expect(
      screen.queryByText(zhCN.errors.authn.invitation_terminal),
    ).not.toBeInTheDocument()
  })

  it('explains a disabled organization instead of silently re-reading the link', async () => {
    const readInvitation = vi.fn(() => Promise.resolve(INVITATION))
    const acceptInvitation = vi.fn(() =>
      Promise.reject({
        status: 409,
        code: 'authn.invitation_tenant_inactive',
      }),
    )
    const user = userEvent.setup()
    renderWithProviders(
      <AcceptInvitationScreen
        token="tok-1"
        invitations={operations({ readInvitation, acceptInvitation })}
        signedIn={true}
      />,
    )
    await user.click(
      await screen.findByRole('button', {
        name: zhCN.acceptInvitation.accept,
      }),
    )
    expect(
      await screen.findByText(zhCN.acceptInvitation.tenantInactive.title),
    ).toBeInTheDocument()
    expect(
      screen.getByText(zhCN.acceptInvitation.tenantInactive.description),
    ).toBeInTheDocument()
    expect(readInvitation).toHaveBeenCalledTimes(1)
    expect(
      screen.getByRole('button', { name: zhCN.acceptInvitation.accept }),
    ).not.toHaveAttribute('aria-disabled', 'true')
    expect(
      screen.queryByText(zhCN.errors.authn.invitation_terminal),
    ).not.toBeInTheDocument()
  })

  it('keeps the suspended-membership text instead of re-reading into a revoked link', async () => {
    let reads = 0
    const readInvitation = vi.fn(() => {
      reads += 1
      return Promise.resolve(
        reads === 1
          ? INVITATION
          : { ...INVITATION, status: 'revoked' as const },
      )
    })
    const acceptInvitation = vi.fn(() =>
      Promise.reject({ status: 409, code: 'authn.membership_inactive' }),
    )
    const user = userEvent.setup()
    renderWithProviders(
      <AcceptInvitationScreen
        token="tok-1"
        invitations={operations({ readInvitation, acceptInvitation })}
        signedIn={true}
      />,
    )
    await user.click(
      await screen.findByRole('button', {
        name: zhCN.acceptInvitation.accept,
      }),
    )
    expect(await screen.findByRole('alert')).toHaveTextContent(
      zhCN.errors.authn.membership_inactive,
    )
    expect(
      screen.queryByText(zhCN.acceptInvitation.revoked.title),
    ).not.toBeInTheDocument()
    expect(readInvitation).toHaveBeenCalledTimes(1)
  })

  it('keeps the refusal visible when a 409 re-read still answers pending', async () => {
    const readInvitation = vi.fn(() => Promise.resolve(INVITATION))
    const acceptInvitation = vi.fn(() =>
      Promise.reject({ status: 409, code: 'authn.invitation_terminal' }),
    )
    const user = userEvent.setup()
    renderWithProviders(
      <AcceptInvitationScreen
        token="tok-1"
        invitations={operations({ readInvitation, acceptInvitation })}
        signedIn={true}
      />,
    )
    await user.click(
      await screen.findByRole('button', {
        name: zhCN.acceptInvitation.accept,
      }),
    )
    expect(await screen.findByRole('alert')).toHaveTextContent(
      zhCN.errors.authn.invitation_terminal,
    )
    expect(readInvitation).toHaveBeenCalledTimes(2)
  })

  it('renders the dedicated text when a non-user account is refused', async () => {
    const acceptInvitation = vi.fn(() =>
      Promise.reject({ status: 403, code: 'authn.invitation_target_invalid' }),
    )
    const user = userEvent.setup()
    renderWithProviders(
      <AcceptInvitationScreen
        token="tok-1"
        invitations={operations({ acceptInvitation })}
        signedIn={true}
      />,
    )
    await user.click(
      await screen.findByRole('button', {
        name: zhCN.acceptInvitation.accept,
      }),
    )
    const banner = await screen.findByRole('alert')
    expect(banner).toHaveTextContent(
      zhCN.errors.authn.invitation_target_invalid,
    )
    expect(banner).not.toHaveTextContent(zhCN.errors.unknown)
  })

  it('reports a refused acceptance with the answer code and stays retryable', async () => {
    const acceptInvitation = vi.fn(() =>
      Promise.reject({ status: 503, code: 'authn.unavailable' }),
    )
    const user = userEvent.setup()
    renderWithProviders(
      <AcceptInvitationScreen
        token="tok-1"
        invitations={operations({ acceptInvitation })}
        signedIn={true}
      />,
    )
    await user.click(
      await screen.findByRole('button', {
        name: zhCN.acceptInvitation.accept,
      }),
    )
    expect(
      await screen.findByText(zhCN.errors.authn.unavailable),
    ).toBeInTheDocument()
    const accept = screen.getByRole('button', {
      name: zhCN.acceptInvitation.accept,
    })
    expect(accept).toHaveFocus()
    expect(accept).not.toHaveAttribute('aria-disabled', 'true')
  })

  it('shows the proof step when the caller has not proved the invited email', async () => {
    const acceptInvitation = vi.fn(() => Promise.reject(UNPROVED))
    const user = userEvent.setup()
    renderWithProviders(
      <AcceptInvitationScreen
        token="tok-1"
        invitations={operations({ acceptInvitation })}
        signedIn={true}
      />,
    )
    await user.click(
      await screen.findByRole('button', {
        name: zhCN.acceptInvitation.accept,
      }),
    )
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
    expect(
      screen.queryByRole('button', { name: zhCN.acceptInvitation.accept }),
    ).not.toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('moves focus to the proof step’s first control when it replaces the accept action', async () => {
    const acceptInvitation = vi.fn(() => Promise.reject(UNPROVED))
    const onProveEmail = vi.fn()
    const user = userEvent.setup()
    renderWithProviders(
      <AcceptInvitationScreen
        token="tok-1"
        invitations={operations({ acceptInvitation })}
        signedIn={true}
        onProveEmail={onProveEmail}
      />,
    )
    await user.click(
      await screen.findByRole('button', {
        name: zhCN.acceptInvitation.accept,
      }),
    )
    const manage = await screen.findByRole('button', {
      name: zhCN.acceptInvitation.unproved.manageAccount,
    })
    await waitFor(() => expect(manage).toHaveFocus())
  })

  it('points at the host account surface from the proof step', async () => {
    const acceptInvitation = vi.fn(() => Promise.reject(UNPROVED))
    const onProveEmail = vi.fn()
    const user = userEvent.setup()
    renderWithProviders(
      <AcceptInvitationScreen
        token="tok-1"
        invitations={operations({ acceptInvitation })}
        signedIn={true}
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
        signedIn={true}
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

  it('holds the manage-account action back without a reason while an acceptance runs', async () => {
    const pending = deferred<void>()
    const acceptInvitation = vi.fn(() => pending.promise)
    const onProveEmail = vi.fn()
    const user = userEvent.setup()
    renderWithProviders(
      <AcceptInvitationScreen
        token="tok-1"
        invitations={operations({
          readEligibility: () =>
            Promise.resolve({ state: 'pending', emailProved: false }),
          acceptInvitation,
        })}
        signedIn={true}
        onProveEmail={onProveEmail}
      />,
    )
    const retry = await screen.findByRole('button', {
      name: zhCN.acceptInvitation.unproved.retry,
    })
    const manage = screen.getByRole('button', {
      name: zhCN.acceptInvitation.unproved.manageAccount,
    })
    await user.click(retry)
    expect(retry).toHaveFocus()
    expect(retry).toHaveAttribute('aria-disabled', 'true')
    expect(retry).toHaveAttribute('aria-busy', 'true')

    expect(manage).toHaveAttribute('aria-disabled', 'true')
    expect(manage).not.toHaveAttribute('aria-busy', 'true')
    expect(manage).not.toHaveAccessibleDescription()

    manage.focus()
    expect(manage).toHaveFocus()
    await user.click(manage)
    await user.keyboard('{Enter}')
    await user.keyboard(' ')
    expect(onProveEmail).not.toHaveBeenCalled()

    await act(async () => {
      pending.resolve()
    })
    expect(
      await screen.findByText(zhCN.acceptInvitation.accepted.title),
    ).toBeInTheDocument()
  })

  it('reports a failed retry from the proof step instead of staying silent', async () => {
    const acceptInvitation = vi.fn(() =>
      Promise.reject({ status: 429, code: 'authn.throttled' }),
    )
    const user = userEvent.setup()
    renderWithProviders(
      <AcceptInvitationScreen
        token="tok-1"
        invitations={operations({
          readEligibility: () =>
            Promise.resolve({ state: 'pending', emailProved: false }),
          acceptInvitation,
        })}
        signedIn={true}
      />,
    )
    await user.click(
      await screen.findByRole('button', {
        name: zhCN.acceptInvitation.unproved.retry,
      }),
    )
    expect(await screen.findByRole('alert')).toHaveTextContent(
      zhCN.errors.authn.throttled,
    )
    expect(
      screen.getByText(zhCN.acceptInvitation.unproved.title),
    ).toBeInTheDocument()
  })

  it('reports a 503 and a network failure from the proof step', async () => {
    const acceptInvitation = vi
      .fn()
      .mockRejectedValueOnce({ status: 503, code: 'authn.unavailable' })
      .mockRejectedValueOnce({ code: 'client.network' })
    const user = userEvent.setup()
    renderWithProviders(
      <AcceptInvitationScreen
        token="tok-1"
        invitations={operations({
          readEligibility: () =>
            Promise.resolve({ state: 'pending', emailProved: false }),
          acceptInvitation,
        })}
        signedIn={true}
      />,
    )
    const retry = await screen.findByRole('button', {
      name: zhCN.acceptInvitation.unproved.retry,
    })
    await user.click(retry)
    expect(await screen.findByRole('alert')).toHaveTextContent(
      zhCN.errors.authn.unavailable,
    )
    expect(
      screen.getByText(zhCN.acceptInvitation.unproved.title),
    ).toBeInTheDocument()

    await user.click(retry)
    expect(await screen.findByRole('alert')).toHaveTextContent(
      zhCN.errors.client.network,
    )
    expect(
      screen.getByText(zhCN.acceptInvitation.unproved.title),
    ).toBeInTheDocument()
  })

  it('explains a disabled organization from the proof step', async () => {
    const acceptInvitation = vi.fn(() =>
      Promise.reject({
        status: 409,
        code: 'authn.invitation_tenant_inactive',
      }),
    )
    const user = userEvent.setup()
    renderWithProviders(
      <AcceptInvitationScreen
        token="tok-1"
        invitations={operations({
          readEligibility: () =>
            Promise.resolve({ state: 'pending', emailProved: false }),
          acceptInvitation,
        })}
        signedIn={true}
      />,
    )
    await user.click(
      await screen.findByRole('button', {
        name: zhCN.acceptInvitation.unproved.retry,
      }),
    )
    expect(
      await screen.findByText(zhCN.acceptInvitation.tenantInactive.title),
    ).toBeInTheDocument()
    expect(
      screen.getByText(zhCN.acceptInvitation.tenantInactive.description),
    ).toBeInTheDocument()
  })

  it('leaves a refusal that is not the unproved email to the banner', async () => {
    const acceptInvitation = vi.fn(() =>
      Promise.reject({ status: 500, code: 'authn.unavailable' }),
    )
    const user = userEvent.setup()
    renderWithProviders(
      <AcceptInvitationScreen
        token="tok-1"
        invitations={operations({ acceptInvitation })}
        signedIn={true}
      />,
    )
    await user.click(
      await screen.findByRole('button', {
        name: zhCN.acceptInvitation.accept,
      }),
    )
    expect(await screen.findByRole('alert')).toHaveTextContent(
      zhCN.errors.authn.unavailable,
    )
    expect(
      screen.queryByText(zhCN.acceptInvitation.unproved.title),
    ).not.toBeInTheDocument()
  })

  it('renders the invalid-link text and the most-recent-email hint for a bare 404', async () => {
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
    expect(
      screen.getByText(zhCN.acceptInvitation.missing.hint),
    ).toBeInTheDocument()
  })

  it('renders the unavailable text for a 409 read answer', async () => {
    renderWithProviders(
      <AcceptInvitationScreen
        token="tok-1"
        invitations={operations({
          readInvitation: () =>
            Promise.reject({ status: 409, code: 'authn.invitation_terminal' }),
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
      <AcceptInvitationScreen
        token="tok-1"
        invitations={operations()}
        {...anonymousActions}
      />,
      { language: 'en-US' },
    )
    expect(
      await screen.findByText(invitedSentence(enUS, 'Acme')),
    ).toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: enUS.acceptInvitation.signIn }),
    ).toBeInTheDocument()
  })

  it('has no axe violations on the anonymous landing', async () => {
    renderWithProviders(
      <AcceptInvitationScreen
        token="tok-1"
        invitations={operations()}
        {...anonymousActions}
      />,
    )
    await screen.findByRole('button', {
      name: zhCN.acceptInvitation.signIn,
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
        signedIn={true}
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
        signedIn={true}
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
        signedIn={true}
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
    renderWithProviders(
      <DisplayTimeZoneProvider timeZone="Asia/Shanghai">
        <AcceptInvitationScreen
          token="tok-1"
          invitations={operations()}
          signedIn={true}
        />
      </DisplayTimeZoneProvider>,
    )
    const formatter = (timeZone: string): string =>
      new Intl.DateTimeFormat('zh-CN', {
        dateStyle: 'medium',
        timeStyle: 'short',
        timeZone,
      }).format(new Date(INVITATION.expiresAt))
    const shanghai = formatter('Asia/Shanghai')
    const utc = formatter('UTC')
    expect(shanghai).not.toBe(utc)
    expect(
      await screen.findByText(
        zhCN.acceptInvitation.expires.replace('{{expires}}', shanghai),
      ),
    ).toBeInTheDocument()
    expect(
      screen.queryByText(
        zhCN.acceptInvitation.expires.replace('{{expires}}', utc),
      ),
    ).not.toBeInTheDocument()
  })

  it('omits the expiry line when the host’s display time zone is invalid', async () => {
    renderWithProviders(
      <DisplayTimeZoneProvider timeZone="Asia/Shangai">
        <AcceptInvitationScreen
          token="tok-1"
          invitations={operations()}
          signedIn={true}
        />
      </DisplayTimeZoneProvider>,
    )
    expect(
      await screen.findByText(invitedSentence(zhCN, 'Acme')),
    ).toBeInTheDocument()
    const emptyExpiry = zhCN.acceptInvitation.expires.replace('{{expires}}', '')
    expect(screen.queryByText(emptyExpiry)).not.toBeInTheDocument()
  })
})
