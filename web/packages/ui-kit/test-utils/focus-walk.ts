/**
 * The Tab walk the browser tier uses to prove a modal overlay holds focus.
 *
 * Only a browser can press Tab through a focus trap, and the background
 * `inert` flag is what keeps the tab order inside the overlay. The walk
 * presses Tab (or Shift+Tab) from the focused control until focus returns to
 * it, failing at the first step that lands outside `root`: on the page behind
 * the modal, or on `body`.
 */

import { expect } from 'vitest'

/** How the walk sends a key sequence; the case supplies its `userEvent`. */
export interface FocusWalkOptions {
  readonly press: (keys: string) => Promise<void>
}

const FOCUSABLE_SELECTOR = [
  'a[href]',
  'button',
  'input',
  'select',
  'textarea',
  '[tabindex]',
].join(', ')

export type FocusWalkDirection = 'forward' | 'backward'

const WALK_KEY: Readonly<Record<FocusWalkDirection, string>> = {
  forward: '{Tab}',
  backward: '{Shift>}{Tab}{/Shift}',
}

function isReachable(element: HTMLElement): boolean {
  return (
    !element.hasAttribute('disabled') &&
    !(element.tagName === 'INPUT' && element.getAttribute('type') === 'hidden') &&
    element.tabIndex >= 0 &&
    element.getClientRects().length > 0 &&
    window.getComputedStyle(element).visibility !== 'hidden'
  )
}

/** The controls inside `root` that the browser's Tab order reaches. */
function reachableControls(root: HTMLElement): readonly HTMLElement[] {
  return [...root.querySelectorAll<HTMLElement>(FOCUSABLE_SELECTOR)].filter(
    isReachable,
  )
}

function describeElement(element: Element | null): string {
  if (!(element instanceof HTMLElement)) {
    return String(element)
  }
  const label = (element.getAttribute('aria-label') ?? element.textContent ?? '')
    .replace(/\s+/g, ' ')
    .trim()
  return label === ''
    ? element.tagName.toLowerCase()
    : `${element.tagName.toLowerCase()} "${label}"`
}

async function stepInside(
  root: HTMLElement,
  direction: FocusWalkDirection,
  step: number,
  { press }: FocusWalkOptions,
): Promise<Element> {
  await press(WALK_KEY[direction])
  const active = document.activeElement
  expect(
    active,
    `step ${step} of the ${direction} walk left focus on nothing`,
  ).not.toBeNull()
  expect(
    active,
    `step ${step} of the ${direction} walk landed on the document body`,
  ).not.toBe(document.body)
  expect(
    root.contains(active!),
    `step ${step} of the ${direction} walk left the overlay; focus is on ${describeElement(active)}`,
  ).toBe(true)
  return active!
}

/**
 * Walks `root`'s focus trap in `direction` until focus returns to the control
 * it started from, asserting at every step that focus stays inside `root`,
 * and that the cycle reaches every focusable control the overlay offers.
 */
export async function expectFocusTrapWalk(
  root: HTMLElement,
  direction: FocusWalkDirection,
  options: FocusWalkOptions,
): Promise<void> {
  const controls = reachableControls(root)
  expect(
    controls.length,
    'the overlay offers no focusable control to walk',
  ).toBeGreaterThan(0)

  const initial = document.activeElement
  expect(
    initial,
    `focus should start inside the overlay; it is ${describeElement(initial)}`,
  ).not.toBeNull()
  expect(
    root.contains(initial!),
    `focus should start inside the overlay; it is ${describeElement(initial)}`,
  ).toBe(true)

  // MUI normally focuses the first control on open. If it leaves focus on the
  // modal container instead, the cycle starts at the first control reached.
  let anchor = initial!
  if (!controls.includes(anchor as HTMLElement)) {
    anchor = await stepInside(root, direction, 0, options)
    expect(
      controls,
      `the walk reached ${describeElement(anchor)}, which is not a control of the overlay`,
    ).toContain(anchor)
  }
  const visited = new Set<Element>([anchor])

  for (let step = 1; step <= controls.length; step += 1) {
    const active = await stepInside(root, direction, step, options)
    visited.add(active)
    if (active === anchor) {
      break
    }
  }

  expect(
    document.activeElement,
    `the ${direction} walk did not return to its start within ${controls.length} steps`,
  ).toBe(anchor)
  for (const control of controls) {
    expect(
      visited.has(control),
      `the ${direction} walk never reached ${describeElement(control)}`,
    ).toBe(true)
  }
}
