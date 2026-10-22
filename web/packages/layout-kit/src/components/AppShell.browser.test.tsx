/**
 * AppShell in a real browser: the geometry and focus-order invariants
 * jsdom cannot answer.
 *
 * The unit suite proves the measured-spacer mechanism by feeding a
 * stubbed ResizeObserver a height. These cases measure the real thing in
 * Chromium: the fixed head's rendered height (app bar + banner), each
 * spacer placeholding exactly that height, the content immediately below
 * the head being hit by a point put there, and the skip link being the
 * first Tab stop. They run across the shell's three viewport classes --
 * narrow (temporary drawer), md (permanent drawer), lg -- and three
 * banner heights, because the head's height changes with both. A
 * collapsed sidebar case covers the narrow rail.
 *
 * Assertions are invariants with a one-pixel tolerance for subpixel
 * rounding, never pixel comparisons: the head height is rounded by the
 * component before it reaches the spacer.
 */
import type { ReactElement } from 'react'
import { describe, expect, it } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import { page, userEvent } from 'vitest/browser'
import { drawerClasses } from '@mui/material/Drawer'
import { toolbarClasses } from '@mui/material/Toolbar'
import { SidePanel } from '@speed/ui-kit'
import { AppShell } from './AppShell.js'
import type { AppShellNavItem } from './AppShell.js'
import { expectNoAxeViolations } from '../../test-utils/axe.js'
import { renderWithProviders } from '../../test-utils/render.js'
import type { LayoutKitRenderOptions } from '../../test-utils/render.js'
import enUS from '../locales/en-US.json' with { type: 'json' }

/** The shipped en-US app-shell strings the cases assert on. */
const SKIP_TO_CONTENT = enUS.appShell.skipToContent
const OPEN_NAV = enUS.appShell.openNav
const NAV_LABEL = enUS.appShell.navLabel

const VIEWPORT_HEIGHT = 900

/** AppShell's default sidebar width (private to the component). */
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

/** Render in en-US: the geometry stays Latin-only, so the pinned Arial /
 * Liberation Sans metrics are the only font participating. */
function renderShell(
  ui: ReactElement,
  options: Omit<LayoutKitRenderOptions, 'language' | 'i18n'> = {},
) {
  return renderWithProviders(ui, { language: 'en-US', ...options })
}

/** The banner slot's host content, with an explicit height: the head's
 * height is exactly app bar + this node. */
function Banner({ height }: { readonly height: number }) {
  return (
    <div data-testid="banner" style={{ height, backgroundColor: '#f0c040' }}>
      banner
    </div>
  )
}

/** The fixed head container: the AppBar's parent, one element holding the
 * app bar first and the banner after it. */
function fixedHead(): HTMLElement {
  const header = screen.getByRole('banner')
  const head = header.parentElement
  if (head === null) {
    throw new Error('AppShell: the app bar has no fixed head container')
  }
  return head
}

/** The spacer inside `main`: the first Toolbar child of the content
 * landmark. */
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

/** The spacer inside the mounted drawer paper. */
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

