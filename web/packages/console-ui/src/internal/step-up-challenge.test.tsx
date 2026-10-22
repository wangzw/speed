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

import { useState } from 'react'
import { describe, expect, it, vi } from 'vitest'
import { act, screen } from '@testing-library/react'
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
import { expectNoAxeViolations } from '../../test-utils/axe.js'
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
        signup: { enabled: false, password: { enabled: false } },
        mfa: { factors: [] },
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
      await user.click(await screen.findByRole('button', { name: zhCN.stepUp.submit }))
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
    await vi.waitFor(() => expect(start).toHaveBeenCalledTimes(1))
    await user.click(screen.getByRole('button', { name: zhCN.common.cancel }))
    await vi.waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())

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
    await vi.waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
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
    await vi.waitFor(() => expect(onSuccess).toHaveBeenCalledTimes(1))

    // The server consumed the settled flow: the closed dialog has no
    // binding to release.
    await vi.waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
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
    await vi.waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
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

    // The answer says the flow is gone, which offers a restart. The old
    // driver left its flow open (an answer refusal is not a settlement),
    // so remounting releases it -- exactly once, while the new flow lives.
    await user.type(await screen.findByLabelText(/code/), '123456')
    await user.click(screen.getByRole('button', { name: zhCN.stepUp.submit }))
    await screen.findByRole('button', { name: zhCN.stepUp.restart })
    await user.click(screen.getByRole('button', { name: zhCN.stepUp.restart }))
    await act(async () => {
      await Promise.resolve()
    })
    expect(abandon).toHaveBeenCalledTimes(1)
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
    await vi.waitFor(() => expect(onSuccess).toHaveBeenCalledTimes(1))
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
    await vi.waitFor(() => expect(onSuccess).toHaveBeenCalledTimes(1))
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

      // The ceremony produces a credential and the server refuses it: the
      // server banner stands alone.
      await user.click(await screen.findByRole('button', { name: zhCN.stepUp.submit }))
      const serverAlert = await screen.findByRole('alert')
      expect(serverAlert).toHaveTextContent(zhCN.errors.authn.login_failed)
      expect(screen.queryByText(zhCN.stepUp.passkeyCancelled)).toBeNull()

      // The next ceremony is dismissed: the newer failure replaces the
      // server banner, so the two are never on screen together.
      await user.click(screen.getByRole('button', { name: zhCN.stepUp.submit }))
      await vi.waitFor(() => {
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
