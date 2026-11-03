/**
 * The host's own tenant list: the caller tenant read's answer as the switcher's
 * list and the announcement's alias source.
 *
 * Two layers are held here. The module store is the one place the list lives:
 * a successful read replaces it, carrying each row's host-answered `kind`, a
 * failed one keeps the rows already on hand and marks the list unsettled so a
 * failure is never read as an empty answer, and an identity change or a
 * sign-out clears it. The switcher renders every row of that list, labels a row
 * whose `kind` is `personal` with the personal-workspace copy, and leaves the
 * current row unselectable. Another account's personal tenant comes back as
 * `business`, so it renders its alias.
 */

import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
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
  getCurrentTenantAlias,
  getCurrentTenantId,
  setCurrentTenantId,
  setTenantAliases,
  TenantMenu,
} from './chrome.js'
import type { DemoTenant } from './chrome.js'
import demoEnUS from './locales/en-US.json' with { type: 'json' }
import { DEMO_NAMESPACE, demoResources } from './resources.js'
import {
  clearSelfTenants,
  getSelfTenantState,
  loadSelfTenants,
  personalTenantId,
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
  setTenantAliases(new Map())
  setCurrentTenantId(null)
})

afterEach(() => {
  clearSelfTenants()
  setTenantAliases(new Map())
  setCurrentTenantId(null)
})

describe('the caller tenant list store', () => {
  it('replaces the list and marks it settled on a successful read', async () => {
    bindSelf(() => ({
      tenants: [
        { id: 'acme', alias: 'acme-inc', kind: 'business' },
        { id: 'personal-account-1', alias: 'my-space', kind: 'personal' },
      ],
    }))

    await loadSelfTenants()

    // The read's kind is carried through unchanged; the switcher and the
    // default settlement branch on it.
    expect(getSelfTenantState()).toEqual({
      tenants: [
        { id: 'acme', alias: 'acme-inc', kind: 'business' },
        { id: 'personal-account-1', alias: 'my-space', kind: 'personal' },
      ],
      settled: true,
      failed: false,
    })
  })

  it('keeps the settled rows and marks the list unsettled when a read fails', async () => {
    bindSelf(() => ({
      tenants: [{ id: 'acme', alias: 'acme-inc', kind: 'business' }],
    }))
    await loadSelfTenants()

    bindSelf(() => {
      throw Object.assign(new Error('the read failed'), { status: 503 })
    })
    await loadSelfTenants()

    // The failure is not an empty answer: the rows already on hand stay, and
    // the unsettled flag is what keeps the personal-tenant ensure from reading
    // this failure as "no memberships".
    expect(getSelfTenantState()).toEqual({
      tenants: [{ id: 'acme', alias: 'acme-inc', kind: 'business' }],
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
            tenants: [{ id: 'acme', alias: 'acme-inc', kind: 'business' }],
          }
    })

    const olderRead = loadSelfTenants()
    const newerRead = loadSelfTenants()
    await newerRead
    expect(getSelfTenantState()).toEqual({
      tenants: [{ id: 'acme', alias: 'acme-inc', kind: 'business' }],
      settled: true,
      failed: false,
    })

    // The older read fails after the newer one answered. Its failure belongs
    // to a superseded attempt, so it must not turn the settled list into a
    // failure the retry control would show again.
    rejectOlder(new Error('the older read failed'))
    await olderRead
    expect(getSelfTenantState()).toEqual({
      tenants: [{ id: 'acme', alias: 'acme-inc', kind: 'business' }],
      settled: true,
      failed: false,
    })
  })

  it('clears the rows and the settled flag', async () => {
    bindSelf(() => ({ tenants: [{ id: 'acme', alias: 'acme-inc' }] }))
    await loadSelfTenants()

    clearSelfTenants()

    expect(getSelfTenantState()).toEqual({
      tenants: [],
      settled: false,
      failed: false,
    })
  })

  it('announces a self-list row alias on the request surface', async () => {
    bindSelf(() => ({ tenants: [{ id: 'globex', alias: 'globex-renamed' }] }))
    await loadSelfTenants()
    setCurrentTenantId('globex')

    // The id and the alias diverge after a rename; the request surface carries
    // the alias and never the id.
    expect(getCurrentTenantAlias()).toBe('globex-renamed')
  })
})

