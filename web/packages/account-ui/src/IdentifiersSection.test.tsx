/**
 * IdentifiersSection behaviour: the sign-in-identifiers list read off the
 * profile (GET /api/v1/auth/me) and the removal write
 * (DELETE /api/v1/auth/identifiers/{id}).
 *
 * What this suite pins: each identifier renders with its kind and value and
 * its one-of-two verified/unverified state; an unverified email row proves its
 * own address through the code flow and flips its chip; a row's remove sends
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
import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'
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
import { hasCoarsePointerTouchTarget } from '@speed/test-utils/emitted-css'
import { installFakeTurnstile } from '@speed/test-utils/turnstile'
import { expectNoAxeViolations } from '../test-utils/axe.js'
import { IdentifiersSection } from './IdentifiersSection.js'

const PROFILE_PATH = '/api/v1/auth/me'
const FLOW_START = '/api/v1/auth/flows/authenticated'
const FLOW_ANSWER = '/api/v1/auth/flows/answer'

function mount(
  respond: RealResponder,
  humanGate?: {
    readonly required: boolean
    readonly provider: string
    readonly site_key?: string
  },
) {
  const rig = makeRealClientRig(respond)
  rig.store.set('access-1')
  const rendered = renderWithProviders(
    <IdentifiersSection session={rig.session} humanGate={humanGate} />,
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

  it('renders every unproved address as Not verified, whatever established it', async () => {
    // The chip answers one question -- has ownership been proved? An address
    // an administrator asserted and an unverified import are both unproved,
    // so they read Not verified like any other; there is no third state.
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            identifiers: [
              identifier({
                id: 'id-admin',
                kind: 'email',
                value: 'operator@demo.test',
                proven: false,
                verified_via: 'admin',
              }),
              identifier({
                id: 'id-import',
                kind: 'email',
                value: 'imported@demo.test',
                proven: false,
                verified_via: 'import-unverified',
              }),
            ],
          }),
        )
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)

    expect(await screen.findByText('operator@demo.test')).toBeTruthy()
    expect(screen.getAllByText(zhCN.identifiers.unverified)).toHaveLength(2)
    expect(screen.queryByText(zhCN.identifiers.verified)).toBeNull()
    // The binary vocabulary has no channel labels left.
    expect(zhCN.identifiers).not.toHaveProperty('addedByAdmin')
    expect(zhCN.identifiers).not.toHaveProperty('importedUnverified')
  })

  it('offers a verify action on an unverified email row of a verified account', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            verification: { state: 'verified', email: 'ada@x.test', since: '' },
            identifiers: [
              identifier({ id: 'id-primary', kind: 'email', value: 'ada@x.test' }),
              identifier({
                id: 'id-secondary',
                kind: 'email',
                value: 'second@x.test',
                proven: false,
                verified_via: 'admin',
              }),
            ],
          }),
        )
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)

    expect(await screen.findByText('second@x.test')).toBeTruthy()
    // The account as a whole is verified, so the banner offers no entry for
    // this row; the row itself must.
    expect(
      screen.getByRole('button', {
        name: zhCN.identifiers.verifyAria.replace('{{value}}', 'second@x.test'),
      }),
    ).toBeTruthy()
    expect(
      screen.queryByRole('button', {
        name: zhCN.identifiers.verifyAria.replace('{{value}}', 'ada@x.test'),
      }),
    ).toBeNull()
  })

  it('keeps the verify action when the row is the only login name', async () => {
    // The remove action is withheld on a password account's only login name;
    // the verify action must still appear, or the unverified row would have
    // no entry at all.
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            password_set: true,
            identifiers: [
              identifier({
                id: 'id-only',
                kind: 'email',
                value: 'only@x.test',
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

    expect(await screen.findByText('only@x.test')).toBeTruthy()
    expect(
      screen.queryByRole('button', {
        name: zhCN.identifiers.removeAria.replace('{{value}}', 'only@x.test'),
      }),
    ).toBeNull()
    expect(
      screen.getByRole('button', {
        name: zhCN.identifiers.verifyAria.replace('{{value}}', 'only@x.test'),
      }),
    ).toBeTruthy()
  })

  it('proves an unverified row through its own address and flips the chip', async () => {
    let proven = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            verification: { state: 'verified', email: 'ada@x.test', since: '' },
            identifiers: [
              identifier({ id: 'id-primary', kind: 'email', value: 'ada@x.test' }),
              identifier({
                id: 'id-secondary',
                kind: 'email',
                value: 'second@x.test',
                proven,
                verified_via: proven ? 'email-code' : 'admin',
              }),
            ],
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
      if (
        call.method === 'POST' &&
        call.path === '/api/v1/auth/identifiers/verify'
      ) {
        proven = true
        return jsonResponse(200, { state: 'added' })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { rig } = mount(respond)
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('button', {
        name: zhCN.identifiers.verifyAria.replace('{{value}}', 'second@x.test'),
      }),
    )

    // The flow shows the row's address and does not let it change.
    const valueField = screen.getByLabelText(
      zhCN.identifiers.add.value.labelEmail,
    )
    expect(valueField).toHaveValue('second@x.test')
    expect(valueField).toHaveAttribute('readonly')

    await user.click(
      screen.getByRole('button', { name: zhCN.identifiers.add.submitEmail }),
    )
    await screen.findByLabelText(zhCN.identifiers.add.code.label)
    await expectNoAxeViolations()
    const sends = rig.calls.filter(
      (call) =>
        call.method === 'POST' && call.path === '/api/v1/auth/identifiers',
    )
    expect(sends).toHaveLength(1)
    expect(sends[0]?.body).toEqual({ kind: 'email', value: 'second@x.test' })

    await user.type(
      screen.getByLabelText(zhCN.identifiers.add.code.label),
      '123456',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.identifiers.add.verify }),
    )

    const verifies = rig.calls.filter(
      (call) =>
        call.method === 'POST' &&
        call.path === '/api/v1/auth/identifiers/verify',
    )
    expect(verifies).toHaveLength(1)
    expect(verifies[0]?.body).toEqual({ code: '123456' })
    // The refetched profile flips the row's chip; both rows are proved now.
    await waitFor(() =>
      expect(screen.getAllByText(zhCN.identifiers.verified)).toHaveLength(2),
    )
    expect(screen.queryByText(zhCN.identifiers.unverified)).toBeNull()
    // The row verification confirms itself, naming the address it proved.
    expect(
      await screen.findByText(
        zhCN.verification.verifiedDescription.replace(
          '{{email}}',
          'second@x.test',
        ),
      ),
    ).toBeTruthy()

    await expectNoAxeViolations()
  })

  it('does not offer the row verify action while the account is pending', async () => {
    // The pending state's one code record belongs to the verification
    // section and the host's banner; a second concurrent request from a row
    // would overwrite it.
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            verification: {
              state: 'pending',
              email: 'pending@x.test',
              since: '',
            },
            identifiers: [
              identifier({
                id: 'id-pending',
                kind: 'email',
                value: 'pending@x.test',
                proven: false,
                verified_via: 'admin',
              }),
            ],
          }),
        )
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)

    expect(await screen.findByText('pending@x.test')).toBeTruthy()
    expect(screen.getByText(zhCN.identifiers.unverified)).toBeTruthy()
    expect(
      screen.queryByRole('button', {
        name: zhCN.identifiers.verifyAria.replace(
          '{{value}}',
          'pending@x.test',
        ),
      }),
    ).toBeNull()
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
    // The silent removal now confirms itself, naming the identifier removed.
    expect(
      await screen.findByText(
        zhCN.identifiers.removed.replace('{{value}}', 'ada@example.test'),
      ),
    ).toBeTruthy()
    expect(zhCN.identifiers.removed).toContain('{{value}}')
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
        return errorResponse(409, 'authn.last_login_method')
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
    expect(alert).toHaveTextContent(zhCN.errors.authn.last_login_method)
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
    // The confirmation names the changed kind (the sign-in identifier).
    expect(zhCN.identifiers.add.added).toContain(zhCN.identifiers.title)
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
        return errorResponse(409, 'authn.username_taken')
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
    // can never free the name, and the unknown fallback names nothing.
    expect(alert).not.toHaveTextContent(zhCN.errors.authn.conflict)
    expect(alert).not.toHaveTextContent(zhCN.errors.unknown)
    expect(enUS.identifiers.add.usernameTaken).toMatch(/already taken/i)
    // The form stays open so another name can be typed.
    expect(
      screen.getByLabelText(zhCN.identifiers.add.value.labelUsername),
    ).toBeTruthy()
  })

  it('names a taken username the identifier route answers as a conflict', async () => {
    // The identifier route answers the same collision with its own conflict
    // code; for this face both refusals mean the name is not available.
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileBody({ identifiers: [] }))
      }
      if (call.method === 'POST' && call.path === '/api/v1/auth/identifiers') {
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
    expect(alert).not.toHaveTextContent(zhCN.errors.authn.conflict)
  })

  it('states a refused username in its field rather than the banner', async () => {
    // A reserved name and a name the server cannot normalize are both the
    // value this face can change, so the field carries the message.
    for (const [code, text] of [
      ['authn.username_reserved', zhCN.identifiers.add.usernameReserved],
      ['authn.bad_request', zhCN.identifiers.add.usernameInvalid],
    ] as const) {
      const respond: RealResponder = (call: RealCall) => {
        if (call.method === 'GET' && call.path === PROFILE_PATH) {
          return jsonResponse(200, profileBody({ identifiers: [] }))
        }
        if (call.method === 'POST' && call.path === '/api/v1/auth/identifiers') {
          return errorResponse(400, code)
        }
        throw new Error(`unexpected ${call.method} ${call.path}`)
      }
      const { unmount } = mount(respond)
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
      const field = screen.getByLabelText(zhCN.identifiers.add.value.labelUsername)
      await user.type(field, 'ada')
      await user.click(
        screen.getByRole('button', { name: zhCN.identifiers.add.submitUsername }),
      )

      expect(await screen.findByText(text)).toBeInTheDocument()
      expect(screen.queryByRole('alert')).not.toBeInTheDocument()
      // Editing the name clears the message for the next attempt.
      await user.type(field, '2')
      expect(screen.queryByText(text)).not.toBeInTheDocument()
      unmount()
    }
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
    await screen.findByLabelText(zhCN.identifiers.add.code.label)
    // The send announces the destination through its own status region and
    // hands focus to the code field.
    const sentNotice = zhCN.identifiers.add.sent.replace(
      '{{value}}',
      'ada@x.test',
    )
    expect(screen.getByText(sentNotice)).toBeTruthy()
    expect(screen.getByText(sentNotice).closest('[role="status"]')).not.toBeNull()
    expect(screen.getByLabelText(zhCN.identifiers.add.code.label)).toHaveFocus()
    await user.type(
      screen.getByLabelText(zhCN.identifiers.add.code.label),
      '123456',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.identifiers.add.verify }),
    )

    const added = await screen.findByText(zhCN.identifiers.add.added)
    expect(added).toBeTruthy()
    // The result sits below the whole control group (the add face), not
    // between the list and the add face.
    expect(
      screen
        .getByRole('button', { name: zhCN.identifiers.add.open })
        .compareDocumentPosition(added) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy()
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

  it('holds the session through the credential rotation a username add performs', async () => {
    // The one-step username add rotates the requesting session's
    // credential. The profile refetch that follows the write is answered
    // 401, standing for a read that left under the outgoing credential. The
    // surface opens an identity-transition window around that write, so the
    // anonymous answer is discarded: the snapshot stays authenticated and no
    // sign-out edge is observed. Without the window the same script ends the
    // session.
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
          : jsonResponse(200, profileBody({ identifiers: [] }))
      }
      if (call.method === 'GET' && call.path === FEATURES_PATH) {
        return jsonResponse(200, EMPTY_FEATURES)
      }
      if (call.method === 'POST' && call.path === '/api/v1/auth/identifiers') {
        rotated = true
        return jsonResponse(200, { state: 'added' })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = await makeRotationRig(respond)
    renderWithProviders(<IdentifiersSection session={rig.session} />)
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
      expect(warn).toHaveBeenCalledWith(
        'access token refresh failed',
        expect.objectContaining({ status: 401 }),
      ),
    )
    expect(rig.session.getSnapshot().state).toBe('authenticated')
    expect(rig.endedEdges()).toBe(0)
  })

  it('holds the session through the credential rotation an added email verifies', async () => {
    // Sending the code does not rotate the credential; the verification that
    // writes the address does. The profile refetch that follows the verify
    // is answered 401, standing for a read that left under the outgoing
    // credential. The surface opens an identity-transition window around
    // that write, so the anonymous answer is discarded: the snapshot stays
    // authenticated and no sign-out edge is observed.
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
          : jsonResponse(200, profileBody({ identifiers: [] }))
      }
      if (call.method === 'GET' && call.path === FEATURES_PATH) {
        return jsonResponse(200, EMPTY_FEATURES)
      }
      if (call.method === 'POST' && call.path === '/api/v1/auth/identifiers') {
        return jsonResponse(200, {
          state: 'pending',
          expires_in: 600,
          resend_after: 60,
        })
      }
      if (
        call.method === 'POST' &&
        call.path === '/api/v1/auth/identifiers/verify'
      ) {
        rotated = true
        return jsonResponse(200, { state: 'added' })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = await makeRotationRig(respond)
    renderWithProviders(<IdentifiersSection session={rig.session} />)
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

    await waitFor(() =>
      expect(warn).toHaveBeenCalledWith(
        'access token refresh failed',
        expect.objectContaining({ status: 401 }),
      ),
    )
    expect(rig.session.getSnapshot().state).toBe('authenticated')
    expect(rig.endedEdges()).toBe(0)
  })

  it('holds the session through the credential rotation a row verification performs', async () => {
    // A row's verification proves the address and rotates the requesting
    // session's credential, exactly as the add-face verification does. The
    // profile refetch that follows the write is answered 401, standing for a
    // read that left under the outgoing credential; the window around the
    // write discards it, so the snapshot stays authenticated.
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
          : jsonResponse(
              200,
              profileBody({
                verification: {
                  state: 'verified',
                  email: 'ada@x.test',
                  since: '',
                },
                identifiers: [
                  identifier({
                    id: 'id-primary',
                    kind: 'email',
                    value: 'ada@x.test',
                  }),
                  identifier({
                    id: 'id-secondary',
                    kind: 'email',
                    value: 'second@x.test',
                    proven: false,
                    verified_via: 'admin',
                  }),
                ],
              }),
            )
      }
      if (call.method === 'GET' && call.path === FEATURES_PATH) {
        return jsonResponse(200, EMPTY_FEATURES)
      }
      if (call.method === 'POST' && call.path === '/api/v1/auth/identifiers') {
        return jsonResponse(200, {
          state: 'pending',
          expires_in: 600,
          resend_after: 60,
        })
      }
      if (
        call.method === 'POST' &&
        call.path === '/api/v1/auth/identifiers/verify'
      ) {
        rotated = true
        return jsonResponse(200, { state: 'added' })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = await makeRotationRig(respond)
    renderWithProviders(<IdentifiersSection session={rig.session} />)
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('button', {
        name: zhCN.identifiers.verifyAria.replace('{{value}}', 'second@x.test'),
      }),
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

    await waitFor(() =>
      expect(warn).toHaveBeenCalledWith(
        'access token refresh failed',
        expect.objectContaining({ status: 401 }),
      ),
    )
    expect(rig.session.getSnapshot().state).toBe('authenticated')
    expect(rig.endedEdges()).toBe(0)
  })

  it('releases the window a refused username add opened', async () => {
    // A refused one-step username add must not leave its window open: the
    // next genuine anonymous read has to end the session. The add is refused
    // 409 (the name is taken), so no silent leg runs; once the failure is on
    // screen the window is closed, and a fresh 401 read turns the snapshot
    // anonymous with one sign-out edge.
    let gone = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === SESSION_PATH) {
        return gone
          ? errorResponse(401, 'authn.login-failed')
          : jsonResponse(200, AUTHENTICATED_SESSION)
      }
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return gone
          ? errorResponse(401, 'authn.login-failed')
          : jsonResponse(200, profileBody({ identifiers: [] }))
      }
      if (call.method === 'GET' && call.path === FEATURES_PATH) {
        return jsonResponse(200, EMPTY_FEATURES)
      }
      if (call.method === 'POST' && call.path === '/api/v1/auth/identifiers') {
        return errorResponse(409, 'authn.username_taken')
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = await makeRotationRig(respond)
    renderWithProviders(<IdentifiersSection session={rig.session} />)
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

    await screen.findByText(zhCN.identifiers.add.usernameTaken)
    expect(rig.transitionBegins()).toBe(1)

    // A fresh 401 read now settles the true state.
    gone = true
    await rig.session.restore()
    expect(rig.session.getSnapshot().state).toBe('anonymous')
    expect(rig.endedEdges()).toBe(1)
  })

  it('refuses an email add until the required verifier holds a token, then sends it', async () => {
    const fake = installFakeTurnstile()
    try {
      const respond: RealResponder = (call: RealCall) => {
        if (call.method === 'GET' && call.path === PROFILE_PATH) {
          return jsonResponse(200, profileBody({ identifiers: [] }))
        }
        if (
          call.method === 'POST' &&
          call.path === '/api/v1/auth/identifiers'
        ) {
          return jsonResponse(200, {
            state: 'pending',
            expires_in: 600,
            resend_after: 60,
          })
        }
        throw new Error(`unexpected ${call.method} ${call.path}`)
      }
      const { rig } = mount(respond, {
        required: true,
        provider: 'turnstile',
        site_key: 'site-1',
      })
      const user = userEvent.setup()

      await user.click(
        await screen.findByRole('button', { name: zhCN.identifiers.add.open }),
      )
      await user.type(
        screen.getByLabelText(zhCN.identifiers.add.value.labelEmail),
        'new@x.test',
      )
      await waitFor(() => expect(fake.widgets).toHaveLength(1))
      const submit = screen.getByRole('button', {
        name: zhCN.identifiers.add.submitEmail,
      })
      expect(submit).toBeDisabled()
      act(() => {
        fake.token('token-1')
      })
      await waitFor(() => expect(submit).toBeEnabled())
      await user.click(submit)

      await waitFor(() =>
        expect(
          rig.calls.filter(
            (call) =>
              call.method === 'POST' &&
              call.path === '/api/v1/auth/identifiers',
          ),
        ).toHaveLength(1),
      )
      const posts = rig.calls.filter(
        (call) =>
          call.method === 'POST' &&
          call.path === '/api/v1/auth/identifiers',
      )
      expect(posts).toHaveLength(1)
      expect(posts[0]?.body).toEqual({
        kind: 'email',
        value: 'new@x.test',
        human_proof: 'token-1',
      })
    } finally {
      fake.restore()
    }
  })

  it('keeps the identifier rows and marks them stale when a refresh fails', async () => {
    const user = userEvent.setup()
    let reads = 0
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        reads += 1
        return reads === 1
          ? jsonResponse(
              200,
              profileBody({
                identifiers: [
                  identifier({
                    id: 'id-email',
                    kind: 'email',
                    value: 'ada@x.test',
                  }),
                ],
              }),
            )
          : errorResponse(500, 'authn.unavailable')
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { queryClient } = mount(respond)

    expect(await screen.findByText('ada@x.test')).toBeTruthy()

    await act(async () => {
      await queryClient.invalidateQueries()
    })

    // The failed refresh keeps the settled rows on screen under the stale
    // notice rather than replacing them with the error placeholder.
    expect(screen.getByText('ada@x.test')).toBeTruthy()
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.stale.description)
    expect(screen.queryByText(zhCN.identifiers.error.title)).toBeNull()

    const readsBeforeRetry = reads
    await user.click(screen.getByRole('button', { name: zhCN.identifiers.retry }))
    await waitFor(() => expect(reads).toBeGreaterThan(readsBeforeRetry))
  })

  it('groups the identifiers by the two axes, with an empty list of its own, and passes axe', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
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
                id: 'id-signin',
                kind: 'email',
                value: 'signin@x.test',
                sign_in: true,
                contact: false,
              }),
              identifier({
                id: 'id-contact',
                kind: 'email',
                value: 'contact@x.test',
                sign_in: false,
                contact: true,
              }),
              identifier({
                id: 'id-phone',
                kind: 'phone',
                value: '+14155550123',
                proven: false,
                verified_at: '',
                verified_via: '',
                sign_in: false,
                contact: false,
              }),
            ],
          }),
        )
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)

    const signIn = await screen.findByRole('region', {
      name: zhCN.identifiers.groups.signIn,
    })
    const contacts = screen.getByRole('region', {
      name: zhCN.identifiers.groups.contact,
    })
    const neither = screen.getByRole('region', {
      name: zhCN.identifiers.groups.neither,
    })
    // A username and the address the profile marks sign_in are sign-in ways.
    expect(within(signIn).getByText('ada')).toBeTruthy()
    expect(within(signIn).getByText('signin@x.test')).toBeTruthy()
    // The address the profile marks contact, and only it, is a contact.
    expect(within(contacts).getByText('contact@x.test')).toBeTruthy()
    expect(within(contacts).queryByText('signin@x.test')).toBeNull()
    // The phone is on neither axis.
    expect(within(neither).getByText('+14155550123')).toBeTruthy()
    expect(within(neither).queryByText('ada')).toBeNull()

    await expectNoAxeViolations()
  })

  it('renders an empty note for each list that has no row', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
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

    await screen.findByText('ada')
    expect(screen.getByText(zhCN.identifiers.groups.contactEmpty)).toBeTruthy()
    expect(screen.getByText(zhCN.identifiers.groups.neitherEmpty)).toBeTruthy()
  })

  it('offers the contact switch on an unproved address but no sign-in switch, and writes the contact axis', async () => {
    let contact = false
    const writes: unknown[] = []
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            identifiers: [
              identifier({
                id: 'id-email',
                kind: 'email',
                value: 'new@x.test',
                proven: false,
                verified_at: '',
                verified_via: 'admin',
                sign_in: false,
                contact,
              }),
            ],
          }),
        )
      }
      if (
        call.method === 'PUT' &&
        call.path === '/api/v1/auth/identifiers/id-email'
      ) {
        writes.push(call.body)
        contact = (call.body as { contact?: boolean }).contact === true
        return new Response(null, { status: 204 })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()

    // An unproved value cannot be a sign-in way, so that switch is absent;
    // its contact axis is still selectable.
    expect(
      screen.queryByRole('switch', {
        name: zhCN.identifiers.axes.signInAria.replace('{{value}}', 'new@x.test'),
      }),
    ).toBeNull()
    await user.click(
      await screen.findByRole('switch', {
        name: zhCN.identifiers.axes.contactAria.replace(
          '{{value}}',
          'new@x.test',
        ),
      }),
    )

    await waitFor(() => expect(writes).toEqual([{ contact: true }]))
    const contacts = await screen.findByRole('region', {
      name: zhCN.identifiers.groups.contact,
    })
    await waitFor(() =>
      expect(within(contacts).getByText('new@x.test')).toBeTruthy(),
    )
  })

  it('puts a proved address into sign-in through its own switch', async () => {
    let signIn = false
    const writes: unknown[] = []
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            identifiers: [
              identifier({
                id: 'id-email',
                kind: 'email',
                value: 'ada@x.test',
                sign_in: signIn,
                contact: false,
              }),
            ],
          }),
        )
      }
      if (
        call.method === 'PUT' &&
        call.path === '/api/v1/auth/identifiers/id-email'
      ) {
        writes.push(call.body)
        signIn = (call.body as { sign_in?: boolean }).sign_in === true
        return new Response(null, { status: 204 })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('switch', {
        name: zhCN.identifiers.axes.signInAria.replace(
          '{{value}}',
          'ada@x.test',
        ),
      }),
    )

    await waitFor(() => expect(writes).toEqual([{ sign_in: true }]))
    const signInRegion = await screen.findByRole('region', {
      name: zhCN.identifiers.groups.signIn,
    })
    await waitFor(() =>
      expect(within(signInRegion).getByText('ada@x.test')).toBeTruthy(),
    )
  })

  it('makes a proved email the default contact through the choice on its row', async () => {
    let defaultContact = false
    const writes: unknown[] = []
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            identifiers: [
              identifier({
                id: 'id-email',
                kind: 'email',
                value: 'ada@x.test',
                sign_in: true,
                contact: false,
                default_contact: defaultContact,
              }),
            ],
          }),
        )
      }
      if (
        call.method === 'PUT' &&
        call.path === '/api/v1/auth/identifiers/id-email'
      ) {
        writes.push(call.body)
        defaultContact =
          (call.body as { default_contact?: boolean }).default_contact === true
        return new Response(null, { status: 204 })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('radio', {
        name: zhCN.identifiers.axes.defaultAria.replace(
          '{{value}}',
          'ada@x.test',
        ),
      }),
    )

    await waitFor(() => expect(writes).toEqual([{ default_contact: true }]))
    // The default contact is always a contact, so the row joins that list
    // while its sign-in axis stays as it was.
    const contacts = await screen.findByRole('region', {
      name: zhCN.identifiers.groups.contact,
    })
    await waitFor(() =>
      expect(within(contacts).getByText('ada@x.test')).toBeTruthy(),
    )
  })

  it('renders the last-login-way code when taking the only way out of sign-in is refused', async () => {
    let writes = 0
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            password_set: false,
            identifiers: [
              identifier({
                id: 'id-phone',
                kind: 'phone',
                value: '+14155550123',
                sign_in: true,
                contact: false,
              }),
            ],
          }),
        )
      }
      if (
        call.method === 'PUT' &&
        call.path === '/api/v1/auth/identifiers/id-phone'
      ) {
        writes += 1
        return errorResponse(409, 'authn.last_login_method')
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('switch', {
        name: zhCN.identifiers.axes.signInAria.replace(
          '{{value}}',
          '+14155550123',
        ),
      }),
    )

    await waitFor(() => expect(writes).toBe(1))
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.errors.authn.last_login_method)
  })
})
