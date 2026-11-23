/**
 * The lifecycle controls in a real browser: the transfer dialog and the
 * delete confirmation at the desktop viewport, in the light and dark
 * themes. The invariant is containment -- the page and the dialog's paper
 * stay inside the viewport -- which only a browser resolves (theme
 * surfaces, the dialog's max width and real font metrics all take part).
 */
import { beforeEach, describe, expect, it } from 'vitest'
import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useTheme } from '@mui/material/styles'
import { page } from 'vitest/browser'
import { attachSession } from '@speed/auth-core'
import { CONFIRM_ARM_LOCKOUT_MS, uiKitResources } from '@speed/ui-kit'
import { TenantRosterPage } from './TenantRosterPage.js'
import { renderWithProviders } from '../test-utils/render.js'
import {
  createAdminOperations,
  makeAdminBackend,
  tenantFixture,
  transferFixture,
} from '../test-utils/operations.js'
import {
  AUTHENTICATED_SNAPSHOT,
  createStepUpSession,
} from '../test-utils/session.js'
import { chooseAccount } from '../test-utils/account-selector.js'
import { expectNoHorizontalOverflow } from '../test-utils/browser-overflow.js'
import zhCN from './locales/zh-CN.json' with { type: 'json' }

/** The zh-CN label the shared confirm shows once its two-step guard arms. */
const CONFIRM_AGAIN_LABEL = (
  uiKitResources['zh-CN'] as unknown as {
    readonly confirmDialog: { readonly confirmAgainLabel: string }
  }
).confirmDialog.confirmAgainLabel

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

/** A promise the case settles by hand, so one request can be held in
 * flight across frames. */
function deferred<T>(): {
  readonly promise: Promise<T>
  readonly resolve: (value: T) => void
  readonly reject: (reason: unknown) => void
} {
  let resolve!: (value: T) => void
  let reject!: (reason: unknown) => void
  const promise = new Promise<T>((settle, fail) => {
    resolve = settle
    reject = fail
  })
  return { promise, resolve, reject }
}

/** One animation frame: the component's updated state has reached the DOM
 * and the browser has had its chance to react to it. */
function nextFrame(): Promise<void> {
  return new Promise((resolve) => {
    requestAnimationFrame(() => resolve())
  })
}

/** What currently holds focus, for a failure message. */
function describeActiveElement(): string {
  const active = document.activeElement
  if (active === null) {
    return 'null'
  }
  const label = active.getAttribute('aria-label')
  const textContent = (active.textContent ?? '').replace(/\s+/g, ' ').trim()
  return [active.tagName.toLowerCase(), label, textContent]
    .filter((part) => part !== null && part !== '')
    .join(' ')
}

/** The two-step danger confirmation: arm, wait out the lockout, confirm. */
async function confirmTwice(
  user: ReturnType<typeof userEvent.setup>,
  confirmLabel: string,
): Promise<void> {
  const dialog = await screen.findByRole('dialog')
  await user.click(within(dialog).getByRole('button', { name: confirmLabel }))
  await act(async () => {
    await new Promise((resolve) =>
      setTimeout(resolve, CONFIRM_ARM_LOCKOUT_MS + 300),
    )
  })
  await user.click(
    within(dialog).getByRole('button', { name: CONFIRM_AGAIN_LABEL }),
  )
}

