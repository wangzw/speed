/**
 * DashboardPage: the console landing.
 *
 * It derives its KPI row from the roster read (a count of tenants and the
 * active/disabled split) and its recent-activity list from the audit
 * query's newest page. Each panel renders through the console async
 * section, so a refused read (403) shows the permission placeholder
 * without a retry and any other failure the retryable error placeholder.
 */

import { describe, expect, it } from 'vitest'
import { screen, within } from '@testing-library/react'
import { DashboardPage } from './DashboardPage.js'
import { renderWithProviders } from '../test-utils/render.js'
import {
  columnVisibility,
  containerMaxWidthOf,
} from '../test-utils/container-visibility.js'
import { hasCoarsePointerTouchTarget } from '@speed/test-utils/emitted-css'
import {
  accountFixture,
  createConsoleOperations,
  makeConsoleBackend,
  recordFixture,
} from '../test-utils/operations.js'
import { expectNoAxeViolations } from '../test-utils/axe.js'

const TENANTS = [
  { id: 'acme', alias: 'Acme', status: 'active' as const },
  { id: 'beta', alias: 'Beta', status: 'active' as const },
  { id: 'old', alias: 'Old', status: 'disabled' as const },
]

describe('DashboardPage', () => {
  it('raises its refresh action to the 44px floor under a coarse pointer', async () => {
    const operations = createConsoleOperations(
      makeConsoleBackend({ listTenants: () => Promise.resolve(TENANTS) }),
    )
    renderWithProviders(<DashboardPage />, { operations })
    const refresh = await screen.findByRole('button', { name: '刷新' })
    expect(hasCoarsePointerTouchTarget(refresh)).toBe(true)
  })

  it('summarizes the roster in the KPI row', async () => {
    const operations = createConsoleOperations(
      makeConsoleBackend({ listTenants: () => Promise.resolve(TENANTS) }),
    )
    renderWithProviders(<DashboardPage />, { operations })

    expect(await screen.findByText('租户总数')).toBeInTheDocument()
    // total / active / disabled, derived from the whole roster read.
    expect(screen.getByText('3')).toBeInTheDocument()
    expect(screen.getByText('2')).toBeInTheDocument()
    expect(screen.getByText('1')).toBeInTheDocument()
  })

  it('lists the newest audit activity', async () => {
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listRecords: () =>
          Promise.resolve({
            entries: [recordFixture({ id: 'rec-9', action: 'tenant.read' })],
            next: '',
          }),
      }),
    )
    renderWithProviders(<DashboardPage />, { operations })
    const recent = await screen.findByRole('heading', { level: 2, name: '最近活动' })
    expect(recent).toBeInTheDocument()
    expect(await screen.findByText('tenant.read')).toBeInTheDocument()
  })

  it('renders the recent activity actor as the account display name', async () => {
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listRecords: () =>
          Promise.resolve({
            entries: [recordFixture({ id: 'rec-9' })],
            next: '',
          }),
        getAccount: () =>
          Promise.resolve(accountFixture({ display_name: 'Ada Lovelace' })),
      }),
    )
    renderWithProviders(<DashboardPage />, { operations })
    expect(await screen.findByText('Ada Lovelace')).toBeInTheDocument()
    expect(screen.queryByText('acc-1')).not.toBeInTheDocument()
  })

  it('marks a recent activity actor whose account read is refused as unresolved', async () => {
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listRecords: () =>
          Promise.resolve({
            entries: [recordFixture({ id: 'rec-9' })],
            next: '',
          }),
        getAccount: () => Promise.reject(new Error('forbidden')),
      }),
    )
    renderWithProviders(<DashboardPage />, { operations })
    // The cell keeps the recorded id but labels it unresolved rather than
    // passing it off as a resolved (or system) actor.
    const cell = await screen.findByLabelText(/身份未解析/)
    expect(cell).toHaveTextContent('acc-1')
    expect(cell).toHaveAttribute('title', '身份未解析（需账户读取权限）')
  })

  it('renders recent activity with the audit page columns and priorities', async () => {
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listRecords: () =>
          Promise.resolve({
            entries: [recordFixture({ id: 'rec-9', action: 'tenant.read' })],
            next: '',
          }),
      }),
    )
    renderWithProviders(<DashboardPage />, { operations })
    await screen.findByText('tenant.read')
    // The list carries the same set as the audit query page: action and
    // object identify the record, so the object column outranks the actor
    // and the outcome, which share the md tier so a failure stays visible.
    expect(
      columnVisibility(screen.getByRole('columnheader', { name: '对象' })),
    ).toEqual({ hiddenAt: 0, visibleFrom: 600 })
    // The body cell carries its own copy of the rule; assert it too so a
    // header/body divergence cannot pass with only the header checked.
    expect(
      columnVisibility(screen.getByText('acme').closest('td')!),
    ).toEqual({ hiddenAt: 0, visibleFrom: 600 })
    expect(
      columnVisibility(screen.getByRole('columnheader', { name: '操作者' })),
    ).toEqual({ hiddenAt: 0, visibleFrom: 900 })
    expect(
      columnVisibility(screen.getByRole('columnheader', { name: '结果' })),
    ).toEqual({ hiddenAt: 0, visibleFrom: 900 })
    expect(
      columnVisibility(screen.getByRole('columnheader', { name: '时间' })),
    ).toBeNull()
    expect(
      columnVisibility(screen.getByRole('columnheader', { name: '动作' })),
    ).toBeNull()
  })

  it('caps the recent-activity long-text columns like the audit page', async () => {
    const action = 'pkg.objectstore.metadata.create'
    const object = '/api/v1/objects/a-very-long-registered-route'
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listRecords: () =>
          Promise.resolve({
            entries: [recordFixture({ id: 'rec-9', action, object })],
            next: '',
          }),
      }),
    )
    renderWithProviders(<DashboardPage />, { operations })
    await screen.findByText(action)

    const sharedCap = new Map([
      [0, 160],
      [900, 200],
    ])
    const actionText = screen.getByText(action)
    expect(containerMaxWidthOf(actionText)).toEqual(sharedCap)
    expect(actionText.closest('td')).toHaveAttribute('title', action)

    const objectText = screen.getByText(object)
    expect(containerMaxWidthOf(objectText)).toEqual(sharedCap)
    expect(objectText.closest('td')).toHaveAttribute('title', object)

    const actorText = await screen.findByText('Operator')
    expect(containerMaxWidthOf(actorText)).toEqual(sharedCap)
    expect(actorText.closest('td')).toHaveAttribute('title', 'acc-1')
  })

  it('derives the platform health row from the windowed audit read', async () => {
    const operations = createConsoleOperations(
      makeConsoleBackend({
        // The sign-in health read narrows by the login action; the activity
        // read carries only the window. Each answers with its own page.
        listRecords: (params) =>
          params?.action?.includes('authn.login')
            ? Promise.resolve({
                entries: [
                  recordFixture({ id: 'l1', action: 'authn.login' }),
                  recordFixture({ id: 'l2', action: 'authn.login' }),
                ],
                next: '',
              })
            : Promise.resolve({
                entries: [recordFixture({ id: 'r1' })],
                next: '',
              }),
      }),
    )
    renderWithProviders(<DashboardPage />, { operations })
    const health = await screen.findByRole('heading', {
      level: 2,
      name: '平台健康度',
    })
    const section = health.closest('section') as HTMLElement
    expect(
      await within(section).findByText('近 24 小时登录（成功）'),
    ).toBeInTheDocument()
    expect(within(section).getByText('2')).toBeInTheDocument()
    expect(within(section).getByText('近 24 小时审计活动')).toBeInTheDocument()
    expect(within(section).getByText('1')).toBeInTheDocument()
  })

  it('reports a windowed health count as a lower bound when the page has a next cursor', async () => {
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listRecords: (params) =>
          params?.action?.includes('authn.login')
            ? Promise.resolve({
                entries: [recordFixture({ id: 'l1', action: 'authn.login' })],
                next: 'cur-2',
              })
            : Promise.resolve({ entries: [], next: '' }),
      }),
    )
    renderWithProviders(<DashboardPage />, { operations })
    const health = await screen.findByRole('heading', {
      level: 2,
      name: '平台健康度',
    })
    const section = health.closest('section') as HTMLElement
    // A page that carries a next cursor is a lower bound, shown with a "+".
    expect(await within(section).findByText('1+')).toBeInTheDocument()
  })

  it('renders the retryable placeholder when the health read fails', async () => {
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listRecords: (params) =>
          params?.action?.includes('authn.login')
            ? Promise.reject(new Error('boom'))
            : Promise.resolve({ entries: [], next: '' }),
      }),
    )
    renderWithProviders(<DashboardPage />, { operations })
    expect(await screen.findByText('平台健康度加载失败')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '重试' })).toBeInTheDocument()
  })

  it('renders the permission placeholder with no retry when the roster read is refused with 403', async () => {
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listTenants: () => Promise.reject({ status: 403, code: 'client.http.403' }),
      }),
    )
    renderWithProviders(<DashboardPage />, { operations })
    expect(await screen.findByText('权限不足')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '重试' })).toBeNull()
  })

  it('renders the retryable error placeholder when the roster read fails otherwise', async () => {
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listTenants: () => Promise.reject(new Error('boom')),
      }),
    )
    renderWithProviders(<DashboardPage />, { operations })
    expect(await screen.findByText('概览加载失败')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '重试' })).toBeInTheDocument()
  })

  it('has no axe violations', async () => {
    const operations = createConsoleOperations(
      makeConsoleBackend({ listTenants: () => Promise.resolve(TENANTS) }),
    )
    renderWithProviders(<DashboardPage />, { operations })
    await screen.findByText('租户总数')
    await expectNoAxeViolations()
  })
})
