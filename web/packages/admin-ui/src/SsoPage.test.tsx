import { afterEach, describe, expect, it, vi } from 'vitest'
import { waitForCondition } from '@speed/test-utils/wait'
import { act, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { SsoPage, ssoProviderWriteBody } from './SsoPage.js'
import type { SsoProviderWriteDraft } from './SsoPage.js'
import type {
  AdminClaimSsoDomainInput,
  AdminCreateSsoProviderInput,
  AdminSsoProvider,
  AdminWriteSsoProviderInput,
} from './operations.js'
import { renderWithProviders } from '../test-utils/render.js'
import {
  columnVisibility,
  containerMaxWidthOf,
} from '../test-utils/container-visibility.js'
import { hasCoarsePointerTouchTarget } from '@speed/test-utils/emitted-css'
import { uiKitResources } from '@speed/ui-kit'
import {
  createAdminOperations,
  makeAdminBackend,
  providerFixture,
  tenantFixture,
  type AdminBackend,
} from '../test-utils/operations.js'
import { createStepUpSession } from '../test-utils/session.js'
import { expectNoAxeViolations } from '../test-utils/axe.js'
import zhCN from './locales/zh-CN.json' with { type: 'json' }

const TENANT = tenantFixture({ name: 'acme', display_name: 'Acme', status: 'active' })

function text(template: string, values: Record<string, string>): string {
  return template.replace(/\{\{(\w+)\}\}/g, (match, key: string) =>
    values[key] ?? match,
  )
}

const CONFIRM_LABEL = (
  uiKitResources['zh-CN'] as unknown as {
    readonly confirmDialog: { readonly confirmLabel: string }
  }
).confirmDialog.confirmLabel

async function pickTenant(user: ReturnType<typeof userEvent.setup>): Promise<void> {
  await user.click(screen.getByLabelText(zhCN.common.tenant))
  await user.click(await screen.findByRole('option', { name: 'Acme' }))
}

function withProviders(
  overrides: Partial<AdminBackend> = {},
): ReturnType<typeof createAdminOperations> {
  return createAdminOperations(
    makeAdminBackend({
      listTenants: () => Promise.resolve([TENANT]),
      listSsoProviders: () =>
        Promise.resolve([providerFixture({ name: 'Acme IdP' })]),
      ...overrides,
    }),
  )
}

async function pickProvider(user: ReturnType<typeof userEvent.setup>): Promise<void> {
  await user.click(screen.getByLabelText(zhCN.sso.domains.providerLabel))
  await user.click(await screen.findByRole('option', { name: 'Acme IdP' }))
}

async function openProviderActions(
  user: ReturnType<typeof userEvent.setup>,
  name = 'Acme IdP',
): Promise<void> {
  await user.click(
    screen.getByRole('button', {
      name: text(zhCN.sso.actionsAria, { name }),
    }),
  )
}

async function chooseProviderAction(
  user: ReturnType<typeof userEvent.setup>,
  label: string,
  name = 'Acme IdP',
): Promise<void> {
  await openProviderActions(user, name)
  await user.click(await screen.findByRole('menuitem', { name: label }))
}

async function chooseDomainAction(
  user: ReturnType<typeof userEvent.setup>,
  domain: string,
  label: string,
): Promise<void> {
  await user.click(
    screen.getByRole('button', {
      name: text(zhCN.sso.domains.actionsAria, { domain }),
    }),
  )
  await user.click(await screen.findByRole('menuitem', { name: label }))
}

function writeDraft(
  overrides: Partial<SsoProviderWriteDraft> = {},
): SsoProviderWriteDraft {
  return {
    protocol: 'oidc',
    name: 'New IdP',
    provisioning: 'jit',
    usernameStrategy: 'user-confirm',
    usernameClaim: '',
    enabled: 'unchanged',
    trustIdpMfa: 'unchanged',
    allowIdpInitiated: 'unchanged',
    allowSha1: 'unchanged',
    mergeExisting: 'unchanged',
    config: '',
    clientSecret: '',
    ...overrides,
  }
}

describe('ssoProviderWriteBody', () => {
  it('sends a claim and its claim on a jit create', () => {
    expect(
      ssoProviderWriteBody(
        writeDraft({ usernameStrategy: 'claim', usernameClaim: 'email' }),
        false,
      ),
    ).toEqual({
      protocol: 'oidc',
      name: 'New IdP',
      provisioning: 'jit',
      username_strategy: 'claim',
      username_claim: 'email',
    })
  })

  it('sends only the strategy on a jit create that asks the user', () => {
    expect(ssoProviderWriteBody(writeDraft(), false)).toEqual({
      protocol: 'oidc',
      name: 'New IdP',
      provisioning: 'jit',
      username_strategy: 'user-confirm',
    })
  })

  it('sends neither username field on a preprovisioned create', () => {
    expect(
      ssoProviderWriteBody(
        writeDraft({ provisioning: 'preprovisioned' }),
        false,
      ),
    ).toEqual({
      protocol: 'oidc',
      name: 'New IdP',
      provisioning: 'preprovisioned',
    })
  })

  it('clears both username fields when an update leaves jit', () => {
    expect(
      ssoProviderWriteBody(
        writeDraft({
          provisioning: 'preprovisioned',
          usernameStrategy: 'claim',
          usernameClaim: 'email',
        }),
        true,
      ),
    ).toEqual({
      protocol: 'oidc',
      name: 'New IdP',
      provisioning: 'preprovisioned',
      username_strategy: '',
      username_claim: '',
    })
  })

  it('clears the stored claim when an update asks the user instead', () => {
    expect(
      ssoProviderWriteBody(
        writeDraft({ usernameStrategy: 'user-confirm', usernameClaim: 'email' }),
        true,
      ),
    ).toEqual({
      protocol: 'oidc',
      name: 'New IdP',
      provisioning: 'jit',
      username_strategy: 'user-confirm',
      username_claim: '',
    })
  })

  it('sends the chosen claim when an update moves to the claim strategy', () => {
    expect(
      ssoProviderWriteBody(
        writeDraft({
          usernameStrategy: 'claim',
          usernameClaim: 'preferred_username',
        }),
        true,
      ),
    ).toEqual({
      protocol: 'oidc',
      name: 'New IdP',
      provisioning: 'jit',
      username_strategy: 'claim',
      username_claim: 'preferred_username',
    })
  })

  it('trims the claim and drops a value outside the closed set', () => {
    expect(
      ssoProviderWriteBody(
        writeDraft({ usernameStrategy: 'claim', usernameClaim: ' email ' }),
        false,
      ),
    ).toEqual({
      protocol: 'oidc',
      name: 'New IdP',
      provisioning: 'jit',
      username_strategy: 'claim',
      username_claim: 'email',
    })
    expect(
      ssoProviderWriteBody(
        writeDraft({ usernameStrategy: 'claim', usernameClaim: 'nickname' }),
        false,
      ),
    ).toEqual({
      protocol: 'oidc',
      name: 'New IdP',
      provisioning: 'jit',
      username_strategy: 'claim',
      username_claim: '',
    })
  })
})

describe('SsoPage', () => {
  afterEach(() => {
    Reflect.deleteProperty(navigator, 'clipboard')
  })

  it('prompts for a tenant before listing', () => {
    const operations = withProviders()
    renderWithProviders(<SsoPage />, { operations })
    expect(screen.getByText(zhCN.sso.needTenant.title)).toBeInTheDocument()
  })

  it('does not read an empty tenant scope', async () => {
    const listSsoProviders = vi.fn(() => Promise.resolve([]))
    const operations = withProviders({ listSsoProviders })
    renderWithProviders(<SsoPage />, { operations })

    await act(async () => {
      await Promise.resolve()
    })
    expect(listSsoProviders).not.toHaveBeenCalled()
  })

  it('opens on the tenant named by the query string', async () => {
    window.history.replaceState({}, '', '/admin/sso?tenant=acme')
    try {
      const listSsoProviders = vi.fn(() =>
        Promise.resolve([providerFixture({ name: 'Acme IdP' })]),
      )
      const operations = withProviders({ listSsoProviders })
      renderWithProviders(<SsoPage />, { operations })
      expect(await screen.findByText('Acme IdP')).toBeInTheDocument()
      expect(screen.queryByText(zhCN.sso.needTenant.title)).toBeNull()
      expect(screen.getByLabelText(zhCN.common.tenant)).toHaveTextContent('Acme')
      expect(listSsoProviders).toHaveBeenCalledWith('acme')
    } finally {
      window.history.replaceState({}, '', '/')
    }
  })

  it('lists the tenant providers', async () => {
    const user = userEvent.setup()
    const operations = withProviders()
    renderWithProviders(<SsoPage />, { operations })
    await pickTenant(user)
    expect(await screen.findByText('Acme IdP')).toBeInTheDocument()
    expect(screen.getByText('oidc')).toBeInTheDocument()
  })

  it('declares the designed column priorities and caps the primary identity', async () => {
    const user = userEvent.setup()
    const name = 'A-very-long-enterprise-provider-name-for-acme'
    const operations = withProviders({
      listSsoProviders: () => Promise.resolve([providerFixture({ name })]),
    })
    renderWithProviders(<SsoPage />, { operations })
    await pickTenant(user)
    await screen.findByText(name)

    const header = (label: string): Element =>
      screen.getByRole('columnheader', { name: label })
    expect(columnVisibility(header(zhCN.sso.column.name))).toBeNull()
    expect(columnVisibility(header(zhCN.sso.column.protocol))).toEqual({
      hiddenAt: 0,
      visibleFrom: 900,
    })
    expect(columnVisibility(header(zhCN.sso.column.enabled))).toEqual({
      hiddenAt: 0,
      visibleFrom: 900,
    })
    expect(columnVisibility(header(zhCN.sso.column.version))).toEqual({
      hiddenAt: 0,
      visibleFrom: 1200,
    })
    expect(columnVisibility(header(zhCN.sso.column.actions))).toBeNull()

    const nameCell = screen.getByText(name).closest('td')
    expect(nameCell).toHaveAttribute('title', name)
    expect(containerMaxWidthOf(nameCell!.firstElementChild!)).toEqual(
      new Map([
        [0, 160],
        [900, 200],
      ]),
    )
  })

  it('shows no error banner while no write has failed', async () => {
    const user = userEvent.setup()
    const operations = withProviders()
    renderWithProviders(<SsoPage />, { operations })
    await pickTenant(user)
    await screen.findByText('Acme IdP')
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('raises its compact actions to the 44px floor under a coarse pointer', async () => {
    const user = userEvent.setup()
    const operations = withProviders()
    renderWithProviders(<SsoPage />, { operations })
    await pickTenant(user)
    await screen.findByText('Acme IdP')
    const gated = [
      screen.getByRole('button', {
        name: text(zhCN.sso.actionsAria, { name: 'Acme IdP' }),
      }),
    ]
    for (const control of gated) {
      expect(hasCoarsePointerTouchTarget(control)).toBe(true)
    }
    await openProviderActions(user)
    for (const item of await screen.findAllByRole('menuitem')) {
      expect(hasCoarsePointerTouchTarget(item)).toBe(true)
    }
  })

  it('deletes a provider through the bundle', async () => {
    const user = userEvent.setup()
    const deleted: string[] = []
    const operations = withProviders({
      deleteSsoProvider: (input) => {
        deleted.push(input.id)
        return Promise.resolve()
      },
    })
    renderWithProviders(<SsoPage />, { operations })
    await pickTenant(user)
    await screen.findByText('Acme IdP')
    await chooseProviderAction(user, zhCN.sso.delete)
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: CONFIRM_LABEL }))
    await waitForCondition(() => {
      expect(deleted).toEqual(['idp-1'])
    })
  })

  it('refreshes metadata and re-verifies domains through the bundle', async () => {
    const user = userEvent.setup()
    const refreshed: string[] = []
    const reverified: string[] = []
    const operations = withProviders({
      refreshSsoMetadata: (input) => {
        refreshed.push(input.id)
        return Promise.resolve()
      },
      reverifySsoDomains: (input) => {
        reverified.push(input.id)
        return Promise.resolve()
      },
    })
    renderWithProviders(<SsoPage />, { operations })
    await pickTenant(user)
    await screen.findByText('Acme IdP')
    await chooseProviderAction(user, zhCN.sso.refreshMetadata)
    await chooseProviderAction(user, zhCN.sso.reverifyDomains)
    await waitForCondition(() => {
      expect(refreshed).toEqual(['idp-1'])
      expect(reverified).toEqual(['idp-1'])
    })
  })

  it("lists a selected provider's domains with their verification state", async () => {
    const user = userEvent.setup()
    const operations = withProviders({
      listSsoProviders: () =>
        Promise.resolve([
          providerFixture({
            name: 'Acme IdP',
            domains: [
              { domain: 'acme.example', verified: true },
              { domain: 'beta.example', verified: false },
            ],
          }),
        ]),
    })
    renderWithProviders(<SsoPage />, { operations })
    await pickTenant(user)
    await screen.findByText('Acme IdP')
    await pickProvider(user)

    const list = await screen.findByRole('list', {
      name: zhCN.sso.domains.listLabel,
    })
    expect(within(list).getByText('acme.example')).toBeInTheDocument()
    expect(within(list).getByText('beta.example')).toBeInTheDocument()
    expect(
      within(list).getByText(zhCN.sso.domains.state.verified),
    ).toBeInTheDocument()
    expect(
      within(list).getByText(zhCN.sso.domains.state.unverified),
    ).toBeInTheDocument()

    await user.click(
      within(list).getByRole('button', {
        name: text(zhCN.sso.domains.actionsAria, { domain: 'beta.example' }),
      }),
    )
    expect(
      await screen.findByRole('menuitem', { name: zhCN.sso.domains.verify }),
    ).toBeInTheDocument()
    expect(
      screen.getByRole('menuitem', { name: zhCN.sso.domains.remove }),
    ).toBeInTheDocument()
    await user.keyboard('{Escape}')
    await waitForCondition(() => expect(screen.queryByRole('menuitem')).toBeNull())

    await user.click(
      within(list).getByRole('button', {
        name: text(zhCN.sso.domains.actionsAria, { domain: 'acme.example' }),
      }),
    )
    expect(
      await screen.findByRole('menuitem', { name: zhCN.sso.domains.remove }),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('menuitem', { name: zhCN.sso.domains.verify }),
    ).toBeNull()
  })

  it('marks a declared domain verified in place after a successful verify', async () => {
    const user = userEvent.setup()
    let verified = false
    const verifies: string[] = []
    const operations = withProviders({
      listSsoProviders: () =>
        Promise.resolve([
          providerFixture({
            name: 'Acme IdP',
            domains: [{ domain: 'acme.example', verified }],
          }),
        ]),
      verifySsoDomain: (input) => {
        verifies.push(input.domain)
        verified = true
        return Promise.resolve()
      },
    })
    renderWithProviders(<SsoPage />, { operations })
    await pickTenant(user)
    await screen.findByText('Acme IdP')
    await pickProvider(user)

    const before = await screen.findByRole('list', {
      name: zhCN.sso.domains.listLabel,
    })
    expect(
      within(before).getByText(zhCN.sso.domains.state.unverified),
    ).toBeInTheDocument()

    await chooseDomainAction(user, 'acme.example', zhCN.sso.domains.verify)

    await waitForCondition(() => {
      const list = screen.getByRole('list', {
        name: zhCN.sso.domains.listLabel,
      })
      expect(
        within(list).getByText(zhCN.sso.domains.state.verified),
      ).toBeInTheDocument()
      expect(
        within(list).queryByText(zhCN.sso.domains.state.unverified),
      ).toBeNull()
      expect(within(list).getAllByRole('listitem')).toHaveLength(1)
    })
    expect(verifies).toEqual(['acme.example'])
  })

  it('removes a declared domain through its row control', async () => {
    const user = userEvent.setup()
    const removed: string[] = []
    const operations = withProviders({
      listSsoProviders: () =>
        Promise.resolve([
          providerFixture({
            name: 'Acme IdP',
            domains: [{ domain: 'acme.example', verified: true }],
          }),
        ]),
      removeSsoDomain: (input) => {
        removed.push(input.domain)
        return Promise.resolve()
      },
    })
    renderWithProviders(<SsoPage />, { operations })
    await pickTenant(user)
    await screen.findByText('Acme IdP')
    await pickProvider(user)

    await screen.findByRole('list', {
      name: zhCN.sso.domains.listLabel,
    })
    await chooseDomainAction(user, 'acme.example', zhCN.sso.domains.remove)
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: CONFIRM_LABEL }))
    await waitForCondition(() => {
      expect(removed).toEqual(['acme.example'])
    })
  })

  it('shows a claimed domain in the provider domain list', async () => {
    const user = userEvent.setup()
    const claimedDomains: { domain: string; verified: boolean }[] = []
    const operations = withProviders({
      listSsoProviders: () =>
        Promise.resolve([
          providerFixture({ name: 'Acme IdP', domains: [...claimedDomains] }),
        ]),
      claimSsoDomain: (input) => {
        claimedDomains.push({ domain: input.domain, verified: false })
        return Promise.resolve({ verify_token: 'tok-1' })
      },
    })
    renderWithProviders(<SsoPage />, { operations })
    await pickTenant(user)
    await screen.findByText('Acme IdP')
    await pickProvider(user)

    await user.type(
      screen.getByRole('textbox', { name: zhCN.sso.domains.domainLabel }),
      'acme.example',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.sso.domains.claim }),
    )

    const list = await screen.findByRole('list', {
      name: zhCN.sso.domains.listLabel,
    })
    expect(await within(list).findByText('acme.example')).toBeInTheDocument()
    expect(
      within(list).getByText(zhCN.sso.domains.state.unverified),
    ).toBeInTheDocument()
  })

  it('shows the DNS record name and value beside the claim token, each copyable', async () => {
    const user = userEvent.setup()
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { writeText },
    })
    const operations = withProviders({
      claimSsoDomain: () => Promise.resolve({ verify_token: 'tok-1' }),
    })
    renderWithProviders(<SsoPage />, { operations })
    await pickTenant(user)
    await screen.findByText('Acme IdP')
    await pickProvider(user)
    await user.type(
      screen.getByRole('textbox', { name: zhCN.sso.domains.domainLabel }),
      'acme.example',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.sso.domains.claim }),
    )

    const recordName = '_speed-authn.acme.example'
    const recordValue = 'speed-authn=tok-1'
    expect(await screen.findByText(recordName)).toBeInTheDocument()
    expect(screen.getByText(recordValue)).toBeInTheDocument()
    expect(
      screen.getByText(zhCN.sso.domains.claimRecordName),
    ).toBeInTheDocument()
    expect(
      screen.getByText(zhCN.sso.domains.claimRecordValue),
    ).toBeInTheDocument()

    await user.click(
      screen.getByRole('button', {
        name: zhCN.sso.domains.claimRecordNameCopy,
      }),
    )
    await waitForCondition(() => {
      expect(writeText).toHaveBeenLastCalledWith(recordName)
    })
    await user.click(
      screen.getByRole('button', {
        name: zhCN.sso.domains.claimRecordValueCopy,
      }),
    )
    await waitForCondition(() => {
      expect(writeText).toHaveBeenLastCalledWith(recordValue)
    })
  })

  it('hands over the record for the domain the claim stores, without surrounding spaces', async () => {
    const user = userEvent.setup()
    const claims: AdminClaimSsoDomainInput[] = []
    const operations = withProviders({
      claimSsoDomain: (input) => {
        claims.push(input)
        return Promise.resolve({ verify_token: 'tok-1' })
      },
    })
    renderWithProviders(<SsoPage />, { operations })
    await pickTenant(user)
    await screen.findByText('Acme IdP')
    await pickProvider(user)
    await user.type(
      screen.getByRole('textbox', { name: zhCN.sso.domains.domainLabel }),
      '  acme.example  ',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.sso.domains.claim }),
    )

    expect(
      await screen.findByText('_speed-authn.acme.example'),
    ).toBeInTheDocument()
    expect(claims).toEqual([
      { id: 'idp-1', tenant: 'acme', domain: 'acme.example' },
    ])
  })

  it('creates a provider through the bundle', async () => {
    const user = userEvent.setup()
    const created: AdminCreateSsoProviderInput[] = []
    const operations = withProviders({
      createSsoProvider: (input) => {
        created.push(input)
        return Promise.resolve(providerFixture())
      },
    })
    renderWithProviders(<SsoPage />, { operations })
    await pickTenant(user)
    await screen.findByText('Acme IdP')
    await user.type(screen.getByLabelText(zhCN.sso.provider.protocolLabel), 'oidc')
    await user.type(screen.getByLabelText(zhCN.sso.provider.nameLabel), 'New IdP')
    await user.type(screen.getByLabelText(zhCN.sso.provider.provisioningLabel), 'preprovisioned')
    await user.click(screen.getByRole('button', { name: zhCN.sso.provider.createSubmit }))
    await waitForCondition(() => {
      expect(created).toEqual([
        { tenant: 'acme', protocol: 'oidc', name: 'New IdP', provisioning: 'preprovisioned' },
      ])
    })
  })

  it('creates a jit provider that reads the username from a claim', async () => {
    const user = userEvent.setup()
    const created: AdminCreateSsoProviderInput[] = []
    const operations = withProviders({
      createSsoProvider: (input) => {
        created.push(input)
        return Promise.resolve(providerFixture())
      },
    })
    renderWithProviders(<SsoPage />, { operations })
    await pickTenant(user)
    await screen.findByText('Acme IdP')
    await user.type(screen.getByLabelText(zhCN.sso.provider.protocolLabel), 'oidc')
    await user.type(screen.getByLabelText(zhCN.sso.provider.nameLabel), 'New IdP')
    await user.type(screen.getByLabelText(zhCN.sso.provider.provisioningLabel), 'jit')

    await user.click(
      screen.getByRole('combobox', { name: zhCN.sso.provider.usernameStrategyLabel }),
    )
    await user.click(
      await screen.findByRole('option', {
        name: zhCN.sso.provider.usernameStrategy.claim,
      }),
    )

    const claimField = screen.getByRole('combobox', {
      name: zhCN.sso.provider.usernameClaimLabel,
    })
    await user.click(claimField)
    await waitForCondition(() =>
      expect(
        screen.getAllByRole('option').map((option) => option.textContent),
      ).toEqual([
        zhCN.sso.provider.usernameClaim.preferredUsername,
        zhCN.sso.provider.usernameClaim.email,
      ]),
    )
    await user.click(
      screen.getByRole('option', { name: zhCN.sso.provider.usernameClaim.email }),
    )

    await user.click(screen.getByRole('button', { name: zhCN.sso.provider.createSubmit }))
    await waitForCondition(() => {
      expect(created).toEqual([
        {
          tenant: 'acme',
          protocol: 'oidc',
          name: 'New IdP',
          provisioning: 'jit',
          username_strategy: 'claim',
          username_claim: 'email',
        },
      ])
    })
  })

  it('creates a jit provider that asks the user to confirm the username', async () => {
    const user = userEvent.setup()
    const created: AdminCreateSsoProviderInput[] = []
    const operations = withProviders({
      createSsoProvider: (input) => {
        created.push(input)
        return Promise.resolve(providerFixture())
      },
    })
    renderWithProviders(<SsoPage />, { operations })
    await pickTenant(user)
    await screen.findByText('Acme IdP')
    await user.type(screen.getByLabelText(zhCN.sso.provider.protocolLabel), 'oidc')
    await user.type(screen.getByLabelText(zhCN.sso.provider.nameLabel), 'New IdP')
    await user.type(screen.getByLabelText(zhCN.sso.provider.provisioningLabel), 'jit')
    await user.click(screen.getByRole('button', { name: zhCN.sso.provider.createSubmit }))
    await waitForCondition(() => {
      expect(created).toEqual([
        {
          tenant: 'acme',
          protocol: 'oidc',
          name: 'New IdP',
          provisioning: 'jit',
          username_strategy: 'user-confirm',
        },
      ])
    })
  })

  it('clears the create form, the write-only secret included, after a create lands', async () => {
    const user = userEvent.setup()
    const operations = withProviders({
      createSsoProvider: () => Promise.resolve(providerFixture()),
    })
    renderWithProviders(<SsoPage />, { operations })
    await pickTenant(user)
    await screen.findByText('Acme IdP')
    await user.type(screen.getByLabelText(zhCN.sso.provider.protocolLabel), 'oidc')
    await user.type(screen.getByLabelText(zhCN.sso.provider.nameLabel), 'New IdP')
    await user.type(screen.getByLabelText(zhCN.sso.provider.provisioningLabel), 'preprovisioned')
    await user.type(
      screen.getByLabelText(zhCN.sso.provider.configLabel),
      '{{"issuer":"https://idp"}',
    )
    await user.type(screen.getByLabelText(zhCN.sso.provider.clientSecretLabel), 'top-secret')
    await user.click(screen.getByRole('button', { name: zhCN.sso.provider.createSubmit }))

    await waitForCondition(() => {
      expect(screen.getByLabelText(zhCN.sso.provider.protocolLabel)).toHaveValue('')
      expect(screen.getByLabelText(zhCN.sso.provider.nameLabel)).toHaveValue('')
      expect(screen.getByLabelText(zhCN.sso.provider.provisioningLabel)).toHaveValue('')
      expect(screen.getByLabelText(zhCN.sso.provider.configLabel)).toHaveValue('')
      expect(screen.getByLabelText(zhCN.sso.provider.clientSecretLabel)).toHaveValue('')
    })
    expect(screen.getByLabelText(zhCN.sso.provider.enabledLabel)).toHaveTextContent(zhCN.sso.provider.tri.unchanged)
  })

  it('keeps the create form content when the create is refused', async () => {
    const user = userEvent.setup()
    const operations = withProviders({
      createSsoProvider: () =>
        Promise.reject({
          status: 403,
          code: 'authn.forbidden',
          message: 'denied',
          auth: true,
        }),
    })
    renderWithProviders(<SsoPage />, { operations })
    await pickTenant(user)
    await screen.findByText('Acme IdP')
    await user.type(screen.getByLabelText(zhCN.sso.provider.protocolLabel), 'oidc')
    await user.type(screen.getByLabelText(zhCN.sso.provider.nameLabel), 'New IdP')
    await user.type(screen.getByLabelText(zhCN.sso.provider.provisioningLabel), 'preprovisioned')
    await user.type(screen.getByLabelText(zhCN.sso.provider.clientSecretLabel), 'top-secret')
    await user.click(screen.getByRole('button', { name: zhCN.sso.provider.createSubmit }))

    expect(await screen.findByText(zhCN.errors.authn.forbidden)).toBeInTheDocument()
    expect(screen.getByLabelText(zhCN.sso.provider.protocolLabel)).toHaveValue('oidc')
    expect(screen.getByLabelText(zhCN.sso.provider.nameLabel)).toHaveValue('New IdP')
    expect(screen.getByLabelText(zhCN.sso.provider.provisioningLabel)).toHaveValue('preprovisioned')
    expect(screen.getByLabelText(zhCN.sso.provider.clientSecretLabel)).toHaveValue('top-secret')
  })

  it('updates a provider through the bundle', async () => {
    const user = userEvent.setup()
    const updates: AdminWriteSsoProviderInput[] = []
    const operations = withProviders({
      listSsoProviders: () =>
        Promise.resolve([
          providerFixture({ name: 'Acme IdP', provisioning: 'preprovisioned' }),
        ]),
      updateSsoProvider: (input) => {
        updates.push(input)
        return Promise.resolve(providerFixture())
      },
    })
    renderWithProviders(<SsoPage />, { operations })
    await pickTenant(user)
    await screen.findByText('Acme IdP')
    await user.click(screen.getByLabelText(zhCN.sso.provider.editLabel))
    await user.click(await screen.findByRole('option', { name: 'Acme IdP' }))
    const nameField = screen.getByLabelText(zhCN.sso.provider.nameLabel)
    await user.clear(nameField)
    await user.type(nameField, 'Acme IdP 2')
    await user.click(screen.getByRole('button', { name: zhCN.sso.provider.updateSubmit }))
    await waitForCondition(() => {
      expect(updates).toEqual([
        {
          id: 'idp-1',
          tenant: 'acme',
          protocol: 'oidc',
          name: 'Acme IdP 2',
          provisioning: 'preprovisioned',
          username_strategy: '',
          username_claim: '',
        },
      ])
    })
  })

  it('clears the stored claim when an update moves a jit provider to user-confirm', async () => {
    const user = userEvent.setup()
    const updates: AdminWriteSsoProviderInput[] = []
    const operations = withProviders({
      listSsoProviders: () =>
        Promise.resolve([
          providerFixture({
            name: 'Acme IdP',
            provisioning: 'jit',
            username_strategy: 'claim',
            username_claim: 'email',
          }),
        ]),
      updateSsoProvider: (input) => {
        updates.push(input)
        return Promise.resolve(providerFixture())
      },
    })
    renderWithProviders(<SsoPage />, { operations })
    await pickTenant(user)
    await screen.findByText('Acme IdP')
    await user.click(screen.getByLabelText(zhCN.sso.provider.editLabel))
    await user.click(await screen.findByRole('option', { name: 'Acme IdP' }))
    await user.click(
      screen.getByRole('combobox', { name: zhCN.sso.provider.usernameStrategyLabel }),
    )
    await user.click(
      await screen.findByRole('option', {
        name: zhCN.sso.provider.usernameStrategy.userConfirm,
      }),
    )
    await user.click(screen.getByRole('button', { name: zhCN.sso.provider.updateSubmit }))
    await waitForCondition(() => {
      expect(updates).toEqual([
        {
          id: 'idp-1',
          tenant: 'acme',
          protocol: 'oidc',
          name: 'Acme IdP',
          provisioning: 'jit',
          username_strategy: 'user-confirm',
          username_claim: '',
        },
      ])
    })
  })

  it('sends the chosen claim when an update moves a jit provider to the claim strategy', async () => {
    const user = userEvent.setup()
    const updates: AdminWriteSsoProviderInput[] = []
    const operations = withProviders({
      listSsoProviders: () =>
        Promise.resolve([
          providerFixture({
            name: 'Acme IdP',
            provisioning: 'jit',
            username_strategy: 'user-confirm',
            username_claim: '',
          }),
        ]),
      updateSsoProvider: (input) => {
        updates.push(input)
        return Promise.resolve(providerFixture())
      },
    })
    renderWithProviders(<SsoPage />, { operations })
    await pickTenant(user)
    await screen.findByText('Acme IdP')
    await user.click(screen.getByLabelText(zhCN.sso.provider.editLabel))
    await user.click(await screen.findByRole('option', { name: 'Acme IdP' }))
    await user.click(
      screen.getByRole('combobox', { name: zhCN.sso.provider.usernameStrategyLabel }),
    )
    await user.click(
      await screen.findByRole('option', {
        name: zhCN.sso.provider.usernameStrategy.claim,
      }),
    )
    await user.click(
      screen.getByRole('combobox', { name: zhCN.sso.provider.usernameClaimLabel }),
    )
    await user.click(
      await screen.findByRole('option', {
        name: zhCN.sso.provider.usernameClaim.email,
      }),
    )
    await user.click(screen.getByRole('button', { name: zhCN.sso.provider.updateSubmit }))
    await waitForCondition(() => {
      expect(updates).toEqual([
        {
          id: 'idp-1',
          tenant: 'acme',
          protocol: 'oidc',
          name: 'Acme IdP',
          provisioning: 'jit',
          username_strategy: 'claim',
          username_claim: 'email',
        },
      ])
    })
  })

  it('clears both username fields when an update moves a jit provider to preprovisioned', async () => {
    const user = userEvent.setup()
    const updates: AdminWriteSsoProviderInput[] = []
    const operations = withProviders({
      listSsoProviders: () =>
        Promise.resolve([
          providerFixture({
            name: 'Acme IdP',
            provisioning: 'jit',
            username_strategy: 'claim',
            username_claim: 'email',
          }),
        ]),
      updateSsoProvider: (input) => {
        updates.push(input)
        return Promise.resolve(providerFixture())
      },
    })
    renderWithProviders(<SsoPage />, { operations })
    await pickTenant(user)
    await screen.findByText('Acme IdP')
    await user.click(screen.getByLabelText(zhCN.sso.provider.editLabel))
    await user.click(await screen.findByRole('option', { name: 'Acme IdP' }))
    const provisioningField = screen.getByLabelText(
      zhCN.sso.provider.provisioningLabel,
    )
    await user.clear(provisioningField)
    await user.type(provisioningField, 'preprovisioned')
    await user.click(screen.getByRole('button', { name: zhCN.sso.provider.updateSubmit }))
    await waitForCondition(() => {
      expect(updates).toEqual([
        {
          id: 'idp-1',
          tenant: 'acme',
          protocol: 'oidc',
          name: 'Acme IdP',
          provisioning: 'preprovisioned',
          username_strategy: '',
          username_claim: '',
        },
      ])
    })
  })

  it('prefills the edit form with the stored configuration so an unchanged save needs no retype', async () => {
    const user = userEvent.setup()
    const updates: AdminWriteSsoProviderInput[] = []
    const storedConfig = { issuer: 'https://idp.example', client_id: 'acme' }
    const operations = withProviders({
      listSsoProviders: () =>
        Promise.resolve([
          providerFixture({
            name: 'Acme IdP',
            provisioning: 'preprovisioned',
            config: storedConfig,
          }),
        ]),
      updateSsoProvider: (input) => {
        updates.push(input)
        return Promise.resolve(providerFixture({ name: 'Acme IdP' }))
      },
    })
    renderWithProviders(<SsoPage />, { operations })
    await pickTenant(user)
    await screen.findByText('Acme IdP')
    await user.click(screen.getByLabelText(zhCN.sso.provider.editLabel))
    await user.click(await screen.findByRole('option', { name: 'Acme IdP' }))

    expect(screen.getByLabelText(zhCN.sso.provider.configLabel)).toHaveValue(
      JSON.stringify(storedConfig, null, 2),
    )

    await user.click(screen.getByRole('button', { name: zhCN.sso.provider.updateSubmit }))
    await waitForCondition(() => {
      expect(updates).toEqual([
        {
          id: 'idp-1',
          tenant: 'acme',
          protocol: 'oidc',
          name: 'Acme IdP',
          provisioning: 'preprovisioned',
          username_strategy: '',
          username_claim: '',
          config: storedConfig,
        },
      ])
    })
  })

  it('claims a domain through the bundle and shows the token', async () => {
    const user = userEvent.setup()
    const claims: AdminClaimSsoDomainInput[] = []
    const operations = withProviders({
      claimSsoDomain: (input) => {
        claims.push(input)
        return Promise.resolve({ verify_token: 'tok-1' })
      },
    })
    renderWithProviders(<SsoPage />, { operations })
    await pickTenant(user)
    await screen.findByText('Acme IdP')
    await pickProvider(user)
    await user.type(screen.getByRole('textbox', { name: zhCN.sso.domains.domainLabel }), 'acme.com')
    await user.click(screen.getByRole('button', { name: zhCN.sso.domains.claim }))
    await waitForCondition(() => {
      expect(claims).toEqual([{ id: 'idp-1', tenant: 'acme', domain: 'acme.com' }])
    })
    expect(
      await screen.findByText(zhCN.sso.domains.claimToken),
    ).toBeInTheDocument()
  })

  it('renders the retryable error placeholder when the read fails', async () => {
    const user = userEvent.setup()
    const operations = withProviders({
      listSsoProviders: () => Promise.reject(new Error('boom')),
    })
    renderWithProviders(<SsoPage />, { operations })
    await pickTenant(user)
    expect(await screen.findByText(zhCN.sso.error.title)).toBeInTheDocument()
    expect(screen.getByText(zhCN.sso.error.description)).toBeInTheDocument()
  })

  it('renders the permission placeholder with no retry when the read is refused with 403', async () => {
    const user = userEvent.setup()
    const operations = withProviders({
      listSsoProviders: () =>
        Promise.reject({ status: 403, code: 'client.http.403' }),
    })
    renderWithProviders(<SsoPage />, { operations })
    await pickTenant(user)
    expect(await screen.findByText(zhCN.forbidden.title)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: zhCN.sso.retry })).toBeNull()
  })

  it('reports a provider action refusal in the list region and marks the row', async () => {
    const user = userEvent.setup()
    const operations = withProviders({
      refreshSsoMetadata: () =>
        Promise.reject({ status: 403, code: 'authn.forbidden' }),
    })
    renderWithProviders(<SsoPage />, { operations })
    await pickTenant(user)
    await screen.findByText('Acme IdP')
    await chooseProviderAction(user, zhCN.sso.refreshMetadata)

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.errors.authn.forbidden)
    expect(alert.closest('section')).toContainElement(screen.getByRole('table'))
    const row = screen.getByText('Acme IdP').closest('tr')
    expect(row).not.toBeNull()
    const marker = (row as HTMLElement).querySelector(
      '[id^="sso-provider-error-"]',
    )
    expect(marker).not.toBeNull()
    expect(marker).toHaveTextContent(zhCN.errors.authn.forbidden)
  })

  it('offers the enterprise re-login exit for an SSO refusal on a provider-list write', async () => {
    const user = userEvent.setup()
    const session = createStepUpSession()
    const logout = vi.spyOn(session, 'logout').mockResolvedValue(undefined)
    const operations = withProviders({
      refreshSsoMetadata: () =>
        Promise.reject({ status: 401, code: 'authn.sso_required' }),
    })
    renderWithProviders(<SsoPage />, { operations, session })
    await pickTenant(user)
    await screen.findByText('Acme IdP')
    await chooseProviderAction(user, zhCN.sso.refreshMetadata)

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.errors.authn.sso_required)
    expect(screen.getAllByRole('alert')).toHaveLength(1)
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(alert.closest('section')).toContainElement(
      screen.getByRole('table'),
    )
    const relogin = screen.getByRole('button', { name: zhCN.stepUp.relogin })
    await user.click(relogin)
    expect(logout).toHaveBeenCalledTimes(1)
  })

  it('offers the enterprise re-login exit for an SSO refusal on a provider-form write', async () => {
    const user = userEvent.setup()
    const session = createStepUpSession()
    const logout = vi.spyOn(session, 'logout').mockResolvedValue(undefined)
    const operations = withProviders({
      createSsoProvider: () =>
        Promise.reject({ status: 401, code: 'authn.sso_required' }),
    })
    renderWithProviders(<SsoPage />, { operations, session })
    await pickTenant(user)
    await screen.findByText('Acme IdP')
    await user.type(screen.getByLabelText(zhCN.sso.provider.protocolLabel), 'oidc')
    await user.type(screen.getByLabelText(zhCN.sso.provider.nameLabel), 'New IdP')
    await user.type(
      screen.getByLabelText(zhCN.sso.provider.provisioningLabel),
      'preprovisioned',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.sso.provider.createSubmit }),
    )

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.errors.authn.sso_required)
    expect(alert.closest('section')).toContainElement(
      screen.getByLabelText(zhCN.sso.provider.nameLabel),
    )
    const relogin = screen.getByRole('button', { name: zhCN.stepUp.relogin })
    await user.click(relogin)
    expect(logout).toHaveBeenCalledTimes(1)
  })

  it('offers the enterprise re-login exit for an SSO refusal on a domain write', async () => {
    const user = userEvent.setup()
    const session = createStepUpSession()
    const logout = vi.spyOn(session, 'logout').mockResolvedValue(undefined)
    const operations = withProviders({
      claimSsoDomain: () =>
        Promise.reject({ status: 401, code: 'authn.sso_required' }),
    })
    renderWithProviders(<SsoPage />, { operations, session })
    await pickTenant(user)
    await screen.findByText('Acme IdP')
    await pickProvider(user)
    await user.type(
      screen.getByRole('textbox', { name: zhCN.sso.domains.domainLabel }),
      'acme.com',
    )
    await user.click(screen.getByRole('button', { name: zhCN.sso.domains.claim }))

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.errors.authn.sso_required)
    expect(alert.closest('section')).toContainElement(
      screen.getByRole('textbox', { name: zhCN.sso.domains.domainLabel }),
    )
    const relogin = screen.getByRole('button', { name: zhCN.stepUp.relogin })
    await user.click(relogin)
    expect(logout).toHaveBeenCalledTimes(1)
  })

  it('confirms each provider-list action with the provider it changed', async () => {
    const user = userEvent.setup()
    const operations = withProviders({
      refreshSsoMetadata: () => Promise.resolve(),
      reverifySsoDomains: () => Promise.resolve(),
      deleteSsoProvider: () => Promise.resolve(),
    })
    renderWithProviders(<SsoPage />, { operations })
    await pickTenant(user)
    await screen.findByText('Acme IdP')

    await chooseProviderAction(user, zhCN.sso.refreshMetadata)
    expect(
      await screen.findByText(text(zhCN.sso.refreshMetadataSaved, { name: 'Acme IdP' })),
    ).toBeInTheDocument()

    await chooseProviderAction(user, zhCN.sso.reverifyDomains)
    expect(
      await screen.findByText(text(zhCN.sso.reverifyDomainsSaved, { name: 'Acme IdP' })),
    ).toBeInTheDocument()

    await chooseProviderAction(user, zhCN.sso.delete)
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: CONFIRM_LABEL }))
    expect(await screen.findByText(text(zhCN.sso.deleted, { name: 'Acme IdP' }))).toBeInTheDocument()
  })

  it('renders a write refused with a bare 403 as the permission copy, not a status line', async () => {
    const user = userEvent.setup()
    const operations = withProviders({
      refreshSsoMetadata: () =>
        Promise.reject({ status: 403, code: 'client.http.403' }),
    })
    renderWithProviders(<SsoPage />, { operations })
    await pickTenant(user)
    await screen.findByText('Acme IdP')
    await chooseProviderAction(user, zhCN.sso.refreshMetadata)
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.errors.authn.forbidden)
    expect(alert).not.toHaveTextContent('HTTP 403')
  })

  it('lands a create refusal in the form region and a success naming the provider', async () => {
    const user = userEvent.setup()
    let attempts = 0
    const operations = withProviders({
      createSsoProvider: () => {
        attempts += 1
        return attempts === 1
          ? Promise.reject({ status: 403, code: 'authn.forbidden' })
          : Promise.resolve(providerFixture({ name: 'New IdP' }))
      },
    })
    renderWithProviders(<SsoPage />, { operations })
    await pickTenant(user)
    await screen.findByText('Acme IdP')
    await user.type(screen.getByLabelText(zhCN.sso.provider.protocolLabel), 'oidc')
    await user.type(screen.getByLabelText(zhCN.sso.provider.nameLabel), 'New IdP')
    await user.type(screen.getByLabelText(zhCN.sso.provider.provisioningLabel), 'preprovisioned')
    await user.click(screen.getByRole('button', { name: zhCN.sso.provider.createSubmit }))

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.errors.authn.forbidden)
    expect(alert.closest('section')).toContainElement(screen.getByLabelText(zhCN.sso.provider.nameLabel))
    expect(screen.getByLabelText(zhCN.sso.provider.nameLabel)).toHaveValue('New IdP')

    await user.click(screen.getByRole('button', { name: zhCN.sso.provider.createSubmit }))
    expect(await screen.findByText(text(zhCN.sso.provider.created, { name: 'New IdP' }))).toBeInTheDocument()
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('confirms an update with the provider it saved', async () => {
    const user = userEvent.setup()
    const operations = withProviders({
      listSsoProviders: () =>
        Promise.resolve([providerFixture({ name: 'Acme IdP', provisioning: 'preprovisioned' })]),
      updateSsoProvider: () =>
        Promise.resolve(providerFixture({ name: 'Acme IdP 2' })),
    })
    renderWithProviders(<SsoPage />, { operations })
    await pickTenant(user)
    await screen.findByText('Acme IdP')
    await user.click(screen.getByLabelText(zhCN.sso.provider.editLabel))
    await user.click(await screen.findByRole('option', { name: 'Acme IdP' }))
    const nameField = screen.getByLabelText(zhCN.sso.provider.nameLabel)
    await user.clear(nameField)
    await user.type(nameField, 'Acme IdP 2')
    await user.click(screen.getByRole('button', { name: zhCN.sso.provider.updateSubmit }))
    expect(
      await screen.findByText(text(zhCN.sso.provider.updated, { name: 'Acme IdP 2' })),
    ).toBeInTheDocument()
  })

  it('confirms the domain writes with the provider and domain they changed', async () => {
    const user = userEvent.setup()
    const operations = withProviders({
      listSsoProviders: () =>
        Promise.resolve([
          providerFixture({
            name: 'Acme IdP',
            domains: [{ domain: 'acme.com', verified: false }],
          }),
        ]),
      verifySsoDomain: () => Promise.resolve(),
      claimSsoDomain: () => Promise.resolve({ verify_token: 'tok-1' }),
      removeSsoDomain: () => Promise.resolve(),
    })
    renderWithProviders(<SsoPage />, { operations })
    await pickTenant(user)
    await screen.findByText('Acme IdP')
    await pickProvider(user)

    await screen.findByRole('list', {
      name: zhCN.sso.domains.listLabel,
    })
    await chooseDomainAction(user, 'acme.com', zhCN.sso.domains.verify)
    expect(
      await screen.findByText(text(zhCN.sso.domains.verified, { name: 'Acme IdP', domain: 'acme.com' })),
    ).toBeInTheDocument()

    await user.type(
      screen.getByRole('textbox', { name: zhCN.sso.domains.domainLabel }),
      'acme.com',
    )
    await user.click(screen.getByRole('button', { name: zhCN.sso.domains.claim }))
    expect(
      await screen.findByText(text(zhCN.sso.domains.claimed, { name: 'Acme IdP', domain: 'acme.com' })),
    ).toBeInTheDocument()

    await chooseDomainAction(user, 'acme.com', zhCN.sso.domains.remove)
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: CONFIRM_LABEL }))
    expect(
      await screen.findByText(text(zhCN.sso.domains.removed, { name: 'Acme IdP', domain: 'acme.com' })),
    ).toBeInTheDocument()
  })

  it('reports a domain refusal in the domain region', async () => {
    const user = userEvent.setup()
    const operations = withProviders({
      listSsoProviders: () =>
        Promise.resolve([
          providerFixture({
            name: 'Acme IdP',
            domains: [{ domain: 'acme.com', verified: false }],
          }),
        ]),
      verifySsoDomain: () =>
        Promise.reject({ status: 403, code: 'authn.forbidden' }),
    })
    renderWithProviders(<SsoPage />, { operations })
    await pickTenant(user)
    await screen.findByText('Acme IdP')
    await pickProvider(user)

    const list = await screen.findByRole('list', {
      name: zhCN.sso.domains.listLabel,
    })
    await chooseDomainAction(user, 'acme.com', zhCN.sso.domains.verify)

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.errors.authn.forbidden)
    expect(alert.closest('section')).toContainElement(list)
  })

  it('shows the claim token in a named, closable panel that survives a scope reset', async () => {
    const user = userEvent.setup()
    const operations = withProviders({
      listSsoProviders: () =>
        Promise.resolve([
          providerFixture({
            name: 'Acme IdP',
            domains: [{ domain: 'acme.com', verified: false }],
          }),
        ]),
      claimSsoDomain: () => Promise.resolve({ verify_token: 'tok-1' }),
      verifySsoDomain: () =>
        Promise.reject({ status: 403, code: 'authn.forbidden' }),
    })
    renderWithProviders(<SsoPage />, { operations })
    await pickTenant(user)
    await screen.findByText('Acme IdP')
    await pickProvider(user)
    await user.type(screen.getByRole('textbox', { name: zhCN.sso.domains.domainLabel }), 'acme.com')
    await user.click(screen.getByRole('button', { name: zhCN.sso.domains.claim }))

    const token = await screen.findByText('tok-1')
    expect(token.closest('[role="status"]')).not.toBeNull()
    expect(screen.getAllByText('tok-1')).toHaveLength(1)
    expect(screen.getByText(zhCN.sso.domains.claimToken)).toBeInTheDocument()
    await expectNoAxeViolations()

    await chooseDomainAction(user, 'acme.com', zhCN.sso.domains.verify)
    expect(await screen.findByRole('alert')).toHaveTextContent(
      zhCN.errors.authn.forbidden,
    )
    expect(screen.getByText('tok-1')).toBeInTheDocument()

    const close = screen.getByRole('button', {
      name: text(zhCN.sso.domains.claimTokenCloseAria, { tenant: 'acme', name: 'Acme IdP', domain: 'acme.com' }),
    })
    await user.click(close)
    expect(screen.queryByText('tok-1')).toBeNull()
  })

  it('disables the tenant and the driving provider selectors while the form write is pending', async () => {
    const user = userEvent.setup()
    let finish!: (provider: AdminSsoProvider) => void
    const operations = withProviders({
      createSsoProvider: () =>
        new Promise<AdminSsoProvider>((resolve) => {
          finish = resolve
        }),
    })
    renderWithProviders(<SsoPage />, { operations })
    await pickTenant(user)
    await screen.findByText('Acme IdP')
    await user.type(screen.getByLabelText(zhCN.sso.provider.protocolLabel), 'oidc')
    await user.type(screen.getByLabelText(zhCN.sso.provider.nameLabel), 'New IdP')
    await user.type(screen.getByLabelText(zhCN.sso.provider.provisioningLabel), 'preprovisioned')
    await user.click(screen.getByRole('button', { name: zhCN.sso.provider.createSubmit }))

    await waitForCondition(() => {
      expect(screen.getByLabelText(zhCN.common.tenant)).toHaveAttribute(
        'aria-disabled',
        'true',
      )
    })
    expect(screen.getByLabelText(zhCN.sso.provider.editLabel)).toHaveAttribute(
      'aria-disabled',
      'true',
    )
    expect(
      screen.getByLabelText(zhCN.sso.domains.providerLabel),
    ).not.toHaveAttribute('aria-disabled', 'true')

    await act(async () => {
      finish(providerFixture({ name: 'New IdP' }))
    })
    await waitForCondition(() => {
      expect(screen.getByLabelText(zhCN.common.tenant)).not.toHaveAttribute(
        'aria-disabled',
        'true',
      )
    })
    expect(
      screen.getByLabelText(zhCN.sso.provider.editLabel),
    ).not.toHaveAttribute('aria-disabled', 'true')
  })

  it('keeps the claim control unavailable with its reason until a provider and a domain are chosen', async () => {
    const user = userEvent.setup()
    const claimSsoDomain = vi.fn(() => Promise.resolve({ verify_token: 'tok-1' }))
    const operations = withProviders({ claimSsoDomain })
    renderWithProviders(<SsoPage />, { operations })
    await pickTenant(user)
    await screen.findByText('Acme IdP')

    const claim = screen.getByRole('button', { name: zhCN.sso.domains.claim })
    expect(claim).toHaveAttribute('aria-disabled', 'true')
    expect(claim).not.toBeDisabled()
    expect(claim).toHaveAccessibleDescription(
      zhCN.sso.domains.claimUnavailable,
    )

    await user.click(claim)
    await user.keyboard('{Enter}')
    await user.keyboard(' ')
    expect(claimSsoDomain).not.toHaveBeenCalled()

    await pickProvider(user)
    expect(claim).toHaveAttribute('aria-disabled', 'true')

    await user.type(
      screen.getByRole('textbox', { name: zhCN.sso.domains.domainLabel }),
      'acme.com',
    )
    expect(claim).not.toHaveAttribute('aria-disabled', 'true')
  })

  it('keeps the provider form submit unavailable with a reason until its required fields are filled', async () => {
    const user = userEvent.setup()
    const createSsoProvider = vi.fn(() => Promise.resolve(providerFixture()))
    const operations = withProviders({ createSsoProvider })
    renderWithProviders(<SsoPage />, { operations })
    await pickTenant(user)
    await screen.findByText('Acme IdP')

    const submit = screen.getByRole('button', {
      name: zhCN.sso.provider.createSubmit,
    })
    expect(submit).toHaveAttribute('aria-disabled', 'true')
    expect(submit).not.toBeDisabled()
    expect(submit).toHaveAccessibleDescription(
      zhCN.sso.provider.requiredMissing,
    )

    for (const [label, value] of [
      [zhCN.sso.provider.protocolLabel, 'oidc'],
      [zhCN.sso.provider.nameLabel, 'New IdP'],
      [zhCN.sso.provider.provisioningLabel, 'preprovisioned'],
    ] as const) {
      await user.type(screen.getByLabelText(label), value)
    }
    expect(submit).not.toHaveAttribute('aria-disabled', 'true')

    await user.type(
      screen.getByLabelText(zhCN.sso.provider.configLabel),
      '{{',
    )
    expect(submit).toHaveAttribute('aria-disabled', 'true')
    expect(submit).toHaveAccessibleDescription(zhCN.sso.provider.configHelper)
    await user.click(submit)
    expect(createSsoProvider).not.toHaveBeenCalled()
  })

  it('reports the claim control busy while its write is in flight and ignores further activation', async () => {
    const user = userEvent.setup()
    let finish!: (answer: { verify_token: string }) => void
    const claimSsoDomain = vi.fn(
      () =>
        new Promise<{ verify_token: string }>((resolve) => {
          finish = resolve
        }),
    )
    const operations = withProviders({ claimSsoDomain })
    renderWithProviders(<SsoPage />, { operations })
    await pickTenant(user)
    await screen.findByText('Acme IdP')
    await pickProvider(user)
    await user.type(
      screen.getByRole('textbox', { name: zhCN.sso.domains.domainLabel }),
      'acme.com',
    )

    const claim = screen.getByRole('button', { name: zhCN.sso.domains.claim })
    await user.click(claim)
    await waitForCondition(() => {
      expect(claim).toHaveAttribute('aria-busy', 'true')
    })
    expect(claim).toHaveAttribute('aria-disabled', 'true')
    expect(claim).not.toBeDisabled()

    await user.click(claim)
    await user.keyboard('{Enter}')
    expect(claimSsoDomain).toHaveBeenCalledTimes(1)

    await act(async () => {
      finish({ verify_token: 'tok-1' })
    })
  })

  it('locks the scope to a controlled tenant and hides the selector', async () => {
    window.history.replaceState({}, '', '/admin/sso?tenant=globex')
    try {
      const listSsoProviders = vi.fn(() =>
        Promise.resolve([providerFixture({ name: 'Acme IdP' })]),
      )
      const operations = withProviders({ listSsoProviders })
      renderWithProviders(<SsoPage tenant="acme" />, { operations })

      expect(await screen.findByText('Acme IdP')).toBeInTheDocument()
      expect(listSsoProviders).toHaveBeenCalledWith('acme')
      expect(screen.queryByLabelText(zhCN.common.tenant)).toBeNull()
    } finally {
      window.history.replaceState({}, '', '/')
    }
  })

  it('seeds from defaultTenant ahead of the URL and reports a re-selection', async () => {
    window.history.replaceState({}, '', '/admin/sso?tenant=globex')
    try {
      const user = userEvent.setup()
      const listSsoProviders = vi.fn(() =>
        Promise.resolve([providerFixture({ name: 'Acme IdP' })]),
      )
      const onTenantChange = vi.fn()
      const operations = withProviders({
        listTenants: () =>
          Promise.resolve([
            TENANT,
            tenantFixture({ name: 'globex', display_name: 'Globex', status: 'active' }),
          ]),
        listSsoProviders,
      })
      renderWithProviders(
        <SsoPage defaultTenant="acme" onTenantChange={onTenantChange} />,
        { operations },
      )

      expect(await screen.findByText('Acme IdP')).toBeInTheDocument()
      expect(listSsoProviders).toHaveBeenCalledWith('acme')
      await waitForCondition(() => {
        expect(screen.getByLabelText(zhCN.common.tenant)).toHaveTextContent(
          'Acme',
        )
      })

      await user.click(screen.getByLabelText(zhCN.common.tenant))
      await user.click(await screen.findByRole('option', { name: 'Globex' }))
      expect(onTenantChange).toHaveBeenCalledWith('globex')
      await waitForCondition(() => {
        expect(listSsoProviders).toHaveBeenCalledWith('globex')
      })
    } finally {
      window.history.replaceState({}, '', '/')
    }
  })

  it('hides the metadata refresh when its feature is absent and keeps the other row actions', async () => {
    const user = userEvent.setup()
    const operations = withProviders()
    renderWithProviders(<SsoPage />, {
      operations,
      hasFeature: (name) => name !== 'authn.admin.sso.metadata',
    })
    await pickTenant(user)
    await screen.findByText('Acme IdP')

    await openProviderActions(user)
    expect(
      screen.queryByRole('menuitem', { name: zhCN.sso.refreshMetadata }),
    ).toBeNull()
    expect(
      screen.getByRole('menuitem', { name: zhCN.sso.reverifyDomains }),
    ).toBeInTheDocument()
    expect(
      screen.getByRole('menuitem', { name: zhCN.sso.delete }),
    ).toBeInTheDocument()
  })

  it('offers the claim token through a copy control', async () => {
    const user = userEvent.setup()
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { writeText },
    })
    const operations = withProviders({
      claimSsoDomain: () => Promise.resolve({ verify_token: 'tok-1' }),
    })
    renderWithProviders(<SsoPage />, { operations })
    await pickTenant(user)
    await screen.findByText('Acme IdP')
    await pickProvider(user)
    await user.type(screen.getByRole('textbox', { name: zhCN.sso.domains.domainLabel }), 'acme.com')
    await user.click(screen.getByRole('button', { name: zhCN.sso.domains.claim }))
    await screen.findByText('tok-1')

    await user.click(
      screen.getByRole('button', { name: zhCN.sso.domains.claimTokenCopy }),
    )
    await waitForCondition(() => {
      expect(writeText).toHaveBeenCalledWith('tok-1')
    })
  })

  it('has no axe violations', async () => {
    const user = userEvent.setup()
    const operations = withProviders({
      listSsoProviders: () =>
        Promise.resolve([
          providerFixture({
            name: 'Acme IdP',
            domains: [{ domain: 'acme.example', verified: false }],
          }),
        ]),
      claimSsoDomain: () => Promise.resolve({ verify_token: 'tok-1' }),
    })
    renderWithProviders(<SsoPage />, { operations })
    await pickTenant(user)
    await screen.findByText('Acme IdP')
    await pickProvider(user)
    await user.type(
      screen.getByRole('textbox', { name: zhCN.sso.domains.domainLabel }),
      'beta.example',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.sso.domains.claim }),
    )
    await screen.findByText('_speed-authn.beta.example')
    await expectNoAxeViolations()
  })
})
