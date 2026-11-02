/**
 * PasswordSection behaviour: the change-password surface over the profile
 * read (GET /api/v1/auth/me, for the account's `password_set`) and the change
 * write (PUT /api/v1/auth/password).
 *
 * What this suite pins: the current-password field renders only when the
 * profile says a password is set, and an account without one gets the
 * two new-password fields alone (its first password); the two client-side
 * guards (an empty new password, a mismatched confirmation) render on
 * their fields, send no request and clear a settled server result; a
 * valid change PUTs the current and new passwords, reports the success in
 * the region beneath the form and clears the fields; a 401 login-failed
 * renders the section's own sentence, which names no cause because the
 * server answers it for a wrong current password and, silently, while
 * the account's back-off is in force; a code outside the whitelist
 * renders the unknown fallback rather than a raw key; and a change
 * refused with 401 authn.insufficient_user_authentication opens the
 * step-up dialog, a won step-up re-running it exactly once.
 */

import { describe, expect, it } from 'vitest'
import { act, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import enUS from './locales/en-US.json' with { type: 'json' }
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
import { captureRefreshWarning } from '../test-utils/refresh-warning.js'
import {
  AUTHENTICATED_SESSION,
  EMPTY_FEATURES,
  FEATURES_PATH,
  SESSION_PATH,
  makeRotationRig,
} from '../test-utils/rotation.js'
import { expectNoAxeViolations } from '../test-utils/axe.js'
import { PasswordSection } from './PasswordSection.js'

const PROFILE_PATH = '/api/v1/auth/me'
const PASSWORD_PATH = '/api/v1/auth/password'
const FLOW_START = '/api/v1/auth/flows/authenticated'
const FLOW_ANSWER = '/api/v1/auth/flows/answer'

function mount(
  respond: RealResponder,
  withoutLoginName?: 'form' | 'explain',
) {
  const rig = makeRealClientRig(respond)
  rig.store.set('access-1')
  const rendered = renderWithProviders(
    <PasswordSection session={rig.session} withoutLoginName={withoutLoginName} />,
  )
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

  it('keeps the form by default for an account with no login name', async () => {
    // The default 'form' mode leaves the server's 409 to answer: the form
    // renders even though no identifier could serve as a login name.
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({ password_set: false, identifiers: [] }),
        )
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)

    expect(await screen.findByLabelText(zhCN.password.new.label)).toBeTruthy()
    expect(screen.queryByText(zhCN.password.loginNameRequired)).toBeNull()
  })

  it("explains the missing login name up front in 'explain' mode", async () => {
    // Neither a username nor an email address: the page that already knows
    // the account cannot set a password yet gets the explanation instead of
    // a form whose submit could only answer 409. A phone is not a login
    // name, however it was proved.
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            password_set: false,
            identifiers: [
              identifier({
                kind: 'phone',
                value: '+14155550123',
                proven: true,
                verified_via: 'sms-code',
              }),
            ],
          }),
        )
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond, 'explain')

    expect(
      await screen.findByText(zhCN.password.loginNameRequired),
    ).toBeTruthy()
    // The login name is an identifier of either kind; verification is not
    // part of the requirement the copy states.
    expect(enUS.password.loginNameRequired).toMatch(
      /username or an email address/i,
    )
    expect(enUS.password.loginNameRequired).not.toMatch(/verified/i)
    expect(screen.queryByLabelText(zhCN.password.new.label)).toBeNull()
    // The section header stays: the explanation belongs under it.
    expect(
      screen.getByRole('heading', { name: zhCN.password.title }),
    ).toBeTruthy()
    await expectNoAxeViolations()
  })

  it("keeps the form in 'explain' mode once a login name exists", async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            password_set: false,
            identifiers: [identifier({ kind: 'username', value: 'ada' })],
          }),
        )
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond, 'explain')

    expect(await screen.findByLabelText(zhCN.password.new.label)).toBeTruthy()
    expect(screen.queryByText(zhCN.password.loginNameRequired)).toBeNull()
  })

  it("treats an email as a login name without a verification timestamp", async () => {
    // The judgement is the identifier's kind: an email identifier is the
    // login name a password is set against even when its ownership was
    // never proved. Reading `verified_at` would withhold the form here.
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            password_set: false,
            identifiers: [
              identifier({
                kind: 'email',
                proven: false,
                verified_at: '',
                verified_via: '',
              }),
            ],
          }),
        )
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond, 'explain')

    expect(await screen.findByLabelText(zhCN.password.new.label)).toBeTruthy()
    expect(screen.queryByText(zhCN.password.loginNameRequired)).toBeNull()
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
    // The section's sentence renders in the same place a success does:
    // beneath the form, and it is the one settled result (no success stands
    // beside it).
    const submit = screen.getByRole('button', { name: zhCN.password.submitFirst })
    expect(
      submit.compareDocumentPosition(alert) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy()
    expect(screen.getByRole('status')).toHaveTextContent('')
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

  it('renders a generic refusal in the region beneath the form', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileBody({ password_set: true }))
      }
      if (call.method === 'PUT' && call.path === PASSWORD_PATH) {
        return errorResponse(403, 'authn.forbidden')
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()

    await user.type(
      await screen.findByLabelText(zhCN.password.current.label),
      'old-pass',
    )
    await user.type(screen.getByLabelText(zhCN.password.new.label), 'new-pass')
    await user.type(screen.getByLabelText(zhCN.password.confirm.label), 'new-pass')
    await user.click(screen.getByRole('button', { name: zhCN.password.submit }))

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.errors.authn.forbidden)
    // The region's failure sits in the same place as its success.
    expect(
      screen
        .getByRole('button', { name: zhCN.password.submit })
        .compareDocumentPosition(alert) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy()
    expect(screen.getByRole('status')).toHaveTextContent('')
  })

  it('renders the unknown fallback for a code outside the whitelist, never a raw key', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileBody({ password_set: false }))
      }
      if (call.method === 'PUT' && call.path === PASSWORD_PATH) {
        return errorResponse(400, 'authn.future_code')
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()

    await user.type(
      await screen.findByLabelText(zhCN.password.new.label),
      'new-pass',
    )
    await user.type(screen.getByLabelText(zhCN.password.confirm.label), 'new-pass')
    await user.click(
      screen.getByRole('button', { name: zhCN.password.submitFirst }),
    )

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.errors.unknown)
    expect(alert).not.toHaveTextContent('authn.future_code')
  })

  it('clears a settled server failure when a field guard refuses the next submit', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileBody({ password_set: false }))
      }
      if (call.method === 'PUT' && call.path === PASSWORD_PATH) {
        return errorResponse(403, 'authn.forbidden')
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()

    const newPassword = await screen.findByLabelText(zhCN.password.new.label)
    await user.type(newPassword, 'new-pass')
    await user.type(screen.getByLabelText(zhCN.password.confirm.label), 'new-pass')
    await user.click(
      screen.getByRole('button', { name: zhCN.password.submitFirst }),
    )
    expect(await screen.findByRole('alert')).toHaveTextContent(
      zhCN.errors.authn.forbidden,
    )

    // The next submit is refused by a field guard: the settled server
    // failure belongs to the attempt that just started and must not stay.
    await user.clear(newPassword)
    await user.click(
      screen.getByRole('button', { name: zhCN.password.submitFirst }),
    )
    expect(await screen.findByText(zhCN.password.missingNew)).toBeTruthy()
    expect(screen.queryByRole('alert')).toBeNull()
    expect(screen.getByRole('status')).toHaveTextContent('')
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
    // The confirmation names the changed object and sits beneath the form
    // that made it.
    expect(zhCN.password.changedFirst).toContain(zhCN.password.title)
    expect(enUS.password.changedFirst.toLowerCase()).toContain(
      enUS.password.title.toLowerCase(),
    )
    // The refetch flips the form to the change-password shape, but the
    // result still sits beneath the form's submit control.
    expect(
      screen
        .getByRole('button', { name: zhCN.password.submit })
        .compareDocumentPosition(screen.getByRole('status')) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy()
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
    // The confirmation names the changed object and sits beneath the form.
    expect(zhCN.password.changed).toContain(zhCN.password.title)
    expect(
      screen
        .getByRole('button', { name: zhCN.password.submit })
        .compareDocumentPosition(screen.getByRole('status')) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy()
    const put = rig.calls.find((call) => call.method === 'PUT')
    expect(put?.body).toEqual({
      current_password: 'old-pass',
      new_password: 'new-pass',
    })
    expect(screen.getByLabelText(zhCN.password.new.label)).toHaveValue('')
  })

  it('names a wrong current password in the section\'s own words', async () => {
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
    expect(alert).toHaveTextContent(zhCN.password.currentRejected)
    expect(
      screen
        .getByRole('button', { name: zhCN.password.submit })
        .compareDocumentPosition(alert) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy()
    // The code's own sign-in sentence names identifiers and a verification
    // code; none of those belong to this write, so the section must not
    // reuse it.
    expect(alert).not.toHaveTextContent(zhCN.errors.authn.login_failed)
    expect(enUS.password.currentRejected).toMatch(/current password/i)
    expect(enUS.password.currentRejected).not.toMatch(/verification code/i)
    // The same code answers a correct current password while the account's
    // back-off is in force, so the sentence must not assert that the
    // password is wrong, and it must tell the reader to wait after
    // repeated failures.
    expect(enUS.password.currentRejected).not.toMatch(/is incorrect/i)
    expect(enUS.password.currentRejected).toMatch(/wait/i)
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

    await screen.findByRole('button', { name: zhCN.mfa.factor.kind.totp })
    await user.click(screen.getByRole('button', { name: zhCN.mfa.factor.kind.totp }))
    await user.type(await screen.findByLabelText(zhCN.flowStep.codeLabel), '123456')
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

  it('holds the session through the credential rotation a password change performs', async () => {
    // The change rotates the requesting session's credential. This suite
    // scripts the changeover: the profile refetch that follows the PUT is
    // answered 401, standing for a read that left under the outgoing
    // credential (the response's Set-Cookie has not reached a read already
    // in flight, or one issued in the same round). The surface opens an
    // identity-transition window around the change, so the anonymous answer
    // speaks for the credential being replaced and is discarded: the
    // snapshot stays authenticated and no sign-out edge is observed. Without
    // the window the same script ends the session -- the regression this
    // case pins.
    const warn = captureRefreshWarning()
    let rotated = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === SESSION_PATH) {
        return rotated
          ? errorResponse(401, 'authn.login-failed')
          : jsonResponse(200, AUTHENTICATED_SESSION)
      }
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return rotated
          ? errorResponse(401, 'authn.login-failed')
          : jsonResponse(200, profileBody({ password_set: true }))
      }
      if (call.method === 'GET' && call.path === FEATURES_PATH) {
        return jsonResponse(200, EMPTY_FEATURES)
      }
      if (call.method === 'PUT' && call.path === PASSWORD_PATH) {
        rotated = true
        return new Response(null, { status: 204 })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = await makeRotationRig(respond)
    renderWithProviders(<PasswordSection session={rig.session} />)
    const user = userEvent.setup()

    await user.type(
      await screen.findByLabelText(zhCN.password.current.label),
      'old-pass',
    )
    await user.type(screen.getByLabelText(zhCN.password.new.label), 'new-pass')
    await user.type(screen.getByLabelText(zhCN.password.confirm.label), 'new-pass')
    await user.click(screen.getByRole('button', { name: zhCN.password.submit }))

    // The stale-credential read settles through the silent leg and reports
    // the failed refresh.
    await waitFor(() =>
      expect(warn).toHaveBeenCalledWith(
        'access token refresh failed',
        expect.objectContaining({ status: 401 }),
      ),
    )
    // The anonymous answer speaks for the credential the change replaced,
    // so it neither ends the session nor evicts the cache.
    expect(rig.session.getSnapshot().state).toBe('authenticated')
    expect(rig.endedEdges()).toBe(0)
  })

  it('releases the window a refused password change opened', async () => {
    // A refused change must not leave its window open: the next genuine
    // anonymous read has to end the session. The current-password 401 makes
    // the silent leg answer anonymous as well, and the open window discards
    // that answer while the write is in flight. The failure surfaces only
    // after the window was released, so the fresh 401 read that follows
    // turns the snapshot anonymous with one sign-out edge. A window left
    // open would keep the stale session and never sign the user out.
    let refused = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === SESSION_PATH) {
        return refused
          ? errorResponse(401, 'authn.login-failed')
          : jsonResponse(200, AUTHENTICATED_SESSION)
      }
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return refused
          ? errorResponse(401, 'authn.login-failed')
          : jsonResponse(200, profileBody({ password_set: true }))
      }
      if (call.method === 'GET' && call.path === FEATURES_PATH) {
        return jsonResponse(200, EMPTY_FEATURES)
      }
      if (call.method === 'PUT' && call.path === PASSWORD_PATH) {
        refused = true
        return errorResponse(401, 'authn.login_failed')
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = await makeRotationRig(respond)
    renderWithProviders(<PasswordSection session={rig.session} />)
    const user = userEvent.setup()

    await user.type(
      await screen.findByLabelText(zhCN.password.current.label),
      'old-pass',
    )
    await user.type(screen.getByLabelText(zhCN.password.new.label), 'new-pass')
    await user.type(screen.getByLabelText(zhCN.password.confirm.label), 'new-pass')
    await user.click(screen.getByRole('button', { name: zhCN.password.submit }))

    await screen.findByText(zhCN.password.currentRejected)
    expect(rig.transitionBegins()).toBe(1)

    // A fresh 401 read now settles the true state.
    await rig.session.restore()
    expect(rig.session.getSnapshot().state).toBe('anonymous')
    expect(rig.endedEdges()).toBe(1)
  })

  it('keeps the form and marks it stale when a refresh fails', async () => {
    const user = userEvent.setup()
    let reads = 0
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        reads += 1
        return reads === 1
          ? jsonResponse(200, profileBody({ password_set: true }))
          : errorResponse(500, 'authn.unavailable')
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { queryClient } = mount(respond)

    expect(
      await screen.findByLabelText(zhCN.password.current.label),
    ).toBeTruthy()

    await act(async () => {
      await queryClient.invalidateQueries()
    })

    // The failed refresh keeps the settled form on screen under the stale
    // notice rather than the error placeholder.
    expect(screen.getByLabelText(zhCN.password.current.label)).toBeTruthy()
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.stale.description)
    expect(screen.queryByText(zhCN.password.error.title)).toBeNull()

    const readsBeforeRetry = reads
    await user.click(screen.getByRole('button', { name: zhCN.password.retry }))
    await waitFor(() => expect(reads).toBeGreaterThan(readsBeforeRetry))
  })
})
