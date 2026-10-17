/**
 * AuditDetailPage: one audit record in full.
 *
 * It reads the record by id, renders its fields (its detail object as
 * key/value pairs), links back to the audit query when given the query's
 * path, and splits a refused read into the permission placeholder (403,
 * no retry) or the retryable error placeholder.
 */

import { describe, expect, it, vi } from 'vitest'
import { screen, within } from '@testing-library/react'
import { AuditDetailPage } from './AuditDetailPage.js'
import { renderWithProviders } from '../test-utils/render.js'
import { hasCoarsePointerTouchTarget } from '@speed/test-utils/emitted-css'
import {
  createConsoleOperations,
  makeConsoleBackend,
  recordFixture,
} from '../test-utils/operations.js'
import { expectNoAxeViolations } from '../test-utils/axe.js'

describe('AuditDetailPage', () => {
  it('renders the record fields read by id', async () => {
    const getRecord = vi.fn(() =>
      Promise.resolve(
        recordFixture({
          id: 'rec-7',
          action: 'tenant.rename',
          outcome: 'allowed',
          detail: { alias: 'Acme' },
        }),
      ),
    )
    const operations = createConsoleOperations(makeConsoleBackend({ getRecord }))
    renderWithProviders(<AuditDetailPage id="rec-7" />, { operations })

    expect(await screen.findByText('tenant.rename')).toBeInTheDocument()
    expect(screen.getByText('allowed')).toBeInTheDocument()
    // The detail object renders as key/value pairs.
    expect(screen.getByText('alias')).toBeInTheDocument()
    expect(screen.getByText('Acme')).toBeInTheDocument()
    expect(getRecord).toHaveBeenCalledWith({ id: 'rec-7' })
  })

  it('raises its refresh action to the 44px floor under a coarse pointer', async () => {
    const operations = createConsoleOperations(makeConsoleBackend())
    renderWithProviders(<AuditDetailPage id="rec-1" />, { operations })
    const refresh = await screen.findByRole('button', { name: '刷新' })
    expect(hasCoarsePointerTouchTarget(refresh)).toBe(true)
  })

  it('links back to the audit query when given its path', async () => {
    const operations = createConsoleOperations(makeConsoleBackend())
    renderWithProviders(
      <AuditDetailPage id="rec-1" parentHref="/console/audit" />,
      { operations },
    )
    await screen.findByRole('heading', { level: 1, name: '审计记录' })
    expect(screen.getByRole('link', { name: '返回审计查询' })).toHaveAttribute(
      'href',
      '/console/audit',
    )
  })

  it('names the record in the breadcrumb rather than repeating the query page', async () => {
    const operations = createConsoleOperations(makeConsoleBackend())
    renderWithProviders(
      <AuditDetailPage id="rec-1" parentHref="/console/audit" />,
      { operations },
    )
    await screen.findByRole('heading', { level: 1, name: '审计记录' })
    const breadcrumb = screen.getByRole('navigation')
    // The parent step keeps the query page's name and links back to it...
    expect(
      within(breadcrumb).getByRole('link', { name: '审计记录' }),
    ).toHaveAttribute('href', '/console/audit')
    // ...while the current step names the record itself, so the trail does
    // not read "控制台 / 审计记录 / 审计记录".
    expect(within(breadcrumb).getByText('记录「rec-1」')).toHaveAttribute(
      'aria-current',
      'page',
    )
  })

  it('renders the permission placeholder with no retry when the read is refused with 403', async () => {
    const operations = createConsoleOperations(
      makeConsoleBackend({
        getRecord: () => Promise.reject({ status: 403, code: 'client.http.403' }),
      }),
    )
    renderWithProviders(<AuditDetailPage id="rec-1" />, { operations })
    expect(await screen.findByText('权限不足')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '重试' })).toBeNull()
  })

  it('renders the retryable error placeholder when the read fails otherwise', async () => {
    const operations = createConsoleOperations(
      makeConsoleBackend({
        getRecord: () => Promise.reject(new Error('boom')),
      }),
    )
    renderWithProviders(<AuditDetailPage id="rec-1" />, { operations })
    expect(await screen.findByText('审计记录加载失败')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '重试' })).toBeInTheDocument()
  })

  it('has no axe violations', async () => {
    const operations = createConsoleOperations(makeConsoleBackend())
    renderWithProviders(<AuditDetailPage id="rec-1" />, { operations })
    await screen.findByRole('heading', { level: 1, name: '审计记录' })
    await expectNoAxeViolations()
  })
})
