/**
 * AuditPage in a real browser: the audit table at the sm/md/lg floor
 * widths with fixed data, and the shared column priorities at a laptop
 * container. The columns carry priorities and caps; the table and the
 * page must stay inside their widths, and at 1184px the object column
 * must be shed while the actor and the outcome stay.
 */
import { describe, expect, it } from 'vitest'
import { screen } from '@testing-library/react'
import { page } from 'vitest/browser'
import { AuditPage } from './AuditPage.js'
import { renderWithProviders } from '../test-utils/render.js'
import {
  accountFixture,
  createAdminOperations,
  makeAdminBackend,
  recordFixture,
} from '../test-utils/operations.js'
import { expectNoHorizontalOverflow } from '../test-utils/browser-overflow.js'
import { adminUiResources } from './resources.js'
import type { AdminAuditRecord } from './operations.js'

const FLOOR_WIDTHS = [600, 900, 1200] as const

/** The shipped en-US audit strings the visibility case reads. */
const AUDIT = (
  adminUiResources['en-US'] as unknown as {
    readonly audit: {
      readonly column: {
        readonly object: string
        readonly actor: string
        readonly outcome: string
      }
    }
  }
).audit

const RECORDS: readonly AdminAuditRecord[] = [
  recordFixture({
    id: 'record-identifier-with-a-long-value-0001',
    action: 'authn.account.identity.add',
    object: 'account/account-identifier-with-a-long-value-0001',
  }),
  recordFixture({
    id: 'record-identifier-with-a-long-value-0002',
    action: 'tenant.membership.remove',
    outcome: 'denied',
  }),
]

describe('AuditPage horizontal overflow in Chromium', () => {
  for (const width of FLOOR_WIDTHS) {
    it(`keeps the audit table inside a ${width}px viewport`, async () => {
      await page.viewport(width, 900)
      const operations = createAdminOperations(
        makeAdminBackend({
          listRecords: () =>
            Promise.resolve({ entries: [...RECORDS], next: '' }),
          getAccount: () =>
            Promise.resolve(accountFixture({ display_name: 'Ada Lovelace' })),
        }),
      )
      renderWithProviders(<AuditPage />, { operations, language: 'en-US' })

      await screen.findAllByText('Ada Lovelace')
      await expectNoHorizontalOverflow()
    })
  }
})

describe('AuditPage column priorities in Chromium', () => {
  it('sheds the object column and keeps the actor and the outcome at 1184px', async () => {
    await page.viewport(1184, 900)
    const operations = createAdminOperations(
      makeAdminBackend({
        listRecords: () =>
          Promise.resolve({ entries: [...RECORDS], next: '' }),
        getAccount: () =>
          Promise.resolve(accountFixture({ display_name: 'Ada Lovelace' })),
      }),
    )
    renderWithProviders(<AuditPage />, { operations, language: 'en-US' })

    await screen.findAllByText('Ada Lovelace')

    // The priorities resolve against the table's containment box, not the
    // viewport. Pin the regime this case is about: wide enough for the
    // actor (md, 900px) and the outcome (sm, 600px), too narrow for the
    // object (lg, 1200px). At this width the old declaration still showed
    // the object, so the three assertions below distinguish the two.
    const table = document.querySelector('.MuiTableContainer-root')
    expect(table).not.toBeNull()
    const containment = table!.parentElement as HTMLElement
    expect(containment.clientWidth).toBeGreaterThanOrEqual(900)
    expect(containment.clientWidth).toBeLessThan(1200)

    const outcomeHeader = screen.getByText(AUDIT.column.outcome)
    const actorHeader = screen.getByText(AUDIT.column.actor)
    const objectHeader = screen.getByText(AUDIT.column.object)

    expect(outcomeHeader).toBeVisible()
    expect(getComputedStyle(outcomeHeader.closest('th') as HTMLElement).display).toBe(
      'table-cell',
    )
    expect(actorHeader).toBeVisible()
    expect(getComputedStyle(actorHeader.closest('th') as HTMLElement).display).toBe(
      'table-cell',
    )
    expect(objectHeader).not.toBeVisible()
    expect(getComputedStyle(objectHeader.closest('th') as HTMLElement).display).toBe(
      'none',
    )

    // The result value of the denied record stays readable, not only its
    // header: the outcome is the column triage starts from.
    expect(screen.getByText('denied')).toBeVisible()
  })
})
