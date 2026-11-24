import type { ReactElement } from 'react'
import { describe, expect, it } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import { page, userEvent } from 'vitest/browser'
import Button from '@mui/material/Button'
import IconButton from '@mui/material/IconButton'
import Typography from '@mui/material/Typography'
import { drawerClasses } from '@mui/material/Drawer'
import { toolbarClasses } from '@mui/material/Toolbar'
import { SidePanel } from '@speed/ui-kit'
import { AppShell } from './AppShell.js'
import type { AppShellNavItem } from './AppShell.js'
import { expectNoAxeViolations } from '../../test-utils/axe.js'
import { renderWithProviders } from '../../test-utils/render.js'
import type { LayoutKitRenderOptions } from '../../test-utils/render.js'
import enUS from '../locales/en-US.json' with { type: 'json' }

const SKIP_TO_CONTENT = enUS.appShell.skipToContent
const OPEN_NAV = enUS.appShell.openNav
const NAV_LABEL = enUS.appShell.navLabel

const VIEWPORT_HEIGHT = 900

const DEFAULT_SIDEBAR_WIDTH = 280

const VIEWPORTS = [
  { label: 'narrow (temporary drawer)', width: 480, desktop: false },
  { label: 'md (permanent drawer)', width: 900, desktop: true },
  { label: 'lg (permanent drawer)', width: 1280, desktop: true },
] as const

const BANNER_HEIGHTS = [40, 72, 120] as const

const NAV_ITEMS: readonly AppShellNavItem[] = [
  { id: 'home', label: 'Home', href: '/home', selected: true },
]

function renderShell(
  ui: ReactElement,
  options: Omit<LayoutKitRenderOptions, 'language' | 'i18n'> = {},
) {
  return renderWithProviders(ui, { language: 'en-US', ...options })
}

function Banner({ height }: { readonly height: number }) {
  return (
    <div data-testid="banner" style={{ height, backgroundColor: '#f0c040' }}>
      banner
    </div>
  )
}

function fixedHead(): HTMLElement {
  const header = screen.getByRole('banner')
  const head = header.parentElement
  if (head === null) {
    throw new Error('AppShell: the app bar has no fixed head container')
  }
  return head
}

function mainSpacer(): HTMLElement {
  const main = screen.getByRole('main')
  const spacer = main.querySelector<HTMLElement>(
    `:scope > .${toolbarClasses.root}`,
  )
  if (spacer === null) {
    throw new Error('AppShell: main holds no spacer Toolbar')
  }
  return spacer
}

function drawerSpacer(): HTMLElement {
  const paper = document.querySelector<HTMLElement>(
    `.${drawerClasses.paper}`,
  )
  if (paper === null) {
    throw new Error('AppShell: no drawer paper is mounted')
  }
  const spacer = paper.querySelector<HTMLElement>(
    `.${toolbarClasses.root}`,
  )
  if (spacer === null) {
    throw new Error('AppShell: the drawer paper holds no spacer Toolbar')
  }
  return spacer
}

async function waitForMeasuredSpacer(spacer: HTMLElement): Promise<void> {
  await waitFor(() => {
    expect(spacer.style.minHeight).not.toBe('')
  })
}

