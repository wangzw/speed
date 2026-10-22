/**
 * The persistent email-verification notice under the app bar.
 *
 * It renders while GET /api/v1/auth/me's `verification.state` is pending,
 * names the target address, links to the verification page for both the
 * verify and change actions, and carries no dismiss control. A verified
 * account, and an account with no unproved email, shows no notice.
 */

import { act, screen, waitFor, within } from '@testing-library/react'
import { beforeEach, describe, expect, it } from 'vitest'
import { getAuthnProfileGetQueryKey } from '@speed/api-sdk'
import type { RequestFn } from '@speed/api-client'
import { bindRequestFn } from '@speed/api-sdk/runtime'
import type { AuthnProfileProfile } from './app-api/index.js'
import demoEnUS from './locales/en-US.json' with { type: 'json' }
import { ACCOUNT_VERIFY_PATH } from './paths.js'
import { profileWithVerification } from './test-utils/profile.js'
import { renderDemoApp } from './test-utils/render.js'
import {
  authenticatedSnapshot,
  createTestSession,
  readyFeatures,
} from './test-utils/session.js'

const PROFILE_PATH = '/api/v1/auth/me'

/** Whether the profile read has been answered in the current case. */
let profileRead = false

/** Binds a request function answering only the profile read. */
function bindProfile(profile: AuthnProfileProfile): void {
  const request = async <T,>(path: string): Promise<T> => {
    if (path === PROFILE_PATH) {
      profileRead = true
      return profile as T
    }
    throw new Error(`the suite binds only the profile read: ${path}`)
  }
  bindRequestFn(request as RequestFn)
}

function renderFrame() {
  return renderDemoApp({
    session: createTestSession(
      authenticatedSnapshot({ features: readyFeatures() }),
    ),
  })
}

beforeEach(() => {
  window.history.replaceState({}, '', '/')
  profileRead = false
})

describe('ContactVerificationBanner', () => {
  it('shows the target address and both actions while verification is pending', async () => {
    bindProfile(profileWithVerification('pending', 'ada@x.test'))
    renderFrame()

    const region = await screen.findByRole('region', {
      name: demoEnUS.verificationBanner.region,
    })
    expect(region).toHaveTextContent(
      demoEnUS.verificationBanner.pending.replace('{{email}}', 'ada@x.test'),
    )
    expect(
      screen.getByRole('link', { name: demoEnUS.verificationBanner.verify }),
    ).toHaveAttribute('href', ACCOUNT_VERIFY_PATH)
    // The two actions reach the same page with different intent: the change
    // link asks the page to start in the change-address form.
    expect(
      screen.getByRole('link', { name: demoEnUS.verificationBanner.change }),
    ).toHaveAttribute('href', `${ACCOUNT_VERIFY_PATH}?change=email`)
    // No close control: the notice stays until the address is proved.
    expect(within(region).queryByRole('button')).toBeNull()
  })

  it('names the add action when a pending account has no target', async () => {
    bindProfile(profileWithVerification('pending', ''))
    renderFrame()

    const region = await screen.findByRole('region', {
      name: demoEnUS.verificationBanner.region,
    })
    expect(region).toHaveTextContent(
      demoEnUS.verificationBanner.pendingNoEmail,
    )
    expect(
      screen.getByRole('link', { name: demoEnUS.verificationBanner.add }),
    ).toHaveAttribute('href', ACCOUNT_VERIFY_PATH)
    expect(
      screen.queryByRole('link', { name: demoEnUS.verificationBanner.change }),
    ).toBeNull()
  })

  it('renders nothing once verification is complete', async () => {
    bindProfile(profileWithVerification('verified', 'ada@x.test'))
    renderFrame()

    // Wait for the profile read to answer, then assert the region is gone.
    await waitFor(() => expect(profileRead).toBe(true))
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 0))
    })
    expect(
      screen.queryByRole('region', {
        name: demoEnUS.verificationBanner.region,
      }),
    ).toBeNull()
  })

  it('disappears once verification moves the profile to verified', async () => {
    // The verify write invalidates the profile; the banner reads the same
    // query, so the settled pending notice clears without a reload.
    const profile = profileWithVerification('pending', 'ada@x.test')
    bindProfile(profile)
    const { queryClient } = renderFrame()

    await screen.findByRole('region', {
      name: demoEnUS.verificationBanner.region,
    })

    profile.verification = { state: 'verified', email: 'ada@x.test', since: '' }
    await act(async () => {
      await queryClient.invalidateQueries({
        queryKey: getAuthnProfileGetQueryKey(),
      })
    })

    await waitFor(() =>
      expect(
        screen.queryByRole('region', {
          name: demoEnUS.verificationBanner.region,
        }),
      ).toBeNull(),
    )
  })

  it('renders nothing for an account with no unproved email', async () => {
    bindProfile(profileWithVerification('none', ''))
    renderFrame()

    await waitFor(() => expect(profileRead).toBe(true))
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 0))
    })
    expect(
      screen.queryByRole('region', {
        name: demoEnUS.verificationBanner.region,
      }),
    ).toBeNull()
  })
})
