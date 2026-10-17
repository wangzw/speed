/**
 * AppShell contract: a fully controlled, props-driven app-chrome shell.
 *
 * Covers: nav item wiring (selected/href/onClick, host-computed only --
 * AppShell never path-matches), the desktop/mobile drawer split driven
 * by useMediaQuery (permanent at md+, temporary below, uncontrolled by
 * default and promotable to controlled via mobileOpen/onMobileOpenChange),
 * that header/headerActions/userMenu/children render only host content,
 * the header/nav/main landmarks in both languages via the shipped
 * bundles, a zero-violation axe scan with `region` left enabled
 * (AppShell is page-level chrome, unlike ui-kit's per-widget components),
 * and the narrow-viewport CSS protections (the capped mobile
 * drawer width, the nav content filling its paper rather than
 * re-declaring the width itself, the wrapping AppBar row) -- see the
 * "responsive protections" describe block for what each assertion does
 * and does not prove. The a11y- and state-hardening guarantees live
 * alongside: the skip link activating without touching the host hash
 * (a hash-routed host would lose the route to fragment navigation),
 * the uncontrolled temporary drawer closing itself on nav-item
 * activation with the scrim cleared and focus released (controlled
 * contract untouched), the open state resetting when the drawer leaves
 * the temporary variant for the permanent one -- the uncontrolled state
 * cleared, a controlled host told through onMobileOpenChange on the
 * same crossing -- and the header-spacer placeholders deriving from
 * the measured header height ("measured header spacer" describe block
 * states what jsdom can and cannot prove about the wrap case).
 */

