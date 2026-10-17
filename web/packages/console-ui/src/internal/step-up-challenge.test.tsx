/**
 * StepUpChallenge: the re-authentication dialog of the step-up-gated
 * console writes.
 *
 * The dialog drives the session's step-up flow (auth-core's useStepUp)
 * and forwards each answer; the assertion here is the one wire detail the
 * backend requires and a naive renderer misses: the second factor's code
 * challenge is answered under the `factor` step with the chosen factor's
 * identifier resent alongside the code (pkg/authn/login's applyFactor
 * re-reads the factor from the answer).
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

import { describe, expect, it, vi } from 'vitest'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type {
  AuthSession,
  AuthSessionListener,
  AuthSnapshot,
  AuthnFlowState,
} from '@speed/auth-core'
import zhCN from '../locales/zh-CN.json' with { type: 'json' }
import { ConsoleAccountHrefProvider } from './account-href.js'
import { StepUpChallenge } from './step-up-challenge.js'
import { renderWithProviders } from '../../test-utils/render.js'
import { bindConsoleRequest } from '../../test-utils/request-rig.js'
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
        signup: { enabled: false },
        mfa: { factors: [] },
      }),
    discoverSso: () => Promise.resolve([]),
    startFlow: () => Promise.reject(new Error('unused')),
    logout: () => Promise.reject(new Error('unused')),
    restore: () => Promise.resolve(false),
    refresh: () => Promise.resolve(false),
    setPermissionSet: () => undefined,
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
function bindFactors(factors: readonly unknown[]): ReturnType<typeof bindConsoleRequest> {
  return bindConsoleRequest((call) => {
    if (call.method === 'GET' && call.path === '/auth/factors') {
      return { factors }
    }
    throw new Error(`unexpected ${call.method} ${call.path}`)
  })
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
    await user.click(await screen.findByRole('button', { name: 'totp' }))
    await user.type(await screen.findByLabelText(/code/), '123456')
    await user.click(screen.getByRole('button', { name: '验证' }))
    await vi.waitFor(() => {
      expect(onSuccess).toHaveBeenCalledTimes(1)
    })
    // The factor-choice answer carries the id; the code answer carries the
    // same id again beside the code.
    expect(answers).toEqual([{ factor: 'f1' }, { code: '123456', factor: 'f1' }])
  })

  it('answers a passkey step-up under the factor step, driven by its options', async () => {
    const user = userEvent.setup()
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
      // The passkey step drives the ceremony; its answer travels under the
      // second factor's step (`factor`), not the enrollment one.
      await user.click(await screen.findByRole('button', { name: '验证' }))
      await vi.waitFor(() => expect(onSuccess).toHaveBeenCalledTimes(1))
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
    expect(await screen.findByText(/尚未配置两步验证/)).toBeInTheDocument()
    expect(screen.queryAllByRole('link')).toHaveLength(0)
    expect(
      screen.queryByRole('button', { name: '重新登录' }),
    ).toBeNull()
    // Nothing is left to verify, so the flow is never started.
    expect(startFlow).not.toHaveBeenCalled()
    expect(calls.map((call) => `${call.method} ${call.path}`)).toEqual([
      'GET /auth/factors',
    ])

    await user.click(screen.getByRole('button', { name: '取消' }))
    expect(onCancel).toHaveBeenCalledTimes(1)
  })

  it('uses the account settings destination the shell supplies', async () => {
    bindFactors([])
    renderWithProviders(
      <ConsoleAccountHrefProvider accountHref="/host/security">
        <StepUpChallenge
          open
          session={createStepUpSession()}
          onSuccess={() => undefined}
          onCancel={() => undefined}
        />
      </ConsoleAccountHrefProvider>,
    )

    // The destination is the host's, carried through the shell's context;
    // the dialog only renders it.
    expect(
      await screen.findByRole('link', { name: '前往账户设置' }),
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
    expect(screen.queryByText(/本部署无法完成此次升级/)).toBeNull()
    expect(screen.queryByText(/尚未配置两步验证/)).toBeNull()
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
      await screen.findByText(/本部署无法完成此次升级/),
    ).toBeInTheDocument()
    expect(screen.queryByText(/尚未配置两步验证/)).toBeNull()
    await user.click(screen.getByRole('button', { name: '重新登录' }))
    expect(logout).toHaveBeenCalledTimes(1)
    expect(onCancel).toHaveBeenCalledTimes(1)
  })
})
