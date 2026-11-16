/**
 * The host's own tenant list: the caller tenant read's answer as the switcher's
 * list and the announcement's name source.
 *
 * Two layers are held here. The module store is the one place the list lives:
 * a successful read replaces it, carrying each row's host-answered `kind`, a
 * failed one keeps the rows already on hand and marks the list unsettled so a
 * failure is never read as an empty answer, and an identity change or a
 * sign-out clears it. The switcher renders every row of that list, labels a row
 * whose `kind` is `personal` with the personal-organization copy, and leaves the
 * current row unselectable. Another account's personal tenant comes back as
 * `business`, so it renders its request-surface name.
 */

import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import MenuList from '@mui/material/MenuList'
import type { ReactElement } from 'react'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import type { RequestFn } from '@speed/api-client'
import { bindRequestFn } from '@speed/api-sdk/runtime'
import {
  createQueryClient,
  createSpeedI18n,
  SpeedAppProviders,
} from '@speed/product-shell/bootstrap'
import type { SpeedAppServices } from '@speed/product-shell/bootstrap'
import { TENANCY_UI_NAMESPACE, tenancyUiResources } from '@speed/tenancy-ui'
import { AppServicesProvider } from './app-services.js'
import {
  getCurrentTenantName,
  getCurrentTenant,
  setCurrentTenant,
  setTenantNames,
  OrganizationMenuEntry,
} from './chrome.js'
import type { DemoTenant } from './chrome.js'
import demoEnUS from './locales/en-US.json' with { type: 'json' }
import { DEMO_NAMESPACE, demoResources } from './resources.js'
import {
  clearSelfTenants,
  getSelfTenantState,
  loadSelfTenants,
  selfTenantDisplayName,
} from './tenant-self.js'
import { createHarnessClient, renderDemoApp } from './test-utils/render.js'
import {
  authenticatedSnapshot,
  createTestSession,
} from './test-utils/session.js'

const SELF_PATH = '/api/v1/auth/me/tenants'

/** Binds a request function that answers the caller tenant read with the
 * given body (or throws), failing loudly on every other path. */
function bindSelf(read: () => unknown): void {
  bindRequestFn((async (path: string) => {
    if (path === SELF_PATH) {
      return await read()
    }
    throw new Error(`unexpected read: ${path}`)
  }) as RequestFn)
}

beforeEach(() => {
  clearSelfTenants()
  setTenantNames(new Map())
  setCurrentTenant(null)
})

afterEach(() => {
  clearSelfTenants()
  setTenantNames(new Map())
  setCurrentTenant(null)
})

