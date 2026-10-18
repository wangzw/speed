/**
 * The horizontal-overflow invariant shared by the console list pages'
 * browser cases.
 *
 * Each list page renders one or more DataTables. The table container is
 * the component's stated scroll fallback, but the pages also declare
 * per-column priorities and content caps so that, at the sm/md/lg floor
 * widths, the columns shed instead of scrolling: the page must not
 * overflow, and neither may the table's own container. Only a browser
 * resolves container queries, font metrics and the actual column widths,
 * which is why the check lives in the browser tier.
 *
 * The check waits until the document's font set is ready before
 * measuring, so the widths come from the settled fonts rather than a
 * mid-load fallback; it waits on that event, never on a fixed sleep.
 */
import { expect } from 'vitest'
import { waitFor } from '@testing-library/react'

/** One pixel of tolerance for subpixel layout rounding. */
const TOLERANCE = 1

/**
 * Assert the document and every rendered table container keep their
 * content inside their own box horizontally.
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
}
