/**
 * ConsoleShell: the platform frame.
 *
 * It renders the three landmarks (a banner, a nav and a main) through the
 * shared @speed/layout-kit AppShell frame, derives the platform navigation
 * from the labelled declarations only, links each entry to its
 * declaration's path, and moves focus to main in place from the skip link.
 * It does no path matching and marks no entry selected.
 *
 * The two frame-shape guarantees the console shares with the tenant shell
 * are asserted here, each against the structural marker that produces it
 * (jsdom renders no real layout or scroll, so a computed-style assertion
 * could not see either): on the desktop the navigation lives inside the
 * permanent Drawer -- a fixed sidebar that does not scroll away with the
 * content, the bug the shared frame fixes over a stacked in-flow column --
 * and below md it lives inside a temporary (modal) Drawer behind a
 * hamburger toggle, never as a block stacked above the content.
 */

import { useCallback, useState } from 'react'
import type { ReactElement } from 'react'
import { describe, expect, it, vi } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { mockMatchMedia } from '@speed/test-utils/matchMedia'
import layoutKitZhCN from '../../layout-kit/src/locales/zh-CN.json' with { type: 'json' }
import consoleUiZhCN from './locales/zh-CN.json' with { type: 'json' }
import { ConsoleShell } from './ConsoleShell.js'
import type { ConsoleShellProps } from './ConsoleShell.js'
import { consoleNavGroups } from './nav-groups.js'
import { consoleRoutes } from './routes.js'
import type { ConsoleNavGroup, ConsoleRoute } from './route.js'
import { StepUpChallenge } from './internal/step-up-challenge.js'
import { renderWithProviders } from '../test-utils/render.js'
import { bindConsoleRequest } from '../test-utils/request-rig.js'
import { createStepUpSession } from '../test-utils/session.js'
import {
  createConsoleOperations,
  makeConsoleBackend,
} from '../test-utils/operations.js'
import { expectNoAxeViolationsForPage } from '../test-utils/axe.js'

/** The feature answer these shell-shape cases use: everything present. */
function hasFeature(): boolean {
  return true
}

/** ConsoleShell with the required feature answer filled in. */
function Shell(props: Omit<ConsoleShellProps, 'hasFeature'>): ReactElement {
  return <ConsoleShell {...props} hasFeature={hasFeature} />
}

const operations = createConsoleOperations(makeConsoleBackend())

const routes: readonly ConsoleRoute[] = [
  {
    path: '/tenants',
    view: () => null,
    label: 'Tenants',
    permission: 'tenant.read',
    icon: <svg aria-hidden="true" data-testid="icon-tenants" />,
  },
  {
    path: '/settings',
    view: () => null,
    label: 'Settings',
    icon: <svg aria-hidden="true" data-testid="icon-settings" />,
  },
  // No label: reachable but not listed in the navigation.
  { path: '/audit/:id', view: () => null },
]

// The controlled-collapse host: the collapse state lives here, ConsoleShell
// reports every flip through onSidebarCollapsedChange, and nothing else writes
// it -- the shape a real controlled consumer renders.
function ControlledCollapseConsole({
  onCollapseChange,
}: {
  readonly onCollapseChange: (collapsed: boolean) => void
}) {
  const [collapsed, setCollapsed] = useState(false)
  const reportCollapse = useCallback(
    (next: boolean) => {
      onCollapseChange(next)
      setCollapsed(next)
    },
    [onCollapseChange],
  )
  return (
    <Shell
      routes={routes}
      title="Console"
      sidebarCollapsed={collapsed}
      onSidebarCollapsedChange={reportCollapse}
    >
      <h1>Home</h1>
    </Shell>
  )
}

