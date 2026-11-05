/**
 * OrganizationMembersPage in a real browser.
 *
 * The page stacks two DataTables (members, then invitations) inside a
 * tenant-scoped management surface. Its geometry is settled only by a
 * browser: the two tables declare container-query column priorities, the
 * member management panel sizes itself from the viewport's breakpoint, and
 * the theme's coarse-pointer rule raises controls to a finger-sized hit
 * area. The cases below cover those four facts at the sm/md/lg floors:
 *
 *   - both tables, and the page, keep their content inside their own width;
 *   - the role, status, joined-at and invitation columns -- high priority,
 *     the information an operator opens the tables for -- are visible from
 *     the sm tier up, including the 1024px desktop shell beside its 280px
 *     sidebar, rather than dropping at a narrow container the way a medium
 *     or low column does;
 *   - the management panel takes its 36rem tier on a wide viewport and the
 *     full width on a phone-sized one;
 *   - the dark theme paints the page's surface and keeps the same columns;
 *   - under a coarse pointer the page's buttons and text fields reach the
 *     44px touch-target floor.
 */

import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import type { ReactNode } from 'react'
import Box from '@mui/material/Box'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useTheme } from '@mui/material/styles'
import { selectClasses } from '@mui/material/Select'
import { cdp, page } from 'vitest/browser'
import { AppShell } from '@speed/layout-kit'
import { attachSession } from '@speed/auth-core'
import { OrganizationAdminProvider } from './OrganizationAdminProvider.js'
import { OrganizationMembersPage } from './OrganizationMembersPage.js'
import { renderWithProviders } from '../test-utils/render.js'
import {
  createAdminOperations,
  makeAdminBackend,
} from '../test-utils/operations.js'
import type { AdminBackend } from '../test-utils/operations.js'
import {
  AUTHENTICATED_SNAPSHOT,
  createStepUpSession,
} from '../test-utils/session.js'
import { expectNoHorizontalOverflow, TIER_FLOORS } from '../test-utils/browser-overflow.js'
import type {
  AdminInvitation,
  AdminMember,
} from './operations.js'
import type {
  AdminTenantBinding,
  AdminTenantRole,
} from './organization-operations.js'
import enUS from './locales/en-US.json' with { type: 'json' }

const TENANT = 'acme'
const SELF = 'acc-1'

const ADA: AdminMember = {
  account: SELF,
  tenant: TENANT,
  active: true,
  display_name: 'Ada Lovelace',
  source: 'admin',
  sso_exempt: false,
  roles: ['support'],
  joined_at: '2026-10-15T12:00:00Z',
}

const BOB: AdminMember = {
  account: 'acc-2',
  tenant: TENANT,
  active: false,
  display_name: 'Bob Stone',
  source: 'invite',
  sso_exempt: false,
  roles: [],
  joined_at: '2026-10-20T08:30:00Z',
}

const MEMBERS: readonly AdminMember[] = [ADA, BOB]

const INVITATIONS: readonly AdminInvitation[] = [
  {
    id: 'inv-1',
    tenant: TENANT,
    email: 'ada@example.com',
    invited_by: SELF,
    status: 'pending',
    created_at: '2026-10-15T12:00:00Z',
    expires_at: '2099-01-01T00:00:00Z',
    accepted_at: '0001-01-01T00:00:00Z',
  },
]

const SUPPORT_ROLE: AdminTenantRole = {
  tenant: TENANT,
  role: 'support',
  builtin: false,
  description_key: 'role.support',
  created_at: '2026-01-01T00:00:00Z',
}

const ADA_BINDING: AdminTenantBinding = {
  tenant: TENANT,
  account: ADA.account,
  role: SUPPORT_ROLE.role,
  granted_at: '2026-10-15T12:00:00Z',
}

/** The scripted backend: both lists populated, the panel's reads settled.
 * The list values are parameters so a case can put over-long values in the
 * same two tables. */
function backend(
  members: readonly AdminMember[] = MEMBERS,
  invitations: readonly AdminInvitation[] = INVITATIONS,
): AdminBackend {
  return makeAdminBackend({
    listMembers: () => Promise.resolve({ members, next_cursor: '' }),
    listInvitations: () =>
      Promise.resolve({ invitations, next_cursor: '' }),
    listTenantRoles: () => Promise.resolve([SUPPORT_ROLE]),
    listTenantAccountBindings: () => Promise.resolve([ADA_BINDING]),
  })
}

