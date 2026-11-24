/**
 * Whether a click is a plain primary click: primary button (or no value), no
 * modifier, no `_blank`/`download` anchor, not already default-prevented.
 * `@speed/route-kit` keeps a stricter private variant; change both together.
 */

import type { MouseEvent as ReactMouseEvent } from 'react'

function anchorOf(target: EventTarget | null): Element | null {
  if (target === null) {
    return null
  }
  const element =
    typeof (target as Partial<Element>).closest === 'function'
      ? (target as Element)
      : ((target as Partial<Node>).parentElement ?? null)
  return element?.closest('a') ?? null
}

/** Judges a click as a plain primary activation that an in-app navigation link may intercept. */
export function isPlainPrimaryClick(
  event: ReactMouseEvent | MouseEvent,
): boolean {
  if (event.defaultPrevented) {
    return false
  }
  // An absent button value reads as the primary button, never as a non-primary one.
  const button = (event as { readonly button?: number }).button
  if (button !== undefined && button !== 0) {
    return false
  }
  if (event.ctrlKey || event.metaKey || event.shiftKey || event.altKey) {
    return false
  }
  const anchor = anchorOf(event.target)
  if (anchor === null) {
    return true
  }
  if (anchor.getAttribute('target')?.toLowerCase() === '_blank') {
    return false
  }
  if (anchor.hasAttribute('download')) {
    return false
  }
  return true
}
