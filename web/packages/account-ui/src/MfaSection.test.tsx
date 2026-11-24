/**
 * MfaSection checks the factors list, the enrollment entries, the remove route and the enroll flow.
 */

import { describe, expect, it, vi } from 'vitest'
import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { getAuthnMfaFactorsQueryKey, getAuthnProfileGetQueryKey } from '@speed/api-sdk'
import { formatInstant } from '@speed/i18n'
import { DisplayTimeZoneProvider } from '@speed/ui-kit'
import zhCN from './locales/zh-CN.json' with { type: 'json' }
import {
  errorResponse,
  jsonResponse,
  makeRealClientRig,
  type RealCall,
  type RealResponder,
} from '../test-utils/real-client.js'
import { identifier, profileBody } from '../test-utils/fixtures.js'
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

function passkey(overrides: Record<string, unknown>): Record<string, unknown> {
  return factor({ kind: 'webauthn', ...overrides })
}

const replaceName = (): string => zhCN.mfa.replaceAria

function mount(
  respond: RealResponder,
  options: {
    readonly returnPath?: string
    readonly profile?: Record<string, unknown>
    readonly timeZone?: string
  } = {},
) {
  const rig = makeRealClientRig((call: RealCall) => {
    if (call.method === 'GET' && call.path === PROFILE_PATH) {
      return jsonResponse(200, options.profile ?? profileBody())
    }
    return respond(call)
  })
  rig.store.set('access-1')
  const section = (
    <MfaSection session={rig.session} returnPath={options.returnPath} />
  )
  const rendered = renderWithProviders(
    options.timeZone === undefined ? (
      section
    ) : (
      <DisplayTimeZoneProvider timeZone={options.timeZone}>
        {section}
      </DisplayTimeZoneProvider>
    ),
  )
  return { rig, ...rendered }
}

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

const addNameOf = (method: string): string =>
  zhCN.mfa.addAria.replace('{{method}}', method)

async function openEnrollment(
  user: ReturnType<typeof userEvent.setup>,
  method: string,
): Promise<void> {
  await user.click(
    await screen.findByRole('button', { name: addNameOf(method) }),
  )
}

async function confirmEnrollment(
  user: ReturnType<typeof userEvent.setup>,
): Promise<void> {
  await user.click(
    await screen.findByRole('button', { name: zhCN.mfa.enroll.confirmLabel }),
  )
}

const enrollTitleOf = (method: string): string =>
  zhCN.mfa.enroll.title.replace('{{method}}', method)

const abandonCalls = (calls: readonly RealCall[]): RealCall[] =>
  calls.filter(
    (call) => call.method === 'DELETE' && call.path === FLOW_CURRENT,
  )

const renameCalls = (calls: readonly RealCall[]): RealCall[] =>
  calls.filter((call) => call.method === 'PATCH')

function factorRow(name: string): HTMLElement {
  const row = screen
    .getAllByRole('listitem')
    .find((item) => within(item).queryByText(name, { exact: true }) !== null)
  if (row === undefined) {
    throw new Error(`no factor row reads as ${name}`)
  }
  return row
}

function rowActions(name: string): HTMLElement[] {
  return within(factorRow(name)).queryAllByRole('button')
}

function rowAction(name: string, action: string): HTMLElement | null {
  return within(factorRow(name)).queryByRole('button', { name: action })
}

async function clickRowAction(
  user: ReturnType<typeof userEvent.setup>,
  name: string,
  action: string,
): Promise<void> {
  await waitFor(() => expect(factorRow(name)).toBeTruthy())
  await user.click(within(factorRow(name)).getByRole('button', { name: action }))
}

async function openRemoveConfirm(
  user: ReturnType<typeof userEvent.setup>,
  factor: string,
): Promise<HTMLElement> {
  await clickRowAction(user, factor, zhCN.mfa.remove)
  return screen.findByRole('dialog')
}

async function openRenameEditor(
  user: ReturnType<typeof userEvent.setup>,
  factor: string,
): Promise<HTMLElement> {
  await clickRowAction(user, factor, zhCN.mfa.rename)
  return screen.findByLabelText(zhCN.mfa.renameLabel)
}

function deferred<T>(): {
  readonly promise: Promise<T>
  readonly resolve: (value: T) => void
} {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((settle) => {
    resolve = settle
  })
  return { promise, resolve }
}

