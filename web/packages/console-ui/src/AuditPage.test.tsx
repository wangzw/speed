/**
 * AuditPage: the audit-query page.
 *
 * Its read renders the settled records as a table, the recorded time
 * through Intl in the current language and explicitly in UTC (the zone the
 * view carries), and a failed read settles into the retryable error
 * placeholder.
 */

import { describe, expect, it } from 'vitest'
import { screen } from '@testing-library/react'
import { AuditPage } from './AuditPage.js'
import type { ConsoleAuditRecord } from './operations.js'
import { renderWithProviders } from '../test-utils/render.js'
import {
  createConsoleOperations,
  makeConsoleBackend,
} from '../test-utils/operations.js'
import { expectNoAxeViolations } from '../test-utils/axe.js'

const record: ConsoleAuditRecord = {
  id: '42',
  tenant: 'acme',
  occurred_at: '2026-10-15T12:00:00Z',
  actor: 'operator',
  source: 'console',
  action: 'tenant.create',
  object: 'tenant/acme',
  outcome: 'ok',
  purpose: '',
  detail: {},
}

describe('AuditPage', () => {
  it('renders the settled empty state when no record matches', async () => {
    const operations = createConsoleOperations(makeConsoleBackend())
    renderWithProviders(<AuditPage />, { operations })
    expect(await screen.findByText('暂无审计记录')).toBeInTheDocument()
  })

  it('lists records', async () => {
    const operations = createConsoleOperations(
      makeConsoleBackend({ listRecords: () => Promise.resolve([record]) }),
    )
    renderWithProviders(<AuditPage />, { operations })
    expect(await screen.findByText('tenant.create')).toBeInTheDocument()
    expect(screen.getByText('operator')).toBeInTheDocument()
    expect(screen.getByText('tenant/acme')).toBeInTheDocument()
    expect(screen.getByText('ok')).toBeInTheDocument()
  })

  it('renders records as a table with column headers', async () => {
    const operations = createConsoleOperations(
      makeConsoleBackend({ listRecords: () => Promise.resolve([record]) }),
    )
    renderWithProviders(<AuditPage />, { operations })
    await screen.findByText('tenant.create')
    for (const name of ['时间', '动作', '结果', '操作者', '对象']) {
      expect(
        screen.getByRole('columnheader', { name }),
      ).toBeInTheDocument()
    }
  })

  it('formats the recorded time in UTC in the current language', async () => {
    const operations = createConsoleOperations(
      makeConsoleBackend({ listRecords: () => Promise.resolve([record]) }),
    )
    renderWithProviders(<AuditPage />, { operations })
    await screen.findByText('tenant.create')
    const expected = new Intl.DateTimeFormat('zh-CN', {
      dateStyle: 'medium',
      timeStyle: 'short',
      timeZone: 'UTC',
    }).format(new Date(record.occurred_at))
    expect(screen.getByText(expected)).toBeInTheDocument()
  })

  it('renders no time for a malformed timestamp', async () => {
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listRecords: () =>
          Promise.resolve([{ ...record, occurred_at: 'not-a-date' }]),
      }),
    )
    renderWithProviders(<AuditPage />, { operations })
    // The action still renders; the bad timestamp is dropped, never thrown.
    expect(await screen.findByText('tenant.create')).toBeInTheDocument()
    expect(screen.queryByText('not-a-date')).not.toBeInTheDocument()
  })

  it('renders the retryable error placeholder when the read fails', async () => {
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listRecords: () => Promise.reject(new Error('boom')),
      }),
    )
    renderWithProviders(<AuditPage />, { operations })
    expect(
      await screen.findByText('审计记录加载失败'),
    ).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '重试' })).toBeInTheDocument()
  })

  it('has no axe violations', async () => {
    const operations = createConsoleOperations(
      makeConsoleBackend({ listRecords: () => Promise.resolve([record]) }),
    )
    renderWithProviders(<AuditPage />, { operations })
    await screen.findByText('tenant.create')
    await expectNoAxeViolations()
  })
})
