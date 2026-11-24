
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
import { themePresets } from '@speed/theme-presets'
import type { ThemePreset } from '@speed/theme-presets'
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
  username: '',
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
  username: '',
  source: 'invite',
  sso_exempt: false,
  roles: [],
  joined_at: '2026-10-20T08:30:00Z',
}

const MEMBERS: readonly AdminMember[] = [ADA, BOB]

const INVITATION_ADA: AdminInvitation = {
  id: 'inv-1',
  tenant: TENANT,
  email: 'ada@example.com',
  invited_by: SELF,
  invited_by_name: ADA.display_name,
  invited_by_username: 'ada',
  status: 'pending',
  state: 'pending',
  created_at: '2026-10-15T12:00:00Z',
  expires_at: '2099-01-01T00:00:00Z',
  accepted_at: null,
  revoked_at: null,
  last_sent_at: '2026-10-15T12:00:00Z',
  send_count: 1,
}

const INVITATIONS: readonly AdminInvitation[] = [INVITATION_ADA]

const SETTLED_AT = '2026-06-01T12:00:00Z'
const STATE_INVITATIONS: readonly AdminInvitation[] = [
  { ...INVITATION_ADA, id: 'inv-pending', email: 'pending@example.com' },
  {
    ...INVITATION_ADA,
    id: 'inv-expired',
    email: 'expired@example.com',
    status: 'pending',
    state: 'expired',
    expires_at: '2020-01-01T00:00:00Z',
  },
  {
    ...INVITATION_ADA,
    id: 'inv-accepted',
    email: 'accepted@example.com',
    status: 'accepted',
    state: 'accepted',
    accepted_at: SETTLED_AT,
  },
  {
    ...INVITATION_ADA,
    id: 'inv-revoked',
    email: 'revoked@example.com',
    status: 'revoked',
    state: 'revoked',
    revoked_at: SETTLED_AT,
  },
]

const SUPPORT_ROLE: AdminTenantRole = {
  tenant: TENANT,
  role: 'support',
  builtin: false,
  description: '',
  permission_count: 0,
  member_count: null,
  can_manage: false,
  created_at: '2026-01-01T00:00:00Z',
}

const AUDITOR_ROLE: AdminTenantRole = {
  ...SUPPORT_ROLE,
  role: 'auditor',
}

const ADA_BINDING: AdminTenantBinding = {
  tenant: TENANT,
  account: ADA.account,
  role: SUPPORT_ROLE.role,
  granted_at: '2026-10-15T12:00:00Z',
}

function backend(
  members: readonly AdminMember[] = MEMBERS,
  invitations: readonly AdminInvitation[] = INVITATIONS,
): AdminBackend {
  return makeAdminBackend({
    listMembers: () => Promise.resolve({ members: [...members], next_cursor: '' }),
    listInvitations: () =>
      Promise.resolve({ invitations: [...invitations], next_cursor: '' }),
    listTenantRoles: () => Promise.resolve([SUPPORT_ROLE]),
    listTenantAccountBindings: () => Promise.resolve([ADA_BINDING]),
  })
}

