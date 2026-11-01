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
 * otpauth_uri is rendered as a QR symbol, the code is answered under
 * `step: 'enroll'`, the ready step is taken with `step: 'complete'` and
 * the settled `enrolled` refetches the factors; a refused answer keeps the
 * typed code and reports the refusal.
 */

import { describe, expect, it, vi } from 'vitest'
import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { formatInstant } from '@speed/i18n'
import zhCN from './locales/zh-CN.json' with { type: 'json' }
import {
  errorResponse,
  jsonResponse,
  makeRealClientRig,
  type RealCall,
  type RealResponder,
} from '../test-utils/real-client.js'
import { profileBody } from '../test-utils/fixtures.js'
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
import { expectNoAxeViolations } from '../test-utils/axe.js'
import { MfaSection } from './MfaSection.js'

const PROFILE_PATH = '/api/v1/auth/me'
const FACTORS_PATH = '/api/v1/auth/factors'
const METHODS_PATH = '/api/v1/auth/methods'
const FLOW_START = '/api/v1/auth/flows/authenticated'
const FLOW_ANSWER = '/api/v1/auth/flows/answer'

const OTPAUTH_URI =
  'otpauth://totp/speed:ada?secret=JBSWY3DPEHPK3PXP&issuer=speed'

/** The GET /api/v1/auth/methods discovery document, carrying the deployment's
 * enrollable factor kinds. */