/** Renders the page as the organization surface composes it. The optional
 * probe sits inside the theme provider, so a case can compare the page's
 * paint against the theme role it is supposed to use. */
function renderPage(
  options: {
    readonly defaultMode?: 'light' | 'dark' | 'system'
  } = {},
  probe?: ReactNode,
  scripted: AdminBackend = backend(),
) {
  return renderWithProviders(
    <>
      {probe}
      <OrganizationAdminProvider
        tenant={TENANT}
        operations={createAdminOperations(scripted)}
        hasFeature={() => true}
        protectedRoles={['tenant-admin']}
      >
        <OrganizationMembersPage tenant={TENANT} />
      </OrganizationAdminProvider>
    </>,
    { language: 'en-US', ...options },
  )
}

/** The page as the demo hosts it: the tenant frame's sidebar shell (280px
 * drawer) around the padded content region. */
function renderShellPage(scripted: AdminBackend = backend()) {
  return renderWithProviders(
    <AppShell
      shellLayout="sidebar"
      sidebarWidth={280}
      navItems={[
        {
          id: 'members',
          label: 'Members',
          href: '/organization/admin/invitations',
          selected: true,
        },
      ]}
      header={<span>Speed</span>}
    >
      {/* The demo's own content padding (starters/demo/web/src/app.tsx). */}
      <Box sx={{ p: 3 }}>
        <OrganizationAdminProvider
          tenant={TENANT}
          operations={createAdminOperations(scripted)}
          hasFeature={() => true}
          protectedRoles={['tenant-admin']}
        >
          <OrganizationMembersPage tenant={TENANT} />
        </OrganizationAdminProvider>
      </Box>
    </AppShell>,
    { language: 'en-US' },
  )
}

/** A probe that reports the assembled theme's color mode and default
 * surface, so a dark-mode assertion compares against the theme rather than
 * against a color written into this test. */
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

/** The table whose head carries `header`, with its scroll container and the
 * containment box whose width the column queries resolve against. */
function tableFor(header: string): {
  readonly container: HTMLElement
  readonly containment: HTMLElement
} {
  const cell = Array.from(document.querySelectorAll('thead th')).find(
    (candidate) => candidate.textContent?.trim() === header,
  )
  if (cell === undefined) {
    throw new Error(`no table with a "${header}" column header`)
  }
  const container = cell.closest<HTMLElement>('.MuiTableContainer-root')
  const containment = container?.parentElement
  if (container === null || containment === null || containment === undefined) {
    throw new Error(`the "${header}" table has no containment box`)
  }
  return { container, containment }
}

/** The computed display value of a column header inside `container`. */
function headerDisplay(container: HTMLElement, header: string): string {
  const cell = Array.from(container.querySelectorAll('thead th')).find(
    (candidate) => candidate.textContent?.trim() === header,
  )
  if (cell === undefined) {
    throw new Error(`the table has no "${header}" column header`)
  }
  return getComputedStyle(cell).display
}

/** The zero-based position of the column headed `header` inside `container`. */
function columnIndexIn(container: HTMLElement, header: string): number {
  const headers = Array.from(container.querySelectorAll('thead th'))
  const index = headers.findIndex(
    (candidate) => candidate.textContent?.trim() === header,
  )
  if (index < 0) {
    throw new Error(`the table has no "${header}" column header`)
  }
  return index
}

/** A column header's rendered width inside `container`. */
function headerWidth(container: HTMLElement, header: string): number {
  const cell = Array.from(container.querySelectorAll('thead th')).find(
    (candidate) => candidate.textContent?.trim() === header,
  )
  if (cell === undefined) {
    throw new Error(`the table has no "${header}" column header`)
  }
  return cell.getBoundingClientRect().width
}

/** The first body cell inside `container` whose text contains `value`. */
function bodyCellWith(container: HTMLElement, value: string): HTMLElement {
  const cell = Array.from(container.querySelectorAll('tbody td')).find(
    (candidate) => candidate.textContent?.includes(value),
  )
  if (cell === undefined) {
    throw new Error(`no body cell carries "${value}"`)
  }
  return cell
}

