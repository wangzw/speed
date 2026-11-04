/**
 * StepUpChallenge behaviour: the dialog driving one `purpose: 'step-up'`
 * flow (POST /api/v1/auth/flows/authenticated, then POST /api/v1/auth/flows/answer per
 * step).
 *
 * What this suite pins: opening the dialog starts the flow and renders
 * its first step; choosing a factor answers with `step: 'factor'` and
 * the factor identifier; the flow's challenge step then asks for a code
 * answered under the same `factor` step, the chosen factor re-named
 * beside it; a completion in state 'elevated' fires onSuccess; a ready
 * state's `complete` step is answered automatically once per sequence,
 * and the step's own confirmation button takes over when that automatic
 * answer is refused; a refused answer renders its code text and stays
 * retryable; an account with no
 * active factor is recognised from the caller's own factor read and the
 * dialog names the missing second factor, renders no link unless the host
 * supplies accountHref, and starts no flow at all; a 401 start refusal on an established
 * session (factors exist, none may strengthen it) offers a re-login; a
 * 400 or any other start failure renders as its code, never as either of
 * those explanations; an account with no factor re-proves the first factor
 * with the method that established its session when the session is past
 * the fresh-first-factor window, answers under the flow's `answer` step,
 * and re-logins instead when that method cannot be named; a reopening
 * decides from a session read taken after it opens, not from the cached
 * view, and starts no flow until that read lands; a dialog parked on the
 * enrollment hint keeps its verdict past the five-minute window; and cancel
 * fires onCancel without running the gated action.
 */

import { useState } from 'react'
import { describe, expect, it, vi } from 'vitest'
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

/** One active factor as the caller's own factor read answers it. */
const ACTIVE_FACTOR = {
  id: 'factor-1',
  kind: 'totp',
  label: 'Authenticator app',
  status: 'active',
  created_at: '2026-01-01T00:00:00Z',
}

/** A GET /api/v1/auth/session body as the caller's own session answers it. The
 * default is a password session authenticated just now; `ageMs` moves the
 * authentication time back past the five-minute window. */
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

/** Binds a scripted request function that answers the caller's own factor
 * read with `factors`; any other leg is a defect of the case. For the
 * cases that hand the dialog a session directly and never touch the wire. */
function bindFactors(factors: readonly unknown[]): void {
  bindRequestFn((async (path: string, options?: RequestOptions) => {
    if ((options?.method ?? 'GET') === 'GET' && path === '/api/v1/auth/factors') {
      return { factors }
    }
    throw new Error(`unexpected ${options?.method ?? 'GET'} ${path}`)
  }) as RequestFn)
}

/** A responder whose step-up flow offers one TOTP factor, challenges for a
 * code, and elevates on the code answer; the caller's own factor read
 * answers one active factor. */
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

/** A step-up that re-proves the session's password first factor and answers
 * its next step in the ready state, `next.kind: 'complete'`: the shape the
 * dialog meets when a gated write asks for a fresh first factor and the
 * server settles right after the first-factor answer. `completeAnswer`
 * scripts that automatic answer; the default elevates. */
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

/** A step-up whose account holds a factor registered during this session: the
 * server starts at the password and offers the factor only once it is proved
 * again. The caller's own factor read answers one active factor, so the
 * dialog starts the flow without asking for a first-factor re-proof. */
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

/** The 429 the ratelimit layer answers a throttled request with: the
 * platform problem type and the wait its Retry-After names. */
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

/** Mounts the dialog over a session the test hands in directly, for the
 * phase cases that never touch the wire. */
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

/** Test control over a dialog mounted once under the providers: the reopen
 * and re-render cases need one provider tree (and so one QueryClient)
 * across several renders, which RTL's `rerender` cannot give -- it
 * replaces the whole tree, providers included. */
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

/** An established session: the snapshot the step-up shortfall classifier
 * reads -- a start refusal is only "unavailable" while the session is
 * still authenticated. */
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
    verification: { state: 'none', email: '', since: '' },
  },
  impersonating: false,
  permissionSets: { tenant: null, system: null },
  features: { state: 'ready', names: new Set() },
}

function unused(): never {
  throw new Error('the step-up dialog drives no such operation')
}

