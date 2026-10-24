/**
 * The host's own tenant list: the caller tenant read's answer as the switcher's
 * list and the announcement's alias source.
 *
 * Two layers are held here. The module store is the one place the list lives:
 * a successful read replaces it, a failed one keeps the rows already on hand
 * and marks the list unsettled so a failure is never read as an empty answer,
 * and an identity change or a sign-out clears it. The switcher renders every
 * row of that list, labels only the row whose identifier EXACTLY equals the
 * account's derived personal identifier with the personal-workspace copy (a
 * prefix test would mislabel another account's personal tenant), and leaves
 * the current row unselectable.
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
        { id: 'acme', alias: 'acme-inc' },
        { id: 'globex', alias: 'globex' },
      ],
    }))

    await loadSelfTenants()

    expect(getSelfTenantState()).toEqual({
      tenants: [
        { id: 'acme', alias: 'acme-inc' },
        { id: 'globex', alias: 'globex' },
      ],
      settled: true,
    })
  })

  it('keeps the settled rows and marks the list unsettled when a read fails', async () => {
    bindSelf(() => ({ tenants: [{ id: 'acme', alias: 'acme-inc' }] }))
    await loadSelfTenants()

    bindSelf(() => {
      throw Object.assign(new Error('the read failed'), { status: 503 })
    })
    await loadSelfTenants()

    // The failure is not an empty answer: the rows already on hand stay, and
    // the unsettled flag is what keeps the personal-tenant ensure from reading
    // this failure as "no memberships".
    expect(getSelfTenantState()).toEqual({
      tenants: [{ id: 'acme', alias: 'acme-inc' }],
      settled: false,
    })
  })

  it('clears the rows and the settled flag', async () => {
    bindSelf(() => ({ tenants: [{ id: 'acme', alias: 'acme-inc' }] }))
    await loadSelfTenants()

    clearSelfTenants()

    expect(getSelfTenantState()).toEqual({ tenants: [], settled: false })
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

/** Two tenants as the host offers them, plus two personal tenants: the
 * caller's own and another account's. */
const OFFERED: readonly DemoTenant[] = [
  { id: 'personal-account-1', alias: 'my-space', name: 'my-space' },
  { id: 'personal-account-2', alias: 'other-space', name: 'other-space' },
  { id: 'acme', alias: 'acme', name: 'acme' },
]

/** Renders the tenant switcher under the production stack. */
function renderTenantMenu(props: {
  readonly tenants: readonly DemoTenant[]
  readonly tenantId: string | null
  readonly personalTenantId: string | null
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
      personalTenantId={props.personalTenantId}
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
  it('keeps every row and labels only the exact derived personal row', async () => {
    const user = userEvent.setup()
    renderTenantMenu({
      tenants: OFFERED,
      tenantId: 'acme',
      personalTenantId: personalTenantId('account-1'),
    })

    await user.click(screen.getByRole('button', { name: 'acme' }))
    const items = await screen.findAllByRole('menuitem')

    // Every tenant the account holds a membership in is offered, with the
    // caller's own personal row carrying the personal-workspace copy and the
    // other account's personal row carrying its alias.
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
      personalTenantId: personalTenantId('account-1'),
    })

    await user.click(screen.getByRole('button', { name: 'acme' }))
    const current = await screen.findByRole('menuitem', { name: 'acme' })

    expect(current).toHaveAttribute('aria-disabled', 'true')
  })

  it('adds a current tenant the list does not hold, named from the roster cache', async () => {
    // A platform staff member enters a tenant they are not a member of: the
    // roster settles it as the staff default, the caller tenant list does not
    // hold the row, and only the roster's alias cache can name it. Without the
    // added row the bar could neither show nor leave that tenant.
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
      session: createTestSession(authenticatedSnapshot()),
    })

    expect(
      await screen.findByRole('button', { name: 'globex-renamed' }),
    ).toBeInTheDocument()
    expect(getCurrentTenantId()).toBe('globex')
  })
})