describe('ConsoleShell', () => {
  it('renders a banner, a nav and a main landmark', () => {
    renderWithProviders(
      <Shell routes={routes} title="Console">
        <h1>Home</h1>
      </Shell>,
      { operations },
    )
    expect(screen.getByRole('banner')).toBeInTheDocument()
    expect(screen.getByRole('navigation')).toBeInTheDocument()
    expect(screen.getByRole('main')).toBeInTheDocument()
  })

  it('fixes the banner to the top, matching the platform frame', () => {
    renderWithProviders(
      <Shell routes={routes} title="Console">
        <h1>Home</h1>
      </Shell>,
      { operations },
    )
    // The banner does not scroll away with the page, the same treatment the
    // tenant frame's top bar carries.
    expect(screen.getByRole('banner')).toHaveClass('MuiAppBar-positionFixed')
  })

  it('derives the navigation from the labelled declarations only', () => {
    renderWithProviders(
      <Shell routes={routes} title="Console">
        <h1>Home</h1>
      </Shell>,
      { operations },
    )
    const nav = screen.getByRole('navigation')
    const links = within(nav).getAllByRole('link')
    expect(links.map((link) => link.textContent)).toEqual(['Tenants', 'Settings'])
    expect(links.map((link) => link.getAttribute('href'))).toEqual([
      '/tenants',
      '/settings',
    ])
  })

  it('renders the host content inside main', () => {
    renderWithProviders(
      <Shell routes={routes} title="Console">
        <h1>Home</h1>
        <p>body</p>
      </Shell>,
      { operations },
    )
    const main = screen.getByRole('main')
    expect(within(main).getByText('body')).toBeInTheDocument()
  })

  it('carries the host account settings destination to the step-up dialog', async () => {
    // The step-up dialog's no-factor state links to the host's account
    // settings page; the shell is the one place the host supplies that
    // destination, so the dialog rendered inside it must receive it.
    bindConsoleRequest((call) => {
      if (call.method === 'GET' && call.path === '/auth/factors') {
        return { factors: [] }
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    })
    renderWithProviders(
      <Shell routes={routes} title="Console" accountHref="/host/security">
        <StepUpChallenge
          open
          session={createStepUpSession()}
          onSuccess={() => undefined}
          onCancel={() => undefined}
        />
      </Shell>,
      { operations },
    )
    expect(
      await screen.findByRole('link', { name: '前往账户设置' }),
    ).toHaveAttribute('href', '/host/security')
  })

  it('pads the content region so a page does not butt against the chrome', () => {
    renderWithProviders(
      <Shell routes={routes} title="Console">
        <h1>Home</h1>
      </Shell>,
      { operations },
    )
    // The page content sits inside the content region's padded container,
    // clear of the sidebar and the banner -- the same inset the tenant
    // shell's content region applies. Without it the heading's parent is
    // the bare main landmark and the page hugs the sidebar's right edge.
    const main = screen.getByRole('main')
    const heading = screen.getByRole('heading', { level: 1, name: 'Home' })
    expect(heading.parentElement).not.toBe(main)
    expect(heading.parentElement).toHaveStyle({
      paddingTop: '24px',
      paddingLeft: '24px',
    })
  })

  it('renders the title and the header actions in the banner', () => {
    renderWithProviders(
      <Shell
        routes={routes}
        title="Speed Console"
        headerActions={<span>account</span>}
      >
        <h1>Home</h1>
      </Shell>,
      { operations },
    )
    const banner = screen.getByRole('banner')
    expect(within(banner).getByText('Speed Console')).toBeInTheDocument()
    expect(within(banner).getByText('account')).toBeInTheDocument()
  })

  it('hands a navigation entry click to onNavigate instead of the page link', async () => {
    const user = userEvent.setup()
    const onNavigate = vi.fn()
    renderWithProviders(
      <Shell routes={routes} title="Console" onNavigate={onNavigate}>
        <h1>Home</h1>
      </Shell>,
      { operations },
    )
    const nav = screen.getByRole('navigation')
    await user.click(within(nav).getByRole('link', { name: 'Settings' }))
    // The entry stays a link, but its click is the host's to route.
    expect(onNavigate).toHaveBeenCalledWith('/settings')
  })

  it('moves focus to main from the skip link, without navigating', async () => {
    const user = userEvent.setup()
    renderWithProviders(
      <Shell routes={routes} title="Console">
        <h1>Home</h1>
      </Shell>,
      { operations },
    )
    const skip = screen.getByRole('link', {
      name: layoutKitZhCN.appShell.skipToContent,
    })
    await user.click(skip)
    expect(screen.getByRole('main')).toHaveFocus()
  })

  it('has no axe violations as a page', async () => {
    renderWithProviders(
      <Shell routes={routes} title="Console">
        <h1>Home</h1>
        <p>body</p>
      </Shell>,
      { operations },
    )
    await expectNoAxeViolationsForPage()
  })

  describe('desktop frame: a fixed sidebar', () => {
    it('renders the navigation inside the permanent Drawer, not as an in-flow column beside the content', () => {
      // The fixed sidebar is what keeps the nav from scrolling away with the
      // content on a long page (the audit and permissions pages run past a
      // viewport). jsdom renders no real scroll, so the assertion is on the
      // structural marker that produces the fixed behaviour: MUI's permanent
      // Drawer (the `MuiDrawer-docked` container, whose paper is fixed)
      // wraps the nav, and the nav is not a descendant of main.
      mockMatchMedia(true)
      renderWithProviders(
        <Shell routes={routes} title="Console">
          <h1>Home</h1>
        </Shell>,
        { operations },
      )
      const nav = screen.getByRole('navigation', {
        name: consoleUiZhCN.shell.navLabel,
      })
      expect(nav.closest('.MuiDrawer-docked')).not.toBeNull()
      // The permanent variant is the docked drawer, never the temporary one.
      expect(nav.closest('.MuiDrawer-modal')).toBeNull()
      // The sidebar is chrome beside the content, not part of the scrolling
      // content column itself.
      expect(screen.getByRole('main').contains(nav)).toBe(false)
    })

    it('shows no hamburger toggle on the desktop', () => {
      mockMatchMedia(true)
      renderWithProviders(
        <Shell routes={routes} title="Console">
          <h1>Home</h1>
        </Shell>,
        { operations },
      )
      expect(
        screen.queryByRole('button', { name: layoutKitZhCN.appShell.openNav }),
      ).not.toBeInTheDocument()
    })
  })

  describe('collapsible sidebar', () => {
    it('renders no collapse toggle when the host does not supply the collapse pair', () => {
      mockMatchMedia(true)
      renderWithProviders(
        <Shell routes={routes} title="Console">
          <h1>Home</h1>
        </Shell>,
        { operations },
      )
      // Without the host's collapse callback the frame renders no toggle and
      // keeps the always-open permanent drawer -- the same opt-in contract the
      // tenant frame carries.
      expect(
        screen.queryByRole('button', { name: layoutKitZhCN.appShell.collapseNav }),
      ).not.toBeInTheDocument()
      expect(
        screen.queryByRole('button', { name: layoutKitZhCN.appShell.expandNav }),
      ).not.toBeInTheDocument()
      expect(
        screen.getByRole('navigation', { name: consoleUiZhCN.shell.navLabel }),
      ).toBeInTheDocument()
    })

    it('renders the collapse toggle and reports the flip when the host supplies the pair', async () => {
      mockMatchMedia(true)
      const user = userEvent.setup()
      const onCollapseChange = vi.fn()
      renderWithProviders(
        <ControlledCollapseConsole onCollapseChange={onCollapseChange} />,
        { operations },
      )
      // The console passes the collapse pair straight through to the frame, so
      // the desktop toggle is present and starts offering to collapse.
      const toggle = screen.getByRole('button', {
        name: layoutKitZhCN.appShell.collapseNav,
      })
      expect(toggle).toHaveAttribute('aria-expanded', 'true')
      await user.click(toggle)
      // The flip is the host's to hold: the shell reports it and the
      // permanent drawer narrows to the icon-only rail -- it stays mounted,
      // so the nav landmark remains -- leaving the toggle offering to
      // expand.
      expect(onCollapseChange).toHaveBeenCalledWith(true)
      expect(
        screen.getByRole('navigation', { name: consoleUiZhCN.shell.navLabel }),
      ).toBeInTheDocument()
      expect(
        screen.getByRole('button', { name: layoutKitZhCN.appShell.expandNav }),
      ).toHaveAttribute('aria-expanded', 'false')
    })
  })

  describe('navigation groups', () => {
    // Labelled declarations that name groups, plus one ungrouped entry. The
    // group table orders the groups differently from the declaration order, so
    // the grouped output cannot accidentally match the flat one. Every group
    // holds two entries: a group with one visible entry renders ungrouped, so
    // these cases are about the grouped form itself (see the single-entry
    // cases below).
    const groupedRoutes: readonly ConsoleRoute[] = [
      { path: '/', view: () => null, label: 'Overview', group: 'overview' },
      { path: '/activity', view: () => null, label: 'Activity', group: 'overview' },
      { path: '/accounts', view: () => null, label: 'Accounts', group: 'identity' },
      { path: '/roles', view: () => null, label: 'Roles', group: 'identity' },
      { path: '/settings', view: () => null, label: 'Settings', group: 'platform' },
      { path: '/limits', view: () => null, label: 'Limits', group: 'platform' },
      { path: '/help', view: () => null, label: 'Help' },
    ]
    const navGroups: readonly ConsoleNavGroup[] = [
      { id: 'identity', label: 'Identity & access' },
      { id: 'platform', label: 'Platform' },
      { id: 'overview', label: 'Overview' },
    ]

    it('gathers the labelled declarations into their groups: groups in table order, entries in declaration order, ungrouped above', () => {
      mockMatchMedia(true)
      renderWithProviders(
        <Shell
          routes={groupedRoutes}
          navGroups={navGroups}
          title="Console"
        >
          <h1>Home</h1>
        </Shell>,
        { operations },
      )
      const nav = screen.getByRole('navigation')
      // Group headings are the disclosure controls, in the table's order.
      expect(
        within(nav)
          .getAllByRole('button')
          .map((button) => button.textContent),
      ).toEqual(['Identity & access', 'Platform', 'Overview'])
      // The ungrouped entry sits above the groups; each group's entries keep
      // their declaration order.
      expect(
        within(nav)
          .getAllByRole('link')
          .map((link) => link.textContent),
      ).toEqual([
        'Help',
        'Accounts',
        'Roles',
        'Settings',
        'Limits',
        'Overview',
        'Activity',
      ])
    })

    it('drops a group that no declaration names', () => {
      mockMatchMedia(true)
      renderWithProviders(
        <Shell
          routes={groupedRoutes}
          navGroups={[...navGroups, { id: 'empty', label: 'Empty group' }]}
          title="Console"
        >
          <h1>Home</h1>
        </Shell>,
        { operations },
      )
      const nav = screen.getByRole('navigation')
      expect(
        within(nav).queryByRole('button', { name: 'Empty group' }),
      ).not.toBeInTheDocument()
    })

    it('keeps the flat single column when no group table is supplied', () => {
      mockMatchMedia(true)
      renderWithProviders(
        <Shell routes={groupedRoutes} title="Console">
          <h1>Home</h1>
        </Shell>,
        { operations },
      )
      const nav = screen.getByRole('navigation')
      // Backward compatibility: the labelled declarations in declaration
      // order, no group headings, no disclosure controls.
      expect(within(nav).queryAllByRole('button')).toHaveLength(0)
      expect(
        within(nav)
          .getAllByRole('link')
          .map((link) => link.textContent),
      ).toEqual([
        'Overview',
        'Activity',
        'Accounts',
        'Roles',
        'Settings',
        'Limits',
        'Help',
      ])
    })

    it('has no axe violations with the grouped navigation', async () => {
      mockMatchMedia(true)
      renderWithProviders(
        <Shell routes={groupedRoutes} navGroups={navGroups} title="Console">
          <h1>Home</h1>
          <p>body</p>
        </Shell>,
        { operations },
      )
      await expectNoAxeViolationsForPage()
    })
  })

  describe('single-entry groups', () => {
    // The demo's baseline serves every feature the console's pages require
    // except sms.admin: the binary imports that module for the API contract,
    // but no command names it for a run. Narrowing the declarations by that
    // answer is what the demo's host hands the shell.
    const demoRoutes = consoleRoutes.filter((route) =>
      (route.requires ?? []).every((name) => name !== 'sms.admin'),
    )

    it('renders a group holding one visible item as that item alone, at the group position', () => {
      mockMatchMedia(true)
      renderWithProviders(
        <Shell routes={demoRoutes} navGroups={consoleNavGroups} title="Console">
          <h1>Home</h1>
        </Shell>,
        { operations },
      )
      const nav = screen.getByRole('navigation')
      // Identity & access is the one group with more than one visible item;
      // overview, tenants, platform and audit hold a lone entry each and
      // render no heading over it.
      expect(
        within(nav)
          .getAllByRole('button')
          .map((button) => button.textContent),
      ).toEqual(['身份与访问'])
      // The lone entries stand where their groups would have, in the group
      // table's order, with identity & access's six items between them.
      expect(
        within(nav)
          .getAllByRole('link')
          .map((link) => link.textContent),
      ).toEqual([
        '概览',
        '账户',
        '会话',
        '冒充',
        '权限',
        '邀请与成员',
        'SSO 提供方',
        '租户',
        '设置',
        '审计记录',
      ])
    })

    it('renders the heading again, with both items inside, when a second item is visible', () => {
      mockMatchMedia(true)
      // Every feature present: sms.admin is in force, so platform config
      // holds settings and the SMS templates.
      renderWithProviders(
        <Shell
          routes={consoleRoutes}
          navGroups={consoleNavGroups}
          title="Console"
        >
          <h1>Home</h1>
        </Shell>,
        { operations },
      )
      const nav = screen.getByRole('navigation')
      // The same code now renders the platform heading; identity & access
      // keeps its own.
      expect(
        within(nav)
          .getAllByRole('button')
          .map((button) => button.textContent),
      ).toEqual(['身份与访问', '平台配置'])
      expect(
        within(nav)
          .getAllByRole('link')
          .map((link) => link.textContent),
      ).toEqual([
        '概览',
        '账户',
        '会话',
        '冒充',
        '权限',
        '邀请与成员',
        'SSO 提供方',
        '租户',
        '设置',
        '短信模板',
        '审计记录',
      ])
      // Both platform pages sit inside the heading's controlled content,
      // not beside it as lone top-level items.
      const heading = within(nav).getByRole('button', { name: '平台配置' })
      const contentId = heading.getAttribute('aria-controls')
      expect(contentId).not.toBeNull()
      const content = document.getElementById(contentId as string)
      expect(content).not.toBeNull()
      expect(
        within(content as HTMLElement)
          .getAllByRole('link')
          .map((link) => link.textContent),
      ).toEqual(['设置', '短信模板'])
    })
  })

  describe('navigation icons', () => {
    /** The ListItemText root holding an entry's label. */
    const labelRootOf = (element: HTMLElement): Element | null =>
      element.closest('.MuiListItemText-root')

    it('renders a declaration icon beside the visible label while the sidebar is expanded', () => {
      mockMatchMedia(true)
      renderWithProviders(
        <Shell routes={routes} title="Console">
          <h1>Home</h1>
        </Shell>,
        { operations },
      )
      const tenants = screen.getByRole('link', { name: 'Tenants' })
      // Icon and text coexist, and the icon is decorative: the accessible
      // name is still exactly the label.
      expect(within(tenants).getByTestId('icon-tenants')).toBeInTheDocument()
      expect(within(tenants).getByText('Tenants')).toBeInTheDocument()
      expect(labelRootOf(screen.getByText('Tenants'))).not.toHaveStyle({
        position: 'absolute',
      })
    })

    it('collapses to an icon rail: every entry keeps its icon and its label is clipped', async () => {
      mockMatchMedia(true)
      const user = userEvent.setup()
      renderWithProviders(
        <ControlledCollapseConsole onCollapseChange={() => undefined} />,
        { operations },
      )
      await user.click(
        screen.getByRole('button', { name: layoutKitZhCN.appShell.collapseNav }),
      )
      const nav = screen.getByRole('navigation', {
        name: consoleUiZhCN.shell.navLabel,
      })
      const links = within(nav).getAllByRole('link')
      expect(links.map((link) => link.getAttribute('href'))).toEqual([
        '/tenants',
        '/settings',
      ])
      for (const link of links) {
        // The rail is icon-led: each entry renders its icon ...
        expect(link.querySelector('svg')).not.toBeNull()
        // ... and the label taking the clipped accessible-only hiding
        // recipe, rather than the icon-less fallback that keeps the text
        // visible.
        expect(link.querySelector('.MuiListItemText-root')).toHaveStyle({
          position: 'absolute',
        })
      }
      // The clipped label still names each entry.
      expect(
        within(nav).getByRole('link', { name: 'Tenants' }),
      ).toBeInTheDocument()
      expect(
        within(nav).getByRole('link', { name: 'Settings' }),
      ).toBeInTheDocument()
    })

    it('collapses the group heading to its group icon', () => {
      mockMatchMedia(true)
      // Two entries: a group holding one renders as an ungrouped item, with
      // no heading left to collapse.
      const groupedRoutes: readonly ConsoleRoute[] = [
        {
          path: '/accounts',
          view: () => null,
          label: 'Accounts',
          group: 'identity',
          icon: <svg aria-hidden="true" data-testid="entry-icon" />,
        },
        {
          path: '/roles',
          view: () => null,
          label: 'Roles',
          group: 'identity',
        },
      ]
      const groupedNav: readonly ConsoleNavGroup[] = [
        {
          id: 'identity',
          label: 'Identity & access',
          icon: <svg aria-hidden="true" data-testid="group-icon" />,
        },
      ]
      renderWithProviders(
        <Shell
          routes={groupedRoutes}
          navGroups={groupedNav}
          sidebarCollapsed
          onSidebarCollapsedChange={() => undefined}
          title="Console"
        >
          <h1>Home</h1>
        </Shell>,
        { operations },
      )
      const groupButton = screen.getByRole('button', {
        name: 'Identity & access',
      })
      // The group icon replaces the clipped heading in the rail; the label
      // stays as the button's accessible name.
      expect(within(groupButton).getByTestId('group-icon')).toBeInTheDocument()
      expect(
        labelRootOf(within(groupButton).getByText('Identity & access')),
      ).toHaveStyle({ position: 'absolute' })
    })
  })

  describe('narrow-viewport frame: a hamburger drawer, not a stacked block', () => {
    it('renders a temporary (modal) drawer behind a hamburger toggle instead of stacking the nav above the content', () => {
      // Below md the whole navigation must not be laid out above the
      // content (which forces a swipe past a full screen of nav before
      // reaching the page). It lives inside MUI's temporary Drawer -- the
      // overlay variant -- and is reachable through the hamburger toggle.
      mockMatchMedia(false)
      renderWithProviders(
        <Shell routes={routes} title="Console">
          <h1>Home</h1>
        </Shell>,
        { operations },
      )
      // The toggle is the hamburger; it starts closed.
      const toggle = screen.getByRole('button', {
        name: layoutKitZhCN.appShell.openNav,
      })
      expect(toggle).toHaveAttribute('aria-expanded', 'false')

      // The nav is mounted inside the temporary (modal) drawer, not in the
      // content flow. A closed MUI modal keeps its subtree aria-hidden, so
      // look past that to find the drawer's own nav landmark.
      const nav = screen.getByRole('navigation', { hidden: true })
      expect(nav.closest('.MuiDrawer-modal')).not.toBeNull()
      expect(nav.closest('.MuiDrawer-docked')).toBeNull()
      // The nav is never stacked into the content column.
      expect(screen.getByRole('main').contains(nav)).toBe(false)
    })

    it('opens the drawer from the hamburger and closes it again', async () => {
      mockMatchMedia(false)
      const user = userEvent.setup()
      renderWithProviders(
        <Shell routes={routes} title="Console">
          <h1>Home</h1>
        </Shell>,
        { operations },
      )
      await user.click(
        screen.getByRole('button', { name: layoutKitZhCN.appShell.openNav }),
      )
      // Open: the drawer is a real modal and its nav is now in the
      // accessibility tree with the platform navigation.
      const nav = screen.getByRole('navigation', {
        name: consoleUiZhCN.shell.navLabel,
      })
      expect(nav).toBeInTheDocument()
      expect(
        within(nav)
          .getAllByRole('link')
          .map((link) => link.textContent),
      ).toEqual(['Tenants', 'Settings'])

      // Close through the drawer's own toggle; the closed state is the
      // established aria-expanded marker (the background's aria-hidden
      // lifts only once the drawer's exit transition finishes).
      await user.click(
        screen.getByRole('button', {
          name: layoutKitZhCN.appShell.closeNav,
          hidden: true,
        }),
      )
      await waitFor(() => {
        expect(
          screen.getByRole('button', { name: layoutKitZhCN.appShell.openNav }),
        ).toHaveAttribute('aria-expanded', 'false')
      })
    })

    it('has no axe violations on the narrow-viewport layout', async () => {
      mockMatchMedia(false)
      renderWithProviders(
        <Shell routes={routes} title="Console">
          <h1>Home</h1>
          <p>body</p>
        </Shell>,
        { operations },
      )
      await expectNoAxeViolationsForPage()
    })
  })
})