/**
 * Assert the page itself keeps its content inside the viewport when the
 * content region is below the `sm` container floor. At that width the
 * design keeps only the unprioritized columns and lets the table scroll
 * inside its own container, so the table fitting its box is not required;
 * the page must not gain a horizontal scrollbar of its own.
 */
async function expectNoPageHorizontalOverflow(): Promise<void> {
  await waitFor(() => {
    expect(
      document.querySelectorAll('.MuiTableContainer-root').length,
      'the page rendered no table container',
    ).toBeGreaterThan(0)
  })
  await document.fonts.ready

  for (const container of Array.from(
    document.querySelectorAll<HTMLElement>('.MuiTableContainer-root'),
  )) {
    expect(
      container.getBoundingClientRect().right,
      'a table container extends past the viewport',
    ).toBeLessThanOrEqual(document.documentElement.clientWidth + 1)
  }
  expect(
    document.documentElement.scrollWidth,
    'the page overflows the viewport horizontally',
  ).toBeLessThanOrEqual(document.documentElement.clientWidth + 1)
}

/**
 * Assert a column header is inside the table's own visible box, not merely
 * that CSS keeps it displayed. A table scrolls horizontally once its
 * columns exceed the container, so `toBeVisible()` alone still passes for a
 * column scrolled out of the viewport; this compares the header's rendered
 * rect against the scroll container's instead.
 */
function expectHeaderOnScreen(container: HTMLElement, header: string): void {
  const cell = within(container).getByRole('columnheader', {
    name: header,
    exact: true,
  })
  expect(cell, `the ${header} column is hidden`).toBeVisible()
  const cellRect = cell.getBoundingClientRect()
  const containerRect = container.getBoundingClientRect()
  expect(
    cellRect.left >= containerRect.left - 1 &&
      cellRect.right <= containerRect.right + 1,
    `the ${header} column is scrolled out of the table's visible area`,
  ).toBe(true)
}

/** The number of line boxes the text node reading exactly `value` is laid
 * out in inside `cell`. A value on one line yields one line top even when
 * truncation makes the range report more than one rect; a value squeezed
 * until it breaks mid-token yields more than one. Measured from the
 * rendered line boxes, not from a font size or a pixel width. */
function lineBoxCount(cell: HTMLElement, value: string): number {
  const walker = document.createTreeWalker(cell, NodeFilter.SHOW_TEXT)
  for (let node = walker.nextNode(); node !== null; node = walker.nextNode()) {
    if (node.textContent?.trim() !== value) continue
    const range = document.createRange()
    range.selectNodeContents(node)
    const tops = new Set(
      Array.from(range.getClientRects(), (rect) => Math.round(rect.top)),
    )
    return tops.size
  }
  throw new Error(`no text node reads exactly "${value}"`)
}

/** Runs `check` with the table's containment box pinned to `width`, then
 * restores the declared width. The column priorities are container queries,
 * so this reads the exact tier rather than whatever width the page's chrome
 * happens to leave. */
function atContainerWidth(
  containment: HTMLElement,
  width: number,
  check: () => void,
): void {
  const original = containment.style.width
  containment.style.width = `${width}px`
  // Force layout before the query resolves.
  void containment.offsetWidth
  try {
    check()
  } finally {
    containment.style.width = original
  }
}

/** The management control's accessible name for a member. */
function manageName(member: AdminMember): string {
  return enUS.members.manageAria.replace('{{name}}', member.display_name)
}

/** A human-readable name for a control in a touch-target failure message. */
function controlName(control: HTMLElement): string {
  return (
    control.getAttribute('aria-label') ??
    control.textContent?.trim() ??
    control.tagName.toLowerCase()
  )
}

beforeEach(() => {
  // The panel's role tiers read the session's tenant permission set; the
  // page's lists themselves need no permission.
  attachSession(
    createStepUpSession({
      ...AUTHENTICATED_SNAPSHOT,
      principal:
        AUTHENTICATED_SNAPSHOT.principal === null
          ? null
          : { ...AUTHENTICATED_SNAPSHOT.principal, account_id: SELF },
      permissionSets: {
        tenant: ['authz.tenant.role.read', 'authz.tenant.role.manage'],
        system: null,
      },
    }),
  )
})

