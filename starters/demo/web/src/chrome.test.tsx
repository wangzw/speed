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
import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  createI18n,
  I18nextProvider,
  registerNamespace,
  SPEED_LOCALE_STORAGE_KEY,
} from '@speed/i18n'
import {
  DEMO_TENANTS,
  getCurrentTenantAlias,
  getCurrentTenantId,
  LanguageSwitcher,
  setCurrentTenantId,
  setTenantAliases,
  tenantAlias,
} from './chrome.js'
import { DEMO_NAMESPACE, demoResources } from './resources.js'

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
