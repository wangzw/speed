/**
 * PasswordSection behaviour: the change-password surface over the profile
 * read (GET /api/v1/auth/me, for the account's `password_set`) and the change
 * write (PUT /api/v1/auth/password).
 *
 * What this suite pins: the current-password field renders only when the
 * profile says a password is set, and an account without one gets the
 * two new-password fields alone (its first password); the two client-side
 * guards (an empty new password, a mismatched confirmation) render on
 * their fields and send no request; a valid change PUTs the current and
 * new passwords, fills the success live region and clears the fields; a
 * wrong current password (401 login-failed) renders its code text; and a
 * change refused with 401 authn.insufficient_user_authentication opens the
 * step-up dialog, a won step-up re-running it exactly once.
 */

import { describe, expect, it } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import enUS from './locales/en-US.json' with { type: 'json' }
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
import { captureRefreshWarning } from '../test-utils/refresh-warning.js'
import { expectNoAxeViolations } from '../test-utils/axe.js'
import { PasswordSection } from './PasswordSection.js'

const PROFILE_PATH = '/api/v1/auth/me'
const PASSWORD_PATH = '/api/v1/auth/password'
const FLOW_START = '/api/v1/auth/flows/authenticated'
const FLOW_ANSWER = '/api/v1/auth/flows/answer'

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

  it('offers the first-password form to an account without a password', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileBody({ password_set: false }))
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)

    // No current password to ask for; the new-password pair is the whole
    // form, so a holder whose account carries none sets the first one.
    expect(await screen.findByLabelText(zhCN.password.new.label)).toBeTruthy()
    expect(screen.getByLabelText(zhCN.password.confirm.label)).toBeTruthy()
    expect(screen.queryByLabelText(zhCN.password.current.label)).toBeNull()
    expect(
      screen.getByRole('button', { name: zhCN.password.submitFirst }),
    ).toBeTruthy()
    // "A password this account has not used before" describes a change; an
    // account with no password needs the rule instead, and the rule names
    // the length requirement without a number (the minimum is the
    // deployment's setting). The wording claim is pinned on the en-US
    // leaf; the zh-CN leaf is pinned by the render assertions above.
    expect(screen.getByText(zhCN.password.new.helperFirst)).toBeTruthy()
    expect(screen.queryByText(zhCN.password.new.helper)).toBeNull()
    expect(enUS.password.new.helperFirst).toMatch(/long enough/i)

    await expectNoAxeViolations()
  })

  it('states the password policy when the server refuses a new password', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileBody({ password_set: false }))
      }
      if (call.method === 'PUT' && call.path === PASSWORD_PATH) {
        // The route's only 400 is the policy: min length, max bytes, or a
        // password equal to one of the account's own identifiers.
        return errorResponse(400, 'authn.bad_request')
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()

    await user.type(
      await screen.findByLabelText(zhCN.password.new.label),
      'short',
    )
    await user.type(screen.getByLabelText(zhCN.password.confirm.label), 'short')
    await user.click(
      screen.getByRole('button', { name: zhCN.password.submitFirst }),
    )

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.password.policyFailure)
    expect(alert).not.toHaveTextContent(zhCN.errors.authn.bad_request)
    // The route's 400 covers a password that is too long and, when
    // changing, one identical to the current password, so the sentence
    // names both without a number.
    expect(enUS.password.policyFailure).toMatch(
      /neither too short nor too long/i,
    )
    expect(enUS.password.policyFailure).toMatch(/current password/i)
    expect(enUS.password.policyFailure).toMatch(
      /does not meet the requirements/,
    )
  })

  it('reports a first password as set and names the other devices signed out', async () => {
    let passwordSet = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileBody({ password_set: passwordSet }))
      }
      if (call.method === 'PUT' && call.path === PASSWORD_PATH) {
        passwordSet = true
        return new Response(null, { status: 204 })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { rig } = mount(respond)
    const user = userEvent.setup()

    await user.type(
      await screen.findByLabelText(zhCN.password.new.label),
      'first-pass',
    )
    await user.type(screen.getByLabelText(zhCN.password.confirm.label), 'first-pass')
    await user.click(
      screen.getByRole('button', { name: zhCN.password.submitFirst }),
    )

    await waitFor(() =>
      expect(screen.getByRole('status')).toHaveTextContent(
        zhCN.password.changedFirst,
      ),
    )
    // The profile's refetch reports the password as set, but the text this
    // write produced is the first-password one. The server rotates this
    // session and revokes the account's others for a first password too, so
    // the notice must still say the other devices were signed out.
    expect(enUS.password.changedFirst).toMatch(
      /Other devices have been signed out/,
    )
    const put = rig.calls.find((call) => call.method === 'PUT')
    expect(put?.body).toEqual({ new_password: 'first-pass' })
    expect(screen.getByRole('status')).not.toHaveTextContent(
      zhCN.password.changed,
    )
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
    const warn = captureRefreshWarning()
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
    // The wrong-password 401 is the expected refresh-failure diagnostic.
    expect(warn).toHaveBeenCalledWith(
      'access token refresh failed',
      expect.objectContaining({
        status: 401,
        code: 'authn.login_failed',
        trace_id: 'trace-1',
      }),
    )
    // Exactly one refused request was sent; a retried or duplicated write
    // would each add its own refresh failure.
    expect(warn).toHaveBeenCalledTimes(1)
  })

  it('gates a step_up_required change behind the dialog and re-runs it once', async () => {
    const warn = captureRefreshWarning()
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
    // The step-up shortfall's 401 is the expected refresh-failure diagnostic.
    expect(warn).toHaveBeenCalledWith(
      'access token refresh failed',
      expect.objectContaining({
        status: 401,
        code: 'authn.insufficient_user_authentication',
        trace_id: 'trace-1',
      }),
    )
    // The shortfall and its re-run produce one refresh failure between
    // them; a second one means a write was sent twice.
    expect(warn).toHaveBeenCalledTimes(1)
  })
})
