/**
 * WriteFeedbackRegion: the console's binding of the shared region.
 *
 * The region itself is ui-kit's; this binding exists so a failure code
 * resolves through the admin-ui namespace's whitelist and nothing else.
 * The cases below drive a real scope slot: a whitelisted code renders its
 * own text, while a plain throw collapses through `errorCodeOf` to a code
 * no resolver lists and renders the shared unknown fallback -- never a
 * raw key.
 */

import { describe, expect, it, vi } from 'vitest'
import { act, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { getAuthnMfaFactorsQueryKey } from '@speed/api-sdk'
import { useWriteFeedback } from '@speed/ui-kit'
import { WriteFeedbackRegion } from './write-feedback.js'
import { AdminAccountHrefProvider } from './account-href.js'
import { renderWithProviders } from '../../test-utils/render.js'
import { bindAdminRequest } from '../../test-utils/request-rig.js'
import zhCN from '../locales/zh-CN.json' with { type: 'json' }

/** The success message the scripted attempt settles; the caller owns this copy. */
const SUCCESS = 'Saved'

/** One active factor as the caller's own factor read answers it. */
const ACTIVE_FACTOR = {
  id: 'factor-1',
  kind: 'totp',
  label: 'Authenticator app',
  status: 'active',
  created_at: '2026-01-01T00:00:00Z',
}

/** Binds the refusal exit's factor read; any other request leg is a defect. */
function bindFactors(factors: readonly unknown[]): void {
  bindAdminRequest((call) => {
    if (call.method === 'GET' && call.path === '/api/v1/auth/factors') {
      return { factors }
    }
    throw new Error(`unexpected ${call.method} ${call.path}`)
  })
}

/** Waits until the refusal exit's factor read has settled. */
async function awaitFactorRead(
  queryClient: ReturnType<typeof renderWithProviders>['queryClient'],
): Promise<void> {
  await waitFor(() => {
    expect(
      queryClient.getQueryState(getAuthnMfaFactorsQueryKey())?.status,
    ).toBe('success')
  })
}

/** A probe that settles the scope's slot with a scripted attempt. */
function Probe({
  fail,
  mfaRelogin,
}: {
  readonly fail: () => Promise<void>
  readonly mfaRelogin?: () => void
}) {
  const feedback = useWriteFeedback()
  return (
    <div>
      <button
        type="button"
        onClick={() => {
          void feedback.run(async () => {
            await fail()
            return SUCCESS
          })
        }}
      >
        write
      </button>
      <WriteFeedbackRegion
        outcome={feedback.outcome}
        mfaRelogin={mfaRelogin}
      />
    </div>
  )
}

/** A probe that runs two attempts the test settles by hand. */
function OverlapProbe({
  first,
  second,
}: {
  readonly first: () => Promise<string | null>
  readonly second: () => Promise<string | null>
}) {
  const feedback = useWriteFeedback()
  return (
    <div>
      <button type="button" onClick={() => void feedback.run(first)}>
        first
      </button>
      <button type="button" onClick={() => void feedback.run(second)}>
        second
      </button>
      <WriteFeedbackRegion outcome={feedback.outcome} />
    </div>
  )
}

describe('WriteFeedbackRegion', () => {
  it('resolves a whitelisted failure code through the console namespace', async () => {
    const user = userEvent.setup()
    renderWithProviders(
      <Probe fail={() => Promise.reject({ code: 'authz.forbidden' })} />,
    )
    await user.click(screen.getByRole('button', { name: 'write' }))
    expect(await screen.findByRole('alert')).toHaveTextContent(
      zhCN.errors.authz.forbidden,
    )
  })

  it('renders the unknown fallback for a plain throw, never a raw key', async () => {
    const user = userEvent.setup()
    renderWithProviders(<Probe fail={() => Promise.reject(new Error('boom'))} />)
    await user.click(screen.getByRole('button', { name: 'write' }))
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.errors.unknown)
    // `errorCodeOf` collapses the throw to client.unknown; the resolver
    // carries no leaf for it, so the fallback stands in -- the raw code
    // must never reach the banner.
    expect(alert).not.toHaveTextContent('client.unknown')
  })

  it('announces a settled success in the polite live region', async () => {
    const user = userEvent.setup()
    renderWithProviders(<Probe fail={() => Promise.resolve()} />)
    await user.click(screen.getByRole('button', { name: 'write' }))
    expect(await screen.findByText(SUCCESS)).toBeInTheDocument()
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('keeps the attempt that settles last and leaves the slot empty on null', async () => {
    const user = userEvent.setup()
    let settleFirst!: (message: string | null) => void
    let settleSecond!: (message: string | null) => void
    const first = () =>
      new Promise<string | null>((resolve) => {
        settleFirst = resolve
      })
    const second = () =>
      new Promise<string | null>((resolve) => {
        settleSecond = resolve
      })
    renderWithProviders(<OverlapProbe first={first} second={second} />)

    await user.click(screen.getByRole('button', { name: 'first' }))
    await user.click(screen.getByRole('button', { name: 'second' }))
    // The second attempt settles first; the first settles last and wins.
    settleSecond('second message')
    expect(await screen.findByText('second message')).toBeInTheDocument()
    settleFirst('first message')
    expect(await screen.findByText('first message')).toBeInTheDocument()
    expect(screen.queryByText('second message')).toBeNull()

    // A null settlement -- the elevation gate took the write over -- does
    // not touch the slot: the attempt's own reset leaves it empty.
    await user.click(screen.getByRole('button', { name: 'first' }))
    // Flush the settled attempt before reading the slot: the attempt's own
    // reset empties it at the start, so an assertion made too early cannot
    // tell an empty slot from one a null settlement wrongly filled.
    await act(async () => {
      settleFirst(null)
      await new Promise((resolve) => setTimeout(resolve, 0))
    })
    expect(screen.queryByText('first message')).toBeNull()
    expect(screen.queryByRole('alert')).toBeNull()
    // The slot is empty, not merely silent about this attempt: the standing
    // polite region holds no message at all.
    expect(screen.getByRole('status')).toHaveTextContent('')
  })

  it('renders the sign-in exit for a multi-factor refusal when the account has a factor', async () => {
    bindFactors([ACTIVE_FACTOR])
    const user = userEvent.setup()
    const mfaRelogin = vi.fn()
    const { queryClient } = renderWithProviders(
      <Probe
        fail={() =>
          Promise.reject({
            status: 401,
            code: 'authn.insufficient_user_authentication',
          })
        }
        mfaRelogin={mfaRelogin}
      />,
    )
    await user.click(screen.getByRole('button', { name: 'write' }))
    expect(await screen.findByRole('alert')).toHaveTextContent(
      zhCN.errors.authn.insufficient_user_authentication,
    )
    // The settled factor read says the account can raise its session with a
    // fresh sign-in, so the write's exit matches the read's.
    await awaitFactorRead(queryClient)
    expect(screen.queryByText(zhCN.stepUp.mfaEnrollRequired)).toBeNull()
    await user.click(
      screen.getByRole('button', { name: zhCN.stepUp.relogin }),
    )
    expect(mfaRelogin).toHaveBeenCalledTimes(1)
  })

  it('names the missing second factor and points at enrollment when the account has none', async () => {
    bindFactors([])
    const user = userEvent.setup()
    const { queryClient } = renderWithProviders(
      <AdminAccountHrefProvider accountHref="/account/security">
        <Probe
          fail={() =>
            Promise.reject({
              status: 401,
              code: 'authn.insufficient_user_authentication',
            })
          }
          mfaRelogin={() => {}}
        />
      </AdminAccountHrefProvider>,
    )
    await user.click(screen.getByRole('button', { name: 'write' }))
    await awaitFactorRead(queryClient)
    // The code's own sentence would tell the operator to sign in again,
    // which cannot add a factor; the alert carries the enrollment guidance
    // and the region points at the host's account settings.
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.stepUp.mfaEnrollRequired)
    expect(alert).not.toHaveTextContent(
      zhCN.errors.authn.insufficient_user_authentication,
    )
    expect(screen.queryByRole('button', { name: zhCN.stepUp.relogin })).toBeNull()
    expect(
      screen.getByRole('link', { name: zhCN.stepUp.goToAccount }),
    ).toHaveAttribute('href', '/account/security')
  })

  it('renders the no-factor guidance as text when the host supplies no account destination', async () => {
    bindFactors([])
    const user = userEvent.setup()
    const { queryClient } = renderWithProviders(
      <Probe
        fail={() =>
          Promise.reject({
            status: 401,
            code: 'authn.insufficient_user_authentication',
          })
        }
        mfaRelogin={() => {}}
      />,
    )
    await user.click(screen.getByRole('button', { name: 'write' }))
    await awaitFactorRead(queryClient)
    expect(await screen.findByRole('alert')).toHaveTextContent(
      zhCN.stepUp.mfaEnrollRequired,
    )
    // The package holds no host path of its own.
    expect(screen.queryAllByRole('link')).toHaveLength(0)
  })
})
