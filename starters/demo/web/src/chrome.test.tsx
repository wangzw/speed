/**
 * The host's two tenant layers: the identity the switcher keys on (the tenant
 * id) and the name the request surface announces (the alias). They are
 * distinct -- a rename moves the alias and leaves the id -- so these tests hold
 * them apart, above all for the diverged case the demo's own seeded tenant does
 * not exhibit.
 *
 * The header-actions suite renders the shared chrome under the real provider
 * tree, because what it holds is a property of the composition: the header
 * carries the color-mode toggle and no language switch, since the language is
 * set on the account preferences page alone.
 */

import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { ReactElement } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { tenantQueryKey } from '@speed/auth-core'
import {
  createQueryClient,
  createSpeedI18n,
  SpeedAppProviders,
} from '@speed/product-shell/bootstrap'
import type { SpeedAppServices } from '@speed/product-shell/bootstrap'
import { TENANCY_UI_NAMESPACE, tenancyUiResources } from '@speed/tenancy-ui'
import { AppServicesProvider } from './app-services.js'
import {
  COLOR_MODE_STORAGE_KEY,
  ColorModeToggle,
  getCurrentTenantAlias,
  getCurrentTenantId,
  HeaderActions,
  initialColorMode,
  readStoredColorMode,
  setCurrentTenantId,
  setTenantAliases,
  tenantAlias,
  TenantMenu,
  useTenantSelection,
} from './chrome.js'
import type { DemoTenant } from './chrome.js'
import { DEMO_NAMESPACE, demoResources } from './resources.js'
import { createHarnessClient } from './test-utils/render.js'
import { createTestSession } from './test-utils/session.js'

afterEach(() => {
  setTenantAliases(new Map())
  setCurrentTenantId(null)
})

describe('the tenant alias layer', () => {
  it('resolves a tenant id to its alias, not back to the id', () => {
    const tenants = [{ id: 'acme', alias: 'acme-inc', name: 'Acme Inc.' }]
    expect(tenantAlias('acme', tenants)).toBe('acme-inc')
    // An id nobody lists, and the no-tenant case, resolve to nothing.
    expect(tenantAlias('absent', tenants)).toBeNull()
    expect(tenantAlias(null, tenants)).toBeNull()
  })

  it('announces the alias while the switcher keeps the id', () => {
    // A tenant whose alias a rename has moved away from its id: the id the
    // switcher compares a pick against and the name the server resolves are no
    // longer the same string.
    setTenantAliases(new Map([['acme', 'acme-inc']]))
    setCurrentTenantId('acme')

    // The switcher's current tenant is the id...
    expect(getCurrentTenantId()).toBe('acme')
    // ...and the value announced to the server is the alias. Reading the id
    // here would be the defect: the server's resolve layer looks the name up in
    // the roster's alias column, so the id would be refused 404.
    expect(getCurrentTenantAlias()).toBe('acme-inc')
  })

  it('announces nothing for a tenant no source names', () => {
    // The placeholder roster is retired. A current tenant that neither the
    // caller tenant list nor the roster alias cache names has no alias to
    // announce; a fallback list would send a name the server may no longer
    // resolve, so the request surface stays silent instead.
    setCurrentTenantId('acme')
    expect(getCurrentTenantAlias()).toBeNull()
  })

  it('announces nothing with no current tenant', () => {
    setCurrentTenantId(null)
    expect(getCurrentTenantId()).toBeNull()
    expect(getCurrentTenantAlias()).toBeNull()
  })
})

/** A browser-storage stand-in: the jsdom environment's own localStorage is
 * unavailable (the Node runtime's experimental global shadows it), so the
 * suite installs one it can read back. */
function installStorageDouble(): Map<string, string> {
  const entries = new Map<string, string>()
  vi.stubGlobal('localStorage', {
    getItem: (key: string) => entries.get(key) ?? null,
    setItem: (key: string, value: string) => {
      entries.set(key, value)
    },
    removeItem: (key: string) => {
      entries.delete(key)
    },
    clear: () => entries.clear(),
  })
  return entries
}

describe('the header actions', () => {
  it('carries the color-mode toggle and no language switch', () => {
    const i18n = createSpeedI18n({
      supportedLanguages: ['zh-CN', 'en-US'],
      defaultLanguage: 'en-US',
      storage: null,
      urlParameterName: null,
      navigatorLanguages: [],
      namespaces: [{ namespace: DEMO_NAMESPACE, resources: demoResources }],
    })
    render(
      <SpeedAppProviders i18n={i18n} queryClient={createQueryClient()}>
        <HeaderActions />
      </SpeedAppProviders>,
    )

    // The language entry lives on the account preferences page alone, so the
    // header's only control is the colour-mode toggle.
    const t = i18n.getFixedT('en-US', DEMO_NAMESPACE)
    const controls = screen.getAllByRole('button')
    expect(controls).toHaveLength(1)
    expect(controls[0]).toHaveAccessibleName(t('colorMode.toDark'))
  })
})

