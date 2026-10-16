/**
 * TenantDetailPage: one tenant's administration detail.
 *
 * It reads the tenant by id and renders its fields, drives the rename
 * (PATCH) and enable/disable (status) writes, links back to the roster
 * when given the roster's path, splits a refused read into the permission
 * placeholder (403, no retry) or the retryable error placeholder.
 */

import { describe, expect, it, vi } from 'vitest'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { TenantDetailPage } from './TenantDetailPage.js'
import type { ConsoleTenant } from './operations.js'
import { renderWithProviders } from '../test-utils/render.js'
import {
  createConsoleOperations,
  makeConsoleBackend,
  tenantFixture,
} from '../test-utils/operations.js'
import { expectNoAxeViolations } from '../test-utils/axe.js'

describe('TenantDetailPage', () => {
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

    await user.type(screen.getByLabelText('新别名'), 'Acme Inc')
    await user.click(screen.getByRole('button', { name: '改名' }))
    expect(calls).toEqual(['acme:Acme Inc'])
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