afterEach(async () => {
  // Device emulation is page state and would leak into the next case.
  const session = cdp() as unknown as {
    send(method: string, params?: Record<string, unknown>): Promise<unknown>
  }
  await session.send('Emulation.setTouchEmulationEnabled', { enabled: false })
})

describe('OrganizationMembersPage horizontal overflow in Chromium', () => {
  for (const width of TIER_FLOORS) {
    it(`keeps both tables inside a ${width}px viewport`, async () => {
      await page.viewport(width, 900)
      renderPage()

      await screen.findByText(ADA.display_name)
      await screen.findByText(INVITATIONS[0].email)
      await expectNoHorizontalOverflow()
    })
  }
})

describe('OrganizationMembersPage over-long cell values in Chromium', () => {
  const LONG_DISPLAY_NAME = 'Alexandria Cassandra Montgomery-Worthington'
  const LONG_ACCOUNT =
    'account-identifier-with-a-long-unbroken-value-0000000001'
  const LONG_EMAIL =
    'first.last.with.a.very.long.mailbox.name@example-organization-with-a-long-domain.test'

  const LONG_MEMBER: AdminMember = {
    ...ADA,
    account: LONG_ACCOUNT,
    display_name: LONG_DISPLAY_NAME,
  }
  const LONG_INVITATION: AdminInvitation = {
    ...INVITATIONS[0],
    email: LONG_EMAIL,
  }

  it('caps over-long member and invitation values at the 900px tier and keeps each full value reachable', async () => {
    await page.viewport(900, 900)
    renderPage({}, undefined, backend([LONG_MEMBER], [LONG_INVITATION]))

    await screen.findAllByText(LONG_DISPLAY_NAME)
    await screen.findAllByText(LONG_EMAIL)
    const members = tableFor(enUS.members.column.displayName)
    const invitations = tableFor(enUS.invitations.column.email)

    // The values are longer than their columns' content caps. At 900px the
    // container is below the md tier, so the primary, high and action
    // columns share the narrow width with those long values. The display
    // name and account are both nowrap identity columns, so their caps --
    // tightened below `md` so the row still fits the sm floor -- bound the
    // members table's unbreakable width, and the full values stay reachable
    // through the title and copy control. Both tables must stay inside their
    // own boxes and inside every tier floor.
    await expectNoHorizontalOverflow()

    for (const [container, value] of [
      [members.container, LONG_DISPLAY_NAME],
      [members.container, LONG_ACCOUNT],
      [invitations.container, LONG_EMAIL],
    ] as const) {
      const cell = bodyCellWith(container, value)
      const copy = within(cell).queryByRole('button')
      expect(
        cell.getAttribute('title') === value || copy !== null,
        `the over-long value "${value}" has no full-value channel`,
      ).toBe(true)
    }
  })

  it('keeps both identity columns inside the 600px tier floor when a long email meets a long resolved inviter', async () => {
    await page.viewport(600, 900)
    // The inviter resolves through the members read to a display name as
    // long as the cap, so both identity columns carry their whole cap and
    // the row's unbreakable width has to fit the sm floor together with
    // status, both timestamps and the actions column.
    const invitation: AdminInvitation = {
      ...LONG_INVITATION,
      invited_by: LONG_ACCOUNT,
    }
    renderPage({}, undefined, backend([LONG_MEMBER], [invitation]))

    await screen.findAllByText(LONG_DISPLAY_NAME)
    await screen.findAllByText(LONG_EMAIL)
    // The strict per-tier check, not the page-level one: each table's
    // containment box is pinned to every tier floor and the visible
    // columns must fit without the scroll fallback.
    await expectNoHorizontalOverflow()
  })
})

