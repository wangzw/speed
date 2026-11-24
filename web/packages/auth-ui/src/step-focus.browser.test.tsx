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

    expect(document.body).toHaveFocus()

    const user = userEvent.setup()
    await user.type(
      screen.getByLabelText(enUS.smsSignIn.phoneLabel),
      US_NATIONAL,
    )
    await user.click(
      screen.getByRole('button', { name: enUS.smsSignIn.sendCode }),
    )

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
      name: enUS.signInFlow.factorKind.totp,
    })
    await waitFor(() => expect(factor).toHaveFocus())
  })

  it('leave focus where the viewer moved it before the step arrives', async () => {
    let release: (() => void) | undefined
    const startGate = new Promise<void>((resolve) => {
      release = resolve
    })
    const harness = makeHarness({
      [LOGIN_START]: async () => {
        await startGate
        return factorStep(1, 'Authenticator')
      },
    })
    renderWithProviders(
      <>
        <PasswordSignInForm session={harness.session} />
        <button type="button">Elsewhere</button>
      </>,
      { language: 'en-US' },
    )

    const user = userEvent.setup()
    await user.type(
      screen.getByLabelText(enUS.passwordSignIn.identifierLabel),
      'ada',
    )
    await user.type(
      screen.getByLabelText(enUS.passwordSignIn.passwordLabel),
      'correct horse',
    )
    const submit = screen.getByRole('button', {
      name: enUS.passwordSignIn.submit,
    })
    await user.click(submit)
    await waitFor(() => expect(submit).toHaveAttribute('aria-busy', 'true'))

    const elsewhere = screen.getByRole('button', { name: 'Elsewhere' })
    await user.click(elsewhere)
    expect(elsewhere).toHaveFocus()

    release?.()

    const factor = await screen.findByRole('button', {
      name: enUS.signInFlow.factorKind.totp,
    })
    expect(elsewhere).toHaveFocus()
    expect(factor).not.toHaveFocus()
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
      await screen.findByRole('button', {
        name: enUS.signInFlow.factorKind.totp,
      }),
    )

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

    const code = await screen.findByLabelText(enUS.smsSignIn.codeLabel)
    expect(document.body).toHaveFocus()

    const user = userEvent.setup()
    await user.type(code, '123456')
    await user.click(
      screen.getByRole('button', { name: enUS.signInFlow.submit }),
    )

    const factor = await screen.findByRole('button', {
      name: enUS.signInFlow.factorKind.totp,
    })
    await waitFor(() => expect(factor).toHaveFocus())
  })

  it('focus the unsupported step’s exit control when it replaces the answered step', async () => {
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

  it('land on the expired notice heading when a refused answer replaces the callback step', async () => {
    const harness = makeHarness({
      [LOGIN_CURRENT]: () => smsCodeChallenge(),
      [LOGIN_ANSWER]: () => {
        throw apiError(401, 'authn.flow_invalid')
      },
    })
    renderWithProviders(
      <SocialCallbackHandler session={harness.session} onSignIn={() => {}} />,
      { language: 'en-US' },
    )

    const code = await screen.findByLabelText(enUS.smsSignIn.codeLabel)
    const user = userEvent.setup()
    await user.type(code, '123456')
    await user.click(
      screen.getByRole('button', { name: enUS.signInFlow.submit }),
    )

    const heading = await screen.findByRole('heading', {
      name: enUS.socialCallback.incompleteTitle,
    })
    expect(heading).toHaveAttribute('tabindex', '-1')
    await waitFor(() => expect(heading).toHaveFocus())
  })

  it('leave the focus the viewer moved when the callback step gives way to the expired notice', async () => {
    let refuse!: (error: unknown) => void
    const gate = new Promise<never>((_resolve, reject) => {
      refuse = reject
    })
    const harness = makeHarness({
      [LOGIN_CURRENT]: () => smsCodeChallenge(),
      [LOGIN_ANSWER]: () => gate,
    })
    renderWithProviders(
      <>
        <SocialCallbackHandler session={harness.session} onSignIn={() => {}} />
        <button type="button">Elsewhere</button>
      </>,
      { language: 'en-US' },
    )

    const code = await screen.findByLabelText(enUS.smsSignIn.codeLabel)
    const user = userEvent.setup()
    await user.type(code, '123456')
    const submit = screen.getByRole('button', {
      name: enUS.signInFlow.submit,
    })
    await user.click(submit)
    await waitFor(() => expect(submit).toHaveAttribute('aria-busy', 'true'))

    const elsewhere = screen.getByRole('button', { name: 'Elsewhere' })
    await user.click(elsewhere)
    expect(elsewhere).toHaveFocus()

    refuse(apiError(401, 'authn.flow_invalid'))

    const heading = await screen.findByRole('heading', {
      name: enUS.socialCallback.incompleteTitle,
    })
    expect(elsewhere).toHaveFocus()
    expect(heading).not.toHaveFocus()
  })
})
