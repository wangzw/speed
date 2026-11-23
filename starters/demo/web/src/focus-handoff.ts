/**
 * The demo host's own bookkeeping around the page landing.
 *
 * The landing rule itself is `@speed/ui-kit`'s `focusPageLanding`. Two
 * questions stay in the host: whether a surface that replaced itself is what
 * took the operator's focus, and whether the element a landing chose is the
 * page's own heading or the landmark that stands in before the heading
 * arrives.
 */

const PAGE_HEADING_SELECTOR = 'h1, [role="heading"][aria-level="1"]'

/** Whether `element` is the level-one heading a page landing targets. */
export function isPageHeading(element: Element): boolean {
  return element.matches(PAGE_HEADING_SELECTOR)
}

/**
 * Whether a page swap is what took the operator's focus: focus on the body,
 * or on a node no longer in the document, fell with the swap, and so does
 * focus inside the surface the swap closes -- checked while that surface is
 * still in the document. A connected control elsewhere is a place the
 * operator chose, and the swap leaves it alone.
 */
export function focusFellWithPageSwap(
  closingSurface: HTMLElement | null,
): boolean {
  const active = document.activeElement
  if (active === null || active === document.body) {
    return true
  }
  if (!(active instanceof HTMLElement) || !active.isConnected) {
    return true
  }
  return closingSurface !== null && closingSurface.contains(active)
}
