/**
 * PasswordSection checks the change-password surface's guards, refusals and step-up replay.
 */

import { describe, expect, it } from 'vitest'
import { act, fireEvent, screen, waitFor } from '@testing-library/react'
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

function deferredResponse(): {
  readonly response: Promise<Response>
  readonly release: (response: Response) => void
} {
  let release: (response: Response) => void = () => {}
  const response = new Promise<Response>((resolve) => {
    release = resolve
  })
  return { response, release }
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

    expect(await screen.findByLabelText(zhCN.password.new.label)).toBeTruthy()
    expect(screen.getByLabelText(zhCN.password.confirm.label)).toBeTruthy()
    expect(screen.queryByLabelText(zhCN.password.current.label)).toBeNull()
    expect(
      screen.getByRole('button', { name: zhCN.password.submitFirst }),
    ).toBeTruthy()
    expect(screen.getByText(zhCN.password.new.helperFirst)).toBeTruthy()
    expect(screen.queryByText(zhCN.password.new.helper)).toBeNull()
    expect(enUS.password.new.helperFirst).toMatch(/long enough/i)

    await expectNoAxeViolations()
  })

  it('keeps the form by default for an account with no login name', async () => {
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
    expect(enUS.password.loginNameRequired).toMatch(
      /username or an email address/i,
    )
    expect(enUS.password.loginNameRequired).not.toMatch(/verified/i)
    expect(screen.queryByLabelText(zhCN.password.new.label)).toBeNull()
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
                verified_at: '2026-01-01T00:00:00Z',
                verified_via: 'admin',
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
    const submit = screen.getByRole('button', { name: zhCN.password.submitFirst })
    expect(
      submit.compareDocumentPosition(alert) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy()
    expect(screen.getByRole('status')).toHaveTextContent('')
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
    expect(zhCN.password.changedFirst).toContain(zhCN.password.title)
    expect(enUS.password.changedFirst.toLowerCase()).toContain(
      enUS.password.title.toLowerCase(),
    )
    expect(
      screen
        .getByRole('button', { name: zhCN.password.submit })
        .compareDocumentPosition(screen.getByRole('status')) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy()
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
    expect(alert).not.toHaveTextContent(zhCN.errors.authn.login_failed)
    expect(enUS.password.currentRejected).toMatch(/current password/i)
    expect(enUS.password.currentRejected).not.toMatch(/verification code/i)
    expect(enUS.password.currentRejected).not.toMatch(/is incorrect/i)
    expect(enUS.password.currentRejected).toMatch(/wait/i)
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
    expect(warn).toHaveBeenCalledWith(
      'access token refresh failed',
      expect.objectContaining({
        status: 401,
        code: 'authn.insufficient_user_authentication',
        trace_id: 'trace-1',
      }),
    )
    expect(warn).toHaveBeenCalledTimes(1)
  })

  it('holds the session through the credential rotation a password change performs', async () => {
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

    await waitFor(() =>
      expect(warn).toHaveBeenCalledWith(
        'access token refresh failed',
        expect.objectContaining({ status: 401 }),
      ),
    )
    expect(rig.session.getSnapshot().state).toBe('authenticated')
    expect(rig.endedEdges()).toBe(0)
  })

  it('releases the window a refused password change opened', async () => {
    captureRefreshWarning({ expected: true })
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

    expect(screen.getByLabelText(zhCN.password.current.label)).toBeTruthy()
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.stale.description)
    expect(screen.queryByText(zhCN.password.error.title)).toBeNull()

    const readsBeforeRetry = reads
    await user.click(screen.getByRole('button', { name: zhCN.password.retry }))
    await waitFor(() => expect(reads).toBeGreaterThan(readsBeforeRetry))
  })

  it('keeps the field that submitted with Enter focused through the in-flight write', async () => {
    const held = deferredResponse()
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileBody({ password_set: true }))
      }
      if (call.method === 'PUT' && call.path === PASSWORD_PATH) {
        return held.response
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { rig } = mount(respond)
    const user = userEvent.setup()

    const current = await screen.findByLabelText(zhCN.password.current.label)
    const newPassword = screen.getByLabelText(zhCN.password.new.label)
    const confirm = screen.getByLabelText(zhCN.password.confirm.label)
    await user.type(current, 'old-pass')
    await user.type(newPassword, 'new-pass')
    await user.type(confirm, 'new-pass')
    await user.click(newPassword)
    await user.keyboard('{Enter}')

    await waitFor(() =>
      expect(rig.calls.some((call) => call.method === 'PUT')).toBe(true),
    )

    expect(newPassword).toHaveFocus()
    for (const field of [current, newPassword, confirm]) {
      expect(field).toHaveAttribute('readonly')
      expect(field).not.toBeDisabled()
    }
    const submit = screen.getByRole('button', { name: zhCN.password.submit })
    expect(submit).toHaveAttribute('aria-busy', 'true')
    expect(submit).toHaveAttribute('aria-disabled', 'true')
    expect(submit).not.toBeDisabled()

    await user.keyboard('x')
    expect(newPassword).toHaveValue('new-pass')
    await user.keyboard('{Enter}')
    expect(rig.calls.filter((call) => call.method === 'PUT')).toHaveLength(1)

    await act(async () => {
      held.release(new Response(null, { status: 204 }))
    })
    await waitFor(() =>
      expect(screen.getByRole('status')).toHaveTextContent(
        zhCN.password.changed,
      ),
    )
    expect(newPassword).toHaveFocus()
  })

  it('refuses a direct form submission while the change is in flight', async () => {
    const held = deferredResponse()
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileBody({ password_set: false }))
      }
      if (call.method === 'PUT' && call.path === PASSWORD_PATH) {
        return held.response
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { rig } = mount(respond)
    const user = userEvent.setup()

    const newPassword = await screen.findByLabelText(zhCN.password.new.label)
    const confirm = screen.getByLabelText(zhCN.password.confirm.label)
    await user.type(newPassword, 'first-pass')
    await user.type(confirm, 'first-pass')
    const form = newPassword.closest('form')
    expect(form).not.toBeNull()

    await user.click(
      screen.getByRole('button', { name: zhCN.password.submitFirst }),
    )
    await waitFor(() =>
      expect(rig.calls.some((call) => call.method === 'PUT')).toBe(true),
    )

    await act(async () => {
      fireEvent.submit(form as HTMLFormElement)
    })
    expect(rig.calls.filter((call) => call.method === 'PUT')).toHaveLength(1)

    await act(async () => {
      held.release(new Response(null, { status: 204 }))
    })
    await waitFor(() =>
      expect(screen.getByRole('status')).toHaveTextContent(
        zhCN.password.changedFirst,
      ),
    )
  })

  it('keeps the submit button focused while its own write is in flight', async () => {
    const held = deferredResponse()
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileBody({ password_set: false }))
      }
      if (call.method === 'PUT' && call.path === PASSWORD_PATH) {
        return held.response
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { rig } = mount(respond)
    const user = userEvent.setup()

    const newPassword = await screen.findByLabelText(zhCN.password.new.label)
    const confirm = screen.getByLabelText(zhCN.password.confirm.label)
    await user.type(newPassword, 'first-pass')
    await user.type(confirm, 'first-pass')
    const submit = screen.getByRole('button', {
      name: zhCN.password.submitFirst,
    })
    await user.click(submit)

    await waitFor(() =>
      expect(rig.calls.some((call) => call.method === 'PUT')).toBe(true),
    )
    expect(submit).toHaveFocus()
    expect(submit).toHaveAttribute('aria-busy', 'true')
    expect(submit).toHaveAttribute('aria-disabled', 'true')
    expect(newPassword).toHaveAttribute('readonly')
    expect(confirm).toHaveAttribute('readonly')

    await user.keyboard('{Enter}')
    expect(rig.calls.filter((call) => call.method === 'PUT')).toHaveLength(1)

    await act(async () => {
      held.release(new Response(null, { status: 204 }))
    })
    await waitFor(() =>
      expect(screen.getByRole('status')).toHaveTextContent(
        zhCN.password.changedFirst,
      ),
    )
    expect(submit).toHaveFocus()
  })
})
