/**
 * SsoPage: the SSO-provider administration page.
 *
 * It prompts for a tenant until one is chosen, lists a tenant's providers,
 * creates and updates a provider, claims a domain, and drives delete /
 * refresh-metadata / reverify-domains and the per-domain verify and remove
 * through the bundle.
 */

import { afterEach, describe, expect, it, vi } from 'vitest'
import { act, fireEvent, screen, within } from '@testing-library/react'
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
import enUS from './locales/en-US.json' with { type: 'json' }

const TENANT = tenantFixture({ alias: 'acme', displayName: 'Acme', status: 'active' })

/** Fills a locale template's `{{name}}` placeholders. */
function text(template: string, values: Record<string, string>): string {
  return template.replace(/\{\{(\w+)\}\}/g, (match, key: string) =>
    values[key] ?? match,
  )
}

/** The zh-CN confirm label ui-kit's confirm dialog shows. */
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

/** Opens one provider row's action menu. */
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

/** Picks one item from an open provider row action menu. */
async function chooseProviderAction(
  user: ReturnType<typeof userEvent.setup>,
  label: string,
  name = 'Acme IdP',
): Promise<void> {
  await openProviderActions(user, name)
  await user.click(await screen.findByRole('menuitem', { name: label }))
}

/** Picks one item from a declared domain row's action menu. */
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

/** The copy the refresh-distinguishability test reads from either bundle. */
interface RefreshLocaleBundle {
  readonly common: { readonly tenant: string; readonly refresh: string }
  readonly sso: {
    readonly refreshProviderListAria: string
    readonly refreshMetadata: string
    readonly actionsAria: string
  }
}

/**
 * Asserts the page's refresh action and a row's metadata refresh differ in
 * both their visible text and their accessible name, in the given
 * language, and that each accessible name begins with its own visible
 * text. The tenant is selected through bundle copy, so the steps hold in
 * either language.
 */
async function expectDistinguishableRefreshControls(
  language: string,
  bundle: RefreshLocaleBundle,
): Promise<void> {
  const user = userEvent.setup()
  const operations = withProviders()
  renderWithProviders(<SsoPage />, { operations, language })
  await user.click(screen.getByLabelText(bundle.common.tenant))
  await user.click(await screen.findByRole('option', { name: 'Acme' }))
  await screen.findByText('Acme IdP')

  const rowProviderName = 'Acme IdP'
  const pageRefresh = screen.getByRole('button', {
    name: bundle.sso.refreshProviderListAria,
  })
  await user.click(
    screen.getByRole('button', {
      name: bundle.sso.actionsAria.replace('{{name}}', rowProviderName),
    }),
  )
  const rowRefresh = await screen.findByRole('menuitem', {
    name: bundle.sso.refreshMetadata,
  })

  // The two visible texts differ: the row action is never the bare word
  // the page action shows.
  expect(pageRefresh).toHaveTextContent(bundle.common.refresh)
  expect(rowRefresh).toHaveTextContent(bundle.sso.refreshMetadata)
  expect(bundle.sso.refreshMetadata).not.toBe(bundle.common.refresh)

  // Each control's accessible name is its own, so the two never collide.
  expect(pageRefresh).toHaveAccessibleName(bundle.sso.refreshProviderListAria)
  expect(rowRefresh).toHaveAccessibleName(bundle.sso.refreshMetadata)
  expect(
    bundle.sso.refreshProviderListAria.startsWith(bundle.common.refresh),
  ).toBe(true)
}

/** A promise the test settles by hand, for observing an in-flight write. */
function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((settle) => {
    resolve = settle
  })
  return { promise, resolve }
}

/**
 * Asserts the header refresh is inert while a write is in flight: disabled
 * without the empty-scope explanation, and a forced activation issues no
 * provider read.
 */
async function expectRefreshInert(
  listSsoProviders: ReturnType<typeof vi.fn>,
  readsBefore: number,
): Promise<void> {
  const refresh = screen.getByRole('button', { name: zhCN.sso.refreshProviderListAria })
  await vi.waitFor(() => {
    expect(refresh).toBeDisabled()
  })
  expect(refresh).not.toHaveAttribute('aria-describedby')
  fireEvent.click(refresh)
  await act(async () => {
    await Promise.resolve()
  })
  expect(listSsoProviders).toHaveBeenCalledTimes(readsBefore)
}

/** A provider write draft, with every value at the form's initial state. */
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

