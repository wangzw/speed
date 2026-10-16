/**
 * PasswordSignInForm behaviour: the happy path begins a password login
 * flow with the entered identifier and password, takes the ready step's
 * result and fires onSignedIn (scripted through the bindRequestFn harness,
 * asserted on the request bodies and the store token), a busy submit
 * disables the button for the flight's duration, a login-failed answer
 * renders the code's own text in one alert and changes nothing, an empty
 * submit never reaches the network, a forced password change renders the
 * shared step renderer's new-password field and completes the flow, the
 * en-US bundle renders on an English-starting instance, and the tree
 * passes axe. Text expectations read the bundle values, never inline
 * language.
 */

import { describe, expect, it, vi } from 'vitest'
import { act, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { PasswordSignInForm } from './PasswordSignInForm.js'
import { renderWithProviders } from '../test-utils/render.js'
import {
  LOGIN_ANSWER,
  LOGIN_START,
  PROFILE_GET,
  apiError,
  authenticatedCompletion,
  completeFlow,
  makeHarness,
  openFlow,
  principal,
} from '@speed/test-utils/session-harness'
import { expectNoAxeViolations } from '../test-utils/axe.js'
import zhCN from './locales/zh-CN.json' with { type: 'json' }
import enUS from './locales/en-US.json' with { type: 'json' }

const SUBMIT_ZH = zhCN.passwordSignIn.submit
const SUBMIT_EN = enUS.passwordSignIn.submit

interface SignInLabels {
  readonly identifierLabel: string
  readonly passwordLabel: string
}

async function fillAndSubmit(
  identifier: string,
  password: string,
  labels: SignInLabels,
  submitName: string = SUBMIT_ZH,
) {
  const user = userEvent.setup()
  await user.type(screen.getByLabelText(labels.identifierLabel), identifier)
  await user.type(screen.getByLabelText(labels.passwordLabel), password)
  await user.click(screen.getByRole('button', { name: submitName }))
}

const ZH_LABELS: SignInLabels = {
  identifierLabel: zhCN.passwordSignIn.identifierLabel,
  passwordLabel: zhCN.passwordSignIn.passwordLabel,
}

describe('PasswordSignInForm', () => {
  it('begin a password flow with the entered credentials and fire onSignedIn on completion', async () => {
    const harness = makeHarness({
      [LOGIN_START]: () => completeFlow(1),
      [LOGIN_ANSWER]: () => authenticatedCompletion(),
      [PROFILE_GET]: () => principal(),
    })
    const onSignedIn = vi.fn()
    renderWithProviders(
      <PasswordSignInForm session={harness.session} onSignedIn={onSignedIn} />,
    )
    await fillAndSubmit('alice@example.com', 's3cret-pass', ZH_LABELS)
    await waitFor(() => expect(onSignedIn).toHaveBeenCalledTimes(1))
    expect(harness.calls.map((call) => `${call.method} ${call.path}`)).toEqual([
      'POST /auth/flows',
      'POST /auth/flows/answer',
      'GET /auth/me',
    ])
    expect(harness.calls[0]?.options?.body).toMatchObject({
      method: 'password',
      delivery: 'session',
      binding: 'cookie',
      input: { identifier: 'alice@example.com', password: 's3cret-pass' },
    })
    // The ready step's result is taken with step: 'complete'.
    expect(harness.calls[1]?.options?.body).toMatchObject({
      step: 'complete',
    })
    expect(harness.store.get()).toBe('access-1')
  })

  it('disable the submit button while the login is in flight, re-enable after', async () => {
    let resolveStart: (value: unknown) => void = () => {}
    const harness = makeHarness({
      [LOGIN_START]: () =>
        new Promise((resolve) => {
          resolveStart = resolve
        }),
      [LOGIN_ANSWER]: () => authenticatedCompletion(),
      [PROFILE_GET]: () => principal(),
    })
    renderWithProviders(<PasswordSignInForm session={harness.session} />)
    const user = userEvent.setup()
    await user.type(
      screen.getByLabelText(zhCN.passwordSignIn.identifierLabel),
      'alice@example.com',
    )
    await user.type(
      screen.getByLabelText(zhCN.passwordSignIn.passwordLabel),
      's3cret-pass',
    )
    const submit = screen.getByRole('button', { name: SUBMIT_ZH })
    await user.click(submit)
    await waitFor(() => expect(submit).toBeDisabled())
    resolveStart(completeFlow(1))
    await waitFor(() => expect(submit).toBeEnabled())
  })

  it('render the code text of a login-failed answer and change nothing', async () => {
    const harness = makeHarness({
      [LOGIN_START]: () => {
        throw apiError(401, 'authn.login_failed')
      },
    })
    renderWithProviders(<PasswordSignInForm session={harness.session} />)
    await fillAndSubmit('alice@example.com', 'wrong-password', ZH_LABELS)
    await waitFor(() =>
      expect(screen.getByRole('alert')).toHaveTextContent(
        zhCN.errors.authn.login_failed,
      ),
    )
    expect(harness.store.get()).toBeNull()
    expect(harness.calls).toHaveLength(1)
  })

  it('never reach the network for an empty submit', async () => {
    const harness = makeHarness({})
    renderWithProviders(<PasswordSignInForm session={harness.session} />)
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: SUBMIT_ZH }))
    await waitFor(() =>
      expect(screen.queryByRole('alert')).not.toBeInTheDocument(),
    )
    expect(harness.calls).toHaveLength(0)
  })

  it('render a forced password change step and complete the flow through it', async () => {
    // The password method answers a must-change account with a challenge
    // step asking for a new password; the shared step renderer renders it
    // and the answer advances the same flow to completion.
    const harness = makeHarness({
      [LOGIN_START]: () =>
        openFlow({
          seq: 1,
          state: 'first',
          next: {
            kind: 'challenge',
            fields: [{ name: 'new_password', kind: 'password', secret: true }],
          },
        }),
      [LOGIN_ANSWER]: (call) => {
        const step = (call.options?.body as { step?: string }).step
        return step === 'complete'
          ? authenticatedCompletion()
          : completeFlow(2)
      },
      [PROFILE_GET]: () => principal(),
    })
    const onSignedIn = vi.fn()
    renderWithProviders(
      <PasswordSignInForm session={harness.session} onSignedIn={onSignedIn} />,
    )
    await fillAndSubmit('alice@example.com', 'temp-pass', ZH_LABELS)
    const newPassword = await screen.findByLabelText(
      zhCN.passwordSignIn.newPasswordLabel,
    )
    await userEvent.setup().type(newPassword, 'brand-new-pass')
    await userEvent
      .setup()
      .click(screen.getByRole('button', { name: zhCN.signInFlow.submit }))
    await waitFor(() => expect(onSignedIn).toHaveBeenCalledTimes(1))
    const answerBodies = harness.calls
      .filter((call) => call.path === '/auth/flows/answer')
      .map((call) => call.options?.body)
    expect(answerBodies[0]).toMatchObject({
      step: 'answer',
      input: { new_password: 'brand-new-pass' },
    })
    expect(answerBodies[1]).toMatchObject({ step: 'complete' })
    expect(harness.store.get()).toBe('access-1')
  })

  it('render the en-US bundle on an English-starting instance', async () => {
    const harness = makeHarness({
      [LOGIN_START]: () => completeFlow(1),
      [LOGIN_ANSWER]: () => authenticatedCompletion(),
      [PROFILE_GET]: () => principal(),
    })
    renderWithProviders(
      <PasswordSignInForm session={harness.session} />,
      { language: 'en-US' },
    )
    expect(
      screen.getByLabelText(enUS.passwordSignIn.identifierLabel),
    ).toBeInTheDocument()
    expect(
      screen.getByLabelText(enUS.passwordSignIn.passwordLabel),
    ).toBeInTheDocument()
    await fillAndSubmit('alice@example.com', 's3cret-pass', {
      identifierLabel: enUS.passwordSignIn.identifierLabel,
      passwordLabel: enUS.passwordSignIn.passwordLabel,
    }, SUBMIT_EN)
    await waitFor(() =>
      expect(
        harness.calls.filter((call) => call.path === '/auth/flows'),
      ).toHaveLength(1),
    )
  })

  it('pass axe with no violations', async () => {
    const harness = makeHarness({
      [LOGIN_START]: () => completeFlow(1),
      [LOGIN_ANSWER]: () => authenticatedCompletion(),
      [PROFILE_GET]: () => principal(),
    })
    // Rendered under the page h1 a real host page supplies: the form
    // renders no heading of its own (the page above it is host content),
    // and page-has-heading-one is determinate in jsdom now (see the axe
    // helper header) -- a scan without that context fails instead of
    // passing by indeterminacy.
    renderWithProviders(
      <div>
        <h1>Sign in</h1>
        <PasswordSignInForm session={harness.session} />
      </div>,
    )
    await expectNoAxeViolations()
  })

  it('contain a throwing onSignedIn: the committed login never looks like a failure', async () => {
    // A host callback that throws is not a login failure (the login
    // committed -- the store holds the issued token): it must not paint
    // the failure banner, and the containment keeps the throw out of the
    // submit promise as an unhandled rejection.
    const harness = makeHarness({
      [LOGIN_START]: () => completeFlow(1),
      [LOGIN_ANSWER]: () => authenticatedCompletion(),
      [PROFILE_GET]: () => principal(),
    })
    const onSignedIn = vi.fn(() => {
      throw new Error('host navigation failed')
    })
    renderWithProviders(
      <PasswordSignInForm session={harness.session} onSignedIn={onSignedIn} />,
    )
    await fillAndSubmit('alice@example.com', 's3cret-pass', ZH_LABELS)
    await waitFor(() => expect(onSignedIn).toHaveBeenCalledTimes(1))
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(harness.store.get()).toBe('access-1')
  })

  it('treat a superseded submit as the lost race it is, rendering no error', async () => {
    // A concurrent sign-in commits to the session while this form's own
    // login is still in flight, so auth-core rejects this submit with
    // OperationSupersededError when its answer arrives: the losing submit
    // must not render the generic failure banner -- it is not a failure,
    // and the session being authenticated now is the host's own snapshot
    // to observe.
    let releaseFirst: (value: unknown) => void = () => {}
    const firstGate = new Promise((resolve) => {
      releaseFirst = resolve
    })
    let answers = 0
    const harness = makeHarness({
      [LOGIN_START]: () => completeFlow(1),
      [LOGIN_ANSWER]: () => {
        answers += 1
        return answers === 1
          ? firstGate
          : authenticatedCompletion({ access_token: 'access-race' })
      },
      [PROFILE_GET]: () => principal(),
    })
    const onSignedIn = vi.fn()
    renderWithProviders(
      <PasswordSignInForm session={harness.session} onSignedIn={onSignedIn} />,
    )
    const user = userEvent.setup()
    await user.type(
      screen.getByLabelText(zhCN.passwordSignIn.identifierLabel),
      'alice@example.com',
    )
    await user.type(
      screen.getByLabelText(zhCN.passwordSignIn.passwordLabel),
      's3cret-pass',
    )
    const submit = screen.getByRole('button', { name: SUBMIT_ZH })
    await user.click(submit)
    await waitFor(() => expect(submit).toBeDisabled())
    // The winning sign-in commits while the first answer is in flight.
    await act(async () => {
      await harness.session.answerFlow({ seq: 1, step: 'complete', input: {} })
    })
    expect(harness.store.get()).toBe('access-race')
    // The password answer arrives after the winner committed: auth-core
    // rejects it as superseded, and the form treats that as the lost race
    // it is -- quiet, no error banner.
    await act(async () => {
      releaseFirst(authenticatedCompletion())
    })
    await waitFor(() => expect(submit).toBeEnabled())
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(onSignedIn).not.toHaveBeenCalled()
    expect(harness.store.get()).toBe('access-race')
  })
})
