/**
 * StepUpChallenge checks the dialog driving one step-up flow.
 */

import { useState } from 'react'
import { describe, expect, it, vi } from 'vitest'
import { restoreRealTimers } from '@speed/test-utils/timers'
import { act, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { AuthSession, AuthSnapshot, AuthnFlowState } from '@speed/auth-core'
import type { AuthnProfileIdentifier } from '@speed/api-sdk'
import type { RequestFn, RequestOptions } from '@speed/api-client'
import { isApiError } from '@speed/api-client'
import { bindRequestFn } from '@speed/api-sdk/runtime'
import zhCN from '../locales/zh-CN.json' with { type: 'json' }
import {
  errorResponse,
  jsonResponse,
  makeRealClientRig,
  type RealCall,
  type RealResponder,
} from '../../test-utils/real-client.js'
import { renderWithProviders } from '../../test-utils/render.js'
import { captureRefreshWarning } from '../../test-utils/refresh-warning.js'
import { methodList } from '@speed/test-utils/session-harness'
import { installFakeTurnstile } from '@speed/test-utils/turnstile'
import { StepUpChallenge } from './step-up-challenge.js'

const FLOW_START = '/api/v1/auth/flows/authenticated'
const FLOW_ANSWER = '/api/v1/auth/flows/answer'

const ACTIVE_FACTOR = {
  id: 'factor-1',
  kind: 'totp',
  label: 'Authenticator app',
  status: 'active',
  created_at: '2026-01-01T00:00:00Z',
}

function sessionBody(
  options: {
    readonly methods?: readonly string[]
    readonly assurance?: 'none' | 'single' | 'multi'
    readonly ageMs?: number
  } = {},
): Record<string, unknown> {
  return {
    authenticated: true,
    session_id: 'session-1',
    account_id: 'account-1',
    methods: options.methods ?? ['pwd'],
    assurance: options.assurance ?? 'single',
    auth_time: new Date(Date.now() - (options.ageMs ?? 0)).toISOString(),
  }
}

function bindFactors(factors: readonly unknown[]): void {
  bindRequestFn((async (path: string, options?: RequestOptions) => {
    if ((options?.method ?? 'GET') === 'GET' && path === '/api/v1/auth/factors') {
      return { factors }
    }
    throw new Error(`unexpected ${options?.method ?? 'GET'} ${path}`)
  }) as RequestFn)
}

function makeStepUpResponder(overrides: {
  readonly codeAnswer?: () => Response
} = {}): RealResponder {
  return (call: RealCall) => {
    if (call.method === 'GET' && call.path === '/api/v1/auth/factors') {
      return jsonResponse(200, { factors: [ACTIVE_FACTOR] })
    }
    if (call.method === 'POST' && call.path === FLOW_START) {
      return jsonResponse(200, {
        seq: 1,
        state: 'second',
        next: {
          kind: 'factor',
          factors: [{ id: 'factor-1', kind: 'totp', label: 'Authenticator app' }],
        },
      })
    }
    if (call.method === 'POST' && call.path === FLOW_ANSWER) {
      const body = call.body as { seq: number; step: string; input?: Record<string, string> }
      if (body.seq === 1) {
        return jsonResponse(200, {
          seq: 2,
          state: 'second',
          next: { kind: 'challenge', fields: [{ name: 'code', kind: 'text' }] },
        })
      }
      if (overrides.codeAnswer !== undefined) {
        return overrides.codeAnswer()
      }
      return jsonResponse(200, {
        state: 'elevated',
        token_type: 'Bearer',
        access_token: 'access-2',
        expires_in: 3600,
      })
    }
    throw new Error(`unexpected ${call.method} ${call.path}`)
  }
}

function makeReadyAfterFirstFactorResponder(overrides: {
  readonly completeAnswer?: () => Response | Promise<Response>
} = {}): RealResponder {
  return (call: RealCall) => {
    if (call.method === 'GET' && call.path === '/api/v1/auth/factors') {
      return jsonResponse(200, { factors: [] })
    }
    if (call.method === 'GET' && call.path === '/api/v1/auth/session') {
      return jsonResponse(
        200,
        sessionBody({ methods: ['pwd'], ageMs: 6 * 60 * 1000 }),
      )
    }
    if (call.method === 'POST' && call.path === FLOW_START) {
      return jsonResponse(200, {
        seq: 1,
        state: 'first',
        next: {
          kind: 'challenge',
          fields: [
            { name: 'identifier', kind: 'text' },
            { name: 'password', kind: 'password', secret: true },
          ],
        },
      })
    }
    if (call.method === 'POST' && call.path === FLOW_ANSWER) {
      const body = call.body as { seq: number; step: string }
      if (body.step === 'complete') {
        return overrides.completeAnswer !== undefined
          ? overrides.completeAnswer()
          : jsonResponse(200, {
              state: 'elevated',
              token_type: 'Bearer',
              access_token: 'access-2',
              expires_in: 3600,
            })
      }
      return jsonResponse(200, {
        seq: 2,
        state: 'ready',
        next: { kind: 'complete' },
      })
    }
    if (
      call.method === 'DELETE' &&
      call.path === '/api/v1/auth/flows/current'
    ) {
      return new Response(null, { status: 204 })
    }
    throw new Error(`unexpected ${call.method} ${call.path}`)
  }
}

function makeFactorsRegisteredThisSessionResponder(): RealResponder {
  return (call: RealCall) => {
    if (call.method === 'GET' && call.path === '/api/v1/auth/factors') {
      return jsonResponse(200, { factors: [ACTIVE_FACTOR] })
    }
    if (call.method === 'POST' && call.path === FLOW_START) {
      return jsonResponse(200, {
        seq: 1,
        state: 'first',
        next: {
          kind: 'challenge',
          fields: [{ name: 'password', kind: 'password', secret: true }],
        },
      })
    }
    if (call.method === 'POST' && call.path === FLOW_ANSWER) {
      return jsonResponse(200, {
        seq: 2,
        state: 'second',
        next: {
          kind: 'factor',
          factors: [{ id: 'factor-1', kind: 'totp', label: 'Authenticator app' }],
        },
      })
    }
    if (
      call.method === 'DELETE' &&
      call.path === '/api/v1/auth/flows/current'
    ) {
      return new Response(null, { status: 204 })
    }
    throw new Error(`unexpected ${call.method} ${call.path}`)
  }
}

function throttledResponse(retryAfterSeconds: number): Response {
  return new Response(
    JSON.stringify({
      type: 'urn:speed:ratelimit:throttled',
      title: 'Too Many Requests',
      status: 429,
    }),
    {
      status: 429,
      headers: {
        'content-type': 'application/problem+json',
        'retry-after': String(retryAfterSeconds),
      },
    },
  )
}

function mount(
  respond: RealResponder,
  handlers: {
    readonly onSuccess?: () => void
    readonly onCancel?: () => void
    readonly accountHref?: string
    readonly returnPath?: string
    readonly onRelogin?: () => void
  } = {},
) {
  const rig = makeRealClientRig(respond)
  rig.store.set('access-1')
  const onSuccess = vi.fn(handlers.onSuccess)
  const onCancel = vi.fn(handlers.onCancel)
  renderWithProviders(
    <StepUpChallenge
      open
      session={rig.session}
      onSuccess={onSuccess}
      onCancel={onCancel}
      accountHref={handlers.accountHref}
      returnPath={handlers.returnPath}
      onRelogin={handlers.onRelogin}
    />,
  )
  return { rig, onSuccess, onCancel }
}

function mountSession(
  session: AuthSession,
  handlers: {
    readonly reason?: 'mfa' | 'sso'
    readonly onSuccess?: () => void
    readonly onCancel?: () => void
    readonly onRelogin?: () => void
  } = {},
) {
  const onSuccess = vi.fn(handlers.onSuccess)
  const onCancel = vi.fn(handlers.onCancel)
  renderWithProviders(
    <StepUpChallenge
      open
      reason={handlers.reason}
      session={session}
      onSuccess={onSuccess}
      onCancel={onCancel}
      onRelogin={handlers.onRelogin}
    />,
  )
  return { onSuccess, onCancel }
}

interface DialogControl {
  setOpen(dialogOpen: boolean): void
  rerender(): void
}

function Harness({
  session,
  onSuccess,
  onCancel,
  control,
}: {
  readonly session: AuthSession
  readonly onSuccess: () => void
  readonly onCancel: () => void
  readonly control: DialogControl
}) {
  const [open, setOpen] = useState(true)
  const [, setVersion] = useState(0)
  control.setOpen = setOpen
  control.rerender = () => setVersion((version) => version + 1)
  return (
    <StepUpChallenge
      open={open}
      session={session}
      onSuccess={onSuccess}
      onCancel={onCancel}
    />
  )
}

function renderControlled(
  session: AuthSession,
  handlers: {
    readonly onSuccess: () => void
    readonly onCancel: () => void
  },
): DialogControl {
  const control: DialogControl = {
    setOpen: () => undefined,
    rerender: () => undefined,
  }
  renderWithProviders(
    <Harness
      session={session}
      onSuccess={handlers.onSuccess}
      onCancel={handlers.onCancel}
      control={control}
    />,
  )
  return control
}

const AUTHENTICATED_SNAPSHOT: AuthSnapshot = {
  state: 'authenticated',
  principal: {
    account_id: 'account-1',
    kind: 'user',
    display_name: 'Ada',
    mfa_required: false,
    password_set: true,
    created_at: '2026-01-01T00:00:00Z',
    attributes: {},
    locale: 'zh-CN',
    time_zone: 'UTC',
    color_mode: '',
    identifiers: [],
    links: [],
    verification: { state: 'none', email: '', since: null },
  },
  impersonating: false,
  permissionSets: { tenant: null, system: null },
  features: { state: 'ready', names: new Set() },
}

function unused(): never {
  throw new Error('the step-up dialog drives no such operation')
}

function refusingStartSession(
  refusal: unknown,
  logout: () => Promise<void> = () => Promise.resolve(),
): AuthSession {
  return {
    getSnapshot: () => AUTHENTICATED_SNAPSHOT,
    subscribe: () => () => undefined,
    isStepUpShortfall: (error) =>
      isApiError(error) &&
      error.status === 401 &&
      AUTHENTICATED_SNAPSHOT.state === 'authenticated',
    listMethods: unused,
    discoverSso: unused,
    register: unused,
    startFlow: unused,
    startAuthenticatedFlow: () => Promise.reject(refusal),
    answerFlow: unused,
    readFlow: unused,
    abandonFlow: () => Promise.resolve(),
    logout,
    restore: unused,
    beginIdentityTransition: () => () => undefined,
    refresh: unused,
    retryFeatures: unused,
    setPermissionSet: unused,
    setDisplayName: unused,
  }
}

function scriptedPasswordSession(
  requests: { body: unknown }[] = [],
): AuthSession {
  const snapshot: AuthSnapshot = {
    ...AUTHENTICATED_SNAPSHOT,
    principal: {
      ...AUTHENTICATED_SNAPSHOT.principal!,
      identifiers: [
        {
          id: 'identifier-1',
          kind: 'username',
          value: 'ada',
          proven: false,
          contact: false,
          default_contact: false,
          sign_in: true,
          verified_at: '2026-01-01T00:00:00Z',
          verified_via: 'self',
        },
      ],
    },
  }
  return {
    getSnapshot: () => snapshot,
    subscribe: () => () => undefined,
    isStepUpShortfall: () => false,
    listMethods: unused,
    discoverSso: unused,
    register: unused,
    startFlow: unused,
    startAuthenticatedFlow: () =>
      Promise.resolve({
        seq: 1,
        state: 'first',
        next: {
          kind: 'challenge',
          fields: [
            { name: 'identifier', kind: 'text' },
            { name: 'password', kind: 'password', secret: true },
          ],
        },
      } as AuthnFlowState),
    answerFlow: (request) => {
      requests.push({ body: request })
      return Promise.resolve({
        state: 'elevated',
        token_type: 'Bearer',
        access_token: 'access-2',
        expires_in: 3600,
      } as AuthnFlowState)
    },
    readFlow: unused,
    abandonFlow: () => Promise.resolve(),
    logout: () => Promise.resolve(),
    restore: unused,
    beginIdentityTransition: () => () => undefined,
    refresh: unused,
    retryFeatures: unused,
    setPermissionSet: unused,
    setDisplayName: unused,
  }
}

function profileIdentifier(
  id: string,
  kind: string,
  value: string,
  proven = kind !== 'username',
): AuthnProfileIdentifier {
  const username = kind === 'username'
  return {
    id,
    kind,
    value,
    proven,
    contact: true,
    default_contact: false,
    sign_in: true,
    verified_at: '2026-01-01T00:00:00Z',
    verified_via: username
      ? proven
        ? 'idp:google'
        : 'self'
      : proven
        ? 'sms-code'
        : 'admin',
  }
}

function scriptedPhoneSession(
  requests: { body: unknown }[] = [],
  identifiers: readonly AuthnProfileIdentifier[] = [],
  options: { readonly impersonating?: boolean } = {},
): AuthSession {
  const snapshot: AuthSnapshot = {
    ...AUTHENTICATED_SNAPSHOT,
    impersonating: options.impersonating ?? false,
    principal: {
      ...AUTHENTICATED_SNAPSHOT.principal!,
      identifiers: [...identifiers],
    },
  }
  return {
    getSnapshot: () => snapshot,
    subscribe: () => () => undefined,
    isStepUpShortfall: () => false,
    listMethods: unused,
    discoverSso: unused,
    register: unused,
    startFlow: unused,
    startAuthenticatedFlow: () =>
      Promise.resolve({
        seq: 1,
        state: 'first',
        next: {
          kind: 'challenge',
          fields: [{ name: 'phone', kind: 'phone' }],
        },
      } as AuthnFlowState),
    answerFlow: (request) => {
      requests.push({ body: request })
      return Promise.resolve({
        state: 'elevated',
        token_type: 'Bearer',
        access_token: 'access-2',
        expires_in: 3600,
      } as AuthnFlowState)
    },
    readFlow: unused,
    abandonFlow: () => Promise.resolve(),
    logout: () => Promise.resolve(),
    restore: unused,
    beginIdentityTransition: () => () => undefined,
    refresh: unused,
    retryFeatures: unused,
    setPermissionSet: unused,
    setDisplayName: unused,
  }
}

describe('StepUpChallenge', () => {
  it('stacks the dialog body with one theme spacing gap', async () => {
    mount(makeStepUpResponder())
    const dialog = await screen.findByRole('dialog')
    const body = dialog.querySelector<HTMLElement>('.MuiDialogContent-root')
    expect(body).not.toBeNull()
    const style = getComputedStyle(body as HTMLElement)
    expect(style.display).toBe('flex')
    expect(style.flexDirection).toBe('column')
    expect(style.gap).toBe('16px')
  })

  it('starts the flow, drives the factor then the code, and elevates', async () => {
    const { rig, onSuccess } = mount(makeStepUpResponder())
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('button', { name: zhCN.mfa.factor.kind.totp }),
    )
    await waitFor(() =>
      expect(
        rig.calls.find(
          (call) => call.method === 'POST' && call.path === FLOW_ANSWER,
        )?.body,
      ).toEqual({ seq: 1, step: 'factor', input: { factor: 'factor-1' } }),
    )

    await user.type(await screen.findByLabelText(zhCN.flowStep.codeLabel), '123456')
    const cancel = screen.getByRole('button', {
      name: zhCN.mfa.stepUp.cancelLabel,
    })
    expect(cancel).toHaveClass('MuiButton-text')
    expect(cancel).not.toHaveClass('MuiButton-contained')
    await user.click(
      screen.getByRole('button', { name: zhCN.mfa.stepUp.confirmLabel }),
    )

    await waitFor(() => expect(onSuccess).toHaveBeenCalledTimes(1))
    const answers = rig.calls.filter(
      (call) => call.method === 'POST' && call.path === FLOW_ANSWER,
    )
    expect(answers[1]?.body).toEqual({
      seq: 2,
      step: 'factor',
      input: { code: '123456', factor: 'factor-1' },
    })
  })

  it("locks the identifier to the account's own login name", async () => {
    bindFactors([
      { id: 'factor-1', kind: 'totp', status: 'active' },
    ])
    const requests: { body: unknown }[] = []
    mountSession(scriptedPasswordSession(requests))
    const user = userEvent.setup()

    const identifier = await screen.findByLabelText(
      zhCN.flowStep.field.identifier,
    )
    expect(identifier).toHaveValue('ada')
    expect(identifier).toHaveAttribute('readonly')
    expect(identifier).not.toBeDisabled()
    expect(
      screen.getByText(zhCN.flowStep.lockedIdentifierHelp),
    ).toBeInTheDocument()

    await user.type(
      screen.getByLabelText(zhCN.flowStep.field.password),
      'secret',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.mfa.stepUp.confirmLabel }),
    )

    await waitFor(() => expect(requests).toHaveLength(1))
    expect(requests[0]?.body).toMatchObject({
      seq: 1,
      step: 'answer',
      input: { identifier: 'ada', password: 'secret' },
    })
  })

  it("locks a phone challenge to the account's one proved phone", async () => {
    bindFactors([
      { id: 'factor-1', kind: 'totp', status: 'active' },
    ])
    const requests: { body: unknown }[] = []
    mountSession(
      scriptedPhoneSession(requests, [
        profileIdentifier('phone-1', 'phone', '+8613800138000'),
        profileIdentifier('username-1', 'username', 'ada'),
      ]),
    )
    const user = userEvent.setup()

    const phone = await screen.findByLabelText(zhCN.flowStep.field.phone)
    expect(phone).toHaveValue('+8613800138000')
    expect(phone).toHaveAttribute('readonly')
    expect(phone).not.toBeDisabled()
    expect(screen.getByText(zhCN.flowStep.lockedPhoneHelp)).toBeInTheDocument()

    await user.click(
      screen.getByRole('button', { name: zhCN.mfa.stepUp.confirmLabel }),
    )

    await waitFor(() => expect(requests).toHaveLength(1))
    expect(requests[0]?.body).toEqual({
      seq: 1,
      step: 'answer',
      input: { phone: '+8613800138000' },
    })
  })

  it('leaves a phone challenge editable while impersonating', async () => {
    bindFactors([
      { id: 'factor-1', kind: 'totp', status: 'active' },
    ])
    mountSession(
      scriptedPhoneSession(
        [],
        [profileIdentifier('phone-1', 'phone', '+8613800138000')],
        { impersonating: true },
      ),
    )

    const phone = await screen.findByLabelText(zhCN.flowStep.field.phone)
    expect(phone).toHaveValue('')
    expect(phone).not.toHaveAttribute('readonly')
    expect(phone).not.toBeDisabled()
    expect(
      screen.queryByText(zhCN.flowStep.lockedPhoneHelp),
    ).not.toBeInTheDocument()
  })

  it('leaves a phone challenge editable when the account proved several phones', async () => {
    bindFactors([
      { id: 'factor-1', kind: 'totp', status: 'active' },
    ])
    mountSession(
      scriptedPhoneSession([], [
        profileIdentifier('phone-1', 'phone', '+8613800138000'),
        profileIdentifier('phone-2', 'phone', '+8613900139000'),
      ]),
    )

    const phone = await screen.findByLabelText(zhCN.flowStep.field.phone)
    expect(phone).toHaveValue('')
    expect(phone).not.toHaveAttribute('readonly')
    expect(
      screen.queryByText(zhCN.flowStep.lockedPhoneHelp),
    ).not.toBeInTheDocument()
  })

  it('does not lock a phone the account has not proved', async () => {
    bindFactors([
      { id: 'factor-1', kind: 'totp', status: 'active' },
    ])
    mountSession(
      scriptedPhoneSession([], [
        profileIdentifier('phone-1', 'phone', '+8613800138000', false),
      ]),
    )

    const phone = await screen.findByLabelText(zhCN.flowStep.field.phone)
    expect(phone).toHaveValue('')
    expect(phone).not.toHaveAttribute('readonly')
  })

  it('answers the ready step on its own and elevates once', async () => {
    const { rig, onSuccess } = mount(makeReadyAfterFirstFactorResponder())
    const user = userEvent.setup()

    await user.type(
      await screen.findByLabelText(zhCN.flowStep.field.identifier),
      'ada',
    )
    await user.type(
      screen.getByLabelText(zhCN.flowStep.field.password),
      'secret',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.mfa.stepUp.confirmLabel }),
    )

    await waitFor(() => expect(onSuccess).toHaveBeenCalledTimes(1))
    const answers = rig.calls.filter(
      (call) => call.method === 'POST' && call.path === FLOW_ANSWER,
    )
    expect(answers[0]?.body).toEqual({
      seq: 1,
      step: 'answer',
      input: { identifier: 'ada', password: 'secret' },
    })
    expect(answers[1]?.body).toEqual({ seq: 2, step: 'complete' })
    expect(answers).toHaveLength(2)
  })

  it('answers a first-factor password under the answer step when the account already holds factors', async () => {
    const { rig } = mount(makeFactorsRegisteredThisSessionResponder())
    const user = userEvent.setup()

    await user.type(
      await screen.findByLabelText(zhCN.flowStep.field.password),
      'secret',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.mfa.stepUp.confirmLabel }),
    )

    await waitFor(() => {
      const answer = rig.calls.find(
        (call) => call.method === 'POST' && call.path === FLOW_ANSWER,
      )
      expect(answer?.body).toEqual({
        seq: 1,
        step: 'answer',
        input: { password: 'secret' },
      })
    })
  })

  it('falls back to the confirmation button when the automatic answer is refused', async () => {
    const warn = captureRefreshWarning()
    let completeAnswers = 0
    const { rig, onSuccess } = mount(
      makeReadyAfterFirstFactorResponder({
        completeAnswer: () => {
          completeAnswers += 1
          return completeAnswers === 1
            ? errorResponse(401, 'authn.login_failed')
            : jsonResponse(200, {
                state: 'elevated',
                token_type: 'Bearer',
                access_token: 'access-2',
                expires_in: 3600,
              })
        },
      }),
    )
    const user = userEvent.setup()

    await user.type(
      await screen.findByLabelText(zhCN.flowStep.field.identifier),
      'ada',
    )
    await user.type(
      screen.getByLabelText(zhCN.flowStep.field.password),
      'secret',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.mfa.stepUp.confirmLabel }),
    )

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.errors.authn.login_failed)
    expect(onSuccess).not.toHaveBeenCalled()
    const confirm = screen.getByRole('button', {
      name: zhCN.mfa.stepUp.confirmLabel,
    })
    await user.click(confirm)

    await waitFor(() => expect(onSuccess).toHaveBeenCalledTimes(1))
    const completes = rig.calls
      .filter((call) => call.method === 'POST' && call.path === FLOW_ANSWER)
      .filter((call) => (call.body as { step: string }).step === 'complete')
    expect(completes.map((call) => call.body)).toEqual([
      { seq: 2, step: 'complete' },
      { seq: 2, step: 'complete' },
    ])
    expect(warn).toHaveBeenCalledWith(
      'access token refresh failed',
      expect.objectContaining({
        status: 401,
        code: 'authn.login_failed',
        trace_id: 'trace-1',
      }),
    )
  })

  it('answers the ready step once per sequence across re-renders', async () => {
    let release: (() => void) | null = null
    const held = new Promise<void>((resolve) => {
      release = resolve
    })
    let completeAnswers = 0
    const rig = makeRealClientRig(
      makeReadyAfterFirstFactorResponder({
        completeAnswer: async () => {
          completeAnswers += 1
          await held
          return jsonResponse(200, {
            state: 'elevated',
            token_type: 'Bearer',
            access_token: 'access-2',
            expires_in: 3600,
          })
        },
      }),
    )
    rig.store.set('access-1')
    const onSuccess = vi.fn()
    const onCancel = vi.fn()
    const control = renderControlled(rig.session, { onSuccess, onCancel })
    const user = userEvent.setup()

    await user.type(
      await screen.findByLabelText(zhCN.flowStep.field.identifier),
      'ada',
    )
    await user.type(
      screen.getByLabelText(zhCN.flowStep.field.password),
      'secret',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.mfa.stepUp.confirmLabel }),
    )

    await waitFor(() => expect(completeAnswers).toBe(1))
    act(() => control.rerender())
    act(() => control.rerender())
    expect(completeAnswers).toBe(1)

    act(() => release?.())
    await waitFor(() => expect(onSuccess).toHaveBeenCalledTimes(1))
    const completes = rig.calls
      .filter((call) => call.method === 'POST' && call.path === FLOW_ANSWER)
      .filter((call) => (call.body as { step: string }).step === 'complete')
    expect(completes).toHaveLength(1)
  })

  it('sends the host’s path as the flow’s return_to', async () => {
    const { rig } = mount(makeStepUpResponder(), { returnPath: '/account' })
    await waitFor(() => {
      const start = rig.calls.find(
        (call) => call.method === 'POST' && call.path === FLOW_START,
      )
      expect(start?.body).toMatchObject({
        purpose: 'step-up',
        return_to: '/account',
      })
    })
  })

  it('present the human proof on an SMS second-factor answer', async () => {
    const fake = installFakeTurnstile()
    try {
      const respond: RealResponder = (call) => {
        if (call.method === 'GET' && call.path === '/api/v1/auth/methods') {
          return jsonResponse(
            200,
            methodList(['password'], {
              humanRequired: true,
              humanProvider: 'turnstile',
              humanSiteKey: 'site-1',
            }),
          )
        }
        if (call.method === 'GET' && call.path === '/api/v1/auth/factors') {
          return jsonResponse(200, {
            factors: [{ ...ACTIVE_FACTOR, kind: 'sms', label: 'Phone' }],
          })
        }
        if (call.method === 'POST' && call.path === FLOW_START) {
          return jsonResponse(200, {
            seq: 1,
            state: 'second',
            next: {
              kind: 'factor',
              factors: [{ id: 'factor-1', kind: 'sms', label: 'Phone' }],
            },
          })
        }
        if (call.method === 'POST' && call.path === FLOW_ANSWER) {
          return jsonResponse(200, {
            seq: 2,
            state: 'second',
            next: { kind: 'challenge', fields: [{ name: 'code', kind: 'text' }] },
          })
        }
        throw new Error(`unexpected ${call.method} ${call.path}`)
      }
      const { rig } = mount(respond)
      const user = userEvent.setup()

      await screen.findByRole('button', { name: zhCN.mfa.factor.kind.sms })
      expect(fake.widgets).toHaveLength(0)

      await user.click(
        screen.getByRole('button', { name: zhCN.mfa.factor.kind.sms }),
      )

      await waitFor(() => expect(fake.widgets).toHaveLength(1))
      expect(fake.widgets[0]?.siteKey).toBe('site-1')
      expect(
        rig.calls.filter(
          (call) => call.method === 'POST' && call.path === FLOW_ANSWER,
        ),
      ).toHaveLength(0)
      act(() => {
        fake.token('token-1')
      })

      await waitFor(() => {
        const body = rig.calls.find(
          (call) => call.method === 'POST' && call.path === FLOW_ANSWER,
        )?.body
        expect(body).toMatchObject({
          seq: 1,
          step: 'factor',
          input: { factor: 'factor-1' },
          human_proof: 'token-1',
        })
      })
    } finally {
      fake.restore()
    }
  })

  it('renders a refused answer as code text and stays retryable', async () => {
    const warn = captureRefreshWarning()
    const { rig, onSuccess } = mount(
      makeStepUpResponder({
        codeAnswer: () => errorResponse(401, 'authn.login_failed'),
      }),
    )
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('button', { name: zhCN.mfa.factor.kind.totp }),
    )
    await user.type(await screen.findByLabelText(zhCN.flowStep.codeLabel), '000000')
    await user.click(
      screen.getByRole('button', { name: zhCN.mfa.stepUp.confirmLabel }),
    )

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.errors.authn.login_failed)
    expect(onSuccess).not.toHaveBeenCalled()
    expect(screen.getByLabelText(zhCN.flowStep.codeLabel)).toBeTruthy()
    expect(rig.calls.length).toBeGreaterThan(0)
    expect(warn).toHaveBeenCalledWith(
      'access token refresh failed',
      expect.objectContaining({
        status: 401,
        code: 'authn.login_failed',
        trace_id: 'trace-1',
      }),
    )
    expect(warn).toHaveBeenCalledTimes(1)
  })

  it('holds the submit for the wait a throttled answer stated', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    try {
      const user = userEvent.setup({
        advanceTimers: vi.advanceTimersByTimeAsync,
      })
      mount(
        makeStepUpResponder({ codeAnswer: () => throttledResponse(30) }),
      )

      await user.click(
        await screen.findByRole('button', { name: zhCN.mfa.factor.kind.totp }),
      )
      await user.type(
        await screen.findByLabelText(zhCN.flowStep.codeLabel),
        '000000',
      )
      await user.click(
        screen.getByRole('button', { name: zhCN.mfa.stepUp.confirmLabel }),
      )

      const alert = await screen.findByRole('alert')
      expect(alert).toHaveTextContent(zhCN.errors.ratelimit.throttled)
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

      const codeField = screen.getByLabelText(zhCN.flowStep.codeLabel)
      expect(codeField).toBeTruthy()
      const submit = screen.getByRole('button', {
        name: zhCN.mfa.stepUp.confirmLabel,
      }) as HTMLButtonElement
      expect(submit).toHaveAttribute('aria-disabled', 'true')
      expect(submit).not.toBeDisabled()
      expect(submit).toHaveAccessibleDescription(
        new RegExp(zhCN.throttle.countdown.replace('{{seconds}}', '\\d+')),
      )
      submit.focus()
      expect(submit).toHaveFocus()

      await act(async () => {
        await vi.advanceTimersByTimeAsync(30_000)
      })
      await waitFor(() => {
        expect(
          screen.getByRole('button', {
            name: zhCN.mfa.stepUp.confirmLabel,
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

  it('offers a restart when the answer finds its flow gone', async () => {
    captureRefreshWarning({ expected: true })
    const { rig } = mount(
      makeStepUpResponder({
        codeAnswer: () => errorResponse(401, 'authn.flow_invalid'),
      }),
    )
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('button', { name: zhCN.mfa.factor.kind.totp }),
    )
    await user.type(await screen.findByLabelText(zhCN.flowStep.codeLabel), '000000')
    await user.click(
      screen.getByRole('button', { name: zhCN.mfa.stepUp.confirmLabel }),
    )

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.errors.authn.flow_invalid)
    expect(screen.queryByLabelText(zhCN.flowStep.codeLabel)).toBeNull()

    const starts = () =>
      rig.calls.filter((call) => call.path === FLOW_START).length
    const before = starts()
    await user.click(
      screen.getByRole('button', { name: zhCN.mfa.stepUp.restart }),
    )

    await waitFor(() => {
      expect(starts()).toBe(before + 1)
    })
    expect(
      await screen.findByRole('button', { name: zhCN.mfa.factor.kind.totp }),
    ).toBeTruthy()
  })

  it('fires onCancel on the cancel button without elevating', async () => {
    const { onSuccess, onCancel } = mount(makeStepUpResponder())
    const user = userEvent.setup()

    await screen.findByRole('button', { name: zhCN.mfa.factor.kind.totp })
    await user.click(
      screen.getByRole('button', { name: zhCN.mfa.stepUp.cancelLabel }),
    )
    expect(onCancel).toHaveBeenCalledTimes(1)
    expect(onSuccess).not.toHaveBeenCalled()
  })

  it('names the missing second factor for a refused multi-factor requirement', async () => {
    const { rig } = mount(
      (call: RealCall) => {
        if (call.method === 'GET' && call.path === '/api/v1/auth/factors') {
          return jsonResponse(200, { factors: [] })
        }
        if (call.method === 'GET' && call.path === '/api/v1/auth/session') {
          return jsonResponse(200, sessionBody())
        }
        throw new Error(`unexpected ${call.method} ${call.path}`)
      },
    )

    expect(await screen.findByText(zhCN.mfa.stepUp.noFactor)).toBeTruthy()
    expect(screen.queryAllByRole('link')).toHaveLength(0)
    expect(
      screen.queryByRole('button', { name: zhCN.mfa.stepUp.relogin }),
    ).toBeNull()
    expect(rig.calls.some((call) => call.path === FLOW_START)).toBe(false)
  })

  it('renders the host account route as a link beside the missing-factor hint', async () => {
    mount(
      (call: RealCall) => {
        if (call.method === 'GET' && call.path === '/api/v1/auth/factors') {
          return jsonResponse(200, { factors: [] })
        }
        if (call.method === 'GET' && call.path === '/api/v1/auth/session') {
          return jsonResponse(200, sessionBody())
        }
        throw new Error(`unexpected ${call.method} ${call.path}`)
      },
      { accountHref: '/account' },
    )

    expect(await screen.findByText(zhCN.mfa.stepUp.noFactor)).toBeTruthy()
    const link = screen.getByRole('link', {
      name: zhCN.mfa.stepUp.accountSettingsLink,
    })
    expect(link).toHaveAttribute('href', '/account')
  })

  it('re-proves the first factor with the session method when the window is past', async () => {
    const { rig, onSuccess } = mount((call: RealCall) => {
      if (call.method === 'GET' && call.path === '/api/v1/auth/factors') {
        return jsonResponse(200, { factors: [] })
      }
      if (call.method === 'GET' && call.path === '/api/v1/auth/session') {
        return jsonResponse(
          200,
          sessionBody({ methods: ['sms'], ageMs: 6 * 60 * 1000 }),
        )
      }
      if (call.method === 'POST' && call.path === FLOW_START) {
        return jsonResponse(200, {
          seq: 1,
          state: 'first',
          next: { kind: 'challenge', fields: [{ name: 'code', kind: 'text' }] },
        })
      }
      if (call.method === 'POST' && call.path === FLOW_ANSWER) {
        return jsonResponse(200, {
          state: 'elevated',
          token_type: 'Bearer',
          access_token: 'access-2',
          expires_in: 3600,
        })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    })
    const user = userEvent.setup()

    await user.type(await screen.findByLabelText(zhCN.flowStep.codeLabel), '123456')
    await user.click(
      screen.getByRole('button', { name: zhCN.mfa.stepUp.confirmLabel }),
    )
    await waitFor(() => expect(onSuccess).toHaveBeenCalledTimes(1))

    const start = rig.calls.find((call) => call.path === FLOW_START)
    expect(start?.body).toEqual({ purpose: 'step-up', method: 'sms' })
    const answers = rig.calls.filter((call) => call.path === FLOW_ANSWER)
    expect(answers[0]?.body).toEqual({
      seq: 1,
      step: 'answer',
      input: { code: '123456' },
    })
  })

  it('names a password session password on the first-factor step-up', async () => {
    const { rig } = mount((call: RealCall) => {
      if (call.method === 'GET' && call.path === '/api/v1/auth/factors') {
        return jsonResponse(200, { factors: [] })
      }
      if (call.method === 'GET' && call.path === '/api/v1/auth/session') {
        return jsonResponse(
          200,
          sessionBody({ methods: ['pwd'], ageMs: 6 * 60 * 1000 }),
        )
      }
      if (call.method === 'POST' && call.path === FLOW_START) {
        return jsonResponse(200, {
          seq: 1,
          state: 'first',
          next: {
            kind: 'challenge',
            fields: [
              { name: 'identifier', kind: 'text' },
              { name: 'password', kind: 'password', secret: true },
            ],
          },
        })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    })

    await screen.findByLabelText(zhCN.flowStep.field.identifier)
    const start = rig.calls.find((call) => call.path === FLOW_START)
    expect(start?.body).toEqual({ purpose: 'step-up', method: 'password' })
  })

  it('decides a reopening from a fresh session read, not the cached view', async () => {
    let justAuthenticated = false
    let unblockSession: (() => void) | null = null
    const sessionHeld = new Promise<void>((resolve) => {
      unblockSession = resolve
    })
    const rig = makeRealClientRig(async (call: RealCall) => {
      if (call.method === 'GET' && call.path === '/api/v1/auth/factors') {
        return jsonResponse(200, { factors: [] })
      }
      if (call.method === 'GET' && call.path === '/api/v1/auth/session') {
        if (justAuthenticated) {
          await sessionHeld
        }
        return jsonResponse(
          200,
          sessionBody({
            methods: ['pwd'],
            ageMs: justAuthenticated ? 0 : 10 * 60 * 1000,
          }),
        )
      }
      if (call.method === 'POST' && call.path === FLOW_START) {
        return jsonResponse(200, {
          seq: 1,
          state: 'first',
          next: {
            kind: 'challenge',
            fields: [{ name: 'code', kind: 'text' }],
          },
        })
      }
      if (
        call.method === 'DELETE' &&
        call.path === '/api/v1/auth/flows/current'
      ) {
        return new Response(null, { status: 204 })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    })
    rig.store.set('access-1')
    const onSuccess = vi.fn()
    const onCancel = vi.fn()
    const starts = () =>
      rig.calls.filter(
        (call) => call.method === 'POST' && call.path === FLOW_START,
      )
    const control = renderControlled(rig.session, { onSuccess, onCancel })

    await screen.findByLabelText(zhCN.flowStep.codeLabel)
    expect(starts()).toHaveLength(1)

    act(() => control.setOpen(false))
    await waitFor(() =>
      expect(
        rig.calls.some(
          (call) =>
            call.method === 'DELETE' && call.path === '/api/v1/auth/flows/current',
        ),
      ).toBe(true),
    )
    justAuthenticated = true

    act(() => control.setOpen(true))
    expect(await screen.findByText(zhCN.mfa.stepUp.starting)).toBeTruthy()
    expect(starts()).toHaveLength(1)

    act(() => unblockSession?.())
    expect(await screen.findByText(zhCN.mfa.stepUp.noFactor)).toBeTruthy()
    expect(starts()).toHaveLength(1)
  })

  it('keeps a parked verdict across the five-minute boundary', async () => {
    const openedAt = Date.now()
    const clock = vi.spyOn(Date, 'now').mockReturnValue(openedAt)
    try {
      const rig = makeRealClientRig((call: RealCall) => {
        if (call.method === 'GET' && call.path === '/api/v1/auth/factors') {
          return jsonResponse(200, { factors: [] })
        }
        if (call.method === 'GET' && call.path === '/api/v1/auth/session') {
          return jsonResponse(200, sessionBody())
        }
        if (
          call.method === 'DELETE' &&
          call.path === '/api/v1/auth/flows/current'
        ) {
          return new Response(null, { status: 204 })
        }
        throw new Error(`unexpected ${call.method} ${call.path}`)
      })
      rig.store.set('access-1')
      const onSuccess = vi.fn()
      const onCancel = vi.fn()
      const control = renderControlled(rig.session, { onSuccess, onCancel })

      expect(await screen.findByText(zhCN.mfa.stepUp.noFactor)).toBeTruthy()
      expect(
        rig.calls.some((call) => call.path === FLOW_START),
      ).toBe(false)

      clock.mockReturnValue(openedAt + 6 * 60 * 1000)
      act(() => control.rerender())
      expect(screen.getByText(zhCN.mfa.stepUp.noFactor)).toBeTruthy()
      expect(
        rig.calls.some((call) => call.path === FLOW_START),
      ).toBe(false)
    } finally {
      clock.mockRestore()
    }
  })

  it('reads the session view once per opening', async () => {
    const rig = makeRealClientRig((call: RealCall) => {
      if (call.method === 'GET' && call.path === '/api/v1/auth/factors') {
        return jsonResponse(200, { factors: [] })
      }
      if (call.method === 'GET' && call.path === '/api/v1/auth/session') {
        return jsonResponse(200, sessionBody())
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    })
    rig.store.set('access-1')
    const onSuccess = vi.fn()
    const onCancel = vi.fn()
    const control = renderControlled(rig.session, { onSuccess, onCancel })
    const reads = () =>
      rig.calls.filter(
        (call) => call.method === 'GET' && call.path === '/api/v1/auth/session',
      )

    expect(await screen.findByText(zhCN.mfa.stepUp.noFactor)).toBeTruthy()
    expect(reads()).toHaveLength(1)

    act(() => control.setOpen(false))
    act(() => control.setOpen(true))
    expect(await screen.findByText(zhCN.mfa.stepUp.noFactor)).toBeTruthy()
    expect(reads()).toHaveLength(2)
  })

  it('offers a re-login when the session names no first-factor method', async () => {
    const { rig, onCancel } = mount((call: RealCall) => {
      if (call.method === 'GET' && call.path === '/api/v1/auth/factors') {
        return jsonResponse(200, { factors: [] })
      }
      if (call.method === 'GET' && call.path === '/api/v1/auth/session') {
        return jsonResponse(
          200,
          sessionBody({ methods: ['oauth'], ageMs: 6 * 60 * 1000 }),
        )
      }
      if (call.method === 'POST' && call.path === '/api/v1/auth/logout') {
        return new Response(null, { status: 204 })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    })
    const user = userEvent.setup()

    expect(
      await screen.findByText(zhCN.mfa.stepUp.firstFactorRelogin),
    ).toBeTruthy()
    await user.click(
      screen.getByRole('button', { name: zhCN.mfa.stepUp.relogin }),
    )
    expect(onCancel).toHaveBeenCalledTimes(1)
    expect(rig.calls.some((call) => call.path === FLOW_START)).toBe(false)
    await waitFor(() =>
      expect(
        rig.calls.some(
          (call) => call.method === 'POST' && call.path === '/api/v1/auth/logout',
        ),
      ).toBe(true),
    )
  })

  it('renders a 400 start refusal as an ordinary error when a factor is registered', async () => {
    bindFactors([ACTIVE_FACTOR])
    mountSession(
      refusingStartSession({
        status: 400,
        code: 'authn.bad_request',
        message: 'refused',
        auth: false,
      }),
    )

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.errors.authn.bad_request)
    expect(screen.queryByText(zhCN.mfa.stepUp.cannotElevate)).toBeNull()
    expect(screen.queryByText(zhCN.mfa.stepUp.noFactor)).toBeNull()
  })

  it('treats a 401 start refusal on an established session as unavailable', async () => {
    bindFactors([ACTIVE_FACTOR])
    mountSession(
      refusingStartSession({
        status: 401,
        code: 'authn.insufficient_user_authentication',
        message: 'refused',
        auth: true,
      }),
    )

    expect(await screen.findByText(zhCN.mfa.stepUp.cannotElevate)).toBeTruthy()
    expect(
      screen.queryByText(zhCN.errors.authn.insufficient_user_authentication),
    ).toBeNull()
    expect(
      screen.getByRole('button', { name: zhCN.mfa.stepUp.relogin }),
    ).toBeTruthy()
  })

  it('records the operation before ending the session on the re-login exit', async () => {
    bindFactors([ACTIVE_FACTOR])
    const order: string[] = []
    const logout = vi.fn(() => {
      order.push('logout')
      return Promise.resolve()
    })
    mountSession(
      refusingStartSession(
        {
          status: 401,
          code: 'authn.insufficient_user_authentication',
          message: 'refused',
          auth: true,
        },
        logout,
      ),
      {
        onRelogin: () => order.push('relogin'),
        onCancel: () => order.push('cancel'),
      },
    )
    const user = userEvent.setup()

    expect(await screen.findByText(zhCN.mfa.stepUp.cannotElevate)).toBeTruthy()
    await user.click(
      screen.getByRole('button', { name: zhCN.mfa.stepUp.relogin }),
    )
    expect(order).toEqual(['relogin', 'cancel', 'logout'])
    expect(logout).toHaveBeenCalledTimes(1)
  })

  it('records the operation before ending the session on the SSO re-login exit', async () => {
    const order: string[] = []
    const logout = vi.fn(() => {
      order.push('logout')
      return Promise.resolve()
    })
    mountSession(refusingStartSession('unused', logout), {
      reason: 'sso',
      onRelogin: () => order.push('relogin'),
      onCancel: () => order.push('cancel'),
    })
    const user = userEvent.setup()

    expect(await screen.findByText(zhCN.mfa.stepUp.ssoRequired)).toBeTruthy()
    await user.click(
      screen.getByRole('button', { name: zhCN.mfa.stepUp.relogin }),
    )
    expect(order).toEqual(['relogin', 'cancel', 'logout'])
    expect(logout).toHaveBeenCalledTimes(1)
  })

  it('renders a start failure that is not a refusal as an ordinary banner', async () => {
    bindFactors([ACTIVE_FACTOR])
    mountSession(
      refusingStartSession({
        status: 503,
        code: 'authn.unavailable',
        message: 'down',
        auth: false,
      }),
    )

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.errors.authn.unavailable)
    expect(screen.queryByText(zhCN.mfa.stepUp.cannotElevate)).toBeNull()
    expect(
      screen.queryByRole('button', { name: zhCN.mfa.stepUp.relogin }),
    ).toBeNull()
  })
})

function hangingFactorAnswerResponder(): {
  readonly respond: RealResponder
  readonly release: () => void
} {
  let releaseFn: (() => void) | null = null
  const pending = new Promise<void>((resolve) => {
    releaseFn = resolve
  })
  const respond: RealResponder = (call: RealCall) => {
    if (call.method === 'GET' && call.path === '/api/v1/auth/factors') {
      return jsonResponse(200, { factors: [ACTIVE_FACTOR] })
    }
    if (call.method === 'POST' && call.path === FLOW_START) {
      return jsonResponse(200, {
        seq: 1,
        state: 'second',
        next: {
          kind: 'factor',
          factors: [{ id: 'factor-1', kind: 'totp', label: 'Authenticator app' }],
        },
      })
    }
    if (call.method === 'POST' && call.path === FLOW_ANSWER) {
      return pending.then(() =>
        jsonResponse(200, {
          seq: 2,
          state: 'second',
          next: { kind: 'challenge', fields: [{ name: 'code', kind: 'text' }] },
        }),
      )
    }
    throw new Error(`unexpected ${call.method} ${call.path}`)
  }
  return { respond, release: () => releaseFn?.() }
}

describe('StepUpChallenge held step', () => {
  it('keeps the activated factor busy while the step cancel stays available', async () => {
    const responder = hangingFactorAnswerResponder()
    const { onCancel } = mount(responder.respond)
    const user = userEvent.setup()

    const totp = await screen.findByRole('button', {
      name: zhCN.mfa.factor.kind.totp,
    })
    totp.focus()
    expect(totp).toHaveFocus()
    await user.click(totp)

    await waitFor(() =>
      expect(totp).toHaveAttribute('aria-busy', 'true'),
    )
    expect(totp).toHaveAttribute('aria-disabled', 'true')
    expect(document.activeElement).toBe(totp)

    const cancel = screen.getByRole('button', {
      name: zhCN.mfa.stepUp.cancelLabel,
    })
    expect(cancel).not.toHaveAttribute('aria-disabled')
    expect(cancel).not.toHaveAttribute('aria-busy')
    expect(cancel).not.toHaveAccessibleDescription()
    cancel.focus()
    expect(cancel).toHaveFocus()
    await user.click(cancel)
    expect(onCancel).toHaveBeenCalledTimes(1)
  })

  it('keeps the step cancel available while the answer waits on a human proof', async () => {
    const fake = installFakeTurnstile()
    try {
      const respond: RealResponder = (call) => {
        if (call.method === 'GET' && call.path === '/api/v1/auth/methods') {
          return jsonResponse(
            200,
            methodList(['password'], {
              humanRequired: true,
              humanProvider: 'turnstile',
              humanSiteKey: 'site-1',
            }),
          )
        }
        if (call.method === 'GET' && call.path === '/api/v1/auth/factors') {
          return jsonResponse(200, {
            factors: [{ ...ACTIVE_FACTOR, kind: 'sms', label: 'Phone' }],
          })
        }
        if (call.method === 'POST' && call.path === FLOW_START) {
          return jsonResponse(200, {
            seq: 1,
            state: 'second',
            next: {
              kind: 'factor',
              factors: [{ id: 'factor-1', kind: 'sms', label: 'Phone' }],
            },
          })
        }
        throw new Error(`unexpected ${call.method} ${call.path}`)
      }
      const { rig, onCancel } = mount(respond)
      const user = userEvent.setup()

      await user.click(
        await screen.findByRole('button', { name: zhCN.mfa.factor.kind.sms }),
      )
      await waitFor(() => expect(fake.widgets).toHaveLength(1))
      expect(
        rig.calls.filter(
          (call) => call.method === 'POST' && call.path === FLOW_ANSWER,
        ),
      ).toHaveLength(0)

      const cancel = screen.getByRole('button', {
        name: zhCN.mfa.stepUp.cancelLabel,
      })
      expect(cancel).not.toHaveAttribute('aria-disabled')
      expect(cancel).not.toHaveAttribute('aria-busy')
      expect(cancel).not.toHaveAccessibleDescription()
      await user.click(cancel)
      expect(onCancel).toHaveBeenCalledTimes(1)
    } finally {
      fake.restore()
    }
  })

  it('lands focus on the dialog title when the answer replaces the step', async () => {
    const responder = hangingFactorAnswerResponder()
    mount(responder.respond)
    const user = userEvent.setup()

    const totp = await screen.findByRole('button', {
      name: zhCN.mfa.factor.kind.totp,
    })
    await user.click(totp)
    await waitFor(() => expect(totp).toHaveAttribute('aria-busy', 'true'))

    await act(async () => {
      responder.release()
    })
    expect(
      await screen.findByLabelText(zhCN.flowStep.codeLabel),
    ).toBeTruthy()

    const title = screen.getByRole('heading', { name: zhCN.mfa.stepUp.title })
    expect(title).toHaveAttribute('tabindex', '-1')
    expect(document.activeElement).toBe(title)
  })

  it('does not take focus back from where the operator put it', async () => {
    const responder = hangingFactorAnswerResponder()
    mount(responder.respond)
    const user = userEvent.setup()

    const totp = await screen.findByRole('button', {
      name: zhCN.mfa.factor.kind.totp,
    })
    await user.click(totp)
    await waitFor(() => expect(totp).toHaveAttribute('aria-busy', 'true'))

    const dialog = screen.getByRole('dialog')
    dialog.focus()
    expect(document.activeElement).toBe(dialog)

    await act(async () => {
      responder.release()
    })
    expect(
      await screen.findByLabelText(zhCN.flowStep.codeLabel),
    ).toBeTruthy()
    expect(document.activeElement).toBe(dialog)
  })
})