describe('AppShell geometry in Chromium', () => {
  for (const viewport of VIEWPORTS) {
    for (const bannerHeight of BANNER_HEIGHTS) {
      it(`keeps the content clear of the fixed head at ${viewport.label}, banner ${bannerHeight}px`, async () => {
        await page.viewport(viewport.width, VIEWPORT_HEIGHT)
        renderShell(
          <AppShell navItems={NAV_ITEMS} banner={<Banner height={bannerHeight} />}>
            <div
              data-testid="probe"
              style={{ height: 240, backgroundColor: '#40a0f0' }}
            >
              content
            </div>
          </AppShell>,
        )

        const head = fixedHead()
        const header = screen.getByRole('banner')
        const banner = screen.getByTestId('banner')
        const spacer = mainSpacer()
        await waitForMeasuredSpacer(spacer)

        const headRect = head.getBoundingClientRect()
        const headerRect = header.getBoundingClientRect()
        const bannerRect = banner.getBoundingClientRect()
        const spacerRect = spacer.getBoundingClientRect()

        expect(bannerRect.height).toBeCloseTo(bannerHeight, 0)
        expect(headRect.height).toBeCloseTo(
          headerRect.height + bannerRect.height,
          0,
        )

        expect(spacerRect.height).toBeCloseTo(headRect.height, 0)
        expect(drawerSpacer().getBoundingClientRect().height).toBeCloseTo(
          headRect.height,
          0,
        )

        const probe = screen.getByTestId('probe')
        const probeRect = probe.getBoundingClientRect()
        expect(probeRect.top).toBeGreaterThanOrEqual(headRect.bottom - 0.5)
        const hit = document.elementFromPoint(
          probeRect.left + probeRect.width / 2,
          headRect.bottom + 1,
        )
        expect(hit).not.toBeNull()
        expect(probe.contains(hit as Node)).toBe(true)

        const menuButton = screen.queryByRole('button', { name: OPEN_NAV })
        if (viewport.desktop) {
          expect(menuButton).toBeNull()
          expect(
            document
              .querySelector<HTMLElement>(`.${drawerClasses.paper}`)
              ?.getBoundingClientRect().width,
          ).toBeCloseTo(DEFAULT_SIDEBAR_WIDTH, 0)
        } else {
          expect(menuButton).not.toBeNull()
          const paperWidth =
            document.querySelector<HTMLElement>(`.${drawerClasses.paper}`)
              ?.getBoundingClientRect().width ?? 0
          expect(paperWidth).toBeLessThanOrEqual(viewport.width * 0.85 + 0.5)
        }
      })
    }
  }

  it('keeps the collapsed sidebar rail and its spacers in lockstep with the head', async () => {
    await page.viewport(900, VIEWPORT_HEIGHT)
    renderShell(
      <AppShell
        navItems={NAV_ITEMS}
        banner={<Banner height={72} />}
        sidebarCollapsed
        onSidebarCollapsedChange={() => undefined}
      >
        <div style={{ height: 240 }}>
            <h1>content</h1>
          </div>
      </AppShell>,
    )

    const head = fixedHead()
    const spacer = mainSpacer()
    await waitForMeasuredSpacer(spacer)

    const headRect = head.getBoundingClientRect()
    expect(spacer.getBoundingClientRect().height).toBeCloseTo(headRect.height, 0)
    expect(drawerSpacer().getBoundingClientRect().height).toBeCloseTo(
      headRect.height,
      0,
    )

    const paper = document.querySelector<HTMLElement>(
    `.${drawerClasses.paper}`,
  )
    expect(paper).not.toBeNull()
    expect(paper?.getBoundingClientRect().width).toBeGreaterThan(0)
    expect(paper?.getBoundingClientRect().width).toBeLessThan(
      DEFAULT_SIDEBAR_WIDTH,
    )
  })

  it('opens the temporary drawer with a paper capped by the narrow viewport', async () => {
    await page.viewport(400, VIEWPORT_HEIGHT)
    renderShell(
      <AppShell navItems={NAV_ITEMS} banner={<Banner height={40} />}>
        <div style={{ height: 240 }}>
            <h1>content</h1>
          </div>
      </AppShell>,
    )

    const head = fixedHead()
    const main = mainSpacer()
    await waitForMeasuredSpacer(main)

    const menuButton = screen.getByRole('button', { name: OPEN_NAV })
    await userEvent.click(menuButton)

    await waitFor(() => {
      expect(
        document
          .querySelector<HTMLElement>(`.${drawerClasses.paper}`)
          ?.getBoundingClientRect().width ?? 0,
      ).toBeGreaterThan(0)
    })

    const paperRect =
      document.querySelector<HTMLElement>(`.${drawerClasses.paper}`)?.getBoundingClientRect() ??
      null
    expect(paperRect).not.toBeNull()
    expect(paperRect?.width ?? 0).toBeLessThanOrEqual(400 * 0.85 + 0.5)
    expect(drawerSpacer().getBoundingClientRect().height).toBeCloseTo(
      head.getBoundingClientRect().height,
      0,
    )
  })

  it('keeps the temporary drawer focus trap holding on the mobile layout', async () => {
    await page.viewport(400, VIEWPORT_HEIGHT)
    renderShell(
      <AppShell navItems={NAV_ITEMS}>
        <h1>Main content</h1>
      </AppShell>,
    )

    await userEvent.click(screen.getByRole('button', { name: OPEN_NAV }))
    await waitFor(() => {
      expect(
        document
          .querySelector<HTMLElement>(`.${drawerClasses.paper}`)
          ?.getBoundingClientRect().width ?? 0,
      ).toBeGreaterThan(0)
    })
    await expectNoAxeViolations({
      decidedInBrowser: 'appShellMobileDrawer',
    })
  })
})

