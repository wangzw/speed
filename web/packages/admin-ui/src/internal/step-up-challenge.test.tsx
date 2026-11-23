/**
 * StepUpChallenge: the re-authentication dialog of the step-up-gated
 * admin writes.
 *
 * The dialog drives the session's step-up flow (auth-core's useStepUp)
 * and forwards each answer; the assertion here is the one wire detail the
 * backend requires and a naive renderer misses: the second factor's code
 * challenge is answered under the `factor` step with the chosen factor's
 * identifier resent alongside the code (pkg/authn/login's applyFactor
 * re-reads the factor from the answer). A ready state's `complete` step is
 * answered on its own once per sequence, and the step's own confirmation
 * button takes over when that automatic answer is refused.
 *
 * The failure cases pin the two different reasons a challenge cannot be
 * completed. An account with no active factor is recognised from its own
 * factor read: the dialog names the missing second factor and never starts a
 * flow it knows cannot come; the host's account-settings destination is a
 * link when the shell supplies one and text only when it does not, so the
 * dialog itself holds no host path. A start the server refuses with 401
 * while the account does have factors (none of them may strengthen this
 * session) is the deployment's own refusal, and the dialog offers the
 * re-login instead. Any other start failure -- a 400 included -- is an
 * ordinary error rendered as its code, never folded into either of those
 * two explanations.
 */

import { useState } from 'react'
import { describe, expect, it, vi } from 'vitest'
import { waitForCondition } from '@speed/test-utils/wait'
import { act, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ApiError } from '@speed/api-client'
import type {
  AuthSession,
  AuthSessionListener,
  AuthSnapshot,
  AuthnFlowState,
} from '@speed/auth-core'
import zhCN from '../locales/zh-CN.json' with { type: 'json' }
import { AdminAccountHrefProvider } from './account-href.js'
import { AdminReturnPathProvider } from './return-path.js'
import { StepUpChallenge } from './step-up-challenge.js'
import { expectNoAxeViolations } from '../../test-utils/axe.js'
import { renderWithProviders } from '../../test-utils/render.js'
import { bindAdminRequest } from '../../test-utils/request-rig.js'
import { installFakeTurnstile } from '@speed/test-utils/turnstile'
import {
  AUTHENTICATED_SNAPSHOT,
  createStepUpSession,
  type StepUpTestSession,
} from '../../test-utils/session.js'

/** A session whose step-up flow offers one factor, then asks for its code,
 * then settles elevated; it records every answer's input. */
function flowSession(): {
  readonly session: AuthSession
  readonly answers: Array<Readonly<Record<string, string>> | undefined>
} {
  const answers: Array<Readonly<Record<string, string>> | undefined> = []
  const snapshot: AuthSnapshot = AUTHENTICATED_SNAPSHOT
  const listeners = new Set<AuthSessionListener>()
  const session: AuthSession = {
    getSnapshot: () => snapshot,
    subscribe: (listener) => {
      listeners.add(listener)
      return () => {
        listeners.delete(listener)
      }
    },
    isStepUpShortfall: () => true,
    startAuthenticatedFlow: () =>
      Promise.resolve<AuthnFlowState>({
        seq: 1,
        state: 'second',
        next: { kind: 'factor', factors: [{ id: 'f1', kind: 'totp' }] },
      }),
    answerFlow: (request) => {
      answers.push(request.input)
      return Promise.resolve<AuthnFlowState>(
        answers.length === 1
          ? {
              seq: 2,
              state: 'second',
              next: {
                kind: 'challenge',
                fields: [{ name: 'code', kind: 'code', required: true }],
              },
            }
          : {
              state: 'authenticated',
              session: { id: 'sess-1', expires_at: '2026-10-17T00:00:00Z' },
            },
      )
    },
    abandonFlow: () => Promise.resolve(),
    readFlow: () => Promise.resolve(null),
    listMethods: () =>
      Promise.resolve({
        methods: [{ name: 'password', kind: 'form' }],
        signup: { enabled: false, password: { enabled: false } },
        mfa: { factors: [] },
        human: {
          phone: { required: false, provider: 'off' },
          other: { required: false, provider: 'off' },
        },
        password_reset: { enabled: false, methods: [] },
      }),
    discoverSso: () => Promise.resolve([]),
    register: () => Promise.reject(new Error('unused')),
    startFlow: () => Promise.reject(new Error('unused')),
    logout: () => Promise.reject(new Error('unused')),
    restore: () => Promise.resolve(false),
    beginIdentityTransition: () => () => undefined,
    refresh: () => Promise.resolve(false),
    retryFeatures: () => Promise.resolve(),
    setPermissionSet: () => undefined,
    setDisplayName: () => undefined,
  }
  return { session, answers }
}

/** A session whose step-up flow starts at the password because every factor
 * was registered during the session, then offers the factor once the password
 * is proved. It records every answer's step and input. */
function firstFactorSession(): {
  readonly session: AuthSession
  readonly answers: Array<{
    readonly step?: string
    readonly input?: Readonly<Record<string, string>>
  }>
} {
  const answers: Array<{
    readonly step?: string
    readonly input?: Readonly<Record<string, string>>
  }> = []
  const session: AuthSession = {
    ...flowSession().session,
    startAuthenticatedFlow: () =>
      Promise.resolve<AuthnFlowState>({
        seq: 1,
        state: 'first',
        next: {
          kind: 'challenge',
          fields: [{ name: 'password', kind: 'password', secret: true }],
        },
      }),
    answerFlow: (request) => {
      answers.push(request)
      return Promise.resolve<AuthnFlowState>({
        seq: 2,
        state: 'second',
        next: { kind: 'factor', factors: [{ id: 'f1', kind: 'totp' }] },
      })
    },
  }
  return { session, answers }
}

