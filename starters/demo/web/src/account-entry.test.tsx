/**
 * The account entry's placement: the skeleton puts it at the bottom of the
 * navigation column, and the header's avatar menu offers the same
 * unconditional entries. The menu carries the notification centre, the four
 * account panes and sign-out -- each with its own glyph; the organization admin
 * group joins it only while the caller's admission allows it. The navigation
 * column itself holds the home entry alone. The collapsed rail keeps the
 * avatar alone with the account still named, and the menu is fully
 * keyboard-operated.
 */

import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { attachSession } from '@speed/auth-core'
import { layoutKitResources } from '@speed/layout-kit'
import type { AppShellNavItem } from '@speed/layout-kit'
import {
  createQueryClient,
  createSpeedI18n,
  SpeedAppProviders,
} from '@speed/product-shell/bootstrap'
import type { SpeedAppServices } from '@speed/product-shell/bootstrap'
import { RouteProvider } from '@speed/route-kit'
import { AppServicesProvider } from './app-services.js'
import { AccountEntry } from './chrome.js'
import demoEnUS from './locales/en-US.json' with { type: 'json' }
import demoZhCN from './locales/zh-CN.json' with { type: 'json' }
import { DEMO_NAMESPACE, demoResources } from './resources.js'
import { createHarnessClient, renderDemoApp } from './test-utils/render.js'
import {
  authenticatedSnapshot,
  createTestSession,
} from './test-utils/session.js'

const layoutEn = layoutKitResources['en-US'] as {
  readonly appShell: {
    readonly collapseNav: string
    readonly navLabel: string
  }
}

/** The organization entries this suite hands the account entry: the shape the
 * tenant frame passes while the caller's admission allows the surface. The
 * fixture carries no `href` so the activation does not make jsdom attempt a
 * navigation it does not implement; the case asserts the dismiss channel. */
const ORGANIZATION_ITEMS: readonly AppShellNavItem[] = [
  { id: '/organization/admin/invitations', label: 'Invitations' },
]

/** Renders AccountEntry alone, under the production provider stack and the
 * app services it reads, so a case can supply its drawer close channel. */
function renderAccountEntry(options: {
  readonly closeDrawer?: () => void
  readonly organizationItems?: readonly AppShellNavItem[]
}) {
  const i18n = createSpeedI18n({
    supportedLanguages: ['zh-CN', 'en-US'],
    defaultLanguage: 'en-US',
    storage: null,
    urlParameterName: null,
    navigatorLanguages: [],
    namespaces: [{ namespace: DEMO_NAMESPACE, resources: demoResources }],
  })
  const queryClient = createQueryClient()
  const session = createTestSession(authenticatedSnapshot())
  attachSession(session)
  const services: SpeedAppServices = {
    session,
    api: createHarnessClient(),
    queryClient,
  }
  return render(
    <SpeedAppProviders i18n={i18n} queryClient={queryClient}>
      <AppServicesProvider services={services}>
        <RouteProvider table={[]} fallback={() => null}>
          <AccountEntry
            closeDrawer={options.closeDrawer}
            organizationItems={options.organizationItems}
          />
        </RouteProvider>
      </AppServicesProvider>
    </SpeedAppProviders>,
  )
}

/** The menu's unconditional labels, in render order. */
const menuLabels = [
  demoEnUS.userMenu.messages,
  demoEnUS.account.sections.profile,
  demoEnUS.account.sections.security,
  demoEnUS.account.sections.sessions,
  demoEnUS.userMenu.preferences,
  demoEnUS.userMenu.signOut,
]