describe('AppShell drawer slots in Chromium', () => {
  it('stacks the header and footer slots immediately around the nav landmark in the permanent drawer', async () => {
    await page.viewport(900, VIEWPORT_HEIGHT)
    renderShell(
      <AppShell
        navItems={NAV_ITEMS}
        navHeader={<div data-testid="nav-header" style={{ height: 40 }}>header</div>}
        navFooter={<div data-testid="nav-footer" style={{ height: 48 }}>footer</div>}
      >
        <div style={{ height: 240 }}>
            <h1>content</h1>
          </div>
      </AppShell>,
    )

    const nav = screen.getByRole('navigation', { name: NAV_LABEL })
    const header = screen.getByTestId('nav-header')
    const footer = screen.getByTestId('nav-footer')
    const paper = document.querySelector<HTMLElement>(
    `.${drawerClasses.paper}`,
  )
    expect(paper).not.toBeNull()

    const headerRect = header.getBoundingClientRect()
    const navRect = nav.getBoundingClientRect()
    const footerRect = footer.getBoundingClientRect()
    const paperRect = (paper as HTMLElement).getBoundingClientRect()

    expect(headerRect.height).toBeCloseTo(40, 0)
    expect(footerRect.height).toBeCloseTo(48, 0)
    expect(headerRect.bottom).toBeCloseTo(navRect.top, 0)
    expect(footerRect.top).toBeCloseTo(navRect.bottom, 0)

    for (const rect of [headerRect, navRect, footerRect]) {
      expect(rect.left).toBeGreaterThanOrEqual(paperRect.left - 0.5)
      expect(rect.right).toBeLessThanOrEqual(paperRect.right + 0.5)
    }
    expect(nav.contains(header)).toBe(false)
    expect(nav.contains(footer)).toBe(false)
  })

  it('renders the slots in the open temporary drawer, where narrow is false, and closeDrawer closes it', async () => {
    await page.viewport(480, VIEWPORT_HEIGHT)
    renderShell(
      <AppShell
        navItems={NAV_ITEMS}
        navHeader={({ narrow }) => (
          <div data-testid="nav-header" style={{ height: 40 }}>
            {narrow ? 'rail' : 'full'}
          </div>
        )}
        navFooter={({ closeDrawer }) => (
          <button type="button" data-testid="nav-footer" onClick={closeDrawer}>
            Close
          </button>
        )}
      >
        <div style={{ height: 240 }}>
            <h1>content</h1>
          </div>
      </AppShell>,
    )

    await userEvent.click(screen.getByRole('button', { name: OPEN_NAV }))
    await waitFor(() => {
      expect(
        document
          .querySelector<HTMLElement>(`.${drawerClasses.paper}`)
          ?.getBoundingClientRect().width ?? 0,
      ).toBeGreaterThan(0)
    })

    const nav = screen.getByRole('navigation', { name: NAV_LABEL })
    const header = screen.getByTestId('nav-header')
    const footer = screen.getByTestId('nav-footer')
    expect(header.textContent).toBe('full')
    expect(header.getBoundingClientRect().bottom).toBeCloseTo(
      nav.getBoundingClientRect().top,
      0,
    )
    expect(footer.getBoundingClientRect().top).toBeCloseTo(
      nav.getBoundingClientRect().bottom,
      0,
    )

    await userEvent.click(footer)
    await waitFor(() => {
      expect(
        screen.getByRole('button', { name: OPEN_NAV }),
      ).toHaveAttribute('aria-expanded', 'false')
    })
  })
})