describe('OrganizationMembersPage single-token identity values in Chromium', () => {
  // A single-token identity is what an operator reads to tell one row from
  // another: it must never be split mid-token, however narrow the column is
  // laid out. The token is longer than the squeezed column, which is the
  // case a wrapping policy splits and one-line truncation keeps whole.
  const IDENTITY_NAME = 'AlexandriaCassandraMontgomeryWorthington'
  // The account is an opaque identifier and every member carries one, so
  // the display-name column competes with it for the same row width.
  const IDENTITY_ACCOUNT =
    'account-identifier-with-a-long-unbroken-value-0000002'
  const IDENTITY_MEMBER: AdminMember = {
    ...ADA,
    account: IDENTITY_ACCOUNT,
    display_name: IDENTITY_NAME,
  }
  // The inviter is a second member so the invitation's resolved name stays
  // a short token while the members table still carries the long one.
  const INVITER_NAME = 'Demo'
  const INVITER_ACCOUNT =
    'account-identifier-with-a-long-unbroken-value-0000003'
  const INVITER_MEMBER: AdminMember = {
    ...ADA,
    account: INVITER_ACCOUNT,
    display_name: INVITER_NAME,
  }
  const INVITATION: AdminInvitation = {
    ...INVITATIONS[0],
    invited_by: INVITER_ACCOUNT,
  }

  it('keeps a single-token display name and inviter on one line in the 1024px shell', async () => {
    await page.viewport(1024, 900)
    renderShellPage(backend([IDENTITY_MEMBER, INVITER_MEMBER], [INVITATION]))

    await screen.findAllByText(IDENTITY_NAME)
    await screen.findByText(INVITATION.email)
    // The shell's content region is ~640-700px here, above the sm container
    // floor: both tables stay inside it and inside every tier floor, and each
    // table's visible columns stay on screen rather than scrolling out.
    await expectNoHorizontalOverflow()

    const members = tableFor(enUS.members.column.displayName)
    const invitations = tableFor(enUS.invitations.column.email)

    for (const header of [
      enUS.members.column.displayName,
      enUS.members.column.account,
      enUS.members.column.roles,
      enUS.members.column.status,
      enUS.members.column.joinedAt,
      enUS.members.column.actions,
    ]) {
      expectHeaderOnScreen(members.container, header)
    }
    for (const header of [
      enUS.invitations.column.email,
      enUS.invitations.column.status,
      enUS.invitations.column.invitedBy,
      enUS.invitations.column.expires,
      enUS.invitations.column.created,
      enUS.invitations.column.actions,
    ]) {
      expectHeaderOnScreen(invitations.container, header)
    }

    const memberNameCell = bodyCellWith(members.container, IDENTITY_NAME)
    expect(
      lineBoxCount(memberNameCell, IDENTITY_NAME),
      'the member display name is split across lines',
    ).toBe(1)
    const inviterCell = bodyCellWith(invitations.container, INVITER_NAME)
    expect(
      lineBoxCount(inviterCell, INVITER_NAME),
      'the inviter name is split across lines',
    ).toBe(1)
  })

  it('leaves the 600px viewport behavior unchanged', async () => {
    await page.viewport(600, 900)
    renderPage(
      {},
      undefined,
      backend([IDENTITY_MEMBER, INVITER_MEMBER], [INVITATION]),
    )

    await screen.findAllByText(IDENTITY_NAME)
    await screen.findByText(INVITATION.email)
    // At this viewport the content region is below the sm container floor,
    // so only the unprioritized columns stay and the table's own scroll is
    // the documented fallback; the page must not gain a scrollbar of its own.
    await expectNoPageHorizontalOverflow()

    const members = tableFor(enUS.members.column.displayName)
    const memberNameCell = bodyCellWith(members.container, IDENTITY_NAME)
    expect(
      lineBoxCount(memberNameCell, IDENTITY_NAME),
      'the member display name is split across lines at 600px',
    ).toBe(1)
  })
})