/** A session over an authenticated snapshot whose step-up start is
 * refused by `refusal`. The dialog reads only getSnapshot,
 * isStepUpShortfall, startAuthenticatedFlow and abandonFlow; every other
 * operation is unused. */
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

/** A session over an authenticated snapshot that names its username, whose
 * step-up start asks for the password and whose answer elevates. The answer
 * requests are collected for the assertions. */
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
          proven: true,
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

/** One identifier row of the authenticated profile, as GET
 * /api/v1/auth/me publishes it. */
function profileIdentifier(
  id: string,
  kind: string,
  value: string,
  proven = true,
): AuthnProfileIdentifier {
  return {
    id,
    kind,
    value,
    proven,
    contact: true,
    default_contact: false,
    sign_in: true,
    verified_at: '2026-01-01T00:00:00Z',
    verified_via: proven ? 'sms-code' : 'admin',
  }
}

/** A session over an authenticated snapshot holding `identifiers`, whose
 * step-up start asks for the phone number and whose answer elevates. The
 * answer requests are collected for the assertions. `impersonating` makes
 * the snapshot the impersonated account's, as a session started from an
 * impersonation window carries it. */
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
    // The body's direct children -- the notice, the failure alert, the
    // explanation and the step fields -- all sit theme.spacing(2) apart, so
    // the alert has the same gap above and below instead of being flushed
    // against a neighbour. jsdom applies the injected stylesheet, so the
    // computed style is the rendered one. The default token unit is 8.
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

    // The factor step offers the flow's factor.
    await user.click(
      await screen.findByRole('button', { name: zhCN.mfa.factor.kind.totp }),
    )
    // The chosen factor is answered under the `factor` step.
    await waitFor(() =>
      expect(
        rig.calls.find(
          (call) => call.method === 'POST' && call.path === FLOW_ANSWER,
        )?.body,
      ).toEqual({ seq: 1, step: 'factor', input: { factor: 'factor-1' } }),
    )

    await user.type(await screen.findByLabelText(zhCN.flowStep.codeLabel), '123456')
    // Cancel is the action row's secondary control: a text button, not the
    // primary (contained) confirm beside it.
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
    // The code answer re-names the chosen factor: a challenge reply carries
    // only the field the factor asks for, and the server re-reads the
    // factor from it.
    expect(answers[1]?.body).toEqual({
      seq: 2,
      step: 'factor',
      input: { code: '123456', factor: 'factor-1' },
    })
  })

  it("locks the identifier to the account's own login name", async () => {
    // The dialog knows who is signed in: the password step shows the login
    // name as a read-only value with the sentence that says what it is.
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
    // Read-only, not disabled: it stays readable and focusable.
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
    // The answer carries the prefilled identifier, not an empty one.
    expect(requests[0]?.body).toMatchObject({
      seq: 1,
      step: 'answer',
      input: { identifier: 'ada', password: 'secret' },
    })
  })

  it("locks a phone challenge to the account's one proved phone", async () => {
    // A step-up may send a code only to a number the account proved. With
    // exactly one, there is nothing to choose: it is shown read-only, and the
    // answer carries it.
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
    // Read-only, not disabled: it stays readable and focusable.
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
    // The snapshot names the impersonated account, but a step-up flow proves
    // the initiator's own factor: locking the snapshot's phone would only be
    // refused by the server, and the field must stay open for the operator's
    // own number.
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
    // Several proved numbers are the person's to choose from: the dialog does
    // not pick one, and the field stays free text.
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
    // A row without a proof is not a target the server accepts.
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
    // The first-factor answer parks the flow on a ready state whose step is
    // a bare confirmation. It collects nothing, so the dialog answers it
    // without a second click.
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
    // The automatic answer carries the ready step's own sequence number and
    // the complete step, and it is the only completion sent.
    expect(answers[1]?.body).toEqual({ seq: 2, step: 'complete' })
    expect(answers).toHaveLength(2)
  })

  it('answers a first-factor password under the answer step when the account already holds factors', async () => {
    // A factor registered during the session does not put the flow at the
    // factor step: the server proves the password again first, then offers
    // the factor. The dialog did not ask for a first-factor re-proof, yet
    // this challenge is the first factor, so its answer travels under
    // `answer` rather than `factor`.
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
    // A refused automatic answer must not leave the dialog on its
    // placeholder: the failure code renders and the step's own confirmation
    // button retries the same answer by hand.
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
    // The visible manual path: one confirmation button carrying the
    // step's own submit label.
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
    // The refused automatic answer is the expected refresh-failure
    // diagnostic.
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
    // The automatic answer is held open while the dialog re-renders: a
    // second answer for the same sequence number would be a double submit,
    // which the effect's per-sequence record must prevent.
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

    // The one automatic answer is in flight; ordinary re-renders leave it
    // alone.
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
    // A redirect method's round trip lands back on the page that opened the
    // dialog, so that page reads the refusal.
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
      // The factor step shows no challenge before the answer is asked for.
      expect(fake.widgets).toHaveLength(0)

      await user.click(
        screen.getByRole('button', { name: zhCN.mfa.factor.kind.sms }),
      )

      // The answer waits on the proof: the block renders above the step and
      // the step's own action gives way until the widget yields a
      // single-use token.
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

      // The proof arrives and the answer leaves by itself.
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
    // The flow is still open: the answer stayed parked on the code step.
    expect(screen.getByLabelText(zhCN.flowStep.codeLabel)).toBeTruthy()
    expect(rig.calls.length).toBeGreaterThan(0)
    // The refused answer's 401 is the expected refresh-failure diagnostic.
    expect(warn).toHaveBeenCalledWith(
      'access token refresh failed',
      expect.objectContaining({
        status: 401,
        code: 'authn.login_failed',
        trace_id: 'trace-1',
      }),
    )
    // Exactly one answer was refused; a duplicated answer would warn again.
    expect(warn).toHaveBeenCalledTimes(1)
  })

  it('holds the submit for the wait a throttled answer stated', async () => {
    // The ratelimit layer refuses a throttled answer with 429 and a
    // Retry-After. The dialog names the wait in its banner, keeps the step
    // form but disables its submit until the interval elapses, then clears
    // the failure and makes the step answerable again.
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

      // The step stays on screen, but its submit is held until the wait
      // elapses.
      expect(screen.getByLabelText(zhCN.flowStep.codeLabel)).toBeTruthy()
      const submit = screen.getByRole('button', {
        name: zhCN.mfa.stepUp.confirmLabel,
      }) as HTMLButtonElement
      expect(submit.disabled).toBe(true)

      await act(async () => {
        await vi.advanceTimersByTimeAsync(30_000)
      })
      await waitFor(() => {
        expect(
          (
            screen.getByRole('button', {
              name: zhCN.mfa.stepUp.confirmLabel,
            }) as HTMLButtonElement
          ).disabled,
        ).toBe(false)
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

  it('offers a restart when the answer finds its flow gone', async () => {
    // A flow the deployment no longer holds (expired, or lost with the
    // process that stored it) can never accept another answer, so the
    // dialog drops the step form and offers a fresh flow instead of a
    // submit that could only repeat the same refusal.
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
    // The fresh flow is parked on its own first step again.
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
    // The account has no active factor (the caller's own factor read says
    // so) and its session already proves a fresh first factor, so a write
    // asking for a fresh first factor would have been allowed: the refused
    // requirement is multi-factor, which no step-up can reach without a
    // factor. The dialog names the missing two-step verification, renders
    // no link (these sections are the account settings surface, so nothing
    // here navigates), starts no flow, and offers no re-login -- a new
    // single-factor session would meet the same wall.
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
    // A host that has an account route passes accountHref; the dialog
    // renders the one link, pointing where the host said.
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
    // An account opened by SMS sign-up has no factor, and its writes ask
    // for a first factor proved within five minutes. Past the window the
    // session view says so (auth_time older than the window), and the
    // dialog starts a step-up named with the method that established the
    // session -- sms -- whose answers travel under the flow's `answer`
    // step.
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
    // A first-factor flow's answer is the `answer` step, not `factor`.
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
    // The first opening reads a first factor proved ten minutes ago, so it
    // starts a first-factor flow. The dialog is then closed and the session
    // re-established: the server now answers a just-authenticated session.
    // Reopening must read again -- the cached view still says ten minutes
    // and would start a second, spurious flow before the fresh answer
    // switched the dialog to the enrollment hint. One QueryClient serves
    // both openings, so the cache is the one a host would hold.
    let justAuthenticated = false
    // The reopened opening's session read is held open, so the case can
    // observe the dialog before the fresh answer lands.
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

    // The first opening re-proves the first factor on its `answer` step.
    await screen.findByLabelText(zhCN.flowStep.codeLabel)
    expect(starts()).toHaveLength(1)

    // Cancel closes the dialog; the session is re-established while it is
    // closed.
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

    // The reopening must wait on its own read: while that read is in
    // flight the dialog shows its starting placeholder and starts nothing
    // -- the cached view would start a first-factor flow here at once.
    act(() => control.setOpen(true))
    expect(await screen.findByText(zhCN.mfa.stepUp.starting)).toBeTruthy()
    expect(starts()).toHaveLength(1)

    // The fresh read lands: the requirement is multi-factor now, so the
    // dialog names the missing second factor and still starts nothing.
    act(() => unblockSession?.())
    expect(await screen.findByText(zhCN.mfa.stepUp.noFactor)).toBeTruthy()
    expect(starts()).toHaveLength(1)
  })

  it('keeps a parked verdict across the five-minute boundary', async () => {
    // A dialog parked on the enrollment hint must not re-decide from the
    // clock: a later render after the five-minute window would otherwise
    // turn the same panel into a first-factor flow and start it.
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

      // Just inside the window: the refusal asks for multi-factor.
      expect(await screen.findByText(zhCN.mfa.stepUp.noFactor)).toBeTruthy()
      expect(
        rig.calls.some((call) => call.path === FLOW_START),
      ).toBe(false)

      // Six minutes later, a re-render reads the same frozen verdict.
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
    // One opening decides the refusal from one read of the caller's own
    // session. The read is issued by the dialog's effect; the query itself
    // is held back, because an enabled query beside that refetch issues a
    // second GET whenever the cache already holds a view -- the default
    // `cancelRefetch` replaces the in-flight read. The cold-cache first
    // opening would dedupe to one either way; the warm-cache reopening is
    // the one that shows the second read.
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

    // A cold-cache opening reads once.
    expect(await screen.findByText(zhCN.mfa.stepUp.noFactor)).toBeTruthy()
    expect(reads()).toHaveLength(1)

    // A reopening reads once more, with the cache already holding a view.
    act(() => control.setOpen(false))
    act(() => control.setOpen(true))
    expect(await screen.findByText(zhCN.mfa.stepUp.noFactor)).toBeTruthy()
    expect(reads()).toHaveLength(2)
  })

  it('offers a re-login when the session names no first-factor method', async () => {
    // A social or enterprise sign-in establishes the session through a
    // channel this dialog cannot re-prove, so no step-up is started and
    // the only way out is a new sign-in.
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
    // A 400 is not, by itself, "no factor to elevate with": the account
    // has an active factor here, so the dialog shows the refusal's own
    // code instead of either of the two explanations.
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
    // The account has factors, but none whose methods may strengthen this
    // session: the start answers 401 insufficient_user_authentication and
    // the session stays authenticated -- a refusal no step can satisfy.
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
    // The unavailable branch offers "sign in to continue": the caller must
    // record the operation before the cancel clears the gate and the logout
    // ends the session, so the new sign-in can re-issue it.
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
    // The resume record is written first, then the gate clears, then the
    // session ends.
    expect(order).toEqual(['relogin', 'cancel', 'logout'])
    expect(logout).toHaveBeenCalledTimes(1)
  })

  it('records the operation before ending the session on the SSO re-login exit', async () => {
    // The SSO refusal is answered by the same exit: a sign-in through the
    // tenant's identity provider resumes the refused write, so the caller
    // records it before the cancel clears the gate and the logout ends the
    // session.
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
    // A 503 is not the deployment declining to raise the session -- the
    // session is established, but the failure is not an elevation refusal
    // (not a 401 shortfall), so it must not be merged into the unavailable
    // phase: the dialog shows its resolved code text and still offers the
    // flow.
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