function renderPage(
  options: {
    readonly defaultMode?: 'light' | 'dark' | 'system'
    readonly presets?: readonly ThemePreset[]
    readonly defaultPreset?: string
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

function ThemeProbe() {
  const theme = useTheme()
  return (
    <div
      data-testid="theme-probe"
      data-mode={theme.palette.mode}
      data-radius={theme.shape.borderRadius}
      style={{ backgroundColor: theme.palette.background.default }}
    />
  )
}

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

function headerDisplay(container: HTMLElement, header: string): string {
  const cell = Array.from(container.querySelectorAll('thead th')).find(
    (candidate) => candidate.textContent?.trim() === header,
  )
  if (cell === undefined) {
    throw new Error(`the table has no "${header}" column header`)
  }
  return getComputedStyle(cell).display
}

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

function headerWidth(container: HTMLElement, header: string): number {
  const cell = Array.from(container.querySelectorAll('thead th')).find(
    (candidate) => candidate.textContent?.trim() === header,
  )
  if (cell === undefined) {
    throw new Error(`the table has no "${header}" column header`)
  }
  return cell.getBoundingClientRect().width
}

function bodyCellWith(container: HTMLElement, value: string): HTMLElement {
  const cell = Array.from(container.querySelectorAll<HTMLElement>('tbody td')).find(
    (candidate) => candidate.textContent?.includes(value),
  )
  if (cell === undefined) {
    throw new Error(`no body cell carries "${value}"`)
  }
  return cell
}

interface Rgba {
  readonly r: number
  readonly g: number
  readonly b: number
  readonly a: number
}

function parseRgba(value: string): Rgba {
  const match = /^rgba?\(([^)]+)\)$/.exec(value.trim())
  if (match === null) {
    throw new Error(`unexpected computed color "${value}"`)
  }
  const parts = match[1]!.split(/[\s,/]+/).filter(Boolean).map(Number)
  if (parts.length < 3) {
    throw new Error(`unexpected computed color "${value}"`)
  }
  return {
    r: parts[0]!,
    g: parts[1]!,
    b: parts[2]!,
    a: parts[3] === undefined ? 1 : parts[3],
  }
}

function compositeLayer(front: Rgba, back: Rgba): Rgba {
  const blend = (f: number, b: number): number =>
    Math.round(front.a * f + (1 - front.a) * b)
  return {
    r: blend(front.r, back.r),
    g: blend(front.g, back.g),
    b: blend(front.b, back.b),
    a: 1,
  }
}

function flattenedBackground(element: Element): Rgba {
  const layers: Rgba[] = []
  for (
    let node: Element | null = element;
    node !== null;
    node = node.parentElement
  ) {
    const color = parseRgba(getComputedStyle(node).backgroundColor)
    if (color.a > 0) {
      layers.push(color)
      if (color.a >= 1) break
    }
  }
  let result: Rgba = { r: 255, g: 255, b: 255, a: 1 }
  for (let index = layers.length - 1; index >= 0; index -= 1) {
    result = compositeLayer(layers[index]!, result)
  }
  return result
}

function hexRgba(value: string): Rgba {
  const hex = value.replace(/^#/, '')
  return {
    r: Number.parseInt(hex.slice(0, 2), 16),
    g: Number.parseInt(hex.slice(2, 4), 16),
    b: Number.parseInt(hex.slice(4, 6), 16),
    a: 1,
  }
}

function flattenedOver(element: Element, base: Rgba): Rgba {
  const layers: Rgba[] = []
  for (
    let node: Element | null = element;
    node !== null && node !== document.body;
    node = node.parentElement
  ) {
    const color = parseRgba(getComputedStyle(node).backgroundColor)
    if (color.a > 0) {
      layers.push(color)
      if (color.a >= 1) break
    }
  }
  let result = base
  for (let index = layers.length - 1; index >= 0; index -= 1) {
    result = compositeLayer(layers[index]!, result)
  }
  return result
}

function linearChannel(channel: number): number {
  const value = channel / 255
  return value <= 0.04045 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4
}

function luminance(color: Rgba): number {
  return (
    0.2126 * linearChannel(color.r) +
    0.7152 * linearChannel(color.g) +
    0.0722 * linearChannel(color.b)
  )
}

function contrastRatio(first: Rgba, second: Rgba): number {
  const a = luminance(first)
  const b = luminance(second)
  const lighter = Math.max(a, b)
  const darker = Math.min(a, b)
  return (lighter + 0.05) / (darker + 0.05)
}

function statusChip(label: string): HTMLElement {
  const table = tableFor(enUS.invitations.column.email).container
  const labelNode = within(table).getByText(label, { exact: true })
  const chip = labelNode.closest<HTMLElement>('.MuiChip-root')
  if (chip === null) {
    throw new Error(`the "${label}" status has no chip`)
  }
  return chip
}

async function waitForMemberName(displayName: string): Promise<void> {
  await waitFor(() => {
    expect(
      bodyCellWith(
        tableFor(enUS.members.column.displayName).container,
        displayName,
      ),
    ).toBeInTheDocument()
  })
}

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

function expectHeaderOnScreen(container: HTMLElement, header: string): void {
  const cell = within(container).getByRole('columnheader', {
    name: header,
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

// Truncation reports several rects for one line, so count by line top.
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

const SM_FLOOR = 600

// Headroom beyond the strict containment check: the layout promises no
// overflow, not this margin.
const TIER_HEADROOM = 8

// The container's scrollWidth cannot answer the required width: while the
// columns fit, the table stretches to fill the box.
function requiredWidthAtFloor(
  containment: HTMLElement,
  container: HTMLElement,
  floor: number,
): number {
  const table = container.querySelector('table')
  if (table === null) {
    throw new Error('the table has no <table> element')
  }
  const originalContainment = containment.style.width
  const originalTable = table.style.width
  try {
    containment.style.width = `${floor}px`
    table.style.width = 'min-content'
    void table.offsetWidth
    return table.getBoundingClientRect().width
  } finally {
    table.style.width = originalTable
    containment.style.width = originalContainment
  }
}

function manageName(member: AdminMember): string {
  const name =
    member.username === ''
      ? member.display_name
      : `${member.display_name} (${member.username})`
  return enUS.members.manageAria.replace('{{name}}', name)
}

function controlName(control: HTMLElement): string {
  return (
    control.getAttribute('aria-label') ??
    control.textContent?.trim() ??
    control.tagName.toLowerCase()
  )
}

beforeEach(() => {
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
  // Reset touch emulation: the rest of the tier relies on the hovering,
  // fine pointer.
  const session = cdp() as unknown as {
    send(method: string, params?: Record<string, unknown>): Promise<unknown>
  }
  await session.send('Emulation.setTouchEmulationEnabled', { enabled: false })
  await waitFor(() => {
    expect(
      window.matchMedia('(hover: hover) and (pointer: fine)').matches,
      'turning touch emulation off did not restore the hovering, fine pointer',
    ).toBe(true)
  })
})

describe('OrganizationMembersPage horizontal overflow in Chromium', () => {
  for (const width of TIER_FLOORS) {
    it(`keeps both tables inside a ${width}px viewport`, async () => {
      await page.viewport(width, 900)
      renderPage()

      await waitForMemberName(ADA.display_name)
      await screen.findByText(INVITATION_ADA.email)
      await expectNoHorizontalOverflow()
    })
  }
})

describe('OrganizationMembersPage over-long cell values in Chromium', () => {
  const LONG_DISPLAY_NAME = 'Alexandria Cassandra Montgomery-Worthington'
  const LONG_ACCOUNT =
    'account-identifier-with-a-long-unbroken-value-0000000001'
  const LONG_USERNAME =
    'alexandria.cassandra.montgomery-worthington.the.third'
  const LONG_EMAIL =
    'first.last.with.a.very.long.mailbox.name@example-organization-with-a-long-domain.test'

  const LONG_MEMBER: AdminMember = {
    ...ADA,
    account: LONG_ACCOUNT,
    display_name: LONG_DISPLAY_NAME,
    username: LONG_USERNAME,
  }
  const LONG_INVITATION: AdminInvitation = {
    ...INVITATION_ADA,
    email: LONG_EMAIL,
    invited_by: LONG_ACCOUNT,
    invited_by_name: LONG_DISPLAY_NAME,
    invited_by_username: LONG_USERNAME,
  }

  it('caps over-long member and invitation values at the 900px tier and keeps each full value reachable', async () => {
    await page.viewport(900, 900)
    renderPage({}, undefined, backend([LONG_MEMBER], [LONG_INVITATION]))

    await screen.findAllByText(LONG_DISPLAY_NAME)
    await screen.findAllByText(LONG_EMAIL)
    const members = tableFor(enUS.members.column.displayName)
    const invitations = tableFor(enUS.invitations.column.email)

    await expectNoHorizontalOverflow()

    for (const [container, value] of [
      [members.container, LONG_DISPLAY_NAME],
      [members.container, LONG_USERNAME],
      [invitations.container, LONG_EMAIL],
    ] as const) {
      const cell = bodyCellWith(container, value)
      const copy = within(cell).queryByRole('button')
      const titled = cell.querySelector<HTMLElement>('[title]')
      expect(
        titled?.getAttribute('title') === value || copy !== null,
        `the over-long value "${value}" has no full-value channel`,
      ).toBe(true)
    }

    expect(
      bodyCellWith(members.container, LONG_DISPLAY_NAME)
        .querySelector<HTMLElement>('[title]')
        ?.getAttribute('title'),
    ).toBe(LONG_DISPLAY_NAME)
  })

  for (const status of [
    'pending',
    'expired',
    'accepted',
    'revoked',
  ] as const) {
    it(`keeps both invitation identity columns inside the 600px tier floor when a long email meets a long resolved inviter (${status})`, async () => {
      await page.viewport(600, 900)
      const invitation: AdminInvitation = {
        ...LONG_INVITATION,
        invited_by: LONG_ACCOUNT,
        status: status === 'expired' ? 'pending' : status,
        state: status,
        expires_at:
          status === 'expired'
            ? '2020-01-01T00:00:00Z'
            : LONG_INVITATION.expires_at,
        accepted_at: status === 'accepted' ? SETTLED_AT : null,
        revoked_at: status === 'revoked' ? SETTLED_AT : null,
      }
      renderPage({}, undefined, backend([LONG_MEMBER], [invitation]))

      await screen.findAllByText(LONG_DISPLAY_NAME)
      await screen.findAllByText(LONG_EMAIL)
      await expectNoHorizontalOverflow()

      const invitations = tableFor(enUS.invitations.column.email)
      expect(
        requiredWidthAtFloor(
          invitations.containment,
          invitations.container,
          SM_FLOOR,
        ),
        `the ${status} row leaves less than the tier headroom inside the 600px floor`,
      ).toBeLessThanOrEqual(SM_FLOOR - TIER_HEADROOM)
    })
  }

  it('keeps the member table inside the 600px tier floor when a long identity meets a suspended member', async () => {
    await page.viewport(600, 900)
    const suspended: AdminMember = {
      ...LONG_MEMBER,
      active: false,
    }
    renderPage({}, undefined, backend([suspended], []))

    await screen.findAllByText(LONG_DISPLAY_NAME)
    await expectNoHorizontalOverflow()

    const members = tableFor(enUS.members.column.displayName)
    expect(
      requiredWidthAtFloor(members.containment, members.container, SM_FLOOR),
      'the suspended member row leaves less than the tier headroom inside the 600px floor',
    ).toBeLessThanOrEqual(SM_FLOOR - TIER_HEADROOM)
  })

  it('keeps the invitation table inside the 600px tier floor when the copy-link action is present', async () => {
    await page.viewport(600, 900)
    const invitation: AdminInvitation = {
      ...LONG_INVITATION,
      status: 'pending',
      state: 'pending',
    }
    renderPage({}, undefined, backend([LONG_MEMBER], [invitation]))

    await screen.findAllByText(LONG_DISPLAY_NAME)
    await screen.findAllByText(LONG_EMAIL)

    const user = userEvent.setup()
    await user.type(
      screen.getByRole('textbox', {
        name: enUS.invitations.issue.emailLabel,
      }),
      'someone@example.com',
    )
    await user.click(
      screen.getByRole('button', { name: enUS.invitations.issue.submit }),
    )
    await screen.findByRole('button', { name: enUS.invitations.copyLink })

    await expectNoHorizontalOverflow()

    const invitations = tableFor(enUS.invitations.column.email)
    expect(
      requiredWidthAtFloor(
        invitations.containment,
        invitations.container,
        SM_FLOOR,
      ),
      'the copy-link row leaves less than the tier headroom inside the 600px floor',
    ).toBeLessThanOrEqual(SM_FLOOR - TIER_HEADROOM)
  })
})

describe('OrganizationMembersPage status tag readability in Chromium', () => {
  const DEFAULT_RADIUS = 8
  for (const preset of themePresets) {
    for (const mode of ['light', 'dark'] as const) {
      it(`keeps the four state tags readable under the ${preset.id} preset in ${mode} mode`, async () => {
        await page.viewport(1280, 900)
        renderPage(
          {
            defaultMode: mode,
            presets: themePresets,
            defaultPreset: preset.id,
          },
          <ThemeProbe />,
          backend(MEMBERS, STATE_INVITATIONS),
        )

        await screen.findAllByText('pending@example.com')
        const probe = screen.getByTestId('theme-probe')
        expect(probe.getAttribute('data-mode')).toBe(mode)
        expect(Number(probe.getAttribute('data-radius'))).toBe(
          preset.tokens.shape?.borderRadius ?? DEFAULT_RADIUS,
        )

        const canvas = preset.tokens.color?.background?.canvas?.[mode]
        const backdrops =
          canvas === undefined
            ? [flattenedBackground(statusChip(enUS.invitations.status.pending))]
            : [hexRgba(canvas.from), hexRgba(canvas.to)]
        if (canvas !== undefined) {
          const backgroundImage = getComputedStyle(document.body).backgroundImage
          for (const stop of [canvas.from, canvas.to]) {
            const { r, g, b } = hexRgba(stop)
            expect(
              backgroundImage,
              `${preset.id} ${mode}: the body paints the ${stop} stop`,
            ).toContain(`rgb(${r}, ${g}, ${b})`)
          }
        }

        for (const state of ['pending', 'expired', 'accepted', 'revoked'] as const) {
          const chip = statusChip(enUS.invitations.status[state])
          const text = parseRgba(getComputedStyle(chip).color)
          for (const backdrop of backdrops) {
            const ratio = contrastRatio(
              text,
              canvas === undefined ? backdrop : flattenedOver(chip, backdrop),
            )
            expect(
              ratio,
              `${preset.id} ${mode}: the ${state} status text`,
            ).toBeGreaterThanOrEqual(4.5)
          }
        }
      })
    }
  }
})

describe('OrganizationMembersPage single-token identity values in Chromium', () => {
  const IDENTITY_NAME = 'AlexandriaCassandraMontgomeryWorthington'
  const IDENTITY_ACCOUNT =
    'account-identifier-with-a-long-unbroken-value-0000002'
  const IDENTITY_MEMBER: AdminMember = {
    ...ADA,
    account: IDENTITY_ACCOUNT,
    display_name: IDENTITY_NAME,
  }
  const INVITER_NAME = 'Demo'
  const INVITER_ACCOUNT =
    'account-identifier-with-a-long-unbroken-value-0000003'
  const INVITER_MEMBER: AdminMember = {
    ...ADA,
    account: INVITER_ACCOUNT,
    display_name: INVITER_NAME,
  }
  const INVITATION: AdminInvitation = {
    ...INVITATION_ADA,
    invited_by: INVITER_ACCOUNT,
    invited_by_name: INVITER_NAME,
    invited_by_username: 'demo',
  }

  it('keeps a single-token display name and inviter on one line in the 1024px shell', async () => {
    await page.viewport(1024, 900)
    renderShellPage(backend([IDENTITY_MEMBER, INVITER_MEMBER], [INVITATION]))

    await screen.findAllByText(IDENTITY_NAME)
    await screen.findByText(INVITATION.email)
    await expectNoHorizontalOverflow()

    const members = tableFor(enUS.members.column.displayName)
    const invitations = tableFor(enUS.invitations.column.email)

    for (const header of [
      enUS.members.column.displayName,
      enUS.members.column.username,
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
    await waitForMemberName(ADA.display_name)
    await screen.findByText(INVITATION_ADA.email)

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
    await waitForMemberName(ADA.display_name)
    await screen.findByText(INVITATION_ADA.email)
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

    for (const header of memberHeaders) {
      expect(
        within(members.container).getByRole('columnheader', {
          name: header,
        }),
      ).toBeVisible()
    }
    for (const header of invitationHeaders) {
      expect(
        within(invitations.container).getByRole('columnheader', {
          name: header,
        }),
      ).toBeVisible()
    }

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
      name: new RegExp(INVITATION_ADA.email),
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
    await waitForMemberName(ADA.display_name)

    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: manageName(ADA) }))
    const panel = await screen.findByRole('dialog', {
      name: enUS.members.panel.title,
    })
    await waitFor(() =>
      expect(panel.getBoundingClientRect().width).toBeGreaterThan(0),
    )

    const width = panel.getBoundingClientRect().width
    expect(width).toBeGreaterThanOrEqual(560)
    expect(width).toBeLessThanOrEqual(592)
    expect(width).toBeLessThan(document.documentElement.clientWidth)
  })

  it('fills a phone-sized viewport', async () => {
    await page.viewport(480, 800)
    renderPage()
    await waitForMemberName(ADA.display_name)

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
    await waitForMemberName(ADA.display_name)
    await screen.findByText(INVITATION_ADA.email)

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
    await waitForMemberName(ADA.display_name)
    await screen.findByText(INVITATION_ADA.email)

    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: manageName(ADA) }))
    await screen.findByRole('dialog', { name: enUS.members.panel.title })

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
    expect(controls.length).toBeGreaterThan(5)
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

function deferred<T>(): {
  readonly promise: Promise<T>
  readonly resolve: (value: T) => void
} {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((settle) => {
    resolve = settle
  })
  return { promise, resolve }
}

function nextFrame(): Promise<void> {
  return new Promise((resolve) => {
    requestAnimationFrame(() => resolve())
  })
}

function describeActiveElement(): string {
  const active = document.activeElement
  if (active === null) {
    return 'null'
  }
  const label = active.getAttribute('aria-label')
  const text = (active.textContent ?? '').replace(/\s+/g, ' ').trim().slice(0, 80)
  return `${active.tagName.toLowerCase()}${
    label === null ? '' : ` aria-label="${label}"`
  }${text === '' ? '' : ` text="${text}"`}`
}

describe('OrganizationMembersPage write focus in Chromium', () => {
  it('keeps focus on the panel bind control while the bind is in flight', async () => {
    await page.viewport(1200, 900)
    const written = deferred<void>()
    const scripted = makeAdminBackend({
      listMembers: () =>
        Promise.resolve({ members: [...MEMBERS], next_cursor: '' }),
      listInvitations: () =>
        Promise.resolve({ invitations: [...INVITATIONS], next_cursor: '' }),
      listTenantRoles: () =>
        Promise.resolve([SUPPORT_ROLE, AUDITOR_ROLE]),
      listTenantAccountBindings: () => Promise.resolve([ADA_BINDING]),
      bindTenantRole: () => written.promise,
    })
    renderPage({}, undefined, scripted)
    await waitForMemberName(ADA.display_name)

    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: manageName(ADA) }))
    const panel = await screen.findByRole('dialog', {
      name: enUS.members.panel.title,
    })
    const select = within(panel).getByRole('combobox', {
      name: enUS.members.panel.roleLabel,
    })
    await waitFor(() => expect(select).toBeEnabled())
    await user.click(select)
    await user.click(
      await screen.findByRole('option', { name: AUDITOR_ROLE.role }),
    )
    const bind = within(panel).getByRole('button', {
      name: enUS.members.panel.bind,
    })
    await waitFor(() =>
      expect(
        within(panel).queryByText(enUS.members.panel.escalationChecking),
      ).toBeNull(),
    )
    await user.click(bind)

    await waitFor(() =>
      expect(within(bind).getByRole('progressbar')).toBeInTheDocument(),
    )
    await nextFrame()
    expect(
      bind,
      `focus while the bind is in flight; document.activeElement is ${describeActiveElement()}`,
    ).toHaveFocus()
    expect(bind).toHaveAttribute('aria-busy', 'true')

    written.resolve()
    await waitFor(() =>
      expect(within(bind).queryByRole('progressbar')).toBeNull(),
    )
  })

  it('keeps an unavailable bind control focusable and points it at the reason', async () => {
    await page.viewport(1200, 900)
    renderPage({}, undefined, backend([ADA, BOB]))
    await waitForMemberName(BOB.display_name)

    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: manageName(BOB) }))
    const panel = await screen.findByRole('dialog', {
      name: enUS.members.panel.title,
    })
    const bind = within(panel).getByRole('button', {
      name: enUS.members.panel.bind,
    })
    await waitFor(() =>
      expect(bind).toHaveAttribute('aria-disabled', 'true'),
    )
    bind.focus()
    expect(bind).toHaveFocus()
    expect(bind).toHaveAccessibleDescription(
      enUS.members.panel.suspendedBindNote,
    )
  })

  it('moves focus to the next binding row after an unbind lands, inside the panel', async () => {
    await page.viewport(1200, 900)
    let bindings: AdminTenantBinding[] = [
      { ...ADA_BINDING, role: SUPPORT_ROLE.role },
      { ...ADA_BINDING, role: AUDITOR_ROLE.role },
    ]
    const scripted = makeAdminBackend({
      listMembers: () => Promise.resolve({ members: [ADA], next_cursor: '' }),
      listInvitations: () => Promise.resolve({ invitations: [], next_cursor: '' }),
      listTenantRoles: () => Promise.resolve([SUPPORT_ROLE, AUDITOR_ROLE]),
      listTenantAccountBindings: () => Promise.resolve([...bindings]),
      unbindTenantRole: (input) => {
        bindings = bindings.filter((binding) => binding.role !== input.role)
        return Promise.resolve()
      },
    })
    renderPage({}, undefined, scripted)
    await waitForMemberName(ADA.display_name)

    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: manageName(ADA) }))
    const panel = await screen.findByRole('dialog', {
      name: enUS.members.panel.title,
    })
    const unbinds = within(panel).getAllByRole('button', {
      name: enUS.members.panel.unbind,
    })
    expect(unbinds).toHaveLength(2)
    await user.click(unbinds[0]!)

    await user.click(
      await screen.findByRole('button', { name: enUS.common.confirm }),
    )

    await waitFor(() =>
      expect(
        within(panel).getAllByRole('button', {
          name: enUS.members.panel.unbind,
        }),
      ).toHaveLength(1),
    )
    const remaining = within(panel).getByRole('button', {
      name: enUS.members.panel.unbind,
    })
    await waitFor(() =>
      expect(
        remaining,
        `focus after the unbind; document.activeElement is ${describeActiveElement()}`,
      ).toHaveFocus(),
    )
    expect(panel).toContainElement(document.activeElement as HTMLElement)
  })

  it("leaves the panel paper's focus alone when the reader clicks it during an unbind", async () => {
    await page.viewport(1200, 900)
    const removed = deferred<void>()
    let bindings: AdminTenantBinding[] = [
      { ...ADA_BINDING, role: SUPPORT_ROLE.role },
      { ...ADA_BINDING, role: AUDITOR_ROLE.role },
    ]
    const scripted = makeAdminBackend({
      listMembers: () => Promise.resolve({ members: [ADA], next_cursor: '' }),
      listInvitations: () => Promise.resolve({ invitations: [], next_cursor: '' }),
      listTenantRoles: () => Promise.resolve([SUPPORT_ROLE, AUDITOR_ROLE]),
      listTenantAccountBindings: () => Promise.resolve([...bindings]),
      unbindTenantRole: (input) =>
        removed.promise.then(() => {
          bindings = bindings.filter((binding) => binding.role !== input.role)
        }),
    })
    renderPage({}, undefined, scripted)
    await waitForMemberName(ADA.display_name)

    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: manageName(ADA) }))
    const panel = await screen.findByRole('dialog', {
      name: enUS.members.panel.title,
    })
    const unbinds = within(panel).getAllByRole('button', {
      name: enUS.members.panel.unbind,
    })
    expect(unbinds).toHaveLength(2)
    await user.click(unbinds[0]!)
    await user.click(
      await screen.findByRole('button', { name: enUS.common.confirm }),
    )

    await waitFor(() =>
      expect(unbinds[0]).toHaveAttribute('aria-busy', 'true'),
    )
    await user.click(panel)
    expect(
      panel,
      `focus after clicking the panel; document.activeElement is ${describeActiveElement()}`,
    ).toHaveFocus()

    removed.resolve()
    await waitFor(() =>
      expect(
        within(panel).getAllByRole('button', {
          name: enUS.members.panel.unbind,
        }),
      ).toHaveLength(1),
    )
    await nextFrame()
    expect(
      panel,
      `focus after the unbind; document.activeElement is ${describeActiveElement()}`,
    ).toHaveFocus()
  })
})