/** A session whose step-up flow offers one factor, asks for its code, parks
 * on the ready state's `complete` step, and settles the elevation when that
 * step is answered. It records every answer's step and input, so a suite can
 * tell the automatic completion from a second click. `completeAnswer`
 * scripts that answer; the default elevates. */
function readySession(
  completeAnswer: () => Promise<AuthnFlowState> = () =>
    Promise.resolve<AuthnFlowState>({
      state: 'authenticated',
      session: { id: 'sess-1', expires_at: '2026-10-17T00:00:00Z' },
    }),
): {
  readonly session: AuthSession
  readonly answers: Array<{
    readonly step?: string
    readonly input?: Readonly<Record<string, string>>
  }>
} {
  const answers: Array<{
    readonly step?: string
    readonly input?: Readonly<Record<string, string>>
  }> = []
  const session: AuthSession = {
    ...flowSession().session,
    answerFlow: (request) => {
      answers.push({ step: request.step, input: request.input })
      if (answers.length === 1) {
        return Promise.resolve<AuthnFlowState>({
          seq: 2,
          state: 'second',
          next: {
            kind: 'challenge',
            fields: [{ name: 'code', kind: 'code', required: true }],
          },
        })
      }
      if (answers.length === 2) {
        return Promise.resolve<AuthnFlowState>({
          seq: 3,
          state: 'ready',
          next: { kind: 'complete' },
        })
      }
      return completeAnswer()
    },
  }
  return { session, answers }
}

/** One active factor as the caller's own factor read answers it. */
const ACTIVE_FACTOR = {
  id: 'factor-1',
  kind: 'totp',
  label: 'Authenticator app',
  status: 'active',
  created_at: '2026-01-01T00:00:00Z',
}

/** Binds a scripted request function that answers the caller's own factor
 * read with `factors`, and records every request. Any other leg is a
 * defect of the case. */
function bindFactors(factors: readonly unknown[]): ReturnType<typeof bindAdminRequest> {
  return bindAdminRequest((call) => {
    if (call.method === 'GET' && call.path === '/api/v1/auth/factors') {
      return { factors }
    }
    throw new Error(`unexpected ${call.method} ${call.path}`)
  })
}

/** A session whose flow release is recorded, so a suite can assert whether
 * the dialog sent any abandon request. */
function withRecordedAbandon(session: AuthSession): {
  readonly session: AuthSession
  readonly abandon: ReturnType<typeof vi.fn>
} {
  const abandon = vi.fn<AuthSession['abandonFlow']>(() => Promise.resolve())
  return { session: { ...session, abandonFlow: abandon }, abandon }
}

/** A session whose step-up start is refused by `refusal`. Its shortfall
 * classifier is auth-core's own: a 401 on an established session. */
function refusingStartSession(
  refusal: unknown,
  logout: () => Promise<void> = () => Promise.resolve(),
): StepUpTestSession {
  return {
    ...createStepUpSession(),
    startAuthenticatedFlow: () => Promise.reject(refusal),
    logout,
  }
}

