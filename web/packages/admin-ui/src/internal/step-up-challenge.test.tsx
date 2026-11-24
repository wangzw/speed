import { useState } from 'react'
import { describe, expect, it, vi } from 'vitest'
import { restoreRealTimers } from '@speed/test-utils/timers'
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

const ACTIVE_FACTOR = {
  id: 'factor-1',
  kind: 'totp',
  label: 'Authenticator app',
  status: 'active',
  created_at: '2026-01-01T00:00:00Z',
}

function bindFactors(factors: readonly unknown[]): ReturnType<typeof bindAdminRequest> {
  return bindAdminRequest((call) => {
    if (call.method === 'GET' && call.path === '/api/v1/auth/factors') {
      return { factors }
    }
    throw new Error(`unexpected ${call.method} ${call.path}`)
  })
}

function withRecordedAbandon(session: AuthSession): {
  readonly session: AuthSession
  readonly abandon: ReturnType<typeof vi.fn>
} {
  const abandon = vi.fn<AuthSession['abandonFlow']>(() => Promise.resolve())
  return { session: { ...session, abandonFlow: abandon }, abandon }
}

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

  it('marks the page behind the open dialog inert', async () => {
    bindFactors([ACTIVE_FACTOR])
    const { container } = renderWithProviders(
      <StepUpChallenge
        open
        session={flowSession().session}
        onSuccess={vi.fn()}
        onCancel={vi.fn()}
      />,
    )
    const dialog = await screen.findByRole('dialog')
    expect(container).toHaveAttribute('inert')
    expect(dialog).not.toHaveAttribute('inert')
  })

  it('sends the host’s path as the flow’s return_to', async () => {
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
      const sms = await screen.findByRole('button', {
        name: zhCN.stepUp.factorKind.sms,
      })
      expect(fake.widgets).toHaveLength(0)
      await user.click(sms)

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
    await user.click(
      await screen.findByRole('button', { name: zhCN.stepUp.factorKind.totp }),
    )
    await user.type(await screen.findByLabelText(/code/), '123456')
    await user.click(screen.getByRole('button', { name: zhCN.stepUp.submit }))
    await waitForCondition(() => {
      expect(onSuccess).toHaveBeenCalledTimes(1)
    })
    expect(answers).toEqual([{ factor: 'f1' }, { code: '123456', factor: 'f1' }])
  })

  it('answers a first-factor password under the answer step when the account already holds factors', async () => {
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
      expect(alert).toHaveTextContent(zhCN.errors.ratelimit.throttled)
      // The countdown is a separate line, outside the alert and aria-live off.
      const countdown = screen.getByText(
        zhCN.throttle.countdown.replace('{{seconds}}', '30'),
      )
      expect(countdown.closest('[role="alert"]')).toBeNull()
      expect(countdown.getAttribute('aria-live')).toBe('off')

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
      expect(
        screen.queryByText(zhCN.throttle.countdown.replace('{{seconds}}', '1')),
      ).toBeNull()
      expect(screen.getByRole('alert')).toHaveTextContent(
        zhCN.errors.ratelimit.throttled,
      )
    } finally {
      await restoreRealTimers()
    }
  })

  it('describes the restart control with the wait a throttled start stated', async () => {
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
      await restoreRealTimers()
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

    expect(await screen.findByText(zhCN.stepUp.noFactor)).toBeInTheDocument()
    expect(screen.queryAllByRole('link')).toHaveLength(0)
    expect(
      screen.queryByRole('button', { name: zhCN.stepUp.relogin }),
    ).toBeNull()
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

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.errors.authn.bad_request)
    expect(screen.queryByText(zhCN.stepUp.cannotElevate)).toBeNull()
    expect(screen.queryByText(zhCN.stepUp.noFactor)).toBeNull()
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

    await waitForCondition(() => expect(start).toHaveBeenCalledTimes(1))
    await user.click(screen.getByRole('button', { name: zhCN.common.cancel }))
    await waitForCondition(() => expect(screen.queryByRole('dialog')).toBeNull())

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

    await user.click(
      await screen.findByRole('button', { name: zhCN.stepUp.factorKind.totp }),
    )
    await user.type(await screen.findByLabelText(/code/), '123456')
    await user.click(screen.getByRole('button', { name: zhCN.stepUp.submit }))
    await waitForCondition(() => expect(onSuccess).toHaveBeenCalledTimes(1))

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

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.errors.authn.unavailable)
    expect(screen.queryByText(zhCN.stepUp.description)).toBeNull()
    expect(screen.queryByRole('button', { name: zhCN.stepUp.submit })).toBeNull()
    expect(
      screen.getByRole('button', { name: zhCN.stepUp.restart }),
    ).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: zhCN.stepUp.restart }))
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

      const serverAlert = await screen.findByRole('alert')
      expect(serverAlert).toHaveTextContent(zhCN.errors.authn.login_failed)
      expect(screen.queryByText(zhCN.stepUp.passkeyCancelled)).toBeNull()

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

    expect(
      await screen.findByText(zhCN.stepUp.cannotElevate),
    ).toBeInTheDocument()
    expect(screen.queryByText(zhCN.stepUp.noFactor)).toBeNull()
    await user.click(screen.getByRole('button', { name: zhCN.stepUp.relogin }))
    expect(logout).toHaveBeenCalledTimes(1)
    expect(onCancel).toHaveBeenCalledTimes(1)
  })
})