describe('the derived personal tenant identifier', () => {
  it('is the server prefix plus the account identifier, exactly', () => {
    expect(personalTenantId('account-1')).toBe('personal-account-1')
  })
})

/** The rows the host offers: the caller's own space, another account's
 * personal tenant, and a business tenant. The read answers `kind` per row, and
 * the host's resolver marks only the caller's own space personal; another
 * account's personal tenant comes back as business and renders its alias. The
 * caller's own row carries an identifier no derivation would produce, so a
 * label driven by the identifier rather than by `kind` fails here. */
const OFFERED: readonly DemoTenant[] = [
  { id: 'my-space', alias: 'my-space', name: 'my-space', kind: 'personal' },
  {
    id: 'personal-account-2',
    alias: 'other-space',
    name: 'other-space',
    kind: 'business',
  },
  { id: 'acme', alias: 'acme', name: 'acme', kind: 'business' },
]

/** Renders the tenant switcher under the production stack. */
function renderTenantMenu(props: {
  readonly tenants: readonly DemoTenant[]
  readonly tenantId: string | null
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
    <TenantMenu
      tenants={props.tenants}
      tenantId={props.tenantId}
      onTenantSelect={() => undefined}
    />
  )
  render(
    <SpeedAppProviders i18n={i18n} queryClient={queryClient}>
      <AppServicesProvider services={services}>{view}</AppServicesProvider>
    </SpeedAppProviders>,
  )
}

describe('the tenant switcher over the self list', () => {
  it('keeps every row and labels the row the read answers as personal', async () => {
    const user = userEvent.setup()
    renderTenantMenu({
      tenants: OFFERED,
      tenantId: 'acme',
    })

    await user.click(screen.getByRole('button', { name: 'acme' }))
    const items = await screen.findAllByRole('menuitem')

    // Every tenant the account holds a membership in is offered. The row the
    // read answers as personal carries the personal-workspace copy; another
    // account's personal tenant came back as business, so it keeps its alias.
    expect(items.map((item) => item.textContent)).toEqual([
      demoEnUS.workspace.personal,
      'other-space',
      'acme',
    ])
  })

  it('leaves the current tenant in the list but not selectable', async () => {
    const user = userEvent.setup()
    renderTenantMenu({
      tenants: OFFERED,
      tenantId: 'acme',
    })

    await user.click(screen.getByRole('button', { name: 'acme' }))
    const current = await screen.findByRole('menuitem', { name: 'acme' })

    expect(current).toHaveAttribute('aria-disabled', 'true')
  })

  it('adds a current tenant the list does not hold, named from the roster cache', async () => {
    // A platform staff member works in a tenant they are not a member of (a
    // route named it and the roster read cached its alias): the caller tenant
    // list does not hold the row, so only the roster's alias cache can name
    // it. Without the added row the bar could neither show nor leave that
    // tenant.
    setTenantAliases(new Map([['globex', 'globex-renamed']]))
    setCurrentTenantId('globex')
    bindRequestFn((async (path: string) => {
      if (path === SELF_PATH) {
        return { tenants: [] }
      }
      if (path === '/api/v1/system/tenants') {
        return {
          tenants: [
            { id: 'globex', alias: 'globex-renamed', status: 'active' },
          ],
        }
      }
      throw new Error(`unexpected read: ${path}`)
    }) as RequestFn)

    renderDemoApp({
      session: createTestSession(authenticatedSnapshot({ system: ['tenant.read'] })),
    })

    expect(
      await screen.findByRole('button', { name: 'globex-renamed' }),
    ).toBeInTheDocument()
    expect(getCurrentTenantId()).toBe('globex')
  })
})
