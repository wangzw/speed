/**
 * AuditPage in a real browser: the audit table at the sm/md/lg floor
 * widths with fixed data, and the shared column priorities at a laptop
 * container. The columns carry priorities and caps; the table and the
 * page must stay inside their widths, and at 1184px the object column
 * must be shed while the actor and the outcome stay.
 */
import { describe, expect, it } from 'vitest'
import {
  fireEvent,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
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
import enUS from './locales/en-US.json' with { type: 'json' }

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

/** One animation frame: the state change has reached the DOM and the
 * browser has had its chance to react to it. */
function nextFrame(): Promise<void> {
  return new Promise((resolve) => {
    requestAnimationFrame(() => resolve())
  })
}

describe('AuditPage focus in Chromium', () => {
  it('keeps focus on the export control while an export runs and after it settles', async () => {
    await page.viewport(1200, 900)
    let resolveExport!: (blob: Blob) => void
    const operations = createAdminOperations(
      makeAdminBackend({
        listRecords: () =>
          Promise.resolve({ entries: [...RECORDS], next: '' }),
        getAccount: () =>
          Promise.resolve(accountFixture({ display_name: 'Ada Lovelace' })),
        exportRecords: () =>
          new Promise<Blob>((resolve) => {
            resolveExport = resolve
          }),
      }),
    )
    renderWithProviders(<AuditPage />, { operations, language: 'en-US' })
    await screen.findAllByText('Ada Lovelace')

    const control = screen.getByRole('button', {
      name: enUS.audit.export.label,
    })
    const user = userEvent.setup()
    await user.click(control)
    await waitFor(() => {
      expect(within(control).getByRole('progressbar')).toBeInTheDocument()
    })
    expect(control).toHaveAttribute('aria-disabled', 'true')
    await nextFrame()
    expect(control).toHaveFocus()

    resolveExport(new Blob([]))
    await waitFor(() => {
      expect(control).not.toHaveAttribute('aria-busy')
    })
    expect(control).toHaveFocus()
  })

  it('keeps the export control reachable and unavailable while drafts are unapplied', async () => {
    await page.viewport(1200, 900)
    let calls = 0
    const operations = createAdminOperations(
      makeAdminBackend({
        listRecords: () =>
          Promise.resolve({ entries: [...RECORDS], next: '' }),
        getAccount: () =>
          Promise.resolve(accountFixture({ display_name: 'Ada Lovelace' })),
        exportRecords: () => {
          calls += 1
          return Promise.resolve(new Blob([]))
        },
      }),
    )
    renderWithProviders(<AuditPage />, { operations, language: 'en-US' })
    await screen.findAllByText('Ada Lovelace')

    const control = screen.getByRole('button', {
      name: enUS.audit.export.label,
    })
    fireEvent.change(screen.getByLabelText(enUS.audit.filters.since), {
      target: { value: '2026-10-15T09:30' },
    })
    await waitFor(() => {
      expect(control).toHaveAttribute('aria-disabled', 'true')
    })
    control.focus()
    expect(control).toHaveFocus()

    // Every activation path reaches the guard and issues nothing.
    const user = userEvent.setup()
    await user.keyboard('{Enter}')
    await user.keyboard(' ')
    await user.click(control)
    await nextFrame()
    expect(calls).toBe(0)
    expect(control).toHaveFocus()
  })

  it('keeps the previous control reachable on the first page and focus on the next control at the last', async () => {
    await page.viewport(1200, 900)
    const operations = createAdminOperations(
      makeAdminBackend({
        listRecords: (params) =>
          Promise.resolve(
            params?.cursor === 'c1'
              ? { entries: [RECORDS[1]!], next: '' }
              : { entries: [RECORDS[0]!], next: 'c1' },
          ),
        getAccount: () =>
          Promise.resolve(accountFixture({ display_name: 'Ada Lovelace' })),
      }),
    )
    renderWithProviders(<AuditPage />, { operations, language: 'en-US' })
    await screen.findAllByText('Ada Lovelace')

    const prev = screen.getByRole('button', {
      name: enUS.audit.prevPage,
    })
    const next = screen.getByRole('button', {
      name: enUS.audit.nextPage,
    })
    // The first page has nothing behind it; the control says so through
    // its state alone and still takes focus.
    expect(prev).toHaveAttribute('aria-disabled', 'true')
    expect(prev).not.toHaveAttribute('aria-describedby')
    prev.focus()
    expect(prev).toHaveFocus()

    const user = userEvent.setup()
    await user.click(next)
    await waitFor(() => {
      expect(next).toHaveAttribute('aria-disabled', 'true')
    })
    await nextFrame()
    expect(next).toHaveFocus()
    expect(prev).not.toHaveAttribute('aria-disabled', 'true')
  })
})
