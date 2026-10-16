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
import { describe, expect, it, vi } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { mockMatchMedia } from '@speed/test-utils/matchMedia'
import layoutKitZhCN from '../../layout-kit/src/locales/zh-CN.json' with { type: 'json' }
import consoleUiZhCN from './locales/zh-CN.json' with { type: 'json' }
import { ConsoleShell } from './ConsoleShell.js'
import type { ConsoleRoute } from './route.js'
import { renderWithProviders } from '../test-utils/render.js'
import {
  createConsoleOperations,
  makeConsoleBackend,
} from '../test-utils/operations.js'
import { expectNoAxeViolationsForPage } from '../test-utils/axe.js'

const operations = createConsoleOperations(makeConsoleBackend())

const routes: readonly ConsoleRoute[] = [
  { path: '/tenants', view: () => null, label: 'Tenants', permission: 'tenant.read' },
  { path: '/settings', view: () => null, label: 'Settings' },
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
    <ConsoleShell
      routes={routes}
      title="Console"
      sidebarCollapsed={collapsed}
      onSidebarCollapsedChange={reportCollapse}
    >
      <h1>Home</h1>
    </ConsoleShell>
  )
}

describe('ConsoleShell', () => {
  it('renders a banner, a nav and a main landmark', () => {
    renderWithProviders(
      <ConsoleShell routes={routes} title="Console">
        <h1>Home</h1>
      </ConsoleShell>,
      { operations },
    )
    expect(screen.getByRole('banner')).toBeInTheDocument()
    expect(screen.getByRole('navigation')).toBeInTheDocument()
    expect(screen.getByRole('main')).toBeInTheDocument()
  })

  it('fixes the banner to the top, matching the platform frame', () => {
    renderWithProviders(
      <ConsoleShell routes={routes} title="Console">
        <h1>Home</h1>
      </ConsoleShell>,
      { operations },
    )
    // The banner does not scroll away with the page, the same treatment the
    // tenant frame's top bar carries.
    expect(screen.getByRole('banner')).toHaveClass('MuiAppBar-positionFixed')
  })

  it('derives the navigation from the labelled declarations only', () => {
    renderWithProviders(
      <ConsoleShell routes={routes} title="Console">
        <h1>Home</h1>
      </ConsoleShell>,
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
      <ConsoleShell routes={routes} title="Console">
        <h1>Home</h1>
        <p>body</p>
      </ConsoleShell>,
      { operations },
    )
    const main = screen.getByRole('main')
    expect(within(main).getByText('body')).toBeInTheDocument()
  })

  it('pads the content region so a page does not butt against the chrome', () => {
    renderWithProviders(
      <ConsoleShell routes={routes} title="Console">
        <h1>Home</h1>
      </ConsoleShell>,
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
      <ConsoleShell
        routes={routes}
        title="Speed Console"
        headerActions={<span>account</span>}
      >
        <h1>Home</h1>
      </ConsoleShell>,
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
      <ConsoleShell routes={routes} title="Console" onNavigate={onNavigate}>
        <h1>Home</h1>
      </ConsoleShell>,
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
      <ConsoleShell routes={routes} title="Console">
        <h1>Home</h1>
      </ConsoleShell>,
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
      <ConsoleShell routes={routes} title="Console">
        <h1>Home</h1>
        <p>body</p>
      </ConsoleShell>,
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
        <ConsoleShell routes={routes} title="Console">
          <h1>Home</h1>
        </ConsoleShell>,
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
        <ConsoleShell routes={routes} title="Console">
          <h1>Home</h1>
        </ConsoleShell>,
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
        <ConsoleShell routes={routes} title="Console">
          <h1>Home</h1>
        </ConsoleShell>,
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
      // The flip is the host's to hold: the shell reports it and the permanent
      // drawer disappears, leaving the toggle offering to expand.
      expect(onCollapseChange).toHaveBeenCalledWith(true)
      expect(
        screen.queryByRole('navigation', { name: consoleUiZhCN.shell.navLabel }),
      ).not.toBeInTheDocument()
      expect(
        screen.getByRole('button', { name: layoutKitZhCN.appShell.expandNav }),
      ).toHaveAttribute('aria-expanded', 'false')
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
        <ConsoleShell routes={routes} title="Console">
          <h1>Home</h1>
        </ConsoleShell>,
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
        <ConsoleShell routes={routes} title="Console">
          <h1>Home</h1>
        </ConsoleShell>,
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
        <ConsoleShell routes={routes} title="Console">
          <h1>Home</h1>
          <p>body</p>
        </ConsoleShell>,
        { operations },
      )
      await expectNoAxeViolationsForPage()
    })
  })
})