describe('the caller tenant list store', () => {
  it('replaces the list and marks it settled on a successful read', async () => {
    bindSelf(() => ({
      tenants: [
        { name: 'acme-inc', display_name: '', kind: 'business' },
        { name: 'my-space', display_name: '', kind: 'personal' },
      ],
    }))

    await loadSelfTenants()

    // The read's kind is carried through unchanged; the switcher and the
    // default settlement branch on it.
    expect(getSelfTenantState()).toEqual({
      tenants: [
        { name: 'acme-inc', displayName: '', kind: 'business' },
        { name: 'my-space', displayName: '', kind: 'personal' },
      ],
      settled: true,
      failed: false,
    })
  })

  it('carries the display name and falls back to the request-surface name when it is empty', async () => {
    bindSelf(() => ({
      tenants: [
        {
          name: 'acme-inc',
          display_name: 'Acme Incorporated',
          kind: 'business',
        },
        {
          name: 'my-space',
          display_name: '',
          kind: 'personal',
        },
      ],
    }))
    await loadSelfTenants()

    // A profile that names a display name carries it; an empty one is the
    // same fact as no name, so the accessor falls back to the request-surface name.
    expect(selfTenantDisplayName('acme-inc')).toBe('Acme Incorporated')
    expect(selfTenantDisplayName('my-space')).toBe('my-space')
    expect(selfTenantDisplayName('unknown')).toBeNull()
  })

  it('keeps the settled rows and marks the list unsettled when a read fails', async () => {
    bindSelf(() => ({
      tenants: [{ name: 'acme-inc', display_name: '', kind: 'business' }],
    }))
    await loadSelfTenants()

    bindSelf(() => {
      throw Object.assign(new Error('the read failed'), { status: 503 })
    })
    await loadSelfTenants()

    // The failure is not an empty answer: the rows already on hand stay, and
    // the unsettled flag keeps a failed read from settling a current tenant or
    // standing in for "no memberships".
    expect(getSelfTenantState()).toEqual({
      tenants: [{ name: 'acme-inc', displayName: '', kind: 'business' }],
      settled: false,
      failed: true,
    })
  })

  it('tells a read still in flight apart from a failed one', async () => {
    let resolveRead!: (value: unknown) => void
    bindSelf(
      () =>
        new Promise((resolve) => {
          resolveRead = resolve
        }),
    )

    const pending = loadSelfTenants()
    // Until the read answers the list is neither settled nor failed: the
    // identity-change gap waits for an outcome, and a read in flight is not
    // one.
    expect(getSelfTenantState()).toEqual({
      tenants: [],
      settled: false,
      failed: false,
    })

    resolveRead({ tenants: [] })
    await pending
    expect(getSelfTenantState()).toEqual({
      tenants: [],
      settled: true,
      failed: false,
    })
  })

  it('keeps the newest read when an older read fails after it settles', async () => {
    let rejectOlder!: (error: unknown) => void
    const older = new Promise((_resolve, reject) => {
      rejectOlder = reject
    })
    let calls = 0
    bindSelf(() => {
      calls += 1
      return calls === 1
        ? older
        : {
            tenants: [{ name: 'acme-inc', display_name: '', kind: 'business' }],
          }
    })

    const olderRead = loadSelfTenants()
    const newerRead = loadSelfTenants()
    await newerRead
    expect(getSelfTenantState()).toEqual({
      tenants: [{ name: 'acme-inc', displayName: '', kind: 'business' }],
      settled: true,
      failed: false,
    })

    // The older read fails after the newer one answered. Its failure belongs
    // to a superseded attempt, so it must not turn the settled list into a
    // failure the retry control would show again.
    rejectOlder(new Error('the older read failed'))
    await olderRead
    expect(getSelfTenantState()).toEqual({
      tenants: [{ name: 'acme-inc', displayName: '', kind: 'business' }],
      settled: true,
      failed: false,
    })
  })

  it('clears the rows and the settled flag', async () => {
    bindSelf(() => ({ tenants: [{ name: 'acme-inc' }] }))
    await loadSelfTenants()

    clearSelfTenants()

    expect(getSelfTenantState()).toEqual({
      tenants: [],
      settled: false,
      failed: false,
    })
  })

  it('announces a self-list row name on the request surface', async () => {
    bindSelf(() => ({ tenants: [{ name: 'globex-renamed' }] }))
    await loadSelfTenants()
    setCurrentTenant('globex-renamed')

    // The caller tenant list's row is addressed by its name; that is the value
    // the request surface announces.
    expect(getCurrentTenantName()).toBe('globex-renamed')
  })
})

/** The rows the host offers: the caller's own space, another account's
 * personal tenant, and a business tenant. The read answers `kind` per row, and
 * the host's resolver marks only the caller's own space personal; another
 * account's personal tenant comes back as business and renders its request-surface name. The
 * caller's own row carries an identifier no derivation would produce, so a
 * label driven by the identifier rather than by `kind` fails here. */
const OFFERED: readonly DemoTenant[] = [
  { id: 'my-space', name: 'my-space', kind: 'personal' },
  {
    id: 'other-space',
    name: 'other-space',
    kind: 'business',
  },
  { id: 'acme', name: 'acme', kind: 'business' },
]

