/**
 * IdentifiersSection behaviour: the sign-in-identifiers list read off the
 * profile (GET /auth/me) and the removal write
 * (DELETE /auth/identifiers/{id}).
 *
 * What this suite pins: each identifier renders with its kind and value
 * and its verified/unverified state; a row's remove sends
 * DELETE /auth/identifiers/{id} behind the danger confirm and refetches
 * the profile so the row drops off; a last-way-in refusal (409) renders
 * its code text; a removal refused with 401
 * authn.insufficient_user_authentication opens the step-up dialog, a won
 * step-up re-running it once; an account with no identifiers renders the
 * empty state; and an answer that omitted the identifiers key renders the
 * error state (never a "no identifiers" claim).
 */

import { describe, expect, it } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import zhCN from './locales/zh-CN.json' with { type: 'json' }
import { identifier, profileBody } from '../test-utils/fixtures.js'
import {
  errorResponse,
  jsonResponse,
  makeRealClientRig,
  type RealCall,
  type RealResponder,
} from '../test-utils/real-client.js'
import { renderWithProviders } from '../test-utils/render.js'
import { expectNoAxeViolations } from '../test-utils/axe.js'
import { IdentifiersSection } from './IdentifiersSection.js'

const PROFILE_PATH = '/auth/me'
const FLOW_START = '/auth/flows/authenticated'
const FLOW_ANSWER = '/auth/flows/answer'

function mount(respond: RealResponder) {
  const rig = makeRealClientRig(respond)
  rig.store.set('access-1')
  const rendered = renderWithProviders(
    <IdentifiersSection session={rig.session} />,
  )
  return { rig, ...rendered }
}

describe('IdentifiersSection', () => {
  it('renders each identifier with its kind, value and verification, and passes axe', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            identifiers: [
              identifier({ id: 'id-email', kind: 'email', value: 'ada@x.test' }),
              identifier({
                id: 'id-phone',
                kind: 'phone',
                value: '+14155550123',
                verified_at: '',
                verified_via: '',
              }),
            ],
          }),
        )
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)

    expect(
      await screen.findByRole('heading', { name: zhCN.identifiers.title }),
    ).toBeTruthy()
    expect(await screen.findByText('ada@x.test')).toBeTruthy()
    expect(screen.getByText(zhCN.identifiers.kind.email)).toBeTruthy()
    expect(screen.getByText('+14155550123')).toBeTruthy()
    expect(screen.getByText(zhCN.identifiers.verified)).toBeTruthy()
    expect(screen.getByText(zhCN.identifiers.unverified)).toBeTruthy()

    await expectNoAxeViolations()
  })

  it('removes an identifier behind the confirm and refetches', async () => {
    let removed = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            identifiers: removed ? [] : [identifier({ id: 'id-email' })],
          }),
        )
      }
      if (call.method === 'DELETE' && call.path === '/auth/identifiers/id-email') {
        removed = true
        return new Response(null, { status: 204 })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { rig } = mount(respond)
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('button', {
        name: zhCN.identifiers.removeAria.replace('{{value}}', 'ada@example.test'),
      }),
    )
    const dialog = await screen.findByRole('dialog')
    await user.click(
      within(dialog).getByRole('button', {
        name: zhCN.identifiers.confirmLabel,
      }),
    )

    await waitFor(() =>
      expect(screen.getByText(zhCN.identifiers.empty.title)).toBeTruthy(),
    )
    expect(
      rig.calls.filter(
        (call) =>
          call.method === 'DELETE' && call.path === '/auth/identifiers/id-email',
      ),
    ).toHaveLength(1)
  })

  it('renders a last-way-in refusal as its code text', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({ identifiers: [identifier({ id: 'id-email' })] }),
        )
      }
      if (call.method === 'DELETE' && call.path === '/auth/identifiers/id-email') {
        return errorResponse(409, 'authn.conflict')
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('button', {
        name: zhCN.identifiers.removeAria.replace('{{value}}', 'ada@example.test'),
      }),
    )
    const dialog = await screen.findByRole('dialog')
    await user.click(
      within(dialog).getByRole('button', {
        name: zhCN.identifiers.confirmLabel,
      }),
    )

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.errors.authn.conflict)
  })

  it('gates a step_up_required removal behind the dialog and re-runs it once', async () => {
    let elevated = false
    let removed = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            identifiers: removed ? [] : [identifier({ id: 'id-email' })],
          }),
        )
      }
      if (call.method === 'DELETE' && call.path === '/auth/identifiers/id-email') {
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
        name: zhCN.identifiers.removeAria.replace('{{value}}', 'ada@example.test'),
      }),
    )
    const confirm = await screen.findByRole('dialog')
    await user.click(
      within(confirm).getByRole('button', {
        name: zhCN.identifiers.confirmLabel,
      }),
    )

    // The 401 opens the step-up dialog; winning it re-runs the removal.
    await screen.findByRole('button', { name: 'App' })
    await user.click(screen.getByRole('button', { name: 'App' }))
    await user.type(await screen.findByLabelText('code'), '123456')
    await user.click(
      screen.getByRole('button', { name: zhCN.mfa.stepUp.confirmLabel }),
    )

    await waitFor(() =>
      expect(screen.getByText(zhCN.identifiers.empty.title)).toBeTruthy(),
    )
    expect(
      rig.calls.filter(
        (call) =>
          call.method === 'DELETE' && call.path === '/auth/identifiers/id-email',
      ),
    ).toHaveLength(2)
  })

  it('renders the empty state for an account with no identifiers', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileBody({ identifiers: [] }))
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)

    expect(await screen.findByText(zhCN.identifiers.empty.title)).toBeTruthy()
    expect(
      screen.queryByRole('heading', { name: zhCN.identifiers.title }),
    ).toBeNull()
  })

  it('renders the error state when the answer omits the identifiers key', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        const body = profileBody()
        delete body.identifiers
        return jsonResponse(200, body)
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)

    expect(await screen.findByText(zhCN.identifiers.error.title)).toBeTruthy()
    expect(screen.queryByText(zhCN.identifiers.empty.title)).toBeNull()
  })
})