/** Two tenants, as a host with a membership source would offer them. */
const TWO_TENANTS: readonly DemoTenant[] = [
  { id: 'acme', alias: 'acme', name: 'Acme Inc.' },
  { id: 'globex', alias: 'globex', name: 'Globex Corp.' },
]

/** The tenant switcher over the host's own selection state, the way the
 * tenant frame composes it. */
function TenantSwitchHarness(): ReactElement {
  const [tenantId, setTenantId] = useTenantSelection('acme')
  return (
    <TenantMenu
      tenants={TWO_TENANTS}
      tenantId={tenantId}
      personalTenantId={null}
      onTenantSelect={setTenantId}
    />
  )
}

/** Renders the switcher under the production stack and its own services. */
function renderTenantSwitch() {
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
  const result = render(
    <SpeedAppProviders i18n={i18n} queryClient={queryClient}>
      <AppServicesProvider services={services}>
        <TenantSwitchHarness />
      </AppServicesProvider>
    </SpeedAppProviders>,
  )
  return { ...result, queryClient }
}

describe('the tenant switch', () => {
  afterEach(() => {
    setCurrentTenantId(null)
  })

  it("evicts the departing tenant's cached rows when the menu switches", async () => {
    const user = userEvent.setup()
    const { queryClient } = renderTenantSwitch()
    // A row read under the tenant the viewer is about to leave. Its key
    // carries the tenant id as the outermost segment (auth-core's
    // convention), which is what makes it the departing tenant's row.
    queryClient.setQueryData(tenantQueryKey('acme', 'rows'), [{ id: 'row-1' }])

    await user.click(screen.getByRole('button', { name: 'acme' }))
    await user.click(await screen.findByRole('menuitem', { name: 'globex' }))

    // The switch is a host action: the departing tenant's whole cache
    // subtree is gone, and the new tenant is what every later read is keyed
    // under. A row left behind would be served to the new tenant's reads --
    // or, worse, shown as the new tenant's data.
    expect(getCurrentTenantId()).toBe('globex')
    expect(
      queryClient.getQueryData(tenantQueryKey('acme', 'rows')),
    ).toBeUndefined()
  })
})

/** A browser-storage stand-in for the colour-mode suite (see
 * installStorageDouble for why jsdom's own is unavailable). */
describe('the colour mode choice', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
  })

  it("starts in the device's stored choice", () => {
    const entries = installStorageDouble()
    // The suite's matchMedia stub matches every query (so the
    // operating-system branch would answer dark); a stored light choice must
    // win over it, which is what distinguishes reading the stored value from
    // following the system.
    entries.set(COLOR_MODE_STORAGE_KEY, 'light')
    expect(readStoredColorMode()).toBe('light')
    expect(initialColorMode()).toBe('light')
  })

  it('ignores a stored value that is not a mode', () => {
    const entries = installStorageDouble()
    entries.set(COLOR_MODE_STORAGE_KEY, 'sepia')
    // With nothing usable stored, the operating system's preference decides
    // (the suite's default stub reports every query as matching, so dark is
    // the answer here).
    expect(readStoredColorMode()).toBeNull()
    expect(initialColorMode()).toBe('dark')
  })

  it('follows the operating system when nothing was stored', () => {
    installStorageDouble()
    const original = window.matchMedia
    window.matchMedia = vi.fn().mockImplementation((query: string) => ({
      matches: query === '(prefers-color-scheme: dark)',
      media: query,
      onchange: null,
      addListener: () => {},
      removeListener: () => {},
      addEventListener: () => {},
      removeEventListener: () => {},
      dispatchEvent: () => true,
    })) as unknown as typeof window.matchMedia
    try {
      expect(initialColorMode()).toBe('dark')
    } finally {
      window.matchMedia = original
    }
  })

  it('writes the flipped mode to the device from the header control', async () => {
    const entries = installStorageDouble()
    const user = userEvent.setup()
    const i18n = createSpeedI18n({
      supportedLanguages: ['zh-CN', 'en-US'],
      defaultLanguage: 'en-US',
      storage: null,
      urlParameterName: null,
      navigatorLanguages: [],
      namespaces: [{ namespace: DEMO_NAMESPACE, resources: demoResources }],
    })
    const queryClient = createQueryClient()
    render(
      <SpeedAppProviders i18n={i18n} queryClient={queryClient}>
        <ColorModeToggle />
      </SpeedAppProviders>,
    )

    // The label comes from the instance the tree renders with, the way the
    // control resolves it.
    const t = i18n.getFixedT('en-US', DEMO_NAMESPACE)
    await user.click(
      screen.getByRole('button', { name: t('colorMode.toDark') }),
    )

    // The choice is manual, so it must survive a reload: the next boot reads
    // it back as the theme's defaultMode.
    expect(entries.get(COLOR_MODE_STORAGE_KEY)).toBe('dark')
    expect(
      screen.getByRole('button', { name: t('colorMode.toLight') }),
    ).toBeInTheDocument()
  })
})