/**
 * The body a provider write sends. An update names both username fields
 * explicitly, because the module keeps a field the body leaves out; a create
 * names them only under the combination that uses them.
 */
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

  it('does not read an empty tenant scope on refresh', async () => {
    const listSsoProviders = vi.fn(() => Promise.resolve([]))
    const operations = withProviders({ listSsoProviders })
    renderWithProviders(<SsoPage />, { operations })

    // The providers read is disabled while no tenant is chosen; the refresh
    // control follows it instead of issuing the read with an empty tenant.
    const refresh = screen.getByRole('button', { name: zhCN.sso.refreshProviderListAria })
    expect(refresh).toBeDisabled()
    // The disabled control is described by the page's own "choose a tenant
    // first" text.
    const describedBy = refresh.getAttribute('aria-describedby')
    expect(describedBy).not.toBeNull()
    expect(document.getElementById(describedBy as string)).toHaveTextContent(
      zhCN.sso.needTenant.description,
    )
    // A programmatic click, because user-event refuses a disabled target:
    // the control must stay inert even when reached from a script.
    fireEvent.click(refresh)
    await act(async () => {
      await Promise.resolve()
    })
    expect(listSsoProviders).not.toHaveBeenCalled()
  })

  it('disables the refresh while the list scope is in flight', async () => {
    const user = userEvent.setup()
    const pending = deferred<void>()
    const listSsoProviders = vi.fn(() =>
      Promise.resolve([providerFixture({ name: 'Acme IdP' })]),
    )
    const operations = withProviders({
      listSsoProviders,
      refreshSsoMetadata: () => pending.promise,
    })
    renderWithProviders(<SsoPage />, { operations })
    await pickTenant(user)
    await screen.findByText('Acme IdP')
    const reads = listSsoProviders.mock.calls.length

    await chooseProviderAction(user, zhCN.sso.refreshMetadata)
    await expectRefreshInert(listSsoProviders, reads)

    await act(async () => {
      pending.resolve()
    })
  })

  it('disables the refresh while the provider form scope is in flight', async () => {
    const user = userEvent.setup()
    const pending = deferred<void>()
    const listSsoProviders = vi.fn(() =>
      Promise.resolve([providerFixture({ name: 'Acme IdP' })]),
    )
    const operations = withProviders({
      listSsoProviders,
      createSsoProvider: () =>
        pending.promise.then(() => providerFixture({ name: 'New IdP' })),
    })
    renderWithProviders(<SsoPage />, { operations })
    await pickTenant(user)
    await screen.findByText('Acme IdP')
    const reads = listSsoProviders.mock.calls.length

    await user.type(screen.getByLabelText(zhCN.sso.provider.protocolLabel), 'oidc')
    await user.type(screen.getByLabelText(zhCN.sso.provider.nameLabel), 'New IdP')
    await user.type(screen.getByLabelText(zhCN.sso.provider.provisioningLabel), 'preprovisioned')
    await user.click(screen.getByRole('button', { name: zhCN.sso.provider.createSubmit }))
    await expectRefreshInert(listSsoProviders, reads)

    await act(async () => {
      pending.resolve()
    })
  })

  it('disables the refresh while the domain scope is in flight', async () => {
    const user = userEvent.setup()
    const pending = deferred<void>()
    const listSsoProviders = vi.fn(() =>
      Promise.resolve([providerFixture({ name: 'Acme IdP' })]),
    )
    const operations = withProviders({
      listSsoProviders,
      claimSsoDomain: () =>
        pending.promise.then(() => ({ verify_token: 'tok-1' })),
    })
    renderWithProviders(<SsoPage />, { operations })
    await pickTenant(user)
    await screen.findByText('Acme IdP')
    const reads = listSsoProviders.mock.calls.length

    await pickProvider(user)
    await user.type(screen.getByRole('textbox', { name: zhCN.sso.domains.domainLabel }), 'acme.com')
    await user.click(screen.getByRole('button', { name: zhCN.sso.domains.claim }))
    await expectRefreshInert(listSsoProviders, reads)

    await act(async () => {
      pending.resolve()
    })
  })

  it('opens on the tenant named by the query string', async () => {
    window.history.replaceState({}, '', '/admin/sso?tenant=acme')
    try {
      const listSsoProviders = vi.fn(() =>
        Promise.resolve([providerFixture({ name: 'Acme IdP' })]),
      )
      const operations = withProviders({ listSsoProviders })
      renderWithProviders(<SsoPage />, { operations })
      // The link that carried the tenant arrives with a selection already
      // made: the providers read runs for it and the selector shows its
      // alias.
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
    // The provider name is the primary identity: no priority, shared cap.
    // Protocol and enabled stay to `md`, version is the first to go, and
    // the row actions never hide.
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
    // The whole-cell wrapper carries the cap, so the copy control beside
    // the name is counted inside it.
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
    // The write form is mounted with both mutations idle. React Query holds
    // null on an idle mutation, and null means "no error" -- it must not be
    // classified into an unknown-failure banner.
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('raises its compact actions to the 44px floor under a coarse pointer', async () => {
    const user = userEvent.setup()
    const operations = withProviders()
    renderWithProviders(<SsoPage />, { operations })
    await pickTenant(user)
    await screen.findByText('Acme IdP')
    const gated = [
      screen.getByRole('button', { name: zhCN.sso.refreshProviderListAria }),
      screen.getByRole('button', {
        name: text(zhCN.sso.actionsAria, { name: 'Acme IdP' }),
      }),
    ]
    for (const control of gated) {
      expect(hasCoarsePointerTouchTarget(control)).toBe(true)
    }
    // The row's three writes now live in the menu; each item is still a
    // coarse-pointer touch target.
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
    await vi.waitFor(() => {
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
    await vi.waitFor(() => {
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

    // Only an unverified domain offers verification; every declared domain
    // offers removal.
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
    await vi.waitFor(() => expect(screen.queryByRole('menuitem')).toBeNull())

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

    // The write re-reads the provider list, so the same row turns verified
    // without a new one appearing.
    await vi.waitFor(() => {
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
    await vi.waitFor(() => {
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

    // The re-read list carries the new claim, unverified until its record is
    // published and verified.
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

    // The panel hands over the complete record: the operator publishes
    // exactly this name and value and needs nothing else.
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
    await vi.waitFor(() => {
      expect(writeText).toHaveBeenLastCalledWith(recordName)
    })
    await user.click(
      screen.getByRole('button', {
        name: zhCN.sso.domains.claimRecordValueCopy,
      }),
    )
    await vi.waitFor(() => {
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

    // The module stores the trimmed domain, so the published record name
    // must be the trimmed one as well.
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
    await vi.waitFor(() => {
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

    // The claim is a closed set offered as a select, not free text.
    const claimField = screen.getByRole('combobox', {
      name: zhCN.sso.provider.usernameClaimLabel,
    })
    await user.click(claimField)
    expect(
      (await screen.findAllByRole('option')).map((option) => option.textContent),
    ).toEqual([
      zhCN.sso.provider.usernameClaim.preferredUsername,
      zhCN.sso.provider.usernameClaim.email,
    ])
    await user.click(
      screen.getByRole('option', { name: zhCN.sso.provider.usernameClaim.email }),
    )

    await user.click(screen.getByRole('button', { name: zhCN.sso.provider.createSubmit }))
    await vi.waitFor(() => {
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
    await vi.waitFor(() => {
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

    // A landed create returns the form to its initial state: every input is
    // empty, the three-state selects read "unchanged", and the write-only
    // client secret is cleared with the rest.
    await vi.waitFor(() => {
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
    // The refused create keeps every field for correction.
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
    await vi.waitFor(() => {
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
    await vi.waitFor(() => {
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
    await vi.waitFor(() => {
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
    await vi.waitFor(() => {
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

    // The provider read carries the configuration back, so the field starts
    // from it instead of empty: the server refuses an empty config, and the
    // operator should not have to retype one the provider already holds.
    expect(screen.getByLabelText(zhCN.sso.provider.configLabel)).toHaveValue(
      JSON.stringify(storedConfig, null, 2),
    )

    // Saving unchanged succeeds -- no retype -- and the stored configuration
    // travels with the write.
    await user.click(screen.getByRole('button', { name: zhCN.sso.provider.updateSubmit }))
    await vi.waitFor(() => {
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
    await vi.waitFor(() => {
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
    // The result lands under the provider list, not under the write form or
    // the domain bar.
    expect(alert.closest('section')).toContainElement(screen.getByRole('table'))
    // The refused row carries the same resolved text the region shows.
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

    // The tenant admission layer's refusal names the enterprise sign-in and
    // offers the re-login exit in the list region. No local factor dialog
    // opens, so the exit is the only banner on the page.
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
    // The result lands under the write form, not under the provider list or
    // the domain bar.
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
    // The form's own scope: the region sits with the fields, not with the
    // provider list or the domain bar.
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
    // The token is the panel's value line only: the message above it names
    // the DNS step without interpolating the value, so it reads once and the
    // live region announces it once.
    expect(screen.getAllByText('tok-1')).toHaveLength(1)
    expect(screen.getByText(zhCN.sso.domains.claimToken)).toBeInTheDocument()
    await expectNoAxeViolations()

    // A second attempt in the domain scope resets its slot and settles as a
    // failure; the token is page state, not slot state, and stays.
    await chooseDomainAction(user, 'acme.com', zhCN.sso.domains.verify)
    expect(await screen.findByRole('alert')).toHaveTextContent(
      zhCN.errors.authn.forbidden,
    )
    expect(screen.getByText('tok-1')).toBeInTheDocument()

    // The explicit close control names the tenant, provider and domain, and
    // is the only way the value goes away.
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

    await vi.waitFor(() => {
      expect(screen.getByLabelText(zhCN.common.tenant)).toHaveAttribute(
        'aria-disabled',
        'true',
      )
    })
    // The form's own selector follows its scope; the domain bar's selector is
    // a sibling scope and stays operable.
    expect(screen.getByLabelText(zhCN.sso.provider.editLabel)).toHaveAttribute(
      'aria-disabled',
      'true',
    )
    expect(screen.getByLabelText(zhCN.sso.domains.providerLabel)).not.toHaveAttribute(
      'aria-disabled',
      'true',
    )

    await act(async () => {
      finish(providerFixture({ name: 'New IdP' }))
    })
    await vi.waitFor(() => {
      expect(screen.getByLabelText(zhCN.common.tenant)).not.toHaveAttribute(
        'aria-disabled',
        'true',
      )
    })
    expect(screen.getByLabelText(zhCN.sso.provider.editLabel)).not.toHaveAttribute(
      'aria-disabled',
      'true',
    )
  })

  it('marks the refresh control busy while the provider read is in flight', async () => {
    const user = userEvent.setup()
    let calls = 0
    let finishRefresh!: (providers: readonly AdminSsoProvider[]) => void
    const operations = withProviders({
      listSsoProviders: () => {
        calls += 1
        if (calls === 1) {
          return Promise.resolve([providerFixture({ name: 'Acme IdP' })])
        }
        return new Promise<readonly AdminSsoProvider[]>((resolve) => {
          finishRefresh = resolve
        })
      },
    })
    renderWithProviders(<SsoPage />, { operations })
    await pickTenant(user)
    await screen.findByText('Acme IdP')

    await user.click(screen.getByRole('button', { name: zhCN.sso.refreshProviderListAria }))
    const refresh = screen.getByRole('button', { name: zhCN.sso.refreshProviderListAria })
    await vi.waitFor(() => {
      expect(refresh).toHaveAttribute('aria-busy', 'true')
    })
    // A busy refresh stays enabled and keeps keyboard focus: MUI's
    // `loading` would set the native disabled state, which drops focus to
    // the document body until the read settles.
    expect(refresh).not.toBeDisabled()
    expect(refresh).toBe(document.activeElement)
    expect(within(refresh).getByRole('progressbar')).toBeInTheDocument()

    await act(async () => {
      finishRefresh([providerFixture({ name: 'Acme IdP' })])
    })
    await vi.waitFor(() => {
      expect(refresh).not.toHaveAttribute('aria-busy')
    })
    expect(refresh).not.toBeDisabled()
    expect(refresh).toBe(document.activeElement)
  })

  it('re-reads the provider list on refresh', async () => {
    const user = userEvent.setup()
    const listSsoProviders = vi.fn(() =>
      Promise.resolve([providerFixture({ name: 'Acme IdP' })]),
    )
    const operations = withProviders({ listSsoProviders })
    renderWithProviders(<SsoPage />, { operations })
    await pickTenant(user)
    await screen.findByText('Acme IdP')

    const before = listSsoProviders.mock.calls.length
    await user.click(
      screen.getByRole('button', { name: zhCN.sso.refreshProviderListAria }),
    )
    await vi.waitFor(() =>
      expect(listSsoProviders.mock.calls.length).toBe(before + 1),
    )
  })

  it('keeps the two refresh controls distinguishable in zh-CN', async () => {
    await expectDistinguishableRefreshControls('zh-CN', zhCN)
  })

  it('keeps the two refresh controls distinguishable in en-US', async () => {
    await expectDistinguishableRefreshControls('en-US', enUS)
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
            tenantFixture({ alias: 'globex', displayName: 'Globex', status: 'active' }),
          ]),
        listSsoProviders,
      })
      renderWithProviders(
        <SsoPage defaultTenant="acme" onTenantChange={onTenantChange} />,
        { operations },
      )

      expect(await screen.findByText('Acme IdP')).toBeInTheDocument()
      expect(listSsoProviders).toHaveBeenCalledWith('acme')
      await vi.waitFor(() => {
        expect(screen.getByLabelText(zhCN.common.tenant)).toHaveTextContent(
          'Acme',
        )
      })

      await user.click(screen.getByLabelText(zhCN.common.tenant))
      await user.click(await screen.findByRole('option', { name: 'Globex' }))
      expect(onTenantChange).toHaveBeenCalledWith('globex')
      await vi.waitFor(() => {
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
    await vi.waitFor(() => {
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
