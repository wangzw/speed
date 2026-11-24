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

    expect(getCurrentTenantName()).toBe('globex-renamed')
  })
})

const OFFERED: readonly DemoTenant[] = [
  { id: 'my-space', name: 'my-space', kind: 'personal' },
  {
    id: 'other-space',
    name: 'other-space',
    kind: 'business',
  },
  { id: 'acme', name: 'acme', kind: 'business' },
]

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

    await userEvent
      .setup()
      .click(await screen.findByRole('menuitem', { name: 'acme' }))

    expect(
      await screen.findByRole('menuitem', {
        name: demoEnUS.organization.personal,
      }),
    ).toBeInTheDocument()
    expect(
      screen.getByRole('menuitem', { name: 'other-space' }),
    ).toBeInTheDocument()
    expect(screen.getAllByRole('menuitem', { name: 'acme' })).toHaveLength(1)
  })

  it('leaves the current tenant in the list but not selectable', async () => {
    renderTenantMenu({
      tenants: OFFERED,
      tenantName: 'acme',
    })

    const user = userEvent.setup()
    const current = await screen.findByRole('menuitem', { name: 'acme' })
    await user.click(current)
    expect(current).toHaveAttribute('aria-expanded', 'true')
    expect(screen.getAllByRole('menuitem', { name: 'acme' })).toHaveLength(1)
  })

  it('adds a current tenant the list does not hold, named from the roster cache', async () => {
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
