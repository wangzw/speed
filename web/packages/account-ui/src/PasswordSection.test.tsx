/**
 * PasswordSection behaviour: the change-password surface over the profile
 * read (GET /auth/me, for the account's `password_set`) and the change
 * write (PUT /auth/password).
 *
 * What this suite pins: the current-password field renders only when the
 * profile says a password is set, and a provider-only account sees the
 * no-password note instead of a form; the two client-side guards (an
 * empty new password, a mismatched confirmation) render on their fields
 * and send no request; a valid change PUTs the current and new passwords,
 * fills the success live region and clears the fields; a wrong current
 * password (401 login-failed) renders its code text; and a change refused
 * with 401 authn.insufficient_user_authentication opens the step-up
 * dialog, a won step-up re-running it exactly once.
 */

import { describe, expect, it } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import zhCN from './locales/zh-CN.json' with { type: 'json' }
import { profileBody } from '../test-utils/fixtures.js'
import {
  errorResponse,
  jsonResponse,
  makeRealClientRig,
  type RealCall,
  type RealResponder,
} from '../test-utils/real-client.js'
import { renderWithProviders } from '../test-utils/render.js'
import { expectNoAxeViolations } from '../test-utils/axe.js'
import { PasswordSection } from './PasswordSection.js'

const PROFILE_PATH = '/auth/me'
const PASSWORD_PATH = '/auth/password'
const FLOW_START = '/auth/flows/authenticated'
const FLOW_ANSWER = '/auth/flows/answer'

function mount(respond: RealResponder) {
  const rig = makeRealClientRig(respond)
  rig.store.set('access-1')
  const rendered = renderWithProviders(<PasswordSection session={rig.session} />)
  return { rig, ...rendered }
}

describe('PasswordSection', () => {
  it('asks for the current password when one is set and passes axe', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileBody({ password_set: true }))
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)

    expect(
      await screen.findByRole('heading', { name: zhCN.password.title }),
    ).toBeTruthy()
    expect(await screen.findByLabelText(zhCN.password.current.label)).toBeTruthy()
    expect(screen.getByLabelText(zhCN.password.new.label)).toBeTruthy()
    expect(screen.getByLabelText(zhCN.password.confirm.label)).toBeTruthy()

    await expectNoAxeViolations()
  })

  it('offers no password form to a provider-only account', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileBody({ password_set: false }))
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)

    expect(await screen.findByText(zhCN.password.noPassword)).toBeTruthy()
    expect(screen.queryByLabelText(zhCN.password.new.label)).toBeNull()
    expect(
      screen.queryByRole('button', { name: zhCN.password.submit }),
    ).toBeNull()
  })

  it('refuses an empty or mismatched new password without a request', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileBody({ password_set: true }))
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { rig } = mount(respond)
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('button', { name: zhCN.password.submit }),
    )
    expect(await screen.findByText(zhCN.password.missingNew)).toBeTruthy()

    await user.type(screen.getByLabelText(zhCN.password.new.label), 'a-new-pass')
    await user.type(screen.getByLabelText(zhCN.password.confirm.label), 'other')
    await user.click(screen.getByRole('button', { name: zhCN.password.submit }))
    expect(await screen.findByText(zhCN.password.mismatch)).toBeTruthy()

    expect(rig.calls.some((call) => call.method === 'PUT')).toBe(false)
  })

  it('PUTs the current and new passwords, reports success and clears the fields', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileBody({ password_set: true }))
      }
      if (call.method === 'PUT' && call.path === PASSWORD_PATH) {
        return new Response(null, { status: 204 })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { rig } = mount(respond)
    const user = userEvent.setup()

    await user.type(
      await screen.findByLabelText(zhCN.password.current.label),
      'old-pass',
    )
    await user.type(screen.getByLabelText(zhCN.password.new.label), 'new-pass')
    await user.type(screen.getByLabelText(zhCN.password.confirm.label), 'new-pass')
    await user.click(screen.getByRole('button', { name: zhCN.password.submit }))

    await waitFor(() =>
      expect(screen.getByRole('status')).toHaveTextContent(zhCN.password.changed),
    )
    const put = rig.calls.find((call) => call.method === 'PUT')
    expect(put?.body).toEqual({
      current_password: 'old-pass',
      new_password: 'new-pass',
    })
    expect(screen.getByLabelText(zhCN.password.new.label)).toHaveValue('')
  })

  it('renders a wrong current password as its code text', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileBody({ password_set: true }))
      }
      if (call.method === 'PUT' && call.path === PASSWORD_PATH) {
        return errorResponse(401, 'authn.login_failed')
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()

    await user.type(
      await screen.findByLabelText(zhCN.password.current.label),
      'wrong',
    )
    await user.type(screen.getByLabelText(zhCN.password.new.label), 'new-pass')
    await user.type(screen.getByLabelText(zhCN.password.confirm.label), 'new-pass')
    await user.click(screen.getByRole('button', { name: zhCN.password.submit }))

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.errors.authn.login_failed)
  })

  it('gates a step_up_required change behind the dialog and re-runs it once', async () => {
    let elevated = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileBody({ password_set: true }))
      }
      if (call.method === 'PUT' && call.path === PASSWORD_PATH) {
        if (!elevated) {
          return errorResponse(401, 'authn.insufficient_user_authentication')
        }
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

    await user.type(
      await screen.findByLabelText(zhCN.password.current.label),
      'old-pass',
    )
    await user.type(screen.getByLabelText(zhCN.password.new.label), 'new-pass')
    await user.type(screen.getByLabelText(zhCN.password.confirm.label), 'new-pass')
    await user.click(screen.getByRole('button', { name: zhCN.password.submit }))

    await screen.findByRole('button', { name: 'App' })
    await user.click(screen.getByRole('button', { name: 'App' }))
    await user.type(await screen.findByLabelText('code'), '123456')
    await user.click(
      screen.getByRole('button', { name: zhCN.mfa.stepUp.confirmLabel }),
    )

    await waitFor(() =>
      expect(screen.getByRole('status')).toHaveTextContent(zhCN.password.changed),
    )
    const puts = rig.calls.filter((call) => call.method === 'PUT')
    expect(puts).toHaveLength(2)
    expect(puts[1]?.body).toEqual({
      current_password: 'old-pass',
      new_password: 'new-pass',
    })
  })
})
