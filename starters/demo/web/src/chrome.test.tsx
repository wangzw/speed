/**
 * The host's two tenant layers: the identity the switcher keys on (the tenant
 * id) and the name the request surface announces (the alias). They are
 * distinct -- a rename moves the alias and leaves the id -- so these tests hold
 * them apart, above all for the diverged case the demo's own seeded tenant does
 * not exhibit.
 *
 * The language-switch suite drives the real LanguageSwitcher under the real
 * provider tree, because what it holds is a property of the composition
 * rather than of the switcher: the choice must reach the device's storage
 * when the user picks a language.
 */

import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { ReactElement } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { tenantQueryKey } from '@speed/auth-core'
import {
  createI18n,
  I18nextProvider,
  registerNamespace,
  SPEED_LOCALE_STORAGE_KEY,
} from '@speed/i18n'
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
  DEMO_TENANTS,
  getCurrentTenantAlias,
  getCurrentTenantId,
  HeaderActions,
  initialColorMode,
  LanguageSwitcher,
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

  it('falls back to the placeholder alias until the roster is read', () => {
    const first = DEMO_TENANTS[0]
    expect(first).toBeDefined()
    setCurrentTenantId(first?.id ?? null)
    expect(getCurrentTenantAlias()).toBe(first?.alias)
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

describe('the language switch', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('persists the chosen language to the device storage', async () => {
    const entries = installStorageDouble()
    const i18n = createI18n({
      supportedLanguages: ['zh-CN', 'en-US'],
      navigatorLanguages: ['zh-CN'],
      urlParameterName: null,
    })
    registerNamespace(i18n, DEMO_NAMESPACE, demoResources)
    render(
      <I18nextProvider i18n={i18n}>
        <LanguageSwitcher />
      </I18nextProvider>,
    )

    fireEvent.click(screen.getByRole('button', { expanded: false }))
    const items = await screen.findAllByRole('menuitem')
    // The switcher lists the supported set in order: zh-CN, then en-US. The
    // name is resolved through Intl.DisplayNames in the current UI language.
    fireEvent.click(items[1] as HTMLElement)

    await waitFor(() => expect(i18n.language).toBe('en-US'))
    // The choice is a manual one, so it must land on the device too: a
    // reload negotiates the stored tier before the browser languages, and
    // without the write the interface falls back to the browser's language.
    expect(entries.get(SPEED_LOCALE_STORAGE_KEY)).toBe('en-US')
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
      personalTenant={null}
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

  it('keeps the header action controls as siblings, so a collapsed rail can stack them', () => {
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
        <div data-testid="slot">
          <HeaderActions />
        </div>
      </SpeedAppProviders>,
    )

    // The sidebar rail lays a slot's children out in a column; a wrapper
    // around the pair would keep them in one row wider than the rail, which
    // pushes the second control out of the paper. Two top-level nodes, no
    // wrapper between them, is what lets the rail stack them.
    const t = i18n.getFixedT('en-US', DEMO_NAMESPACE)
    expect(screen.getByRole('button', { name: t('languageSwitcher.label') }))
      .toBeInTheDocument()
    expect(screen.getByRole('button', { name: t('colorMode.toDark') }))
      .toBeInTheDocument()
    expect(screen.getByTestId('slot').children).toHaveLength(2)
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
