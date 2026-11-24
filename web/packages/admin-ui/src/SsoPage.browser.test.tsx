import { afterEach, describe, expect, it } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { page } from 'vitest/browser'
import { SsoPage } from './SsoPage.js'
import { renderWithProviders } from '../test-utils/render.js'
import {
  createAdminOperations,
  makeAdminBackend,
  providerFixture,
  tenantFixture,
} from '../test-utils/operations.js'
import { expectNoHorizontalOverflow } from '../test-utils/browser-overflow.js'
import { clearTenant, seedTenant } from '../test-utils/tenant-url.js'
import enUS from './locales/en-US.json' with { type: 'json' }

const FLOOR_WIDTHS = [600, 900, 1200] as const

const TENANTS = [tenantFixture({ name: 'acme', display_name: 'Acme', status: 'active' })]

const PROVIDERS = [
  providerFixture({ name: 'Acme Corporate Identity Provider' }),
]

// A long declared domain and a verified one, so both row states are measured.
const DOMAIN_PROVIDER = providerFixture({
  name: 'Acme Corporate Identity Provider',
  domains: [
    { domain: 'acme-corporate-identity.example', verified: false },
    { domain: 'acme.example', verified: true },
  ],
})

const TOKEN = 'a'.repeat(64)

function horizontalBounds(element: HTMLElement): {
  width: number
  left: number
  right: number
} {
  const rect = element.getBoundingClientRect()
  return { width: rect.width, left: rect.left, right: rect.right }
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

function nextFrame(): Promise<void> {
  return new Promise((resolve) => {
    requestAnimationFrame(() => resolve())
  })
}

function describeActiveElement(): string {
  const active = document.activeElement
  if (active === null) {
    return 'null'
  }
  const parts = [active.tagName.toLowerCase()]
  const role = active.getAttribute('role')
  const label = active.getAttribute('aria-label')
  const text = (active.textContent ?? '').replace(/\s+/g, ' ').trim().slice(0, 80)
  if (role !== null) {
    parts.push(`role="${role}"`)
  }
  if (label !== null) {
    parts.push(`aria-label="${label}"`)
  }
  if (text !== '') {
    parts.push(`text="${text}"`)
  }
  return parts.join(' ')
}

afterEach(clearTenant)

describe('SsoPage horizontal overflow in Chromium', () => {
  for (const width of FLOOR_WIDTHS) {
    it(`keeps the provider table inside a ${width}px viewport`, async () => {
      await page.viewport(width, 900)
      seedTenant('acme')

      const operations = createAdminOperations(
        makeAdminBackend({
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

// The domain list and DNS panel carry the page's widest strings.
describe('SsoPage domain list and DNS record panel in Chromium', () => {
  for (const width of FLOOR_WIDTHS) {
    it(`keeps the domain rows and DNS record inside a ${width}px viewport`, async () => {
      await page.viewport(width, 900)
      seedTenant('acme')

      const operations = createAdminOperations(
        makeAdminBackend({
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
        const actions = screen.getByRole('button', {
          name: enUS.sso.domains.actionsAria.replace('{{domain}}', domain),
        })
        const bounds = horizontalBounds(actions)
        expect(bounds.width).toBeGreaterThan(0)
        expect(bounds.left).toBeGreaterThanOrEqual(0)
        expect(bounds.right).toBeLessThanOrEqual(viewportWidth + 1)
      }

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

// jsdom does not honour focusability, so a focused trigger losing focus on its
// write or failed precondition is observable only in a real browser.
describe('SsoPage focus retention in Chromium', () => {
  it('keeps focus on the claim control while its precondition is unmet and through its write', async () => {
    await page.viewport(900, 900)
    seedTenant('acme')
    const claimed = deferred<{ verify_token: string }>()
    const operations = createAdminOperations(
      makeAdminBackend({
        listTenants: () => Promise.resolve(TENANTS),
        listSsoProviders: () => Promise.resolve(PROVIDERS),
        claimSsoDomain: () => claimed.promise,
      }),
    )
    renderWithProviders(<SsoPage />, { operations, language: 'en-US' })
    await screen.findByText('Acme Corporate Identity Provider')
    const user = userEvent.setup()

    const claim = screen.getByRole('button', { name: enUS.sso.domains.claim })
    claim.focus()
    await waitFor(() => {
      expect(
        claim,
        `focus while unavailable; document.activeElement is ${describeActiveElement()}`,
      ).toHaveFocus()
    })

    await user.click(
      screen.getByRole('combobox', { name: enUS.sso.domains.providerLabel }),
    )
    await user.click(
      await screen.findByRole('option', {
        name: 'Acme Corporate Identity Provider',
      }),
    )
    await user.type(
      screen.getByRole('textbox', { name: enUS.sso.domains.domainLabel }),
      'beta.example',
    )
    claim.focus()
    await user.click(claim)
    await waitFor(() => {
      expect(within(claim).getByRole('progressbar')).toBeInTheDocument()
    })
    await nextFrame()
    expect(
      claim,
      `focus while the claim is in flight; document.activeElement is ${describeActiveElement()}`,
    ).toHaveFocus()

    claimed.resolve({ verify_token: TOKEN })
    await waitFor(() => {
      expect(within(claim).queryByRole('progressbar')).not.toBeInTheDocument()
    })
    expect(
      claim,
      `focus after the claim settled; document.activeElement is ${describeActiveElement()}`,
    ).toHaveFocus()
  })

  it('keeps focus on the provider form submit while required fields are empty and through its write', async () => {
    await page.viewport(900, 900)
    seedTenant('acme')
    const created = deferred<ReturnType<typeof providerFixture>>()
    const operations = createAdminOperations(
      makeAdminBackend({
        listTenants: () => Promise.resolve(TENANTS),
        listSsoProviders: () => Promise.resolve(PROVIDERS),
        createSsoProvider: () => created.promise,
      }),
    )
    renderWithProviders(<SsoPage />, { operations, language: 'en-US' })
    await screen.findByText('Acme Corporate Identity Provider')
    const user = userEvent.setup()

    const submit = screen.getByRole('button', {
      name: enUS.sso.provider.createSubmit,
    })
    submit.focus()
    await waitFor(() => {
      expect(
        submit,
        `focus while unavailable; document.activeElement is ${describeActiveElement()}`,
      ).toHaveFocus()
    })

    for (const [label, value] of [
      [enUS.sso.provider.protocolLabel, 'oidc'],
      [enUS.sso.provider.nameLabel, 'New IdP'],
      [enUS.sso.provider.provisioningLabel, 'preprovisioned'],
    ] as const) {
      await user.type(screen.getByLabelText(label), value)
    }
    submit.focus()
    await user.click(submit)
    await waitFor(() => {
      expect(within(submit).getByRole('progressbar')).toBeInTheDocument()
    })
    await nextFrame()
    expect(
      submit,
      `focus while the create is in flight; document.activeElement is ${describeActiveElement()}`,
    ).toHaveFocus()

    created.resolve(providerFixture({ name: 'New IdP' }))
    await waitFor(() => {
      expect(within(submit).queryByRole('progressbar')).not.toBeInTheDocument()
    })
    expect(
      submit,
      `focus after the create settled; document.activeElement is ${describeActiveElement()}`,
    ).toHaveFocus()
  })
})