describe('StepUpChallenge', () => {
  it('stacks the dialog body with one theme spacing gap', async () => {
    // The account and admin dialogs read the same: the body's direct
    // children sit theme.spacing(2) apart, so the failure alert has the
    // same gap above and below. The default token unit is 8.
    bindFactors([ACTIVE_FACTOR])
    renderWithProviders(
      <StepUpChallenge
        open
        session={flowSession().session}
        onSuccess={vi.fn()}
        onCancel={vi.fn()}
      />,
    )
    const dialog = await screen.findByRole('dialog')
    const body = dialog.querySelector<HTMLElement>('.MuiDialogContent-root')
    expect(body).not.toBeNull()
    const style = getComputedStyle(body as HTMLElement)
    expect(style.display).toBe('flex')
    expect(style.flexDirection).toBe('column')
    expect(style.gap).toBe('16px')
  })

  it('sends the host’s path as the flow’s return_to', async () => {
    // A redirect method's round trip lands back on the admin page that
    // opened the dialog, so that page reads the refusal.
    const starts: Array<Parameters<AuthSession['startAuthenticatedFlow']>[0]> =
      []
    const base = flowSession().session
    const session: AuthSession = {
      ...base,
      startAuthenticatedFlow: (request) => {
        starts.push(request)
        return Promise.resolve<AuthnFlowState>({
          seq: 1,
          state: 'second',
          next: { kind: 'factor', factors: [{ id: 'f1', kind: 'totp' }] },
        })
      },
    }
    renderWithProviders(
      <AdminReturnPathProvider returnPath="/admin/accounts">
        <StepUpChallenge
          open
          session={session}
          onSuccess={vi.fn()}
          onCancel={() => undefined}
        />
      </AdminReturnPathProvider>,
    )

    await waitFor(() => expect(starts).toHaveLength(1))
    expect(starts[0]).toMatchObject({
      purpose: 'step-up',
      return_to: '/admin/accounts',
    })
  })

  it('presents the human proof on an SMS second-factor answer', async () => {
    const fake = installFakeTurnstile()
    try {
      const requests: Array<{
        step: string | undefined
        input?: Readonly<Record<string, string>>
        human_proof?: string
      }> = []
      const base = flowSession().session
      const session: AuthSession = {
        ...base,
        listMethods: () =>
          Promise.resolve({
            methods: [{ name: 'password', kind: 'form' }],
            signup: { enabled: false, password: { enabled: false } },
            mfa: { factors: [] },
            human: {
              phone: {
                required: true,
                provider: 'turnstile',
                site_key: 'site-1',
              },
              other: {
                required: true,
                provider: 'turnstile',
                site_key: 'site-1',
              },
            },
            password_reset: { enabled: false, methods: [] },
          }),
        startAuthenticatedFlow: () =>
          Promise.resolve<AuthnFlowState>({
            seq: 1,
            state: 'second',
            next: {
              kind: 'factor',
              factors: [{ id: 'f1', kind: 'sms', label: 'Phone' }],
            },
          }),
        answerFlow: (request) => {
          requests.push({
            step: request.step,
            input: request.input,
            human_proof: request.human_proof,
          })
          return Promise.resolve<AuthnFlowState>({
            seq: 2,
            state: 'second',
            next: {
              kind: 'challenge',
              fields: [{ name: 'code', kind: 'code', required: true }],
            },
          })
        },
      }
      renderWithProviders(
        <StepUpChallenge
          open
          session={session}
          onSuccess={vi.fn()}
          onCancel={() => undefined}
        />,
      )
      const user = userEvent.setup()
      // The block is not on screen before anything asks for a send: the
      // factor choice is what asks for the proof.
      const sms = await screen.findByRole('button', {
        name: zhCN.stepUp.factorKind.sms,
      })
      expect(fake.widgets).toHaveLength(0)
      await user.click(sms)

      // Asking for the SMS answer renders the block above the factor choice,
      // and the control that asked gives way while the proof is outstanding:
      // no request leaves until the widget yields its single-use token.
      await waitFor(() => expect(fake.widgets).toHaveLength(1))
      expect(fake.widgets[0]?.siteKey).toBe('site-1')
      expect(
        screen.getByRole('button', { name: zhCN.stepUp.factorKind.sms }),
      ).toHaveAttribute('aria-disabled', 'true')
      expect(requests).toHaveLength(0)

      act(() => {
        fake.token('token-1')
      })
      await waitFor(() => expect(requests).toHaveLength(1))
      expect(requests[0]).toMatchObject({
        step: 'factor',
        input: { factor: 'f1' },
        human_proof: 'token-1',
      })
    } finally {
      fake.restore()
    }
  })

  it('resends the chosen factor alongside the code of its challenge', async () => {
    const user = userEvent.setup()
    const { session, answers } = flowSession()
    const onSuccess = vi.fn()
    renderWithProviders(
      <StepUpChallenge
        open
        session={session}
        onSuccess={onSuccess}
        onCancel={() => undefined}
      />,
    )
    // The flow offers the factor; choosing it starts the code challenge.
    await user.click(
      await screen.findByRole('button', { name: zhCN.stepUp.factorKind.totp }),
    )
    await user.type(await screen.findByLabelText(/code/), '123456')
    await user.click(screen.getByRole('button', { name: zhCN.stepUp.submit }))
    await waitForCondition(() => {
      expect(onSuccess).toHaveBeenCalledTimes(1)
    })
    // The factor-choice answer carries the id; the code answer carries the
    // same id again beside the code.
    expect(answers).toEqual([{ factor: 'f1' }, { code: '123456', factor: 'f1' }])
  })

  it('answers a first-factor password under the answer step when the account already holds factors', async () => {
    // A factor registered during the session does not put the flow at the
    // factor step: the server proves the password again first. The dialog
    // did not ask for a first-factor re-proof, yet this challenge is the
    // first factor, so its answer travels under `answer`.
    bindFactors([ACTIVE_FACTOR])
    const user = userEvent.setup()
    const { session, answers } = firstFactorSession()
    renderWithProviders(
      <StepUpChallenge
        open
        session={session}
        onSuccess={vi.fn()}
        onCancel={() => undefined}
      />,
    )

    await user.type(await screen.findByLabelText(/password/), 'secret')
    await user.click(screen.getByRole('button', { name: zhCN.stepUp.submit }))

    await waitFor(() => expect(answers).toHaveLength(1))
    expect(answers[0]).toMatchObject({
      step: 'answer',
      input: { password: 'secret' },
    })
  })

  it('answers the ready step on its own without a second click', async () => {
    const user = userEvent.setup()
    bindFactors([ACTIVE_FACTOR])
    const { session, answers } = readySession()
    const onSuccess = vi.fn()
    renderWithProviders(
      <StepUpChallenge
        open
        session={session}
        onSuccess={onSuccess}
        onCancel={() => undefined}
      />,
    )

    // The factor proof parks the flow on its ready `complete` step, a bare
    // confirmation that collects nothing. The dialog submits it on its own:
    // the elevation lands without a second click on the same control.
    await user.click(
      await screen.findByRole('button', { name: zhCN.stepUp.factorKind.totp }),
    )
    await user.type(await screen.findByLabelText(/code/), '123456')
    await user.click(screen.getByRole('button', { name: zhCN.stepUp.submit }))
    await waitForCondition(() => expect(onSuccess).toHaveBeenCalledTimes(1))

    expect(answers.map((answer) => answer.step)).toEqual([
      'factor',
      'factor',
      'complete',
    ])
  })

  it('falls back to the confirmation button when the automatic answer is refused', async () => {
    const user = userEvent.setup()
    bindFactors([ACTIVE_FACTOR])
    let completions = 0
    const { session, answers } = readySession(() => {
      completions += 1
      if (completions === 1) {
        return Promise.reject({
          status: 401,
          code: 'authn.login_failed',
          message: 'refused',
          auth: true,
        })
      }
      return Promise.resolve<AuthnFlowState>({
        state: 'authenticated',
        session: { id: 'sess-1', expires_at: '2026-10-17T00:00:00Z' },
      })
    })
    const onSuccess = vi.fn()
    renderWithProviders(
      <StepUpChallenge
        open
        session={session}
        onSuccess={onSuccess}
        onCancel={() => undefined}
      />,
    )
    await user.click(
      await screen.findByRole('button', { name: zhCN.stepUp.factorKind.totp }),
    )
    await user.type(await screen.findByLabelText(/code/), '123456')
    await user.click(screen.getByRole('button', { name: zhCN.stepUp.submit }))

    // The automatic completion is refused: its code renders and the step's
    // own confirmation button comes back for a manual retry.
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.errors.authn.login_failed)
    expect(onSuccess).not.toHaveBeenCalled()

    await user.click(screen.getByRole('button', { name: zhCN.stepUp.submit }))
    await waitForCondition(() => expect(onSuccess).toHaveBeenCalledTimes(1))
    expect(
      answers.filter((answer) => answer.step === 'complete'),
    ).toHaveLength(2)
  })

  it('holds the submit for the wait a throttled answer stated', async () => {
    // A throttled answer arrives as the ratelimit layer's 429 with a
    // Retry-After. The dialog names the wait, keeps the step form but
    // disables its submit until the interval elapses, then clears the
    // failure and makes the step answerable again.
    vi.useFakeTimers({ shouldAdvanceTime: true })
    try {
      bindFactors([ACTIVE_FACTOR])
      let answered = 0
      const session: AuthSession = {
        ...flowSession().session,
        answerFlow: () => {
          answered += 1
          if (answered === 1) {
            return Promise.resolve<AuthnFlowState>({
              seq: 2,
              state: 'second',
              next: {
                kind: 'challenge',
                fields: [{ name: 'code', kind: 'code', required: true }],
              },
            })
          }
          return Promise.reject(
            new ApiError({
              status: 429,
              code: 'ratelimit.throttled',
              attempts: 1,
              retryAfterMs: 30_000,
            }),
          )
        },
      }
      const user = userEvent.setup({
        advanceTimers: vi.advanceTimersByTimeAsync,
      })
      renderWithProviders(
        <StepUpChallenge
          open
          session={session}
          onSuccess={() => undefined}
          onCancel={() => undefined}
        />,
      )

      await user.click(
        await screen.findByRole('button', {
          name: zhCN.stepUp.factorKind.totp,
        }),
      )
      await user.type(await screen.findByLabelText(/code/), '123456')
      await user.click(
        screen.getByRole('button', { name: zhCN.stepUp.submit }),
      )

      const alert = await screen.findByRole('alert')
      // The live region carries only the code's static sentence, so a wait
      // that ticks does not re-announce the whole banner.
      expect(alert).toHaveTextContent(zhCN.errors.ratelimit.throttled)
      // The countdown is a separate line, outside the alert and aria-live off.
      const countdown = screen.getByText(
        zhCN.throttle.countdown.replace('{{seconds}}', '30'),
      )
      expect(countdown.closest('[role="alert"]')).toBeNull()
      expect(countdown.getAttribute('aria-live')).toBe('off')

      // One tick later the announced text is unchanged; only the countdown
      // line has moved.
      await act(async () => {
        await vi.advanceTimersByTimeAsync(1_000)
      })
      expect(screen.getByRole('alert')).toHaveTextContent(
        zhCN.errors.ratelimit.throttled,
      )
      expect(
        screen.getByText(zhCN.throttle.countdown.replace('{{seconds}}', '29')),
      ).toBeTruthy()

      const submit = screen.getByRole('button', {
        name: zhCN.stepUp.submit,
      })
      expect(submit).toHaveAttribute('aria-disabled', 'true')
      // The held control points at the countdown text already on the page
      // instead of repeating the reason.
      expect(submit).toHaveAccessibleDescription(
        new RegExp(
          zhCN.throttle.countdown.replace('{{seconds}}', '29'),
        ),
      )

      await act(async () => {
        await vi.advanceTimersByTimeAsync(30_000)
      })
      await waitFor(() => {
        expect(
          screen.getByRole('button', {
            name: zhCN.stepUp.submit,
          }),
        ).not.toHaveAttribute('aria-disabled', 'true')
      })
      // The wait is spent, so the non-live countdown is gone and the banner
      // stays the code's static sentence.
      expect(
        screen.queryByText(zhCN.throttle.countdown.replace('{{seconds}}', '1')),
      ).toBeNull()
      expect(screen.getByRole('alert')).toHaveTextContent(
        zhCN.errors.ratelimit.throttled,
      )
    } finally {
      vi.useRealTimers()
    }
  })

  it('describes the restart control with the wait a throttled start stated', async () => {
    // A throttled start leaves no step to retry, so the restart is the whole
    // surface; it is held for the wait and names the countdown already on
    // the page as its reason.
    vi.useFakeTimers({ shouldAdvanceTime: true })
    try {
      bindFactors([ACTIVE_FACTOR])
      const session = refusingStartSession(
        new ApiError({
          status: 429,
          code: 'ratelimit.throttled',
          attempts: 1,
          retryAfterMs: 30_000,
        }),
      )
      renderWithProviders(
        <StepUpChallenge
          open
          session={session}
          onSuccess={() => undefined}
          onCancel={() => undefined}
        />,
      )

      const restart = await screen.findByRole('button', {
        name: zhCN.stepUp.restart,
      })
      expect(restart).toHaveAttribute('aria-disabled', 'true')
      expect(restart).toHaveAccessibleDescription(
        new RegExp(zhCN.throttle.countdown.replace('{{seconds}}', '\\d+')),
      )
    } finally {
      vi.useRealTimers()
    }
  })

  it('answers a passkey step-up under the factor step, driven by its options', async () => {
    const requests: Array<{ step: string | undefined; input?: Readonly<Record<string, string>> }> = []
    const session: AuthSession = {
      ...flowSession().session,
      startAuthenticatedFlow: () =>
        Promise.resolve<AuthnFlowState>({
          seq: 1,
          state: 'second',
          next: {
            kind: 'webauthn',
            options: { challenge: 'Y2hhbGxlbmdl', allowCredentials: [] },
          },
        }),
      answerFlow: (request) => {
        requests.push({ step: request.step, input: request.input })
        return Promise.resolve<AuthnFlowState>({
          state: 'authenticated',
          session: { id: 'sess-1', expires_at: '2026-10-17T00:00:00Z' },
        })
      },
    }
    const get = vi.fn().mockResolvedValue({
      id: 'AQID',
      rawId: new Uint8Array([1, 2, 3]).buffer,
      type: 'public-key',
      response: {
        clientDataJSON: new Uint8Array([4]).buffer,
        authenticatorData: new Uint8Array([5]).buffer,
        signature: new Uint8Array([6]).buffer,
        userHandle: null,
      },
      getClientExtensionResults: () => ({}),
      authenticatorAttachment: 'platform',
    })
    const original = Object.getOwnPropertyDescriptor(window.navigator, 'credentials')
    Object.defineProperty(window.navigator, 'credentials', {
      value: { get, create: vi.fn() },
      configurable: true,
    })
    try {
      const onSuccess = vi.fn()
      renderWithProviders(
        <StepUpChallenge
          open
          session={session}
          onSuccess={onSuccess}
          onCancel={() => undefined}
        />,
      )
      // The assertion step starts its ceremony on arrival -- choosing the
      // passkey factor was the only click -- and its answer travels under
      // the second factor's step (`factor`), not the enrollment one.
      await waitForCondition(() => expect(onSuccess).toHaveBeenCalledTimes(1))
    } finally {
      if (original === undefined) {
        delete (window.navigator as { credentials?: unknown }).credentials
      } else {
        Object.defineProperty(window.navigator, 'credentials', original)
      }
    }
    expect(requests).toHaveLength(1)
    expect(requests[0]?.step).toBe('factor')
    expect(requests[0]?.input?.credential).toContain('AQID')
  })

  it('names the missing second factor instead of starting a flow that cannot come', async () => {
    const user = userEvent.setup()
    const calls = bindFactors([])
    const session = createStepUpSession()
    const startFlow = vi.spyOn(session, 'startAuthenticatedFlow')
    const onCancel = vi.fn()
    renderWithProviders(
      <StepUpChallenge
        open
        session={session}
        onSuccess={() => undefined}
        onCancel={onCancel}
      />,
    )

    // The caller's own factor read says the account has no active factor:
    // the dialog names that, rather than offering a re-login that can only
    // produce the same single-factor session. With no accountHref from the
    // host it renders no link at all -- the package holds no host path.
    expect(await screen.findByText(zhCN.stepUp.noFactor)).toBeInTheDocument()
    expect(screen.queryAllByRole('link')).toHaveLength(0)
    expect(
      screen.queryByRole('button', { name: zhCN.stepUp.relogin }),
    ).toBeNull()
    // Nothing is left to verify, so the flow is never started.
    expect(startFlow).not.toHaveBeenCalled()
    expect(calls.map((call) => `${call.method} ${call.path}`)).toEqual([
      'GET /api/v1/auth/factors',
    ])

    await user.click(screen.getByRole('button', { name: zhCN.common.cancel }))
    expect(onCancel).toHaveBeenCalledTimes(1)
  })

  it('uses the account settings destination the shell supplies', async () => {
    bindFactors([])
    renderWithProviders(
      <AdminAccountHrefProvider accountHref="/host/security">
        <StepUpChallenge
          open
          session={createStepUpSession()}
          onSuccess={() => undefined}
          onCancel={() => undefined}
        />
      </AdminAccountHrefProvider>,
    )

    // The destination is the host's, carried through the shell's context;
    // the dialog only renders it.
    expect(
      await screen.findByRole('link', { name: zhCN.stepUp.goToAccount }),
    ).toHaveAttribute('href', '/host/security')
  })

  it('renders a 400 start refusal as an ordinary error when a factor is registered', async () => {
    bindFactors([ACTIVE_FACTOR])
    renderWithProviders(
      <StepUpChallenge
        open
        session={refusingStartSession({
          status: 400,
          code: 'authn.bad_request',
          message: 'refused',
          auth: false,
        })}
        onSuccess={() => undefined}
        onCancel={() => undefined}
      />,
    )

    // A 400 is not, by itself, "the account has no factor": it is an
    // ordinary refusal and the dialog shows its code, not either of the
    // two explanations that would excuse the failure.
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.errors.authn.bad_request)
    expect(screen.queryByText(zhCN.stepUp.cannotElevate)).toBeNull()
    expect(screen.queryByText(zhCN.stepUp.noFactor)).toBeNull()
    // A 400 answered to a still-established session is the start request
    // itself being unusable; a restart would repeat it, so none is offered.
    expect(screen.queryByRole('button', { name: zhCN.stepUp.restart })).toBeNull()
  })

  it('does not replay a cancelled write when its start lands after the close', async () => {
    const user = userEvent.setup()
    bindFactors([ACTIVE_FACTOR])
    let settleStart!: (state: AuthnFlowState) => void
    const start = vi.fn<AuthSession['startAuthenticatedFlow']>(
      () =>
        new Promise<AuthnFlowState>((resolve) => {
          settleStart = resolve
        }),
    )
    const session: AuthSession = {
      ...createStepUpSession(),
      startAuthenticatedFlow: start,
    }
    function Harness({ onSuccess }: { readonly onSuccess: () => void }) {
      const [open, setOpen] = useState(true)
      return (
        <StepUpChallenge
          open={open}
          session={session}
          onSuccess={onSuccess}
          onCancel={() => setOpen(false)}
        />
      )
    }
    const onSuccess = vi.fn()
    renderWithProviders(<Harness onSuccess={onSuccess} />)

    // The start is in flight when the operator cancels: the dialog closes
    // and its driver unmounts.
    await waitForCondition(() => expect(start).toHaveBeenCalledTimes(1))
    await user.click(screen.getByRole('button', { name: zhCN.common.cancel }))
    await waitForCondition(() => expect(screen.queryByRole('dialog')).toBeNull())

    // The start then settles elevated. The host must not replay the write
    // the operator cancelled, so the elevation is dropped.
    settleStart({
      state: 'authenticated',
      session: { id: 'sess-1', expires_at: '2026-10-17T00:00:00Z' },
    })
    await act(async () => {
      await Promise.resolve()
    })
    expect(onSuccess).not.toHaveBeenCalled()
  })

  it('sends no abandon request when a refused start is closed', async () => {
    const user = userEvent.setup()
    bindFactors([ACTIVE_FACTOR])
    const { session, abandon } = withRecordedAbandon(
      refusingStartSession({
        status: 400,
        code: 'authn.bad_request',
        message: 'refused',
        auth: false,
      }),
    )
    function Harness() {
      const [open, setOpen] = useState(true)
      return (
        <StepUpChallenge
          open={open}
          session={session}
          onSuccess={() => undefined}
          onCancel={() => setOpen(false)}
        />
      )
    }
    renderWithProviders(<Harness />)

    // The start was refused, so the server created no flow: closing the
    // dialog must not invent a binding to release.
    expect(await screen.findByRole('alert')).toHaveTextContent(
      zhCN.errors.authn.bad_request,
    )
    await user.click(screen.getByRole('button', { name: zhCN.common.cancel }))
    await waitForCondition(() => expect(screen.queryByRole('dialog')).toBeNull())
    await act(async () => {
      await Promise.resolve()
    })
    expect(abandon).not.toHaveBeenCalled()
  })

  it('sends no abandon request when a settled elevation is closed', async () => {
    const user = userEvent.setup()
    bindFactors([ACTIVE_FACTOR])
    const { session, abandon } = withRecordedAbandon(flowSession().session)
    const onSuccess = vi.fn()
    function Harness() {
      const [open, setOpen] = useState(true)
      return (
        <StepUpChallenge
          open={open}
          session={session}
          onSuccess={() => {
            onSuccess()
            setOpen(false)
          }}
          onCancel={() => setOpen(false)}
        />
      )
    }
    renderWithProviders(<Harness />)

    // The flow settles the elevation and the host closes the dialog, as it
    // does after a won challenge.
    await user.click(
      await screen.findByRole('button', { name: zhCN.stepUp.factorKind.totp }),
    )
    await user.type(await screen.findByLabelText(/code/), '123456')
    await user.click(screen.getByRole('button', { name: zhCN.stepUp.submit }))
    await waitForCondition(() => expect(onSuccess).toHaveBeenCalledTimes(1))

    // The server consumed the settled flow: the closed dialog has no
    // binding to release.
    await waitForCondition(() => expect(screen.queryByRole('dialog')).toBeNull())
    await act(async () => {
      await Promise.resolve()
    })
    expect(abandon).not.toHaveBeenCalled()
  })

  it('abandons once when a still-open flow unmounts with the dialog', async () => {
    const user = userEvent.setup()
    bindFactors([ACTIVE_FACTOR])
    const base = createStepUpSession()
    const { session, abandon } = withRecordedAbandon({
      ...base,
      startAuthenticatedFlow: () =>
        Promise.resolve<AuthnFlowState>({
          seq: 1,
          state: 'second',
          next: {
            kind: 'challenge',
            fields: [{ name: 'code', kind: 'code', required: true }],
          },
        }),
    })
    function Harness() {
      const [open, setOpen] = useState(true)
      return (
        <StepUpChallenge
          open={open}
          session={session}
          onSuccess={() => undefined}
          onCancel={() => setOpen(false)}
        />
      )
    }
    renderWithProviders(<Harness />)

    // The flow is parked on its step when the operator closes the dialog:
    // the binding is live, so it is released exactly once -- not once per
    // layer that happens to unmount.
    await screen.findByLabelText(/code/)
    await user.click(screen.getByRole('button', { name: zhCN.common.cancel }))
    await waitForCondition(() => expect(screen.queryByRole('dialog')).toBeNull())
    await act(async () => {
      await Promise.resolve()
    })
    expect(abandon).toHaveBeenCalledTimes(1)
  })

  it('abandons the still-open flow once when a restart remounts the driver', async () => {
    const user = userEvent.setup()
    bindFactors([ACTIVE_FACTOR])
    const base = createStepUpSession()
    let starts = 0
    const { session, abandon } = withRecordedAbandon({
      ...base,
      startAuthenticatedFlow: () => {
        starts += 1
        return Promise.resolve<AuthnFlowState>({
          seq: starts,
          state: 'second',
          next: {
            kind: 'challenge',
            fields: [{ name: 'code', kind: 'code', required: true }],
          },
        })
      },
      answerFlow: () =>
        Promise.reject({
          status: 401,
          code: 'authn.flow_invalid',
          message: 'gone',
          auth: true,
        }),
    })
    renderWithProviders(
      <StepUpChallenge
        open
        session={session}
        onSuccess={() => undefined}
        onCancel={() => undefined}
      />,
    )

    // The answer says the flow is gone, which offers a restart. Nothing is
    // left to release for it: an abandon here would delete whatever flow
    // the cookie names by then, which is the fresh one the restart opens.
    await user.type(await screen.findByLabelText(/code/), '123456')
    await user.click(screen.getByRole('button', { name: zhCN.stepUp.submit }))
    await screen.findByRole('button', { name: zhCN.stepUp.restart })
    await user.click(screen.getByRole('button', { name: zhCN.stepUp.restart }))
    await act(async () => {
      await Promise.resolve()
    })
    expect(abandon).not.toHaveBeenCalled()
    expect(starts).toBe(2)
  })

  it('renders a failed start as its code with a restart and a cancel', async () => {
    const user = userEvent.setup()
    bindFactors([ACTIVE_FACTOR])
    const base = createStepUpSession()
    const start = vi
      .fn<AuthSession['startAuthenticatedFlow']>()
      .mockRejectedValueOnce({
        status: 503,
        code: 'authn.unavailable',
        message: 'down',
        auth: false,
      })
      .mockImplementation(base.startAuthenticatedFlow)
    const session: AuthSession = { ...base, startAuthenticatedFlow: start }
    const onSuccess = vi.fn()
    const onCancel = vi.fn()
    renderWithProviders(
      <StepUpChallenge
        open
        session={session}
        onSuccess={onSuccess}
        onCancel={onCancel}
      />,
    )

    // A failed start leaves no step to answer: the code's banner, a
    // restart and a cancel are the whole surface -- not the description
    // of a verification that never came.
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.errors.authn.unavailable)
    expect(screen.queryByText(zhCN.stepUp.description)).toBeNull()
    expect(screen.queryByRole('button', { name: zhCN.stepUp.submit })).toBeNull()
    expect(
      screen.getByRole('button', { name: zhCN.stepUp.restart }),
    ).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: zhCN.stepUp.restart }))
    // The restart drives a fresh flow locally; the second start settles
    // the elevation and the refused write is replayed.
    await waitForCondition(() => expect(onSuccess).toHaveBeenCalledTimes(1))
    expect(start).toHaveBeenCalledTimes(2)

    await user.click(screen.getByRole('button', { name: zhCN.common.cancel }))
    expect(onCancel).toHaveBeenCalledTimes(1)
  })

  it('renders a flow_invalid answer as its code with a restart and a cancel', async () => {
    const user = userEvent.setup()
    bindFactors([ACTIVE_FACTOR])
    const base = createStepUpSession()
    const start = vi
      .fn<AuthSession['startAuthenticatedFlow']>()
      .mockResolvedValueOnce({
        seq: 1,
        state: 'second',
        next: {
          kind: 'challenge',
          fields: [{ name: 'code', kind: 'code', required: true }],
        },
      })
      .mockImplementation(base.startAuthenticatedFlow)
    const session: AuthSession = {
      ...base,
      startAuthenticatedFlow: start,
      answerFlow: () =>
        Promise.reject({
          status: 401,
          code: 'authn.flow_invalid',
          message: 'gone',
          auth: true,
        }),
    }
    const onSuccess = vi.fn()
    renderWithProviders(
      <StepUpChallenge
        open
        session={session}
        onSuccess={onSuccess}
        onCancel={() => undefined}
      />,
    )

    await user.type(await screen.findByLabelText(/code/), '123456')
    await user.click(screen.getByRole('button', { name: zhCN.stepUp.submit }))

    // The flow is gone (expired, consumed, nothing left to answer): the
    // step form is replaced by the code's banner and a restart, because
    // answering the same step again cannot succeed.
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.errors.authn.flow_invalid)
    expect(screen.queryByText(zhCN.stepUp.description)).toBeNull()
    expect(screen.queryByLabelText('code')).toBeNull()
    expect(
      screen.getByRole('button', { name: zhCN.stepUp.restart }),
    ).toBeInTheDocument()
    expect(screen.getByRole('button', { name: zhCN.common.cancel })).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: zhCN.stepUp.restart }))
    await waitForCondition(() => expect(onSuccess).toHaveBeenCalledTimes(1))
    expect(start).toHaveBeenCalledTimes(2)
  })

  it('keeps a failed passkey ceremony and a server failure to one banner', async () => {
    const user = userEvent.setup()
    bindFactors([ACTIVE_FACTOR])
    const base = createStepUpSession()
    const session: AuthSession = {
      ...base,
      startAuthenticatedFlow: () =>
        Promise.resolve<AuthnFlowState>({
          seq: 1,
          state: 'second',
          next: {
            kind: 'webauthn',
            options: { challenge: 'Y2hhbGxlbmdl', allowCredentials: [] },
          },
        }),
      answerFlow: () =>
        Promise.reject({
          status: 401,
          code: 'authn.login_failed',
          message: 'refused',
          auth: true,
        }),
    }
    const credential = {
      id: 'AQID',
      rawId: new Uint8Array([1, 2, 3]).buffer,
      type: 'public-key',
      response: {
        clientDataJSON: new Uint8Array([4]).buffer,
        authenticatorData: new Uint8Array([5]).buffer,
        signature: new Uint8Array([6]).buffer,
        userHandle: null,
      },
      getClientExtensionResults: () => ({}),
      authenticatorAttachment: 'platform',
    }
    const get = vi
      .fn()
      .mockResolvedValueOnce(credential)
      .mockRejectedValueOnce(
        Object.assign(new Error('dismissed'), { name: 'NotAllowedError' }),
      )
    const original = Object.getOwnPropertyDescriptor(window.navigator, 'credentials')
    Object.defineProperty(window.navigator, 'credentials', {
      value: { get, create: vi.fn() },
      configurable: true,
    })
    try {
      renderWithProviders(
        <StepUpChallenge
          open
          session={session}
          onSuccess={() => undefined}
          onCancel={() => undefined}
        />,
      )

      // The arrival ceremony produces a credential and the server refuses
      // it: the server banner stands alone.
      const serverAlert = await screen.findByRole('alert')
      expect(serverAlert).toHaveTextContent(zhCN.errors.authn.login_failed)
      expect(screen.queryByText(zhCN.stepUp.passkeyCancelled)).toBeNull()

      // The next ceremony is dismissed: the newer failure replaces the
      // server banner, so the two are never on screen together.
      await user.click(screen.getByRole('button', { name: zhCN.stepUp.submit }))
      await waitForCondition(() => {
        expect(screen.getAllByRole('alert')).toHaveLength(1)
      })
      const alerts = screen.getAllByRole('alert')
      expect(alerts).toHaveLength(1)
      expect(alerts[0]).toHaveTextContent(zhCN.stepUp.passkeyCancelled)
      expect(screen.queryByText(zhCN.errors.authn.login_failed)).toBeNull()
    } finally {
      if (original === undefined) {
        delete (window.navigator as { credentials?: unknown }).credentials
      } else {
        Object.defineProperty(window.navigator, 'credentials', original)
      }
    }
  })

  it('has no axe violations on the terminal failure surface', async () => {
    bindFactors([ACTIVE_FACTOR])
    const base = createStepUpSession()
    const session: AuthSession = {
      ...base,
      startAuthenticatedFlow: () =>
        Promise.reject({
          status: 503,
          code: 'authn.unavailable',
          message: 'down',
          auth: false,
        }),
    }
    renderWithProviders(
      <StepUpChallenge
        open
        session={session}
        onSuccess={() => undefined}
        onCancel={() => undefined}
      />,
    )
    await screen.findByRole('alert')
    await expectNoAxeViolations()
  })

  it('offers a re-login when the account has factors none of which may strengthen the session', async () => {
    const user = userEvent.setup()
    bindFactors([ACTIVE_FACTOR])
    const logout = vi.fn(() => Promise.resolve())
    const onCancel = vi.fn()
    renderWithProviders(
      <StepUpChallenge
        open
        session={refusingStartSession(
          {
            status: 401,
            code: 'authn.insufficient_user_authentication',
            message: 'refused',
            auth: true,
          },
          logout,
        )}
        onSuccess={() => undefined}
        onCancel={onCancel}
      />,
    )

    // Factors exist but none of them may strengthen this session: the
    // deployment declines the step-up, and a fresh sign-in is the way to a
    // session whose authentication time lets those factors count.
    expect(
      await screen.findByText(zhCN.stepUp.cannotElevate),
    ).toBeInTheDocument()
    expect(screen.queryByText(zhCN.stepUp.noFactor)).toBeNull()
    await user.click(screen.getByRole('button', { name: zhCN.stepUp.relogin }))
    expect(logout).toHaveBeenCalledTimes(1)
    expect(onCancel).toHaveBeenCalledTimes(1)
  })
})
