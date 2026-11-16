/**
 * The lifecycle controls in a real browser: the transfer dialog and the
 * delete confirmation at the desktop viewport, in the light and dark
 * themes. The invariant is containment -- the page and the dialog's paper
 * stay inside the viewport -- which only a browser resolves (theme
 * surfaces, the dialog's max width and real font metrics all take part).
 */
import { beforeEach, describe, expect, it } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useTheme } from '@mui/material/styles'
import { page } from 'vitest/browser'
import { attachSession } from '@speed/auth-core'
import { TenantRosterPage } from './TenantRosterPage.js'
import { renderWithProviders } from '../test-utils/render.js'
import {
  createAdminOperations,
  makeAdminBackend,
  tenantFixture,
} from '../test-utils/operations.js'
import {
  AUTHENTICATED_SNAPSHOT,
  createStepUpSession,
} from '../test-utils/session.js'
import { expectNoHorizontalOverflow } from '../test-utils/browser-overflow.js'
import zhCN from './locales/zh-CN.json' with { type: 'json' }

/** One pixel of tolerance for subpixel layout rounding. */
const TOLERANCE = 1

function ThemeProbe() {
  const theme = useTheme()
  return (
    <div
      data-testid="theme-probe"
      data-mode={theme.palette.mode}
      style={{ backgroundColor: theme.palette.background.default }}
    />
  )
}

/** Fills a locale template's `{{name}}` placeholders. */
function text(
  template: string,
  values: Record<string, string>,
): string {
  return template.replace(/\{\{(\w+)\}\}/g, (match, key: string) =>
    values[key] ?? match,
  )
}

const TENANT = tenantFixture({
  name: 'acme',
  displayName: 'Acme Corporation',
  status: 'active',
})

beforeEach(() => {
  attachSession(
    createStepUpSession({
      ...AUTHENTICATED_SNAPSHOT,
      permissionSets: {
        system: ['tenant.manage'],
        tenant: ['authn.membership.manage'],
      },
    }),
  )
})

describe('tenant lifecycle geometry in Chromium', () => {
  for (const mode of ['light', 'dark'] as const) {
    it(`keeps the transfer dialog and delete confirmation inside the ${mode} viewport`, async () => {
      await page.viewport(1512, 861)
      const operations = createAdminOperations(
        makeAdminBackend({
          listTenants: () => Promise.resolve([TENANT]),
          listMembers: () =>
            Promise.resolve({
              members: [
                {
                  tenant: 'acme',
                  account: 'acc-2',
                  display_name: 'Ada Lovelace',
                  username: '',
                  active: true,
                  joined_at: '2026-01-01T00:00:00Z',
                  roles: [],
                  source: 'admin',
                  sso_exempt: false,
                },
              ],
              next_cursor: '',
            }),
        }),
      )
      renderWithProviders(
        <>
          <ThemeProbe />
          <TenantRosterPage />
        </>,
        { operations, language: 'zh-CN', defaultMode: mode },
      )
      const user = userEvent.setup()
      await screen.findByText('Acme Corporation')

      const probe = screen.getByTestId('theme-probe')
      expect(probe.dataset.mode).toBe(mode)
      if (mode === 'dark') {
        const surface = getComputedStyle(probe).backgroundColor
        const channels = surface.match(/\d+/g)?.map(Number) ?? []
        expect(channels).toHaveLength(3)
        expect(
          Math.max(...channels),
          `the dark surface is not dark (${surface})`,
        ).toBeLessThan(128)
      }

      // The transfer dialog: the paper stays inside the viewport, the page
      // keeps its horizontal containment with the modal open, and the
      // member selector settles inside the paper.
      await user.click(
        screen.getByRole('button', {
          name: text(zhCN.tenantLifecycle.transferAria, { name: 'acme' }),
        }),
      )
      const transfer = await screen.findByRole('dialog')
      await within(transfer).findByRole('combobox', {
        name: zhCN.tenantLifecycle.transfer.memberLabel,
      })
      expectInsideViewport(transfer)
      await expectNoHorizontalOverflow()
      await user.click(
        within(transfer).getByRole('button', { name: zhCN.common.cancel }),
      )
      await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())

      // The delete confirmation: same containment, on the danger dialog.
      await user.click(
        screen.getByRole('button', {
          name: text(zhCN.tenantLifecycle.deleteAria, { name: 'acme' }),
        }),
      )
      const confirm = await screen.findByRole('dialog')
      expectInsideViewport(confirm)
      expect(
        within(confirm).getByText(zhCN.tenantLifecycle.deleteConfirm.message),
      ).toBeInTheDocument()
      await expectNoHorizontalOverflow()
      await user.click(
        within(confirm).getByRole('button', { name: zhCN.common.cancel }),
      )
      await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    })
  }
})

/** The dialog's paper lies inside the viewport horizontally and vertically. */
function expectInsideViewport(dialog: HTMLElement): void {
  const rect = dialog.getBoundingClientRect()
  expect(rect.left).toBeGreaterThanOrEqual(-TOLERANCE)
  expect(rect.top).toBeGreaterThanOrEqual(-TOLERANCE)
  expect(rect.right).toBeLessThanOrEqual(window.innerWidth + TOLERANCE)
  expect(rect.bottom).toBeLessThanOrEqual(window.innerHeight + TOLERANCE)
  expect(rect.width).toBeGreaterThan(0)
  expect(rect.height).toBeGreaterThan(0)
}