function methodsDoc(factors: readonly string[]): Record<string, unknown> {
  return {
    methods: [],
    signup: { enabled: false, password: { enabled: false } },
    mfa: { factors: [...factors] },
    human: { required: false, provider: 'off' },
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

function mount(
  respond: RealResponder,
  options: {
    readonly returnPath?: string
    readonly profile?: Record<string, unknown>
  } = {},
) {
  // Every section that formats a time reads the profile for the account's
  // stored zone; answer that read here unless the case wants a specific
  // profile, so a case scripts only the calls it is about.
  const rig = makeRealClientRig((call: RealCall) => {
    if (call.method === 'GET' && call.path === PROFILE_PATH) {
      return jsonResponse(200, options.profile ?? profileBody())
    }
    return respond(call)
  })
  rig.store.set('access-1')
  const rendered = renderWithProviders(
    <MfaSection session={rig.session} returnPath={options.returnPath} />,
  )
  return { rig, ...rendered }
}

/** The browser/OS summary the name form is prefilled with for a given user
 * agent (the same summary the session rows show). */
const SUGGESTED_NAME = 'Chrome · macOS'

/** Replaces the browser's user agent for one case; the returned function
 * restores whatever was there. */
function stubUserAgent(ua: string): () => void {
  const original = Object.getOwnPropertyDescriptor(
    window.navigator,
    'userAgent',
  )
  Object.defineProperty(window.navigator, 'userAgent', {
    value: ua,
    configurable: true,
  })
  return () => {
    if (original === undefined) {
      delete (window.navigator as { userAgent?: string }).userAgent
    } else {
      Object.defineProperty(window.navigator, 'userAgent', original)
    }
  }
}

/**
 * Open the enrollment name step, clear the prefilled name and confirm it, so
 * the flow starts with an empty label. Answers the typed name field.
 */
async function startEnrollWithEmptyName(
  user: ReturnType<typeof userEvent.setup>,
): Promise<HTMLElement> {
  const field = await screen.findByLabelText(zhCN.mfa.enroll.labelField)
  await user.clear(field)
  await user.click(
    screen.getByRole('button', { name: zhCN.mfa.enroll.confirmLabel }),
  )
  return field
}

/** The accessible name of a method's action while that method already has an
 * active factor: the button says "Manage <method>" instead of "Add <method>". */
const manageNameOf = (method: string): string =>
  zhCN.mfa.manageAria.replace('{{method}}', method)

/** The accessible name of a method's action while that method has no active
 * factor: the visible action word leads, followed by the method. */
const addNameOf = (method: string): string =>
  zhCN.mfa.addAria.replace('{{method}}', method)

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
    // The active state reads the same word on the method row and on the
    // factor row, so the one active factor puts that word on screen twice.
    expect(
      screen.getAllByText(zhCN.mfa.factor.status.active),
    ).toHaveLength(2)
    expect(screen.getByText(zhCN.mfa.factor.status.pending)).toBeTruthy()
    // Both enrollment entries are offered (the deployment reports both); the
    // one whose method already has an active factor reads as its manager.
    expect(
      await screen.findByRole('button', {
        name: manageNameOf(zhCN.mfa.factor.kind.totp),
      }),
    ).toBeTruthy()
    expect(
      screen.getByRole('button', { name: addNameOf(zhCN.mfa.factor.kind.webauthn) }),
    ).toBeTruthy()

    await expectNoAxeViolations()
  })

  it('offers another device for a method that already has an active factor', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(200, methodsDoc(['totp']))
      }
      if (call.method === 'GET' && call.path === FACTORS_PATH) {
        return jsonResponse(200, { factors: [factor({ status: 'active' })] })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)

    const button = await screen.findByRole('button', {
      name: manageNameOf(zhCN.mfa.factor.kind.totp),
    })
    // The action starts an enrollment even while the method has an active
    // factor, so its word is the add word rather than a management one.
    expect(button.textContent).toBe(zhCN.mfa.action.manage)
    expect(button.textContent).toContain(zhCN.mfa.action.add)
  })

  it('offers the plain add action for a method with no active factor', async () => {
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

    const button = await screen.findByRole('button', {
      name: addNameOf(zhCN.mfa.factor.kind.totp),
    })
    expect(button.textContent).toBe(zhCN.mfa.action.add)
  })

  it('names the method state and the factor row state with one word', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(200, methodsDoc(['totp']))
      }
      if (call.method === 'GET' && call.path === FACTORS_PATH) {
        return jsonResponse(200, { factors: [factor({ status: 'active' })] })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)

    // One active factor: the method row's state chip and the factor row's
    // status chip describe the same state, so they read the same word.
    expect(zhCN.mfa.state.on).toBe(zhCN.mfa.factor.status.active)
    expect(await screen.findAllByText(zhCN.mfa.state.on)).toHaveLength(2)
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
      await screen.findByRole('button', { name: addNameOf(zhCN.mfa.factor.kind.totp) }),
    ).toBeTruthy()
    expect(
      screen.queryByRole('button', { name: addNameOf(zhCN.mfa.factor.kind.webauthn) }),
    ).toBeNull()
  })

  it('says so in place when exactly one factor is enabled', async () => {
    // One enabled row, one pending: the pending row does not count as a
    // usable second method, so the section says the account has only one and
    // points at the enrollment entries below it.
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(200, methodsDoc(['totp', 'webauthn']))
      }
      if (call.method === 'GET' && call.path === FACTORS_PATH) {
        return jsonResponse(200, {
          factors: [
            factor({ id: 'factor-1', kind: 'totp', status: 'active' }),
            factor({ id: 'factor-2', kind: 'webauthn', status: 'pending' }),
          ],
        })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)

    expect(await screen.findByText(zhCN.mfa.singleFactor)).toBeTruthy()
    // The note points at entries that are actually on the page; the enabled
    // method's own entry reads as its manager.
    expect(
      screen.getByRole('button', {
        name: manageNameOf(zhCN.mfa.factor.kind.totp),
      }),
    ).toBeTruthy()
  })

  it('hides the single-factor note when two factors are enabled', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(200, methodsDoc(['totp', 'webauthn']))
      }
      if (call.method === 'GET' && call.path === FACTORS_PATH) {
        return jsonResponse(200, {
          factors: [
            factor({ id: 'factor-1', kind: 'totp', label: 'Phone app' }),
            factor({ id: 'factor-2', kind: 'webauthn', label: 'Laptop key' }),
          ],
        })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)

    // The list has settled before the note's absence is asserted.
    expect(await screen.findByText('Phone app')).toBeTruthy()
    expect(screen.getByText('Laptop key')).toBeTruthy()
    expect(screen.queryByText(zhCN.mfa.singleFactor)).toBeNull()
  })

  it('hides the single-factor note when no factor is enabled', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(200, methodsDoc(['totp']))
      }
      if (call.method === 'GET' && call.path === FACTORS_PATH) {
        return jsonResponse(200, {
          factors: [factor({ status: 'pending' })],
        })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)

    expect(await screen.findByText('My authenticator')).toBeTruthy()
    expect(screen.queryByText(zhCN.mfa.singleFactor)).toBeNull()
  })

  it('withholds the single-factor note when the deployment offers no enrollment entry', async () => {
    // The note points at registering another method. A deployment that
    // reports no enrollable kind offers nothing to point at, so the note is
    // withheld rather than naming a control that is not there.
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(200, methodsDoc([]))
      }
      if (call.method === 'GET' && call.path === FACTORS_PATH) {
        return jsonResponse(200, { factors: [factor({ status: 'active' })] })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)

    expect(await screen.findByText('My authenticator')).toBeTruthy()
    expect(screen.queryByText(zhCN.mfa.singleFactor)).toBeNull()
  })

  it('lists the reported methods before any factor is set up', async () => {
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

    // The method is on the page ready to be set up; no "nothing set up"
    // sentence competes with it.
    expect(await screen.findByRole('button', { name: addNameOf(zhCN.mfa.factor.kind.totp) })).toBeTruthy()
    expect(screen.getByText(zhCN.mfa.state.off)).toBeTruthy()
    expect(screen.queryByText(zhCN.mfa.empty)).toBeNull()
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
      expect(screen.getAllByText(zhCN.mfa.state.off)).toHaveLength(2),
    )
    expect(screen.queryByText(zhCN.mfa.state.on)).toBeNull()
    // The removal confirms itself, naming the factor row removed -- its own
    // label, not just its kind.
    expect(zhCN.mfa.removed).toContain('{{factor}}')
    expect(
      await screen.findByText(
        zhCN.mfa.removed.replace('{{factor}}', 'My authenticator'),
      ),
    ).toBeTruthy()
    expect(
      rig.calls.filter(
        (call) => call.method === 'DELETE' && call.path === '/api/v1/auth/factors/factor-1',
      ),
    ).toHaveLength(1)
  })

  it('holds the session through the credential rotation an active factor removal performs', async () => {
    // Removing an active factor rotates the requesting session's cookie:
    // the server answers the DELETE with Set-Cookie. A read issued under the
    // outgoing cookie can then answer 401 before the new cookie is in
    // effect, and the client's silent 401 leg -- here the session's own
    // restore(), the leg a production host binds -- would read that
    // anonymous answer as the end of the session.
    //
    // This suite reproduces the changeover: the refetch that follows the
    // removal is answered 401, standing for a read that left under the old
    // credential (an already-in-flight or background read; a read issued
    // after the response would carry the new cookie). The surface opens an
    // identity-transition window around the rotation, so the anonymous
    // answer belongs to the credential being replaced and is discarded: the
    // snapshot stays authenticated and no sign-out edge is observed. Without
    // the window the same script ends the session -- the regression this
    // case pins.
    const warn = captureRefreshWarning()
    let removed = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === SESSION_PATH) {
        return removed
          ? errorResponse(401, 'authn.login-failed')
          : jsonResponse(200, AUTHENTICATED_SESSION)
      }
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return removed
          ? errorResponse(401, 'authn.login-failed')
          : jsonResponse(200, profileBody())
      }
      if (call.method === 'GET' && call.path === FEATURES_PATH) {
        return jsonResponse(200, EMPTY_FEATURES)
      }
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(200, methodsDoc(['totp']))
      }
      if (call.method === 'GET' && call.path === FACTORS_PATH) {
        return removed
          ? errorResponse(401, 'authn.login-failed')
          : jsonResponse(200, { factors: [factor({ status: 'active' })] })
      }
      if (
        call.method === 'DELETE' &&
        call.path === '/api/v1/auth/factors/factor-1'
      ) {
        removed = true
        return new Response(null, { status: 204 })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = await makeRotationRig(respond)
    renderWithProviders(<MfaSection session={rig.session} />)
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

    // The stale-credential read settles through the silent leg: the factors
    // refetch 401s, the restore the client runs in its place 401s too, and
    // the client reports the failed refresh.
    await waitFor(() =>
      expect(warn).toHaveBeenCalledWith(
        'access token refresh failed',
        expect.objectContaining({ status: 401 }),
      ),
    )
    expect(
      rig.calls.filter(
        (call) => call.method === 'GET' && call.path === FACTORS_PATH,
      ),
    ).toHaveLength(2)
    expect(rig.transitionBegins()).toBe(1)
    // The anonymous answer speaks for the credential the removal replaced,
    // so it neither ends the session nor evicts the cache.
    expect(rig.session.getSnapshot().state).toBe('authenticated')
    expect(rig.endedEdges()).toBe(0)
  })

  it('lets a real anonymous answer end the session when a pending factor removal rotates nothing', async () => {
    // Removing a pending factor replaces no credential: the route settles
    // the sessions only for a factor that was active. The removal therefore
    // runs bare, without an identity-transition window, so a read that lands
    // anonymous after it is the session genuinely ending -- the opposite of
    // the active case above. Wrapping this removal would keep the stale
    // snapshot and postpone the sign-out to the next read.
    let removed = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === SESSION_PATH) {
        return removed
          ? errorResponse(401, 'authn.login-failed')
          : jsonResponse(200, AUTHENTICATED_SESSION)
      }
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return removed
          ? errorResponse(401, 'authn.login-failed')
          : jsonResponse(200, profileBody())
      }
      if (call.method === 'GET' && call.path === FEATURES_PATH) {
        return jsonResponse(200, EMPTY_FEATURES)
      }
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(200, methodsDoc(['totp']))
      }
      if (call.method === 'GET' && call.path === FACTORS_PATH) {
        return removed
          ? errorResponse(401, 'authn.login-failed')
          : jsonResponse(200, { factors: [factor({ status: 'pending' })] })
      }
      if (
        call.method === 'DELETE' &&
        call.path === '/api/v1/auth/factors/factor-1'
      ) {
        removed = true
        return new Response(null, { status: 204 })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = await makeRotationRig(respond)
    renderWithProviders(<MfaSection session={rig.session} />)
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

    // The refetch that follows the write is the first read issued after the
    // removal. No window stands between it and the silent leg, so its
    // anonymous answer ends the session.
    await waitFor(() =>
      expect(rig.session.getSnapshot().state).toBe('anonymous'),
    )
    expect(rig.transitionBegins()).toBe(0)
    expect(rig.endedEdges()).toBe(1)
  })

  it('releases the window a refused active-factor removal opened', async () => {
    // A refused write must not leave its window open: the next genuine
    // anonymous read has to end the session. The removal is refused 409 (the
    // factor is the account's last one); after that failure the session read
    // is answered 401, and the snapshot must turn anonymous with one
    // sign-out edge. A window still open would discard that answer and leave
    // the user signed in forever.
    let refused = false
    let gone = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === SESSION_PATH) {
        return gone
          ? errorResponse(401, 'authn.login-failed')
          : jsonResponse(200, AUTHENTICATED_SESSION)
      }
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileBody())
      }
      if (call.method === 'GET' && call.path === FEATURES_PATH) {
        return jsonResponse(200, EMPTY_FEATURES)
      }
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(200, methodsDoc(['totp']))
      }
      if (call.method === 'GET' && call.path === FACTORS_PATH) {
        return jsonResponse(200, { factors: [factor({ status: 'active' })] })
      }
      if (
        call.method === 'DELETE' &&
        call.path === '/api/v1/auth/factors/factor-1'
      ) {
        refused = true
        return errorResponse(409, 'authn.conflict')
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = await makeRotationRig(respond)
    renderWithProviders(<MfaSection session={rig.session} />)
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

    // The failure is on screen only after the write rejected and its window
    // was released.
    expect(refused).toBe(true)
    await screen.findByText(zhCN.errors.authn.conflict)
    expect(rig.transitionBegins()).toBe(1)

    // A fresh anonymous read now settles the true state.
    gone = true
    await rig.session.restore()
    expect(rig.session.getSnapshot().state).toBe('anonymous')
    expect(rig.endedEdges()).toBe(1)
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
    await waitFor(() =>
      expect(screen.getAllByText(zhCN.mfa.state.off).length).toBeGreaterThan(0),
    )
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

  it('opens a fresh window for the removal a won step-up re-runs', async () => {
    // The refused removal opens a window, releases it on the failure, and
    // the re-run is a new write: it opens its own window before the rotating
    // DELETE goes out. The refetch that follows the re-run is answered 401
    // -- a read under the credential the re-run replaced -- and is
    // discarded by that fresh window. The two begins count the shortfall's
    // window and the re-run's.
    const warn = captureRefreshWarning()
    let elevated = false
    let rotated = false
    // The shortfall's own silent leg answers anonymous: the server would
    // read the outgoing cookie that way. The open window discards it, so the
    // client does not retry the refused removal and the session stays
    // established for the challenge.
    let signedOut = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === SESSION_PATH) {
        return signedOut
          ? errorResponse(401, 'authn.login-failed')
          : jsonResponse(200, AUTHENTICATED_SESSION)
      }
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return signedOut
          ? errorResponse(401, 'authn.login-failed')
          : jsonResponse(200, profileBody())
      }
      if (call.method === 'GET' && call.path === FEATURES_PATH) {
        return jsonResponse(200, EMPTY_FEATURES)
      }
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(200, methodsDoc(['totp']))
      }
      if (call.method === 'GET' && call.path === FACTORS_PATH) {
        return rotated
          ? errorResponse(401, 'authn.login-failed')
          : jsonResponse(200, { factors: [factor({ status: 'active' })] })
      }
      if (
        call.method === 'DELETE' &&
        call.path === '/api/v1/auth/factors/factor-1'
      ) {
        if (!elevated) {
          signedOut = true
          return errorResponse(401, 'authn.insufficient_user_authentication')
        }
        rotated = true
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
          state: 'authenticated',
          session: { id: 'sess-1', expires_at: '2026-01-01T00:00:00Z' },
        })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = await makeRotationRig(respond)
    renderWithProviders(<MfaSection session={rig.session} />)
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

    // The re-run's refetch lands 401 and is discarded by the re-run's own
    // window: the snapshot stays authenticated and no sign-out is observed.
    await waitFor(() =>
      expect(warn).toHaveBeenCalledWith(
        'access token refresh failed',
        expect.objectContaining({ status: 401 }),
      ),
    )
    expect(
      rig.calls.filter(
        (call) =>
          call.method === 'DELETE' &&
          call.path === '/api/v1/auth/factors/factor-1',
      ),
    ).toHaveLength(2)
    expect(rig.transitionBegins()).toBe(2)
    expect(rig.session.getSnapshot().state).toBe('authenticated')
    expect(rig.endedEdges()).toBe(0)
  })

  it('sends the host’s path as the enrollment flow’s return_to', async () => {
    // An enrollment a redirect method finishes lands back on the page that
    // started it, so that page reads the refusal.
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
          next: { kind: 'webauthn', options: { otpauth_uri: OTPAUTH_URI } },
        })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { rig } = mount(respond, { returnPath: '/account' })
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('button', { name: addNameOf(zhCN.mfa.factor.kind.totp) }),
    )
    await startEnrollWithEmptyName(user)

    await waitFor(() => {
      const start = rig.calls.find(
        (call) => call.method === 'POST' && call.path === FLOW_START,
      )
      expect(start?.body).toMatchObject({
        purpose: 'enroll',
        return_to: '/account',
      })
    })
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
      await screen.findByRole('button', { name: addNameOf(zhCN.mfa.factor.kind.totp) }),
    )
    await startEnrollWithEmptyName(user)

    // The enroll start names the factor kind and the (cleared) label.
    const start = rig.calls.find(
      (call) => call.method === 'POST' && call.path === FLOW_START,
    )
    expect(start?.body).toEqual({
      purpose: 'enroll',
      input: { kind: 'totp', label: '' },
    })

    // The step's otpauth URI is its own scannable symbol: the step's JSON is
    // never rendered as a blob, and the URI never appears as text.
    expect(
      await screen.findByRole('img', { name: zhCN.flowStep.totpQrLabel }),
    ).toBeVisible()
    expect(screen.queryByText(/"otpauth_uri"/)).toBeNull()
    expect(screen.queryByText(/otpauth:\/\//)).toBeNull()
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
    // The completed enrollment confirms itself, naming the factor kind.
    expect(zhCN.mfa.enroll.enrolled).toContain('{{factor}}')
    expect(
      await screen.findByText(
        zhCN.mfa.enroll.enrolled.replace(
          '{{factor}}',
          zhCN.mfa.factor.kind.totp,
        ),
      ),
    ).toBeTruthy()
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

  it('keeps the typed code and reports the refusal when an enrollment answer is rejected', async () => {
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
          next: { kind: 'webauthn', options: { otpauth_uri: OTPAUTH_URI } },
        })
      }
      if (call.method === 'POST' && call.path === FLOW_ANSWER) {
        return errorResponse(400, 'authn.bad_request')
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { rig } = mount(respond)
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('button', { name: addNameOf(zhCN.mfa.factor.kind.totp) }),
    )
    const code = await screen.findByLabelText(zhCN.flowStep.codeLabel)
    await user.type(code, '123456')
    // Typing alone issues nothing: only the confirm control answers.
    expect(
      rig.calls.filter(
        (call) => call.method === 'POST' && call.path === FLOW_ANSWER,
      ),
    ).toHaveLength(0)
    await user.click(
      screen.getByRole('button', { name: zhCN.mfa.enroll.confirmLabel }),
    )

    expect(await screen.findByRole('alert')).toHaveTextContent(
      zhCN.errors.authn.bad_request,
    )
    expect(code).toHaveValue('123456')
    expect(
      rig.calls.filter(
        (call) => call.method === 'POST' && call.path === FLOW_ANSWER,
      ),
    ).toHaveLength(1)
  })

  it('prefills the enrollment name with this browser and sends it', async () => {
    const restore = stubUserAgent(
      'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36',
    )
    try {
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
            next: { kind: 'webauthn', options: { otpauth_uri: OTPAUTH_URI } },
          })
        }
        throw new Error(`unexpected ${call.method} ${call.path}`)
      }
      const { rig } = mount(respond)
      const user = userEvent.setup()

      await user.click(
        await screen.findByRole('button', {
          name: addNameOf(zhCN.mfa.factor.kind.totp),
        }),
      )
      // The name step carries the browser/OS summary the session rows show,
      // editable and sent with the flow's first input.
      const field = await screen.findByLabelText(zhCN.mfa.enroll.labelField)
      expect(field).toHaveValue(SUGGESTED_NAME)
      await expectNoAxeViolations()

      await user.click(
        screen.getByRole('button', { name: zhCN.mfa.enroll.confirmLabel }),
      )
      await waitFor(() => {
        const start = rig.calls.find(
          (call) => call.method === 'POST' && call.path === FLOW_START,
        )
        expect(start?.body).toEqual({
          purpose: 'enroll',
          input: { kind: 'totp', label: SUGGESTED_NAME },
        })
      })
    } finally {
      restore()
    }
  })

  it('submits an empty name when the reader clears the prefill', async () => {
    const restore = stubUserAgent(
      'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36',
    )
    try {
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
            next: { kind: 'webauthn', options: { otpauth_uri: OTPAUTH_URI } },
          })
        }
        throw new Error(`unexpected ${call.method} ${call.path}`)
      }
      const { rig } = mount(respond)
      const user = userEvent.setup()

      await user.click(
        await screen.findByRole('button', {
          name: addNameOf(zhCN.mfa.factor.kind.totp),
        }),
      )
      // Clearing the prefill is not a client-side validation failure: the
      // empty value is sent, and the server stores no name.
      const field = await startEnrollWithEmptyName(user)
      expect(field).toHaveValue('')
      await waitFor(() => {
        const start = rig.calls.find(
          (call) => call.method === 'POST' && call.path === FLOW_START,
        )
        expect(start?.body).toEqual({
          purpose: 'enroll',
          input: { kind: 'totp', label: '' },
        })
      })
    } finally {
      restore()
    }
  })

  it('shows each row’s name, creation time and last use in the account time zone', async () => {
    const timeZone = 'Asia/Shanghai'
    const created = formatInstant('2026-01-01T00:00:00Z', 'zh-CN', {
      timeZone,
    })
    const lastUsed = formatInstant('2026-02-01T00:00:00Z', 'zh-CN', {
      timeZone,
    })
    const laterCreated = formatInstant('2026-03-01T00:00:00Z', 'zh-CN', {
      timeZone,
    })
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(200, methodsDoc(['totp']))
      }
      if (call.method === 'GET' && call.path === FACTORS_PATH) {
        return jsonResponse(200, {
          factors: [
            factor({
              id: 'factor-1',
              kind: 'totp',
              label: 'Phone app',
              created_at: '2026-01-01T00:00:00Z',
              last_used_at: '2026-02-01T00:00:00Z',
            }),
            factor({
              id: 'factor-2',
              kind: 'totp',
              label: '',
              created_at: '2026-03-01T00:00:00Z',
            }),
          ],
        })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond, { profile: profileBody({ time_zone: timeZone }) })

    expect(await screen.findByText('Phone app')).toBeTruthy()
    expect(
      screen.getByText(zhCN.mfa.factor.createdAt.replace('{{time}}', created)),
    ).toBeTruthy()
    expect(
      screen.getByText(
        zhCN.mfa.factor.lastUsedAt.replace('{{time}}', lastUsed),
      ),
    ).toBeTruthy()
    expect(
      screen.getByText(
        zhCN.mfa.factor.createdAt.replace('{{time}}', laterCreated),
      ),
    ).toBeTruthy()
    // The unnamed row reads as its kind; the method heading is the only
    // other place that text appears.
    expect(screen.getAllByText(zhCN.mfa.factor.kind.totp)).toHaveLength(2)
    // A factor that has never passed a verification says so.
    expect(screen.getAllByText(zhCN.mfa.factor.neverUsed)).toHaveLength(1)
  })

  it('renames a factor through PATCH and refetches the list', async () => {
    let renamed = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(200, methodsDoc(['totp']))
      }
      if (call.method === 'GET' && call.path === FACTORS_PATH) {
        return jsonResponse(200, {
          factors: [
            factor({
              id: 'factor-1',
              label: renamed ? 'New name' : 'Old name',
            }),
          ],
        })
      }
      if (
        call.method === 'PATCH' &&
        call.path === '/api/v1/auth/factors/factor-1'
      ) {
        renamed = true
        return jsonResponse(200, factor({ id: 'factor-1', label: 'New name' }))
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { rig } = mount(respond)
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('button', {
        name: zhCN.mfa.renameAria.replace('{{factor}}', 'Old name'),
      }),
    )
    const field = await screen.findByLabelText(zhCN.mfa.renameLabel)
    await expectNoAxeViolations()
    await user.clear(field)
    await user.type(field, 'New name')
    await user.click(screen.getByRole('button', { name: zhCN.mfa.renameSave }))

    await waitFor(() => {
      const patch = rig.calls.find(
        (call) =>
          call.method === 'PATCH' &&
          call.path === '/api/v1/auth/factors/factor-1',
      )
      expect(patch?.body).toEqual({ label: 'New name' })
    })
    expect(
      await screen.findByText(
        zhCN.mfa.renamed.replace('{{factor}}', 'New name'),
      ),
    ).toBeTruthy()
    // The list converged on the server's answer.
    expect(await screen.findByText('New name')).toBeTruthy()
    expect(screen.queryByText('Old name')).toBeNull()
  })

  it('renders a refused rename as its error code and keeps the editor open', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(200, methodsDoc(['totp']))
      }
      if (call.method === 'GET' && call.path === FACTORS_PATH) {
        return jsonResponse(200, {
          factors: [factor({ id: 'factor-1', label: 'Old name' })],
        })
      }
      if (
        call.method === 'PATCH' &&
        call.path === '/api/v1/auth/factors/factor-1'
      ) {
        return errorResponse(400, 'authn.bad_request')
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('button', {
        name: zhCN.mfa.renameAria.replace('{{factor}}', 'Old name'),
      }),
    )
    const field = await screen.findByLabelText(zhCN.mfa.renameLabel)
    await user.clear(field)
    // Over the label's 64-character domain: the server refuses it.
    await user.type(field, 'x'.repeat(65))
    await user.click(screen.getByRole('button', { name: zhCN.mfa.renameSave }))

    expect(
      await screen.findByText(zhCN.errors.authn.bad_request),
    ).toBeTruthy()
    // The editor stays open so the value can be corrected.
    expect(screen.getByLabelText(zhCN.mfa.renameLabel)).toBeVisible()
  })

  it('names the removed row when two factors share a kind', async () => {
    // Two authenticator-app factors with different labels: the confirmation
    // must say which row went, not just its kind.
    let removed = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(200, methodsDoc(['totp']))
      }
      if (call.method === 'GET' && call.path === FACTORS_PATH) {
        return jsonResponse(200, {
          factors: removed
            ? [factor({ id: 'factor-1', kind: 'totp', label: 'Phone app' })]
            : [
                factor({ id: 'factor-1', kind: 'totp', label: 'Phone app' }),
                factor({ id: 'factor-2', kind: 'totp', label: 'Tablet app' }),
              ],
        })
      }
      if (call.method === 'DELETE' && call.path === '/api/v1/auth/factors/factor-2') {
        removed = true
        return new Response(null, { status: 204 })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()

    const removeButtons = await screen.findAllByRole('button', {
      name: zhCN.mfa.removeAria.replace(
        '{{factor}}',
        zhCN.mfa.factor.kind.totp,
      ),
    })
    expect(removeButtons).toHaveLength(2)
    await user.click(removeButtons[1] as HTMLElement)
    const dialog = await screen.findByRole('dialog')
    await user.click(
      within(dialog).getByRole('button', {
        name: zhCN.mfa.removeConfirm.confirmLabel,
      }),
    )

    expect(
      await screen.findByText(
        zhCN.mfa.removed.replace('{{factor}}', 'Tablet app'),
      ),
    ).toBeTruthy()
    // The surviving same-kind row and the bare kind are not what it names.
    expect(
      screen.queryByText(
        zhCN.mfa.removed.replace('{{factor}}', zhCN.mfa.factor.kind.totp),
      ),
    ).toBeNull()
    expect(
      screen.queryByText(zhCN.mfa.removed.replace('{{factor}}', 'Phone app')),
    ).toBeNull()
  })

  it('keeps the enrollment confirmation when a removal settles later', async () => {
    // Enrollment and removal are two control groups with their own scopes:
    // settling the removal must not clear the enrollment's confirmation.
    let enrolled = false
    let removed = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(200, methodsDoc(['totp']))
      }
      if (call.method === 'GET' && call.path === FACTORS_PATH) {
        if (removed) {
          return jsonResponse(200, {
            factors: [
              factor({ id: 'factor-new', kind: 'totp', label: 'My authenticator' }),
            ],
          })
        }
        return jsonResponse(200, {
          factors: enrolled
            ? [
                factor({ id: 'factor-old', kind: 'totp', label: 'Old app' }),
                factor({
                  id: 'factor-new',
                  kind: 'totp',
                  label: 'My authenticator',
                }),
              ]
            : [factor({ id: 'factor-old', kind: 'totp', label: 'Old app' })],
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
      if (call.method === 'DELETE' && call.path === '/api/v1/auth/factors/factor-old') {
        removed = true
        return new Response(null, { status: 204 })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('button', {
        name: manageNameOf(zhCN.mfa.factor.kind.totp),
      }),
    )
    await startEnrollWithEmptyName(user)
    await user.type(
      await screen.findByLabelText(zhCN.flowStep.codeLabel),
      '123456',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.mfa.enroll.confirmLabel }),
    )
    await user.click(
      await screen.findByRole('button', { name: zhCN.mfa.enroll.confirmLabel }),
    )

    const enrollText = zhCN.mfa.enroll.enrolled.replace(
      '{{factor}}',
      zhCN.mfa.factor.kind.totp,
    )
    expect(await screen.findByText(enrollText)).toBeTruthy()

    // Remove the pre-existing row through its own scope.
    const removeButtons = await screen.findAllByRole('button', {
      name: zhCN.mfa.removeAria.replace(
        '{{factor}}',
        zhCN.mfa.factor.kind.totp,
      ),
    })
    await user.click(removeButtons[0] as HTMLElement)
    const dialog = await screen.findByRole('dialog')
    await user.click(
      within(dialog).getByRole('button', {
        name: zhCN.mfa.removeConfirm.confirmLabel,
      }),
    )

    const removeText = zhCN.mfa.removed.replace('{{factor}}', 'Old app')
    expect(await screen.findByText(removeText)).toBeTruthy()
    // Both outcomes stand, each in its own control group's region.
    const enrollRegion = screen.getByText(enrollText).closest('[role="status"]')
    const removeRegion = screen.getByText(removeText).closest('[role="status"]')
    expect(enrollRegion).not.toBeNull()
    expect(removeRegion).not.toBeNull()
    expect(enrollRegion).not.toBe(removeRegion)
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
      await screen.findByRole('button', { name: addNameOf(zhCN.mfa.factor.kind.totp) }),
    )
    await startEnrollWithEmptyName(user)

    expect(await screen.findByLabelText(zhCN.flowStep.codeLabel)).toBeVisible()
    expect(
      screen.queryByRole('img', { name: zhCN.flowStep.totpQrLabel }),
    ).toBeNull()
    expect(
      screen.queryByRole('button', { name: zhCN.flowStep.totpManualToggle }),
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
        await screen.findByRole('button', { name: addNameOf(zhCN.mfa.factor.kind.webauthn) }),
      )
      await startEnrollWithEmptyName(user)
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
      await screen.findByRole('button', { name: addNameOf(zhCN.mfa.factor.kind.sms) }),
    ).toBeTruthy()
    const unknown = screen.getByRole('button', { name: addNameOf(zhCN.mfa.factor.kind.other) })
    expect(unknown).toBeTruthy()

    await user.click(unknown)
    await startEnrollWithEmptyName(user)
    // The unknown kind's own flow starts; the generic label never hides
    // the deployment's vocabulary from the request.
    const start = rig.calls.find(
      (call) => call.method === 'POST' && call.path === FLOW_START,
    )
    expect(start?.body).toEqual({
      purpose: 'enroll',
      input: { kind: 'magic', label: '' },
    })
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

  it('keeps the factor rows and marks them stale when a refresh fails', async () => {
    const user = userEvent.setup()
    let reads = 0
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(200, methodsDoc(['totp']))
      }
      if (call.method === 'GET' && call.path === FACTORS_PATH) {
        reads += 1
        return reads === 1
          ? jsonResponse(200, { factors: [factor({})] })
          : errorResponse(500, 'authn.unavailable')
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { queryClient } = mount(respond)

    expect(await screen.findByText('My authenticator')).toBeTruthy()

    await act(async () => {
      await queryClient.invalidateQueries()
    })

    // The failed refresh keeps the settled rows on screen under the stale
    // notice rather than replacing them with the error placeholder.
    expect(screen.getByText('My authenticator')).toBeTruthy()
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.stale.description)
    expect(screen.queryByText(zhCN.mfa.error.title)).toBeNull()

    const readsBeforeRetry = reads
    await user.click(screen.getByRole('button', { name: zhCN.mfa.retry }))
    await waitFor(() => expect(reads).toBeGreaterThan(readsBeforeRetry))
  })
})
