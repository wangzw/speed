/**
 * The verification page's landing states.
 *
 * The persistent banner offers two actions that reach this one page: "verify"
 * goes to `/account/verify`, "change address" carries `?change=email`. The page
 * turns the flag into the section's initial state, so a visitor who asked to
 * change the address meets the change form instead of the code step. A plain
 * verify landing sends one code on arrival; the change landing never sends on
 * its own, not even after its form is closed.
 */

import { act, fireEvent, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it } from 'vitest'
import { accountUiResources } from '@speed/account-ui'
import { bindRequestFn } from '@speed/api-sdk/runtime'
import type { RequestFn, RequestOptions } from '@speed/api-client'
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
const ADD_PATH = '/api/v1/auth/identifiers'

/** The calls the page issued in the current case, in order. */
let calls: { readonly method: string; readonly path: string }[] = []

/** The account-ui strings this suite reads, at the app's en-US default. */
const accountEnUS = accountUiResources['en-US'] as {
  readonly verification: Readonly<Record<string, string>>
}

/** Binds a request function answering the profile read and the page's own
 * automatic code-send. */
function bindProfile(profile: AuthnProfileProfile): void {
  const request = async <T,>(
    path: string,
    options?: RequestOptions,
  ): Promise<T> => {
    const method = options?.method ?? 'GET'
    calls.push({ method, path })
    if (method === 'GET' && path === PROFILE_PATH) {
      return profile as T
    }
    if (method === 'POST' && path === ADD_PATH) {
      return {
        state: 'pending',
        expires_in: 600,
        resend_after: 60,
      } as T
    }
    throw new Error(`the suite binds only the profile read and the send: ${method} ${path}`)
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
  calls = []
})

/** The send calls the page issued so far. */
function sends() {
  return calls.filter((call) => call.method === 'POST' && call.path === ADD_PATH)
}

describe('AccountVerifyPage', () => {
  it('starts on the code step and sends one code for a plain verify landing', async () => {
    bindProfile(profileWithVerification('pending', 'ada@x.test'))
    renderVerifyPage('')

    expect(
      await screen.findByLabelText(accountEnUS.verification.codeLabel ?? ''),
    ).toBeInTheDocument()
    expect(
      screen.queryByLabelText(accountEnUS.verification.changeLabel ?? ''),
    ).toBeNull()
    // The landing is the verification page's own: it puts one code on its
    // way, and the profile refetch that follows sends none.
    await waitFor(() => expect(sends()).toHaveLength(1))
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 0))
    })
    expect(sends()).toHaveLength(1)
  })

  it('starts in the change-address form when the banner asked to change, sending nothing', async () => {
    bindProfile(profileWithVerification('pending', 'ada@x.test'))
    renderVerifyPage('?change=email')

    expect(
      await screen.findByLabelText(accountEnUS.verification.changeLabel ?? ''),
    ).toBeInTheDocument()
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 0))
    })
    expect(sends()).toHaveLength(0)
  })

  it('keeps sending nothing after the change form is closed', async () => {
    bindProfile(profileWithVerification('pending', 'ada@x.test'))
    renderVerifyPage('?change=email')

    fireEvent.click(
      await screen.findByRole('button', {
        name: accountEnUS.verification.changeCancel ?? '',
      }),
    )
    expect(
      await screen.findByLabelText(accountEnUS.verification.codeLabel ?? ''),
    ).toBeInTheDocument()
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 0))
    })
    expect(sends()).toHaveLength(0)
  })
})
