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
import { AppShell } from './AppShell.js'
import type { AppShellNavItem } from './AppShell.js'
import { renderWithProviders } from '../../test-utils/render.js'
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
function renderShell(ui: ReactElement) {
  return renderWithProviders(ui, { language: 'en-US' })
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
  const spacer = main.querySelector<HTMLElement>(':scope > .MuiToolbar-root')
  if (spacer === null) {
    throw new Error('AppShell: main holds no spacer Toolbar')
  }
  return spacer
}

/** The spacer inside the mounted drawer paper. */
function drawerSpacer(): HTMLElement {
  const paper = document.querySelector<HTMLElement>('.MuiDrawer-paper')
  if (paper === null) {
    throw new Error('AppShell: no drawer paper is mounted')
  }
  const spacer = paper.querySelector<HTMLElement>('.MuiToolbar-root')
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
              .querySelector<HTMLElement>('.MuiDrawer-paper')
              ?.getBoundingClientRect().width,
          ).toBeCloseTo(DEFAULT_SIDEBAR_WIDTH, 0)
        } else {
          expect(menuButton).not.toBeNull()
          // The temporary paper is capped to min(sidebarWidth, 85vw).
          const paperWidth =
            document.querySelector<HTMLElement>('.MuiDrawer-paper')
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

    const paper = document.querySelector<HTMLElement>('.MuiDrawer-paper')
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
          .querySelector<HTMLElement>('.MuiDrawer-paper')
          ?.getBoundingClientRect().width ?? 0,
      ).toBeGreaterThan(0)
    })

    const paperRect =
      document.querySelector<HTMLElement>('.MuiDrawer-paper')?.getBoundingClientRect() ??
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
    const paper = document.querySelector<HTMLElement>('.MuiDrawer-paper')
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
          .querySelector<HTMLElement>('.MuiDrawer-paper')
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
