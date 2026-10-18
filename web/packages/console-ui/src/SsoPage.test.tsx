/**
 * SsoPage: the SSO-provider administration page.
 *
 * It prompts for a tenant until one is chosen, lists a tenant's providers,
 * creates and updates a provider, claims a domain, and drives delete /
 * refresh-metadata / reverify-domains and the per-domain verify and remove
 * through the bundle.
 */

import { describe, expect, it, vi } from 'vitest'
import { act, fireEvent, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { SsoPage } from './SsoPage.js'
import type {
  ConsoleClaimSsoDomainInput,
  ConsoleCreateSsoProviderInput,
  ConsoleSsoProvider,
  ConsoleWriteSsoProviderInput,
} from './operations.js'
import { renderWithProviders } from '../test-utils/render.js'
import {
  columnVisibility,
  containerMaxWidthOf,
} from '../test-utils/container-visibility.js'
import { hasCoarsePointerTouchTarget } from '@speed/test-utils/emitted-css'
import {
  createConsoleOperations,
  makeConsoleBackend,
  providerFixture,
  type ConsoleBackend,
} from '../test-utils/operations.js'
import { expectNoAxeViolations } from '../test-utils/axe.js'
import zhCN from './locales/zh-CN.json' with { type: 'json' }

const TENANT = { id: 'acme', alias: 'Acme', status: 'active' as const }

async function pickTenant(user: ReturnType<typeof userEvent.setup>): Promise<void> {
  await user.click(screen.getByLabelText('租户'))
  await user.click(await screen.findByRole('option', { name: 'Acme' }))
}

function withProviders(
  overrides: Partial<ConsoleBackend> = {},
): ReturnType<typeof createConsoleOperations> {
  return createConsoleOperations(
    makeConsoleBackend({
      listTenants: () => Promise.resolve([TENANT]),
      listSsoProviders: () =>
        Promise.resolve([providerFixture({ name: 'Acme IdP' })]),
      ...overrides,
    }),
  )
}

async function pickProvider(user: ReturnType<typeof userEvent.setup>): Promise<void> {
  await user.click(screen.getByLabelText('提供方'))
  await user.click(await screen.findByRole('option', { name: 'Acme IdP' }))
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
  const refresh = screen.getByRole('button', { name: '刷新' })
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

describe('SsoPage', () => {
  it('prompts for a tenant before listing', () => {
    const operations = withProviders()
    renderWithProviders(<SsoPage />, { operations })
    expect(screen.getByText('请选择租户')).toBeInTheDocument()
  })

  it('does not read an empty tenant scope on refresh', async () => {
    const listSsoProviders = vi.fn(() => Promise.resolve([]))
    const operations = withProviders({ listSsoProviders })
    renderWithProviders(<SsoPage />, { operations })

    // The providers read is disabled while no tenant is chosen; the refresh
    // control follows it instead of issuing the read with an empty tenant.
    const refresh = screen.getByRole('button', { name: '刷新' })
    expect(refresh).toBeDisabled()
    // The disabled control is described by the page's own "choose a tenant
    // first" text.
    const describedBy = refresh.getAttribute('aria-describedby')
    expect(describedBy).not.toBeNull()
    expect(document.getElementById(describedBy as string)).toHaveTextContent(
      'SSO 提供方按租户存储，请先选择一个租户。',
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

    await user.click(
      screen.getByRole('button', { name: '刷新元数据（Acme IdP）' }),
    )
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

    await user.type(screen.getByLabelText('协议'), 'oidc')
    await user.type(screen.getByLabelText('名称'), 'New IdP')
    await user.type(screen.getByLabelText('开通方式'), 'manual')
    await user.click(screen.getByRole('button', { name: '新建提供方' }))
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
    await user.type(screen.getByLabelText('域名'), 'acme.com')
    await user.click(screen.getByRole('button', { name: '认领' }))
    await expectRefreshInert(listSsoProviders, reads)

    await act(async () => {
      pending.resolve()
    })
  })

  it('opens on the tenant named by the query string', async () => {
    window.history.replaceState({}, '', '/console/sso?tenant=acme')
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
      expect(screen.queryByText('请选择租户')).toBeNull()
      expect(screen.getByLabelText('租户')).toHaveTextContent('Acme')
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
    expect(columnVisibility(header('名称'))).toBeNull()
    expect(columnVisibility(header('协议'))).toEqual({
      hiddenAt: 0,
      visibleFrom: 900,
    })
    expect(columnVisibility(header('已启用'))).toEqual({
      hiddenAt: 0,
      visibleFrom: 900,
    })
    expect(columnVisibility(header('版本'))).toEqual({
      hiddenAt: 0,
      visibleFrom: 1200,
    })
    expect(columnVisibility(header('操作'))).toBeNull()

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
      screen.getByRole('button', { name: '刷新' }),
      screen.getByRole('button', { name: '刷新元数据（Acme IdP）' }),
      screen.getByRole('button', { name: '重新验证域名（Acme IdP）' }),
      screen.getByRole('button', { name: '删除提供方 Acme IdP' }),
    ]
    for (const control of gated) {
      expect(hasCoarsePointerTouchTarget(control)).toBe(true)
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
    await user.click(screen.getByRole('button', { name: '删除提供方 Acme IdP' }))
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: '确认' }))
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
    await user.click(screen.getByRole('button', { name: '刷新元数据（Acme IdP）' }))
    await user.click(screen.getByRole('button', { name: '重新验证域名（Acme IdP）' }))
    await vi.waitFor(() => {
      expect(refreshed).toEqual(['idp-1'])
      expect(reverified).toEqual(['idp-1'])
    })
  })

  it('verifies and removes a named domain through the bundle', async () => {
    const user = userEvent.setup()
    const verified: string[] = []
    const removed: string[] = []
    const operations = withProviders({
      verifySsoDomain: (input) => {
        verified.push(input.domain)
        return Promise.resolve()
      },
      removeSsoDomain: (input) => {
        removed.push(input.domain)
        return Promise.resolve()
      },
    })
    renderWithProviders(<SsoPage />, { operations })
    await pickTenant(user)
    await screen.findByText('Acme IdP')
    await pickProvider(user)
    await user.type(screen.getByLabelText('域名'), 'acme.com')
    await user.click(screen.getByRole('button', { name: '验证' }))
    await user.click(screen.getByRole('button', { name: '移除' }))
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: '确认' }))
    await vi.waitFor(() => {
      expect(verified).toEqual(['acme.com'])
      expect(removed).toEqual(['acme.com'])
    })
  })

  it('creates a provider through the bundle', async () => {
    const user = userEvent.setup()
    const created: ConsoleCreateSsoProviderInput[] = []
    const operations = withProviders({
      createSsoProvider: (input) => {
        created.push(input)
        return Promise.resolve(providerFixture())
      },
    })
    renderWithProviders(<SsoPage />, { operations })
    await pickTenant(user)
    await screen.findByText('Acme IdP')
    await user.type(screen.getByLabelText('协议'), 'oidc')
    await user.type(screen.getByLabelText('名称'), 'New IdP')
    await user.type(screen.getByLabelText('开通方式'), 'manual')
    await user.click(screen.getByRole('button', { name: '新建提供方' }))
    await vi.waitFor(() => {
      expect(created).toEqual([
        { tenant: 'acme', protocol: 'oidc', name: 'New IdP', provisioning: 'manual' },
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
    await user.type(screen.getByLabelText('协议'), 'oidc')
    await user.type(screen.getByLabelText('名称'), 'New IdP')
    await user.type(screen.getByLabelText('开通方式'), 'manual')
    await user.type(
      screen.getByLabelText('配置（JSON）'),
      '{{"issuer":"https://idp"}',
    )
    await user.type(screen.getByLabelText('客户端密钥'), 'top-secret')
    await user.click(screen.getByRole('button', { name: '新建提供方' }))

    // A landed create returns the form to its initial state: every input is
    // empty, the three-state selects read "unchanged", and the write-only
    // client secret is cleared with the rest.
    await vi.waitFor(() => {
      expect(screen.getByLabelText('协议')).toHaveValue('')
      expect(screen.getByLabelText('名称')).toHaveValue('')
      expect(screen.getByLabelText('开通方式')).toHaveValue('')
      expect(screen.getByLabelText('配置（JSON）')).toHaveValue('')
      expect(screen.getByLabelText('客户端密钥')).toHaveValue('')
    })
    expect(screen.getByLabelText('已启用')).toHaveTextContent('保持不变')
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
    await user.type(screen.getByLabelText('协议'), 'oidc')
    await user.type(screen.getByLabelText('名称'), 'New IdP')
    await user.type(screen.getByLabelText('开通方式'), 'manual')
    await user.type(screen.getByLabelText('客户端密钥'), 'top-secret')
    await user.click(screen.getByRole('button', { name: '新建提供方' }))

    expect(await screen.findByText('你没有执行此操作的权限。')).toBeInTheDocument()
    // The refused create keeps every field for correction.
    expect(screen.getByLabelText('协议')).toHaveValue('oidc')
    expect(screen.getByLabelText('名称')).toHaveValue('New IdP')
    expect(screen.getByLabelText('开通方式')).toHaveValue('manual')
    expect(screen.getByLabelText('客户端密钥')).toHaveValue('top-secret')
  })

  it('updates a provider through the bundle', async () => {
    const user = userEvent.setup()
    const updates: ConsoleWriteSsoProviderInput[] = []
    const operations = withProviders({
      listSsoProviders: () =>
        Promise.resolve([
          providerFixture({ name: 'Acme IdP', provisioning: 'manual' }),
        ]),
      updateSsoProvider: (input) => {
        updates.push(input)
        return Promise.resolve(providerFixture())
      },
    })
    renderWithProviders(<SsoPage />, { operations })
    await pickTenant(user)
    await screen.findByText('Acme IdP')
    await user.click(screen.getByLabelText('选择提供方'))
    await user.click(await screen.findByRole('option', { name: 'Acme IdP' }))
    const nameField = screen.getByLabelText('名称')
    await user.clear(nameField)
    await user.type(nameField, 'Acme IdP 2')
    await user.click(screen.getByRole('button', { name: '保存提供方' }))
    await vi.waitFor(() => {
      expect(updates).toEqual([
        {
          id: 'idp-1',
          tenant: 'acme',
          protocol: 'oidc',
          name: 'Acme IdP 2',
          provisioning: 'manual',
        },
      ])
    })
  })

  it('claims a domain through the bundle and shows the token', async () => {
    const user = userEvent.setup()
    const claims: ConsoleClaimSsoDomainInput[] = []
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
    await user.type(screen.getByLabelText('域名'), 'acme.com')
    await user.click(screen.getByRole('button', { name: '认领' }))
    await vi.waitFor(() => {
      expect(claims).toEqual([{ id: 'idp-1', tenant: 'acme', domain: 'acme.com' }])
    })
    expect(
      await screen.findByText('请在 DNS 中发布该令牌：tok-1'),
    ).toBeInTheDocument()
  })

  it('renders the retryable error placeholder when the read fails', async () => {
    const user = userEvent.setup()
    const operations = withProviders({
      listSsoProviders: () => Promise.reject(new Error('boom')),
    })
    renderWithProviders(<SsoPage />, { operations })
    await pickTenant(user)
    expect(await screen.findByText('提供方加载失败')).toBeInTheDocument()
    expect(screen.getByText('SSO 面未能完成这次请求。')).toBeInTheDocument()
  })

  it('renders the permission placeholder with no retry when the read is refused with 403', async () => {
    const user = userEvent.setup()
    const operations = withProviders({
      listSsoProviders: () =>
        Promise.reject({ status: 403, code: 'client.http.403' }),
    })
    renderWithProviders(<SsoPage />, { operations })
    await pickTenant(user)
    expect(await screen.findByText('权限不足')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '重试' })).toBeNull()
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
    await user.click(screen.getByRole('button', { name: '刷新元数据（Acme IdP）' }))

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('你没有执行此操作的权限。')
    // The result lands under the provider list, not under the write form or
    // the domain bar.
    expect(alert.closest('section')).toContainElement(screen.getByRole('table'))
    // The refused row control carries the same resolved text the region shows.
    const button = screen.getByRole('button', { name: '刷新元数据（Acme IdP）' })
    expect(button).toHaveAttribute('aria-invalid', 'true')
    const describedBy = button.getAttribute('aria-describedby')
    expect(describedBy).not.toBeNull()
    expect(document.getElementById(describedBy ?? '')).toHaveTextContent(
      '你没有执行此操作的权限。',
    )
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

    await user.click(screen.getByRole('button', { name: '刷新元数据（Acme IdP）' }))
    expect(
      await screen.findByText('已刷新提供方「Acme IdP」的元数据'),
    ).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: '重新验证域名（Acme IdP）' }))
    expect(
      await screen.findByText('已重新验证提供方「Acme IdP」的域名'),
    ).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: '删除提供方 Acme IdP' }))
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: '确认' }))
    expect(await screen.findByText('已删除提供方「Acme IdP」')).toBeInTheDocument()
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
    await user.click(screen.getByRole('button', { name: '刷新元数据（Acme IdP）' }))
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('你没有执行此操作的权限。')
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
    await user.type(screen.getByLabelText('协议'), 'oidc')
    await user.type(screen.getByLabelText('名称'), 'New IdP')
    await user.type(screen.getByLabelText('开通方式'), 'manual')
    await user.click(screen.getByRole('button', { name: '新建提供方' }))

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('你没有执行此操作的权限。')
    // The form's own scope: the region sits with the fields, not with the
    // provider list or the domain bar.
    expect(alert.closest('section')).toContainElement(screen.getByLabelText('名称'))
    expect(screen.getByLabelText('名称')).toHaveValue('New IdP')

    await user.click(screen.getByRole('button', { name: '新建提供方' }))
    expect(await screen.findByText('已创建提供方「New IdP」')).toBeInTheDocument()
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('confirms an update with the provider it saved', async () => {
    const user = userEvent.setup()
    const operations = withProviders({
      listSsoProviders: () =>
        Promise.resolve([providerFixture({ name: 'Acme IdP', provisioning: 'manual' })]),
      updateSsoProvider: () =>
        Promise.resolve(providerFixture({ name: 'Acme IdP 2' })),
    })
    renderWithProviders(<SsoPage />, { operations })
    await pickTenant(user)
    await screen.findByText('Acme IdP')
    await user.click(screen.getByLabelText('选择提供方'))
    await user.click(await screen.findByRole('option', { name: 'Acme IdP' }))
    const nameField = screen.getByLabelText('名称')
    await user.clear(nameField)
    await user.type(nameField, 'Acme IdP 2')
    await user.click(screen.getByRole('button', { name: '保存提供方' }))
    expect(
      await screen.findByText('已保存提供方「Acme IdP 2」'),
    ).toBeInTheDocument()
  })

  it('confirms the domain writes with the provider and domain they changed', async () => {
    const user = userEvent.setup()
    const operations = withProviders({
      verifySsoDomain: () => Promise.resolve(),
      claimSsoDomain: () => Promise.resolve({ verify_token: 'tok-1' }),
      removeSsoDomain: () => Promise.resolve(),
    })
    renderWithProviders(<SsoPage />, { operations })
    await pickTenant(user)
    await screen.findByText('Acme IdP')
    await pickProvider(user)
    await user.type(screen.getByLabelText('域名'), 'acme.com')

    await user.click(screen.getByRole('button', { name: '验证' }))
    expect(
      await screen.findByText('已验证提供方「Acme IdP」的域名「acme.com」'),
    ).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: '认领' }))
    expect(
      await screen.findByText('已为提供方「Acme IdP」认领域名「acme.com」'),
    ).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: '移除' }))
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: '确认' }))
    expect(
      await screen.findByText('已从提供方「Acme IdP」移除域名「acme.com」'),
    ).toBeInTheDocument()
  })

  it('reports a domain refusal in the domain region', async () => {
    const user = userEvent.setup()
    const operations = withProviders({
      verifySsoDomain: () =>
        Promise.reject({ status: 403, code: 'authn.forbidden' }),
    })
    renderWithProviders(<SsoPage />, { operations })
    await pickTenant(user)
    await screen.findByText('Acme IdP')
    await pickProvider(user)
    await user.type(screen.getByLabelText('域名'), 'acme.com')
    await user.click(screen.getByRole('button', { name: '验证' }))

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('你没有执行此操作的权限。')
    expect(alert.closest('section')).toContainElement(
      screen.getByLabelText('域名'),
    )
  })

  it('shows the claim token in a named, closable panel that survives a scope reset', async () => {
    const user = userEvent.setup()
    const operations = withProviders({
      claimSsoDomain: () => Promise.resolve({ verify_token: 'tok-1' }),
      verifySsoDomain: () =>
        Promise.reject({ status: 403, code: 'authn.forbidden' }),
    })
    renderWithProviders(<SsoPage />, { operations })
    await pickTenant(user)
    await screen.findByText('Acme IdP')
    await pickProvider(user)
    await user.type(screen.getByLabelText('域名'), 'acme.com')
    await user.click(screen.getByRole('button', { name: '认领' }))

    const token = await screen.findByText('请在 DNS 中发布该令牌：tok-1')
    expect(token.closest('[role="status"]')).not.toBeNull()
    await expectNoAxeViolations()

    // A second attempt in the domain scope resets its slot and settles as a
    // failure; the token is page state, not slot state, and stays.
    await user.click(screen.getByRole('button', { name: '验证' }))
    expect(await screen.findByRole('alert')).toHaveTextContent(
      '你没有执行此操作的权限。',
    )
    expect(screen.getByText('请在 DNS 中发布该令牌：tok-1')).toBeInTheDocument()

    // The explicit close control names the tenant, provider and domain, and
    // is the only way the value goes away.
    const close = screen.getByRole('button', {
      name: '关闭租户「acme」中提供方「Acme IdP」域名「acme.com」的认领令牌',
    })
    await user.click(close)
    expect(screen.queryByText('请在 DNS 中发布该令牌：tok-1')).toBeNull()
  })

  it('disables the tenant and the driving provider selectors while the form write is pending', async () => {
    const user = userEvent.setup()
    let finish!: (provider: ConsoleSsoProvider) => void
    const operations = withProviders({
      createSsoProvider: () =>
        new Promise<ConsoleSsoProvider>((resolve) => {
          finish = resolve
        }),
    })
    renderWithProviders(<SsoPage />, { operations })
    await pickTenant(user)
    await screen.findByText('Acme IdP')
    await user.type(screen.getByLabelText('协议'), 'oidc')
    await user.type(screen.getByLabelText('名称'), 'New IdP')
    await user.type(screen.getByLabelText('开通方式'), 'manual')
    await user.click(screen.getByRole('button', { name: '新建提供方' }))

    await vi.waitFor(() => {
      expect(screen.getByLabelText('租户')).toHaveAttribute(
        'aria-disabled',
        'true',
      )
    })
    // The form's own selector follows its scope; the domain bar's selector is
    // a sibling scope and stays operable.
    expect(screen.getByLabelText('选择提供方')).toHaveAttribute(
      'aria-disabled',
      'true',
    )
    expect(screen.getByLabelText('提供方')).not.toHaveAttribute(
      'aria-disabled',
      'true',
    )

    await act(async () => {
      finish(providerFixture({ name: 'New IdP' }))
    })
    await vi.waitFor(() => {
      expect(screen.getByLabelText('租户')).not.toHaveAttribute(
        'aria-disabled',
        'true',
      )
    })
    expect(screen.getByLabelText('选择提供方')).not.toHaveAttribute(
      'aria-disabled',
      'true',
    )
  })

  it('marks the refresh control busy while the provider read is in flight', async () => {
    const user = userEvent.setup()
    let calls = 0
    let finishRefresh!: (providers: readonly ConsoleSsoProvider[]) => void
    const operations = withProviders({
      listSsoProviders: () => {
        calls += 1
        if (calls === 1) {
          return Promise.resolve([providerFixture({ name: 'Acme IdP' })])
        }
        return new Promise<readonly ConsoleSsoProvider[]>((resolve) => {
          finishRefresh = resolve
        })
      },
    })
    renderWithProviders(<SsoPage />, { operations })
    await pickTenant(user)
    await screen.findByText('Acme IdP')

    await user.click(screen.getByRole('button', { name: '刷新' }))
    const refresh = screen.getByRole('button', { name: '刷新' })
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
      screen.getByRole('button', { name: zhCN.common.refresh }),
    )
    await vi.waitFor(() =>
      expect(listSsoProviders.mock.calls.length).toBe(before + 1),
    )
  })

  it('has no axe violations', async () => {
    const user = userEvent.setup()
    const operations = withProviders()
    renderWithProviders(<SsoPage />, { operations })
    await pickTenant(user)
    await screen.findByText('Acme IdP')
    await expectNoAxeViolations()
  })
})
