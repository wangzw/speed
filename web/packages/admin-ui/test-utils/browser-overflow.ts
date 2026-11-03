/**
 * The width invariants shared by the console list pages' browser cases.
 *
 * Each list page renders one or more DataTables. The table container is
 * the component's stated scroll fallback, but the pages also declare
 * per-column priorities and content caps so that, at the sm/md/lg floor
 * widths, the columns shed instead of scrolling: the page must not
 * overflow, and neither may the table's own container. Only a browser
 * resolves container queries, font metrics and the actual column widths,
 * which is why the check lives in the browser tier.
 *
 * The second check is the layout design's per-tier rule: at each
 * breakpoint tier's floor width, the unbreakable widths of the columns
 * visible in that tier must not exceed the floor, so the primary identity
 * and the row actions stay on screen together before the scroll fallback
 * appears. The tiers are the table's own container widths (the priorities
 * are container queries), not the viewport's; the check therefore pins
 * each table's containment box to the floor rather than trusting the
 * page's chrome to place it there. A table narrower than the lowest floor
 * keeps only its always-visible columns and is not subject to this rule
 * (the design states that case explicitly).
 *
 * Both checks wait until the document's font set is ready before
 * measuring, so the widths come from the settled fonts rather than a
 * mid-load fallback; they wait on that event, never on a fixed sleep.
 */
import { expect } from 'vitest'
import { waitFor } from '@testing-library/react'

/** One pixel of tolerance for subpixel layout rounding. */
const TOLERANCE = 1

/**
 * The breakpoint tiers that have a floor width, on the shared scale
 * (@speed/tokens' breakpoints.values: sm:600/md:900/lg:1200). A table
 * container narrower than the lowest floor shows only its unprioritized
 * columns and is outside the per-tier rule.
 */
export const TIER_FLOORS = [600, 900, 1200] as const

/**
 * Assert the document and every rendered table container keep their
 * content inside their own box horizontally, and that each table fits
 * each tier's floor width with the columns that tier shows.
 */
export async function expectNoHorizontalOverflow(): Promise<void> {
  // Wait for the page's read to settle into its table -- an event wait,
  // not a sleep: the container mounts when the state it renders arrives.
  await waitFor(() => {
    expect(
      document.querySelectorAll('.MuiTableContainer-root').length,
      'the page rendered no table container',
    ).toBeGreaterThan(0)
  })
  await document.fonts.ready

  const containers = Array.from(
    document.querySelectorAll<HTMLElement>('.MuiTableContainer-root'),
  )

  for (const container of containers) {
    expect(
      container.scrollWidth,
      'a table container overflows its own width',
    ).toBeLessThanOrEqual(container.clientWidth + TOLERANCE)
  }

  expect(
    document.documentElement.scrollWidth,
    'the page overflows the viewport horizontally',
  ).toBeLessThanOrEqual(document.documentElement.clientWidth + TOLERANCE)

  // The per-tier rule, measured on the table's own containment box: the
  // root box above the scroll container declares
  // `container-type: inline-size`, so pinning its width to a tier floor
  // makes the container queries resolve that tier's visible-column set.
  for (const container of containers) {
    const containment = container.parentElement
    expect(containment).not.toBeNull()
    if (containment === null) continue
    expect(
      getComputedStyle(containment).containerType,
      'the table root is not the container-query ancestor',
    ).toBe('inline-size')

    const originalWidth = containment.style.width
    try {
      for (const floor of TIER_FLOORS) {
        containment.style.width = `${floor}px`
        // Force the box to update before reading the scroll extent.
        void containment.offsetWidth
        // A padding or border on the containment box would leave the
        // scroll container short of the floor, so the widths below would
        // describe a different tier than the one named.
        expect(
          Math.abs(container.clientWidth - floor),
          `the table container did not reach the ${floor}px tier floor`,
        ).toBeLessThanOrEqual(TOLERANCE)
        expect(
          container.scrollWidth,
          `the table's columns overflow the ${floor}px tier floor: ${visibleHeaders(container).join(', ')}`,
        ).toBeLessThanOrEqual(container.clientWidth + TOLERANCE)
      }
    } finally {
      containment.style.width = originalWidth
    }
  }
}

/** The visible column headers, for the failure message. */
function visibleHeaders(container: HTMLElement): string[] {
  return Array.from(container.querySelectorAll('thead th'))
    .filter((cell) => getComputedStyle(cell).display !== 'none')
    .map((cell) => `"${cell.textContent ?? ''}"`)
}