describe('AppShell sidebar layout in Chromium', () => {
  const GLASS_FILTER = 'blur(18px) saturate(180%)'

  const SCROLLING_NAV_ITEMS: readonly AppShellNavItem[] = Array.from(
    { length: 60 },
    (_, index) => ({
      id: `item-${index}`,
      label: `Item ${index}`,
      href: `/item-${index}`,
    }),
  )

  function drawerPaper(): HTMLElement {
    const paper = document.querySelector<HTMLElement>(
      `.${drawerClasses.paper}`,
    )
    if (paper === null) {
      throw new Error('AppShell: no drawer paper is mounted')
    }
    return paper
  }

  const SIDEBAR_CHROME = {
    header: <span>Speed Admin</span>,
    headerActions: <button type="button">Search</button>,
    userMenu: <button type="button">Account</button>,
  } as const

  const RAIL_CHROME = {
    header: <a href="/home">Speed Admin</a>,
    headerActions: (
      <button
        type="button"
        aria-label="Search"
        style={{ width: 32, height: 32, padding: 0 }}
      >
        S
      </button>
    ),
    userMenu: (
      <button
        type="button"
        aria-label="Account"
        style={{ width: 32, height: 32, padding: 0 }}
      >
        A
      </button>
    ),
  } as const

  it('starts the brand on the navigation icons’ drawn edge', async () => {
    await page.viewport(1280, VIEWPORT_HEIGHT)
    renderShell(
      <AppShell
        shellLayout="sidebar"
        navItems={[
          {
            id: 'home',
            label: 'Home',
            href: '/home',
            icon: (
              <svg viewBox="0 0 24 24" width={24} height={24}>
                <path d="M4 4h16v16H4z" />
              </svg>
            ),
          },
        ]}
        header={<span>Brand</span>}
      >
        <div />
      </AppShell>,
    )

    const brand = screen.getByText('Brand').getBoundingClientRect()
    const icon = screen
      .getByRole('navigation')
      .querySelector('svg') as SVGSVGElement | null
    expect(icon).not.toBeNull()
    const box = (icon as SVGSVGElement).getBoundingClientRect()
    const drawn = (icon as SVGSVGElement).querySelector('path') as SVGPathElement
    expect(brand.left).toBeCloseTo(box.left + drawn.getBBox().x, 0)
  })

  it('separates the brand row from the navigation below it', async () => {
    await page.viewport(1280, VIEWPORT_HEIGHT)
    renderShell(
      <AppShell shellLayout="sidebar" navItems={NAV_ITEMS} header={<span>Brand</span>}>
        <div />
      </AppShell>,
    )

    const brand = screen.getByText('Brand')
    const topRow = brand.parentElement?.parentElement as HTMLElement | null
    expect(topRow).not.toBeNull()
    const divider = (topRow as HTMLElement).nextElementSibling as HTMLElement | null
    expect(divider).not.toBeNull()
    const style = getComputedStyle(divider as HTMLElement)
    expect(style.borderBottomStyle).toBe('solid')
    expect(style.borderBottomWidth).toBe('1px')
    const nav = screen.getByRole('navigation')
    expect(
      nav.getBoundingClientRect().top -
        (divider as HTMLElement).getBoundingClientRect().bottom,
    ).toBeLessThanOrEqual(1)
  })

  it('starts main at the viewport top and keeps the sidebar in place while the content scrolls, with no banner', async () => {
    await page.viewport(1280, VIEWPORT_HEIGHT)
    renderShell(
      <AppShell shellLayout="sidebar" navItems={NAV_ITEMS} header={<span>Brand</span>}>
        <div
          data-testid="probe"
          style={{ height: 2400, backgroundColor: '#40a0f0' }}
        >
          content
        </div>
      </AppShell>,
    )

    expect(screen.getByRole('main').getBoundingClientRect().top).toBeCloseTo(
      0,
      0,
    )
    expect(
      screen.getByTestId('probe').getBoundingClientRect().top,
    ).toBeCloseTo(0, 0)

    const paper = drawerPaper()
    const paperBefore = paper.getBoundingClientRect()
    const brand = screen.getByText('Brand')
    const brandBefore = brand.getBoundingClientRect()

    window.scrollTo(0, 600)
    await waitFor(() => {
      expect(window.scrollY).toBeGreaterThan(0)
    })

    const paperAfter = paper.getBoundingClientRect()
    expect(paperAfter.top).toBeCloseTo(paperBefore.top, 0)
    expect(paperAfter.left).toBeCloseTo(paperBefore.left, 0)
    expect(brand.getBoundingClientRect().top).toBeCloseTo(brandBefore.top, 0)
    window.scrollTo(0, 0)
  })

  for (const bannerHeight of [40, 120] as const) {
    it(`reserves a ${bannerHeight}px banner at the top of both main and the drawer content`, async () => {
      await page.viewport(1280, VIEWPORT_HEIGHT)
      renderShell(
        <AppShell
          shellLayout="sidebar"
          navItems={NAV_ITEMS}
          header={<span>Brand</span>}
          banner={<Banner height={bannerHeight} />}
        >
          <div data-testid="probe" style={{ height: 240 }}>
            content
          </div>
        </AppShell>,
      )

      const bannerRect = screen.getByTestId('banner').getBoundingClientRect()
      const mainSpacerElement = mainSpacer()
      await waitForMeasuredSpacer(mainSpacerElement)

      expect(bannerRect.top).toBeCloseTo(0, 0)
      expect(
        mainSpacerElement.getBoundingClientRect().height,
      ).toBeCloseTo(bannerHeight, 0)
      expect(drawerSpacer().getBoundingClientRect().height).toBeCloseTo(
        bannerHeight,
        0,
      )

      expect(
        screen.getByTestId('probe').getBoundingClientRect().top,
      ).toBeCloseTo(bannerRect.bottom, 0)
      const brand = screen.getByText('Brand')
      const topRow = brand.parentElement?.parentElement ?? null
      expect(topRow).not.toBeNull()
      const topRowRect = (topRow as HTMLElement).getBoundingClientRect()
      expect(topRowRect.top).toBeGreaterThanOrEqual(bannerRect.bottom - 0.5)
      expect(topRowRect.top - bannerRect.bottom).toBeLessThanOrEqual(1.5)
      expect(topRowRect.top).toBeCloseTo(
        drawerSpacer().getBoundingClientRect().bottom,
        0,
      )
    })
  }

  it('pins the bottom row to the paper bottom while the nav takes the rest and scrolls itself', async () => {
    await page.viewport(1280, VIEWPORT_HEIGHT)
    renderShell(
      <AppShell
        shellLayout="sidebar"
        navItems={SCROLLING_NAV_ITEMS}
        {...SIDEBAR_CHROME}
      >
        <div style={{ height: 240 }}>
            <h1>content</h1>
          </div>
      </AppShell>,
    )

    const paper = drawerPaper()
    const paperRect = paper.getBoundingClientRect()
    const account = screen.getByRole('button', { name: 'Account' })
    const bottomRow = account.parentElement as HTMLElement
    const bottomRowRect = bottomRow.getBoundingClientRect()

    expect(Math.abs(bottomRowRect.bottom - paperRect.bottom)).toBeLessThanOrEqual(1)
    expect(Math.abs(bottomRowRect.left - paperRect.left)).toBeLessThanOrEqual(1)
    expect(Math.abs(bottomRowRect.right - paperRect.right)).toBeLessThanOrEqual(1)

    const nav = screen.getByRole('navigation', { name: NAV_LABEL })
    expect(getComputedStyle(nav).overflowY).toBe('auto')
    expect(nav.scrollHeight).toBeGreaterThan(nav.clientHeight)
    expect(nav.getBoundingClientRect().bottom).toBeCloseTo(
      bottomRowRect.top,
      0,
    )
    nav.scrollTop = 200
    expect(nav.scrollTop).toBeGreaterThan(0)
    expect(paper.scrollTop).toBe(0)
  })

  it('keeps the collapsed rail free of horizontal overflow with its bottom actions inside the paper', async () => {
    await page.viewport(1280, VIEWPORT_HEIGHT)
    renderShell(
      <AppShell
        shellLayout="sidebar"
        navItems={NAV_ITEMS}
        sidebarCollapsed
        onSidebarCollapsedChange={() => undefined}
        {...RAIL_CHROME}
      >
        <div style={{ height: 240 }}>
            <h1>content</h1>
          </div>
      </AppShell>,
    )

    const paper = drawerPaper()
    const paperRect = paper.getBoundingClientRect()
    expect(paper.scrollWidth).toBeLessThanOrEqual(paper.clientWidth)
    expect(
      screen.getByRole('button', { name: enUS.appShell.expandNav }),
    ).toBeInTheDocument()
    const brand = screen.getByRole('link', { name: 'Speed Admin' })
    expect(brand).toBeInTheDocument()
    expect(
      (brand.parentElement as HTMLElement).getBoundingClientRect().width,
    ).toBeCloseTo(0, 0)
    for (const control of [
      screen.getByRole('button', { name: 'Search' }),
      screen.getByRole('button', { name: 'Account' }),
    ]) {
      const rect = control.getBoundingClientRect()
      expect(rect.left).toBeGreaterThanOrEqual(paperRect.left - 0.5)
      expect(rect.right).toBeLessThanOrEqual(paperRect.right + 0.5)
    }
  })

  it('keeps the collapsed rail wide enough for its controls and their focus ring under a dense spacing base', async () => {
    await page.viewport(1280, VIEWPORT_HEIGHT)
    const RING_REACH = 4
    const control = (label: string) => (
      <button
        type="button"
        aria-label={label}
        style={{ width: 44, height: 44, padding: 0 }}
      >
        {label.slice(0, 1)}
      </button>
    )
    renderShell(
      <AppShell
        shellLayout="sidebar"
        navItems={NAV_ITEMS}
        sidebarCollapsed
        onSidebarCollapsedChange={() => undefined}
        header={<a href="/home">Speed Admin</a>}
        headerActions={
          <>
            {control('Search')}
            {control('Colour mode')}
          </>
        }
        userMenu={control('Account')}
      >
        <div style={{ height: 240 }}>
            <h1>content</h1>
          </div>
      </AppShell>,
      { projectTokens: { spacing: { unit: 6 } } },
    )

    const paper = drawerPaper()
    const paperRect = paper.getBoundingClientRect()
    expect(paper.scrollWidth).toBeLessThanOrEqual(paper.clientWidth)

    const search = screen
      .getByRole('button', { name: 'Search' })
      .getBoundingClientRect()
    const colour = screen
      .getByRole('button', { name: 'Colour mode' })
      .getBoundingClientRect()
    for (const rect of [search, colour]) {
      expect(rect.left - paperRect.left).toBeGreaterThanOrEqual(
        RING_REACH - 0.5,
      )
      expect(paperRect.right - rect.right).toBeGreaterThanOrEqual(
        RING_REACH - 0.5,
      )
    }
    expect(colour.top).toBeGreaterThanOrEqual(search.bottom - 0.5)
  })

  it('shows the brand once at 400px and pins the open drawer bottom row, with no accessibility violations', async () => {
    await page.viewport(400, VIEWPORT_HEIGHT)
    renderShell(
      <AppShell
        shellLayout="sidebar"
        navItems={NAV_ITEMS}
        {...SIDEBAR_CHROME}
      >
        <h1>Main content</h1>
      </AppShell>,
    )

    expect(screen.getAllByText('Speed Admin')).toHaveLength(1)

    await userEvent.click(screen.getByRole('button', { name: OPEN_NAV }))
    await waitFor(() => {
      expect(drawerPaper().getBoundingClientRect().width).toBeGreaterThan(0)
    })

    const paperRect = drawerPaper().getBoundingClientRect()
    const bottomRow = screen.getByRole('button', { name: 'Account' })
      .parentElement as HTMLElement
    expect(
      Math.abs(bottomRow.getBoundingClientRect().bottom - paperRect.bottom),
    ).toBeLessThanOrEqual(1)

    await expectNoAxeViolations({
      decidedInBrowser: 'appShellSidebarDrawer',
    })
  })

  it('keeps the sidebar minimal top bar on the theme gutter below md', async () => {
    await page.viewport(400, VIEWPORT_HEIGHT)
    renderShell(
      <AppShell
        shellLayout="sidebar"
        navItems={NAV_ITEMS}
        {...SIDEBAR_CHROME}
      >
        <h1>Main content</h1>
      </AppShell>,
    )

    const toggleRect = screen
      .getByRole('button', { name: OPEN_NAV })
      .getBoundingClientRect()
    const brandRect = screen.getByText('Speed Admin').getBoundingClientRect()
    expect(Math.abs(brandRect.left - toggleRect.right - 16)).toBeLessThanOrEqual(1)
  })

  it('anchors the fixed head, the drawer paper and a side panel under backdrop-filtered surfaces', async () => {
    await page.viewport(1280, VIEWPORT_HEIGHT)
    renderShell(
      <AppShell
        shellLayout="sidebar"
        navItems={NAV_ITEMS}
        header={<span>Brand</span>}
        banner={<Banner height={40} />}
      >
        <SidePanel
          open
          title="Task"
          onClose={() => undefined}
          onExited={() => undefined}
        >
          panel body
        </SidePanel>
      </AppShell>,
      {
        projectTokens: {
          surface: {
            container: { backdropFilter: GLASS_FILTER, opacity: 0.36 },
            overlay: { backdropFilter: GLASS_FILTER, opacity: 0.36 },
            header: { backdropFilter: GLASS_FILTER, opacity: 0.3 },
          },
        },
      },
    )

    const banner = screen.getByTestId('banner')
    const head = banner.parentElement?.parentElement as HTMLElement
    const headRect = head.getBoundingClientRect()
    expect(headRect.top).toBeCloseTo(0, 0)
    expect(headRect.left).toBeCloseTo(0, 0)
    expect(headRect.right).toBeCloseTo(window.innerWidth, 0)

    const paperRect = drawerPaper().getBoundingClientRect()
    expect(paperRect.left).toBeCloseTo(0, 0)
    expect(paperRect.width).toBeCloseTo(DEFAULT_SIDEBAR_WIDTH, 0)

    const panelPaper = document.querySelector<HTMLElement>(
      `.${drawerClasses.paper}[role="dialog"]`,
    )
    expect(panelPaper).not.toBeNull()
    await waitFor(() => {
      expect(
        (panelPaper as HTMLElement).getBoundingClientRect().width,
      ).toBeGreaterThan(0)
    })
    const panelRect = (panelPaper as HTMLElement).getBoundingClientRect()
    expect(panelRect.top).toBeCloseTo(0, 0)
    expect(panelRect.right).toBeCloseTo(window.innerWidth, 0)
    expect(panelRect.height).toBeCloseTo(VIEWPORT_HEIGHT, 0)
  })
})

