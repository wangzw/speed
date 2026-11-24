import { act, screen, waitFor, within } from '@testing-library/react'
import { beforeEach, describe, expect, it } from 'vitest'
import { getAuthnProfileGetQueryKey } from '@speed/api-sdk'
import type { RequestFn } from '@speed/api-client'
import { bindRequestFn } from '@speed/api-sdk/runtime'
import type { AuthnProfileProfile } from './app-api/index.js'
import demoEnUS from './locales/en-US.json' with { type: 'json' }
import { ACCOUNT_PATH } from './paths.js'
import { profileWithVerification } from './test-utils/profile.js'
import { renderDemoApp } from './test-utils/render.js'
import {
  authenticatedSnapshot,
  createTestSession,
  readyFeatures,
} from './test-utils/session.js'

const PROFILE_PATH = '/api/v1/auth/me'

function bindProfile(profile: AuthnProfileProfile): void {
  const request = async <T,>(path: string): Promise<T> => {
    if (path === PROFILE_PATH) {
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
})

describe('ContactVerificationBanner', () => {
  it('shows the target address and both actions while verification is pending', async () => {
    bindProfile(profileWithVerification('pending', 'ada@x.test'))
    renderFrame()

    const region = await screen.findByRole('region', {
      name: new RegExp(demoEnUS.verificationBanner.region),
    })
    expect(region).toHaveTextContent(
      demoEnUS.verificationBanner.pending.replace('{{email}}', 'ada@x.test'),
    )
    expect(
      screen.getByRole('link', { name: demoEnUS.verificationBanner.verify }),
    ).toHaveAttribute('href', `${ACCOUNT_PATH}?verify=1`)
    expect(
      screen.getByRole('link', { name: demoEnUS.verificationBanner.change }),
    ).toHaveAttribute('href', `${ACCOUNT_PATH}?change=email`)
    expect(within(region).queryByRole('button')).toBeNull()
  })

  it('names the add action when a pending account has no target', async () => {
    bindProfile(profileWithVerification('pending', ''))
    renderFrame()

    const region = await screen.findByRole('region', {
      name: new RegExp(demoEnUS.verificationBanner.region),
    })
    expect(region).toHaveTextContent(
      demoEnUS.verificationBanner.pendingNoEmail,
    )
    expect(
      screen.getByRole('link', { name: demoEnUS.verificationBanner.add }),
    ).toHaveAttribute('href', `${ACCOUNT_PATH}?verify=1`)
    expect(
      screen.queryByRole('link', { name: demoEnUS.verificationBanner.change }),
    ).toBeNull()
  })

  it('renders nothing once verification is complete', async () => {
    bindProfile(profileWithVerification('verified', 'ada@x.test'))
    const { queryClient } = renderFrame()

    await waitFor(() =>
      expect(
        queryClient.getQueryState(getAuthnProfileGetQueryKey())?.status,
      ).toBe('success'),
    )
    expect(
      screen.queryByRole('region', {
        name: new RegExp(demoEnUS.verificationBanner.region),
      }),
    ).toBeNull()
  })

  it('disappears once verification moves the profile to verified', async () => {
    const profile = profileWithVerification('pending', 'ada@x.test')
    bindProfile(profile)
    const { queryClient } = renderFrame()

    await screen.findByRole('region', {
      name: new RegExp(demoEnUS.verificationBanner.region),
    })

    profile.verification = { state: 'verified', email: 'ada@x.test', since: null }
    await act(async () => {
      await queryClient.invalidateQueries({
        queryKey: getAuthnProfileGetQueryKey(),
      })
    })

    await waitFor(() =>
      expect(
        screen.queryByRole('region', {
          name: new RegExp(demoEnUS.verificationBanner.region),
        }),
      ).toBeNull(),
    )
  })

  it('renders nothing for an account with no unproved email', async () => {
    bindProfile(profileWithVerification('none', ''))
    const { queryClient } = renderFrame()

    await waitFor(() =>
      expect(
        queryClient.getQueryState(getAuthnProfileGetQueryKey())?.status,
      ).toBe('success'),
    )
    expect(
      screen.queryByRole('region', {
        name: new RegExp(demoEnUS.verificationBanner.region),
      }),
    ).toBeNull()
  })
})
