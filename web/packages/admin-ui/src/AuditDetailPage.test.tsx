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
  createAdminOperations,
  makeAdminBackend,
  recordFixture,
} from '../test-utils/operations.js'
import { expectNoAxeViolations } from '../test-utils/axe.js'
import zhCN from './locales/zh-CN.json' with { type: 'json' }

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
    const operations = createAdminOperations(makeAdminBackend({ getRecord }))
    renderWithProviders(<AuditDetailPage id="rec-7" />, { operations })

    expect(await screen.findByText('tenant.rename')).toBeInTheDocument()
    expect(screen.getByText('allowed')).toBeInTheDocument()
    // The detail object renders as key/value pairs.
    expect(screen.getByText('alias')).toBeInTheDocument()
    expect(screen.getByText('Acme')).toBeInTheDocument()
    expect(getRecord).toHaveBeenCalledWith({ id: 'rec-7' })
  })

  it("shows the actor's display name, or the recorded id marked unresolved when the account read is refused", async () => {
    const resolved = createAdminOperations(
      makeAdminBackend({
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

    const refused = createAdminOperations(
      makeAdminBackend({
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
    const cell = await screen.findByTitle(zhCN.common.accountUnresolved)
    expect(cell).toHaveTextContent('acc-9')
    expect(cell).toHaveAttribute('title', zhCN.common.accountUnresolved)
  })

  it('shows the empty-value placeholder for fields the record carries empty', async () => {
    // A journal field that is empty is still part of the record: the row
    // stays, with a placeholder instead of a label and nothing beside it.
    const getRecord = vi.fn(() =>
      Promise.resolve(
        recordFixture({ purpose: '', tenant: '', detail: { note: '' } }),
      ),
    )
    const operations = createAdminOperations(makeAdminBackend({ getRecord }))
    renderWithProviders(<AuditDetailPage id="rec-1" />, { operations })

    await screen.findByText('tenant.read')
    for (const label of [
      zhCN.auditDetail.field.purpose,
      zhCN.auditDetail.field.tenant,
      'note',
    ]) {
      const row = screen.getByText(label).parentElement as HTMLElement
      expect(within(row).getByText(zhCN.common.emptyValue)).toBeInTheDocument()
    }
    expect(screen.getAllByText(zhCN.common.emptyValue)).toHaveLength(3)
  })

  it('raises its refresh action to the 44px floor under a coarse pointer', async () => {
    const operations = createAdminOperations(makeAdminBackend())
    renderWithProviders(<AuditDetailPage id="rec-1" />, { operations })
    const refresh = await screen.findByRole('button', {
      name: zhCN.common.refresh,
    })
    expect(hasCoarsePointerTouchTarget(refresh)).toBe(true)
  })

  it('links back to the audit query when given its path', async () => {
    const operations = createAdminOperations(makeAdminBackend())
    renderWithProviders(
      <AuditDetailPage id="rec-1" parentHref="/admin/audit" />,
      { operations },
    )
    await screen.findByRole('heading', {
      level: 1,
      name: zhCN.auditDetail.title,
    })
    expect(
      screen.getByRole('link', { name: zhCN.auditDetail.back }),
    ).toHaveAttribute('href', '/admin/audit')
  })

  it('names the record in the breadcrumb rather than repeating the query page', async () => {
    const operations = createAdminOperations(makeAdminBackend())
    renderWithProviders(
      <AuditDetailPage id="rec-1" parentHref="/admin/audit" />,
      { operations },
    )
    await screen.findByRole('heading', {
      level: 1,
      name: zhCN.auditDetail.title,
    })
    const breadcrumb = screen.getByRole('navigation')
    // The parent step keeps the query page's name and links back to it...
    expect(
      within(breadcrumb).getByRole('link', { name: zhCN.audit.title }),
    ).toHaveAttribute('href', '/admin/audit')
    // ...while the current step names the record itself, so the trail does
    // not read "Admin / Audit / Audit".
    expect(
      within(breadcrumb).getByText(
        zhCN.auditDetail.crumb.replace('{{id}}', 'rec-1'),
      ),
    ).toHaveAttribute('aria-current', 'page')
  })

  it('renders the permission placeholder with no retry when the read is refused with 403', async () => {
    const operations = createAdminOperations(
      makeAdminBackend({
        getRecord: () => Promise.reject({ status: 403, code: 'client.http.403' }),
      }),
    )
    renderWithProviders(<AuditDetailPage id="rec-1" />, { operations })
    expect(await screen.findByText(zhCN.forbidden.title)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: zhCN.auditDetail.retry })).toBeNull()
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
    const operations = createAdminOperations(makeAdminBackend({ getRecord }))
    renderWithProviders(<AuditDetailPage id="rec-1" />, { operations })

    expect(await screen.findByText(zhCN.detail.notFound.title)).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: zhCN.auditDetail.retry }))
    expect(await screen.findByText('tenant.read')).toBeInTheDocument()
    expect(getRecord).toHaveBeenCalledTimes(2)
  })

  it('renders the not-found placeholder for an id the route cannot parse, and keeps the 404 and failure branches', async () => {
    // A path identifier the route cannot parse is a 400: no record can carry
    // it, so it says the same thing to the reader as a missing record.
    const malformed = createAdminOperations(
      makeAdminBackend({
        getRecord: () =>
          Promise.reject({ status: 400, code: 'audit.bad-request' }),
      }),
    )
    const first = renderWithProviders(
      <AuditDetailPage id="does-not-exist" />,
      { operations: malformed },
    )
    expect(await screen.findByText(zhCN.detail.notFound.title)).toBeInTheDocument()
    first.unmount()

    // A 404 still renders the same placeholder.
    const missing = createAdminOperations(
      makeAdminBackend({
        getRecord: () => Promise.reject({ status: 404, code: 'client.http.404' }),
      }),
    )
    const second = renderWithProviders(<AuditDetailPage id="999999999" />, {
      operations: missing,
    })
    expect(await screen.findByText(zhCN.detail.notFound.title)).toBeInTheDocument()
    second.unmount()

    // Any other failure keeps the retryable error placeholder.
    const failed = createAdminOperations(
      makeAdminBackend({
        getRecord: () => Promise.reject({ status: 500, code: 'client.http.500' }),
      }),
    )
    renderWithProviders(<AuditDetailPage id="rec-1" />, { operations: failed })
    expect(await screen.findByText(zhCN.auditDetail.error.title)).toBeInTheDocument()
    expect(screen.getByText(zhCN.auditDetail.error.description)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: zhCN.auditDetail.retry })).toBeInTheDocument()
  })

  it('renders the retryable error placeholder when the read fails otherwise', async () => {
    const operations = createAdminOperations(
      makeAdminBackend({
        getRecord: () => Promise.reject(new Error('boom')),
      }),
    )
    renderWithProviders(<AuditDetailPage id="rec-1" />, { operations })
    expect(await screen.findByText(zhCN.auditDetail.error.title)).toBeInTheDocument()
    expect(screen.getByText(zhCN.auditDetail.error.description)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: zhCN.auditDetail.retry })).toBeInTheDocument()
  })

  it('has no axe violations', async () => {
    const operations = createAdminOperations(makeAdminBackend())
    renderWithProviders(<AuditDetailPage id="rec-1" />, { operations })
    await screen.findByRole('heading', { level: 1, name: zhCN.auditDetail.title })
    await expectNoAxeViolations()
  })
})