const NARROW_HEADER_LABELS = {
  brand: 'Speed demo',
  notifications: 'Notifications',
  language: 'Change language',
  colourMode: 'Switch to dark theme',
  console: 'Console',
  tenant: 'Acme Corporation',
  account: 'Ada Lovelace',
} as const

function GlyphButton({
  label,
  glyph,
}: {
  readonly label: string
  readonly glyph: string
}): ReactElement {
  return (
    <IconButton color="inherit" aria-label={label}>
      <span aria-hidden="true">{glyph}</span>
    </IconButton>
  )
}

function narrowHeaderShell(): ReactElement {
  return (
    <AppShell
      navItems={NAV_ITEMS}
      header={
        <Typography
          data-testid="header-brand"
          noWrap
          variant="h6"
          component="div"
          sx={{ color: 'inherit', fontWeight: 600 }}
        >
          {NARROW_HEADER_LABELS.brand}
        </Typography>
      }
      headerActions={
        <>
          <GlyphButton label={NARROW_HEADER_LABELS.notifications} glyph="N" />
          <GlyphButton label={NARROW_HEADER_LABELS.language} glyph="L" />
          <GlyphButton label={NARROW_HEADER_LABELS.colourMode} glyph="C" />
          <Button color="inherit" href="/console">
            {NARROW_HEADER_LABELS.console}
          </Button>
        </>
      }
      userMenu={
        <>
          <Button color="inherit" variant="outlined">
            {NARROW_HEADER_LABELS.tenant}
          </Button>
          <GlyphButton label={NARROW_HEADER_LABELS.account} glyph="A" />
        </>
      }
    >
      <div style={{ height: 240 }}>
            <h1>content</h1>
          </div>
    </AppShell>
  )
}

