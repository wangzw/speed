import { useCallback, useState } from 'react'
import { act, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { appBarClasses } from '@mui/material/AppBar'
import { backdropClasses } from '@mui/material/Backdrop'
import { drawerClasses } from '@mui/material/Drawer'
import { listItemTextClasses } from '@mui/material/ListItemText'
import { paperClasses } from '@mui/material/Paper'
import { toolbarClasses } from '@mui/material/Toolbar'
import { switchLanguage } from '@speed/i18n'
import { createAppTheme } from '@speed/ui-kit'
import enUS from '../locales/en-US.json' with { type: 'json' }
import zhCN from '../locales/zh-CN.json' with { type: 'json' }
import {
  expectNoAxeViolations,
} from '../../test-utils/axe.js'
import {
  emittedStyleText,
  hasCoarsePointerTouchTarget,
} from '@speed/test-utils/emitted-css'
import { mockMatchMedia } from '@speed/test-utils/matchMedia'
import { renderWithProviders } from '../../test-utils/render.js'
import { stubResizeObserver } from '../../test-utils/resize-observer.js'
import { AppShell, type AppShellNavEntry, type AppShellNavItem } from './AppShell.js'

afterEach(() => {
  window.location.hash = ''
})

function emittedDeclarations(element: Element): string {
  const css = emittedStyleText()
  return Array.from(element.classList)
    .filter((name) => name.startsWith('css-'))
    .map((name) => {
      const rule = new RegExp(`\\.${name}\\{([^}]*)\\}`, 'g')
      return Array.from(css.matchAll(rule))
        .map((match) => match[1])
        .join(';')
    })
    .join(';')
}

const NAV_ITEMS: readonly AppShellNavItem[] = [
  { id: 'home', label: 'Home', href: '/home', selected: true },
  { id: 'reports', label: 'Reports', href: '/reports' },
]

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

    it('tints the navigation states with the theme primary instead of a neutral wash', () => {
      mockMatchMedia(true)
      const { getByRole } = renderWithProviders(
        <AppShell navItems={NAV_ITEMS}>content</AppShell>,
      )
      const css = emittedStyleText()
      expect(css).toContain('rgba(37, 99, 235, 0.08)')
      expect(css).toContain('rgba(37, 99, 235, 0.14)')
      expect(css).toContain('rgba(37, 99, 235, 0.18)')
      expect(
        getComputedStyle(getByRole('link', { name: 'Home' })).backgroundColor,
      ).toBe('rgba(37, 99, 235, 0.14)')
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
      const closeToggle = getByRole('button', {
        name: zhCN.appShell.closeNav,
        hidden: true,
      })
      expect(closeToggle).toHaveAttribute('aria-expanded', 'true')

      await user.click(closeToggle)
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
      expect(getByRole('button', { name: zhCN.appShell.openNav })).toHaveAttribute(
        'aria-expanded',
        'false',
      )
    })

    it('closes the uncontrolled temporary drawer itself when a nav item is activated, scrim cleared and focus released', async () => {
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

      await waitFor(() => {
        expect(getByRole('button', { name: zhCN.appShell.openNav })).toHaveAttribute(
          'aria-expanded',
          'false',
        )
      })
      const backdrop = document.querySelector(`.${backdropClasses.root}`)
      expect(backdrop).not.toBeNull()
      expect(backdrop).not.toBeVisible()
      expect(window.location.hash).toBe('#/home')
      const paper = document.querySelector(`.${drawerClasses.paper}`)
      expect(paper).not.toBeNull()
      expect(paper?.contains(document.activeElement)).toBe(false)
    })

    it('keeps the controlled contract on nav activation: reports nothing and stays open until the host closes', async () => {
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
      const media = mockMatchMedia(false)
      const user = userEvent.setup()
      const { getByRole, queryByRole } = renderWithProviders(
        <AppShell navItems={NAV_ITEMS}>content</AppShell>,
      )

      await user.click(getByRole('button', { name: zhCN.appShell.openNav }))
      expect(
        getByRole('button', { name: zhCN.appShell.closeNav, hidden: true }),
      ).toHaveAttribute('aria-expanded', 'true')

      act(() => {
        media.changeMatches(true)
      })
      await waitFor(() => {
        expect(
          queryByRole('button', { name: zhCN.appShell.openNav, hidden: true }),
        ).not.toBeInTheDocument()
      })
      expect(getByRole('navigation', { name: zhCN.appShell.navLabel })).toBeInTheDocument()

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
      const media = mockMatchMedia(false)
      const onMobileOpenChange = vi.fn()
      const { getByRole } = renderWithProviders(
        <ControlledDrawerHost initialOpen onMobileOpenChange={onMobileOpenChange} />,
      )

      expect(
        getByRole('button', { name: zhCN.appShell.closeNav, hidden: true }),
      ).toHaveAttribute('aria-expanded', 'true')

      act(() => {
        media.changeMatches(true)
      })
      expect(onMobileOpenChange).toHaveBeenCalledTimes(1)
      expect(onMobileOpenChange).toHaveBeenCalledWith(false)

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

    const labelRootOf = (element: HTMLElement): Element | null =>
      element.closest(`.${listItemTextClasses.root}`)

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

      expect(
        getByRole('navigation', { name: zhCN.appShell.navLabel }),
      ).toBeInTheDocument()
      expect(getByRole('link', { name: 'Home' })).toBeInTheDocument()
      expect(getByTestId('icon-home')).toBeInTheDocument()
      expect(getByTestId('icon-reports')).toBeInTheDocument()
      expect(labelRootOf(getByText('Home'))).toHaveStyle({
        position: 'absolute',
      })
      expect(emittedStyleText()).toMatch(
        new RegExp(
          `\\.${drawerClasses.paper}\\{[^}]*width:max\\(56px,\\s*53px\\)`,
        ),
      )
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
          `.${listItemTextClasses.primary}`,
        )
        if (primary === null) {
          throw new Error('expected a ListItemText primary element')
        }
        return primary
      }

      for (const control of [
        getByRole('link', { name: 'Reports' }),
        getByRole('button', { name: 'Platform configuration' }),
      ]) {
        expect(primaryOf(control)).toHaveStyle({
          minWidth: '0px',
          overflow: 'hidden',
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
      expect(labelRootOf(getByText('Home'))).toHaveStyle({
        position: 'absolute',
      })

      await user.click(getByRole('button', { name: zhCN.appShell.expandNav }))

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

  describe('header surface', () => {
    it('paints the app bar from the surface tokens instead of the primary fill', () => {
      mockMatchMedia(true)
      const { tokens } = createAppTheme()
      renderWithProviders(<AppShell navItems={NAV_ITEMS}>content</AppShell>)
      const banner = screen.getByRole('banner')
      expect(banner).toHaveStyle({
        backgroundColor: tokens.color.background.paper,
        color: tokens.color.text.primary,
        borderBottom: `1px solid ${tokens.color.divider}`,
      })
      expect(banner).toHaveClass(paperClasses.elevation0)
    })
  })

  describe('drawer header and footer slots', () => {
    it('renders both slots immediately before and after the nav landmark inside the permanent drawer paper', () => {
      mockMatchMedia(true)
      const { getByRole, getByTestId } = renderWithProviders(
        <AppShell
          navItems={NAV_ITEMS}
          navHeader={<div data-testid="nav-header">Header</div>}
          navFooter={<div data-testid="nav-footer">Footer</div>}
        >
          content
        </AppShell>,
      )
      const nav = getByRole('navigation', { name: zhCN.appShell.navLabel })
      const header = getByTestId('nav-header')
      const footer = getByTestId('nav-footer')
      expect(header.nextElementSibling).toBe(nav)
      expect(nav.nextElementSibling).toBe(footer)
      expect(nav.contains(header)).toBe(false)
      expect(nav.contains(footer)).toBe(false)

      const paper = document.querySelector(`.${drawerClasses.paper}`)
      expect(paper).not.toBeNull()
      expect(paper).toContainElement(header)
      expect(paper).toContainElement(footer)
    })

    it('passes narrow=true to the render functions only in the collapsed rail', () => {
      mockMatchMedia(true)
      const collapsed = renderWithProviders(
        <AppShell
          navItems={NAV_ITEMS}
          sidebarCollapsed
          onSidebarCollapsedChange={() => undefined}
          navHeader={({ narrow }) => (
            <span data-testid="nav-header">{narrow ? 'rail' : 'full'}</span>
          )}
          navFooter={({ narrow }) => (
            <span data-testid="nav-footer">{narrow ? 'rail' : 'full'}</span>
          )}
        >
          content
        </AppShell>,
      )
      expect(collapsed.getByTestId('nav-header')).toHaveTextContent('rail')
      expect(collapsed.getByTestId('nav-footer')).toHaveTextContent('rail')
      collapsed.unmount()

      const expanded = renderWithProviders(
        <AppShell
          navItems={NAV_ITEMS}
          navHeader={({ narrow }) => (
            <span data-testid="nav-header">{narrow ? 'rail' : 'full'}</span>
          )}
          navFooter={({ narrow }) => (
            <span data-testid="nav-footer">{narrow ? 'rail' : 'full'}</span>
          )}
        >
          content
        </AppShell>,
      )
      expect(expanded.getByTestId('nav-header')).toHaveTextContent('full')
      expect(expanded.getByTestId('nav-footer')).toHaveTextContent('full')
    })

    it('renders both slots in the temporary drawer, where narrow is false, and closeDrawer closes an uncontrolled drawer', async () => {
      mockMatchMedia(false)
      const user = userEvent.setup()
      const { getByRole, getByTestId } = renderWithProviders(
        <AppShell
          navItems={NAV_ITEMS}
          navHeader={({ narrow }) => (
            <span data-testid="nav-header">{narrow ? 'rail' : 'full'}</span>
          )}
          navFooter={({ closeDrawer }) => (
            <button type="button" data-testid="nav-footer" onClick={closeDrawer}>
              Close
            </button>
          )}
        >
          content
        </AppShell>,
      )

      await user.click(getByRole('button', { name: zhCN.appShell.openNav }))
      const nav = getByRole('navigation', { name: zhCN.appShell.navLabel })
      expect(getByTestId('nav-header')).toHaveTextContent('full')
      expect(getByTestId('nav-header').nextElementSibling).toBe(nav)
      expect(nav.nextElementSibling).toBe(getByTestId('nav-footer'))

      await user.click(getByTestId('nav-footer'))
      await waitFor(() => {
        expect(
          getByRole('button', { name: zhCN.appShell.openNav }),
        ).toHaveAttribute('aria-expanded', 'false')
      })
    })

    it('reports the close through onMobileOpenChange for a controlled temporary drawer, leaving the close to the host', async () => {
      mockMatchMedia(false)
      const onMobileOpenChange = vi.fn()
      const user = userEvent.setup()
      const { getByRole, getByTestId } = renderWithProviders(
        <AppShell
          navItems={NAV_ITEMS}
          mobileOpen
          onMobileOpenChange={onMobileOpenChange}
          navFooter={({ closeDrawer }) => (
            <button type="button" data-testid="nav-footer" onClick={closeDrawer}>
              Close
            </button>
          )}
        >
          content
        </AppShell>,
      )

      await user.click(getByTestId('nav-footer'))
      expect(onMobileOpenChange).toHaveBeenCalledTimes(1)
      expect(onMobileOpenChange).toHaveBeenCalledWith(false)
      expect(
        getByRole('button', { name: zhCN.appShell.closeNav, hidden: true }),
      ).toHaveAttribute('aria-expanded', 'true')
    })

    it('makes closeDrawer a no-op for the permanent drawer', () => {
      mockMatchMedia(true)
      const onMobileOpenChange = vi.fn()
      let captured: (() => void) | undefined
      const { getByTestId } = renderWithProviders(
        <AppShell
          navItems={NAV_ITEMS}
          mobileOpen={false}
          onMobileOpenChange={onMobileOpenChange}
          navFooter={({ closeDrawer }) => {
            captured = closeDrawer
            return <span data-testid="nav-footer">Footer</span>
          }}
        >
          content
        </AppShell>,
      )
      expect(getByTestId('nav-footer')).toBeInTheDocument()
      expect(captured).toBeTypeOf('function')
      act(() => {
        captured?.()
      })
      expect(onMobileOpenChange).not.toHaveBeenCalled()
    })

    it('renders nothing for an absent slot and leaves the permanent drawer paper unchanged', () => {
      mockMatchMedia(true)
      const { getByRole } = renderWithProviders(
        <AppShell navItems={NAV_ITEMS}>content</AppShell>,
      )
      const nav = getByRole('navigation', { name: zhCN.appShell.navLabel })
      expect(nav.previousElementSibling).toHaveClass(toolbarClasses.root)
      expect(nav.nextElementSibling).toBeNull()
    })
  })

  describe('sidebar shell layout', () => {
    afterEach(() => {
      vi.unstubAllGlobals()
    })


    const paper = (): HTMLElement => {
      const element = document.querySelector<HTMLElement>(
        `.${drawerClasses.paper}`,
      )
      if (element === null) {
        throw new Error('expected a mounted drawer paper')
      }
      return element
    }

    it('starts its first row on the rule under the brand row', () => {
      mockMatchMedia(true)
      const { getByRole } = renderWithProviders(
        <AppShell
          shellLayout="sidebar"
          navItems={NAV_ITEMS}
          header={<span>Speed Admin</span>}
        >
          content
        </AppShell>,
      )

      const list = getByRole('navigation', {
        name: zhCN.appShell.navLabel,
      }).querySelector('ul')
      expect(list).not.toBeNull()
      expect(getComputedStyle(list as Element).paddingTop).toBe('0px')
    })

    it('keeps the list inset in the topbar shape, where no rule separates it', () => {
      mockMatchMedia(true)
      const { getByRole } = renderWithProviders(
        <AppShell navItems={NAV_ITEMS} header={<span>Speed Admin</span>}>
          content
        </AppShell>,
      )

      const list = getByRole('navigation', {
        name: zhCN.appShell.navLabel,
      }).querySelector('ul')
      expect(list).not.toBeNull()
      expect(getComputedStyle(list as Element).paddingTop).toBe('8px')
    })

    it('renders no app bar and no banner landmark at md+, keeping the brand before and the actions after the nav landmark inside the drawer paper', () => {
      mockMatchMedia(true)
      const { queryByRole, getByRole, getByText } = renderWithProviders(
        <AppShell
          shellLayout="sidebar"
          navItems={NAV_ITEMS}
          header={<span>Speed Admin</span>}
          headerActions={<button type="button">Search</button>}
          userMenu={<button type="button">Account</button>}
        >
          content
        </AppShell>,
      )

      expect(queryByRole('banner')).toBeNull()
      expect(
        getByRole('navigation', { name: zhCN.appShell.navLabel }),
      ).toBeInTheDocument()
      expect(getByRole('main')).toBeInTheDocument()

      const nav = getByRole('navigation', { name: zhCN.appShell.navLabel })
      const brand = getByText('Speed Admin')
      const search = getByRole('button', { name: 'Search' })
      const account = getByRole('button', { name: 'Account' })

      const region = getByRole('complementary', {
        name: zhCN.appShell.sidebarRegionLabel,
      })
      expect(region).toContainElement(brand)
      expect(region).toContainElement(search)
      expect(region).toContainElement(account)
      expect(paper()).toContainElement(region)
      expect(nav.contains(brand)).toBe(false)
      expect(
        brand.compareDocumentPosition(nav) & Node.DOCUMENT_POSITION_FOLLOWING,
      ).toBeTruthy()
      expect(nav.contains(search)).toBe(false)
      expect(nav.contains(account)).toBe(false)
      expect(
        search.compareDocumentPosition(nav) & Node.DOCUMENT_POSITION_PRECEDING,
      ).toBeTruthy()
      expect(
        account.compareDocumentPosition(nav) & Node.DOCUMENT_POSITION_PRECEDING,
      ).toBeTruthy()
    })

    it('renders no fixed head and no spacer when a desktop sidebar shell has no banner', () => {
      mockMatchMedia(true)
      const { container, getByRole } = renderWithProviders(
        <AppShell shellLayout="sidebar" navItems={NAV_ITEMS}>
          content
        </AppShell>,
      )

      const root = container.firstElementChild
      expect(root?.children).toHaveLength(3)

      expect(paper().querySelector(`.${toolbarClasses.root}`)).toBeNull()
      expect(
        getByRole('main').querySelector(`.${toolbarClasses.root}`),
      ).toBeNull()
    })

    it('keeps the banner in the fixed head and reserves its height in main and the drawer paper', () => {
      mockMatchMedia(true)
      const { queryByRole, getByTestId, getByRole } = renderWithProviders(
        <AppShell
          shellLayout="sidebar"
          navItems={NAV_ITEMS}
          banner={<p data-testid="notice">Impersonating</p>}
        >
          content
        </AppShell>,
      )

      const notice = getByTestId('notice')
      const head = notice.parentElement?.parentElement ?? null
      expect(head).not.toBeNull()
      expect((head as HTMLElement).children[0]).toBe(notice.parentElement)
      expect(getComputedStyle(head as Element).position).toBe('fixed')
      expect(queryByRole('banner')).toBeNull()

      expect(
        getByRole('main').querySelector(`.${toolbarClasses.root}`),
      ).not.toBeNull()
      expect(
        paper().querySelector(`.${toolbarClasses.root}`),
      ).not.toBeNull()
    })

    it('places the banner below the minimal top bar below md', () => {
      mockMatchMedia(false)
      const { getByTestId, getByRole } = renderWithProviders(
        <AppShell
          shellLayout="sidebar"
          navItems={NAV_ITEMS}
          header={<span>Speed Admin</span>}
          banner={<p data-testid="notice">Impersonating</p>}
        >
          content
        </AppShell>,
      )
      const notice = getByTestId('notice')
      const bar = getByRole('banner')
      expect(
        bar.compareDocumentPosition(notice) & Node.DOCUMENT_POSITION_FOLLOWING,
      ).toBeTruthy()
      const head = notice.parentElement?.parentElement ?? null
      expect(head).not.toBeNull()
      expect((head as HTMLElement).children[0]).toBe(bar)
      expect((head as HTMLElement).children[1]).toBe(notice.parentElement)
    })

    it('renders a minimal top bar below md with the trigger and the brand, and puts the actions in the open drawer, with the brand appearing once', async () => {
      mockMatchMedia(false)
      const user = userEvent.setup()
      const { getByRole, queryByRole, getAllByText } = renderWithProviders(
        <AppShell
          shellLayout="sidebar"
          navItems={NAV_ITEMS}
          header={<span>Speed Admin</span>}
          headerActions={<button type="button">Search</button>}
          userMenu={<button type="button">Account</button>}
        >
          content
        </AppShell>,
      )

      expect(getByRole('banner')).toBeInTheDocument()
      expect(getByRole('button', { name: zhCN.appShell.openNav })).toBeInTheDocument()
      expect(getAllByText('Speed Admin')).toHaveLength(1)
      expect(queryByRole('button', { name: 'Search' })).toBeNull()
      expect(queryByRole('button', { name: 'Account' })).toBeNull()

      await user.click(getByRole('button', { name: zhCN.appShell.openNav }))

      const drawerPaper = paper()
      expect(drawerPaper).toContainElement(
        getByRole('button', { name: 'Search' }),
      )
      expect(drawerPaper).toContainElement(
        getByRole('button', { name: 'Account' }),
      )
      expect(getAllByText('Speed Admin')).toHaveLength(1)
      expect(
        getByRole('button', { name: zhCN.appShell.closeNav, hidden: true }),
      ).toHaveAttribute('aria-expanded', 'true')
    })

    it('keeps the collapse toggle and the bottom actions named in the collapsed rail', () => {
      mockMatchMedia(true)
      const { getByRole } = renderWithProviders(
        <AppShell
          shellLayout="sidebar"
          navItems={NAV_ITEMS}
          sidebarCollapsed
          onSidebarCollapsedChange={() => undefined}
          headerActions={<button type="button">Search</button>}
          userMenu={<button type="button">Account</button>}
        >
          content
        </AppShell>,
      )
      expect(
        getByRole('button', { name: zhCN.appShell.expandNav }),
      ).toBeInTheDocument()
      expect(getByRole('button', { name: 'Search' })).toBeInTheDocument()
      expect(getByRole('button', { name: 'Account' })).toBeInTheDocument()
    })

    it('tells the header actions and the user menu whether they render in the collapsed rail', () => {
      mockMatchMedia(true)
      const slotState = (state: { readonly narrow: boolean }) => (
        <button type="button">{state.narrow ? 'icon form' : 'text form'}</button>
      )
      const collapsed = renderWithProviders(
        <AppShell
          shellLayout="sidebar"
          navItems={NAV_ITEMS}
          sidebarCollapsed
          onSidebarCollapsedChange={() => undefined}
          headerActions={slotState}
          userMenu={slotState}
        >
          content
        </AppShell>,
      )
      expect(collapsed.getAllByRole('button', { name: 'icon form' })).toHaveLength(2)
      collapsed.unmount()

      const expanded = renderWithProviders(
        <AppShell
          shellLayout="sidebar"
          navItems={NAV_ITEMS}
          sidebarCollapsed={false}
          onSidebarCollapsedChange={() => undefined}
          headerActions={slotState}
          userMenu={slotState}
        >
          content
        </AppShell>,
      )
      expect(expanded.getAllByRole('button', { name: 'text form' })).toHaveLength(2)
    })

    it('floors the collapsed rail at the width its contents need under a smaller spacing base', () => {
      mockMatchMedia(true)
      renderWithProviders(
        <AppShell
          shellLayout="sidebar"
          navItems={NAV_ITEMS}
          sidebarCollapsed
          onSidebarCollapsedChange={() => undefined}
        >
          content
        </AppShell>,
        { projectTokens: { spacing: { unit: 6 } } },
      )
      expect(emittedStyleText()).toMatch(
        new RegExp(
          `\\.${drawerClasses.paper}\\{[^}]*width:max\\(42px,\\s*53px\\)`,
        ),
      )
    })

    it('resets the measured head height when the layout changes, then re-measures the mounted head', () => {
      mockMatchMedia(true)
      const resizeObserver = stubResizeObserver()
      const banner = <p data-testid="notice">Impersonating</p>
      const { getByTestId, getByRole, rerender } = renderWithProviders(
        <AppShell shellLayout="topbar" navItems={NAV_ITEMS} banner={banner}>
          content
        </AppShell>,
      )
      const head = getByTestId('notice').parentElement?.parentElement ?? null
      expect(head).not.toBeNull()
      resizeObserver.emitHeightFor(head as Element, 160)
      expect(
        getByRole('main').querySelector(`.${toolbarClasses.root}`),
      ).toHaveStyle({ minHeight: '160px' })

      rerender(
        <AppShell shellLayout="sidebar" navItems={NAV_ITEMS} banner={banner}>
          content
        </AppShell>,
      )
      const mainSpacer = getByRole('main').querySelector<HTMLElement>(
        `.${toolbarClasses.root}`,
      )
      expect(mainSpacer).not.toBeNull()
      expect((mainSpacer as HTMLElement).style.minHeight).toBe('')

      resizeObserver.emitHeightFor(head as Element, 96)
      expect(
        (
          getByRole('main').querySelector<HTMLElement>(
            `.${toolbarClasses.root}`,
          ) as HTMLElement
        ).style.minHeight,
      ).toBe('96px')
    })

    it('resets the measured head height when the viewport crosses the desktop breakpoint', () => {
      const media = mockMatchMedia(true)
      const resizeObserver = stubResizeObserver()
      const banner = <p data-testid="notice">Impersonating</p>
      const { getByTestId, getByRole } = renderWithProviders(
        <AppShell navItems={NAV_ITEMS} banner={banner}>
          content
        </AppShell>,
      )
      const head = getByTestId('notice').parentElement?.parentElement ?? null
      expect(head).not.toBeNull()
      resizeObserver.emitHeightFor(head as Element, 160)
      expect(
        getByRole('main').querySelector(`.${toolbarClasses.root}`),
      ).toHaveStyle({ minHeight: '160px' })

      act(() => {
        media.changeMatches(false)
      })

      const mainSpacer = getByRole('main').querySelector<HTMLElement>(
        `.${toolbarClasses.root}`,
      )
      expect(mainSpacer).not.toBeNull()
      expect((mainSpacer as HTMLElement).style.minHeight).toBe('')
    })

    it('has no axe violations on the desktop sidebar layout, with region enabled', async () => {
      mockMatchMedia(true)
      renderWithProviders(
        <AppShell
          shellLayout="sidebar"
          navItems={NAV_ITEMS}
          header={<span>Speed Admin</span>}
          headerActions={<button type="button">Search</button>}
          userMenu={<button type="button">Account</button>}
        >
          <h1>Main content</h1>
        </AppShell>,
      )
      await expectNoAxeViolations()
    })

    it('has no axe violations on the open sidebar drawer below md', async () => {
      mockMatchMedia(false)
      const user = userEvent.setup()
      const { getByRole } = renderWithProviders(
        <AppShell
          shellLayout="sidebar"
          navItems={NAV_ITEMS}
          header={<span>Speed Admin</span>}
          headerActions={<button type="button">Search</button>}
          userMenu={<button type="button">Account</button>}
        >
          <h1>Main content</h1>
        </AppShell>,
      )
      await user.click(getByRole('button', { name: zhCN.appShell.openNav }))
      await expectNoAxeViolations({
        decidedInBrowser: 'appShellSidebarDrawer',
      })
    })
  })

  describe('drawer column and pinned footer', () => {
    const drawerPaper = (): HTMLElement => {
      const element = document.querySelector<HTMLElement>(
        `.${drawerClasses.paper}`,
      )
      if (element === null) {
        throw new Error('expected a mounted drawer paper')
      }
      return element
    }

    const drawerColumn = (paper: HTMLElement): HTMLElement => {
      const column = paper.querySelector<HTMLElement>('aside')
      if (column === null) {
        throw new Error('expected the drawer paper column')
      }
      return column
    }

    const expectPinnedFooterColumn = (
      paper: HTMLElement,
      nav: HTMLElement,
      footer: HTMLElement,
    ): void => {
      expect(getComputedStyle(paper).overflow).toBe('hidden')

      const navDeclarations = emittedDeclarations(nav)
      expect(navDeclarations).toMatch(/flex:1 1 auto/)
      expect(navDeclarations).toMatch(/min-height:0(?:px)?/)
      expect(navDeclarations).toMatch(/overflow-y:auto/)

      expect(drawerColumn(paper)).toHaveStyle({
        display: 'flex',
        flexDirection: 'column',
        height: '100%',
        minHeight: '0px',
      })

      expect(nav.nextElementSibling).toBe(footer)
      expect(drawerColumn(paper).lastElementChild).toBe(footer)
    }

    for (const layout of ['topbar', 'sidebar'] as const) {
      it(`keeps the ${layout} permanent paper a fixed column whose nav scrolls and whose footer sits at the bottom`, () => {
        mockMatchMedia(true)
        const { getByRole, getByTestId } = renderWithProviders(
          <AppShell
            shellLayout={layout}
            navItems={NAV_ITEMS}
            navFooter={<div data-testid="nav-footer">Footer</div>}
          >
            content
          </AppShell>,
        )
        expectPinnedFooterColumn(
          drawerPaper(),
          getByRole('navigation', { name: zhCN.appShell.navLabel }),
          getByTestId('nav-footer'),
        )
      })
    }

    it('gives the mobile temporary drawer the same fixed column and pinned footer', async () => {
      mockMatchMedia(false)
      const user = userEvent.setup()
      const { getByRole, getByTestId } = renderWithProviders(
        <AppShell
          navItems={NAV_ITEMS}
          navFooter={<div data-testid="nav-footer">Footer</div>}
        >
          content
        </AppShell>,
      )
      await user.click(getByRole('button', { name: zhCN.appShell.openNav }))
      expectPinnedFooterColumn(
        drawerPaper(),
        getByRole('navigation', { name: zhCN.appShell.navLabel }),
        getByTestId('nav-footer'),
      )
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
      expect(skipLink.getAttribute('href')).toBe(`#${main.id}`)
      main.focus()
      expect(document.activeElement).toBe(main)
    })

    it('activates the skip link without touching the host hash and moves focus into main', async () => {
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

      expect(window.location.hash).toBe('#/notes')
      expect(document.activeElement).toBe(main)
    })

    it('keeps the skip link first in tab order even when the banner slot holds a focusable control', async () => {
      mockMatchMedia(true)
      const user = userEvent.setup()
      const { getByRole } = renderWithProviders(
        <AppShell
          navItems={NAV_ITEMS}
          banner={<button type="button">End impersonation</button>}
        >
          content
        </AppShell>,
      )
      await user.tab()
      expect(
        getByRole('link', { name: zhCN.appShell.skipToContent }),
      ).toHaveFocus()
    })

    it('relabels the nav landmark and toggle button when the language switches to en-US', async () => {
      mockMatchMedia(false)
      const { getByRole, i18n } = renderWithProviders(
        <AppShell navItems={NAV_ITEMS}>content</AppShell>,
      )
      await act(async () => {
        await switchLanguage(i18n, 'en-US')
      })
      expect(getByRole('navigation', { hidden: true })).toHaveAttribute(
        'aria-label',
        enUS.appShell.navLabel,
      )
      expect(getByRole('button', { name: enUS.appShell.openNav })).toBeInTheDocument()
    })
  })

  describe('responsive protections', () => {
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
      expect(emittedStyleText()).toMatch(
        new RegExp(
          `\\.${drawerClasses.paper}\\{box-sizing:border-box;width:280px;overflow:hidden;\\}`,
        ),
      )
    })

    it('fills its Drawer paper with the nav content instead of re-declaring sidebarWidth, so the content can never outgrow the mobile paper\'s capped width', () => {
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
      const toolbar = container.querySelector(`.${toolbarClasses.root}`)
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
      mockMatchMedia(true)
      renderWithProviders(
        <AppShell
          navItems={[{ id: 'home', label: 'Home', href: '/home' }]}
          onSidebarCollapsedChange={() => undefined}
        >
          content
        </AppShell>,
      )
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
      expect(resizeObserver.observedElement()).toBe(
        getByRole('banner').parentElement,
      )

      resizeObserver.emitHeight(112)

      const main = getByRole('main')
      const mainSpacer = main.querySelector(`.${toolbarClasses.root}`)
      expect(mainSpacer).not.toBeNull()
      expect(mainSpacer).toHaveStyle({ minHeight: '112px' })
      expect((mainSpacer as HTMLElement).style.minHeight).toBe('112px')

      const paper = document.querySelector(`.${drawerClasses.paper}`)
      expect(paper).not.toBeNull()
      const drawerSpacer = paper?.querySelector(`.${toolbarClasses.root}`) ?? null
      expect(drawerSpacer).not.toBeNull()
      expect(drawerSpacer).toHaveStyle({ minHeight: '112px' })
      expect((drawerSpacer as HTMLElement).style.minHeight).toBe('112px')

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

    const headOf = (notice: Element): HTMLElement => {
      const slot = notice.parentElement
      const head = slot?.parentElement ?? null
      if (slot === null || head === null) {
        throw new Error('expected the banner inside the fixed head')
      }
      return head
    }

    it('places the banner below the app bar inside one fixed shell head', () => {
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
      expect(
        appBar.compareDocumentPosition(notice) &
          Node.DOCUMENT_POSITION_FOLLOWING,
      ).toBeTruthy()
      const head = headOf(notice)
      expect(head).toContainElement(appBar)
      expect(head).toContainElement(notice)
      expect(head.children[0]).toBe(appBar)
      expect(head.children[1]).toBe(notice.parentElement)

      const headStyle = getComputedStyle(head)
      expect(headStyle.position).toBe('fixed')
      expect(headStyle.top).toBe('0px')
      expect(headStyle.left).toBe('0px')
      expect(headStyle.right).toBe('0px')
      expect(headStyle.display).toBe('flex')
      expect(headStyle.flexDirection).toBe('column')
      expect(appBar).toHaveClass(appBarClasses.positionStatic)

      const slot = getComputedStyle(notice.parentElement as Element)
      expect(slot.padding).toMatch(/^0(px)?$/)
      expect(slot.borderWidth).toMatch(/^0(px)?$/)
      expect(slot.minHeight).toBe('auto')

      const { theme } = createAppTheme()
      const zIndex = Number(headStyle.zIndex)
      expect(zIndex).toBe(theme.zIndex.drawer + 1)
      expect(zIndex).toBeLessThan(theme.zIndex.modal)
      const paper = document.querySelector(`.${drawerClasses.paper}`)
      expect(paper).not.toBeNull()
      expect(zIndex).toBeGreaterThan(
        Number(getComputedStyle(paper as Element).zIndex),
      )
    })

    it('measures the one fixed head container and reserves that height in every spacer', () => {
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
      const head = headOf(getByTestId('notice'))
      expect(resizeObserver.observedElements()).toEqual([head])
      expect(resizeObserver.instances()).toHaveLength(1)

      expect((getByRole('banner') as HTMLElement).style.top).toBe('')

      resizeObserver.emitHeightFor(head, 160)

      const mainSpacer = getByRole('main').querySelector(`.${toolbarClasses.root}`)
      expect(mainSpacer).not.toBeNull()
      expect(mainSpacer).toHaveStyle({ minHeight: '160px' })
      const paper = document.querySelector(`.${drawerClasses.paper}`)
      expect(paper).not.toBeNull()
      const drawerSpacer = paper?.querySelector(`.${toolbarClasses.root}`) ?? null
      expect(drawerSpacer).not.toBeNull()
      expect(drawerSpacer).toHaveStyle({ minHeight: '160px' })
      expect(getComputedStyle(paper as Element).top).not.toBe('160px')
      const nav = paper?.querySelector('nav') ?? null
      expect(nav).not.toBeNull()
      expect(
        (drawerSpacer as Node).compareDocumentPosition(nav as Node) &
          Node.DOCUMENT_POSITION_FOLLOWING,
      ).toBeTruthy()
    })

    it('writes the measured head height as an inline style, out of reach of the theme toolbar breakpoint rule', () => {
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
      const head = headOf(getByTestId('notice'))
      resizeObserver.emitHeightFor(head, 160)

      const mainSpacer =
        getByRole('main').querySelector<HTMLElement>(`.${toolbarClasses.root}`)
      expect(mainSpacer).not.toBeNull()
      expect((mainSpacer as HTMLElement).style.minHeight).toBe('160px')
      const drawerSpacer = document.querySelector<HTMLElement>(
        `.${drawerClasses.paper} .${toolbarClasses.root}`,
      )
      expect(drawerSpacer).not.toBeNull()
      expect(drawerSpacer?.style.minHeight).toBe('160px')
    })

    it('reserves the head height in the temporary drawer at narrow widths', () => {
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
      const head = headOf(getByTestId('notice'))
      const drawerSpacer =
        document.querySelector(`.${drawerClasses.paper} .${toolbarClasses.root}`)
      expect(drawerSpacer).not.toBeNull()

      resizeObserver.emitHeightFor(head, 104)

      expect(
        getByRole('main').querySelector(`.${toolbarClasses.root}`),
      ).toHaveStyle({ minHeight: '104px' })
      expect(drawerSpacer).toHaveStyle({ minHeight: '104px' })
    })

    it('keeps the plain theme toolbar spacer where no ResizeObserver exists', () => {
      mockMatchMedia(true)
      expect(typeof ResizeObserver).toBe('undefined')
      const { getByRole, getByTestId } = renderWithProviders(
        <AppShell
          navItems={NAV_ITEMS}
          banner={<p data-testid="notice">Impersonating</p>}
        >
          content
        </AppShell>,
      )
      const head = headOf(getByTestId('notice'))
      expect(head.children[0]).toBe(getByRole('banner'))
      expect(head.children[1]).not.toBeUndefined()
      const mainSpacer =
        getByRole('main').querySelector<HTMLElement>(`.${toolbarClasses.root}`)
      expect(mainSpacer).not.toBeNull()
      expect((mainSpacer as HTMLElement).style.minHeight).toBe('')
      expect(getComputedStyle(mainSpacer as Element).marginTop).toMatch(/^0(px)?$/)
    })

    it('drops the banner slot and re-reserves the head when the banner leaves after being measured', () => {
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
      const head = headOf(getByTestId('notice'))
      resizeObserver.emitHeightFor(head, 160)
      expect(
        getByRole('main').querySelector(`.${toolbarClasses.root}`),
      ).toHaveStyle({ minHeight: '160px' })

      rerender(<AppShell navItems={NAV_ITEMS}>content</AppShell>)
      expect(queryByTestId('notice')).not.toBeInTheDocument()
      expect(getByRole('banner').parentElement).toBe(head)
      expect(head.children).toHaveLength(1)
      resizeObserver.emitHeightFor(head, 112)
      expect(
        getByRole('main').querySelector(`.${toolbarClasses.root}`),
      ).toHaveStyle({ minHeight: '112px' })
    })

    it('keeps one observer on the fixed head for the component lifetime and disconnects on unmount', () => {
      mockMatchMedia(true)
      const resizeObserver = stubResizeObserver()
      const { getByTestId, unmount, rerender } = renderWithProviders(
        <AppShell
          navItems={NAV_ITEMS}
          banner={<p data-testid="notice">Impersonating</p>}
        >
          content
        </AppShell>,
      )
      const head = headOf(getByTestId('notice'))
      expect(resizeObserver.observedElements()).toEqual([head])
      expect(resizeObserver.instances()).toHaveLength(1)

      const other = <p data-testid="other">Other notice</p>
      rerender(
        <AppShell navItems={NAV_ITEMS} banner={other}>
          content
        </AppShell>,
      )
      expect(resizeObserver.observedElements()).toEqual([head])
      expect(resizeObserver.instances()).toHaveLength(1)

      unmount()
      expect(resizeObserver.observedElements()).toHaveLength(0)
      expect(
        resizeObserver.instances().every((instance) => instance.disconnected),
      ).toBe(true)
    })

    it('adds no height of its own when the host node renders empty', () => {
      mockMatchMedia(true)
      const resizeObserver = stubResizeObserver()
      const { getByTestId, getByRole } = renderWithProviders(
        <AppShell navItems={NAV_ITEMS} banner={<span data-testid="empty" />}>
          content
        </AppShell>,
      )
      const head = headOf(getByTestId('empty'))
      resizeObserver.emitHeightFor(head, 112)
      expect(
        getByRole('main').querySelector(`.${toolbarClasses.root}`),
      ).toHaveStyle({ minHeight: '112px' })
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
      expect(getByTestId('notice')).toBe(notice)
      expect(screen.getByText('route b')).toBeInTheDocument()
      expect(screen.queryByText('route a')).not.toBeInTheDocument()
    })

    it('renders no slot container when the slot is absent, null or false', () => {
      mockMatchMedia(true)
      for (const banner of [undefined, null, false] as const) {
        const { container, unmount } = renderWithProviders(
          <AppShell navItems={NAV_ITEMS} banner={banner}>
            content
          </AppShell>,
        )
        const root = container.firstElementChild
        expect(root?.children).toHaveLength(4)
        expect(root?.firstElementChild?.tagName).toBe('A')
        const head = root?.children[1]
        expect(head?.children).toHaveLength(1)
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

    it('keeps the skip link first and puts header controls before banner controls in tab order', async () => {
      mockMatchMedia(false)
      const user = userEvent.setup()
      const { getByRole } = renderWithProviders(
        <AppShell
          navItems={NAV_ITEMS}
          banner={<button type="button">End impersonation</button>}
        >
          content
        </AppShell>,
      )
      await user.tab()
      expect(
        getByRole('link', { name: zhCN.appShell.skipToContent }),
      ).toHaveFocus()
      await user.tab()
      expect(
        getByRole('button', { name: zhCN.appShell.openNav }),
      ).toHaveFocus()
      await user.tab()
      expect(
        getByRole('button', { name: 'End impersonation' }),
      ).toHaveFocus()
    })
  })

  describe('navigation groups', () => {
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
      expect(getByRole('button', { name: 'Overview' })).toHaveAttribute(
        'aria-expanded',
        'true',
      )
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
      expect(queryByRole('link', { name: 'Users' })).not.toBeInTheDocument()
      expect(queryByRole('link', { name: 'Roles' })).not.toBeInTheDocument()
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
      expect(getByRole('button', { name: 'Identity' })).toHaveAttribute(
        'aria-expanded',
        'true',
      )
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
      mockMatchMedia(false)
      const user = userEvent.setup()
      const { getByRole } = renderWithProviders(
        <AppShell navItems={NAV_ITEMS}>
          <h1>Main content</h1>
        </AppShell>,
      )
      await user.click(getByRole('button', { name: zhCN.appShell.openNav }))
      expect(
        document
          .querySelector(`.${drawerClasses.paper}`)
          ?.getAttribute('aria-modal'),
      ).toBe('true')
      await expectNoAxeViolations({
        decidedInBrowser: 'appShellMobileDrawer',
      })
    })
  })
})
