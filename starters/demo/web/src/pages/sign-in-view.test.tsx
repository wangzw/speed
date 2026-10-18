/**
 * The host's sign-in surface.
 *
 * Beyond framing auth-ui's screen, it answers the skeleton's one dead end: a
 * fresh deployment is born with no account and no registration path, so the
 * surface says where the first account comes from. The suite renders an
 * anonymous viewer and reads that guidance.
 *
 * It also carries the host half of registration: a registration that
 * completes through SMS lands on the account setup page, because the account
 * exists but has no login name or password yet.
 */

import { beforeEach, describe, expect, it } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { authUiResources } from '@speed/auth-ui'
import {
  LOGIN_ANSWER,
  LOGIN_METHODS,
  LOGIN_START,
  PROFILE_GET,
  authenticatedCompletion,
  completeFlow,
  makeHarness,
  methodList,
  openFlow,
  principal,
} from '@speed/test-utils/session-harness'
import demoEnUS from '../locales/en-US.json' with { type: 'json' }
import demoZhCN from '../locales/zh-CN.json' with { type: 'json' }
import { ACCOUNT_SETUP_PATH } from '../paths.js'
import { renderDemoApp } from '../test-utils/render.js'

/**
 * The auth-ui bundle the screen renders from, read as test data: the register
 * entry and the SMS form's field labels. The host test drives the package's
 * real bundle, so a renamed label fails here rather than in a selector.
 */
const authEn = authUiResources['en-US'] as unknown as {
  readonly mode: { readonly registerEntry: string }
  readonly smsSignIn: {
    readonly phoneLabel: string
    readonly sendCode: string
    readonly codeLabel: string
    readonly submit: string
  }
}

beforeEach(() => {
  window.history.replaceState({}, '', '/')
})

describe('the sign-in surface', () => {
  it('guides a fresh deployment to its first account', async () => {
    renderDemoApp()

    // The guidance sits under the form, naming the way a fresh deployment
    // gets its first account (the server's seed command).
    expect(
      await screen.findByText(demoEnUS.signIn.firstAccount.title),
    ).toBeInTheDocument()
    expect(
      screen.getByText(demoEnUS.signIn.firstAccount.body),
    ).toBeInTheDocument()
  })

  it('renders the guidance in the active language', () => {
    renderDemoApp({ language: 'zh-CN' })

    expect(
      screen.getByText(demoZhCN.signIn.firstAccount.title),
    ).toBeInTheDocument()
  })

  it('lands a registration completed through SMS on the account setup page', async () => {
    const harness = makeHarness({
      [LOGIN_METHODS]: () =>
        methodList(
          [{ name: 'sms', kind: 'form', opens_account: true }],
          { signup: true },
        ),
      [LOGIN_START]: () =>
        openFlow({
          seq: 1,
          state: 'first',
          next: {
            kind: 'challenge',
            fields: [
              { name: 'code', kind: 'code', required: true, secret: true },
            ],
          },
        }),
      [LOGIN_ANSWER]: (call) =>
        (call.options?.body as { step?: string }).step === 'complete'
          ? authenticatedCompletion()
          : completeFlow(2),
      [PROFILE_GET]: () => principal(),
    })
    renderDemoApp({ session: harness.session })

    const user = userEvent.setup()
    await user.click(
      await screen.findByRole('button', { name: authEn.mode.registerEntry }),
    )
    await user.type(
      screen.getByLabelText(authEn.smsSignIn.phoneLabel),
      '+14155550123',
    )
    await user.click(
      screen.getByRole('button', { name: authEn.smsSignIn.sendCode }),
    )
    await user.type(
      await screen.findByLabelText(authEn.smsSignIn.codeLabel),
      '123456',
    )
    // The register entry's own "sign in" toggle shares the submit's label,
    // so the submit is read inside the form.
    const form = screen
      .getByLabelText(authEn.smsSignIn.codeLabel)
      .closest('form') as HTMLFormElement
    await user.click(
      within(form).getByRole('button', { name: authEn.smsSignIn.submit }),
    )

    // The screen reported register + sms, so the host moved the just-opened
    // account onto the setup page. Its profile lists no login name, so the
    // password section explains what to add first.
    await waitFor(() =>
      expect(window.location.pathname).toBe(ACCOUNT_SETUP_PATH),
    )
    expect(
      await screen.findByText(demoEnUS.accountSetup.password.loginNameRequired),
    ).toBeInTheDocument()
  })
})
