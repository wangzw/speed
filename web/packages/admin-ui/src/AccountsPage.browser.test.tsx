/**
 * AccountsPage in a real browser: the account table at the sm/md/lg
 * floor widths with fixed data. The columns shed by priority and cap the
 * identity cell, so the table must stay inside both the page and its own
 * scroll container. At the 375px mobile floor the table is narrower than
 * its content, and the row-actions column is pinned to the table's
 * scroll edge so Manage and Delete stay visible while the main columns
 * scroll.
 */
import { describe, expect, it } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import Box from '@mui/material/Box'
import { page, userEvent } from 'vitest/browser'
import { AccountsPage } from './AccountsPage.js'
import {
  accountUsername,
  identityLabel,
} from './internal/account-identity.js'
import { renderWithProviders } from '../test-utils/render.js'
import {
  accountFixture,
  createAdminOperations,
  makeAdminBackend,
} from '../test-utils/operations.js'
import { expectNoHorizontalOverflow } from '../test-utils/browser-overflow.js'
import { expectNoAxeViolations } from '../test-utils/axe.js'
import enUS from './locales/en-US.json' with { type: 'json' }

const FLOOR_WIDTHS = [600, 900, 1200] as const

const ACCOUNTS = [
  accountFixture({
    id: 'account-identifier-with-a-long-value-0001',
    display_name: 'Ada Lovelace',
  }),
  accountFixture({
    id: 'account-identifier-with-a-long-value-0002',
    display_name: 'Grace Hopper',
    status: 'disabled',
  }),
]

/** One account whose primary cell and row actions together are wider
 * than the page content at 375px. */
const NARROW_ACCOUNT = accountFixture({
  id: 'account-identifier-with-a-long-value-0003',
  display_name: 'Ada Lovelace the first programmer of recorded history',
  identifiers: [
    { kind: 'username', value: 'ada.lovelace@example.com', proven: true, sign_in: true, contact: false, default_contact: false },
  ],
})

/** The identity the row shows and its controls name: the display name with
 * the login username, never the account id. */
const NARROW_LABEL = identityLabel(
  NARROW_ACCOUNT.display_name,
  accountUsername(NARROW_ACCOUNT.identifiers),
) as string

/** One pixel of tolerance for subpixel layout rounding. */
const PIN_TOLERANCE = 0.5

describe('AccountsPage horizontal overflow in Chromium', () => {
  for (const width of FLOOR_WIDTHS) {
    it(`keeps the account table inside a ${width}px viewport`, async () => {
      await page.viewport(width, 900)
      const operations = createAdminOperations(
        makeAdminBackend({
          listAccounts: () =>
            Promise.resolve({ accounts: ACCOUNTS, next_cursor: '' }),
        }),
      )
      renderWithProviders(<AccountsPage />, { operations, language: 'en-US' })

      await screen.findByText('Ada Lovelace')
      await expectNoHorizontalOverflow()
    })
  }
})

describe('AccountsPage row actions at 375 in Chromium', () => {
  it('pins the account row actions to the table edge while the main columns scroll', async () => {
    await page.viewport(375, 900)
    const operations = createAdminOperations(
      makeAdminBackend({
        listAccounts: () =>
          Promise.resolve({
            accounts: [NARROW_ACCOUNT],
            next_cursor: '',
          }),
      }),
    )
    // The console shell gives page content the theme's spacing-3 inset on
    // each side (AdminShell's content box); reproduce it so the narrow
    // arm sees the same content width the running console does.
    renderWithProviders(
      <Box sx={{ p: 3 }}>
        <AccountsPage />
      </Box>,
      { operations, language: 'en-US' },
    )

    await screen.findByText(NARROW_LABEL)
    await document.fonts.ready

    const manage = screen.getByRole('button', {
      name: enUS.accounts.manageAria.replace('{{name}}', NARROW_LABEL),
    })
    const remove = screen.getByRole('button', {
      name: enUS.accounts.deleteAria.replace('{{name}}', NARROW_LABEL),
    })
    const container = manage.closest<HTMLElement>('.MuiTableContainer-root')
    expect(container).not.toBeNull()
    // The account table is wider than the page content at this width, so
    // the container can scroll and the pin is observable.
    expect(container?.scrollWidth).toBeGreaterThan(container?.clientWidth ?? 0)

    const box = container?.getBoundingClientRect() as DOMRect
    for (const control of [manage, remove]) {
      const rect = control.getBoundingClientRect()
      expect(rect.left).toBeGreaterThanOrEqual(box.left - PIN_TOLERANCE)
      expect(rect.right).toBeLessThanOrEqual(box.right + PIN_TOLERANCE)
    }

    const before = manage.getBoundingClientRect().left
    if (container !== null) {
      container.scrollLeft = 30
    }
    await waitFor(() => {
      expect(container?.scrollLeft ?? 0).toBeGreaterThan(0)
    })

    await waitFor(() => {
      expect(
        Math.abs(manage.getBoundingClientRect().left - before),
      ).toBeLessThanOrEqual(PIN_TOLERANCE)
    })
    const after = manage.getBoundingClientRect()
    expect(after.left).toBeGreaterThanOrEqual(box.left - PIN_TOLERANCE)
    expect(after.right).toBeLessThanOrEqual(box.right + PIN_TOLERANCE)
  })
})

describe('AccountsPage focus traps in Chromium', () => {
  /** Renders the page over the fixture accounts and opens the detail panel. */
  async function openDetail(): Promise<HTMLElement> {
    const operations = createAdminOperations(
      makeAdminBackend({
        listAccounts: () =>
          Promise.resolve({ accounts: ACCOUNTS, next_cursor: '' }),
      }),
    )
    renderWithProviders(<AccountsPage />, { operations, language: 'en-US' })
    await screen.findByText('Ada Lovelace')
    await userEvent.click(
      screen.getByRole('button', {
        name: enUS.accounts.manageAria.replace('{{name}}', 'Ada Lovelace'),
      }),
    )
    return screen.findByRole('dialog')
  }

  it('keeps the detail panel focus trap holding over a hidden page', async () => {
    // The unit suite's open-panel scan cannot press Tab, so aria-hidden-focus
    // is indeterminate there; the helper's browser pass exercises the trap
    // and decides it (@speed/test-utils/axe).
    const panel = await openDetail()
    expect(panel).toBeInTheDocument()
    await expectNoAxeViolations()
  })

  it('keeps the one-time secret focus trap holding over a hidden page', async () => {
    const panel = await openDetail()
    await userEvent.type(
      within(panel).getByLabelText(enUS.accounts.createSecret.labelLabel),
      'ci',
    )
    await userEvent.click(
      within(panel).getByRole('button', {
        name: enUS.accounts.createSecret.submit,
      }),
    )
    await within(panel).findByText(
      enUS.accounts.createSecret.handedOver
        .replace('{{label}}', 'label')
        .replace('{{secret}}', 'one-time'),
    )
    await expectNoAxeViolations()
  })
})
