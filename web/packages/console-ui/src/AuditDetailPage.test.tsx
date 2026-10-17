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
import userEvent from '@testing-library/user-event'
import { AuditDetailPage } from './AuditDetailPage.js'
import { renderWithProviders } from '../test-utils/render.js'
import { hasCoarsePointerTouchTarget } from '@speed/test-utils/emitted-css'
import {
  accountFixture,
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

  it("shows the actor's display name, or the recorded id marked unresolved when the account read is refused", async () => {
    const resolved = createConsoleOperations(
      makeConsoleBackend({
        getRecord: () =>
          Promise.resolve(recordFixture({ actor: 'acc-9' })),
        getAccount: () =>
          Promise.resolve(
            accountFixture({ id: 'acc-9', display_name: 'Demo Operator' }),
          ),
      }),
    )
    const first = renderWithProviders(<AuditDetailPage id="rec-9" />, {
      operations: resolved,
    })
    expect(await screen.findByText('Demo Operator')).toBeInTheDocument()
    expect(screen.queryByText('acc-9')).not.toBeInTheDocument()
    first.unmount()

    const refused = createConsoleOperations(
      makeConsoleBackend({
        getRecord: () =>
          Promise.resolve(recordFixture({ actor: 'acc-9' })),
        getAccount: () => Promise.reject(new Error('forbidden')),
      }),
    )
    renderWithProviders(<AuditDetailPage id="rec-9" />, {
      operations: refused,
    })
    // The row keeps the recorded id but labels it unresolved rather than
    // passing it off as a resolved name.
    const cell = await screen.findByLabelText(/身份未解析/)
    expect(cell).toHaveTextContent('acc-9')
    expect(cell).toHaveAttribute('title', '身份未解析（需账户读取权限）')
  })

  it('shows the empty-value placeholder for fields the record carries empty', async () => {
    // A journal field that is empty is still part of the record: the row
    // stays, with a placeholder instead of a label and nothing beside it.
    const getRecord = vi.fn(() =>
      Promise.resolve(
        recordFixture({ purpose: '', tenant: '', detail: { note: '' } }),
      ),
    )
    const operations = createConsoleOperations(makeConsoleBackend({ getRecord }))
    renderWithProviders(<AuditDetailPage id="rec-1" />, { operations })

    await screen.findByText('tenant.read')
    for (const label of ['用途', '租户', 'note']) {
      const row = screen.getByText(label).parentElement as HTMLElement
      expect(within(row).getByText('—')).toBeInTheDocument()
    }
    expect(screen.getAllByText('—')).toHaveLength(3)
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

  it('renders the unavailable placeholder with a retry that re-reads a 404', async () => {
    const user = userEvent.setup()
    let calls = 0
    const getRecord = vi.fn(() => {
      calls += 1
      return calls === 1
        ? Promise.reject({ status: 404, code: 'client.http.404' })
        : Promise.resolve(recordFixture({ id: 'rec-1', action: 'tenant.read' }))
    })
    const operations = createConsoleOperations(makeConsoleBackend({ getRecord }))
    renderWithProviders(<AuditDetailPage id="rec-1" />, { operations })

    expect(await screen.findByText('记录不存在或暂不可用')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '重试' }))
    expect(await screen.findByText('tenant.read')).toBeInTheDocument()
    expect(getRecord).toHaveBeenCalledTimes(2)
  })

  it('renders the not-found placeholder for an id the route cannot parse, and keeps the 404 and failure branches', async () => {
    // A path identifier the route cannot parse is a 400: no record can carry
    // it, so it says the same thing to the reader as a missing record.
    const malformed = createConsoleOperations(
      makeConsoleBackend({
        getRecord: () =>
          Promise.reject({ status: 400, code: 'audit.bad-request' }),
      }),
    )
    const first = renderWithProviders(
      <AuditDetailPage id="does-not-exist" />,
      { operations: malformed },
    )
    expect(await screen.findByText('记录不存在或暂不可用')).toBeInTheDocument()
    first.unmount()

    // A 404 still renders the same placeholder.
    const missing = createConsoleOperations(
      makeConsoleBackend({
        getRecord: () => Promise.reject({ status: 404, code: 'client.http.404' }),
      }),
    )
    const second = renderWithProviders(<AuditDetailPage id="999999999" />, {
      operations: missing,
    })
    expect(await screen.findByText('记录不存在或暂不可用')).toBeInTheDocument()
    second.unmount()

    // Any other failure keeps the retryable error placeholder.
    const failed = createConsoleOperations(
      makeConsoleBackend({
        getRecord: () => Promise.reject({ status: 500, code: 'client.http.500' }),
      }),
    )
    renderWithProviders(<AuditDetailPage id="rec-1" />, { operations: failed })
    expect(await screen.findByText('审计记录加载失败')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '重试' })).toBeInTheDocument()
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