const TENANT = tenantFixture({
  name: 'acme',
  display_name: 'Acme Corporation',
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

describe('tenant lifecycle write focus in Chromium', () => {
  it('keeps focus on the transfer submit while the request is in flight', async () => {
    await page.viewport(1200, 900)
    const written = deferred<ReturnType<typeof transferFixture>>()
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
                username: 'ada',
                active: true,
                joined_at: '2026-01-01T00:00:00Z',
                roles: [],
                source: 'admin',
                sso_exempt: false,
              },
            ],
            next_cursor: '',
          }),
        requestTenantOwner: () => written.promise,
      }),
    )
    renderWithProviders(<TenantRosterPage />, {
      operations,
      language: 'zh-CN',
    })
    const user = userEvent.setup()
    await screen.findByText('Acme Corporation')

    await user.click(
      screen.getByRole('button', {
        name: text(zhCN.tenantLifecycle.transferAria, { name: 'acme' }),
      }),
    )
    const dialog = await screen.findByRole('dialog')
    await chooseAccount(
      user,
      zhCN.tenantLifecycle.transfer.memberLabel,
      'Ada',
      'Ada Lovelace (ada)',
    )
    const submit = within(dialog).getByRole('button', {
      name: zhCN.tenantLifecycle.transfer.submit,
    })
    await user.click(submit)
    await confirmTwice(user, zhCN.tenantLifecycle.transfer.confirmLabel)

    // The confirmation closed and the request runs. The button that started
    // the flow keeps its focus through the dialog's focus restoration, and
    // reports the write as busy rather than dropping out of the Tab order.
    await waitFor(() => expect(submit).toHaveAttribute('aria-busy', 'true'))
    await nextFrame()
    expect(
      submit,
      `focus while the request is in flight; document.activeElement is ${describeActiveElement()}`,
    ).toHaveFocus()

    written.resolve(transferFixture())
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  })

  it('lands focus on the next row\'s delete control when a row is deleted', async () => {
    await page.viewport(1200, 900)
    let tenants = [
      tenantFixture({ name: 'acme', display_name: 'Acme', status: 'active' }),
      tenantFixture({ name: 'bravo', display_name: 'Bravo', status: 'active' }),
      tenantFixture({ name: 'charlie', display_name: 'Charlie', status: 'active' }),
    ]
    const operations = createAdminOperations(
      makeAdminBackend({
        listTenants: () => Promise.resolve(tenants),
        deleteTenant: (input) => {
          tenants = tenants.filter((tenant) => tenant.name !== input.name)
          return Promise.resolve()
        },
      }),
    )
    renderWithProviders(<TenantRosterPage />, {
      operations,
      language: 'zh-CN',
    })
    const user = userEvent.setup()
    await screen.findByText('Bravo')

    await user.click(
      screen.getByRole('button', {
        name: text(zhCN.tenantLifecycle.deleteAria, { name: 'bravo' }),
      }),
    )
    const dialog = await screen.findByRole('dialog')
    await user.type(
      within(dialog).getByLabelText(
        zhCN.tenantLifecycle.deleteConfirm.nameLabel,
      ),
      'bravo',
    )
    await confirmTwice(user, zhCN.tenantLifecycle.deleteConfirm.confirmLabel)

    await waitFor(() => expect(screen.queryByText('Bravo')).toBeNull())
    const next = screen.getByRole('button', {
      name: text(zhCN.tenantLifecycle.deleteAria, { name: 'charlie' }),
    })
    await waitFor(() => expect(next).toHaveFocus())
    expect(document.activeElement).toBe(next)
  })

  it('lands focus on the lifecycle group when the pending request is cancelled', async () => {
    await page.viewport(1200, 900)
    let pending: ReturnType<typeof transferFixture> | null = transferFixture({
      target_display_name: 'Ada Lovelace',
      target_username: 'ada',
    })
    const operations = createAdminOperations(
      makeAdminBackend({
        listTenants: () => Promise.resolve([TENANT]),
        getPendingTenantOwner: () => Promise.resolve(pending),
        cancelTenantOwner: () => {
          pending = null
          return Promise.resolve()
        },
      }),
    )
    renderWithProviders(<TenantRosterPage />, {
      operations,
      language: 'zh-CN',
    })
    const user = userEvent.setup()
    await screen.findByText('Acme Corporation')

    await user.click(
      await screen.findByRole('button', {
        name: zhCN.tenantLifecycle.transfer.pendingCancel,
      }),
    )
    const dialog = await screen.findByRole('dialog', {
      name: zhCN.tenantLifecycle.transfer.pendingCancelTitle,
    })
    await user.click(
      within(dialog).getByRole('button', {
        name: zhCN.tenantLifecycle.transfer.pendingCancel,
      }),
    )

    await waitFor(() =>
      expect(
        screen.queryByText(zhCN.tenantLifecycle.transfer.pendingTitle),
      ).toBeNull(),
    )
    // The confirmation is still exiting while the page behind it stays
    // aria-hidden; wait for it to leave before reading the control group.
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    const group = await screen.findByRole('group', {
      name: zhCN.tenantLifecycle.sectionTitle,
    })
    await waitFor(() => expect(group).toHaveFocus())
    expect(document.activeElement).toBe(group)
  })
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