const ROW_CENTER_TOLERANCE_PX = 4

function headerBands(): number {
  const header = screen.getByRole('banner')
  const toolbar = header.querySelector<HTMLElement>(`.${toolbarClasses.root}`)
  if (toolbar === null) {
    throw new Error('AppShell: the banner holds no Toolbar')
  }
  const centers = [
    ...toolbar.querySelectorAll<HTMLElement>(
      'button, a, [data-testid="header-brand"]',
    ),
  ]
    .map((element) => element.getBoundingClientRect())
    .filter((rect) => rect.width > 0 && rect.height > 0)
    .map((rect) => rect.top + rect.height / 2)
    .sort((left, right) => left - right)

  let bands = 0
  let bandCenter = Number.NEGATIVE_INFINITY
  for (const center of centers) {
    if (center - bandCenter > ROW_CENTER_TOLERANCE_PX) {
      bands += 1
      bandCenter = center
    }
  }
  return bands
}

describe('AppShell narrow header rows in Chromium', () => {
  for (const width of [375, 430] as const) {
    it(`keeps the tenant header within two rows at ${width}px with every control reachable`, async () => {
      await page.viewport(width, VIEWPORT_HEIGHT)
      renderShell(narrowHeaderShell())

      expect(headerBands(), `bands at ${width}`).toBeLessThanOrEqual(2)

      for (const name of [
        NARROW_HEADER_LABELS.notifications,
        NARROW_HEADER_LABELS.language,
        NARROW_HEADER_LABELS.colourMode,
        NARROW_HEADER_LABELS.tenant,
        NARROW_HEADER_LABELS.account,
      ]) {
        expect(screen.getByRole('button', { name })).toBeInTheDocument()
      }
      expect(
        screen.getByRole('link', { name: NARROW_HEADER_LABELS.console }),
      ).toBeInTheDocument()
    })
  }
})

