/**
 * The account page's own landing behaviour.
 *
 * The account page mounts account-ui's ContactVerificationSection for an
 * account whose registration contact is pending, but it does not hand the
 * section the verify route's `autoSend` flag. The one automatic send belongs
 * to `/account/verify`; the account page waits for the visitor to ask.
 */

import { act, screen } from '@testing-library/react'
import { beforeEach, describe, expect, it } from 'vitest'
import { accountUiResources } from '@speed/account-ui'
import { bindRequestFn } from '@speed/api-sdk/runtime'
import type { RequestFn, RequestOptions } from '@speed/api-client'
import { createBackend } from '../a11y/backends.js'
import demoEnUS from '../locales/en-US.json' with { type: 'json' }
import { ACCOUNT_PATH } from '../paths.js'
import { renderDemoApp } from '../test-utils/render.js'
import {
  authenticatedSnapshot,
  createTestSession,
  readyFeatures,
} from '../test-utils/session.js'

const ADD_PATH = '/api/v1/auth/identifiers'
const accountEnUS = accountUiResources['en-US'] as {
  readonly verification: Readonly<Record<string, string>>
}

/** The calls the page issued in the current case, in order. */
let calls: { readonly method: string; readonly path: string }[] = []

beforeEach(() => {
  window.history.replaceState({}, '', '/')
  calls = []
})

describe('AccountSettingsPage', () => {
  it('does not send a verification code on mount', async () => {
    // The data-mode profile is a pending self-registration with a target
    // address, so the verification section is on the page; only the missing
    // `autoSend` keeps the page from sending.
    const backend = createBackend('data')
    const request = (async <T,>(
      path: string,
      options?: RequestOptions,
    ): Promise<T> => {
      calls.push({ method: options?.method ?? 'GET', path })
      return (await backend(path, options)) as T
    }) as RequestFn
    bindRequestFn(request)
    window.history.replaceState({}, '', ACCOUNT_PATH)
    renderDemoApp({
      session: createTestSession(
        authenticatedSnapshot({ features: readyFeatures() }),
      ),
    })

    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: demoEnUS.account.title,
      }),
    ).toBeInTheDocument()
    // The pending contact is on the page, with its target address: the
    // section is mounted, and only the missing flag keeps it quiet.
    expect(
      await screen.findByText(
        (accountEnUS.verification.target ?? '').replace(
          '{{email}}',
          'user@example.com',
        ),
      ),
    ).toBeInTheDocument()
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 0))
    })
    expect(
      calls.filter(
        (call) => call.method === 'POST' && call.path === ADD_PATH,
      ),
    ).toHaveLength(0)
  })
})