function enrollCodeStep(): Record<string, unknown> {
  return {
    seq: 1,
    state: 'enroll',
    next: { kind: 'webauthn', options: { otpauth_uri: OTPAUTH_URI } },
  }
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
    expect(
      (await screen.findAllByText(zhCN.mfa.factor.kind.totp)).length,
    ).toBeGreaterThan(0)
    expect(
      screen.getByRole('region', { name: zhCN.mfa.factor.kind.totp }),
    ).toBeTruthy()
    expect(
      screen.getByRole('region', { name: zhCN.mfa.factor.kind.webauthn }),
    ).toBeTruthy()
    expect(screen.queryByText(zhCN.mfa.factor.status.pending)).toBeNull()
    expect(screen.queryByText(zhCN.mfa.factor.status.active)).toBeNull()
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
    const active = rowActions('Active row')
    expect(active.map((button) => button.textContent?.trim())).toEqual([
      zhCN.mfa.rename,
      zhCN.mfa.remove,
    ])
    expect(
      active.every((button) => button.closest('[data-action-row]') !== null),
    ).toBe(true)
    expect(screen.queryByRole('menuitem')).toBeNull()

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

    expect(
      await screen.findByRole('button', { name: addNameOf(zhCN.mfa.factor.kind.totp) }),
    ).toBeTruthy()
    expect(
      screen.queryByRole('button', { name: addNameOf(zhCN.mfa.factor.kind.webauthn) }),
    ).toBeNull()
  })

  it('says so in place when exactly one factor is enabled', async () => {
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
    expect(screen.queryByText('My authenticator')).toBeNull()
    expect(screen.queryByText(zhCN.mfa.singleFactor)).toBeNull()
  })

  it('withholds the single-factor note when the deployment offers no enrollment entry', async () => {
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

    expect(
      await screen.findByText(
        zhCN.mfa.removedLast.replace('{{factor}}', 'My authenticator'),
      ),
    ).toBeTruthy()
    expect(screen.queryByText(zhCN.errors.authn.factor_not_found)).toBeNull()
    await waitFor(() => expect(factorsRead).toBe(2))
    expect(factorsRead).toBe(2)
  })

  it('holds the session through the credential rotation an active factor removal performs', async () => {
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
    expect(rig.session.getSnapshot().state).toBe('authenticated')
    expect(rig.endedEdges()).toBe(0)
  })

  it('lets a real anonymous answer end the session when a disabled factor removal rotates nothing', async () => {
    captureRefreshWarning({ expected: true })
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

    await waitFor(() =>
      expect(rig.session.getSnapshot().state).toBe('anonymous'),
    )
    expect(rig.transitionBegins()).toBe(0)
    expect(rig.endedEdges()).toBe(1)
  })

  it('releases the window a refused active-factor removal opened', async () => {
    captureRefreshWarning({ expected: true })
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

    expect(refused).toBe(true)
    await screen.findByText(zhCN.errors.authn.conflict)
    expect(rig.transitionBegins()).toBe(1)

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

    await screen.findByRole('button', { name: zhCN.mfa.factor.kind.totp })
    expect(screen.getByText(zhCN.mfa.stepUpDeleteLastNote)).toBeTruthy()
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

  it('opens a fresh window for the removal a won step-up re-runs', async () => {
    const warn = captureRefreshWarning()
    let elevated = false
    let rotated = false
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

    await screen.findByRole('button', { name: zhCN.mfa.factor.kind.totp })
    await user.click(screen.getByRole('button', { name: zhCN.mfa.factor.kind.totp }))
    await user.type(await screen.findByLabelText(zhCN.flowStep.codeLabel), '123456')
    await user.click(
      screen.getByRole('button', { name: zhCN.mfa.stepUp.confirmLabel }),
    )

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
    expect(
      screen.getByRole('heading', {
        name: enrollTitleOf(zhCN.mfa.factor.kind.totp),
      }),
    ).toBeTruthy()

    await waitFor(() => {
      const start = rig.calls.find(
        (call) => call.method === 'POST' && call.path === FLOW_START,
      )
      expect(start?.body).toEqual({
        purpose: 'enroll',
        input: { kind: 'totp', label: '' },
      })
    })

    expect(screen.getByText(zhCN.mfa.enroll.note.totp)).toBeVisible()
    expect(
      screen.queryByLabelText(zhCN.mfa.enroll.labelField),
    ).toBeNull()

    expect(
      await screen.findByRole('img', { name: zhCN.flowStep.totpQrLabel }),
    ).toBeVisible()
    expect(screen.queryByText(/"otpauth_uri"/)).toBeNull()
    expect(screen.queryByText(/otpauth:\/\//)).toBeNull()
    expect(
      screen.getByRole('heading', {
        name: enrollTitleOf(zhCN.mfa.factor.kind.totp),
      }),
    ).toBeTruthy()
    await user.type(
      screen.getByLabelText(zhCN.flowStep.codeLabel),
      '123456',
    )
    await confirmEnrollment(user)

    await screen.findAllByText(zhCN.mfa.factor.kind.totp)
    expect(zhCN.mfa.enroll.enrolled).toContain('{{factor}}')
    expect(
      await screen.findByText(
        zhCN.mfa.enroll.enrolled.replace(
          '{{factor}}',
          zhCN.mfa.factor.kind.totp,
        ),
      ),
    ).toBeTruthy()
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
    expect(
      rig.calls.filter(
        (call) => call.method === 'POST' && call.path === FLOW_ANSWER,
      ),
    ).toHaveLength(0)
    await confirmEnrollment(user)

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
      const field = screen.getByLabelText(zhCN.mfa.enroll.labelField)
      const suggested = summarizeUserAgent(window.navigator.userAgent)
      expect(suggested).not.toBeNull()
      expect(field).toHaveValue(suggested)
      expect(field).toHaveAttribute(
        'placeholder',
        zhCN.mfa.enroll.labelPlaceholder,
      )
      await expectNoAxeViolations()

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
    await user.clear(screen.getByLabelText(zhCN.mfa.enroll.labelField))
    await user.type(
      await screen.findByLabelText(zhCN.flowStep.codeLabel),
      '123456',
    )
    await confirmEnrollment(user)

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

  it('shows each row’s name and last use in the host’s display time zone', async () => {
    const timeZone = 'Asia/Tokyo'
    const browserZone = Intl.DateTimeFormat().resolvedOptions().timeZone
    const lastUsed = formatInstant('2026-02-01T00:00:00Z', 'zh-CN', {
      timeZone,
    })
    const browserLastUsed = formatInstant('2026-02-01T00:00:00Z', 'zh-CN', {
      timeZone: browserZone,
    })
    expect(lastUsed).not.toBe(browserLastUsed)
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
    mount(respond, { timeZone, profile: profileBody({ time_zone: timeZone }) })

    expect(await screen.findByText('Phone app')).toBeTruthy()
    expect(
      screen.getByText(
        zhCN.mfa.factor.lastUsedAt.replace('{{time}}', lastUsed),
      ),
    ).toBeTruthy()
    expect(
      screen.queryByText(
        zhCN.mfa.factor.lastUsedAt.replace('{{time}}', browserLastUsed),
      ),
    ).toBeNull()
    expect(screen.getAllByText(zhCN.mfa.factor.kind.webauthn)).toHaveLength(2)
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
    await user.type(field, 'x'.repeat(65))
    await user.click(screen.getByRole('button', { name: zhCN.mfa.renameSave }))

    expect(
      await screen.findByText(zhCN.errors.authn.bad_request),
    ).toBeTruthy()
    expect(screen.getByLabelText(zhCN.mfa.renameLabel)).toBeVisible()
  })

  it('closes a rename whose factor is gone and rereads the list once', async () => {
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
    expect(screen.queryByLabelText(zhCN.mfa.renameLabel)).toBeNull()
    await waitFor(() => expect(factorsRead).toBe(2))
    expect(factorsRead).toBe(2)
  })

  it('names the removed row when two factors share a kind', async () => {
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

    const dialog = await openRemoveConfirm(user, 'Old app')
    await user.click(
      within(dialog).getByRole('button', {
        name: zhCN.mfa.removeConfirm.confirmLabel,
      }),
    )

    const removeText = zhCN.mfa.removed.replace('{{factor}}', 'Old app')
    expect(await screen.findByText(removeText)).toBeTruthy()
    await waitFor(() => expect(screen.queryByText(enrollText)).toBeNull())
  })

  it('stays quiet about the single-factor consequence when another method remains', async () => {
    captureRefreshWarning({ expected: true })
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

    expect(
      await screen.findByText(
        zhCN.mfa.removed.replace('{{factor}}', 'My authenticator'),
      ),
    ).toBeTruthy()
  })

  it('walks the password channel when the removed factor is the last one', async () => {
    captureRefreshWarning({ expected: true })
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

    expect(await screen.findByText(zhCN.mfa.stepUpDeleteLastNote)).toBeTruthy()
    await user.type(
      await screen.findByLabelText(zhCN.flowStep.field.password),
      'secret',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.mfa.stepUp.confirmLabel }),
    )

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

    await openEnrollment(user, zhCN.mfa.factor.kind.totp)
    await waitFor(() => expect(screen.queryByText(removeText)).toBeNull())
  })

  it('replaces a rename result with a later removal result', async () => {
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
      expect(
        screen.getByRole('heading', {
          name: enrollTitleOf(zhCN.mfa.factor.kind.webauthn),
        }),
      ).toBeTruthy()
      expect(
        screen.getByText(zhCN.mfa.enroll.note.webauthn),
      ).toBeVisible()
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

    expect(
      await screen.findByRole('button', { name: addNameOf(zhCN.mfa.factor.kind.sms) }),
    ).toBeTruthy()
    const unknown = screen.getByRole('button', { name: addNameOf(zhCN.mfa.factor.kind.other) })
    expect(unknown).toBeTruthy()

    await user.click(unknown)
    expect(
      screen.getByRole('heading', {
        name: enrollTitleOf(zhCN.mfa.factor.kind.other),
      }),
    ).toBeTruthy()
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

  it('shows an informational notice instead of the SMS panel when no phone is proved', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(200, methodsDoc(['sms']))
      }
      if (call.method === 'GET' && call.path === FACTORS_PATH) {
        return jsonResponse(200, { factors: [] })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { rig } = mount(respond)
    const user = userEvent.setup()

    await openEnrollment(user, zhCN.mfa.factor.kind.sms)

    const notice = screen.getByText(zhCN.mfa.enroll.prerequisite.sms)
    expect(notice).toBeVisible()
    expect(
      screen.queryByRole('heading', {
        name: enrollTitleOf(zhCN.mfa.factor.kind.sms),
      }),
    ).toBeNull()
    expect(screen.queryByLabelText(zhCN.mfa.enroll.labelField)).toBeNull()
    expect(
      screen.queryByRole('button', {
        name: zhCN.mfa.enroll.confirmLabel,
      }),
    ).toBeNull()
    expect(
      screen.queryByRole('button', {
        name: zhCN.mfa.enroll.cancelLabel,
      }),
    ).toBeNull()
    const alert = notice.closest('.MuiAlert-root') as HTMLElement | null
    expect(alert).not.toBeNull()
    expect(within(alert as HTMLElement).queryAllByRole('button')).toHaveLength(
      0,
    )
    expect(
      rig.calls.filter(
        (call) => call.method === 'POST' && call.path === FLOW_START,
      ),
    ).toHaveLength(0)
    expect(abandonCalls(rig.calls)).toHaveLength(0)
    expect(
      screen.getByRole('button', {
        name: addNameOf(zhCN.mfa.factor.kind.sms),
      }),
    ).toBeTruthy()
  })

  it('keeps the SMS panel when the profile omits its identifiers collection', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(200, methodsDoc(['sms']))
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
    const { rig } = mount(respond, {
      profile: profileBody({ identifiers: undefined }),
    })
    const user = userEvent.setup()

    await openEnrollment(user, zhCN.mfa.factor.kind.sms)

    expect(
      screen.queryByText(zhCN.mfa.enroll.prerequisite.sms),
    ).toBeNull()
    expect(
      screen.getByRole('heading', {
        name: enrollTitleOf(zhCN.mfa.factor.kind.sms),
      }),
    ).toBeVisible()
    await waitFor(() => {
      const start = rig.calls.find(
        (call) => call.method === 'POST' && call.path === FLOW_START,
      )
      expect(start?.body).toEqual({
        purpose: 'enroll',
        input: { kind: 'sms', label: '' },
      })
    })
  })

  it('clears the SMS notice once the profile has a proved phone, and a later add starts the flow', async () => {
    const phone = '+8613800138000'
    let proved = false
    const rig = makeRealClientRig((call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            identifiers: proved
              ? [identifier({ kind: 'phone', value: phone, proven: true })]
              : [],
          }),
        )
      }
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(200, methodsDoc(['sms']))
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
    })
    rig.store.set('access-1')
    const { queryClient } = renderWithProviders(
      <MfaSection session={rig.session} />,
    )
    const user = userEvent.setup()

    await openEnrollment(user, zhCN.mfa.factor.kind.sms)
    expect(screen.getByText(zhCN.mfa.enroll.prerequisite.sms)).toBeVisible()

    proved = true
    await act(async () => {
      await queryClient.refetchQueries({
        queryKey: getAuthnProfileGetQueryKey(),
      })
    })
    await waitFor(() =>
      expect(
        screen.queryByText(zhCN.mfa.enroll.prerequisite.sms),
      ).toBeNull(),
    )

    await openEnrollment(user, zhCN.mfa.factor.kind.sms)
    expect(
      screen.getByText(
        zhCN.mfa.enroll.note.smsTo.replace('{{phone}}', phone),
      ),
    ).toBeVisible()
    await waitFor(() => {
      const start = rig.calls.find(
        (call) => call.method === 'POST' && call.path === FLOW_START,
      )
      expect(start?.body).toEqual({
        purpose: 'enroll',
        input: { kind: 'sms', label: '' },
      })
    })
  })

  it('names the proved phone in the SMS note and starts the flow', async () => {
    const phone = '+8613800138000'
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(200, methodsDoc(['sms']))
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
    const { rig } = mount(respond, {
      profile: profileBody({
        identifiers: [identifier({ kind: 'phone', value: phone, proven: true })],
      }),
    })
    const user = userEvent.setup()

    await openEnrollment(user, zhCN.mfa.factor.kind.sms)

    expect(
      screen.getByText(
        zhCN.mfa.enroll.note.smsTo.replace('{{phone}}', phone),
      ),
    ).toBeVisible()
    expect(screen.getByLabelText(zhCN.mfa.enroll.labelField)).toHaveValue('')

    await waitFor(() => {
      const start = rig.calls.find(
        (call) => call.method === 'POST' && call.path === FLOW_START,
      )
      expect(start?.body).toEqual({
        purpose: 'enroll',
        input: { kind: 'sms', label: '' },
      })
    })
  })

  it('renders the phone-required refusal as its own sentence', async () => {
    const phone = '+8613800138000'
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(200, methodsDoc(['sms']))
      }
      if (call.method === 'GET' && call.path === FACTORS_PATH) {
        return jsonResponse(200, { factors: [] })
      }
      if (call.method === 'POST' && call.path === FLOW_START) {
        return errorResponse(403, 'authn.phone_required')
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond, {
      profile: profileBody({
        identifiers: [identifier({ kind: 'phone', value: phone, proven: true })],
      }),
    })
    const user = userEvent.setup()

    await openEnrollment(user, zhCN.mfa.factor.kind.sms)

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.errors.authn.phone_required)
    expect(alert).not.toHaveTextContent(zhCN.errors.unknown)
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

    await waitFor(() =>
      expect(screen.getAllByText(zhCN.mfa.factor.kind.totp)).toHaveLength(2),
    )
    expect(screen.queryByText('My authenticator')).toBeNull()
    expect(
      screen.getByRole('button', { name: replaceName() }),
    ).toBeTruthy()
    expect(
      screen.queryByRole('button', {
        name: addNameOf(zhCN.mfa.factor.kind.totp),
      }),
    ).toBeNull()

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
    captureRefreshWarning({ expected: true })
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
    const actions = rowActions('Old key')
    expect(actions.map((button) => button.textContent?.trim())).toEqual([
      zhCN.mfa.remove,
    ])
    const remove = rowAction('Old key', zhCN.mfa.remove)
    expect(remove).not.toBeNull()
    expect(remove).toHaveClass('MuiButton-colorError')
    expect((remove as HTMLElement).closest('[data-action-row]')).toBeNull()
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

  it('marks the row being removed busy and holds the section’s other actions unavailable while the removal is in flight', async () => {
    const heldRefetch = deferred<Response>()
    let factorsRead = 0
    let removed = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(200, methodsDoc(['webauthn']))
      }
      if (call.method === 'GET' && call.path === FACTORS_PATH) {
        factorsRead += 1
        if (factorsRead === 1) {
          return jsonResponse(200, {
            factors: [
              passkey({ id: 'factor-a', label: 'Laptop key' }),
              passkey({ id: 'factor-b', label: 'Backup key' }),
            ],
          })
        }
        return heldRefetch.promise
      }
      if (
        call.method === 'DELETE' &&
        call.path === '/api/v1/auth/factors/factor-a'
      ) {
        removed = true
        return new Response(null, { status: 204 })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()

    const dialog = await openRemoveConfirm(user, 'Laptop key')
    await user.click(
      within(dialog).getByRole('button', {
        name: zhCN.mfa.removeConfirm.confirmLabel,
      }),
    )

    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    const removeA = within(factorRow('Laptop key')).getByRole('button', {
      name: zhCN.mfa.remove,
    })
    await waitFor(() => expect(removeA).toHaveAttribute('aria-busy', 'true'))
    expect(removeA).toHaveAttribute('aria-disabled', 'true')
    expect(removeA).not.toHaveAttribute('disabled')

    const removeB = within(factorRow('Backup key')).getByRole('button', {
      name: zhCN.mfa.remove,
    })
    expect(removeB).toHaveAttribute('aria-disabled', 'true')
    expect(removeB).not.toHaveAttribute('aria-busy')
    expect(removeB).not.toHaveAttribute('disabled')
    expect(removeB).not.toHaveAccessibleDescription()
    const renameB = within(factorRow('Backup key')).getByRole('button', {
      name: zhCN.mfa.rename,
    })
    expect(renameB).toHaveAttribute('aria-disabled', 'true')
    expect(renameB).not.toHaveAttribute('aria-busy')
    expect(renameB).not.toHaveAccessibleDescription()

    const add = screen.getByRole('button', {
      name: addNameOf(zhCN.mfa.factor.kind.webauthn),
    })
    expect(add).toHaveAttribute('aria-disabled', 'true')
    expect(add).not.toHaveAttribute('aria-busy')
    expect(add).not.toHaveAttribute('disabled')
    expect(add).not.toHaveAccessibleDescription()

    renameB.focus()
    expect(renameB).toHaveFocus()
    await user.keyboard('{Enter}')
    expect(screen.queryByLabelText(zhCN.mfa.renameLabel)).toBeNull()
    add.focus()
    expect(add).toHaveFocus()
    await user.keyboard('{Enter}')
    expect(screen.queryByLabelText(zhCN.mfa.enroll.labelField)).toBeNull()

    await act(async () => {
      heldRefetch.resolve(
        jsonResponse(200, {
          factors: [passkey({ id: 'factor-b', label: 'Backup key' })],
        }),
      )
    })
    expect(removed).toBe(true)
    expect(
      await screen.findByText(
        zhCN.mfa.removed.replace('{{factor}}', 'Laptop key'),
      ),
    ).toBeTruthy()
  })

  it('keeps the rename save busy, the cancel and siblings unavailable and the field read-only while the rename is in flight', async () => {
    const held = deferred<Response>()
    let renamed = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(200, methodsDoc(['webauthn']))
      }
      if (call.method === 'GET' && call.path === FACTORS_PATH) {
        return jsonResponse(200, {
          factors: [
            passkey({
              id: 'factor-a',
              label: renamed ? 'New name' : 'Old name',
            }),
            passkey({ id: 'factor-b', label: 'Backup key' }),
          ],
        })
      }
      if (
        call.method === 'PATCH' &&
        call.path === '/api/v1/auth/factors/factor-a'
      ) {
        return held.promise.then((answer) => {
          renamed = true
          return answer
        })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { rig } = mount(respond)
    const user = userEvent.setup()

    const field = await openRenameEditor(user, 'Old name')
    await user.clear(field)
    await user.type(field, 'New name')
    const save = screen.getByRole('button', { name: zhCN.mfa.renameSave })
    await user.click(save)

    await waitFor(() => expect(save).toHaveAttribute('aria-busy', 'true'))
    expect(save).toHaveAttribute('aria-disabled', 'true')
    expect(save).not.toHaveAttribute('disabled')
    const cancel = screen.getByRole('button', { name: zhCN.mfa.renameCancel })
    expect(cancel).toHaveAttribute('aria-disabled', 'true')
    expect(cancel).not.toHaveAttribute('aria-busy')
    expect(cancel).not.toHaveAttribute('disabled')
    expect(cancel).not.toHaveAccessibleDescription()

    expect(field).toHaveAttribute('readonly')
    expect(field).not.toHaveAttribute('disabled')
    field.focus()
    expect(field).toHaveFocus()
    const before = (field as HTMLInputElement).value
    await user.keyboard('9')
    expect((field as HTMLInputElement).value).toBe(before)

    const removeB = within(factorRow('Backup key')).getByRole('button', {
      name: zhCN.mfa.remove,
    })
    expect(removeB).toHaveAttribute('aria-disabled', 'true')
    expect(removeB).not.toHaveAttribute('aria-busy')
    expect(removeB).not.toHaveAccessibleDescription()
    const add = screen.getByRole('button', {
      name: addNameOf(zhCN.mfa.factor.kind.webauthn),
    })
    expect(add).toHaveAttribute('aria-disabled', 'true')
    expect(add).not.toHaveAttribute('disabled')

    await user.click(removeB)
    expect(screen.queryByRole('dialog')).toBeNull()
    removeB.focus()
    await user.keyboard('{Enter}')
    expect(screen.queryByRole('dialog')).toBeNull()
    await user.click(add)
    expect(screen.queryByLabelText(zhCN.mfa.enroll.labelField)).toBeNull()
    add.focus()
    await user.keyboard('{Enter}')
    expect(screen.queryByLabelText(zhCN.mfa.enroll.labelField)).toBeNull()

    const form = field.closest('form')
    expect(form).not.toBeNull()
    fireEvent.submit(form as HTMLFormElement)
    expect(renameCalls(rig.calls)).toHaveLength(1)
    expect(screen.getByRole('button', { name: zhCN.mfa.renameSave })).toBe(
      save,
    )

    await act(async () => {
      held.resolve(
        jsonResponse(
          200,
          passkey({ id: 'factor-a', label: 'New name' }),
        ),
      )
    })
    expect(
      await screen.findByText(
        zhCN.mfa.renamed.replace('{{factor}}', 'New name'),
      ),
    ).toBeTruthy()
  })

  it('keeps the enrollment name field read-only and the panel’s other controls unavailable while the flow starts', async () => {
    const held = deferred<Response>()
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(200, methodsDoc(['webauthn']))
      }
      if (call.method === 'GET' && call.path === FACTORS_PATH) {
        return jsonResponse(200, { factors: [] })
      }
      if (call.method === 'POST' && call.path === FLOW_START) {
        return held.promise
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { rig } = mount(respond)
    const user = userEvent.setup()

    await openEnrollment(user, zhCN.mfa.factor.kind.webauthn)
    const field = await screen.findByLabelText(zhCN.mfa.enroll.labelField)
    expect(field).toHaveAttribute('readonly')
    expect(field).not.toHaveAttribute('disabled')

    const heldControls = [zhCN.mfa.enroll.cancelLabel, zhCN.mfa.retry].map(
      (name) => screen.getByRole('button', { name }),
    )
    for (const control of heldControls) {
      expect(control).toHaveAttribute('aria-disabled', 'true')
      expect(control).not.toHaveAttribute('aria-busy')
      expect(control).not.toHaveAttribute('disabled')
      expect(control).not.toHaveAccessibleDescription()
    }

    const [cancelControl, retryControl] = heldControls as [
      HTMLElement,
      HTMLElement,
    ]
    const starts = () =>
      rig.calls.filter(
        (call) => call.method === 'POST' && call.path === FLOW_START,
      ).length
    await user.click(cancelControl)
    expect(
      screen.getByRole('button', { name: zhCN.mfa.enroll.cancelLabel }),
    ).toBeTruthy()
    await user.click(retryControl)
    retryControl.focus()
    await user.keyboard('{Enter}')
    expect(starts()).toBe(1)

    await act(async () => {
      held.resolve(jsonResponse(200, enrollCodeStep()))
    })
    await waitFor(() =>
      expect(
        screen.getByRole('button', { name: zhCN.mfa.enroll.cancelLabel }),
      ).not.toHaveAttribute('aria-disabled', 'true'),
    )
  })

  it('marks the enrollment confirm busy and the cancel unavailable while the answer is in flight', async () => {
    const held = deferred<Response>()
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
        return jsonResponse(200, enrollCodeStep())
      }
      if (call.method === 'POST' && call.path === FLOW_ANSWER) {
        const body = call.body as { seq: number; step: string }
        if (body.step === 'enroll') {
          return held.promise.then((answer) => {
            enrolled = true
            return answer
          })
        }
        return jsonResponse(200, { state: 'enrolled' })
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

    const confirm = screen.getByRole('button', {
      name: zhCN.mfa.enroll.confirmLabel,
    })
    await waitFor(() => expect(confirm).toHaveAttribute('aria-busy', 'true'))
    expect(confirm).toHaveAttribute('aria-disabled', 'true')
    expect(confirm).not.toHaveAttribute('disabled')
    const cancel = screen.getByRole('button', {
      name: zhCN.mfa.enroll.cancelLabel,
    })
    expect(cancel).toHaveAttribute('aria-disabled', 'true')
    expect(cancel).not.toHaveAttribute('aria-busy')
    expect(cancel).not.toHaveAttribute('disabled')
    expect(cancel).not.toHaveAccessibleDescription()

    await act(async () => {
      held.resolve(
        jsonResponse(200, { seq: 2, state: 'ready', next: { kind: 'complete' } }),
      )
    })
    expect(
      await screen.findByText(
        zhCN.mfa.enroll.enrolled.replace(
          '{{factor}}',
          zhCN.mfa.factor.kind.totp,
        ),
      ),
    ).toBeTruthy()
  })

  it('holds the enrollment panel’s controls unavailable while another write in the section is in flight', async () => {
    const heldPatch = deferred<Response>()
    let renamed = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(200, methodsDoc(['totp', 'webauthn']))
      }
      if (call.method === 'GET' && call.path === FACTORS_PATH) {
        return jsonResponse(200, {
          factors: [
            passkey({
              id: 'factor-b',
              label: renamed ? 'New backup' : 'Backup key',
            }),
          ],
        })
      }
      if (call.method === 'POST' && call.path === FLOW_START) {
        return jsonResponse(200, enrollCodeStep())
      }
      if (
        call.method === 'PATCH' &&
        call.path === '/api/v1/auth/factors/factor-b'
      ) {
        return heldPatch.promise.then((answer) => {
          renamed = true
          return answer
        })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { rig } = mount(respond)
    const user = userEvent.setup()

    await openEnrollment(user, zhCN.mfa.factor.kind.totp)
    await screen.findByLabelText(zhCN.flowStep.codeLabel)
    const field = await openRenameEditor(user, 'Backup key')
    await user.clear(field)
    await user.type(field, 'New backup')
    const save = screen.getByRole('button', { name: zhCN.mfa.renameSave })
    await user.click(save)
    await waitFor(() => expect(save).toHaveAttribute('aria-busy', 'true'))

    const panel = screen.getByRole('region', {
      name: enrollTitleOf(zhCN.mfa.factor.kind.totp),
    })
    const confirm = within(panel).getByRole('button', {
      name: zhCN.mfa.enroll.confirmLabel,
    })
    expect(confirm).toHaveAttribute('aria-disabled', 'true')
    expect(confirm).not.toHaveAttribute('aria-busy')
    expect(confirm).not.toHaveAccessibleDescription()
    const cancel = within(panel).getByRole('button', {
      name: zhCN.mfa.enroll.cancelLabel,
    })
    expect(cancel).toHaveAttribute('aria-disabled', 'true')
    expect(cancel).not.toHaveAttribute('aria-busy')
    expect(cancel).not.toHaveAccessibleDescription()

    const answers = () =>
      rig.calls.filter(
        (call) => call.method === 'POST' && call.path === FLOW_ANSWER,
      ).length
    await user.click(confirm)
    confirm.focus()
    await user.keyboard('{Enter}')
    await user.click(cancel)
    expect(answers()).toBe(0)
    expect(
      screen.getByRole('region', {
        name: enrollTitleOf(zhCN.mfa.factor.kind.totp),
      }),
    ).toBeTruthy()

    await act(async () => {
      heldPatch.resolve(
        jsonResponse(200, passkey({ id: 'factor-b', label: 'New backup' })),
      )
    })
    expect(
      await screen.findByText(
        zhCN.mfa.renamed.replace('{{factor}}', 'New backup'),
      ),
    ).toBeTruthy()
  })

  it('holds the enrollment retry and cancel unavailable while another write in the section is in flight', async () => {
    const heldPatch = deferred<Response>()
    let renamed = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(200, methodsDoc(['totp', 'webauthn']))
      }
      if (call.method === 'GET' && call.path === FACTORS_PATH) {
        return jsonResponse(200, {
          factors: [
            passkey({
              id: 'factor-b',
              label: renamed ? 'New backup' : 'Backup key',
            }),
          ],
        })
      }
      if (call.method === 'POST' && call.path === FLOW_START) {
        return errorResponse(400, 'authn.bad_request')
      }
      if (
        call.method === 'PATCH' &&
        call.path === '/api/v1/auth/factors/factor-b'
      ) {
        return heldPatch.promise.then((answer) => {
          renamed = true
          return answer
        })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { rig } = mount(respond)
    const user = userEvent.setup()

    await openEnrollment(user, zhCN.mfa.factor.kind.totp)
    await screen.findByText(zhCN.errors.authn.bad_request)
    const retry = screen.getByRole('button', { name: zhCN.mfa.retry })
    const cancel = screen.getByRole('button', {
      name: zhCN.mfa.enroll.cancelLabel,
    })

    const field = await openRenameEditor(user, 'Backup key')
    await user.clear(field)
    await user.type(field, 'New backup')
    const save = screen.getByRole('button', { name: zhCN.mfa.renameSave })
    await user.click(save)
    await waitFor(() => expect(save).toHaveAttribute('aria-busy', 'true'))

    for (const control of [retry, cancel]) {
      expect(control).toHaveAttribute('aria-disabled', 'true')
      expect(control).not.toHaveAttribute('aria-busy')
      expect(control).not.toHaveAccessibleDescription()
    }
    const starts = () =>
      rig.calls.filter(
        (call) => call.method === 'POST' && call.path === FLOW_START,
      ).length
    await user.click(retry)
    retry.focus()
    await user.keyboard('{Enter}')
    await user.click(cancel)
    expect(starts()).toBe(1)
    expect(screen.getByRole('button', { name: zhCN.mfa.retry })).toBeTruthy()

    await act(async () => {
      heldPatch.resolve(
        jsonResponse(200, passkey({ id: 'factor-b', label: 'New backup' })),
      )
    })
    expect(
      await screen.findByText(
        zhCN.mfa.renamed.replace('{{factor}}', 'New backup'),
      ),
    ).toBeTruthy()
  })

  it('lands focus on the next row’s remove action when a removal takes the focused row away', async () => {
    const heldRefetch = deferred<Response>()
    let factorsRead = 0
    let removed = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(200, methodsDoc(['webauthn']))
      }
      if (call.method === 'GET' && call.path === FACTORS_PATH) {
        factorsRead += 1
        if (factorsRead === 1) {
          return jsonResponse(200, {
            factors: [
              passkey({ id: 'factor-a', label: 'Laptop key' }),
              passkey({ id: 'factor-b', label: 'Backup key' }),
            ],
          })
        }
        return heldRefetch.promise
      }
      if (
        call.method === 'DELETE' &&
        call.path === '/api/v1/auth/factors/factor-a'
      ) {
        removed = true
        return new Response(null, { status: 204 })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()

    const dialog = await openRemoveConfirm(user, 'Laptop key')
    await user.click(
      within(dialog).getByRole('button', {
        name: zhCN.mfa.removeConfirm.confirmLabel,
      }),
    )
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    const removeA = within(factorRow('Laptop key')).getByRole('button', {
      name: zhCN.mfa.remove,
    })
    await waitFor(() => expect(removeA).toHaveFocus())

    await act(async () => {
      heldRefetch.resolve(
        jsonResponse(200, {
          factors: [passkey({ id: 'factor-b', label: 'Backup key' })],
        }),
      )
    })
    await waitFor(() =>
      expect(screen.queryByText('Laptop key')).toBeNull(),
    )
    expect(removed).toBe(true)
    expect(
      within(factorRow('Backup key')).getByRole('button', {
        name: zhCN.mfa.remove,
      }),
    ).toHaveFocus()
  })

  it('lands focus on the previous row’s remove action when the last row goes', async () => {
    const heldRefetch = deferred<Response>()
    let factorsRead = 0
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(200, methodsDoc(['webauthn']))
      }
      if (call.method === 'GET' && call.path === FACTORS_PATH) {
        factorsRead += 1
        if (factorsRead === 1) {
          return jsonResponse(200, {
            factors: [
              passkey({ id: 'factor-a', label: 'Laptop key' }),
              passkey({ id: 'factor-b', label: 'Backup key' }),
            ],
          })
        }
        return heldRefetch.promise
      }
      if (
        call.method === 'DELETE' &&
        call.path === '/api/v1/auth/factors/factor-b'
      ) {
        return new Response(null, { status: 204 })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()

    const dialog = await openRemoveConfirm(user, 'Backup key')
    await user.click(
      within(dialog).getByRole('button', {
        name: zhCN.mfa.removeConfirm.confirmLabel,
      }),
    )
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())

    await act(async () => {
      heldRefetch.resolve(
        jsonResponse(200, {
          factors: [passkey({ id: 'factor-a', label: 'Laptop key' })],
        }),
      )
    })
    await waitFor(() =>
      expect(screen.queryByText('Backup key')).toBeNull(),
    )
    expect(
      within(factorRow('Laptop key')).getByRole('button', {
        name: zhCN.mfa.remove,
      }),
    ).toHaveFocus()
  })

  it('lands focus on the section container when the removed row’s kind group goes away', async () => {
    const heldRefetch = deferred<Response>()
    let factorsRead = 0
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(200, methodsDoc(['totp']))
      }
      if (call.method === 'GET' && call.path === FACTORS_PATH) {
        factorsRead += 1
        if (factorsRead === 1) {
          return jsonResponse(200, {
            factors: [passkey({ id: 'factor-a', label: 'Laptop key' })],
          })
        }
        return heldRefetch.promise
      }
      if (
        call.method === 'DELETE' &&
        call.path === '/api/v1/auth/factors/factor-a'
      ) {
        return new Response(null, { status: 204 })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()

    const dialog = await openRemoveConfirm(user, 'Laptop key')
    await user.click(
      within(dialog).getByRole('button', {
        name: zhCN.mfa.removeConfirm.confirmLabel,
      }),
    )
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())

    await act(async () => {
      heldRefetch.resolve(jsonResponse(200, { factors: [] }))
    })
    await waitFor(() =>
      expect(screen.queryByText('Laptop key')).toBeNull(),
    )
    expect(
      screen.getByRole('group', { name: zhCN.mfa.title }),
    ).toHaveFocus()
  })

  it('does not move focus when the reader already put it elsewhere before the row goes', async () => {
    const heldRefetch = deferred<Response>()
    let factorsRead = 0
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(200, methodsDoc(['webauthn', 'totp']))
      }
      if (call.method === 'GET' && call.path === FACTORS_PATH) {
        factorsRead += 1
        if (factorsRead === 1) {
          return jsonResponse(200, {
            factors: [
              passkey({ id: 'factor-a', label: 'Laptop key' }),
              passkey({ id: 'factor-b', label: 'Backup key' }),
            ],
          })
        }
        return heldRefetch.promise
      }
      if (
        call.method === 'DELETE' &&
        call.path === '/api/v1/auth/factors/factor-a'
      ) {
        return new Response(null, { status: 204 })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()

    const dialog = await openRemoveConfirm(user, 'Laptop key')
    await user.click(
      within(dialog).getByRole('button', {
        name: zhCN.mfa.removeConfirm.confirmLabel,
      }),
    )
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())

    const addTotp = screen.getByRole('button', {
      name: addNameOf(zhCN.mfa.factor.kind.totp),
    })
    addTotp.focus()
    expect(addTotp).toHaveFocus()

    await act(async () => {
      heldRefetch.resolve(
        jsonResponse(200, {
          factors: [passkey({ id: 'factor-b', label: 'Backup key' })],
        }),
      )
    })
    await waitFor(() =>
      expect(screen.queryByText('Laptop key')).toBeNull(),
    )
    expect(addTotp).toHaveFocus()
    expect(
      within(factorRow('Backup key')).getByRole('button', {
        name: zhCN.mfa.remove,
      }),
    ).not.toHaveFocus()
  })

  it('keeps focus inside another role=dialog node while the removal lands', async () => {
    const heldRefetch = deferred<Response>()
    let factorsRead = 0
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(200, methodsDoc(['webauthn']))
      }
      if (call.method === 'GET' && call.path === FACTORS_PATH) {
        factorsRead += 1
        if (factorsRead === 1) {
          return jsonResponse(200, {
            factors: [
              passkey({ id: 'factor-a', label: 'Laptop key' }),
              passkey({ id: 'factor-b', label: 'Backup key' }),
            ],
          })
        }
        return heldRefetch.promise
      }
      if (
        call.method === 'DELETE' &&
        call.path === '/api/v1/auth/factors/factor-a'
      ) {
        return new Response(null, { status: 204 })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()

    const dialog = await openRemoveConfirm(user, 'Laptop key')
    await user.click(
      within(dialog).getByRole('button', {
        name: zhCN.mfa.removeConfirm.confirmLabel,
      }),
    )
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())

    const shell = document.createElement('div')
    shell.setAttribute('role', 'dialog')
    shell.setAttribute('aria-label', 'shell drawer')
    shell.tabIndex = -1
    document.body.append(shell)
    shell.focus()
    expect(shell).toHaveFocus()
    try {
      await act(async () => {
        heldRefetch.resolve(
          jsonResponse(200, {
            factors: [passkey({ id: 'factor-b', label: 'Backup key' })],
          }),
        )
      })
      await waitFor(() => expect(screen.queryByText('Laptop key')).toBeNull())
      expect(shell).toHaveFocus()
      expect(
        within(factorRow('Backup key')).getByRole('button', {
          name: zhCN.mfa.remove,
        }),
      ).not.toHaveFocus()
    } finally {
      shell.remove()
    }
  })

  it('clears a settled removal’s landing when the list still carries the row', async () => {
    let factorsRead = 0
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(200, methodsDoc(['webauthn', 'totp']))
      }
      if (call.method === 'GET' && call.path === FACTORS_PATH) {
        factorsRead += 1
        return jsonResponse(200, {
          factors:
            factorsRead <= 2
              ? [
                  passkey({ id: 'factor-a', label: 'Laptop key' }),
                  passkey({ id: 'factor-b', label: 'Backup key' }),
                ]
              : [passkey({ id: 'factor-b', label: 'Backup key' })],
        })
      }
      if (
        call.method === 'DELETE' &&
        call.path === '/api/v1/auth/factors/factor-a'
      ) {
        return new Response(null, { status: 204 })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { queryClient } = mount(respond)
    const user = userEvent.setup()

    const dialog = await openRemoveConfirm(user, 'Laptop key')
    await user.click(
      within(dialog).getByRole('button', {
        name: zhCN.mfa.removeConfirm.confirmLabel,
      }),
    )
    expect(
      await screen.findByText(
        zhCN.mfa.removed.replace('{{factor}}', 'Laptop key'),
      ),
    ).toBeTruthy()
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    const removeA = within(factorRow('Laptop key')).getByRole('button', {
      name: zhCN.mfa.remove,
    })
    await waitFor(() => expect(removeA).not.toHaveAttribute('aria-busy'))
    ;(document.activeElement as HTMLElement | null)?.blur()
    expect(document.activeElement).toBe(document.body)

    await act(async () => {
      await queryClient.invalidateQueries({
        queryKey: getAuthnMfaFactorsQueryKey(),
      })
    })
    await waitFor(() =>
      expect(screen.queryByText('Laptop key')).toBeNull(),
    )
    expect(document.activeElement).toBe(document.body)
  })

  it('lands focus on the row’s rename action when a landed rename closes its editor', async () => {
    let renamed = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(200, methodsDoc(['webauthn']))
      }
      if (call.method === 'GET' && call.path === FACTORS_PATH) {
        return jsonResponse(200, {
          factors: [
            passkey({
              id: 'factor-a',
              label: renamed ? 'New name' : 'Old name',
            }),
          ],
        })
      }
      if (
        call.method === 'PATCH' &&
        call.path === '/api/v1/auth/factors/factor-a'
      ) {
        renamed = true
        return jsonResponse(200, passkey({ id: 'factor-a', label: 'New name' }))
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
      await screen.findByText(
        zhCN.mfa.renamed.replace('{{factor}}', 'New name'),
      ),
    ).toBeTruthy()
    expect(screen.queryByLabelText(zhCN.mfa.renameLabel)).toBeNull()
    expect(
      within(factorRow('New name')).getByRole('button', {
        name: zhCN.mfa.rename,
      }),
    ).toHaveFocus()
  })

  it('lands focus on the row’s rename action when the reader cancels the editor', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(200, methodsDoc(['webauthn']))
      }
      if (call.method === 'GET' && call.path === FACTORS_PATH) {
        return jsonResponse(200, {
          factors: [passkey({ id: 'factor-a', label: 'Old name' })],
        })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()

    await openRenameEditor(user, 'Old name')
    await user.click(
      screen.getByRole('button', { name: zhCN.mfa.renameCancel }),
    )
    expect(screen.queryByLabelText(zhCN.mfa.renameLabel)).toBeNull()
    await waitFor(() =>
      expect(
        within(factorRow('Old name')).getByRole('button', {
          name: zhCN.mfa.rename,
        }),
      ).toHaveFocus(),
    )
  })

  it('lands focus on the next row’s rename action when a rename finds its factor gone', async () => {
    let gone = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(200, methodsDoc(['webauthn']))
      }
      if (call.method === 'GET' && call.path === FACTORS_PATH) {
        return jsonResponse(200, {
          factors: gone
            ? [passkey({ id: 'factor-b', label: 'Backup key' })]
            : [
                passkey({ id: 'factor-a', label: 'Old name' }),
                passkey({ id: 'factor-b', label: 'Backup key' }),
              ],
        })
      }
      if (
        call.method === 'PATCH' &&
        call.path === '/api/v1/auth/factors/factor-a'
      ) {
        gone = true
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
    expect(screen.queryByLabelText(zhCN.mfa.renameLabel)).toBeNull()
    await waitFor(() =>
      expect(
        within(factorRow('Backup key')).getByRole('button', {
          name: zhCN.mfa.rename,
        }),
      ).toHaveFocus(),
    )
  })

  it('does not move focus when the reader left the rename editor before it closed', async () => {
    const held = deferred<Response>()
    let renamed = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(200, methodsDoc(['webauthn', 'totp']))
      }
      if (call.method === 'GET' && call.path === FACTORS_PATH) {
        return jsonResponse(200, {
          factors: [
            passkey({
              id: 'factor-a',
              label: renamed ? 'New name' : 'Old name',
            }),
            passkey({ id: 'factor-b', label: 'Backup key' }),
          ],
        })
      }
      if (
        call.method === 'PATCH' &&
        call.path === '/api/v1/auth/factors/factor-a'
      ) {
        return held.promise.then((answer) => {
          renamed = true
          return answer
        })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()

    const field = await openRenameEditor(user, 'Old name')
    await user.clear(field)
    await user.type(field, 'New name')
    await user.click(screen.getByRole('button', { name: zhCN.mfa.renameSave }))

    const addTotp = screen.getByRole('button', {
      name: addNameOf(zhCN.mfa.factor.kind.totp),
    })
    addTotp.focus()
    expect(addTotp).toHaveFocus()

    await act(async () => {
      held.resolve(
        jsonResponse(200, passkey({ id: 'factor-a', label: 'New name' })),
      )
    })
    expect(
      await screen.findByText(
        zhCN.mfa.renamed.replace('{{factor}}', 'New name'),
      ),
    ).toBeTruthy()
    expect(addTotp).toHaveFocus()
  })

  it('lands focus on the method’s add action when a landed enrollment closes its panel', async () => {
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
        return jsonResponse(200, enrollCodeStep())
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
    mount(respond)
    const user = userEvent.setup()

    await openEnrollment(user, zhCN.mfa.factor.kind.totp)
    await user.type(
      await screen.findByLabelText(zhCN.flowStep.codeLabel),
      '123456',
    )
    await confirmEnrollment(user)

    expect(
      await screen.findByText(
        zhCN.mfa.enroll.enrolled.replace(
          '{{factor}}',
          zhCN.mfa.factor.kind.totp,
        ),
      ),
    ).toBeTruthy()
    expect(screen.queryByLabelText(zhCN.flowStep.codeLabel)).toBeNull()
    expect(
      screen.getByRole('button', { name: replaceName() }),
    ).toHaveFocus()
  })

  it('lands focus on the method’s add action when the reader cancels the enrollment panel', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(200, methodsDoc(['totp']))
      }
      if (call.method === 'GET' && call.path === FACTORS_PATH) {
        return jsonResponse(200, { factors: [] })
      }
      if (call.method === 'POST' && call.path === FLOW_START) {
        return jsonResponse(200, enrollCodeStep())
      }
      if (call.method === 'DELETE' && call.path === FLOW_CURRENT) {
        return new Response(null, { status: 204 })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()

    await openEnrollment(user, zhCN.mfa.factor.kind.totp)
    await screen.findByLabelText(zhCN.flowStep.codeLabel)
    await user.click(
      screen.getByRole('button', { name: zhCN.mfa.enroll.cancelLabel }),
    )

    const add = await screen.findByRole('button', {
      name: addNameOf(zhCN.mfa.factor.kind.totp),
    })
    await waitFor(() => expect(add).toHaveFocus())
  })

  it('lands focus on the method’s add action when the reader cancels the step-up an enrollment opened', async () => {
    captureRefreshWarning()
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(200, methodsDoc(['totp', 'webauthn']))
      }
      if (call.method === 'GET' && call.path === FACTORS_PATH) {
        return jsonResponse(200, {
          factors: [factor({ id: 'factor-1', kind: 'totp' })],
        })
      }
      if (call.method === 'POST' && call.path === FLOW_START) {
        const body = call.body as { purpose: string }
        if (body.purpose === 'enroll') {
          return errorResponse(401, 'authn.insufficient_user_authentication')
        }
        return jsonResponse(200, {
          seq: 1,
          state: 'second',
          next: {
            kind: 'factor',
            factors: [{ id: 'factor-1', kind: 'totp', label: 'App' }],
          },
        })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()

    await openEnrollment(user, zhCN.mfa.factor.kind.webauthn)
    const dialog = await screen.findByRole('dialog')
    await user.click(
      within(dialog).getByRole('button', {
        name: zhCN.mfa.stepUp.cancelLabel,
      }),
    )

    const add = await screen.findByRole('button', {
      name: addNameOf(zhCN.mfa.factor.kind.webauthn),
    })
    await waitFor(() => expect(add).toHaveFocus())
  })

  it('lands focus on its row’s remove action when a cancelled step-up drops focus to the body', async () => {
    captureRefreshWarning({ expected: true })
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(200, methodsDoc(['totp']))
      }
      if (call.method === 'GET' && call.path === FACTORS_PATH) {
        return jsonResponse(200, {
          factors: [passkey({ id: 'factor-a', label: 'Laptop key' })],
        })
      }
      if (
        call.method === 'DELETE' &&
        call.path === '/api/v1/auth/factors/factor-a'
      ) {
        return errorResponse(401, 'authn.insufficient_user_authentication')
      }
      if (call.method === 'POST' && call.path === FLOW_START) {
        return jsonResponse(200, {
          seq: 1,
          state: 'second',
          next: {
            kind: 'factor',
            factors: [{ id: 'factor-a', kind: 'webauthn', label: 'Laptop key' }],
          },
        })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()

    await waitFor(() => expect(factorRow('Laptop key')).toBeTruthy())
    expect(document.activeElement).toBe(document.body)
    fireEvent.click(
      within(factorRow('Laptop key')).getByRole('button', {
        name: zhCN.mfa.remove,
      }),
    )
    const confirm = await screen.findByRole('dialog')
    await user.click(
      within(confirm).getByRole('button', {
        name: zhCN.mfa.removeConfirm.confirmLabel,
      }),
    )
    const challenge = await screen.findByRole('dialog')
    await user.click(
      within(challenge).getByRole('button', {
        name: zhCN.mfa.stepUp.cancelLabel,
      }),
    )

    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    const remove = within(factorRow('Laptop key')).getByRole('button', {
      name: zhCN.mfa.remove,
    })
    await waitFor(() => expect(remove).toHaveFocus())
  })

  it('lands focus on the groups container when a removal empties a section with no enrollable method', async () => {
    const heldRefetch = deferred<Response>()
    let factorsRead = 0
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(200, methodsDoc([]))
      }
      if (call.method === 'GET' && call.path === FACTORS_PATH) {
        factorsRead += 1
        if (factorsRead === 1) {
          return jsonResponse(200, {
            factors: [passkey({ id: 'factor-a', label: 'Laptop key' })],
          })
        }
        return heldRefetch.promise
      }
      if (
        call.method === 'DELETE' &&
        call.path === '/api/v1/auth/factors/factor-a'
      ) {
        return new Response(null, { status: 204 })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()

    const dialog = await openRemoveConfirm(user, 'Laptop key')
    await user.click(
      within(dialog).getByRole('button', {
        name: zhCN.mfa.removeConfirm.confirmLabel,
      }),
    )
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())

    await act(async () => {
      heldRefetch.resolve(jsonResponse(200, { factors: [] }))
    })
    await waitFor(() =>
      expect(screen.queryByText('Laptop key')).toBeNull(),
    )
    const container = screen.getByRole('group', { name: zhCN.mfa.title })
    expect(container).toHaveTextContent(zhCN.mfa.empty)
    expect(container).toHaveFocus()
  })

  it('does not move focus when the reader left the enrollment panel before it closed', async () => {
    const held = deferred<Response>()
    let enrolled = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(200, methodsDoc(['totp', 'webauthn']))
      }
      if (call.method === 'GET' && call.path === FACTORS_PATH) {
        return jsonResponse(200, {
          factors: enrolled ? [factor({ id: 'factor-new' })] : [],
        })
      }
      if (call.method === 'POST' && call.path === FLOW_START) {
        return jsonResponse(200, enrollCodeStep())
      }
      if (call.method === 'POST' && call.path === FLOW_ANSWER) {
        const body = call.body as { seq: number; step: string }
        if (body.step === 'enroll') {
          return held.promise.then((answer) => {
            enrolled = true
            return answer
          })
        }
        return jsonResponse(200, { state: 'enrolled' })
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

    const addWebauthn = screen.getByRole('button', {
      name: addNameOf(zhCN.mfa.factor.kind.webauthn),
    })
    addWebauthn.focus()
    expect(addWebauthn).toHaveFocus()

    await act(async () => {
      held.resolve(
        jsonResponse(200, {
          seq: 2,
          state: 'ready',
          next: { kind: 'complete' },
        }),
      )
    })
    expect(
      await screen.findByText(
        zhCN.mfa.enroll.enrolled.replace(
          '{{factor}}',
          zhCN.mfa.factor.kind.totp,
        ),
      ),
    ).toBeTruthy()
    expect(addWebauthn).toHaveFocus()
  })
})
