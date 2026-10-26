/**
 * Step-switch focus in Chromium: a flow step that replaces the surface a
 * viewer was working in takes the focus the unmounted control had, while a
 * step that is the first thing a component rendered moves no focus -- and
 * the callback route's first step after readFlow is that same first
 * appearance. The cases drive the forms the way a viewer does (type, then
 * activate the submit control) and assert only where focus lands.
 */
import { describe, expect, it } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { PhoneCountryDefaultProvider } from '@speed/ui-kit'
import {
  LOGIN_ANSWER,
  LOGIN_CURRENT,
  LOGIN_START,
  apiError,
  completeFlow,
  makeHarness,
  openFlow,
} from '@speed/test-utils/session-harness'
import type { AuthnFlowState } from '@speed/auth-core'
import { renderWithProviders } from '../test-utils/render.js'
import { PasswordSignInForm } from './PasswordSignInForm.js'
import { SMSSignInForm } from './SMSSignInForm.js'
import { SocialCallbackHandler } from './SocialCallbackHandler.js'
import enUS from './locales/en-US.json' with { type: 'json' }

const US_NATIONAL = '4155552671'

/** The SMS method's code prompt: state `first` with a challenge. */
function smsCodeChallenge(): AuthnFlowState {
  return openFlow({
    seq: 1,
    state: 'first',
    next: {
      kind: 'challenge',
      fields: [{ name: 'code', kind: 'code', required: true, secret: true }],
    },
  })
}

/** A second factor offered as one labelled choice. */
function factorStep(seq: number, label: string): AuthnFlowState {
  return openFlow({
    seq,
    state: 'second',
    next: {
      kind: 'factor',
      factors: [{ id: 'mfa-1', kind: 'totp', label }],
    },
  })
}

describe('step-switch focus in Chromium', () => {
  it('leaves the first SMS step alone and focuses the code field the send replaces it with', async () => {
    const harness = makeHarness({
      [LOGIN_START]: () => smsCodeChallenge(),
    })
    renderWithProviders(
      <PhoneCountryDefaultProvider country="US">
        <SMSSignInForm session={harness.session} />
      </PhoneCountryDefaultProvider>,
      { language: 'en-US' },
    )

    // The phone step is the first surface this form rendered: it replaced
    // nothing, so it takes no focus.
    expect(document.body).toHaveFocus()

    const user = userEvent.setup()
    await user.type(
      screen.getByLabelText(enUS.smsSignIn.phoneLabel),
      US_NATIONAL,
    )
    await user.click(
      screen.getByRole('button', { name: enUS.smsSignIn.sendCode }),
    )

    // The challenge replaces the phone step. The send control unmounted
    // with it, so focus lands on the new step's first control.
    const code = await screen.findByLabelText(enUS.smsSignIn.codeLabel)
    await waitFor(() => expect(code).toHaveFocus())
  })

  it('focus the first control of the second factor a password submit parks', async () => {
    const harness = makeHarness({
      [LOGIN_START]: () => factorStep(1, 'Authenticator'),
    })
    renderWithProviders(<PasswordSignInForm session={harness.session} />, {
      language: 'en-US',
    })
    expect(document.body).toHaveFocus()

    const user = userEvent.setup()
    await user.type(
      screen.getByLabelText(enUS.passwordSignIn.identifierLabel),
      'ada',
    )
    await user.type(
      screen.getByLabelText(enUS.passwordSignIn.passwordLabel),
      'correct horse',
    )
    await user.click(
      screen.getByRole('button', { name: enUS.passwordSignIn.submit }),
    )

    const factor = await screen.findByRole('button', {
      name: 'Authenticator',
    })
    await waitFor(() => expect(factor).toHaveFocus())
  })

  it('return focus to the credentials form when a lost flow brings it back', async () => {
    const harness = makeHarness({
      [LOGIN_START]: () => factorStep(1, 'Authenticator'),
      [LOGIN_ANSWER]: () => {
        throw apiError(401, 'authn.flow_invalid')
      },
    })
    renderWithProviders(<PasswordSignInForm session={harness.session} />, {
      language: 'en-US',
    })

    const user = userEvent.setup()
    await user.type(
      screen.getByLabelText(enUS.passwordSignIn.identifierLabel),
      'ada',
    )
    await user.type(
      screen.getByLabelText(enUS.passwordSignIn.passwordLabel),
      'correct horse',
    )
    await user.click(
      screen.getByRole('button', { name: enUS.passwordSignIn.submit }),
    )
    await user.click(
      await screen.findByRole('button', { name: 'Authenticator' }),
    )

    // A lost flow drops the parked step and the channel's own first step
    // returns: it is a step switch too, so its first control takes focus.
    const identifier = await screen.findByLabelText(
      enUS.passwordSignIn.identifierLabel,
    )
    await waitFor(() => expect(identifier).toHaveFocus())
  })

  it('leave the callback readFlow step alone, then focus the step that replaces it', async () => {
    const harness = makeHarness({
      [LOGIN_CURRENT]: () => smsCodeChallenge(),
      [LOGIN_ANSWER]: (call) => {
        const step = (call.options?.body as { step?: string }).step
        return step === 'answer' ? factorStep(2, 'Authenticator') : completeFlow(3)
      },
    })
    renderWithProviders(<SocialCallbackHandler session={harness.session} />, {
      language: 'en-US',
    })

    // The step the callback read back is this route's first appearance:
    // nothing the viewer was working in was replaced, so focus stays put.
    const code = await screen.findByLabelText(enUS.smsSignIn.codeLabel)
    expect(document.body).toHaveFocus()

    const user = userEvent.setup()
    await user.type(code, '123456')
    await user.click(
      screen.getByRole('button', { name: enUS.signInFlow.submit }),
    )

    // Answering parks the next step, which did replace the answered one.
    const factor = await screen.findByRole('button', {
      name: 'Authenticator',
    })
    await waitFor(() => expect(factor).toHaveFocus())
  })

  it('focus the unsupported step’s exit control when it replaces the answered step', async () => {
    // A step the route cannot render carries a notice and its own exit
    // control, so the switch still has a first control to take focus.
    const harness = makeHarness({
      [LOGIN_CURRENT]: () => smsCodeChallenge(),
      [LOGIN_ANSWER]: () =>
        openFlow({
          seq: 2,
          state: 'second',
          next: {
            kind: 'redirect',
            url: 'https://idp.example/authorize',
            method: 'oidc',
          },
        }),
    })
    renderWithProviders(<SocialCallbackHandler session={harness.session} />, {
      language: 'en-US',
    })

    const code = await screen.findByLabelText(enUS.smsSignIn.codeLabel)
    const user = userEvent.setup()
    await user.type(code, '123456')
    await user.click(
      screen.getByRole('button', { name: enUS.signInFlow.submit }),
    )

    const exit = await screen.findByRole('button', {
      name: enUS.socialCallback.retry,
    })
    await waitFor(() => expect(exit).toHaveFocus())
  })
})
