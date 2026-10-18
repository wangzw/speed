/**
 * TenantDetailPage: one tenant's administration detail.
 *
 * It reads the tenant by id and renders its fields, drives the rename
 * (PATCH) and enable/disable (status) writes, links back to the roster
 * when given the roster's path, splits a refused read into the permission
 * placeholder (403, no retry) or the retryable error placeholder.
 */

import { describe, expect, it, vi } from 'vitest'
import { act, fireEvent, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { TenantDetailPage } from './TenantDetailPage.js'
import type { ConsoleTenant } from './operations.js'
import { renderWithProviders } from '../test-utils/render.js'
import { hasCoarsePointerTouchTarget } from '@speed/test-utils/emitted-css'
import {
  createConsoleOperations,
  makeConsoleBackend,
  tenantFixture,
} from '../test-utils/operations.js'
import { expectNoAxeViolations } from '../test-utils/axe.js'

describe('TenantDetailPage', () => {
  it('links to the tenant-addressed pages carrying this tenant', async () => {
    const operations = createConsoleOperations(makeConsoleBackend())
    renderWithProviders(
      <TenantDetailPage
        id="acme"
        parentHref="/console/tenants"
        rootHref="/console"
      />,
      { operations },
    )
    await screen.findByText('Acme')

    // Every link lands on the page's own path with the tenant in the query
    // parameter those pages read their selection from.
    const expected = [
      ['权限与角色', '/console/permissions?tenant=acme'],
      ['邀请与成员', '/console/invitations?tenant=acme'],
      ['SSO 提供方', '/console/sso?tenant=acme'],
      ['冒充记录', '/console/impersonations?tenant=acme'],
    ] as const
    for (const [name, href] of expected) {
      expect(screen.getByRole('link', { name })).toHaveAttribute('href', href)
    }
  })

  it('hides the related-page links when it has no console root path', async () => {
    const operations = createConsoleOperations(makeConsoleBackend())
    renderWithProviders(<TenantDetailPage id="acme" />, { operations })
    await screen.findByText('Acme')
    expect(
      screen.queryByRole('heading', { level: 2, name: '相关页面' }),
    ).toBeNull()
  })

  it('raises its refresh action to the 44px floor under a coarse pointer', async () => {
    const operations = createConsoleOperations(makeConsoleBackend())
    renderWithProviders(<TenantDetailPage id="acme" />, { operations })
    const refresh = await screen.findByRole('button', { name: '刷新' })
    expect(hasCoarsePointerTouchTarget(refresh)).toBe(true)
  })

  it('renders the tenant fields read by id', async () => {
    const getTenant = vi.fn(() =>
      Promise.resolve(tenantFixture({ id: 'acme', alias: 'Acme', status: 'active' })),
    )
    const operations = createConsoleOperations(makeConsoleBackend({ getTenant }))
    renderWithProviders(<TenantDetailPage id="acme" />, { operations })

    expect(await screen.findByText('Acme')).toBeInTheDocument()
    expect(screen.getByText('已启用')).toBeInTheDocument()
    expect(getTenant).toHaveBeenCalledWith({ id: 'acme' })
  })

  it('renames the tenant through the main-column control', async () => {
    const user = userEvent.setup()
    const calls: string[] = []
    const operations = createConsoleOperations(
      makeConsoleBackend({
        getTenant: () => Promise.resolve(tenantFixture({ id: 'acme', alias: 'Acme' })),
        setTenantAlias: (input) => {
          calls.push(`${input.id}:${input.alias}`)
          return Promise.resolve()
        },
      }),
    )
    renderWithProviders(<TenantDetailPage id="acme" />, { operations })
    await screen.findByText('Acme')

    await user.type(screen.getByLabelText('新别名'), 'Acme-Inc')
    await user.click(screen.getByRole('button', { name: '改名' }))
    expect(calls).toEqual(['acme:Acme-Inc'])
  })

  it('withholds a rename whose alias carries a blank', async () => {
    const user = userEvent.setup()
    const calls: string[] = []
    const operations = createConsoleOperations(
      makeConsoleBackend({
        getTenant: () => Promise.resolve(tenantFixture({ id: 'acme', alias: 'Acme' })),
        setTenantAlias: (input) => {
          calls.push(`${input.id}:${input.alias}`)
          return Promise.resolve()
        },
      }),
    )
    renderWithProviders(<TenantDetailPage id="acme" />, { operations })
    await screen.findByText('Acme')

    const input = screen.getByLabelText('新别名')
    await user.type(input, 'Acme Inc')
    const submit = screen.getByRole('button', { name: '改名' })
    expect(input).toHaveAttribute('aria-invalid', 'true')
    expect(submit).toBeDisabled()
    fireEvent.click(submit)
    // The write is dispatched asynchronously, so a request made from the
    // click has not landed yet in this tick. Flush the queue before
    // claiming none was made; the disabled mirror alone is not proof.
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 0))
    })
    expect(calls).toEqual([])
  })

  it('disables the tenant through the main-column control', async () => {
    const user = userEvent.setup()
    const calls: string[] = []
    const operations = createConsoleOperations(
      makeConsoleBackend({
        getTenant: () => Promise.resolve(tenantFixture({ id: 'acme', status: 'active' })),
        setTenantStatus: (input) => {
          calls.push(`${input.id}:${input.status}`)
          return Promise.resolve()
        },
      }),
    )
    renderWithProviders(<TenantDetailPage id="acme" />, { operations })
    await screen.findByText('已启用')

    await user.click(screen.getByRole('button', { name: '停用' }))
    expect(calls).toEqual(['acme:disabled'])
  })

  it('refreshes the detail read after a status change so the control flips', async () => {
    const user = userEvent.setup()
    let status: ConsoleTenant['status'] = 'active'
    const getTenant = vi.fn(() =>
      Promise.resolve(tenantFixture({ id: 'acme', status })),
    )
    const operations = createConsoleOperations(
      makeConsoleBackend({
        getTenant,
        setTenantStatus: (input) => {
          status = input.status
          return Promise.resolve()
        },
      }),
    )
    renderWithProviders(<TenantDetailPage id="acme" />, { operations })
    await screen.findByText('已启用')

    await user.click(screen.getByRole('button', { name: '停用' }))
    // The write invalidates the detail read, so it refetches and the
    // control re-labels from the settled status (停用 -> 启用).
    expect(await screen.findByRole('button', { name: '启用' })).toBeInTheDocument()
    expect(getTenant.mock.calls.length).toBeGreaterThan(1)
  })

  it('disables the refresh while the status write is in flight', async () => {
    const user = userEvent.setup()
    let settle: (() => void) | undefined
    const operations = createConsoleOperations(
      makeConsoleBackend({
        getTenant: () => Promise.resolve(tenantFixture({ id: 'acme', status: 'active' })),
        setTenantStatus: () =>
          new Promise<void>((resolve) => {
            settle = resolve
          }),
      }),
    )
    renderWithProviders(<TenantDetailPage id="acme" />, { operations })
    await screen.findByText('已启用')

    const refresh = () => screen.getByRole('button', { name: '刷新' })
    expect(refresh()).toBeEnabled()

    await user.click(screen.getByRole('button', { name: '停用' }))
    expect(refresh()).toBeDisabled()
    // A write-pending disable carries no "choose a scope first" description.
    expect(refresh()).not.toHaveAttribute('aria-describedby')

    await act(async () => {
      settle?.()
    })
    expect(await screen.findByRole('button', { name: '刷新' })).toBeEnabled()
  })

  it('disables the refresh while the rename write is in flight', async () => {
    const user = userEvent.setup()
    let settle: (() => void) | undefined
    const operations = createConsoleOperations(
      makeConsoleBackend({
        getTenant: () => Promise.resolve(tenantFixture({ id: 'acme', alias: 'Acme' })),
        setTenantAlias: () =>
          new Promise<void>((resolve) => {
            settle = resolve
          }),
      }),
    )
    renderWithProviders(<TenantDetailPage id="acme" />, { operations })
    await screen.findByText('Acme')

    await user.type(screen.getByLabelText('新别名'), 'Acme-Inc')
    await user.click(screen.getByRole('button', { name: '改名' }))
    const refresh = screen.getByRole('button', { name: '刷新' })
    expect(refresh).toBeDisabled()
    expect(refresh).not.toHaveAttribute('aria-describedby')

    await act(async () => {
      settle?.()
    })
  })

  it('links back to the roster when given the roster path', async () => {
    const operations = createConsoleOperations(makeConsoleBackend())
    renderWithProviders(
      <TenantDetailPage id="acme" parentHref="/console/tenants" />,
      { operations },
    )
    await screen.findByText('Acme')
    expect(screen.getByRole('link', { name: '返回名册' })).toHaveAttribute(
      'href',
      '/console/tenants',
    )
  })

  it('renders the permission placeholder with no retry when the read is refused with 403', async () => {
    const operations = createConsoleOperations(
      makeConsoleBackend({
        getTenant: () => Promise.reject({ status: 403, code: 'client.http.403' }),
      }),
    )
    renderWithProviders(<TenantDetailPage id="acme" />, { operations })
    expect(await screen.findByText('权限不足')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '重试' })).toBeNull()
  })

  it('renders the unavailable placeholder with a retry that re-reads a 404', async () => {
    const user = userEvent.setup()
    let calls = 0
    const getTenant = vi.fn(() => {
      calls += 1
      return calls === 1
        ? Promise.reject({ status: 404, code: 'client.http.404' })
        : Promise.resolve(
            tenantFixture({ id: 'acme', alias: 'Acme', status: 'active' }),
          )
    })
    const operations = createConsoleOperations(makeConsoleBackend({ getTenant }))
    renderWithProviders(<TenantDetailPage id="acme" />, { operations })

    // A bare 404 keeps the wording undecided about deletion ("not found or
    // unavailable") and still offers the retry that checks again.
    expect(await screen.findByText('记录不存在或暂不可用')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '重试' }))
    expect(await screen.findByText('Acme')).toBeInTheDocument()
    expect(getTenant).toHaveBeenCalledTimes(2)
  })

  it('replaces the detail with the unavailable placeholder when a refresh answers 404', async () => {
    const user = userEvent.setup()
    let calls = 0
    const getTenant = vi.fn(() => {
      calls += 1
      return calls === 1
        ? Promise.resolve(
            tenantFixture({ id: 'acme', alias: 'Acme', status: 'active' }),
          )
        : Promise.reject({ status: 404, code: 'client.http.404' })
    })
    const operations = createConsoleOperations(makeConsoleBackend({ getTenant }))
    renderWithProviders(<TenantDetailPage id="acme" />, { operations })
    await screen.findByText('Acme')

    await user.click(screen.getByRole('button', { name: '刷新' }))
    // The 404 makes the older content meaningless: it is replaced, not
    // marked stale.
    expect(await screen.findByText('记录不存在或暂不可用')).toBeInTheDocument()
    expect(screen.queryByText('Acme')).toBeNull()
  })

  it('renders the retryable error placeholder when the read fails otherwise', async () => {
    const operations = createConsoleOperations(
      makeConsoleBackend({
        getTenant: () => Promise.reject(new Error('boom')),
      }),
    )
    renderWithProviders(<TenantDetailPage id="acme" />, { operations })
    expect(await screen.findByText('租户加载失败')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '重试' })).toBeInTheDocument()
  })

  it('has no axe violations', async () => {
    const operations = createConsoleOperations(makeConsoleBackend())
    renderWithProviders(<TenantDetailPage id="acme" />, { operations })
    await screen.findByText('Acme')
    await expectNoAxeViolations()
  })
})
