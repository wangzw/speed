/**
 * IdentifiersSection behaviour: the sign-in-identifiers list read off the
 * profile (GET /api/v1/auth/me) and the removal write
 * (DELETE /api/v1/auth/identifiers/{id}).
 *
 * What this suite pins: each identifier renders with its kind and value
 * and its verified/unverified state; a row's remove sends
 * DELETE /api/v1/auth/identifiers/{id} behind the danger confirm and refetches
 * the profile so the row drops off; a last-way-in refusal (409) renders
 * its code text, and the server's last-login-name code renders its own text
 * and re-reads the profile when the client guard read a stale list; a
 * removal refused with 401
 * authn.insufficient_user_authentication opens the step-up dialog, a won
 * step-up re-running it once; an account with no identifiers renders the
 * empty state; and an answer that omitted the identifiers key renders the
 * error state (never a "no identifiers" claim).
 */

import { describe, expect, it } from 'vitest'
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
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
import { hasCoarsePointerTouchTarget } from '@speed/test-utils/emitted-css'
import { expectNoAxeViolations } from '../test-utils/axe.js'
import { IdentifiersSection } from './IdentifiersSection.js'

const PROFILE_PATH = '/api/v1/auth/me'
const FLOW_START = '/api/v1/auth/flows/authenticated'
const FLOW_ANSWER = '/api/v1/auth/flows/answer'

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

  it('renders no verification chip for a username', async () => {
    // A username is a login name with no ownership to prove, so its row has
    // no verified state at all -- not even one read off `proven`.
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            identifiers: [
              identifier({
                id: 'id-self',
                kind: 'username',
                value: 'ada',
                proven: false,
                verified_via: 'self',
              }),
            ],
          }),
        )
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)

    expect(await screen.findByText('ada')).toBeTruthy()
    expect(screen.queryByText(zhCN.identifiers.unverified)).toBeNull()
    expect(screen.queryByText(zhCN.identifiers.verified)).toBeNull()
  })

  it('withholds removal of the only login name on a password account and explains why', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            password_set: true,
            identifiers: [
              identifier({
                id: 'id-user',
                kind: 'username',
                value: 'ada',
                proven: false,
                verified_at: '',
                verified_via: 'self',
              }),
            ],
          }),
        )
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)

    expect(await screen.findByText('ada')).toBeTruthy()
    // The password signs in through this row; removing it would leave the
    // password without a login name, so the action gives way to the rule.
    expect(
      screen.queryByRole('button', {
        name: zhCN.identifiers.removeAria.replace('{{value}}', 'ada'),
      }),
    ).toBeNull()
    expect(screen.getByText(zhCN.identifiers.lastLoginName)).toBeTruthy()

    await expectNoAxeViolations()
  })

  it('withholds removal of the only login name when it is an email and explains why', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            password_set: true,
            identifiers: [
              identifier({ id: 'id-email', kind: 'email', value: 'ada@x.test' }),
            ],
          }),
        )
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)

    expect(await screen.findByText('ada@x.test')).toBeTruthy()
    // An email address is a login name for a password account too.
    expect(
      screen.queryByRole('button', {
        name: zhCN.identifiers.removeAria.replace('{{value}}', 'ada@x.test'),
      }),
    ).toBeNull()
    expect(screen.getByText(zhCN.identifiers.lastLoginName)).toBeTruthy()
  })

  it('keeps a phone removable when the username is the only login name', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            password_set: true,
            identifiers: [
              identifier({
                id: 'id-user',
                kind: 'username',
                value: 'ada',
                proven: false,
                verified_at: '',
                verified_via: 'self',
              }),
              identifier({
                id: 'id-phone',
                kind: 'phone',
                value: '+14155550123',
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
    mount(respond)

    expect(await screen.findByText('ada')).toBeTruthy()
    expect(screen.getByText('+14155550123')).toBeTruthy()
    // The username is the password's only login name; the phone signs in
    // through its own code route, so it is not counted and stays removable.
    expect(
      screen.queryByRole('button', {
        name: zhCN.identifiers.removeAria.replace('{{value}}', 'ada'),
      }),
    ).toBeNull()
    expect(
      screen.getByRole('button', {
        name: zhCN.identifiers.removeAria.replace('{{value}}', '+14155550123'),
      }),
    ).toBeTruthy()
    // The explanation belongs to the withheld row, not to the phone row.
    expect(screen.getAllByText(zhCN.identifiers.lastLoginName)).toHaveLength(1)
  })

  it('keeps the username removal when an email also signs in', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            password_set: true,
            identifiers: [
              identifier({
                id: 'id-user',
                kind: 'username',
                value: 'ada',
                proven: false,
                verified_at: '',
                verified_via: 'self',
              }),
              identifier({
                id: 'id-email',
                kind: 'email',
                value: 'ada@x.test',
                proven: true,
              }),
            ],
          }),
        )
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)

    expect(await screen.findByText('ada')).toBeTruthy()
    expect(
      screen.getByRole('button', {
        name: zhCN.identifiers.removeAria.replace('{{value}}', 'ada'),
      }),
    ).toBeTruthy()
    expect(screen.queryByText(zhCN.identifiers.lastLoginName)).toBeNull()
  })

  it('keeps the only login name removable on an account with no password', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            password_set: false,
            identifiers: [
              identifier({
                id: 'id-user',
                kind: 'username',
                value: 'ada',
                proven: false,
                verified_at: '',
                verified_via: 'self',
              }),
            ],
          }),
        )
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)

    expect(await screen.findByText('ada')).toBeTruthy()
    expect(
      screen.getByRole('button', {
        name: zhCN.identifiers.removeAria.replace('{{value}}', 'ada'),
      }),
    ).toBeTruthy()
    expect(screen.queryByText(zhCN.identifiers.lastLoginName)).toBeNull()
  })

  it('does not offer the email kind while verification is pending', async () => {
    // A pending registration carries one contact awaiting proof; the add face
    // must not put a second pending address on the account.
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            identifiers: [],
            verification: {
              state: 'pending',
              email: 'ada@x.test',
              since: '',
            },
          }),
        )
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('button', { name: zhCN.identifiers.add.open }),
    )
    const combo = await screen.findByRole('combobox', {
      name: zhCN.identifiers.add.kind.label,
    })
    expect(combo).toHaveTextContent(zhCN.identifiers.add.kind.username)
    expect(combo).not.toHaveTextContent(zhCN.identifiers.add.kind.email)
  })

  it('removes an identifier behind the confirm and refetches', async () => {
    let removed = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            password_set: false,
            identifiers: removed ? [] : [identifier({ id: 'id-email' })],
          }),
        )
      }
      if (call.method === 'DELETE' && call.path === '/api/v1/auth/identifiers/id-email') {
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
          call.method === 'DELETE' && call.path === '/api/v1/auth/identifiers/id-email',
      ),
    ).toHaveLength(1)
  })

  it('renders a last-way-in refusal as its code text', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            password_set: false,
            identifiers: [identifier({ id: 'id-email' })],
          }),
        )
      }
      if (call.method === 'DELETE' && call.path === '/api/v1/auth/identifiers/id-email') {
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

  it('renders the last-login-name refusal when the guard read a stale profile', async () => {
    // The client guard withholds the action only when the profile it read
    // shows this row as the sole username/email. Another session can delete
    // the other login name in between, so the server refuses with its own
    // 409 and the section must render that code's text rather than the
    // generic conflict sentence. The re-read then converges on the server's
    // state: the remaining row is the only login name and loses its action.
    let profileReads = 0
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        profileReads += 1
        return jsonResponse(
          200,
          profileBody({
            password_set: true,
            identifiers:
              profileReads === 1
                ? [
                    identifier({
                      id: 'id-user',
                      kind: 'username',
                      value: 'ada',
                      proven: false,
                      verified_at: '',
                      verified_via: 'self',
                    }),
                    identifier({
                      id: 'id-email',
                      kind: 'email',
                      value: 'ada@x.test',
                    }),
                  ]
                : [
                    identifier({
                      id: 'id-email',
                      kind: 'email',
                      value: 'ada@x.test',
                    }),
                  ],
          }),
        )
      }
      if (
        call.method === 'DELETE' &&
        call.path === '/api/v1/auth/identifiers/id-email'
      ) {
        return errorResponse(409, 'authn.login_identifier_required')
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('button', {
        name: zhCN.identifiers.removeAria.replace('{{value}}', 'ada@x.test'),
      }),
    )
    const dialog = await screen.findByRole('dialog')
    await user.click(
      within(dialog).getByRole('button', {
        name: zhCN.identifiers.confirmLabel,
      }),
    )

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.errors.authn.login_identifier_required)
    expect(alert).not.toHaveTextContent(zhCN.errors.authn.conflict)
    // The decisive re-read: the profile is fetched again and the remaining
    // email becomes the only login name, so its removal gives way to the
    // note.
    await waitFor(() => expect(profileReads).toBe(2))
    expect(screen.getByText(zhCN.identifiers.lastLoginName)).toBeTruthy()
    expect(
      screen.queryByRole('button', {
        name: zhCN.identifiers.removeAria.replace('{{value}}', 'ada@x.test'),
      }),
    ).toBeNull()
  })

  it('gates a step_up_required removal behind the dialog and re-runs it once', async () => {
    const warn = captureRefreshWarning()
    let elevated = false
    let removed = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            password_set: false,
            identifiers: removed ? [] : [identifier({ id: 'id-email' })],
          }),
        )
      }
      if (call.method === 'DELETE' && call.path === '/api/v1/auth/identifiers/id-email') {
        if (!elevated) {
          return errorResponse(401, 'authn.insufficient_user_authentication')
        }
        removed = true
        return new Response(null, { status: 204 })
      }
      if (call.method === 'GET' && call.path === '/api/v1/auth/factors') {
        return jsonResponse(200, {
          factors: [
            {
              id: 'factor-1',
              kind: 'totp',
              label: 'App',
              status: 'active',
              created_at: '2026-01-01T00:00:00Z',
            },
          ],
        })
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
    await user.type(await screen.findByLabelText(zhCN.flowStep.codeLabel), '123456')
    await user.click(
      screen.getByRole('button', { name: zhCN.mfa.stepUp.confirmLabel }),
    )

    await waitFor(() =>
      expect(screen.getByText(zhCN.identifiers.empty.title)).toBeTruthy(),
    )
    expect(
      rig.calls.filter(
        (call) =>
          call.method === 'DELETE' && call.path === '/api/v1/auth/identifiers/id-email',
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

  it('adds a username in one step and refetches the profile', async () => {
    let added = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            identifiers: added
              ? [identifier({ id: 'id-user', kind: 'username', value: 'ada2' })]
              : [],
          }),
        )
      }
      if (call.method === 'POST' && call.path === '/api/v1/auth/identifiers') {
        added = true
        return jsonResponse(200, { state: 'added' })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { rig } = mount(respond)
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('button', { name: zhCN.identifiers.add.open }),
    )
    fireEvent.mouseDown(
      screen.getByRole('combobox', { name: zhCN.identifiers.add.kind.label }),
    )
    await user.click(
      screen.getByRole('option', { name: zhCN.identifiers.add.kind.username }),
    )
    await user.type(
      screen.getByLabelText(zhCN.identifiers.add.value.labelUsername),
      'ada2',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.identifiers.add.submitUsername }),
    )

    await waitFor(() =>
      expect(screen.getByText(zhCN.identifiers.add.added)).toBeTruthy(),
    )
    const posts = rig.calls.filter(
      (call) => call.method === 'POST' && call.path === '/api/v1/auth/identifiers',
    )
    expect(posts).toHaveLength(1)
    expect(posts[0]?.body).toEqual({ kind: 'username', value: 'ada2' })
    await waitFor(() => expect(screen.getByText('ada2')).toBeTruthy())
  })

  it('names a taken username instead of the generic conflict text', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileBody({ identifiers: [] }))
      }
      if (call.method === 'POST' && call.path === '/api/v1/auth/identifiers') {
        // An existing username is the expected 409 of the one write the
        // setup page's add face exists for.
        return errorResponse(409, 'authn.conflict')
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('button', { name: zhCN.identifiers.add.open }),
    )
    fireEvent.mouseDown(
      screen.getByRole('combobox', { name: zhCN.identifiers.add.kind.label }),
    )
    await user.click(
      screen.getByRole('option', { name: zhCN.identifiers.add.kind.username }),
    )
    await user.type(
      screen.getByLabelText(zhCN.identifiers.add.value.labelUsername),
      'alice',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.identifiers.add.submitUsername }),
    )

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.identifiers.add.usernameTaken)
    // The generic account-state sentence sends the user to refresh, which
    // can never free the name.
    expect(alert).not.toHaveTextContent(zhCN.errors.authn.conflict)
    expect(enUS.identifiers.add.usernameTaken).toMatch(/already taken/i)
    // The form stays open so another name can be typed.
    expect(
      screen.getByLabelText(zhCN.identifiers.add.value.labelUsername),
    ).toBeTruthy()
  })

  it('states the username first-character rule in the add helper', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileBody({ identifiers: [] }))
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('button', { name: zhCN.identifiers.add.open }),
    )
    fireEvent.mouseDown(
      screen.getByRole('combobox', { name: zhCN.identifiers.add.kind.label }),
    )
    await user.click(
      screen.getByRole('option', { name: zhCN.identifiers.add.kind.username }),
    )

    // The server's username shape begins with a letter or a digit
    // (^[a-z0-9][a-z0-9._-]{2,63}$); a leading dot, dash or underscore is
    // refused with the generic bad-request text. The helper is the setup
    // page's only guidance for the username it exists to add, so it has to
    // state that rule, in both languages.
    expect(
      await screen.findByText(zhCN.identifiers.add.value.helperUsername),
    ).toBeTruthy()
    // The wording claim is pinned on the en-US leaf; the zh-CN leaf is
    // pinned by the render assertion above.
    expect(enUS.identifiers.add.value.helperUsername).toMatch(
      /first character must be a letter or a digit/i,
    )
  })

  it('adds an email through the code step', async () => {
    let verified = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            identifiers: verified
              ? [identifier({ id: 'id-email', kind: 'email', value: 'ada@x.test' })]
              : [],
          }),
        )
      }
      if (call.method === 'POST' && call.path === '/api/v1/auth/identifiers') {
        return jsonResponse(200, {
          state: 'pending',
          expires_in: 600,
          resend_after: 60,
        })
      }
      if (call.method === 'POST' && call.path === '/api/v1/auth/identifiers/verify') {
        verified = true
        return jsonResponse(200, { state: 'added' })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { rig } = mount(respond)
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('button', { name: zhCN.identifiers.add.open }),
    )
    await user.type(
      screen.getByLabelText(zhCN.identifiers.add.value.labelEmail),
      'ada@x.test',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.identifiers.add.submitEmail }),
    )

    // The pending answer moves the form to the code step.
    await user.type(
      await screen.findByLabelText(zhCN.identifiers.add.code.label),
      '123456',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.identifiers.add.verify }),
    )

    await waitFor(() =>
      expect(screen.getByText(zhCN.identifiers.add.added)).toBeTruthy(),
    )
    const verifies = rig.calls.filter(
      (call) =>
        call.method === 'POST' && call.path === '/api/v1/auth/identifiers/verify',
    )
    expect(verifies).toHaveLength(1)
    expect(verifies[0]?.body).toEqual({ code: '123456' })
  })

  it('refuses a malformed email in the field without sending a request', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileBody({ identifiers: [] }))
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { rig } = mount(respond)
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('button', { name: zhCN.identifiers.add.open }),
    )
    // Two @ signs: the shape the server would answer with a bare 400.
    await user.type(
      screen.getByLabelText(zhCN.identifiers.add.value.labelEmail),
      'ada@example@com',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.identifiers.add.submitEmail }),
    )

    const message = await screen.findByText(zhCN.identifiers.add.invalidEmail)
    expect(message).toBeTruthy()
    expect(
      screen.getByLabelText(zhCN.identifiers.add.value.labelEmail),
    ).toHaveAttribute('aria-invalid', 'true')
    expect(screen.queryByRole('alert')).toBeNull()
    expect(
      rig.calls.filter(
        (call) => call.method === 'POST' && call.path === '/api/v1/auth/identifiers',
      ),
    ).toHaveLength(0)

    // Cancelling clears the field error with the rest of the attempt.
    await user.click(
      screen.getByRole('button', { name: zhCN.identifiers.add.cancel }),
    )
    expect(screen.queryByText(zhCN.identifiers.add.invalidEmail)).toBeNull()
    await user.click(
      screen.getByRole('button', { name: zhCN.identifiers.add.open }),
    )
    expect(
      await screen.findByLabelText(zhCN.identifiers.add.value.labelEmail),
    ).toHaveValue('')
    expect(screen.queryByText(zhCN.identifiers.add.invalidEmail)).toBeNull()
  })

  it('sends the trimmed address for a well-formed email', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileBody({ identifiers: [] }))
      }
      if (call.method === 'POST' && call.path === '/api/v1/auth/identifiers') {
        return jsonResponse(200, {
          state: 'pending',
          expires_in: 600,
          resend_after: 60,
        })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { rig } = mount(respond)
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('button', { name: zhCN.identifiers.add.open }),
    )
    const field = screen.getByLabelText(zhCN.identifiers.add.value.labelEmail)
    fireEvent.change(field, { target: { value: '  ada@x.test  ' } })
    await user.click(
      screen.getByRole('button', { name: zhCN.identifiers.add.submitEmail }),
    )

    await screen.findByLabelText(zhCN.identifiers.add.code.label)
    const posts = rig.calls.filter(
      (call) => call.method === 'POST' && call.path === '/api/v1/auth/identifiers',
    )
    expect(posts).toHaveLength(1)
    expect(posts[0]?.body).toEqual({ kind: 'email', value: 'ada@x.test' })
  })

  it('renders a server bad-request for an email as the field error', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileBody({ identifiers: [] }))
      }
      if (call.method === 'POST' && call.path === '/api/v1/auth/identifiers') {
        return errorResponse(400, 'authn.bad_request')
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('button', { name: zhCN.identifiers.add.open }),
    )
    await user.type(
      screen.getByLabelText(zhCN.identifiers.add.value.labelEmail),
      'ada@x.test',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.identifiers.add.submitEmail }),
    )

    expect(
      await screen.findByText(zhCN.identifiers.add.invalidEmail),
    ).toBeTruthy()
    expect(
      screen.queryByText(zhCN.errors.authn.bad_request),
    ).toBeNull()
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('renders a taken email on the value field, not the failure banner', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileBody({ identifiers: [] }))
      }
      if (call.method === 'POST' && call.path === '/api/v1/auth/identifiers') {
        return errorResponse(409, 'authn.email_taken')
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('button', { name: zhCN.identifiers.add.open }),
    )
    await user.type(
      screen.getByLabelText(zhCN.identifiers.add.value.labelEmail),
      'ada@x.test',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.identifiers.add.submitEmail }),
    )

    // The address's own failure: the field describes it, so the value the
    // viewer can change carries the message.
    expect(
      await screen.findByText(zhCN.identifiers.add.emailTaken),
    ).toBeTruthy()
    expect(
      screen.getByLabelText(zhCN.identifiers.add.value.labelEmail),
    ).toHaveAttribute('aria-invalid', 'true')
    // The code's generic account-state text and the shared banner stay out
    // of it: the section's one failure slot renders no role="alert".
    expect(screen.queryByText(zhCN.errors.authn.email_taken)).toBeNull()
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('returns a taken email from the verification step to the value field', async () => {
    // The write is the authoritative refusal: the address can be taken
    // between the code request and the code's submission, so the face
    // returns to the value -- the thing to change -- with the field's
    // message rather than asking for a code nothing can accept.
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileBody({ identifiers: [] }))
      }
      if (call.method === 'POST' && call.path === '/api/v1/auth/identifiers') {
        return jsonResponse(200, {
          state: 'pending',
          expires_in: 600,
          resend_after: 60,
        })
      }
      if (call.method === 'POST' && call.path === '/api/v1/auth/identifiers/verify') {
        return errorResponse(409, 'authn.email_taken')
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('button', { name: zhCN.identifiers.add.open }),
    )
    await user.type(
      screen.getByLabelText(zhCN.identifiers.add.value.labelEmail),
      'ada@x.test',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.identifiers.add.submitEmail }),
    )
    await user.type(
      await screen.findByLabelText(zhCN.identifiers.add.code.label),
      '123456',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.identifiers.add.verify }),
    )

    const email = await screen.findByLabelText(
      zhCN.identifiers.add.value.labelEmail,
    )
    expect(email).toHaveValue('ada@x.test')
    expect(email).toHaveAttribute('aria-invalid', 'true')
    expect(screen.getByText(zhCN.identifiers.add.emailTaken)).toBeTruthy()
    expect(
      screen.queryByLabelText(zhCN.identifiers.add.code.label),
    ).toBeNull()
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('clears the failure banner when the add face is cancelled', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileBody({ identifiers: [] }))
      }
      if (call.method === 'POST' && call.path === '/api/v1/auth/identifiers') {
        return errorResponse(503, 'authn.unavailable')
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('button', { name: zhCN.identifiers.add.open }),
    )
    await user.type(
      screen.getByLabelText(zhCN.identifiers.add.value.labelEmail),
      'ada@x.test',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.identifiers.add.submitEmail }),
    )
    expect(await screen.findByRole('alert')).toHaveTextContent(
      zhCN.errors.authn.unavailable,
    )

    await user.click(
      screen.getByRole('button', { name: zhCN.identifiers.add.cancel }),
    )

    // The banner belonged to the attempt, not to the section.
    expect(screen.queryByRole('alert')).toBeNull()
    expect(
      screen.queryByText(zhCN.errors.authn.unavailable),
    ).toBeNull()
    // The form is closed and an empty one opens on the next attempt.
    expect(
      screen.queryByLabelText(zhCN.identifiers.add.value.labelEmail),
    ).toBeNull()
    await user.click(
      screen.getByRole('button', { name: zhCN.identifiers.add.open }),
    )
    expect(
      await screen.findByLabelText(zhCN.identifiers.add.value.labelEmail),
    ).toHaveValue('')
  })

  it('renders a no-channel refusal of an email add as its code text', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileBody({ identifiers: [] }))
      }
      if (call.method === 'POST' && call.path === '/api/v1/auth/identifiers') {
        return errorResponse(503, 'authn.unavailable')
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('button', { name: zhCN.identifiers.add.open }),
    )
    await user.type(
      screen.getByLabelText(zhCN.identifiers.add.value.labelEmail),
      'ada@x.test',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.identifiers.add.submitEmail }),
    )

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.errors.authn.unavailable)
  })

  it('gates a step_up_required email add behind the dialog and re-runs it once', async () => {
    const warn = captureRefreshWarning()
    let elevated = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileBody({ identifiers: [] }))
      }
      if (call.method === 'POST' && call.path === '/api/v1/auth/identifiers') {
        if (!elevated) {
          return errorResponse(401, 'authn.insufficient_user_authentication')
        }
        return jsonResponse(200, {
          state: 'pending',
          expires_in: 600,
          resend_after: 60,
        })
      }
      if (call.method === 'GET' && call.path === '/api/v1/auth/factors') {
        return jsonResponse(200, {
          factors: [
            {
              id: 'factor-1',
              kind: 'totp',
              label: 'App',
              status: 'active',
              created_at: '2026-01-01T00:00:00Z',
            },
          ],
        })
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
      await screen.findByRole('button', { name: zhCN.identifiers.add.open }),
    )
    await user.type(
      screen.getByLabelText(zhCN.identifiers.add.value.labelEmail),
      'ada@x.test',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.identifiers.add.submitEmail }),
    )

    // The 401 opens the step-up dialog; winning it re-runs the add.
    await screen.findByRole('button', { name: 'App' })
    await user.click(screen.getByRole('button', { name: 'App' }))
    await user.type(await screen.findByLabelText(zhCN.flowStep.codeLabel), '123456')
    await user.click(
      screen.getByRole('button', { name: zhCN.mfa.stepUp.confirmLabel }),
    )

    await screen.findByLabelText(zhCN.identifiers.add.code.label)
    expect(
      rig.calls.filter(
        (call) => call.method === 'POST' && call.path === '/api/v1/auth/identifiers',
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
    // One shortfall, one refresh failure: a duplicated add would warn again.
    expect(warn).toHaveBeenCalledTimes(1)
  })

  it('re-proves an SMS first factor past the window and re-runs the username add', async () => {
    // The registration journey: an account opened by SMS sign-up has no
    // factor and no username. Past the five-minute window its identifier
    // write is refused for a fresh first factor -- which enrolling a
    // factor cannot satisfy either, so the dialog re-proves the SMS first
    // factor itself and the refused add runs again.
    let elevated = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileBody({ identifiers: [] }))
      }
      if (call.method === 'POST' && call.path === '/api/v1/auth/identifiers') {
        if (!elevated) {
          return errorResponse(401, 'authn.insufficient_user_authentication')
        }
        return jsonResponse(200, { state: 'added' })
      }
      if (call.method === 'GET' && call.path === '/api/v1/auth/factors') {
        return jsonResponse(200, { factors: [] })
      }
      if (call.method === 'GET' && call.path === '/api/v1/auth/session') {
        return jsonResponse(200, {
          authenticated: true,
          session_id: 'session-1',
          account_id: 'account-1',
          methods: ['sms'],
          assurance: 'single',
          auth_time: new Date(Date.now() - 6 * 60 * 1000).toISOString(),
        })
      }
      if (call.method === 'POST' && call.path === FLOW_START) {
        return jsonResponse(200, {
          seq: 1,
          state: 'first',
          next: { kind: 'challenge', fields: [{ name: 'code', kind: 'text' }] },
        })
      }
      if (call.method === 'POST' && call.path === FLOW_ANSWER) {
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
      await screen.findByRole('button', { name: zhCN.identifiers.add.open }),
    )
    fireEvent.mouseDown(
      screen.getByRole('combobox', { name: zhCN.identifiers.add.kind.label }),
    )
    await user.click(
      screen.getByRole('option', { name: zhCN.identifiers.add.kind.username }),
    )
    await user.type(
      screen.getByLabelText(zhCN.identifiers.add.value.labelUsername),
      'ada2',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.identifiers.add.submitUsername }),
    )

    // The refusal opens the dialog on the SMS method's first step.
    await user.type(await screen.findByLabelText(zhCN.flowStep.codeLabel), '123456')
    await user.click(
      screen.getByRole('button', { name: zhCN.mfa.stepUp.confirmLabel }),
    )

    await waitFor(() =>
      expect(screen.getByText(zhCN.identifiers.add.added)).toBeTruthy(),
    )
    const posts = rig.calls.filter(
      (call) => call.method === 'POST' && call.path === '/api/v1/auth/identifiers',
    )
    // The refused add ran once more, with the same input.
    expect(posts).toHaveLength(2)
    expect(posts[0]?.body).toEqual({ kind: 'username', value: 'ada2' })
    expect(posts[1]?.body).toEqual({ kind: 'username', value: 'ada2' })
    const start = rig.calls.find((call) => call.path === FLOW_START)
    expect(start?.body).toEqual({ purpose: 'step-up', method: 'sms' })
  })

  it('offers the email kind by default when the host does not gate identifiers', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileBody({ identifiers: [] }))
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()
    await user.click(
      await screen.findByRole('button', { name: zhCN.identifiers.add.open }),
    )
    // The add face's kind selector starts on the email option.
    const combo = await screen.findByRole('combobox')
    expect(combo).toHaveTextContent(zhCN.identifiers.add.kind.email)
    expect(combo).not.toHaveTextContent(zhCN.identifiers.add.kind.username)
  })

  it('offers usernames only when the deployment serves no email identifier', async () => {
    // The host reads the feature document and tells the section whether
    // authn.identifiers.email is served; a deployment without it must not
    // offer a write that would be refused.
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileBody({ identifiers: [] }))
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    renderWithProviders(
      <IdentifiersSection session={rig.session} emailAvailable={false} />,
    )
    fireEvent.click(
      await screen.findByRole('button', { name: zhCN.identifiers.add.open }),
    )
    const combo = await screen.findByRole('combobox')
    expect(combo).toHaveTextContent(zhCN.identifiers.add.kind.username)
    expect(combo).not.toHaveTextContent(zhCN.identifiers.add.kind.email)
  })

  it('does not offer the username kind when the host opts out', async () => {
    // An email-only product passes offerUsername={false}: the add face
    // keeps the email kind and never offers a username.
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileBody({ identifiers: [] }))
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    renderWithProviders(
      <IdentifiersSection session={rig.session} offerUsername={false} />,
    )
    fireEvent.click(
      await screen.findByRole('button', { name: zhCN.identifiers.add.open }),
    )
    const combo = await screen.findByRole('combobox')
    expect(combo).toHaveTextContent(zhCN.identifiers.add.kind.email)
    fireEvent.mouseDown(combo)
    expect(
      screen.queryByRole('option', {
        name: zhCN.identifiers.add.kind.username,
      }),
    ).toBeNull()
  })

  it('does not offer the username kind when the profile already lists one', async () => {
    // An account holds at most one username, and the server answers a
    // second one with the same 409 as a name someone else took. The add
    // face never sends that write: once a username is listed, the kind
    // leaves the selector.
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            identifiers: [
              identifier({ id: 'id-user', kind: 'username', value: 'ada' }),
            ],
          }),
        )
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()

    // The listed username still renders as a row.
    expect(await screen.findByText('ada')).toBeTruthy()
    await user.click(
      screen.getByRole('button', { name: zhCN.identifiers.add.open }),
    )
    const combo = await screen.findByRole('combobox', {
      name: zhCN.identifiers.add.kind.label,
    })
    expect(combo).toHaveTextContent(zhCN.identifiers.add.kind.email)
    fireEvent.mouseDown(combo)
    expect(
      await screen.findByRole('option', {
        name: zhCN.identifiers.add.kind.email,
      }),
    ).toBeTruthy()
    expect(
      screen.queryByRole('option', {
        name: zhCN.identifiers.add.kind.username,
      }),
    ).toBeNull()
  })

  it('renders no add face when a username exists and email is unavailable', async () => {
    // With the username kind gone and no email channel there is nothing
    // left to add, so the section does not offer the opening at all.
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            identifiers: [
              identifier({ id: 'id-user', kind: 'username', value: 'ada' }),
            ],
          }),
        )
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    renderWithProviders(
      <IdentifiersSection session={rig.session} emailAvailable={false} />,
    )

    expect(await screen.findByText('ada')).toBeTruthy()
    expect(
      screen.queryByRole('button', { name: zhCN.identifiers.add.open }),
    ).toBeNull()
  })

  it('renders one failure banner while the add face is open', async () => {
    // Removal, add and verification share one slot: a failure with a
    // non-empty list and the open form is still exactly one alert.
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({ identifiers: [identifier({ id: 'id-email' })] }),
        )
      }
      if (call.method === 'POST' && call.path === '/api/v1/auth/identifiers') {
        return errorResponse(503, 'authn.unavailable')
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()

    await screen.findByText('ada@example.test')
    await user.click(
      screen.getByRole('button', { name: zhCN.identifiers.add.open }),
    )
    await user.type(
      screen.getByLabelText(zhCN.identifiers.add.value.labelEmail),
      'new@x.test',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.identifiers.add.submitEmail }),
    )

    const alerts = await screen.findAllByRole('alert')
    expect(alerts).toHaveLength(1)
    expect(alerts[0]).toHaveTextContent(zhCN.errors.authn.unavailable)
  })

  it('raises the identifier actions to the 44px floor under a coarse pointer', async () => {
    // The row remove and the add-open controls are compact actions; on a
    // finger-driven device each one gates up to the 44x44 floor.
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            password_set: false,
            identifiers: [identifier({ id: 'id-email' })],
          }),
        )
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const remove = await screen.findByRole('button', {
      name: zhCN.identifiers.removeAria.replace('{{value}}', 'ada@example.test'),
    })
    const add = screen.getByRole('button', { name: zhCN.identifiers.add.open })
    expect(hasCoarsePointerTouchTarget(remove)).toBe(true)
    expect(hasCoarsePointerTouchTarget(add)).toBe(true)
  })
})