describe('OrganizationMembersPage column priorities in Chromium', () => {
  it('keeps the core member and invitation columns from the high tier up', async () => {
    await page.viewport(1200, 900)
    renderPage()
    await screen.findByText(ADA.display_name)
    await screen.findByText(INVITATIONS[0].email)

    const members = tableFor(enUS.members.column.displayName)
    const invitations = tableFor(enUS.invitations.column.email)
    const memberHeaders = [
      enUS.members.column.roles,
      enUS.members.column.status,
      enUS.members.column.joinedAt,
    ]
    const invitationHeaders = [
      enUS.invitations.column.invitedBy,
      enUS.invitations.column.expires,
      enUS.invitations.column.created,
    ]

    // High priority clears at the sm container floor (600px) and stays from
    // there up -- the regression this guards is a `medium` column, which
    // would disappear again at 600, and a `low` one, at 1200.
    for (const width of [600, 900, 1200] as const) {
      atContainerWidth(members.containment, width, () => {
        for (const header of memberHeaders) {
          expect(
            headerDisplay(members.container, header),
            `the ${header} column is hidden at the ${width}px tier`,
          ).toBe('table-cell')
          expect(headerWidth(members.container, header)).toBeGreaterThan(0)
        }
      })
      atContainerWidth(invitations.containment, width, () => {
        for (const header of invitationHeaders) {
          expect(
            headerDisplay(invitations.container, header),
            `the ${header} column is hidden at the ${width}px tier`,
          ).toBe('table-cell')
          expect(headerWidth(invitations.container, header)).toBeGreaterThan(0)
        }
      })
    }

    // Below sm the columns shed, which is the mobile limitation the design
    // documents: at a 600px viewport only the primary and action columns
    // remain.
    atContainerWidth(members.containment, 599, () => {
      for (const header of memberHeaders) {
        expect(headerDisplay(members.container, header)).toBe('none')
      }
    })
    atContainerWidth(invitations.containment, 599, () => {
      for (const header of invitationHeaders) {
        expect(headerDisplay(invitations.container, header)).toBe('none')
      }
    })
  })

  it('keeps the core columns visible in the 1024px desktop shell beside the 280px sidebar', async () => {
    await page.viewport(1024, 900)
    renderShellPage()
    await screen.findByText(ADA.display_name)
    await screen.findByText(INVITATIONS[0].email)
    // The shell's content region is ~640-700px wide here, above the sm
    // container floor; the page must fit it without a horizontal scroll.
    await expectNoHorizontalOverflow()

    const members = tableFor(enUS.members.column.displayName)
    const invitations = tableFor(enUS.invitations.column.email)
    const memberHeaders = [
      enUS.members.column.roles,
      enUS.members.column.status,
      enUS.members.column.joinedAt,
    ]
    const invitationHeaders = [
      enUS.invitations.column.invitedBy,
      enUS.invitations.column.expires,
      enUS.invitations.column.created,
    ]

    // A header can be laid out yet hidden with `display: none`; toBeVisible
    // is the assertion that the real cell is on screen, not just in the DOM.
    for (const header of memberHeaders) {
      expect(
        within(members.container).getByRole('columnheader', {
          name: header,
          exact: true,
        }),
      ).toBeVisible()
    }
    for (const header of invitationHeaders) {
      expect(
        within(invitations.container).getByRole('columnheader', {
          name: header,
          exact: true,
        }),
      ).toBeVisible()
    }

    // The same is true of the data cells, not only the headers.
    const memberRow = within(members.container).getByRole('row', {
      name: new RegExp(ADA.display_name),
    })
    const memberCells = within(memberRow).getAllByRole('cell')
    for (const header of memberHeaders) {
      expect(
        memberCells[columnIndexIn(members.container, header)],
        `the ${header} data cell is hidden`,
      ).toBeVisible()
    }
    const invitationRow = within(invitations.container).getByRole('row', {
      name: new RegExp(INVITATIONS[0].email),
    })
    const invitationCells = within(invitationRow).getAllByRole('cell')
    for (const header of invitationHeaders) {
      expect(
        invitationCells[columnIndexIn(invitations.container, header)],
        `the ${header} data cell is hidden`,
      ).toBeVisible()
    }
  })
})

describe('OrganizationMembersPage management panel width in Chromium', () => {
  it('takes the 36rem tier on a wide viewport', async () => {
    await page.viewport(1200, 900)
    renderPage()
    await screen.findByText(ADA.display_name)

    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: manageName(ADA) }))
    const panel = await screen.findByRole('dialog', {
      name: enUS.members.panel.title,
    })
    await waitFor(() =>
      expect(panel.getBoundingClientRect().width).toBeGreaterThan(0),
    )

    // 36rem at the default root size; the panel must not stretch across the
    // whole viewport, where the page behind it would be unreadable.
    const width = panel.getBoundingClientRect().width
    expect(width).toBeGreaterThanOrEqual(560)
    expect(width).toBeLessThanOrEqual(592)
    expect(width).toBeLessThan(document.documentElement.clientWidth)
  })

  it('fills a phone-sized viewport', async () => {
    await page.viewport(480, 800)
    renderPage()
    await screen.findByText(ADA.display_name)

    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: manageName(ADA) }))
    const panel = await screen.findByRole('dialog', {
      name: enUS.members.panel.title,
    })
    await waitFor(() =>
      expect(panel.getBoundingClientRect().width).toBeGreaterThan(0),
    )
    expect(
      Math.abs(
        panel.getBoundingClientRect().width -
          document.documentElement.clientWidth,
      ),
    ).toBeLessThanOrEqual(1.5)
  })
})

