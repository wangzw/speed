/**
 * MfaSection behaviour: the factors read (GET /api/v1/auth/factors), the remove
 * route (DELETE /api/v1/auth/factors/{id}), the deployment's method discovery
 * (GET /api/v1/auth/methods) and the `purpose: 'enroll'` flow
 * (POST /api/v1/auth/flows/authenticated, POST /api/v1/auth/flows/answer).
 *
 * What this suite pins: the factors render with their kind, label and
 * status beside the enrollment entries the deployment reports -- only the
 * kinds GET /api/v1/auth/methods' mfa.factors lists are offered, so a deployment
 * without a passkey relying party shows no passkey entry; a remove
 * DELETE-s the factor and refetches the list; a remove refused with 401
 * step_up_required opens the step-up dialog, and a won step-up re-runs
 * exactly that removal once; a 409 (last factor) renders its code text;
 * and the TOTP enrollment walks the flow -- the `webauthn` step's
 * otpauth_uri is rendered, the code is answered under `step: 'enroll'`,
 * the ready step is taken with `step: 'complete'` and the settled
 * `enrolled` refetches the factors.
 */

import { describe, expect, it, vi } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import zhCN from './locales/zh-CN.json' with { type: 'json' }
import {
  errorResponse,
  jsonResponse,
  makeRealClientRig,
  type RealCall,
  type RealResponder,
} from '../test-utils/real-client.js'
import { renderWithProviders } from '../test-utils/render.js'
import { captureRefreshWarning } from '../test-utils/refresh-warning.js'
import { hasCoarsePointerTouchTarget } from '@speed/test-utils/emitted-css'
import { expectNoAxeViolations } from '../test-utils/axe.js'
import { MfaSection } from './MfaSection.js'

const FACTORS_PATH = '/api/v1/auth/factors'
const METHODS_PATH = '/api/v1/auth/methods'
const FLOW_START = '/api/v1/auth/flows/authenticated'
const FLOW_ANSWER = '/api/v1/auth/flows/answer'

const OTPAUTH_URI = 'otpauth://totp/speed:ada?issuer=speed'

/** The GET /api/v1/auth/methods discovery document, carrying the deployment's
 * enrollable factor kinds. */
function methodsDoc(factors: readonly string[]): Record<string, unknown> {
  return {
    methods: [],
    signup: { enabled: false },
    mfa: { factors: [...factors] },
  }
}

/** One factor row, in the factor fragment's own shape. */
function factor(overrides: Record<string, unknown>): Record<string, unknown> {
  return {
    id: 'factor-1',
    kind: 'totp',
    label: 'My authenticator',
    status: 'active',
    created_at: '2026-01-01T00:00:00Z',
    ...overrides,
  }
}

function mount(respond: RealResponder) {
  const rig = makeRealClientRig(respond)
  rig.store.set('access-1')
  const rendered = renderWithProviders(<MfaSection session={rig.session} />)
  return { rig, ...rendered }
}

