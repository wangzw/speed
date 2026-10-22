/**
 * The verification page's landing states.
 *
 * The persistent banner offers two actions that reach this one page: "verify"
 * goes to `/account/verify`, "change address" carries `?change=email`. The page
 * turns the flag into the section's initial state, so a visitor who asked to
 * change the address meets the change form instead of the code step.
 */

import { screen } from '@testing-library/react'
import { beforeEach, describe, expect, it } from 'vitest'
import { accountUiResources } from '@speed/account-ui'
import { bindRequestFn } from '@speed/api-sdk/runtime'
import type { RequestFn } from '@speed/api-client'
import type { AuthnProfileProfile } from '../app-api/index.js'
import { ACCOUNT_VERIFY_PATH } from '../paths.js'
import { profileWithVerification } from '../test-utils/profile.js'
import { renderDemoApp } from '../test-utils/render.js'
import {
  authenticatedSnapshot,
  createTestSession,
  readyFeatures,
} from '../test-utils/session.js'

const PROFILE_PATH = '/api/v1/auth/me'

/** The account-ui strings this suite reads, at the app's en-US default. */
const accountEnUS = accountUiResources['en-US'] as {
  readonly verification: Readonly<Record<string, string>>
}

/** Binds a request function answering only the profile read. */
function bindProfile(profile: AuthnProfileProfile): void {
  const request = async <T,>(path: string): Promise<T> => {
    if (path === PROFILE_PATH) {
      return profile as T
    }
    throw new Error(`the suite binds only the profile read: ${path}`)
  }
  bindRequestFn(request as RequestFn)
}

function renderVerifyPage(search: string): void {
  window.history.replaceState({}, '', `${ACCOUNT_VERIFY_PATH}${search}`)
  renderDemoApp({
    session: createTestSession(
      authenticatedSnapshot({ features: readyFeatures() }),
    ),
  })
}

beforeEach(() => {
  window.history.replaceState({}, '', '/')
})

describe('AccountVerifyPage', () => {
  it('starts on the code step for a plain verify landing', async () => {
    bindProfile(profileWithVerification('pending', 'ada@x.test'))
    renderVerifyPage('')

    expect(
      await screen.findByLabelText(accountEnUS.verification.codeLabel ?? ''),
    ).toBeInTheDocument()
    expect(
      screen.queryByLabelText(accountEnUS.verification.changeLabel ?? ''),
    ).toBeNull()
  })

  it('starts in the change-address form when the banner asked to change', async () => {
    bindProfile(profileWithVerification('pending', 'ada@x.test'))
    renderVerifyPage('?change=email')

    expect(
      await screen.findByLabelText(accountEnUS.verification.changeLabel ?? ''),
    ).toBeInTheDocument()
  })
})