describe('AppShell focus order in Chromium', () => {  it('makes the skip link the first Tab stop and focuses main when activated', async () => {
    await page.viewport(900, VIEWPORT_HEIGHT)
    renderShell(
      <AppShell
        navItems={NAV_ITEMS}
        header={<button type="button">header action</button>}
        banner={
          <div style={{ height: 72 }}>
            <button type="button">banner control</button>
          </div>
        }
      >
        <div>content</div>
      </AppShell>,
    )

    ;(document.activeElement as HTMLElement | null)?.blur()
    document.body.focus()

    await userEvent.keyboard('{Tab}')

    const active = document.activeElement
    expect(active).not.toBeNull()
    expect(active?.getAttribute('role') ?? active?.tagName.toLowerCase()).toBe(
      'a',
    )
    expect(active?.textContent).toBe(SKIP_TO_CONTENT)
    expect(active?.getAttribute('href')).toMatch(/^#/)

    await userEvent.keyboard('{Enter}')
    await waitFor(() => {
      expect(document.activeElement).toBe(screen.getByRole('main'))
    })
  })
})

function FooterProbe() {
  return (
    <div
      data-testid="nav-footer"
      style={{ height: 44, backgroundColor: '#808080' }}
    >
      footer
    </div>
  )
}

function footerGap(): number {
  const paper = document.querySelector<HTMLElement>(`.${drawerClasses.paper}`)
  const footer = screen.getByTestId('nav-footer')
  if (paper === null) {
    throw new Error('expected a drawer paper')
  }
  return paper.getBoundingClientRect().bottom - footer.getBoundingClientRect().bottom
}

describe('the drawer footer sticks to the bottom', () => {
  for (const layout of ['topbar', 'sidebar'] as const) {
    it(`keeps the footer at the paper's bottom in the ${layout} layout`, async () => {
      await page.viewport(1280, VIEWPORT_HEIGHT)
      renderShell(
        <AppShell
          shellLayout={layout}
          navItems={NAV_ITEMS}
          navFooter={<FooterProbe />}
        >
          <div style={{ height: 240 }}>
            <h1>content</h1>
          </div>
        </AppShell>,
      )
      await waitFor(() => expect(screen.getByTestId('nav-footer')).toBeInTheDocument())
      expect(footerGap()).toBeLessThanOrEqual(1)
      await expectNoAxeViolations()
    })
  }

  it('sticks in the collapsed rail', async () => {
    await page.viewport(1280, VIEWPORT_HEIGHT)
    renderShell(
      <AppShell
        shellLayout="sidebar"
        navItems={NAV_ITEMS}
        navFooter={<FooterProbe />}
        sidebarCollapsed
        onSidebarCollapsedChange={() => undefined}
      >
        <div style={{ height: 240 }}>
            <h1>content</h1>
          </div>
      </AppShell>,
    )
    await waitFor(() => expect(screen.getByTestId('nav-footer')).toBeInTheDocument())
    expect(footerGap()).toBeLessThanOrEqual(1)
  })

  it('sticks in the temporary drawer', async () => {
    await page.viewport(400, VIEWPORT_HEIGHT)
    const user = userEvent.setup()
    renderShell(
      <AppShell shellLayout="sidebar" navItems={NAV_ITEMS} navFooter={<FooterProbe />}>
        <div style={{ height: 240 }}>
            <h1>content</h1>
          </div>
      </AppShell>,
    )
    await user.click(screen.getByRole('button', { name: OPEN_NAV }))
    await waitFor(() => expect(screen.getByTestId('nav-footer')).toBeInTheDocument())
    expect(footerGap()).toBeLessThanOrEqual(1)
  })
})
