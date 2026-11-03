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
 * and the TOTP enrollment starts its flow with the panel -- the
 * `webauthn` step's otpauth_uri is rendered as a QR symbol, the code is
 * answered under `step: 'enroll'`, a following `complete` step is taken
 * automatically so the reader confirms once, and the settled `enrolled`
 * refetches the factors and names the new row with the typed name; a
 * refused answer keeps the typed code and reports the refusal in the panel.
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
import { summarizeUserAgent } from './internal/ua-summary.js'
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
const FLOW_CURRENT = '/api/v1/auth/flows/current'

const OTPAUTH_URI =
  'otpauth://totp/speed:ada?secret=JBSWY3DPEHPK3PXP&issuer=speed'

/** The GET /api/v1/auth/methods discovery document, carrying the deployment's
 * enrollable factor kinds. */
function methodsDoc(factors: readonly string[]): Record<string, unknown> {
  return {
    methods: [],
    signup: { enabled: false, password: { enabled: false } },
    mfa: { factors: [...factors] },
    human: {
      phone: { required: false, provider: 'off' },
      other: { required: false, provider: 'off' },
    },
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

/** One passkey row: the kind whose entries may be several, and the only one
 * whose rows carry a reader-chosen name. */
function passkey(overrides: Record<string, unknown>): Record<string, unknown> {
  return factor({ kind: 'webauthn', ...overrides })
}

/** The accessible name of the authenticator group's replacement action. */
const replaceName = (): string => zhCN.mfa.replaceAria

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

/** The accessible name of a method's action: the visible action word leads,
 * followed by the method. */
const addNameOf = (method: string): string =>
  zhCN.mfa.addAria.replace('{{method}}', method)

/**
 * Open one method's enrollment panel. The flow starts with the panel, so the
 * method's body arrives without a separate name step.
 */
async function openEnrollment(
  user: ReturnType<typeof userEvent.setup>,
  method: string,
): Promise<void> {
  await user.click(
    await screen.findByRole('button', { name: addNameOf(method) }),
  )
}

/** Submit the panel's one confirmation. */
async function confirmEnrollment(
  user: ReturnType<typeof userEvent.setup>,
): Promise<void> {
  await user.click(
    await screen.findByRole('button', { name: zhCN.mfa.enroll.confirmLabel }),
  )
}

/** The enrollment panel's heading for one factor kind: the parameterized
 * title with the kind's own name in its method slot. */
const enrollTitleOf = (method: string): string =>
  zhCN.mfa.enroll.title.replace('{{method}}', method)

/** The DELETE requests a case sent to abandon the open flow. */
const abandonCalls = (calls: readonly RealCall[]): RealCall[] =>
  calls.filter(
    (call) => call.method === 'DELETE' && call.path === FLOW_CURRENT,
  )
/** The factor row that reads as `name`: a passkey or phone by its label, the
 * authenticator entry by its kind. */
function factorRow(name: string): HTMLElement {
  const row = screen
    .getAllByRole('listitem')
    .find((item) => within(item).queryByText(name, { exact: true }) !== null)
  if (row === undefined) {
    throw new Error(`no factor row reads as ${name}`)
  }
  return row
}

/** The action buttons one factor row renders, in display order. */
function rowActions(name: string): HTMLElement[] {
  return within(factorRow(name)).queryAllByRole('button')
}

/** The row's action button named `action`, or null. */
function rowAction(name: string, action: string): HTMLElement | null {
  return within(factorRow(name)).queryByRole('button', { name: action })
}

/** Clicks one of a factor row's inline action buttons. */
async function clickRowAction(
  user: ReturnType<typeof userEvent.setup>,
  name: string,
  action: string,
): Promise<void> {
  // The rows arrive with the factors read; wait for one before choosing its
  // action.
  await waitFor(() => expect(factorRow(name)).toBeTruthy())
  await user.click(within(factorRow(name)).getByRole('button', { name: action }))
}

/** Opens the danger confirm for one row from its inline remove action. */
async function openRemoveConfirm(
  user: ReturnType<typeof userEvent.setup>,
  factor: string,
): Promise<HTMLElement> {
  await clickRowAction(user, factor, zhCN.mfa.remove)
  return screen.findByRole('dialog')
}

/** Opens one row's inline rename editor from its inline rename action. */
async function openRenameEditor(
  user: ReturnType<typeof userEvent.setup>,
  factor: string,
): Promise<HTMLElement> {
  await clickRowAction(user, factor, zhCN.mfa.rename)
  return screen.findByLabelText(zhCN.mfa.renameLabel)
}

describe('MfaSection', () => {
  it('renders the factors grouped by kind and passes axe', async () => {
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
    // The single authenticator entry reads as its kind, whatever name the
    // list carries; the group heading carries the same text.
    expect(
      (await screen.findAllByText(zhCN.mfa.factor.kind.totp)).length,
    ).toBeGreaterThan(0)
    // Every assembled kind is its own group, named by the kind.
    expect(
      screen.getByRole('region', { name: zhCN.mfa.factor.kind.totp }),
    ).toBeTruthy()
    expect(
      screen.getByRole('region', { name: zhCN.mfa.factor.kind.webauthn }),
    ).toBeTruthy()
    // The pending passkey is the enrollment's intermediate state: it is not
    // rendered as a row, and no row carries a status chip.
    expect(screen.queryByText(zhCN.mfa.factor.status.pending)).toBeNull()
    expect(screen.queryByText(zhCN.mfa.factor.status.active)).toBeNull()
    // Both groups are enrollable. The authenticator group already holds its
    // one entry, so its action offers a replacement; the passkey group holds
    // none, so its action is a plain add.
    expect(
      await screen.findByRole('button', { name: replaceName() }),
    ).toBeTruthy()
    expect(
      screen.getByRole('button', { name: addNameOf(zhCN.mfa.factor.kind.webauthn) }),
    ).toBeTruthy()

    await expectNoAxeViolations()
  })

  it('keeps the groups in strength order when only the authenticator app is enabled', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(200, methodsDoc(['totp', 'webauthn']))
      }
      if (call.method === 'GET' && call.path === FACTORS_PATH) {
        return jsonResponse(200, {
          factors: [factor({ id: 'factor-totp', kind: 'totp', status: 'active' })],
        })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)

    // The passkey group has no enabled row and the authenticator app does;
    // the position of a group is its strength, not its enabled state, so the
    // reader does not see a group move after enabling it.
    const passkeyGroup = await screen.findByRole('region', {
      name: zhCN.mfa.factor.kind.webauthn,
    })
    const appGroup = screen.getByRole('region', {
      name: zhCN.mfa.factor.kind.totp,
    })
    expect(
      passkeyGroup.compareDocumentPosition(appGroup) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).not.toBe(0)
  })

  it('words the authenticator action as a replacement once an entry is active', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(200, methodsDoc(['totp', 'webauthn']))
      }
      if (call.method === 'GET' && call.path === FACTORS_PATH) {
        return jsonResponse(200, {
          factors: [
            factor({ id: 'factor-totp', kind: 'totp', status: 'active' }),
            factor({ id: 'factor-passkey', kind: 'webauthn', status: 'active' }),
          ],
        })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)

    // The authenticator entry is single: with one active, its action offers
    // to replace it. The passkey group may hold several, so its action is
    // still a plain add.
    const replace = await screen.findByRole('button', {
      name: replaceName(),
    })
    expect(replace.textContent).toBe(zhCN.mfa.action.replace)
    const add = screen.getByRole('button', {
      name: addNameOf(zhCN.mfa.factor.kind.webauthn),
    })
    expect(add.textContent).toBe(zhCN.mfa.action.add)
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

  it('offers a replacement for the single authenticator entry', async () => {
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
      name: replaceName(),
    })
    expect(button.textContent).toBe(zhCN.mfa.action.replace)
    // The passkey's plain add is not on this deployment.
    expect(
      screen.queryByRole('button', {
        name: addNameOf(zhCN.mfa.factor.kind.webauthn),
      }),
    ).toBeNull()
  })

  it('does not render a pending factor as a row', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(200, methodsDoc(['webauthn']))
      }
      if (call.method === 'GET' && call.path === FACTORS_PATH) {
        return jsonResponse(200, {
          factors: [
            passkey({ id: 'factor-1', status: 'active', label: 'Kept row' }),
            passkey({ id: 'factor-2', status: 'pending', label: 'Pending row' }),
          ],
        })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)

    expect(await screen.findByText('Kept row')).toBeTruthy()
    expect(screen.queryByText('Pending row')).toBeNull()
    expect(screen.queryByText(zhCN.mfa.factor.status.pending)).toBeNull()
  })

  it('keeps a disabled row with a muted marker and no status chip', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(200, methodsDoc(['webauthn']))
      }
      if (call.method === 'GET' && call.path === FACTORS_PATH) {
        return jsonResponse(200, {
          factors: [
            passkey({ id: 'factor-1', status: 'disabled', label: 'Old app' }),
          ],
        })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)

    expect(await screen.findByText('Old app')).toBeTruthy()
    // The disabled state is a muted word beside the name, not a chip.
    expect(screen.getByText(zhCN.mfa.factor.status.disabled)).toBeTruthy()
    expect(screen.queryByText(zhCN.mfa.factor.status.active)).toBeNull()
    expect(screen.queryByText(zhCN.mfa.factor.status.disabled, { selector: '.MuiChip-label' })).toBeNull()
  })

  it('renders rename and remove inline on an enabled row, and only remove on a disabled row', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(200, methodsDoc(['webauthn']))
      }
      if (call.method === 'GET' && call.path === FACTORS_PATH) {
        return jsonResponse(200, {
          factors: [
            passkey({ id: 'factor-1', status: 'active', label: 'Active row' }),
            passkey({
              id: 'factor-2',
              status: 'disabled',
              label: 'Disabled row',
            }),
          ],
        })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)

    await screen.findAllByText('Active row')
    // An enabled row renders rename then removal inline, in one action row.
    const active = rowActions('Active row')
    expect(active.map((button) => button.textContent?.trim())).toEqual([
      zhCN.mfa.rename,
      zhCN.mfa.remove,
    ])
    expect(
      active.every((button) => button.closest('[data-action-row]') !== null),
    ).toBe(true)
    expect(screen.queryByRole('menuitem')).toBeNull()

    // A disabled row offers removal alone: one plain button, no trigger.
    const disabled = rowActions('Disabled row')
    expect(disabled.map((button) => button.textContent?.trim())).toEqual([
      zhCN.mfa.remove,
    ])
    expect(disabled[0]?.closest('[data-action-row]')).toBeNull()
    expect(
      disabled.some((button) => button.hasAttribute('aria-haspopup')),
    ).toBe(false)
    expect(screen.queryByRole('menuitem')).toBeNull()
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
    // The note points at an add action that is actually on the page: the
    // passkey group holds nothing yet, so its action is the plain add.
    expect(
      screen.getByRole('button', {
        name: addNameOf(zhCN.mfa.factor.kind.webauthn),
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
            passkey({ id: 'factor-2', label: 'Laptop key' }),
          ],
        })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)

    // The list has settled before the note's absence is asserted. The
    // authenticator entry reads as its kind; the passkey keeps its name.
    await screen.findAllByText(zhCN.mfa.factor.kind.totp)
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

    expect(
      await screen.findByRole('button', {
        name: addNameOf(zhCN.mfa.factor.kind.totp),
      }),
    ).toBeTruthy()
    // The only row is pending, so it is not rendered and counts for
    // nothing; the note speaks for enabled rows alone.
    expect(screen.queryByText('My authenticator')).toBeNull()
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
        return jsonResponse(200, {
          factors: [passkey({ status: 'active' })],
        })
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
    expect(
      screen.getByRole('region', { name: zhCN.mfa.factor.kind.totp }),
    ).toBeTruthy()
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
          factors: removed ? [] : [passkey({})],
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

    const dialog = await openRemoveConfirm(user, 'My authenticator')
    await user.click(
      within(dialog).getByRole('button', {
        name: zhCN.mfa.removeConfirm.confirmLabel,
      }),
    )

    // The removal confirms itself, naming the factor row removed -- its own
    // label, not just its kind -- and says the account is left with a single
    // verification method, because this was the only one.
    expect(zhCN.mfa.removedLast).toContain('{{factor}}')
    expect(
      await screen.findByText(
        zhCN.mfa.removedLast.replace('{{factor}}', 'My authenticator'),
      ),
    ).toBeTruthy()
    expect(
      rig.calls.filter(
        (call) => call.method === 'DELETE' && call.path === '/api/v1/auth/factors/factor-1',
      ),
    ).toHaveLength(1)
  })

  it('treats a 204 removal of a row already gone as a landed write', async () => {
    // A row another device removed while this list still showed it answers
    // the same 204 a landed removal does: the route is idempotent and has no
    // not-found answer on this path. The surface rereads the list once and
    // confirms; it renders no error.
    let factorsRead = 0
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(200, methodsDoc(['totp']))
      }
      if (call.method === 'GET' && call.path === FACTORS_PATH) {
        factorsRead += 1
        return jsonResponse(200, {
          factors: factorsRead === 1 ? [passkey({})] : [],
        })
      }
      if (
        call.method === 'DELETE' &&
        call.path === '/api/v1/auth/factors/factor-1'
      ) {
        return new Response(null, { status: 204 })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()

    const dialog = await openRemoveConfirm(user, 'My authenticator')
    await user.click(
      within(dialog).getByRole('button', {
        name: zhCN.mfa.removeConfirm.confirmLabel,
      }),
    )

    // The removal confirms itself as a landed write, and the list converges
    // on the reread.
    expect(
      await screen.findByText(
        zhCN.mfa.removedLast.replace('{{factor}}', 'My authenticator'),
      ),
    ).toBeTruthy()
    expect(screen.queryByText(zhCN.errors.authn.factor_not_found)).toBeNull()
    // One reread after the initial read, and no loop from the answer.
    await waitFor(() => expect(factorsRead).toBe(2))
    expect(factorsRead).toBe(2)
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
          : jsonResponse(200, { factors: [passkey({ status: 'active' })] })
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
    const dialog = await openRemoveConfirm(user, 'My authenticator')
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

  it('lets a real anonymous answer end the session when a disabled factor removal rotates nothing', async () => {
    // Removing a disabled factor replaces no credential: the route settles
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
          : jsonResponse(200, { factors: [passkey({ status: 'disabled' })] })
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
    const dialog = await openRemoveConfirm(user, 'My authenticator')
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
        return jsonResponse(200, { factors: [passkey({ status: 'active' })] })
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
    const dialog = await openRemoveConfirm(user, 'My authenticator')
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
        return jsonResponse(200, { factors: [passkey({})] })
      }
      if (call.method === 'DELETE' && call.path === '/api/v1/auth/factors/factor-1') {
        return errorResponse(409, 'authn.conflict')
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()

    const dialog = await openRemoveConfirm(user, 'My authenticator')
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
          factors: removed ? [] : [passkey({})],
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

    const confirm = await openRemoveConfirm(user, 'My authenticator')
    await user.click(
      within(confirm).getByRole('button', {
        name: zhCN.mfa.removeConfirm.confirmLabel,
      }),
    )

    // The 401 opens the step-up dialog, which states the consequence of the
    // removal: this was the account's only verification method.
    await screen.findByRole('button', { name: zhCN.mfa.factor.kind.totp })
    expect(screen.getByText(zhCN.mfa.stepUpDeleteLastNote)).toBeTruthy()
    // The flow is started with the factor being removed excluded: it must
    // never be asked to prove its own removal.
    const start = rig.calls.find(
      (call) => call.method === 'POST' && call.path === FLOW_START,
    )
    expect(start?.body).toMatchObject({
      purpose: 'step-up',
      input: { exclude_factor: 'factor-1' },
    })
    await user.click(screen.getByRole('button', { name: zhCN.mfa.factor.kind.totp }))
    await user.type(await screen.findByLabelText(zhCN.flowStep.codeLabel), '123456')
    await user.click(
      screen.getByRole('button', { name: zhCN.mfa.stepUp.confirmLabel }),
    )

    // The won step-up re-runs the removal exactly once, which now lands.
    await waitFor(() =>
      expect(
        screen.getByText(
          zhCN.mfa.removedLast.replace('{{factor}}', 'My authenticator'),
        ),
      ).toBeTruthy(),
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
          : jsonResponse(200, { factors: [passkey({ status: 'active' })] })
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

    const confirm = await openRemoveConfirm(user, 'My authenticator')
    await user.click(
      within(confirm).getByRole('button', {
        name: zhCN.mfa.removeConfirm.confirmLabel,
      }),
    )

    // The 401 opens the step-up dialog.
    await screen.findByRole('button', { name: zhCN.mfa.factor.kind.totp })
    await user.click(screen.getByRole('button', { name: zhCN.mfa.factor.kind.totp }))
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

    await openEnrollment(user, zhCN.mfa.factor.kind.totp)

    await waitFor(() => {
      const start = rig.calls.find(
        (call) => call.method === 'POST' && call.path === FLOW_START,
      )
      expect(start?.body).toMatchObject({
        purpose: 'enroll',
        input: { kind: 'totp', label: '' },
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

    await openEnrollment(user, zhCN.mfa.factor.kind.totp)
    // The panel's heading names the factor kind it is enrolling.
    expect(
      screen.getByRole('heading', {
        name: enrollTitleOf(zhCN.mfa.factor.kind.totp),
      }),
    ).toBeTruthy()

    // The flow starts with the panel, carrying the kind and an empty label.
    await waitFor(() => {
      const start = rig.calls.find(
        (call) => call.method === 'POST' && call.path === FLOW_START,
      )
      expect(start?.body).toEqual({
        purpose: 'enroll',
        input: { kind: 'totp', label: '' },
      })
    })

    // The panel states what confirming will do before its controls. The
    // single authenticator entry takes no name, so no name field renders.
    expect(screen.getByText(zhCN.mfa.enroll.note.totp)).toBeVisible()
    expect(
      screen.queryByLabelText(zhCN.mfa.enroll.labelField),
    ).toBeNull()

    // The step's otpauth URI is its own scannable symbol: the step's JSON is
    // never rendered as a blob, and the URI never appears as text.
    expect(
      await screen.findByRole('img', { name: zhCN.flowStep.totpQrLabel }),
    ).toBeVisible()
    expect(screen.queryByText(/"otpauth_uri"/)).toBeNull()
    expect(screen.queryByText(/otpauth:\/\//)).toBeNull()
    // The heading keeps naming the kind once the flow's own step renders.
    expect(
      screen.getByRole('heading', {
        name: enrollTitleOf(zhCN.mfa.factor.kind.totp),
      }),
    ).toBeTruthy()
    await user.type(
      screen.getByLabelText(zhCN.flowStep.codeLabel),
      '123456',
    )
    // One confirmation takes the whole flow: the `complete` step that
    // follows the code is submitted without another click.
    await confirmEnrollment(user)

    // The row the enrollment added reads as its kind: it is the account's
    // single authenticator entry.
    await screen.findAllByText(zhCN.mfa.factor.kind.totp)
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
    // The panel closed and the group's action now offers a replacement,
    // because the account holds its one entry.
    expect(screen.queryByLabelText(zhCN.flowStep.codeLabel)).toBeNull()
    expect(
      screen.getByRole('button', { name: replaceName() }),
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

    await openEnrollment(user, zhCN.mfa.factor.kind.totp)
    const code = await screen.findByLabelText(zhCN.flowStep.codeLabel)
    await user.type(code, '123456')
    // Typing alone issues nothing: only the confirm control answers.
    expect(
      rig.calls.filter(
        (call) => call.method === 'POST' && call.path === FLOW_ANSWER,
      ),
    ).toHaveLength(0)
    await confirmEnrollment(user)

    // The refusal renders in the panel that asked for the code, and the
    // panel stays open with the typed value.
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

  it('suggests the browser summary and names the added row with the typed name', async () => {
    // The panel opens with the flow already started, so the name travels to
    // the row the enrollment adds rather than to the flow's first input.
    const restore = stubUserAgent(
      'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36',
    )
    try {
      let enrolled = false
      let renamed = false
      const respond: RealResponder = (call: RealCall) => {
        if (call.method === 'GET' && call.path === METHODS_PATH) {
          return jsonResponse(200, methodsDoc(['webauthn']))
        }
        if (call.method === 'GET' && call.path === FACTORS_PATH) {
          return jsonResponse(200, {
            factors: enrolled
              ? [
                  passkey({
                    id: 'factor-new',
                    label: renamed ? 'My phone' : '',
                  }),
                ]
              : [],
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
        if (
          call.method === 'PATCH' &&
          call.path === '/api/v1/auth/factors/factor-new'
        ) {
          renamed = true
          return jsonResponse(
            200,
            passkey({ id: 'factor-new', label: 'My phone' }),
          )
        }
        throw new Error(`unexpected ${call.method} ${call.path}`)
      }
      const { rig } = mount(respond)
      const user = userEvent.setup()

      await openEnrollment(user, zhCN.mfa.factor.kind.webauthn)
      await screen.findByRole('img', { name: zhCN.flowStep.totpQrLabel })
      // The name field suggests the browser/OS summary the session rows
      // show; the neutral example stays as the placeholder a cleared field
      // falls back to.
      const field = screen.getByLabelText(zhCN.mfa.enroll.labelField)
      const suggested = summarizeUserAgent(window.navigator.userAgent)
      expect(suggested).not.toBeNull()
      expect(field).toHaveValue(suggested)
      expect(field).toHaveAttribute(
        'placeholder',
        zhCN.mfa.enroll.labelPlaceholder,
      )
      await expectNoAxeViolations()

      // The flow's first input carries no label: it was sent before the name
      // was typed.
      const start = rig.calls.find(
        (call) => call.method === 'POST' && call.path === FLOW_START,
      )
      expect(start?.body).toEqual({
        purpose: 'enroll',
        input: { kind: 'webauthn', label: '' },
      })

      await user.clear(field)
      await user.type(field, 'My phone')
      await user.type(
        screen.getByLabelText(zhCN.flowStep.codeLabel),
        '123456',
      )
      await confirmEnrollment(user)

      await waitFor(() => {
        const patch = rig.calls.find(
          (call) =>
            call.method === 'PATCH' &&
            call.path === '/api/v1/auth/factors/factor-new',
        )
        expect(patch?.body).toEqual({ label: 'My phone' })
      })
      // The list row the enrollment added now reads the typed name.
      expect(await screen.findByText('My phone')).toBeTruthy()
    } finally {
      restore()
    }
  })

  it('sends no rename when the enrollment name is left empty', async () => {
    let enrolled = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(200, methodsDoc(['webauthn']))
      }
      if (call.method === 'GET' && call.path === FACTORS_PATH) {
        return jsonResponse(200, {
          factors: enrolled ? [passkey({ id: 'factor-new', label: '' })] : [],
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

    await openEnrollment(user, zhCN.mfa.factor.kind.webauthn)
    // The suggested name is cleared: an empty name is the server's own
    // empty label, so no rename follows.
    await user.clear(screen.getByLabelText(zhCN.mfa.enroll.labelField))
    await user.type(
      await screen.findByLabelText(zhCN.flowStep.codeLabel),
      '123456',
    )
    await confirmEnrollment(user)

    // An empty name is the server's own empty label: no rename follows.
    expect(
      await screen.findByText(
        zhCN.mfa.enroll.enrolled.replace(
          '{{factor}}',
          zhCN.mfa.factor.kind.webauthn,
        ),
      ),
    ).toBeTruthy()
    expect(
      rig.calls.filter(
        (call) =>
          call.method === 'PATCH' &&
          call.path.startsWith('/api/v1/auth/factors/'),
      ),
    ).toHaveLength(0)
  })

  it('shows each row’s name and last use in the account time zone', async () => {
    const timeZone = 'Asia/Shanghai'
    const lastUsed = formatInstant('2026-02-01T00:00:00Z', 'zh-CN', {
      timeZone,
    })
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(200, methodsDoc(['webauthn']))
      }
      if (call.method === 'GET' && call.path === FACTORS_PATH) {
        return jsonResponse(200, {
          factors: [
            passkey({
              id: 'factor-1',
              label: 'Phone app',
              created_at: '2026-01-01T00:00:00Z',
              last_used_at: '2026-02-01T00:00:00Z',
            }),
            passkey({
              id: 'factor-2',
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
      screen.getByText(
        zhCN.mfa.factor.lastUsedAt.replace('{{time}}', lastUsed),
      ),
    ).toBeTruthy()
    // The unnamed row reads as its kind; the group heading is the only
    // other place that text appears.
    expect(screen.getAllByText(zhCN.mfa.factor.kind.webauthn)).toHaveLength(2)
    // A factor that has never passed a verification says so.
    expect(screen.getAllByText(zhCN.mfa.factor.neverUsed)).toHaveLength(1)
  })

  it('renames a factor through PATCH and refetches the list', async () => {
    let renamed = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(200, methodsDoc(['webauthn']))
      }
      if (call.method === 'GET' && call.path === FACTORS_PATH) {
        return jsonResponse(200, {
          factors: [
            passkey({
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
        return jsonResponse(200, passkey({ id: 'factor-1', label: 'New name' }))
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { rig } = mount(respond)
    const user = userEvent.setup()

    const field = await openRenameEditor(user, 'Old name')
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
        return jsonResponse(200, methodsDoc(['webauthn']))
      }
      if (call.method === 'GET' && call.path === FACTORS_PATH) {
        return jsonResponse(200, {
          factors: [passkey({ id: 'factor-1', label: 'Old name' })],
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

    const field = await openRenameEditor(user, 'Old name')
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

  it('closes a rename whose factor is gone and rereads the list once', async () => {
    // The reread keeps the row so that the editor's closing is observable:
    // a missing `setRenameDraft(null)` would leave the field mounted even
    // after the list settles.
    let factorsRead = 0
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(200, methodsDoc(['webauthn']))
      }
      if (call.method === 'GET' && call.path === FACTORS_PATH) {
        factorsRead += 1
        return jsonResponse(200, {
          factors: [passkey({ id: 'factor-1', label: 'Old name' })],
        })
      }
      if (
        call.method === 'PATCH' &&
        call.path === '/api/v1/auth/factors/factor-1'
      ) {
        return errorResponse(404, 'authn.factor_not_found')
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()

    const field = await openRenameEditor(user, 'Old name')
    await user.clear(field)
    await user.type(field, 'New name')
    await user.click(screen.getByRole('button', { name: zhCN.mfa.renameSave }))

    expect(
      await screen.findByText(zhCN.errors.authn.factor_not_found),
    ).toBeTruthy()
    // The editor closed even though the reread still carries the row.
    expect(screen.queryByLabelText(zhCN.mfa.renameLabel)).toBeNull()
    // Exactly one reread after the initial read: the first read, the
    // answer's reread, and no loop from the answer's own rendering.
    await waitFor(() => expect(factorsRead).toBe(2))
    expect(factorsRead).toBe(2)
  })

  it('names the removed row when two factors share a kind', async () => {
    // Two authenticator-app factors with different labels: the confirmation
    // must say which row went, not just its kind.
    let removed = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(200, methodsDoc(['webauthn']))
      }
      if (call.method === 'GET' && call.path === FACTORS_PATH) {
        return jsonResponse(200, {
          factors: removed
            ? [passkey({ id: 'factor-1', label: 'Phone app' })]
            : [
                passkey({ id: 'factor-1', label: 'Phone app' }),
                passkey({ id: 'factor-2', label: 'Tablet app' }),
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

    // Two factors of the same kind are told apart by their own names, not
    // by the kind they share: each row carries its own remove action.
    await screen.findByText('Phone app')
    expect(rowAction('Phone app', zhCN.mfa.remove)).toBeTruthy()
    const dialog = await openRemoveConfirm(user, 'Tablet app')
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
        zhCN.mfa.removed.replace('{{factor}}', zhCN.mfa.factor.kind.webauthn),
      ),
    ).toBeNull()
    expect(
      screen.queryByText(zhCN.mfa.removed.replace('{{factor}}', 'Phone app')),
    ).toBeNull()
  })

  it('shows only the later removal result after an enrollment', async () => {
    // Enrollment and removal report through the section's one result slot:
    // the later removal replaces the enrollment's confirmation instead of
    // the two standing beside each other.
    let enrolled = false
    let removed = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(200, methodsDoc(['totp', 'webauthn']))
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
                passkey({ id: 'factor-old', label: 'Old app' }),
                factor({
                  id: 'factor-new',
                  kind: 'totp',
                  label: 'My authenticator',
                }),
              ]
            : [passkey({ id: 'factor-old', label: 'Old app' })],
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

    await openEnrollment(user, zhCN.mfa.factor.kind.totp)
    await user.type(
      await screen.findByLabelText(zhCN.flowStep.codeLabel),
      '123456',
    )
    await confirmEnrollment(user)

    const enrollText = zhCN.mfa.enroll.enrolled.replace(
      '{{factor}}',
      zhCN.mfa.factor.kind.totp,
    )
    expect(await screen.findByText(enrollText)).toBeTruthy()

    // Remove the pre-existing row.
    const dialog = await openRemoveConfirm(user, 'Old app')
    await user.click(
      within(dialog).getByRole('button', {
        name: zhCN.mfa.removeConfirm.confirmLabel,
      }),
    )

    const removeText = zhCN.mfa.removed.replace('{{factor}}', 'Old app')
    expect(await screen.findByText(removeText)).toBeTruthy()
    // The one slot holds the later result: the enrollment's confirmation is
    // gone, not standing beside the removal's.
    await waitFor(() => expect(screen.queryByText(enrollText)).toBeNull())
  })

  it('stays quiet about the single-factor consequence when another method remains', async () => {
    let elevated = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(200, methodsDoc(['totp']))
      }
      if (call.method === 'GET' && call.path === FACTORS_PATH) {
        return jsonResponse(200, {
          factors: [
            passkey({ id: 'factor-1' }),
            passkey({ id: 'factor-2', label: 'Other app' }),
          ],
        })
      }
      if (
        call.method === 'DELETE' &&
        call.path === '/api/v1/auth/factors/factor-1'
      ) {
        if (!elevated) {
          return errorResponse(401, 'authn.insufficient_user_authentication')
        }
        return new Response(null, { status: 204 })
      }
      if (call.method === 'POST' && call.path === FLOW_START) {
        // The server excludes the factor being removed: only the other one
        // is offered.
        return jsonResponse(200, {
          seq: 1,
          state: 'second',
          next: {
            kind: 'factor',
            factors: [{ id: 'factor-2', kind: 'totp', label: 'Other app' }],
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

    const confirm = await openRemoveConfirm(user, 'My authenticator')
    await user.click(
      within(confirm).getByRole('button', {
        name: zhCN.mfa.removeConfirm.confirmLabel,
      }),
    )

    // Another method remains: the dialog states no consequence and the flow
    // excludes only the factor being removed.
    await screen.findByRole('button', { name: zhCN.mfa.factor.kind.totp })
    expect(screen.queryByText(zhCN.mfa.stepUpDeleteLastNote)).toBeNull()
    const start = rig.calls.find(
      (call) => call.method === 'POST' && call.path === FLOW_START,
    )
    expect(start?.body).toMatchObject({
      purpose: 'step-up',
      input: { exclude_factor: 'factor-1' },
    })
    await user.click(screen.getByRole('button', { name: zhCN.mfa.factor.kind.totp }))
    await user.type(await screen.findByLabelText(zhCN.flowStep.codeLabel), '123456')
    await user.click(
      screen.getByRole('button', { name: zhCN.mfa.stepUp.confirmLabel }),
    )

    // The confirmation is the ordinary one: the account keeps another way.
    expect(
      await screen.findByText(
        zhCN.mfa.removed.replace('{{factor}}', 'My authenticator'),
      ),
    ).toBeTruthy()
  })

  it('walks the password channel when the removed factor is the last one', async () => {
    let elevated = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(200, methodsDoc(['totp']))
      }
      if (call.method === 'GET' && call.path === FACTORS_PATH) {
        return jsonResponse(200, { factors: [passkey({ id: 'factor-1' })] })
      }
      if (
        call.method === 'DELETE' &&
        call.path === '/api/v1/auth/factors/factor-1'
      ) {
        if (!elevated) {
          return errorResponse(401, 'authn.insufficient_user_authentication')
        }
        return new Response(null, { status: 204 })
      }
      if (call.method === 'POST' && call.path === FLOW_START) {
        // The last factor was excluded, so the server starts at the
        // password and offers no factor step at all.
        return jsonResponse(200, {
          seq: 1,
          state: 'first',
          next: {
            kind: 'challenge',
            fields: [{ name: 'password', kind: 'password' }],
          },
        })
      }
      if (call.method === 'POST' && call.path === FLOW_ANSWER) {
        const body = call.body as { seq: number; step: string }
        if (body.step === 'answer') {
          return jsonResponse(200, {
            seq: 2,
            state: 'ready',
            next: { kind: 'complete' },
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

    const confirm = await openRemoveConfirm(user, 'My authenticator')
    await user.click(
      within(confirm).getByRole('button', {
        name: zhCN.mfa.removeConfirm.confirmLabel,
      }),
    )

    // The dialog states the consequence and asks for the password alone.
    expect(await screen.findByText(zhCN.mfa.stepUpDeleteLastNote)).toBeTruthy()
    await user.type(
      await screen.findByLabelText(zhCN.flowStep.field.password),
      'secret',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.mfa.stepUp.confirmLabel }),
    )

    // The won step-up re-runs the removal, which now lands with the
    // single-factor confirmation.
    expect(
      await screen.findByText(
        zhCN.mfa.removedLast.replace('{{factor}}', 'My authenticator'),
      ),
    ).toBeTruthy()
    expect(
      rig.calls.filter(
        (call) =>
          call.method === 'DELETE' && call.path === '/api/v1/auth/factors/factor-1',
      ),
    ).toHaveLength(2)
  })

  it('clears the previous result when a new attempt starts', async () => {
    // The section's result slot is cleared when a new write attempt starts,
    // even an attempt that opens an enrollment rather than a row action.
    let removed = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(200, methodsDoc(['totp']))
      }
      if (call.method === 'GET' && call.path === FACTORS_PATH) {
        return jsonResponse(200, {
          factors: removed ? [] : [passkey({ id: 'factor-1' })],
        })
      }
      if (
        call.method === 'DELETE' &&
        call.path === '/api/v1/auth/factors/factor-1'
      ) {
        removed = true
        return new Response(null, { status: 204 })
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
    mount(respond)
    const user = userEvent.setup()

    const dialog = await openRemoveConfirm(user, 'My authenticator')
    await user.click(
      within(dialog).getByRole('button', {
        name: zhCN.mfa.removeConfirm.confirmLabel,
      }),
    )
    const removeText = zhCN.mfa.removedLast.replace(
      '{{factor}}',
      'My authenticator',
    )
    expect(await screen.findByText(removeText)).toBeTruthy()

    // Opening an enrollment is a new attempt: the slot is reset before the
    // flow goes out, so the removal's confirmation leaves the screen.
    await openEnrollment(user, zhCN.mfa.factor.kind.totp)
    await waitFor(() => expect(screen.queryByText(removeText)).toBeNull())
  })

  it('replaces a rename result with a later removal result', async () => {
    // Rename and removal report through the section's one result slot: the
    // result that settles last replaces the earlier one instead of standing
    // beside it.
    let renamed = false
    let removed = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(200, methodsDoc(['totp']))
      }
      if (call.method === 'GET' && call.path === FACTORS_PATH) {
        const first = passkey({
          id: 'factor-a',
          label: renamed ? 'Alpha2' : 'Alpha',
        })
        return jsonResponse(200, {
          factors: removed
            ? [first]
            : [first, passkey({ id: 'factor-b', label: 'Beta' })],
        })
      }
      if (
        call.method === 'PATCH' &&
        call.path === '/api/v1/auth/factors/factor-a'
      ) {
        renamed = true
        return jsonResponse(200, passkey({ id: 'factor-a', label: 'Alpha2' }))
      }
      if (
        call.method === 'DELETE' &&
        call.path === '/api/v1/auth/factors/factor-b'
      ) {
        removed = true
        return new Response(null, { status: 204 })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()

    const field = await openRenameEditor(user, 'Alpha')
    await user.clear(field)
    await user.type(field, 'Alpha2')
    await user.click(screen.getByRole('button', { name: zhCN.mfa.renameSave }))

    const renamedText = zhCN.mfa.renamed.replace('{{factor}}', 'Alpha2')
    expect(await screen.findByText(renamedText)).toBeTruthy()

    const dialog = await openRemoveConfirm(user, 'Beta')
    await user.click(
      within(dialog).getByRole('button', {
        name: zhCN.mfa.removeConfirm.confirmLabel,
      }),
    )

    const removedText = zhCN.mfa.removed.replace('{{factor}}', 'Beta')
    expect(await screen.findByText(removedText)).toBeTruthy()
    // The row actions' one region shows one result: the rename's
    // confirmation is gone, not standing beside the removal's.
    await waitFor(() => expect(screen.queryByText(renamedText)).toBeNull())
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

    await openEnrollment(user, zhCN.mfa.factor.kind.totp)

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
      await openEnrollment(user, zhCN.mfa.factor.kind.webauthn)
      // A different kind gets its own name in the heading, from the same key.
      expect(
        screen.getByRole('heading', {
          name: enrollTitleOf(zhCN.mfa.factor.kind.webauthn),
        }),
      ).toBeTruthy()
      // The passkey registration step states what confirming will do; its
      // own prompt does not carry that sentence.
      expect(
        screen.getByText(zhCN.mfa.enroll.note.webauthn),
      ).toBeVisible()
      // The one confirmation is also the user gesture the creation ceremony
      // needs; the `complete` step that follows is taken automatically.
      await confirmEnrollment(user)
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
    // A kind this package has no wording for renders the panel's generic
    // other name, still from the one parameterized title key.
    expect(
      screen.getByRole('heading', {
        name: enrollTitleOf(zhCN.mfa.factor.kind.other),
      }),
    ).toBeTruthy()
    // The unknown kind's own flow starts with the panel; the generic label
    // never hides the deployment's vocabulary from the request.
    await waitFor(() => {
      const start = rig.calls.find(
        (call) => call.method === 'POST' && call.path === FLOW_START,
      )
      expect(start?.body).toEqual({
        purpose: 'enroll',
        input: { kind: 'magic', label: '' },
      })
    })
  })

  it('raises the factor remove control to the 44px floor under a coarse pointer', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(200, methodsDoc(['totp']))
      }
      if (call.method === 'GET' && call.path === FACTORS_PATH) {
        return jsonResponse(200, { factors: [passkey({})] })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    await screen.findByText('My authenticator')
    const remove = rowAction('My authenticator', zhCN.mfa.remove)
    expect(remove).not.toBeNull()
    expect(hasCoarsePointerTouchTarget(remove as HTMLElement)).toBe(true)
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
          ? jsonResponse(200, { factors: [passkey({})] })
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

  it('names the authenticator row by its kind and offers no rename', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(200, methodsDoc(['totp', 'webauthn']))
      }
      if (call.method === 'GET' && call.path === FACTORS_PATH) {
        return jsonResponse(200, {
          factors: [
            factor({ id: 'factor-1', label: 'My authenticator' }),
            passkey({ id: 'factor-2', label: 'Work key' }),
          ],
        })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)

    // The single authenticator entry reads as its kind, not as the name
    // the list carries; the heading shows the same word.
    expect(
      (await screen.findAllByText(zhCN.mfa.factor.kind.totp)).length,
    ).toBe(2)
    expect(screen.queryByText('My authenticator')).toBeNull()
    // With one entry active, the group's action offers a replacement rather
    // than a plain add.
    expect(
      screen.getByRole('button', { name: replaceName() }),
    ).toBeTruthy()
    expect(
      screen.queryByRole('button', {
        name: addNameOf(zhCN.mfa.factor.kind.totp),
      }),
    ).toBeNull()

    // The authenticator row offers removal alone; the passkey's row offers
    // rename then removal.
    expect(
      rowActions(zhCN.mfa.factor.kind.totp).map((button) =>
        button.textContent?.trim(),
      ),
    ).toEqual([zhCN.mfa.remove])
    expect(rowAction('Work key', zhCN.mfa.rename)).toBeTruthy()
    expect(
      rowActions('Work key').map((button) => button.textContent?.trim()),
    ).toEqual([zhCN.mfa.rename, zhCN.mfa.remove])
    expect(screen.queryByRole('menuitem')).toBeNull()
  })

  it('asks for a name only on the passkey panel', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(200, methodsDoc(['totp', 'webauthn']))
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
      if (call.method === 'DELETE' && call.path === FLOW_CURRENT) {
        return new Response(null, { status: 401 })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()

    // The passkey may be held several times, so its panel asks for a name.
    await openEnrollment(user, zhCN.mfa.factor.kind.webauthn)
    expect(screen.getByLabelText(zhCN.mfa.enroll.labelField)).toBeTruthy()
    await user.click(
      screen.getByRole('button', { name: zhCN.mfa.enroll.cancelLabel }),
    )
    await waitFor(() =>
      expect(
        screen.queryByLabelText(zhCN.mfa.enroll.labelField),
      ).toBeNull(),
    )

    // The single authenticator entry takes none.
    await openEnrollment(user, zhCN.mfa.factor.kind.totp)
    expect(
      screen.queryByLabelText(zhCN.mfa.enroll.labelField),
    ).toBeNull()
  })

  it('abandons the enrollment flow once when the panel is cancelled', async () => {
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
      if (call.method === 'DELETE' && call.path === FLOW_CURRENT) {
        return new Response(null, { status: 204 })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { rig } = mount(respond)
    const user = userEvent.setup()

    await openEnrollment(user, zhCN.mfa.factor.kind.totp)
    await screen.findByRole('img', { name: zhCN.flowStep.totpQrLabel })
    await user.click(
      screen.getByRole('button', { name: zhCN.mfa.enroll.cancelLabel }),
    )

    await waitFor(() => expect(abandonCalls(rig.calls)).toHaveLength(1))
    // The panel closed onto the entries, and no second release follows.
    expect(
      await screen.findByRole('button', {
        name: addNameOf(zhCN.mfa.factor.kind.totp),
      }),
    ).toBeTruthy()
    await act(async () => {
      await Promise.resolve()
    })
    expect(abandonCalls(rig.calls)).toHaveLength(1)
  })

  it('abandons the enrollment flow once when the section unmounts', async () => {
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
      if (call.method === 'DELETE' && call.path === FLOW_CURRENT) {
        return new Response(null, { status: 204 })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { rig, unmount } = mount(respond)
    const user = userEvent.setup()

    await openEnrollment(user, zhCN.mfa.factor.kind.totp)
    await screen.findByRole('img', { name: zhCN.flowStep.totpQrLabel })

    unmount()
    await waitFor(() => expect(abandonCalls(rig.calls)).toHaveLength(1))
  })

  it('abandons the enrollment flow once when the reader gives up after a failed answer', async () => {
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
      if (call.method === 'DELETE' && call.path === FLOW_CURRENT) {
        return new Response(null, { status: 204 })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { rig } = mount(respond)
    const user = userEvent.setup()

    await openEnrollment(user, zhCN.mfa.factor.kind.totp)
    await user.type(
      await screen.findByLabelText(zhCN.flowStep.codeLabel),
      '123456',
    )
    await confirmEnrollment(user)
    expect(await screen.findByRole('alert')).toHaveTextContent(
      zhCN.errors.authn.bad_request,
    )
    // The step stays for a retry; giving up releases the flow exactly once.
    expect(abandonCalls(rig.calls)).toHaveLength(0)
    await user.click(
      screen.getByRole('button', { name: zhCN.mfa.enroll.cancelLabel }),
    )
    await waitFor(() => expect(abandonCalls(rig.calls)).toHaveLength(1))
  })

  it('renders a row with one action as a plain danger button that opens its confirm', async () => {
    let removed = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(200, methodsDoc(['webauthn']))
      }
      if (call.method === 'GET' && call.path === FACTORS_PATH) {
        return jsonResponse(200, {
          factors: removed
            ? []
            : [passkey({ id: 'factor-9', status: 'disabled', label: 'Old key' })],
        })
      }
      if (
        call.method === 'DELETE' &&
        call.path === '/api/v1/auth/factors/factor-9'
      ) {
        removed = true
        return new Response(null, { status: 204 })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { rig } = mount(respond)
    const user = userEvent.setup()

    await screen.findByText('Old key')
    // A disabled row offers removal alone: one action, rendered as its own
    // small button rather than behind an overflow trigger or in an action row.
    const actions = rowActions('Old key')
    expect(actions.map((button) => button.textContent?.trim())).toEqual([
      zhCN.mfa.remove,
    ])
    const remove = rowAction('Old key', zhCN.mfa.remove)
    expect(remove).not.toBeNull()
    expect(remove).toHaveClass('MuiButton-colorError')
    expect((remove as HTMLElement).closest('[data-action-row]')).toBeNull()
    // Neither an overflow trigger nor a menu item remains.
    expect(
      actions.some((button) => button.hasAttribute('aria-haspopup')),
    ).toBe(false)
    expect(screen.queryByRole('menuitem')).toBeNull()

    await user.click(remove as HTMLElement)
    const dialog = await screen.findByRole('dialog')
    expect(dialog).toHaveTextContent(zhCN.mfa.removeConfirm.title)
    await user.click(
      within(dialog).getByRole('button', {
        name: zhCN.mfa.removeConfirm.confirmLabel,
      }),
    )
    await waitFor(() =>
      expect(
        rig.calls.filter(
          (call) =>
            call.method === 'DELETE' &&
            call.path === '/api/v1/auth/factors/factor-9',
        ),
      ).toHaveLength(1),
    )
    await waitFor(() =>
      expect(screen.queryByText('Old key')).toBeNull(),
    )
  })
})
