/**
 * Whether a click is a plain primary click: an unmodified left-button
 * activation with no intent to open a new tab or download.
 *
 * This is the shared judgement for in-app navigation links. A link
 * component (or a host's own navigation row) intercepts a click -- calls
 * `preventDefault` and routes in place -- only when this returns true, so
 * a Ctrl/⌘ click, a middle click, a `target="_blank"` link and a
 * `download` link keep the browser's own behavior.
 *
 * True only for:
 * - the primary button (`0`), or an event carrying no button value;
 * - no ctrl, meta, shift or alt modifier;
 * - an activation whose nearest anchor has neither `target="_blank"`
 *   (matched ASCII case-insensitively, as browsers treat the keyword) nor
 *   a `download` attribute;
 * - an event not already default-prevented, so a handler that has claimed
 *   the activation is never overridden.
 *
 * The anchor is the clicked element's nearest `a` ancestor, so a click on
 * a child of the link is judged the same as one on the link itself.
 *
 * `@speed/admin-ui` consumes this function: its navigation entries and
 * in-page links judge a click with it before routing in place. `@speed/route-kit` keeps
 * a private variant by design: that package is independent of every
 * sibling package and must not import this one. The variant is stricter
 * than this function -- it requires `button` to be exactly `0` (an absent
 * value is rejected rather than read as the primary button) and rejects
 * every `target` other than absent, empty or `_self`, so `_top`, `_parent`
 * and named windows are never intercepted -- while this function rejects
 * only `target="_blank"` and reads an absent `button` as the primary
 * button. A change to either set of criteria belongs in both copies in
 * the same change.
 */

import type { MouseEvent as ReactMouseEvent } from 'react'

/**
 * The nearest anchor around the event's target, or null when the
 * activation did not happen inside one (or the target is not a DOM node).
 */
function anchorOf(target: EventTarget | null): Element | null {
  if (target === null) {
    return null
  }
  // A click's target is normally an Element; a text node reports its
  // containing element instead. Anything else (the document, a detached
  // event target) has no anchor to consult.
  const element =
    typeof (target as Partial<Element>).closest === 'function'
      ? (target as Element)
      : ((target as Partial<Node>).parentElement ?? null)
  return element?.closest('a') ?? null
}

/**
 * Judges a click as a plain primary activation that an in-app navigation
 * link may intercept. Accepts a React synthetic event or a native DOM
 * event; both carry the fields this reads.
 */
export function isPlainPrimaryClick(
  event: ReactMouseEvent | MouseEvent,
): boolean {
  if (event.defaultPrevented) {
    return false
  }
  // An event-like object without a button value is treated as the primary
  // button: the field is absent only outside a real dispatch, where the
  // native default is 0, and an absent value must not read as a
  // non-primary button.
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
