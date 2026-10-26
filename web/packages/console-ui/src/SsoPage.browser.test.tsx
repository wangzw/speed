/**
 * SsoPage in a real browser: the SSO provider table at the sm/md/lg
 * floor widths with fixed data, and the declared-domain list with the
 * one-time DNS record panel at the same floors. The tenant is seeded
 * through the query parameter the page opens on.
 */
import { afterEach, describe, expect, it } from 'vitest'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { page } from 'vitest/browser'
import { SsoPage } from './SsoPage.js'
import { renderWithProviders } from '../test-utils/render.js'
import {
  createConsoleOperations,
  makeConsoleBackend,
  providerFixture,
} from '../test-utils/operations.js'
import { expectNoHorizontalOverflow } from '../test-utils/browser-overflow.js'
import { clearTenant, seedTenant } from '../test-utils/tenant-url.js'
import enUS from './locales/en-US.json' with { type: 'json' }

const FLOOR_WIDTHS = [600, 900, 1200] as const

const TENANTS = [{ id: 'acme', alias: 'Acme', status: 'active' as const }]

const PROVIDERS = [
  providerFixture({ name: 'Acme Corporate Identity Provider' }),
]

/**
 * A provider with both a long declared domain and a verified one, so the
 * row's state text and its two actions are measured at every floor.
 */
const DOMAIN_PROVIDER = providerFixture({
  name: 'Acme Corporate Identity Provider',
  domains: [
    { domain: 'acme-corporate-identity.example', verified: false },
    { domain: 'acme.example', verified: true },
  ],
})

/** A 64-character verification token, the module's own token shape. */
const TOKEN = 'a'.repeat(64)

/** The rendered width and horizontal bounds of a control. */
function horizontalBounds(element: HTMLElement): {
  width: number
  left: number
  right: number
} {
  const rect = element.getBoundingClientRect()
  return { width: rect.width, left: rect.left, right: rect.right }
}

afterEach(clearTenant)

describe('SsoPage horizontal overflow in Chromium', () => {
  for (const width of FLOOR_WIDTHS) {
    it(`keeps the provider table inside a ${width}px viewport`, async () => {
      await page.viewport(width, 900)
      seedTenant('acme')

      const operations = createConsoleOperations(
        makeConsoleBackend({
          listTenants: () => Promise.resolve(TENANTS),
          listSsoProviders: () => Promise.resolve(PROVIDERS),
        }),
      )
      renderWithProviders(<SsoPage />, { operations, language: 'en-US' })

      await screen.findByText('Acme Corporate Identity Provider')
      await expectNoHorizontalOverflow()
    })
  }
})

/**
 * The domain list and the DNS record panel carry the widest strings on
 * the page -- a long declared domain, a 64-character token, and the
 * record's full name and value. At each floor every row action and copy
 * control must stay inside the viewport's horizontal bounds, and the page
 * must not overflow.
 */
describe('SsoPage domain list and DNS record panel in Chromium', () => {
  for (const width of FLOOR_WIDTHS) {
    it(`keeps the domain rows and DNS record inside a ${width}px viewport`, async () => {
      await page.viewport(width, 900)
      seedTenant('acme')

      const operations = createConsoleOperations(
        makeConsoleBackend({
          listTenants: () => Promise.resolve(TENANTS),
          listSsoProviders: () => Promise.resolve([DOMAIN_PROVIDER]),
          claimSsoDomain: () => Promise.resolve({ verify_token: TOKEN }),
        }),
      )
      renderWithProviders(<SsoPage />, { operations, language: 'en-US' })
      await screen.findByText(DOMAIN_PROVIDER.name)

      const user = userEvent.setup()
      await user.click(
        screen.getByRole('combobox', {
          name: enUS.sso.domains.providerLabel,
        }),
      )
      await user.click(
        await screen.findByRole('option', { name: DOMAIN_PROVIDER.name }),
      )
      await screen.findByRole('list', { name: enUS.sso.domains.listLabel })

      const viewportWidth = document.documentElement.clientWidth
      for (const { domain } of DOMAIN_PROVIDER.domains) {
        const remove = screen.getByRole('button', {
          name: enUS.sso.domains.removeAria.replace('{{domain}}', domain),
        })
        const bounds = horizontalBounds(remove)
        expect(bounds.width).toBeGreaterThan(0)
        expect(bounds.left).toBeGreaterThanOrEqual(0)
        expect(bounds.right).toBeLessThanOrEqual(viewportWidth + 1)
      }

      // Claiming hands out the DNS record: its complete name and value,
      // with their copy controls, must also stay inside the floor.
      await user.type(
        screen.getByRole('textbox', { name: enUS.sso.domains.domainLabel }),
        'beta.example',
      )
      await user.click(
        screen.getByRole('button', { name: enUS.sso.domains.claim }),
      )

      const recordName = '_speed-authn.beta.example'
      const recordValue = `speed-authn=${TOKEN}`
      expect(await screen.findByText(recordName)).toBeInTheDocument()
      expect(screen.getByText(recordValue)).toBeInTheDocument()
      for (const copyLabel of [
        enUS.sso.domains.claimRecordNameCopy,
        enUS.sso.domains.claimRecordValueCopy,
      ]) {
        const copy = screen.getByRole('button', { name: copyLabel })
        const bounds = horizontalBounds(copy)
        expect(bounds.width).toBeGreaterThan(0)
        expect(bounds.left).toBeGreaterThanOrEqual(0)
        expect(bounds.right).toBeLessThanOrEqual(viewportWidth + 1)
      }

      await expectNoHorizontalOverflow()
    })
  }
})
