/**
 * The click judgement behind every in-app link this package produces.
 *
 * A link that navigates in-app must intercept only the click that was meant
 * to stay in the app: the main button, no modifier key, no new-tab or
 * download destination, and nothing already having handled the event. Every
 * other click is left untouched so the browser does what it normally does --
 * a modified click opens a tab or window, a `download` link saves a file.
 *
 * `@speed/layout-kit`'s `isPlainPrimaryClick` implements the same policy --
 * intercept only a click meant to stay in the app, leave every other click
 * to the browser -- and this package imports no sibling by design (see
 * AGENTS.md's "no sibling dependency" rule), so the two carry it as
 * independent implementations. They are not the same code and do not decide
 * every edge case alike:
 *
 * - this copy requires `event.button === 0`; layout-kit also treats an event
 *   carrying no button value as the primary button;
 * - this copy refuses every `target` except absent, empty or `_self`;
 *   layout-kit refuses only `_blank` (ASCII case-insensitive);
 * - this copy reads the anchor from `event.currentTarget` (the handler sits
 *   on the link); layout-kit resolves the nearest `a` from `event.target`
 *   (its handler may sit on a container, so a click on any child judges the
 *   same).
 *
 * A change to the policy is worth considering for both, but never on the
 * assumption that the two implementations are interchangeable.
 */

import type { MouseEvent, MouseEventHandler } from 'react'

/**
 * Whether a click is a plain primary click: the main button, no modifier
 * key, no `target` other than `_self`, no `download` attribute, and no
 * earlier handler having called `preventDefault`.
 */
export function isPlainPrimaryClick(event: MouseEvent<HTMLElement>): boolean {
  if (event.button !== 0) return false
  if (event.metaKey || event.altKey || event.ctrlKey || event.shiftKey) {
    return false
  }
  if (event.defaultPrevented) return false
  const target = event.currentTarget.getAttribute('target')
  if (target !== null && target !== '' && target !== '_self') return false
  if (event.currentTarget.hasAttribute('download')) return false
  return true
}

/**
 * The click handler for a link to `to`: a plain primary click becomes an
 * in-app navigation, every other click is left to the browser. Shared by
 * `useRouteLinkProps` and `useNavItems` so an anchor's own props and a
 * navigation entry's `onClick` cannot judge a click differently.
 */
export function createLinkClickHandler(
  to: string,
  navigate: (to: string) => void,
): MouseEventHandler<HTMLElement> {
  return (event) => {
    if (!isPlainPrimaryClick(event)) return
    event.preventDefault()
    navigate(to)
  }
}