describe('MfaSection', () => {
  it('renders the factors with their kind, label and status, and passes axe', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(200, methodsDoc(['totp', 'webauthn']))
      }
      if (call.method === 'GET' && call.path === FACTORS_PATH) {
        return jsonResponse(200, {
          factors: [
            factor({ id: 'factor-1', kind: 'totp', label: 'My authenticator' }),
            factor({
              id: 'factor-2',
              kind: 'webauthn',
              label: '',
              status: 'pending',
            }),
          ],
        })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)

    expect(
      await screen.findByRole('heading', { name: zhCN.mfa.title }),
    ).toBeTruthy()
    expect(await screen.findByText('My authenticator')).toBeTruthy()
    expect(
      screen.getAllByText(zhCN.mfa.factor.kind.totp).length,
    ).toBeGreaterThan(0)
    expect(screen.getByText(zhCN.mfa.factor.status.active)).toBeTruthy()
    expect(screen.getByText(zhCN.mfa.factor.status.pending)).toBeTruthy()
    // Both enrollment entries are offered (the deployment reports both).
    expect(
      await screen.findByRole('button', { name: zhCN.mfa.enroll.totp }),
    ).toBeTruthy()
    expect(
      screen.getByRole('button', { name: zhCN.mfa.enroll.webauthn }),
    ).toBeTruthy()

    await expectNoAxeViolations()
  })

  it('offers only the enrollment entries the deployment reports', async () => {
    // This deployment took no passkey relying party: its discovery document
    // lists totp alone, so the passkey entry is not offered -- the surface
    // never offers a factor the deployment cannot complete.
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(200, methodsDoc(['totp']))
      }
      if (call.method === 'GET' && call.path === FACTORS_PATH) {
        return jsonResponse(200, { factors: [] })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)

    // The reported kind renders; the unreported one does not.
    expect(
      await screen.findByRole('button', { name: zhCN.mfa.enroll.totp }),
    ).toBeTruthy()
    expect(
      screen.queryByRole('button', { name: zhCN.mfa.enroll.webauthn }),
    ).toBeNull()
  })

  it('removes a factor behind the confirm and refetches', async () => {
    let removed = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(200, methodsDoc(['totp', 'webauthn']))
      }
      if (call.method === 'GET' && call.path === FACTORS_PATH) {
        return jsonResponse(200, {
          factors: removed ? [] : [factor({})],
        })
      }
      if (call.method === 'DELETE' && call.path === '/api/v1/auth/factors/factor-1') {
        removed = true
        return new Response(null, { status: 204 })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { rig } = mount(respond)
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('button', {
        name: zhCN.mfa.removeAria.replace('{{factor}}', zhCN.mfa.factor.kind.totp),
      }),
    )
    const dialog = await screen.findByRole('dialog')
    await user.click(
      within(dialog).getByRole('button', {
        name: zhCN.mfa.removeConfirm.confirmLabel,
      }),
    )

    await waitFor(() =>
      expect(screen.getByText(zhCN.mfa.empty)).toBeTruthy(),
    )
    expect(
      rig.calls.filter(
        (call) => call.method === 'DELETE' && call.path === '/api/v1/auth/factors/factor-1',
      ),
    ).toHaveLength(1)
  })

  it('renders a last-factor refusal as code text', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(200, methodsDoc(['totp', 'webauthn']))
      }
      if (call.method === 'GET' && call.path === FACTORS_PATH) {
        return jsonResponse(200, { factors: [factor({})] })
      }
      if (call.method === 'DELETE' && call.path === '/api/v1/auth/factors/factor-1') {
        return errorResponse(409, 'authn.conflict')
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('button', {
        name: zhCN.mfa.removeAria.replace('{{factor}}', zhCN.mfa.factor.kind.totp),
      }),
    )
    const dialog = await screen.findByRole('dialog')
    await user.click(
      within(dialog).getByRole('button', {
        name: zhCN.mfa.removeConfirm.confirmLabel,
      }),
    )

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.errors.authn.conflict)
  })

  it('gates a step_up_required removal behind the dialog and re-runs it once', async () => {
    const warn = captureRefreshWarning()
    let elevated = false
    let removed = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(200, methodsDoc(['totp', 'webauthn']))
      }
      if (call.method === 'GET' && call.path === FACTORS_PATH) {
        return jsonResponse(200, {
          factors: removed ? [] : [factor({})],
        })
      }
      if (call.method === 'DELETE' && call.path === '/api/v1/auth/factors/factor-1') {
        if (!elevated) {
          return errorResponse(401, 'authn.insufficient_user_authentication')
        }
        removed = true
        return new Response(null, { status: 204 })
      }
      if (call.method === 'POST' && call.path === FLOW_START) {
        return jsonResponse(200, {
          seq: 1,
          state: 'second',
          next: {
            kind: 'factor',
            factors: [{ id: 'factor-1', kind: 'totp', label: 'App' }],
          },
        })
      }
      if (call.method === 'POST' && call.path === FLOW_ANSWER) {
        const body = call.body as { seq: number }
        if (body.seq === 1) {
          return jsonResponse(200, {
            seq: 2,
            state: 'second',
            next: { kind: 'challenge', fields: [{ name: 'code', kind: 'text' }] },
          })
        }
        elevated = true
        return jsonResponse(200, {
          state: 'elevated',
          token_type: 'Bearer',
          access_token: 'access-2',
          expires_in: 3600,
        })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { rig } = mount(respond)
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('button', {
        name: zhCN.mfa.removeAria.replace('{{factor}}', zhCN.mfa.factor.kind.totp),
      }),
    )
    const confirm = await screen.findByRole('dialog')
    await user.click(
      within(confirm).getByRole('button', {
        name: zhCN.mfa.removeConfirm.confirmLabel,
      }),
    )

    // The 401 opens the step-up dialog.
    await screen.findByRole('button', { name: 'App' })
    await user.click(screen.getByRole('button', { name: 'App' }))
    await user.type(await screen.findByLabelText(zhCN.flowStep.codeLabel), '123456')
    await user.click(
      screen.getByRole('button', { name: zhCN.mfa.stepUp.confirmLabel }),
    )

    // The won step-up re-runs the removal exactly once, which now lands.
    await waitFor(() => expect(screen.getByText(zhCN.mfa.empty)).toBeTruthy())
    expect(
      rig.calls.filter(
        (call) =>
          call.method === 'DELETE' && call.path === '/api/v1/auth/factors/factor-1',
      ),
    ).toHaveLength(2)
    // The step-up shortfall's 401 is the expected refresh-failure diagnostic.
    expect(warn).toHaveBeenCalledWith(
      'access token refresh failed',
      expect.objectContaining({
        status: 401,
        code: 'authn.insufficient_user_authentication',
        trace_id: 'trace-1',
      }),
    )
    // One shortfall, one refresh failure: a duplicated removal would warn
    // again.
    expect(warn).toHaveBeenCalledTimes(1)
  })

  it('walks the TOTP enrollment flow and refetches the factors', async () => {
    let enrolled = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(200, methodsDoc(['totp']))
      }
      if (call.method === 'GET' && call.path === FACTORS_PATH) {
        return jsonResponse(200, {
          factors: enrolled ? [factor({ id: 'factor-new' })] : [],
        })
      }
      if (call.method === 'POST' && call.path === FLOW_START) {
        return jsonResponse(200, {
          seq: 1,
          state: 'enroll',
          next: { kind: 'webauthn', options: { otpauth_uri: OTPAUTH_URI } },
        })
      }
      if (call.method === 'POST' && call.path === FLOW_ANSWER) {
        const body = call.body as { seq: number; step: string }
        if (body.step === 'enroll') {
          return jsonResponse(200, {
            seq: 2,
            state: 'ready',
            next: { kind: 'complete' },
          })
        }
        enrolled = true
        return jsonResponse(200, { state: 'enrolled' })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { rig } = mount(respond)
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('button', { name: zhCN.mfa.enroll.totp }),
    )

    // The enroll start names the factor kind.
    const start = rig.calls.find(
      (call) => call.method === 'POST' && call.path === FLOW_START,
    )
    expect(start?.body).toEqual({ purpose: 'enroll', input: { kind: 'totp' } })

    // The step's otpauth URI is its own read-only field: the step's JSON is
    // never rendered as a blob. The label is the field's accessible name,
    // and its value is the URI alone.
    const uriField = await screen.findByRole('textbox', {
      name: zhCN.flowStep.enrollTotpUriLabel,
    })
    expect(uriField).toBeVisible()
    expect(uriField).toHaveValue(OTPAUTH_URI)
    expect(uriField).toHaveAttribute('readonly')
    expect(screen.queryByText(/"otpauth_uri"/)).toBeNull()
    await user.type(
      screen.getByLabelText(zhCN.flowStep.codeLabel),
      '123456',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.mfa.enroll.confirmLabel }),
    )

    // The ready step is taken with `step: 'complete'`.
    await user.click(
      await screen.findByRole('button', { name: zhCN.mfa.enroll.confirmLabel }),
    )

    await waitFor(() => expect(screen.getByText('My authenticator')).toBeTruthy())
    const answers = rig.calls.filter(
      (call) => call.method === 'POST' && call.path === FLOW_ANSWER,
    )
    expect(answers[0]?.body).toEqual({
      seq: 1,
      step: 'enroll',
      input: { code: '123456' },
    })
    expect(answers[1]?.body).toEqual({ seq: 2, step: 'complete' })
  })

  it('renders no payload when a TOTP step carries no setup URI', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(200, methodsDoc(['totp']))
      }
      if (call.method === 'GET' && call.path === FACTORS_PATH) {
        return jsonResponse(200, { factors: [] })
      }
      if (call.method === 'POST' && call.path === FLOW_START) {
        return jsonResponse(200, {
          seq: 1,
          state: 'enroll',
          // The key is there, so the step is the TOTP one, but it carries no
          // usable URI. The code field still renders; the options do not.
          next: { kind: 'webauthn', options: { otpauth_uri: '' } },
        })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('button', { name: zhCN.mfa.enroll.totp }),
    )

    expect(await screen.findByLabelText(zhCN.flowStep.codeLabel)).toBeVisible()
    expect(
      screen.queryByRole('textbox', { name: zhCN.flowStep.enrollTotpUriLabel }),
    ).toBeNull()
    expect(screen.queryByText(/"otpauth_uri"/)).toBeNull()
  })

  it('walks a passkey enrollment, answering the credential under the enroll step', async () => {
    let enrolled = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(200, methodsDoc(['webauthn']))
      }
      if (call.method === 'GET' && call.path === FACTORS_PATH) {
        return jsonResponse(200, {
          factors: enrolled ? [factor({ id: 'factor-new', kind: 'webauthn' })] : [],
        })
      }
      if (call.method === 'POST' && call.path === FLOW_START) {
        return jsonResponse(200, {
          seq: 1,
          state: 'enroll',
          next: {
            kind: 'webauthn',
            options: {
              challenge: 'Y2hhbGxlbmdl',
              user: { id: 'AQID', name: 'ada' },
              rp: { id: 'localhost', name: 'demo' },
              pubKeyCredParams: [],
            },
          },
        })
      }
      if (call.method === 'POST' && call.path === FLOW_ANSWER) {
        const body = call.body as { seq: number; step: string }
        if (body.step === 'enroll') {
          return jsonResponse(200, { seq: 2, state: 'ready', next: { kind: 'complete' } })
        }
        enrolled = true
        return jsonResponse(200, { state: 'enrolled' })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { rig } = mount(respond)
    const user = userEvent.setup()
    // The registration ceremony is the browser's own API: stub create() and
    // hand back a credential shaped as it returns one.
    const create = vi.fn().mockResolvedValue({
      id: 'AQID',
      rawId: new Uint8Array([1, 2, 3]).buffer,
      type: 'public-key',
      response: {
        clientDataJSON: new Uint8Array([4]).buffer,
        attestationObject: new Uint8Array([5]).buffer,
        getTransports: () => ['internal'],
      },
      getClientExtensionResults: () => ({}),
      authenticatorAttachment: 'platform',
    })
    const original = Object.getOwnPropertyDescriptor(window.navigator, 'credentials')
    Object.defineProperty(window.navigator, 'credentials', {
      value: { create, get: vi.fn() },
      configurable: true,
    })
    try {
      await user.click(
        await screen.findByRole('button', { name: zhCN.mfa.enroll.webauthn }),
      )
      // The registration step drives navigator.credentials.create and
      // reports the credential under the enroll step, not a free-text field.
      await user.click(
        await screen.findByRole('button', { name: zhCN.mfa.enroll.confirmLabel }),
      )
      await user.click(
        await screen.findByRole('button', { name: zhCN.mfa.enroll.confirmLabel }),
      )
      await waitFor(() => expect(screen.getByText('My authenticator')).toBeTruthy())
    } finally {
      if (original === undefined) {
        delete (window.navigator as { credentials?: unknown }).credentials
      } else {
        Object.defineProperty(window.navigator, 'credentials', original)
      }
    }
    expect(create).toHaveBeenCalledTimes(1)
    const answers = rig.calls.filter(
      (call) => call.method === 'POST' && call.path === FLOW_ANSWER,
    )
    expect(answers[0]?.body).toMatchObject({ seq: 1, step: 'enroll' })
    expect(
      (answers[0]?.body as { input?: { credential?: string } }).input?.credential,
    ).toContain('AQID')
  })

  it('offers one entry per reported kind, translating known kinds and starting an unknown one', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        // The deployment reports a text-message factor and a kind this
        // package has no wording for.
        return jsonResponse(200, methodsDoc(['sms', 'magic']))
      }
      if (call.method === 'GET' && call.path === FACTORS_PATH) {
        return jsonResponse(200, { factors: [] })
      }
      if (call.method === 'POST' && call.path === FLOW_START) {
        return jsonResponse(200, {
          seq: 1,
          state: 'first',
          next: {
            kind: 'challenge',
            fields: [{ name: 'code', required: true }],
          },
        })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { rig } = mount(respond)
    const user = userEvent.setup()

    // The known kind renders its translated name; the unknown one gets the
    // generic label but is still offered.
    expect(
      await screen.findByRole('button', { name: zhCN.mfa.enroll.sms }),
    ).toBeTruthy()
    const unknown = screen.getByRole('button', {
      name: zhCN.mfa.enroll.other,
    })
    expect(unknown).toBeTruthy()

    await user.click(unknown)
    // The unknown kind's own flow starts; the generic label never hides
    // the deployment's vocabulary from the request.
    const start = rig.calls.find(
      (call) => call.method === 'POST' && call.path === FLOW_START,
    )
    expect(start?.body).toEqual({ purpose: 'enroll', input: { kind: 'magic' } })
  })

  it('raises the factor remove control to the 44px floor under a coarse pointer', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(200, methodsDoc(['totp']))
      }
      if (call.method === 'GET' && call.path === FACTORS_PATH) {
        return jsonResponse(200, { factors: [factor({})] })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const remove = await screen.findByRole('button', {
      name: zhCN.mfa.removeAria.replace('{{factor}}', zhCN.mfa.factor.kind.totp),
    })
    expect(hasCoarsePointerTouchTarget(remove)).toBe(true)
  })
})