import { useCallback, useState } from 'react'
import { act, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { switchLanguage } from '@speed/i18n'
import { createAppTheme } from '@speed/ui-kit'
import enUS from '../locales/en-US.json' with { type: 'json' }
import zhCN from '../locales/zh-CN.json' with { type: 'json' }
import { expectNoAxeViolations } from '../../test-utils/axe.js'
import {
  emittedStyleText,
  hasCoarsePointerTouchTarget,
} from '@speed/test-utils/emitted-css'
import { mockMatchMedia } from '@speed/test-utils/matchMedia'
import { renderWithProviders } from '../../test-utils/render.js'
import { stubResizeObserver } from '../../test-utils/resize-observer.js'
import { AppShell, type AppShellNavEntry, type AppShellNavItem } from './AppShell.js'

// Some harnesses below put an app route into location.hash the way a
// hash-routed host would; nothing else in this file reads the hash, but
// restore it after every test so no test inherits another's route.
afterEach(() => {
  window.location.hash = ''
})

const NAV_ITEMS: readonly AppShellNavItem[] = [
  { id: 'home', label: 'Home', href: '/home', selected: true },
  { id: 'reports', label: 'Reports', href: '/reports' },
]

// The controlled drawer's host, the shape a real controlled consumer
// renders: the open state lives in the host component, AppShell reports
// every change through onMobileOpenChange, and nothing else writes it.
// The widen-and-narrow regression below must be driven by that callback
// alone -- a host that closed itself at the crossing would test nothing.
function ControlledDrawerHost({
  initialOpen,
  onMobileOpenChange,
}: {
  readonly initialOpen: boolean
  readonly onMobileOpenChange: (open: boolean) => void
}) {
  const [open, setOpen] = useState(initialOpen)
  const reportOpenChange = useCallback(
    (next: boolean) => {
      onMobileOpenChange(next)
      setOpen(next)
    },
    [onMobileOpenChange],
  )
  return (
    <AppShell
      navItems={NAV_ITEMS}
      mobileOpen={open}
      onMobileOpenChange={reportOpenChange}
    >
      content
    </AppShell>
  )
}

// The controlled-collapse host: the collapse state lives here, AppShell
// reports every flip through onSidebarCollapsedChange, and nothing else
// writes it -- the shape a real controlled consumer renders.
function ControlledCollapseHost({
  initialCollapsed,
  onCollapseChange,
  navItems = NAV_ITEMS,
}: {
  readonly initialCollapsed: boolean
  readonly onCollapseChange: (collapsed: boolean) => void
  readonly navItems?: readonly AppShellNavEntry[]
}) {
  const [collapsed, setCollapsed] = useState(initialCollapsed)
  const reportCollapse = useCallback(
    (next: boolean) => {
      onCollapseChange(next)
      setCollapsed(next)
    },
    [onCollapseChange],
  )
  return (
    <AppShell
      navItems={navItems}
      sidebarCollapsed={collapsed}
      onSidebarCollapsedChange={reportCollapse}
    >
      content
    </AppShell>
  )
}

describe('AppShell', () => {
  describe('nav items', () => {
    it('renders each item label and wires href/selected from the host, never computing them itself', () => {
      const { getByRole } = renderWithProviders(
        <AppShell navItems={NAV_ITEMS}>content</AppShell>,
      )
      const homeLink = getByRole('link', { name: 'Home' })
      expect(homeLink).toHaveAttribute('href', '/home')
      expect(homeLink).toHaveAttribute('aria-current', 'page')

      const reportsLink = getByRole('link', { name: 'Reports' })
      expect(reportsLink).toHaveAttribute('href', '/reports')
      expect(reportsLink).not.toHaveAttribute('aria-current')
    })

    it('renders an item without href as a button and fires onClick', async () => {
      const onClick = vi.fn()
      const user = userEvent.setup()
      const { getByRole } = renderWithProviders(
        <AppShell navItems={[{ id: 'logout', label: 'Log out', onClick }]}>content</AppShell>,
      )
      const button = getByRole('button', { name: 'Log out' })
      await user.click(button)
      expect(onClick).toHaveBeenCalledTimes(1)
    })
  })

  describe('responsive drawer', () => {
    it('renders a permanent drawer with no toggle button at md and up', () => {
      mockMatchMedia(true)
      const { getByRole, queryByRole } = renderWithProviders(
        <AppShell navItems={NAV_ITEMS}>content</AppShell>,
      )
      expect(getByRole('navigation', { name: zhCN.appShell.navLabel })).toBeInTheDocument()
      expect(
        queryByRole('button', { name: zhCN.appShell.openNav }),
      ).not.toBeInTheDocument()
    })

    it('renders a closed temporary drawer below md and opens/closes it, uncontrolled by default', async () => {
      mockMatchMedia(false)
      const user = userEvent.setup()
      const { getByRole } = renderWithProviders(
        <AppShell navItems={NAV_ITEMS}>content</AppShell>,
      )
      const toggle = getByRole('button', { name: zhCN.appShell.openNav })
      expect(toggle).toHaveAttribute('aria-expanded', 'false')

      await user.click(toggle)
      // Once open, the temporary drawer is a real modal: MUI aria-hides
      // the rest of the page (this same header button included) from
      // assistive tech while it traps focus inside the drawer, exactly
      // as a modal should. `hidden: true` looks past that to assert the
      // toggle state a mouse click still reaches -- screen-reader users
      // close through Escape or the backdrop instead, both of which stay
      // inside the modal's own accessible subtree.
      const closeToggle = getByRole('button', {
        name: zhCN.appShell.closeNav,
        hidden: true,
      })
      expect(closeToggle).toHaveAttribute('aria-expanded', 'true')

      await user.click(closeToggle)
      // The background's aria-hidden marker lifts only once the drawer's
      // exit transition finishes (react-transition-group's onExited),
      // which runs on a real timer even under jsdom -- waitFor polls
      // past that instead of asserting on an animation still in flight.
      await waitFor(() => {
        expect(getByRole('button', { name: zhCN.appShell.openNav })).toHaveAttribute(
          'aria-expanded',
          'false',
        )
      })
    })

    it('defers the open state to the host when mobileOpen/onMobileOpenChange are supplied', async () => {
      mockMatchMedia(false)
      const onMobileOpenChange = vi.fn()
      const user = userEvent.setup()
      const { getByRole } = renderWithProviders(
        <AppShell
          navItems={NAV_ITEMS}
          mobileOpen={false}
          onMobileOpenChange={onMobileOpenChange}
        >
          content
        </AppShell>,
      )
      const toggle = getByRole('button', { name: zhCN.appShell.openNav })
      await user.click(toggle)

      expect(onMobileOpenChange).toHaveBeenCalledTimes(1)
      expect(onMobileOpenChange).toHaveBeenCalledWith(true)
      // Controlled: the prop did not change, so the shell stays closed
      // until the host re-renders it with mobileOpen={true}.
      expect(getByRole('button', { name: zhCN.appShell.openNav })).toHaveAttribute(
        'aria-expanded',
        'false',
      )
    })

    it('closes the uncontrolled temporary drawer itself when a nav item is activated, scrim cleared and focus released', async () => {
      // Activating a nav item in the mobile drawer must close it: the
      // temporary variant owns its open state in uncontrolled mode, so
      // it owns the close-on-navigation transition too (the scrim
      // clearing and the focus returning to the document are part of
      // that close, not separate mechanisms).
      mockMatchMedia(false)
      const user = userEvent.setup()
      const { getByRole } = renderWithProviders(
        <AppShell
          navItems={[
            { id: 'home', label: 'Home', href: '#/home' },
            { id: 'notes', label: 'Notes', href: '#/notes' },
          ]}
        >
          content
        </AppShell>,
      )
      await user.click(getByRole('button', { name: zhCN.appShell.openNav }))
      const homeLink = getByRole('link', { name: 'Home' })
      await user.click(homeLink)

      // The drawer closes by itself (the toggle is back to openNav and
      // aria-expanded false -- the suite's established close marker) ...
      await waitFor(() => {
        expect(getByRole('button', { name: zhCN.appShell.openNav })).toHaveAttribute(
          'aria-expanded',
          'false',
        )
      })
      // ... and the scrim is cleared: MUI keeps the closed modal's
      // backdrop mounted (keepMounted) but invisible, so "cleared" is a
      // visibility assertion, not an absence one.
      const backdrop = document.querySelector('.MuiBackdrop-root')
      expect(backdrop).not.toBeNull()
      expect(backdrop).not.toBeVisible()
      // ... the host navigation still ran (jsdom performs the anchor's
      // fragment navigation; AppShell never blocks a nav item) ...
      expect(window.location.hash).toBe('#/home')
      // ... and focus is back in the document instead of stranded
      // inside the closed drawer's aria-hidden region.
      const paper = document.querySelector('.MuiDrawer-paper')
      expect(paper).not.toBeNull()
      expect(paper?.contains(document.activeElement)).toBe(false)
    })

    it('keeps the controlled contract on nav activation: reports nothing and stays open until the host closes', async () => {
      // The close-on-navigation transition is AppShell's own only while
      // AppShell owns the open state. A controlled host keeps the
      // existing contract untouched: no callback fires from a nav-item
      // click and the drawer stays open until the host's own next
      // render closes it.
      mockMatchMedia(false)
      const onMobileOpenChange = vi.fn()
      const user = userEvent.setup()
      const { getByRole } = renderWithProviders(
        <AppShell
          navItems={[{ id: 'home', label: 'Home', href: '#/home' }]}
          mobileOpen
          onMobileOpenChange={onMobileOpenChange}
        >
          content
        </AppShell>,
      )
      await user.click(getByRole('link', { name: 'Home' }))

      expect(onMobileOpenChange).not.toHaveBeenCalled()
      expect(
        getByRole('button', { name: zhCN.appShell.closeNav, hidden: true }),
      ).toHaveAttribute('aria-expanded', 'true')
    })

    it('does not bring a portrait-open drawer back open after a widen-and-narrow round trip', async () => {
      // The uncontrolled open state must not survive a breakpoint
      // crossing: a drawer opened in portrait stays true while the
      // layout grows into the permanent (always-open) variant and would
      // re-pop open when the viewport narrows back. The open state
      // belongs to the temporary variant alone; leaving temporary
      // resets it.
      const media = mockMatchMedia(false)
      const user = userEvent.setup()
      const { getByRole, queryByRole } = renderWithProviders(
        <AppShell navItems={NAV_ITEMS}>content</AppShell>,
      )

      // Portrait: open the temporary drawer.
      await user.click(getByRole('button', { name: zhCN.appShell.openNav }))
      expect(
        getByRole('button', { name: zhCN.appShell.closeNav, hidden: true }),
      ).toHaveAttribute('aria-expanded', 'true')

      // Widen past md: the toggle leaves and the permanent drawer takes
      // over. (queryByRole, not getByRole: an absence assertion must not
      // throw while the element is still disappearing -- waitFor would
      // retry the thrown query until timeout.)
      act(() => {
        media.changeMatches(true)
      })
      await waitFor(() => {
        expect(
          queryByRole('button', { name: zhCN.appShell.openNav, hidden: true }),
        ).not.toBeInTheDocument()
      })
      expect(getByRole('navigation', { name: zhCN.appShell.navLabel })).toBeInTheDocument()

      // Narrow back below md: the temporary drawer must come back
      // closed -- the portrait-open state must not ride through the
      // permanent spell and re-pop it open.
      act(() => {
        media.changeMatches(false)
      })
      await waitFor(() => {
        expect(getByRole('button', { name: zhCN.appShell.openNav })).toHaveAttribute(
          'aria-expanded',
          'false',
        )
      })
    })

    it('tells a controlled host that a portrait-open drawer must close when the viewport widens, so narrowing back finds it closed', async () => {
      // Regression: the widen-and-narrow reset above only cleared the
      // uncontrolled state. The controlled half of the same
      // configuration was never told that the variant had left
      // temporary -- the reset effect was gated off by !isControlled and
      // no other path fires on a breakpoint crossing -- so a host
      // holding mobileOpen=true rode through the permanent spell
      // untouched and its drawer re-popped open (scrim and all) when the
      // viewport narrowed back. The breakpoint is AppShell's own
      // useMediaQuery knowledge, so onMobileOpenChange is the only
      // channel the host can reset through; the host below stores the
      // prop and is driven by that callback alone.
      const media = mockMatchMedia(false)
      const onMobileOpenChange = vi.fn()
      const { getByRole } = renderWithProviders(
        <ControlledDrawerHost initialOpen onMobileOpenChange={onMobileOpenChange} />,
      )

      // Portrait: the host's drawer starts open.
      expect(
        getByRole('button', { name: zhCN.appShell.closeNav, hidden: true }),
      ).toHaveAttribute('aria-expanded', 'true')

      // Widen past md: leaving temporary means open should become false,
      // reported exactly once -- the host applies it, and no re-fire
      // follows on the renders the reset itself causes.
      act(() => {
        media.changeMatches(true)
      })
      expect(onMobileOpenChange).toHaveBeenCalledTimes(1)
      expect(onMobileOpenChange).toHaveBeenCalledWith(false)

      // Narrow back below md: the temporary drawer comes back closed,
      // because the host's state followed the notification -- without
      // the crossing notification the host's state would leave it
      // open.
      act(() => {
        media.changeMatches(false)
      })
      await waitFor(() => {
        expect(getByRole('button', { name: zhCN.appShell.openNav })).toHaveAttribute(
          'aria-expanded',
          'false',
        )
      })
    })

    it('does not hand a controlled host a meaningless close when the viewport widens past a drawer that is already closed', () => {
      // The reset notification is gated on the drawer actually being
      // open: a host already at false crossing into the permanent
      // variant has nothing to learn from the crossing, so no callback
      // fires -- the gate keeps the crossing silent for the closed case
      // while still reporting it for the open one.
      const media = mockMatchMedia(false)
      const onMobileOpenChange = vi.fn()
      renderWithProviders(
        <ControlledDrawerHost initialOpen={false} onMobileOpenChange={onMobileOpenChange} />,
      )
      act(() => {
        media.changeMatches(true)
      })
      act(() => {
        media.changeMatches(false)
      })
      expect(onMobileOpenChange).not.toHaveBeenCalled()
    })
  })

  describe('collapsible sidebar', () => {
    // Entries that carry an icon: the design's icon-only rail is built
    // from these, so the rail tests use icons to exercise the real shape
    // (the collapsed label is clipped, the icon speaks).
    const NAV_WITH_ICONS: readonly AppShellNavItem[] = [
      {
        id: 'home',
        label: 'Home',
        href: '/home',
        selected: true,
        icon: <span data-testid="icon-home">H</span>,
      },
      {
        id: 'reports',
        label: 'Reports',
        href: '/reports',
        icon: <span data-testid="icon-reports">R</span>,
      },
    ]

    /** The ListItemText root holding an entry's label. */
    const labelRootOf = (element: HTMLElement): Element | null =>
      element.closest('.MuiListItemText-root')

    it('renders no collapse toggle and keeps the permanent icon+text drawer when the host does not opt in', () => {
      mockMatchMedia(true)
      const { queryByRole, getByRole, getByText } = renderWithProviders(
        <AppShell navItems={NAV_ITEMS}>content</AppShell>,
      )
      expect(
        queryByRole('button', { name: zhCN.appShell.collapseNav }),
      ).not.toBeInTheDocument()
      expect(
        queryByRole('button', { name: zhCN.appShell.expandNav }),
      ).not.toBeInTheDocument()
      // The always-open permanent drawer is unchanged, and its label is
      // ordinary visible text (not clipped by any rail).
      expect(
        getByRole('navigation', { name: zhCN.appShell.navLabel }),
      ).toBeInTheDocument()
      expect(labelRootOf(getByText('Home'))).not.toHaveStyle({
        position: 'absolute',
      })
    })

    it('collapses to the icon-only rail: the nav stays mounted at a fixed narrow width, icons remain, labels are clipped but still name the entries', async () => {
      mockMatchMedia(true)
      const onCollapseChange = vi.fn()
      const user = userEvent.setup()
      const { getByRole, getByTestId, getByText } = renderWithProviders(
        <ControlledCollapseHost
          navItems={NAV_WITH_ICONS}
          initialCollapsed={false}
          onCollapseChange={onCollapseChange}
        />,
      )
      const toggle = getByRole('button', { name: zhCN.appShell.collapseNav })
      expect(toggle).toHaveAttribute('aria-expanded', 'true')
      await user.click(toggle)

      expect(onCollapseChange).toHaveBeenCalledTimes(1)
      expect(onCollapseChange).toHaveBeenCalledWith(true)

      // Collapsed NARROWS the drawer to the rail; it does not unmount it.
      // The nav landmark and every entry survive, each still reachable by
      // its accessible name (the label is clipped, not removed from the
      // accessibility tree), and the icons remain rendered.
      expect(
        getByRole('navigation', { name: zhCN.appShell.navLabel }),
      ).toBeInTheDocument()
      expect(getByRole('link', { name: 'Home' })).toBeInTheDocument()
      expect(getByTestId('icon-home')).toBeInTheDocument()
      expect(getByTestId('icon-reports')).toBeInTheDocument()
      // The label is visibly hidden (clipped to preserve the accessible
      // name) once collapsed ...
      expect(labelRootOf(getByText('Home'))).toHaveStyle({
        position: 'absolute',
      })
      // ... and the rail is the fixed narrow width derived from the theme
      // spacing base (7 x 8px = 56px), NOT the old zero-width hide.
      expect(emittedStyleText()).toMatch(/\.MuiDrawer-paper\{[^}]*width:56px/)
      // The toggle now offers to expand.
      expect(
        getByRole('button', { name: zhCN.appShell.expandNav }),
      ).toHaveAttribute('aria-expanded', 'false')
    })

    it('names each rail entry with a tooltip carrying the clipped label', async () => {
      mockMatchMedia(true)
      const user = userEvent.setup()
      const { getByRole, findByRole } = renderWithProviders(
        <ControlledCollapseHost
          navItems={NAV_WITH_ICONS}
          initialCollapsed
          onCollapseChange={() => undefined}
        />,
      )
      // With the label clipped, the tooltip is how a sighted pointer user
      // reads the entry.
      await user.hover(getByRole('link', { name: 'Home' }))
      expect(await findByRole('tooltip')).toHaveTextContent('Home')
    })

    it('keeps an icon-less entry identifiable in the rail instead of a blank cell', () => {
      mockMatchMedia(true)
      const { getByRole, getByText } = renderWithProviders(
        <ControlledCollapseHost
          initialCollapsed
          onCollapseChange={() => undefined}
        />,
      )
      // NAV_ITEMS carries no icons: the rail falls back to the visible
      // (clipped) label rather than rendering nothing, so a host that
      // collapses an icon-less navigation still gets readable rows.
      expect(getByRole('link', { name: 'Home' })).toBeInTheDocument()
      expect(labelRootOf(getByText('Home'))).not.toHaveStyle({
        position: 'absolute',
      })
    })

    it('clips an icon-less label on its text element, so the label ellipsizes instead of being cut mid-glyph', () => {
      mockMatchMedia(true)
      const entries: readonly AppShellNavEntry[] = [
        { id: 'reports', label: 'Reports', href: '/reports' },
        {
          id: 'platform',
          label: 'Platform configuration',
          items: [{ id: 'settings', label: 'Settings', href: '/settings' }],
        },
      ]
      const { getByRole } = renderWithProviders(
        <ControlledCollapseHost
          navItems={entries}
          initialCollapsed
          onCollapseChange={() => undefined}
        />,
      )
      const primaryOf = (control: HTMLElement): HTMLElement => {
        const primary = control.querySelector<HTMLElement>(
          '.MuiListItemText-primary',
        )
        if (primary === null) {
          throw new Error('expected a ListItemText primary element')
        }
        return primary
      }

      // jsdom performs no layout, so what this proves is where the
      // declarations land, not the rendered ellipsis. On the label's own
      // text element they engage as a real one-line ellipsis; on the
      // button -- whose label is a descendant block -- `text-overflow`
      // never applies and the button's `overflow: hidden` cuts the text
      // mid-glyph. Moving these three declarations back onto the button
      // turns this assertion red.
      for (const control of [
        getByRole('link', { name: 'Reports' }),
        getByRole('button', { name: 'Platform configuration' }),
      ]) {
        expect(primaryOf(control)).toHaveStyle({
          minWidth: '0px',
          overflow: 'hidden',
          // `nowrap` is what keeps the label on one line; without it the
          // text wraps and the row grows tall -- the other half of the
          // defect this case exists for.
          whiteSpace: 'nowrap',
          textOverflow: 'ellipsis',
        })
      }
    })

    it('collapses a group heading to its icon instead of a clipped heading', () => {
      mockMatchMedia(true)
      const grouped: readonly AppShellNavEntry[] = [
        {
          id: 'overview',
          label: 'Overview',
          icon: <span data-testid="icon-overview">O</span>,
          items: [
            {
              id: 'home',
              label: 'Home',
              href: '/home',
              icon: <span data-testid="icon-home">H</span>,
            },
          ],
        },
      ]
      const { getByRole, getByTestId, getByText } = renderWithProviders(
        <ControlledCollapseHost
          navItems={grouped}
          initialCollapsed
          onCollapseChange={() => undefined}
        />,
      )
      // The heading keeps its accessible name (and its tooltip), but the
      // rail shows the group's icon rather than half of the heading text.
      expect(getByRole('button', { name: 'Overview' })).toBeInTheDocument()
      expect(getByTestId('icon-overview')).toBeInTheDocument()
      expect(labelRootOf(getByText('Overview'))).toHaveStyle({
        position: 'absolute',
      })
    })

    it('expands back to the full icon+text drawer from the rail', async () => {
      mockMatchMedia(true)
      const user = userEvent.setup()
      const { getByRole, getByText } = renderWithProviders(
        <ControlledCollapseHost
          navItems={NAV_WITH_ICONS}
          initialCollapsed
          onCollapseChange={() => undefined}
        />,
      )
      // Collapsed: the label is clipped (kept for the accessible name).
      expect(labelRootOf(getByText('Home'))).toHaveStyle({
        position: 'absolute',
      })

      await user.click(getByRole('button', { name: zhCN.appShell.expandNav }))

      // Expanded again: the label is ordinary visible text and the nav
      // landmark is still one.
      expect(labelRootOf(getByText('Home'))).not.toHaveStyle({
        position: 'absolute',
      })
      expect(
        getByRole('navigation', { name: zhCN.appShell.navLabel }),
      ).toBeInTheDocument()
    })

    it('leaves the mobile drawer and its own toggle untouched by the collapse state', () => {
      mockMatchMedia(false)
      const { getByRole, queryByRole } = renderWithProviders(
        <ControlledCollapseHost
          initialCollapsed
          onCollapseChange={() => undefined}
        />,
      )
      // Below md the collapse affordance is absent; the mobile nav toggle
      // is the one that renders.
      expect(
        queryByRole('button', { name: zhCN.appShell.collapseNav }),
      ).not.toBeInTheDocument()
      expect(
        getByRole('button', { name: zhCN.appShell.openNav }),
      ).toBeInTheDocument()
    })

    it('has no axe violations on the collapsed rail', async () => {
      mockMatchMedia(true)
      renderWithProviders(
        <AppShell
          navItems={NAV_WITH_ICONS}
          sidebarCollapsed
          onSidebarCollapsedChange={() => undefined}
        >
          <h1>Main content</h1>
        </AppShell>,
      )
      await expectNoAxeViolations()
    })
  })

  describe('slots', () => {
    it('renders only the content passed to each slot, nothing implicit', () => {
      mockMatchMedia(true)
      const { getByText, queryByText, getByRole } = renderWithProviders(
        <AppShell
          navItems={NAV_ITEMS}
          header={<span>Speed Admin</span>}
          headerActions={<button type="button">Search</button>}
          userMenu={<span>Jane Doe</span>}
        >
          <p>Main content</p>
        </AppShell>,
      )
      expect(getByText('Speed Admin')).toBeInTheDocument()
      expect(getByRole('button', { name: 'Search' })).toBeInTheDocument()
      expect(getByText('Jane Doe')).toBeInTheDocument()
      expect(getByText('Main content')).toBeInTheDocument()
      // No stand-in placeholder text ever renders for an absent slot.
      expect(queryByText(/placeholder/i)).not.toBeInTheDocument()
    })

    it('renders no headerActions box when the slot is omitted', () => {
      mockMatchMedia(true)
      const { queryByRole } = renderWithProviders(
        <AppShell navItems={NAV_ITEMS}>content</AppShell>,
      )
      expect(queryByRole('button', { name: 'Search' })).not.toBeInTheDocument()
    })
  })

  describe('landmarks', () => {
    it('exposes header (banner), nav and main landmarks in zh-CN', () => {
      mockMatchMedia(true)
      const { getByRole } = renderWithProviders(
        <AppShell navItems={NAV_ITEMS}>content</AppShell>,
      )
      expect(getByRole('banner')).toBeInTheDocument()
      expect(getByRole('navigation', { name: zhCN.appShell.navLabel })).toBeInTheDocument()
      expect(getByRole('main')).toBeInTheDocument()
    })

    it('names the nav landmark from a host-supplied navLabel, so a host mounting a second shell keeps the landmarks distinguishable', () => {
      mockMatchMedia(true)
      const { getByRole, queryByRole } = renderWithProviders(
        <AppShell navItems={NAV_ITEMS} navLabel="Console navigation">
          content
        </AppShell>,
      )
      expect(
        getByRole('navigation', { name: 'Console navigation' }),
      ).toBeInTheDocument()
      // The package default no longer names this landmark once the host
      // supplies its own.
      expect(
        queryByRole('navigation', { name: zhCN.appShell.navLabel }),
      ).not.toBeInTheDocument()
    })

    it('makes the main landmark focusable so the skip link actually moves focus there', () => {
      mockMatchMedia(true)
      const { getByRole } = renderWithProviders(
        <AppShell navItems={NAV_ITEMS}>content</AppShell>,
      )
      const skipLink = getByRole('link', { name: zhCN.appShell.skipToContent })
      const main = getByRole('main')
      // The skip link keeps an anchor identity (and, with it, an href) ...
      expect(skipLink.getAttribute('href')).toBe(`#${main.id}`)
      // ... and the target must be programmatically focusable
      // (tabIndex={-1}) for the skip mechanism's own programmatic focus
      // to land, or a browser's fragment-navigation focusing algorithm
      // only scrolls to it without ever moving the assistive-tech focus
      // cursor. jsdom enforces the same focusability rule as browsers,
      // so calling .focus() directly on the target is a faithful check
      // of the mechanism the skip link relies on.
      main.focus()
      expect(document.activeElement).toBe(main)
    })

    it('activates the skip link without touching the host hash and moves focus into main', async () => {
      // The skip link is not a plain `href="#target"` anchor: activating
      // such an anchor runs real fragment navigation, which REWRITES
      // location.hash. For a host that routes through the fragment (its
      // own router parses location.hash into its
      // surfaces), that would replace the app route with the generated
      // target id and the route would be lost. The harness below mimics
      // such a host: the hash carries an app route, and the skip link
      // must leave it alone while still moving focus -- driven the way a
      // real keyboard user would (Tab to it, Enter to activate).
      mockMatchMedia(true)
      window.location.hash = '/notes'
      const user = userEvent.setup()
      const { getByRole } = renderWithProviders(
        <AppShell navItems={NAV_ITEMS}>content</AppShell>,
      )
      const main = getByRole('main')
      const skipLink = getByRole('link', { name: zhCN.appShell.skipToContent })

      await user.tab()
      expect(skipLink).toHaveFocus()
      await user.keyboard('{Enter}')

      // The app route survives the activation ...
      expect(window.location.hash).toBe('#/notes')
      // ... and focus moved into the main landmark. jsdom performs the
      // fragment navigation itself (it rewrites the hash and never
      // moves focus), so this half passes only when the app's own
      // activation handler moved focus into the landmark -- with the
      // native mechanism, the hash lands on the target id and focus
      // stays on the link.
      expect(document.activeElement).toBe(main)
    })

    it('relabels the nav landmark and toggle button when the language switches to en-US', async () => {
      mockMatchMedia(false)
      const { getByRole, i18n } = renderWithProviders(
        <AppShell navItems={NAV_ITEMS}>content</AppShell>,
      )
      await act(async () => {
        await switchLanguage(i18n, 'en-US')
      })
      // The temporary drawer starts closed, and MUI keeps a closed (but
      // keepMounted) modal's own root aria-hidden -- an aria-hidden
      // subtree contributes no accessible name at all (by the accname
      // spec), so `hidden: true` finds the node but a `name` filter on
      // it never matches. Assert the built-in text landed on the
      // attribute directly instead; open/close accessibility is covered
      // separately in the "responsive drawer" tests above.
      expect(getByRole('navigation', { hidden: true })).toHaveAttribute(
        'aria-label',
        enUS.appShell.navLabel,
      )
      expect(getByRole('button', { name: enUS.appShell.openNav })).toBeInTheDocument()
    })
  })

  describe('responsive protections', () => {
    // jsdom evaluates neither real layout nor `@media` conditions, so
    // these are property/snapshot assertions on the generated CSS text
    // or on a plain (non-breakpoint-gated) computed style value -- they
    // prove the intended declaration was wired into the render, not
    // that it looks correct at a real narrow viewport.
    it('caps the mobile (temporary) drawer paper width to a CSS min() of sidebarWidth and a viewport fraction', () => {
      mockMatchMedia(false)
      renderWithProviders(
        <AppShell navItems={NAV_ITEMS} sidebarWidth={280}>
          content
        </AppShell>,
      )
      expect(emittedStyleText()).toMatch(/width:\s*min\(280px,\s*85vw\)/)
    })

    it('leaves the desktop (permanent) drawer paper width as the plain sidebarWidth, uncapped', () => {
      mockMatchMedia(true)
      renderWithProviders(
        <AppShell navItems={NAV_ITEMS} sidebarWidth={280}>
          content
        </AppShell>,
      )
      // A precise rule-text match (not a "no 85vw anywhere" assertion,
      // since emotion's injected <style> accumulates every rule any
      // test in this file has rendered so far and never removes one on
      // unmount) -- this is the permanent Drawer's own docked-paper
      // rule, plain box-sizing/width with no CSS min() wrapping it.
      expect(emittedStyleText()).toMatch(
        /\.MuiDrawer-paper\{box-sizing:border-box;width:280px;\}/,
      )
    })

    it('fills its Drawer paper with the nav content instead of re-declaring sidebarWidth, so the content can never outgrow the mobile paper\'s capped width', () => {
      // The nav Box inside both Drawer variants must not carry its own
      // fixed `width: sidebarWidth`, independent of the paper's width.
      // The desktop (permanent) Drawer's paper is the plain
      // sidebarWidth too, but the mobile (temporary) Drawer's paper is
      // capped to `min(sidebarWidth, 85vw)` -- a second, independent
      // sidebarWidth on the content would silently outgrow that cap and
      // bleed past the paper's fixed-position edge (Drawer's paper sets
      // no overflow-x). Asserting `width: 100%` on the nav element
      // itself (fills whichever paper it is mounted into) makes that
      // impossible by construction, in both the closed-mobile and
      // desktop cases.
      mockMatchMedia(false)
      const { getByRole } = renderWithProviders(
        <AppShell navItems={NAV_ITEMS} sidebarWidth={280}>
          content
        </AppShell>,
      )
      expect(getByRole('navigation', { hidden: true })).toHaveStyle({ width: '100%' })
    })

    it('fills the desktop Drawer paper with the nav content the same way', () => {
      mockMatchMedia(true)
      const { getByRole } = renderWithProviders(
        <AppShell navItems={NAV_ITEMS} sidebarWidth={280}>
          content
        </AppShell>,
      )
      expect(
        getByRole('navigation', { name: zhCN.appShell.navLabel }),
      ).toHaveStyle({ width: '100%' })
    })

    it('wraps the AppBar Toolbar row instead of squeezing header/actions/userMenu', () => {
      mockMatchMedia(true)
      const { container } = renderWithProviders(
        <AppShell
          navItems={NAV_ITEMS}
          header={<span>Brand</span>}
          headerActions={<button type="button">Search</button>}
          userMenu={<span>Jane Doe</span>}
        >
          content
        </AppShell>,
      )
      const toolbar = container.querySelector('.MuiToolbar-root')
      expect(toolbar).not.toBeNull()
      expect(toolbar).toHaveStyle({ flexWrap: 'wrap' })
    })

    it('wraps the headerActions group itself when the host supplies several actions', () => {
      mockMatchMedia(true)
      const { getByRole } = renderWithProviders(
        <AppShell
          navItems={NAV_ITEMS}
          headerActions={
            <>
              <button type="button">Search</button>
              <button type="button">Notifications</button>
            </>
          }
        >
          content
        </AppShell>,
      )
      const actionsGroup = getByRole('button', { name: 'Search' }).parentElement
      expect(actionsGroup).toHaveStyle({ flexWrap: 'wrap' })
    })

    it('raises the shell toggles and the nav entries to the 44px touch-target floor under a coarse pointer', () => {
      // The touch-target gate is a media feature, not a breakpoint (see
      // the layout design): a coarse pointer gets the floor on the shell's
      // own toggle buttons and on every navigation entry, a precise
      // pointer the stock sizes. jsdom evaluates no real media condition,
      // so the emitted CSS is matched against the real controls (see
      // @speed/test-utils/emitted-css): a coarse rule elsewhere on the
      // page cannot satisfy these assertions for them.
      mockMatchMedia(true)
      renderWithProviders(
        <AppShell
          navItems={[{ id: 'home', label: 'Home', href: '/home' }]}
          onSidebarCollapsedChange={() => undefined}
        >
          content
        </AppShell>,
      )
      // The samples are the shell's real controls, not the media block in
      // the abstract: the navigation entry (a ListItemButton rendered as
      // an anchor) and the collapse toggle both carry the floor.
      expect(
        hasCoarsePointerTouchTarget(
          screen.getByRole('link', { name: 'Home' }),
        ),
      ).toBe(true)
      expect(
        hasCoarsePointerTouchTarget(
          screen.getByRole('button', { name: zhCN.appShell.collapseNav }),
        ),
      ).toBe(true)
    })
  })

  describe('measured header spacer', () => {
    afterEach(() => {
      vi.unstubAllGlobals()
    })

    it('derives every spacer from the measured header height, so a wrapped header cannot cover content below it', async () => {
      // The AppBar row wraps under real overflow pressure (the flexWrap
      // protection), which makes its height taller than the theme
      // toolbar default -- a taller fixed header would cover the top of
      // main and the drawer's first item. jsdom evaluates no real
      // layout, so this test cannot observe an actual wrap or measure
      // actual coverage; what it can and does prove is the mechanism
      // that keeps the two in lockstep:
      // the header's rendered height is measured (ResizeObserver on the
      // banner) and every spacer placeholder (one in the drawer paper,
      // one in main) derives its min-height from that measurement, and
      // the drawer's nav follows its spacer in document order. In a
      // real browser that height equality is what keeps the fixed
      // AppBar from covering main and the drawer when the toolbar
      // wraps onto a second row.
      mockMatchMedia(true)
      const resizeObserver = stubResizeObserver()
      const { getByRole } = renderWithProviders(
        <AppShell
          navItems={NAV_ITEMS}
          header={<span>Brand</span>}
          headerActions={<button type="button">Search</button>}
          userMenu={<span>Jane Doe</span>}
        >
          content
        </AppShell>,
      )
      // The observer watches the header/banner itself ...
      expect(resizeObserver.observedElement()).toBe(getByRole('banner'))

      // ... and reports a wrapped, two-row header height.
      resizeObserver.emitHeight(112)

      // The main landmark's spacer carries the measured height ...
      const main = getByRole('main')
      const mainSpacer = main.querySelector('.MuiToolbar-root')
      expect(mainSpacer).not.toBeNull()
      expect(mainSpacer).toHaveStyle({ minHeight: '112px' })
      // The height is carried inline, so the theme toolbar's `sm`
      // breakpoint rule cannot restore the single-row height at 600px
      // and above -- the same protection the banner case needs.
      expect((mainSpacer as HTMLElement).style.minHeight).toBe('112px')

      // ... and so does the drawer paper's spacer ...
      const paper = document.querySelector('.MuiDrawer-paper')
      expect(paper).not.toBeNull()
      const drawerSpacer = paper?.querySelector('.MuiToolbar-root') ?? null
      expect(drawerSpacer).not.toBeNull()
      expect(drawerSpacer).toHaveStyle({ minHeight: '112px' })
      expect((drawerSpacer as HTMLElement).style.minHeight).toBe('112px')

      // ... so the first drawer item sits below the spacer in the
      // paper, exactly where the fixed header's overlay ends when the
      // spacer matches the header's height.
      const nav = paper?.querySelector('nav') ?? null
      expect(nav).not.toBeNull()
      expect(
        (drawerSpacer as Node).compareDocumentPosition(nav as Node) &
          Node.DOCUMENT_POSITION_FOLLOWING,
      ).toBeTruthy()
    })
  })

  describe('banner slot', () => {
    afterEach(() => {
      vi.unstubAllGlobals()
    })

    it('pins the host node to the viewport top, above the app bar', () => {
      mockMatchMedia(true)
      const { getByTestId, getByRole } = renderWithProviders(
        <AppShell
          navItems={NAV_ITEMS}
          banner={<p data-testid="notice">Impersonating</p>}
        >
          content
        </AppShell>,
      )
      const notice = getByTestId('notice')
      const appBar = getByRole('banner')
      // Above the app bar: the slot precedes the header landmark in
      // document order.
      expect(
        notice.compareDocumentPosition(appBar) &
          Node.DOCUMENT_POSITION_FOLLOWING,
      ).toBeTruthy()
      // Part of the fixed shell head: pinned to the viewport top and
      // spanning its width, so it stays with the AppBar while the page
      // scrolls. It is neither the banner landmark nor a live region.
      const slot = notice.parentElement
      expect(slot).not.toBeNull()
      const style = getComputedStyle(slot as Element)
      expect(style.position).toBe('fixed')
      expect(style.top).toBe('0px')
      expect(style.left).toBe('0px')
      expect(style.right).toBe('0px')
      // The slot adds no padding, border or minimum height of its own:
      // its height is exactly the host node's.
      expect(style.padding).toMatch(/^0(px)?$/)
      expect(style.borderWidth).toMatch(/^0(px)?$/)
      expect(style.minHeight).toBe('auto')
      // Same layer as the app bar (drawer + 1), which is above the
      // permanent drawer paper and page content and below modal panels
      // and dialogs. The strict equality pins the design's lower bound:
      // a slot at `drawer` or below is painted under the permanent
      // drawer paper's left edge.
      const { theme } = createAppTheme()
      const zIndex = Number(style.zIndex)
      expect(zIndex).toBe(theme.zIndex.drawer + 1)
      expect(zIndex).toBeLessThan(theme.zIndex.modal)
      expect(String(zIndex)).toBe(getComputedStyle(appBar).zIndex)
      const paper = document.querySelector('.MuiDrawer-paper')
      expect(paper).not.toBeNull()
      expect(zIndex).toBeGreaterThan(
        Number(getComputedStyle(paper as Element).zIndex),
      )
    })

    it('offsets the app bar and reserves the banner plus header height in every spacer', () => {
      mockMatchMedia(true)
      const resizeObserver = stubResizeObserver()
      const { getByTestId, getByRole } = renderWithProviders(
        <AppShell
          navItems={NAV_ITEMS}
          banner={<p data-testid="notice">Impersonating</p>}
        >
          content
        </AppShell>,
      )
      const slot = getByTestId('notice').parentElement
      expect(slot).not.toBeNull()
      // Both measured boxes are observed: the AppBar's height feeds the
      // spacers together with the banner's, which also offsets the AppBar.
      expect(resizeObserver.observedElements()).toContain(slot)
      const appBar = getByRole('banner')
      resizeObserver.emitHeightFor(appBar, 112)
      resizeObserver.emitHeightFor(slot as Element, 48)

      // The AppBar starts below the banner ...
      expect(appBar).toHaveStyle({ top: '48px' })
      // ... and both the content and the navigation reserve the banner's
      // height plus the header's.
      const mainSpacer = getByRole('main').querySelector('.MuiToolbar-root')
      expect(mainSpacer).not.toBeNull()
      expect(mainSpacer).toHaveStyle({ minHeight: '160px' })
      const paper = document.querySelector('.MuiDrawer-paper')
      const drawerSpacer = paper?.querySelector('.MuiToolbar-root') ?? null
      expect(drawerSpacer).not.toBeNull()
      expect(drawerSpacer).toHaveStyle({ minHeight: '160px' })
      // The paper starts at the viewport top itself: the banner covers
      // its first strip, and the spacer inside it reserves the head, so
      // the paper carries no top offset of its own.
      expect(paper).not.toBeNull()
      expect(getComputedStyle(paper as Element).top).not.toBe('48px')
    })

    it('writes each measured spacer height as an inline style, out of reach of the theme toolbar breakpoint rule', () => {
      // The theme toolbar mixin contributes `min-height: 56` at the base
      // and `64` under `sm`, and that breakpoint rule lands after an
      // equal-specificity `sx` class -- an sx-written height is
      // overridden from 600px up, where the banner plus header then
      // covers the top of content again. jsdom evaluates no media
      // conditions, so this test proves where the height is carried
      // (the style attribute, which no class rule outranks), not how a
      // real viewport paints; the declaration that survives there is
      // the one asserted here.
      mockMatchMedia(true)
      const resizeObserver = stubResizeObserver()
      const { getByTestId, getByRole } = renderWithProviders(
        <AppShell
          navItems={NAV_ITEMS}
          banner={<p data-testid="notice">Impersonating</p>}
        >
          content
        </AppShell>,
      )
      const slot = getByTestId('notice').parentElement
      expect(slot).not.toBeNull()
      resizeObserver.emitHeightFor(getByRole('banner'), 112)
      resizeObserver.emitHeightFor(slot as Element, 48)

      const mainSpacer =
        getByRole('main').querySelector<HTMLElement>('.MuiToolbar-root')
      expect(mainSpacer).not.toBeNull()
      expect((mainSpacer as HTMLElement).style.minHeight).toBe('160px')
      const drawerSpacer = document.querySelector<HTMLElement>(
        '.MuiDrawer-paper .MuiToolbar-root',
      )
      expect(drawerSpacer).not.toBeNull()
      expect(drawerSpacer?.style.minHeight).toBe('160px')
    })

    it('leaves the spacer at the plain theme height when only the banner height has been measured', () => {
      mockMatchMedia(true)
      const resizeObserver = stubResizeObserver()
      const { getByTestId, getByRole } = renderWithProviders(
        <AppShell
          navItems={NAV_ITEMS}
          banner={<p data-testid="notice">Impersonating</p>}
        >
          content
        </AppShell>,
      )
      const slot = getByTestId('notice').parentElement
      expect(slot).not.toBeNull()
      // Only the banner's own height has arrived; the header's has not.
      resizeObserver.emitHeightFor(slot as Element, 48)
      const mainSpacer =
        getByRole('main').querySelector<HTMLElement>('.MuiToolbar-root')
      expect(mainSpacer).not.toBeNull()
      // Still the theme's plain Toolbar: no measured height and no pixel
      // value written as a theme spacing multiplier (48px must never
      // become 384px).
      expect((mainSpacer as HTMLElement).style.minHeight).toBe('')
      expect(getComputedStyle(mainSpacer as Element).marginTop).toMatch(/^0(px)?$/)
      const drawerSpacer = document.querySelector('.MuiDrawer-paper .MuiToolbar-root')
      expect(drawerSpacer).not.toBeNull()
      expect(getComputedStyle(drawerSpacer as Element).marginTop).toMatch(/^0(px)?$/)
    })

    it('offsets the app bar and reserves the banner plus header in the temporary drawer at narrow widths', () => {
      mockMatchMedia(false)
      const resizeObserver = stubResizeObserver()
      const { getByTestId, getByRole } = renderWithProviders(
        <AppShell
          navItems={NAV_ITEMS}
          banner={<p data-testid="notice">Impersonating</p>}
        >
          content
        </AppShell>,
      )
      const slot = getByTestId('notice').parentElement
      expect(slot).not.toBeNull()
      // The temporary drawer is keepMounted, so its paper and spacer are
      // reachable even while the drawer is closed.
      const drawerSpacer =
        document.querySelector('.MuiDrawer-paper .MuiToolbar-root')
      expect(drawerSpacer).not.toBeNull()

      resizeObserver.emitHeightFor(getByRole('banner'), 56)
      resizeObserver.emitHeightFor(slot as Element, 48)

      // The app bar shifts below the banner at this breakpoint too ...
      expect(getByRole('banner')).toHaveStyle({ top: '48px' })
      // ... and the mobile paper's first navigation item clears the same
      // banner-plus-header height the content does.
      expect(
        getByRole('main').querySelector('.MuiToolbar-root'),
      ).toHaveStyle({ minHeight: '104px' })
      expect(drawerSpacer).toHaveStyle({ minHeight: '104px' })
    })

    it('takes the banner height as zero where no ResizeObserver exists', () => {
      mockMatchMedia(true)
      expect(typeof ResizeObserver).toBe('undefined')
      const { getByRole } = renderWithProviders(
        <AppShell
          navItems={NAV_ITEMS}
          banner={<p data-testid="notice">Impersonating</p>}
        >
          content
        </AppShell>,
      )
      // Unmeasured means zero: the app bar stays at the viewport top and
      // the spacer keeps the plain theme toolbar height.
      expect(getByRole('banner')).toHaveStyle({ top: '0px' })
      const mainSpacer =
        getByRole('main').querySelector<HTMLElement>('.MuiToolbar-root')
      expect(mainSpacer).not.toBeNull()
      expect((mainSpacer as HTMLElement).style.minHeight).toBe('')
      expect(getComputedStyle(mainSpacer as Element).marginTop).toMatch(/^0(px)?$/)
    })

    it.each([null, undefined, false] as const)(
      'zeroes the banner offset and the spacers when the banner becomes %s after being measured',
      (banner) => {
        mockMatchMedia(true)
        const resizeObserver = stubResizeObserver()
        const { getByTestId, getByRole, queryByTestId, rerender } =
          renderWithProviders(
            <AppShell
              navItems={NAV_ITEMS}
              banner={<p data-testid="notice">Impersonating</p>}
            >
              content
            </AppShell>,
          )
        const slot = getByTestId('notice').parentElement
        expect(slot).not.toBeNull()
        resizeObserver.emitHeightFor(getByRole('banner'), 112)
        resizeObserver.emitHeightFor(slot as Element, 48)
        expect(getByRole('banner')).toHaveStyle({ top: '48px' })

        rerender(
          <AppShell navItems={NAV_ITEMS} banner={banner}>
            content
          </AppShell>,
        )
        expect(queryByTestId('notice')).not.toBeInTheDocument()
        // The stale measurement no longer offsets the app bar ...
        expect(getByRole('banner')).toHaveStyle({ top: '0px' })
        // ... and the spacers keep only the header height.
        expect(
          getByRole('main').querySelector('.MuiToolbar-root'),
        ).toHaveStyle({ minHeight: '112px' })
        expect(
          document.querySelector('.MuiDrawer-paper .MuiToolbar-root'),
        ).toHaveStyle({ minHeight: '112px' })
      },
    )

    it('disconnects the observer on every banner change and on unmount, measuring only the live banner', () => {
      mockMatchMedia(true)
      const resizeObserver = stubResizeObserver()
      const { getByTestId, queryByTestId, rerender, unmount } =
        renderWithProviders(
          <AppShell
            navItems={NAV_ITEMS}
            banner={<p data-testid="notice">Impersonating</p>}
          >
            content
          </AppShell>,
        )
      const firstSlot = getByTestId('notice').parentElement
      expect(firstSlot).not.toBeNull()
      expect(resizeObserver.observedElements()).toContain(firstSlot)
      expect(resizeObserver.observedElements()).toHaveLength(2)
      expect(resizeObserver.instances()).toHaveLength(1)

      // The banner leaves: the old observer is disconnected (it watched
      // a node that no longer exists) and its replacement watches the
      // app bar alone.
      rerender(<AppShell navItems={NAV_ITEMS}>content</AppShell>)
      expect(queryByTestId('notice')).not.toBeInTheDocument()
      expect(resizeObserver.observedElements()).not.toContain(firstSlot)
      expect(resizeObserver.observedElements()).toHaveLength(1)
      expect(resizeObserver.instances()).toHaveLength(2)
      expect(resizeObserver.instances().at(0)?.disconnected).toBe(true)
      expect(resizeObserver.instances().at(1)?.disconnected).toBe(false)

      // A new banner mounts: the new node is the one watched, not the
      // removed one.
      rerender(
        <AppShell
          navItems={NAV_ITEMS}
          banner={<p data-testid="notice">Impersonating</p>}
        >
          content
        </AppShell>,
      )
      const secondSlot = getByTestId('notice').parentElement
      expect(secondSlot).not.toBe(firstSlot)
      expect(resizeObserver.observedElements()).toContain(secondSlot)
      expect(resizeObserver.observedElements()).not.toContain(firstSlot)

      unmount()
      expect(resizeObserver.observedElements()).toHaveLength(0)
      expect(
        resizeObserver.instances().every((instance) => instance.disconnected),
      ).toBe(true)
    })

    it('takes an empty host node as zero height, leaving the app bar at the top', () => {
      mockMatchMedia(true)
      const resizeObserver = stubResizeObserver()
      const { getByTestId, getByRole } = renderWithProviders(
        <AppShell navItems={NAV_ITEMS} banner={<span data-testid="empty" />}>
          content
        </AppShell>,
      )
      const slot = getByTestId('empty').parentElement
      expect(slot).not.toBeNull()
      const appBar = getByRole('banner')
      resizeObserver.emitHeightFor(appBar, 112)
      // The host node rendered nothing: the container measures zero and
      // contributes none of its own height.
      resizeObserver.emitHeightFor(slot as Element, 0)
      expect(appBar).toHaveStyle({ top: '0px' })
      const mainSpacer = getByRole('main').querySelector('.MuiToolbar-root')
      expect(mainSpacer).toHaveStyle({ minHeight: '112px' })
    })

    it('keeps the banner mounted when the route content changes', () => {
      mockMatchMedia(true)
      const banner = <p data-testid="notice">Impersonating</p>
      const { getByTestId, rerender } = renderWithProviders(
        <AppShell navItems={NAV_ITEMS} banner={banner}>
          <p>route a</p>
        </AppShell>,
      )
      const notice = getByTestId('notice')
      rerender(
        <AppShell navItems={NAV_ITEMS} banner={banner}>
          <p>route b</p>
        </AppShell>,
      )
      // The same host node survives the content swap: the slot belongs to
      // the shell, not to the routed children.
      expect(getByTestId('notice')).toBe(notice)
      expect(screen.getByText('route b')).toBeInTheDocument()
      expect(screen.queryByText('route a')).not.toBeInTheDocument()
    })

    it('renders no container and takes no height when the slot is absent, null or false', () => {
      mockMatchMedia(true)
      for (const banner of [undefined, null, false] as const) {
        const { container, unmount } = renderWithProviders(
          <AppShell navItems={NAV_ITEMS} banner={banner}>
            content
          </AppShell>,
        )
        // The AppBar, the sidebar column and main -- no wrapper behind.
        expect(container.firstElementChild?.children).toHaveLength(3)
        unmount()
      }
    })

    it('adds no landmark and no live region around the host node', () => {
      mockMatchMedia(true)
      const { getByTestId, queryAllByRole } = renderWithProviders(
        <AppShell
          navItems={NAV_ITEMS}
          banner={<span data-testid="notice">Impersonating</span>}
        >
          content
        </AppShell>,
      )
      const slot = getByTestId('notice').parentElement
      expect(slot?.hasAttribute('role')).toBe(false)
      expect(slot?.hasAttribute('aria-label')).toBe(false)
      expect(slot?.hasAttribute('aria-live')).toBe(false)
      // The shell's own landmark set is unchanged, and the slot created
      // none of the roles the host node is expected to carry.
      expect(queryAllByRole('region')).toHaveLength(0)
      expect(queryAllByRole('status')).toHaveLength(0)
    })

    it('renders the banner at both breakpoints', () => {
      mockMatchMedia(true)
      const desktop = renderWithProviders(
        <AppShell navItems={NAV_ITEMS} banner={<p>desktop notice</p>}>
          content
        </AppShell>,
      )
      expect(desktop.getByText('desktop notice')).toBeInTheDocument()
      desktop.unmount()

      mockMatchMedia(false)
      const mobile = renderWithProviders(
        <AppShell navItems={NAV_ITEMS} banner={<p>mobile notice</p>}>
          content
        </AppShell>,
      )
      expect(mobile.getByText('mobile notice')).toBeInTheDocument()
    })
  })

  describe('navigation groups', () => {
    // A grouped navigation: two titled, collapsible sections whose entries are
    // ordinary links. Backward compatibility is the point of the flat test at
    // the end -- an existing AppShellNavItem[] host renders exactly as before.
    const GROUPED: readonly AppShellNavEntry[] = [
      {
        id: 'overview',
        label: 'Overview',
        items: [{ id: 'home', label: 'Home', href: '/home', selected: true }],
      },
      {
        id: 'admin',
        label: 'Administration',
        items: [
          { id: 'users', label: 'Users', href: '/users' },
          { id: 'roles', label: 'Roles', href: '/roles' },
        ],
      },
    ]

    it('renders each group as a titled section whose heading is a disclosure over its entries', () => {
      mockMatchMedia(true)
      const { getByRole } = renderWithProviders(
        <AppShell navItems={GROUPED}>content</AppShell>,
      )
      // The heading is the expand control, open by default.
      expect(getByRole('button', { name: 'Overview' })).toHaveAttribute(
        'aria-expanded',
        'true',
      )
      // The entries beneath it are real links, selection preserved.
      const home = getByRole('link', { name: 'Home' })
      expect(home).toHaveAttribute('href', '/home')
      expect(home).toHaveAttribute('aria-current', 'page')
      expect(getByRole('link', { name: 'Roles' })).toHaveAttribute('href', '/roles')
    })

    it('collapses and expands a group: aria-expanded flips and its entries leave the accessibility tree', async () => {
      mockMatchMedia(true)
      const user = userEvent.setup()
      const { getByRole, queryByRole } = renderWithProviders(
        <AppShell navItems={GROUPED}>content</AppShell>,
      )
      expect(getByRole('link', { name: 'Users' })).toBeInTheDocument()

      await user.click(getByRole('button', { name: 'Administration' }))
      expect(getByRole('button', { name: 'Administration' })).toHaveAttribute(
        'aria-expanded',
        'false',
      )
      // Collapsed: the group's own entries are gone from the a11y tree ...
      expect(queryByRole('link', { name: 'Users' })).not.toBeInTheDocument()
      expect(queryByRole('link', { name: 'Roles' })).not.toBeInTheDocument()
      // ... while another group's entries are untouched.
      expect(getByRole('link', { name: 'Home' })).toBeInTheDocument()

      await user.click(getByRole('button', { name: 'Administration' }))
      expect(getByRole('button', { name: 'Administration' })).toHaveAttribute(
        'aria-expanded',
        'true',
      )
      expect(getByRole('link', { name: 'Users' })).toBeInTheDocument()
    })

    it('starts a group collapsed when defaultCollapsed is set', () => {
      mockMatchMedia(true)
      const { getByRole, queryByRole } = renderWithProviders(
        <AppShell
          navItems={[
            {
              id: 'closed',
              label: 'Closed group',
              defaultCollapsed: true,
              items: [{ id: 'hidden', label: 'Hidden entry', href: '/hidden' }],
            },
          ]}
        >
          content
        </AppShell>,
      )
      expect(getByRole('button', { name: 'Closed group' })).toHaveAttribute(
        'aria-expanded',
        'false',
      )
      expect(queryByRole('link', { name: 'Hidden entry' })).not.toBeInTheDocument()
    })

    it('nests an item with children as an indented list of real links, reusing selected/aria-current', () => {
      mockMatchMedia(true)
      const { getByRole } = renderWithProviders(
        <AppShell
          navItems={[
            {
              id: 'identity',
              label: 'Identity',
              children: [
                { id: 'users', label: 'Users', href: '/users', selected: true },
                { id: 'groups', label: 'Groups', href: '/groups' },
              ],
            },
          ]}
        >
          content
        </AppShell>,
      )
      // The parent is a disclosure (not a link), open by default.
      expect(getByRole('button', { name: 'Identity' })).toHaveAttribute(
        'aria-expanded',
        'true',
      )
      // Each child is a real link, indented one level (theme spacing 4 = 32px).
      const users = getByRole('link', { name: 'Users' })
      expect(users).toHaveAttribute('href', '/users')
      expect(users).toHaveStyle({ paddingLeft: '32px' })
      expect(users).toHaveAttribute('aria-current', 'page')
      const groups = getByRole('link', { name: 'Groups' })
      expect(groups).toHaveAttribute('href', '/groups')
      expect(groups).not.toHaveAttribute('aria-current')
    })

    it('collapses a parent with children, hiding the nested entries', async () => {
      mockMatchMedia(true)
      const user = userEvent.setup()
      const { getByRole, queryByRole } = renderWithProviders(
        <AppShell
          navItems={[
            {
              id: 'parent',
              label: 'Parent',
              children: [{ id: 'child', label: 'Child', href: '/child' }],
            },
          ]}
        >
          content
        </AppShell>,
      )
      expect(getByRole('link', { name: 'Child' })).toBeInTheDocument()
      await user.click(getByRole('button', { name: 'Parent' }))
      expect(getByRole('button', { name: 'Parent' })).toHaveAttribute(
        'aria-expanded',
        'false',
      )
      expect(queryByRole('link', { name: 'Child' })).not.toBeInTheDocument()
    })

    it('renders a flat list unchanged when the host declares no groups or children', () => {
      mockMatchMedia(true)
      const { getByRole, queryAllByRole } = renderWithProviders(
        <AppShell navItems={NAV_ITEMS}>content</AppShell>,
      )
      // Backward compatibility: the original AppShellNavItem[] shape renders
      // the same links and introduces no disclosure controls.
      expect(getByRole('link', { name: 'Home' })).toHaveAttribute('href', '/home')
      expect(getByRole('link', { name: 'Reports' })).toHaveAttribute('href', '/reports')
      expect(queryAllByRole('button')).toHaveLength(0)
    })

    it('has no axe violations on the grouped and nested layout', async () => {
      mockMatchMedia(true)
      renderWithProviders(
        <AppShell
          navItems={[
            ...GROUPED,
            {
              id: 'nested',
              label: 'Nested section',
              items: [
                {
                  id: 'identity',
                  label: 'Identity',
                  children: [
                    { id: 'accounts', label: 'Accounts', href: '/accounts' },
                  ],
                },
              ],
            },
          ]}
        >
          <h1>Main content</h1>
        </AppShell>,
      )
      await expectNoAxeViolations()
    })
  })

  describe('accessibility', () => {
    // Both closed-layout scans render the shell with the page content a
    // real host puts inside `main` -- including the page's h1, which
    // chrome deliberately does not render itself: page-has-heading-one
    // is determinate in jsdom (see the axe helper header), so a scan
    // document without an h1 fails instead of passing by indeterminacy.
    // The shell's own landmark structure (a real main) answers
    // landmark-one-main; region stays enabled.
    it('has no axe violations on the desktop layout, with region enabled', async () => {
      mockMatchMedia(true)
      renderWithProviders(
        <AppShell
          navItems={NAV_ITEMS}
          header={<span>Speed Admin</span>}
          userMenu={<span>Jane Doe</span>}
        >
          <h1>Main content</h1>
        </AppShell>,
      )
      await expectNoAxeViolations()
    })

    it('has no axe violations with a focusable banner slot, with region enabled', async () => {
      // The skip link now renders outside the shell's landmarks, ahead
      // of the banner slot. axe's region rule exempts a skip link whose
      // fragment target resolves (here, `main`), which this scan proves
      // for a banner carrying a focusable control, the shape the host
      // impersonation banner has. The banner fixture carries its own
      // region role, as the host's does.
      mockMatchMedia(true)
      renderWithProviders(
        <AppShell
          navItems={NAV_ITEMS}
          banner={
            <section aria-label="Impersonation">
              <button type="button">End impersonation</button>
            </section>
          }
        >
          <h1>Main content</h1>
        </AppShell>,
      )
      await expectNoAxeViolations()
    })

    it('has no axe violations on the mobile layout, with region enabled', async () => {
      mockMatchMedia(false)
      renderWithProviders(
        <AppShell navItems={NAV_ITEMS}>
          <h1>Main content</h1>
        </AppShell>,
      )
      await expectNoAxeViolations()
    })

    it('has no axe violations on the mobile layout with the drawer OPEN, with region enabled', async () => {
      // The temporary drawer is a real modal (role=dialog aria-modal)
      // while open -- the only usable state of mobile navigation -- so
      // the closed-layout scans above never measure it. The drawer's
      // paper carries the nav label as its dialog name (see
      // AppShell.tsx); without an accessible name, axe's
      // aria-dialog-name rule fails the paper. This scan runs over the
      // open state. Page-context rules are exempted while the modal is
      // open, the same passForModal semantics a browser applies.
      mockMatchMedia(false)
      const user = userEvent.setup()
      const { getByRole } = renderWithProviders(
        <AppShell navItems={NAV_ITEMS}>
          <h1>Main content</h1>
        </AppShell>,
      )
      await user.click(getByRole('button', { name: zhCN.appShell.openNav }))
      expect(
        document.querySelector('.MuiDrawer-paper')?.getAttribute('aria-modal'),
      ).toBe('true')
      await expectNoAxeViolations()
    })
  })
})
