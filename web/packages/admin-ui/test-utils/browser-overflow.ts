/**
 * The width invariants for the list pages' browser cases: no page or table
 * overflow, and each table fits every tier floor.
 */
import { expect } from 'vitest'
import { waitFor } from '@testing-library/react'

/** One pixel of tolerance for subpixel layout rounding. */
const TOLERANCE = 1

/** The breakpoint tiers that have a floor width (sm/md/lg). */
export const TIER_FLOORS = [600, 900, 1200] as const

/** Assert the document and each table container keep their content inside their box. */
export async function expectNoHorizontalOverflow(): Promise<void> {
  // An event wait, not a sleep: the container mounts when its read settles.
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

  // The per-tier rule, measured on the table's own containment box: pinning its
  // width to a floor makes the container queries resolve that tier's columns.
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
        // A padding or border on the containment box would leave the scroll
        // container short of the floor, describing a different tier.
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

function visibleHeaders(container: HTMLElement): string[] {
  return Array.from(container.querySelectorAll('thead th'))
    .filter((cell) => getComputedStyle(cell).display !== 'none')
    .map((cell) => `"${cell.textContent ?? ''}"`)
}
