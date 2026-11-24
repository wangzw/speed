const PAGE_HEADING_SELECTOR = 'h1, [role="heading"][aria-level="1"]'

export function isPageHeading(element: Element): boolean {
  return element.matches(PAGE_HEADING_SELECTOR)
}

/** Whether the swap lost the operator's focus: the active element is the body, gone, detached, or inside the closing surface. */
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
