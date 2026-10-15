/**
 * MfaSection behaviour: the factors read (GET /auth/factors), the remove
 * route (DELETE /auth/factors/{id}) and the `purpose: 'enroll'` flow
 * (POST /auth/flows/authenticated, POST /auth/flows/answer).
 *
 * What this suite pins: the factors render with their kind, label and
 * status beside the two enrollment entries; a remove DELETE-s the factor
 * and refetches the list; a remove refused with 401 step_up_required
 * opens the step-up dialog, and a won step-up re-runs exactly that
 * removal once; a 409 (last factor) renders its code text; and the TOTP
 * enrollment walks the flow -- the `webauthn` step's otpauth_uri is
 * rendered, the code is answered under `step: 'enroll'`, the ready step
 * is taken with `step: 'complete'` and the settled `enrolled` refetches
 * the factors.
 */

import { describe, expect, it } from 'vitest'
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
import { expectNoAxeViolations } from '../test-utils/axe.js'
import { MfaSection } from './MfaSection.js'

const FACTORS_PATH = '/auth/factors'
const FLOW_START = '/auth/flows/authenticated'
const FLOW_ANSWER = '/auth/flows/answer'

const OTPAUTH_URI = 'otpauth://totp/speed:ada?issuer=speed'

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
    // Both enrollment entries are offered.
    expect(
      screen.getByRole('button', { name: zhCN.mfa.enroll.totp }),
    ).toBeTruthy()
    expect(
      screen.getByRole('button', { name: zhCN.mfa.enroll.webauthn }),
    ).toBeTruthy()

    await expectNoAxeViolations()
  })

  it('removes a factor behind the confirm and refetches', async () => {
    let removed = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === FACTORS_PATH) {
        return jsonResponse(200, {
          factors: removed ? [] : [factor({})],
        })
      }
      if (call.method === 'DELETE' && call.path === '/auth/factors/factor-1') {
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
        (call) => call.method === 'DELETE' && call.path === '/auth/factors/factor-1',
      ),
    ).toHaveLength(1)
  })

  it('renders a last-factor refusal as code text', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === FACTORS_PATH) {
        return jsonResponse(200, { factors: [factor({})] })
      }
      if (call.method === 'DELETE' && call.path === '/auth/factors/factor-1') {
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
    let elevated = false
    let removed = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === FACTORS_PATH) {
        return jsonResponse(200, {
          factors: removed ? [] : [factor({})],
        })
      }
      if (call.method === 'DELETE' && call.path === '/auth/factors/factor-1') {
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
    await user.type(await screen.findByLabelText('code'), '123456')
    await user.click(
      screen.getByRole('button', { name: zhCN.mfa.stepUp.confirmLabel }),
    )

    // The won step-up re-runs the removal exactly once, which now lands.
    await waitFor(() => expect(screen.getByText(zhCN.mfa.empty)).toBeTruthy())
    expect(
      rig.calls.filter(
        (call) =>
          call.method === 'DELETE' && call.path === '/auth/factors/factor-1',
      ),
    ).toHaveLength(2)
  })

  it('walks the TOTP enrollment flow and refetches the factors', async () => {
    let enrolled = false
    const respond: RealResponder = (call: RealCall) => {
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

    // The webauthn step hands the factor's own JSON (the otpauth URI),
    // rendered as text in a monospace block.
    expect(
      await screen.findByText(
        (_content, element) =>
          element?.tagName.toLowerCase() === 'pre' &&
          (element.textContent ?? '').includes(OTPAUTH_URI),
      ),
    ).toBeTruthy()
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
})