/** Wait for the ResizeObserver measurement to reach the spacer: the
 * inline height is the measured branch, so its presence is the event
 * these cases wait on. */
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

        // The slot renders exactly the host height, and the head is the
        // app bar plus the banner -- no padding or minimum height of its
        // own.
        expect(bannerRect.height).toBeCloseTo(bannerHeight, 0)
        expect(headRect.height).toBeCloseTo(
          headerRect.height + bannerRect.height,
          0,
        )

        // Every spacer placeholders the head's measured height.
        expect(spacerRect.height).toBeCloseTo(headRect.height, 0)
        expect(drawerSpacer().getBoundingClientRect().height).toBeCloseTo(
          headRect.height,
          0,
        )

        // A point just below the head hits the content itself, not the
        // fixed head overlaying it.
        const probe = screen.getByTestId('probe')
        const probeRect = probe.getBoundingClientRect()
        expect(probeRect.top).toBeGreaterThanOrEqual(headRect.bottom - 0.5)
        const hit = document.elementFromPoint(
          probeRect.left + probeRect.width / 2,
          headRect.bottom + 1,
        )
        expect(hit).not.toBeNull()
        expect(probe.contains(hit as Node)).toBe(true)

        // The viewport class selects the drawer variant: a menu button
        // only where the temporary drawer is mounted.
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
          // The temporary paper is capped to min(sidebarWidth, 85vw).
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
        <div style={{ height: 240 }}>content</div>
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
    // The rail is the theme spacing base times seven (56px), never zero:
    // the drawer stays a real nav landmark.
    expect(paper?.getBoundingClientRect().width).toBeGreaterThan(0)
    expect(paper?.getBoundingClientRect().width).toBeLessThan(
      DEFAULT_SIDEBAR_WIDTH,
    )
  })

  it('opens the temporary drawer with a paper capped by the narrow viewport', async () => {
    await page.viewport(400, VIEWPORT_HEIGHT)
    renderShell(
      <AppShell navItems={NAV_ITEMS} banner={<Banner height={40} />}>
        <div style={{ height: 240 }}>content</div>
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
        <div style={{ height: 240 }}>content</div>
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

    // Each slot renders exactly its host height, and the two are adjacent
    // to the landmark -- no wrapper gap between them.
    expect(headerRect.height).toBeCloseTo(40, 0)
    expect(footerRect.height).toBeCloseTo(48, 0)
    expect(headerRect.bottom).toBeCloseTo(navRect.top, 0)
    expect(footerRect.top).toBeCloseTo(navRect.bottom, 0)

    // Both live inside the drawer paper and inside its width, and neither
    // is part of the landmark.
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
        <div style={{ height: 240 }}>content</div>
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
    // The temporary drawer is never the collapsed rail.
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

  /** Enough entries to overflow the nav's share of the drawer height. */
  const SCROLLING_NAV_ITEMS: readonly AppShellNavItem[] = Array.from(
    { length: 60 },
    (_, index) => ({
      id: `item-${index}`,
      label: `Item ${index}`,
      href: `/item-${index}`,
    }),
  )

  /** The mounted drawer paper (one per shell render). */
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

  /** Icon-sized actions for the rail: a host control wider than the rail is
   * the host's own layout concern (the slot stacks vertically but AppShell
   * does not resize it). */
  const RAIL_CHROME = {
    // A role-bearing brand: the rail clips it visually, but a screen
    // reader must still find it. `getByRole` filters hidden elements, so
    // this fixture is what makes the visibility assertion discriminating.
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

    // No fixed head, so neither main nor its content is pushed down.
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

    // The paper is position: fixed, so the content scroll leaves it --
    // and the brand inside it -- exactly where it was.
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

      // The head holds the banner alone; its measured height is the
      // banner's height, and both spacers reserve exactly that.
      expect(bannerRect.top).toBeCloseTo(0, 0)
      expect(
        mainSpacerElement.getBoundingClientRect().height,
      ).toBeCloseTo(bannerHeight, 0)
      expect(drawerSpacer().getBoundingClientRect().height).toBeCloseTo(
        bannerHeight,
        0,
      )

      // Main's content starts exactly at the banner's bottom edge. The
      // drawer's content starts there too, one pixel lower only because
      // the drawer paper's own surface border sits inside its top edge --
      // the spacer above the top row is exactly the head height.
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
        <div style={{ height: 240 }}>content</div>
      </AppShell>,
    )

    const paper = drawerPaper()
    const paperRect = paper.getBoundingClientRect()
    const account = screen.getByRole('button', { name: 'Account' })
    const bottomRow = account.parentElement as HTMLElement
    const bottomRowRect = bottomRow.getBoundingClientRect()

    // The bottom row's edge is the paper's edge (the row, not the
    // button: the row carries the padding).
    expect(Math.abs(bottomRowRect.bottom - paperRect.bottom)).toBeLessThanOrEqual(1)
    expect(Math.abs(bottomRowRect.left - paperRect.left)).toBeLessThanOrEqual(1)
    expect(Math.abs(bottomRowRect.right - paperRect.right)).toBeLessThanOrEqual(1)

    // The nav fills everything between the top row and the bottom row,
    // and its own overflow is what scrolls -- not the paper.
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
        <div style={{ height: 240 }}>content</div>
      </AppShell>,
    )

    const paper = drawerPaper()
    const paperRect = paper.getBoundingClientRect()
    // The rail is the theme's 56px (its 1px divider included), wide
    // enough for a 40px control on either side of it.
    expect(paper.scrollWidth).toBeLessThanOrEqual(paper.clientWidth)
    expect(
      screen.getByRole('button', { name: enUS.appShell.expandNav }),
    ).toBeInTheDocument()
    // The brand is clipped by `overflow: hidden`, not removed: it stays in
    // the accessibility tree, so a screen-reader user can still identify
    // the product from the rail. A display:none / visibility:hidden /
    // aria-hidden implementation would drop it from `getByRole`.
    expect(
      screen.getByRole('link', { name: 'Speed Admin' }),
    ).toBeInTheDocument()
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
    // The focus ring a control draws when focused: a 2px outline offset by
    // 2px, so the rail has to clear that much on the sides the ring reaches.
    const RING_REACH = 4
    // Two controls at the touch-target minimum, as siblings: the shell
    // stacks them in the rail instead of laying them out in a row.
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
        <div style={{ height: 240 }}>content</div>
      </AppShell>,
      // A dense preset's spacing base: 7 x 6 = 42px, so the rail renders at
      // its floor rather than at the derived width.
      { projectTokens: { spacing: { unit: 6 } } },
    )

    const paper = drawerPaper()
    const paperRect = paper.getBoundingClientRect()
    expect(paper.scrollWidth).toBeLessThanOrEqual(paper.clientWidth)

    const [search, colour] = ['Search', 'Colour mode'].map((name) =>
      screen.getByRole('button', { name }).getBoundingClientRect(),
    )
    for (const rect of [search, colour]) {
      expect(rect.left - paperRect.left).toBeGreaterThanOrEqual(
        RING_REACH - 0.5,
      )
      expect(paperRect.right - rect.right).toBeGreaterThanOrEqual(
        RING_REACH - 0.5,
      )
    }
    // Stacked, not side by side: a row of two would put the second control
    // past the paper's right edge.
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

    // The minimal top bar carries the brand; the temporary drawer has no
    // top row, so the brand appears exactly once.
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

    await expectNoAxeViolations()
  })

  it('anchors the fixed head, the drawer paper and a side panel under backdrop-filtered surfaces', async () => {
    // The glass preset expresses its translucency through
    // `surface.*.backdropFilter`; a backdrop filter makes its element a
    // containing block for `position: fixed` descendants, so the shell's
    // own fixed surfaces are measured here rather than assumed. The
    // override writes the same token fields the preset does.
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

    // The fixed head stays pinned to the viewport's top edge at full
    // width -- the banner slot is its only child in this layout.
    const banner = screen.getByTestId('banner')
    const head = banner.parentElement?.parentElement as HTMLElement
    const headRect = head.getBoundingClientRect()
    expect(headRect.top).toBeCloseTo(0, 0)
    expect(headRect.left).toBeCloseTo(0, 0)
    expect(headRect.right).toBeCloseTo(window.innerWidth, 0)

    // The shell's permanent drawer paper is still anchored to the left
    // edge at its declared width.
    const paperRect = drawerPaper().getBoundingClientRect()
    expect(paperRect.left).toBeCloseTo(0, 0)
    expect(paperRect.width).toBeCloseTo(DEFAULT_SIDEBAR_WIDTH, 0)

    // The side panel (a fixed, right-anchored overlay rendered inside
    // main) still spans the viewport height and hugs the right edge.
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

    // Start from the document, not from whatever the previous case left
    // focused.
    ;(document.activeElement as HTMLElement | null)?.blur()
    document.body.focus()

    await userEvent.keyboard('{Tab}')

    const active = document.activeElement
    expect(active).not.toBeNull()
    expect(active?.getAttribute('role') ?? active?.tagName.toLowerCase()).toBe(
      'a',
    )
    expect(active?.textContent).toBe(SKIP_TO_CONTENT)
    // The skip link precedes every control inside the fixed head.
    expect(active?.getAttribute('href')).toMatch(/^#/)

    await userEvent.keyboard('{Enter}')
    await waitFor(() => {
      expect(document.activeElement).toBe(screen.getByRole('main'))
    })
  })
})