describe('OrganizationMembersPage dark mode in Chromium', () => {
  it('paints the page on the dark surface and keeps the common columns', async () => {
    await page.viewport(1200, 900)
    renderPage({ defaultMode: 'dark' }, <ThemeProbe />)
    await screen.findByText(ADA.display_name)
    await screen.findByText(INVITATIONS[0].email)

    const probe = screen.getByTestId('theme-probe')
    expect(probe.dataset.mode).toBe('dark')
    const surface = getComputedStyle(probe).backgroundColor
    expect(getComputedStyle(document.body).backgroundColor).toBe(surface)
    const channels = surface.match(/\d+/g)?.map(Number) ?? []
    expect(channels).toHaveLength(3)
    expect(
      Math.max(...channels),
      `the dark surface is not dark (${surface})`,
    ).toBeLessThan(128)

    await expectNoHorizontalOverflow()

    const members = tableFor(enUS.members.column.displayName)
    const invitations = tableFor(enUS.invitations.column.email)
    atContainerWidth(members.containment, 900, () => {
      expect(
        headerDisplay(members.container, enUS.members.column.roles),
      ).toBe('table-cell')
    })
    atContainerWidth(invitations.containment, 900, () => {
      expect(
        headerDisplay(invitations.container, enUS.invitations.column.expires),
      ).toBe('table-cell')
    })
  })
})

describe('OrganizationMembersPage touch targets in Chromium', () => {
  it('raises every interactive control to the 44px floor under a coarse pointer', async () => {
    await page.viewport(1200, 900)
    const session = cdp() as unknown as {
      send(method: string, params?: Record<string, unknown>): Promise<unknown>
    }
    await session.send('Emulation.setTouchEmulationEnabled', {
      enabled: true,
      maxTouchPoints: 1,
    })

    await waitFor(() =>
      expect(window.matchMedia('(pointer: coarse)').matches).toBe(true),
    )

    renderPage()
    await screen.findByText(ADA.display_name)
    await screen.findByText(INVITATIONS[0].email)

    // The panel's own controls are part of the page's touch surface: open it
    // so the scan covers a row action and the panel together.
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: manageName(ADA) }))
    await screen.findByRole('dialog', { name: enUS.members.panel.title })

    // The page's own controls, the Select trigger's `div[role=combobox]`
    // included. The trigger is not an `input`: the InputBase `root`/`input`
    // slots do not bound it, and MUI's own `min-height` on the `select`
    // slot wins the cascade, so it only reaches the floor through the
    // theme's `MuiSelect` rule. The visually hidden native input is still
    // skipped -- it is `aria-hidden` and carries no hit area of its own.
    const controls = Array.from(
      document.querySelectorAll<HTMLElement>(
        'button, a[href], input:not(.MuiSelect-nativeInput), [role="combobox"]',
      ),
    ).filter((control) => {
      const rect = control.getBoundingClientRect()
      return (
        rect.width > 0 &&
        rect.height > 0 &&
        getComputedStyle(control).visibility !== 'hidden'
      )
    })
    // A run that measured nothing would be vacuously green.
    expect(controls.length).toBeGreaterThan(5)
    // Nor may the Select trigger be skipped: the panel's role field is the
    // element that exposed the theme gap, so a scan without it would not
    // prove the fix.
    expect(
      controls.filter((control) =>
        control.matches(`.${selectClasses.select}`),
      ).length,
    ).toBeGreaterThan(0)
    for (const control of controls) {
      const rect = control.getBoundingClientRect()
      expect(
        rect.width,
        `"${controlName(control)}" is narrower than the touch floor`,
      ).toBeGreaterThanOrEqual(43.5)
      expect(
        rect.height,
        `"${controlName(control)}" is shorter than the touch floor`,
      ).toBeGreaterThanOrEqual(43.5)
    }
  })
})