/** Renders the account menu's organization group under the production stack. */
function renderTenantMenu(props: {
  readonly tenants: readonly DemoTenant[]
  readonly tenantName: string | null
}): void {
  const i18n = createSpeedI18n({
    supportedLanguages: ['zh-CN', 'en-US'],
    defaultLanguage: 'en-US',
    storage: null,
    urlParameterName: null,
    navigatorLanguages: [],
    namespaces: [
      { namespace: TENANCY_UI_NAMESPACE, resources: tenancyUiResources },
      { namespace: DEMO_NAMESPACE, resources: demoResources },
    ],
  })
  const queryClient = createQueryClient()
  const services: SpeedAppServices = {
    session: createTestSession(),
    api: createHarnessClient(),
    queryClient,
  }
  const view: ReactElement = (
    <MenuList>
      <OrganizationMenuEntry
        organizations={{
          tenants: props.tenants,
          tenantName: props.tenantName,
          onSelect: () => undefined,
        }}
        onClose={() => undefined}
      />
    </MenuList>
  )
  render(
    <SpeedAppProviders i18n={i18n} queryClient={queryClient}>
      <AppServicesProvider services={services}>{view}</AppServicesProvider>
    </SpeedAppProviders>,
  )
}

describe('the tenant switcher over the self list', () => {
  it('keeps every row and labels the row the read answers as personal', async () => {
    renderTenantMenu({
      tenants: OFFERED,
      tenantName: 'acme',
    })

    // The menu states the current organization in its first row and expands
    // to the others; both halves together are the full list.
    await userEvent
      .setup()
      .click(await screen.findByRole('menuitem', { name: 'acme' }))

    // Every tenant the account holds a membership in is offered. The row the
    // read answers as personal carries the personal-organization copy; another
    // account's personal tenant came back as business, so it keeps its request-surface name.
    expect(
      await screen.findByRole('menuitem', {
        name: demoEnUS.organization.personal,
      }),
    ).toBeInTheDocument()
    expect(
      screen.getByRole('menuitem', { name: 'other-space' }),
    ).toBeInTheDocument()
    // The current organization is context, not one more destination: the
    // expansion does not repeat it.
    expect(screen.getAllByRole('menuitem', { name: 'acme' })).toHaveLength(1)
  })

  it('leaves the current tenant in the list but not selectable', async () => {
    renderTenantMenu({
      tenants: OFFERED,
      tenantName: 'acme',
    })

    const user = userEvent.setup()
    const current = await screen.findByRole('menuitem', { name: 'acme' })
    // The current row is the expander, not a destination: clicking it reveals
    // the others, and no second row offers the current tenant to switch to.
    await user.click(current)
    expect(current).toHaveAttribute('aria-expanded', 'true')
    expect(screen.getAllByRole('menuitem', { name: 'acme' })).toHaveLength(1)
  })

  it('adds a current tenant the list does not hold, named from the roster cache', async () => {
    // A platform staff member works in a tenant they are not a member of (a
    // route named it and the roster read cached its name): the caller tenant
    // list does not hold the row, so only the roster's name cache can name
    // it. Without the added row the bar could neither show nor leave that
    // tenant.
    setTenantNames(new Map([['globex-renamed', 'Globex Renamed']]))
    setCurrentTenant('globex-renamed')
    bindRequestFn((async (path: string) => {
      if (path === SELF_PATH) {
        return { tenants: [] }
      }
      if (path === '/api/v1/system/tenants') {
        return {
          tenants: [
            {
              name: 'globex-renamed',
              display_name: 'Globex Renamed',
              kind: 'business',
              status: 'active',
            },
          ],
        }
      }
      throw new Error(`unexpected read: ${path}`)
    }) as RequestFn)

    renderDemoApp({
      session: createTestSession(authenticatedSnapshot({ system: ['tenant.read'] })),
    })

    const user = userEvent.setup()
    await user.click(await screen.findByTestId('user-menu-trigger'))
    expect(
      within(await screen.findByRole('menu')).getByText('Globex Renamed'),
    ).toBeInTheDocument()
    expect(getCurrentTenant()).toBe('globex-renamed')
  })
})