describe('the account entry in the navigation column', () => {
  it('is the sidebar row, alongside the header avatar menu', async () => {
    renderDemoApp({ session: createTestSession(authenticatedSnapshot()) })

    const trigger = await screen.findByTestId('account-entry-trigger')
    const displayName = (
      authenticatedSnapshot().principal?.display_name ?? ''
    ).trim()
    expect(trigger).toHaveAccessibleName(new RegExp(`${displayName}$`))
    // The header carries its own account menu over the same entries; the
    // row at the bottom of the column is the sidebar's copy of it.
    expect(screen.getByTestId('user-menu-trigger')).toBeInTheDocument()
  })

  it('puts a divider above the row, inset 16px on the navigation icon track', async () => {
    renderDemoApp({ session: createTestSession(authenticatedSnapshot()) })

    const entry = await screen.findByTestId('account-entry')
    const trigger = screen.getByTestId('account-entry-trigger')
    // The line above the row belongs to the entry, not to the navigation.
    const divider = entry.firstElementChild
    expect(divider).toHaveClass('MuiDivider-root')
    // The row's leading inset is the navigation's own 16px, and the icon
    // box advances the label to the 52px the navigation labels start at.
    expect(trigger).toHaveStyle({ paddingLeft: '16px' })
    const iconBox = trigger.firstElementChild as HTMLElement
    expect(iconBox).toHaveStyle({ width: '36px' })
  })

  it('opens the unconditional menu entries, each with a glyph, upward', async () => {
    const user = userEvent.setup()
    renderDemoApp({ session: createTestSession(authenticatedSnapshot()) })
    const trigger = await screen.findByTestId('account-entry-trigger')

    trigger.focus()
    await user.keyboard('{Enter}')

    const items = screen.getAllByRole('menuitem')
    expect(items).toHaveLength(menuLabels.length)
    expect(items.map((item) => item.textContent)).toEqual(menuLabels)
    for (const item of items) {
      expect(item.querySelector('svg')).not.toBeNull()
      // The glyph sits centred in the navigation's own icon cell:
      // theme.spacing(4.5) -- 36px at the default 8px spacing base -- holding
      // the navigation's 24px mark. The paper is 9px in with a 1px border, so
      // the entry's own 6px leading inset starts that box on the column's
      // 16px track.
      const icon = item.firstElementChild as HTMLElement
      expect(getComputedStyle(icon).width).toBe('36px')
      expect(getComputedStyle(icon).justifyContent).toBe('center')
      expect(getComputedStyle(item).paddingLeft).toBe('6px')
      expect(item.querySelector('svg')).toHaveAttribute('width', '24')
    }
    // One divider between Messages and the panes, one before sign-out.
    expect(screen.getAllByRole('separator')).toHaveLength(2)

    // The paper sits 9px in from the column's edge and keeps the shared
    // minimum width, with a divider-coloured outline over the theme's deep
    // shadow.
    const paper = screen.getByRole('menu').parentElement as HTMLElement
    expect(paper).toHaveStyle({ minWidth: '240px', marginLeft: '9px' })
    expect(getComputedStyle(paper).borderWidth).toBe('1px')
    expect(getComputedStyle(paper).borderStyle).toBe('solid')
    expect(getComputedStyle(paper).boxShadow).not.toBe('none')

    // Escape closes the menu and returns focus to the trigger.
    await user.keyboard('{Escape}')
    await waitFor(() =>
      expect(screen.queryByRole('menu')).not.toBeInTheDocument(),
    )
    expect(trigger).toHaveFocus()
  })

  it('reads the menu copy from the Chinese bundle', async () => {
    const user = userEvent.setup()
    renderDemoApp({
      session: createTestSession(authenticatedSnapshot()),
      language: 'zh-CN',
    })
    await user.click(await screen.findByTestId('account-entry-trigger'))

    expect(
      screen.getByRole('menuitem', { name: demoZhCN.userMenu.messages }),
    ).toBeInTheDocument()
    expect(
      screen.getByRole('menuitem', { name: demoZhCN.userMenu.preferences }),
    ).toBeInTheDocument()
    // The changed copy is menu-specific: the home card and the account page's
    // preference section keep their existing wording.
    expect(demoZhCN.userMenu.messages).not.toBe(
      demoZhCN.home.cards.notifications.title,
    )
    expect(demoZhCN.userMenu.preferences).not.toBe(
      demoZhCN.account.sections.preferences,
    )
  })

  it('keeps the navigation column to the home entry alone', async () => {
    renderDemoApp({ session: createTestSession(authenticatedSnapshot()) })

    const nav = screen.getByRole('navigation', {
      name: layoutEn.appShell.navLabel,
    })
    expect(
      within(nav).getAllByRole('link').map((link) => link.textContent),
    ).toEqual([demoEnUS.nav.home])
    // The notification centre and the account settings are menu
    // destinations now, not navigation entries.
    expect(
      within(nav).queryByRole('link', { name: demoEnUS.nav.account }),
    ).not.toBeInTheDocument()
  })

  it('keeps only the avatar in the collapsed rail, still named by the account', async () => {
    const user = userEvent.setup()
    renderDemoApp({
      session: createTestSession(authenticatedSnapshot()),
      shellLayout: 'sidebar',
    })
    const displayName = (
      authenticatedSnapshot().principal?.display_name ?? ''
    ).trim()
    const wide = await screen.findByTestId('account-entry-trigger')
    expect(within(wide).getByText(displayName)).toBeInTheDocument()

    await user.click(
      screen.getByRole('button', { name: layoutEn.appShell.collapseNav }),
    )
    await waitFor(() =>
      expect(screen.queryByText(displayName)).not.toBeInTheDocument(),
    )
    const narrow = screen.getByTestId('account-entry-trigger')
    expect(narrow).toHaveAccessibleName(new RegExp(displayName))
    // The glyph is still visible text inside the control.
    expect((narrow.textContent ?? '').trim()).not.toBe('')
  })

  it('closes a temporary drawer when an organization entry is chosen', async () => {
    const user = userEvent.setup()
    const closeDrawer = vi.fn()
    renderAccountEntry({ closeDrawer, organizationItems: ORGANIZATION_ITEMS })

    await user.click(await screen.findByTestId('account-entry-trigger'))
    await user.click(
      await screen.findByRole('menuitem', { name: 'Invitations' }),
    )

    // The sidebar surface owns both dismissals: its menu and the temporary
    // drawer it may be mounted in.
    expect(closeDrawer).toHaveBeenCalledTimes(1)
    await waitFor(() =>
      expect(screen.queryByRole('menu')).not.toBeInTheDocument(),
    )
  })
})
