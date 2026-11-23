/**
 * NavigationFocus -- hands focus to the new page after an in-app navigation.
 *
 * RouteProvider renders this component after `children`, so React runs the
 * page's and the shell's effects first: a page or a shell that placed focus
 * itself in the same commit wins, and the guard below sees that result. The
 * landing itself is the first level-one heading inside the new page's `main`
 * landmark, or that landmark when there is no heading -- the rule
 * `@speed/ui-kit` exposes as `focusPageLanding` (web/packages/ui-kit/src/
 * page-landing.ts). This package may not import a sibling, so the two are
 * independent implementations of one rule, clause for clause, like the
 * click judgement shared with layout-kit: a change to the rule in one must
 * be reviewed against the other in the same change. The scope is the
 * landmark: the heading lookup and its suppressed-subtree test stop at
 * `main`, so a `[hidden]`, `[aria-hidden="true"]` or `[inert]` ancestor
 * above the landmark does not suppress what is inside it.
 *
 * Why the heading and not the landmark: a screen reader announces the
 * heading as soon as focus arrives, and the next Tab reaches the first
 * control after it, so a keyboard user starts at the page rather than at the
 * brand and navigation chrome. A target with no `tabindex` gets `-1` for as
 * long as it holds focus, following the GOV.UK Frontend pattern, so a
 * heading does not become a tab stop after a mouse click.
 *
 * The provider's first location never moves focus: focus is at the document
 * start and a reader is reading the skip link and the landmarks. A REPLACE
 * before the document has received a trusted interaction is a load-time
 * redirect (an alias path, a tenant redirect), not a navigation the user
 * asked for, so it does not move focus either. "Interaction" means an event
 * the browser dispatched -- `pointerdown`, `keydown` or `click` with
 * `isTrusted` set; a synthetic event dispatched by a script is not one. The
 * listener is installed at mount, captures the first trusted event and then
 * removes itself.
 */

import { useEffect, useRef } from 'react'
import { useLocation, useNavigationType } from 'react-router-dom'

const MAIN_SELECTOR = 'main, [role="main"]'
const HEADING_SELECTOR = 'h1, [role="heading"][aria-level="1"]'
const SUPPRESSED_SELECTOR = '[hidden], [aria-hidden="true"], [inert]'
const NATIVELY_FOCUSABLE_SELECTOR = [
  'a[href]',
  'area[href]',
  'button',
  'input',
  'select',
  'textarea',
  'iframe',
  'summary',
  'object',
  'embed',
  'audio[controls]',
  'video[controls]',
  '[contenteditable]:not([contenteditable="false"])',
].join(',')

/**
 * The component RouteProvider renders after its children. It renders
 * nothing; its effects read the router and move focus.
 */
export function NavigationFocus(): null {
  const { key, pathname } = useLocation()
  const navigationType = useNavigationType()
  const firstCommit = useRef(true)
  const previousKey = useRef(key)
  const previousPathname = useRef(pathname)
  const interacted = useRef(false)

  useEffect(() => {
    const markInteraction = (event: Event): void => {
      // Only an event the browser dispatched counts as an interaction: a
      // synthetic event never reached the user.
      if (!event.isTrusted) return
      interacted.current = true
      removeInteractionListeners(markInteraction)
    }
    document.addEventListener('pointerdown', markInteraction, true)
    document.addEventListener('keydown', markInteraction, true)
    document.addEventListener('click', markInteraction, true)
    return () => {
      removeInteractionListeners(markInteraction)
    }
  }, [])

  useEffect(() => {
    if (firstCommit.current) {
      firstCommit.current = false
      previousKey.current = key
      previousPathname.current = pathname
      return
    }
    // location.key changes on every history entry, so an unchanged key means
    // this commit is a re-render, not a navigation. A navigation that only
    // touched the query string or the hash keeps the pathname: not a page
    // change.
    if (key === previousKey.current) return
    const pageChanged = pathname !== previousPathname.current
    previousKey.current = key
    previousPathname.current = pathname
    if (!pageChanged) return
    if (navigationType === 'REPLACE' && !interacted.current) return
    if (!shouldMoveFocus()) return
    focusPageLanding(document)
  }, [key, pathname, navigationType])

  return null
}

function removeInteractionListeners(listener: EventListener): void {
  document.removeEventListener('pointerdown', listener, true)
  document.removeEventListener('keydown', listener, true)
  document.removeEventListener('click', listener, true)
}

/**
 * Whether the navigation should move focus at all. Focus on `body`, on
 * nothing, or on an element the old page's unmount detached means the page
 * change took the focus with it; focus on the page's own `main` or outside it
 * (a nav link, a header button, a menu's trigger) is the chrome, not the
 * page. Focus already inside the new page's `main` was placed by the page or
 * the shell, and focus inside a modal dialog belongs to that dialog. A main
 * landmark that itself holds focus is the exception: a persistent shell
 * landmark (AppShell's `main`) lands focus on itself when a page has no
 * heading, and the next navigation must still move off it.
 */
function shouldMoveFocus(): boolean {
  const active = document.activeElement
  if (active === null || active === document.body || !document.contains(active)) {
    return true
  }
  if (active.closest('[aria-modal="true"]') !== null) return false
  const main = active.closest(MAIN_SELECTOR)
  return main === null || main === active
}

/** `scope` as an Element when it can match selectors (not a document or fragment). */
function scopeElement(scope: ParentNode): Element | null {
  const candidate = scope as Partial<Element>
  return typeof candidate.matches === 'function' ? (scope as Element) : null
}

/** An element the DOM lets `focus()` reach, or null for a non-HTML node. */
function htmlElementOf(element: Element): HTMLElement | null {
  const candidate = element as Partial<HTMLElement>
  return typeof candidate.focus === 'function' ? (element as HTMLElement) : null
}

/**
 * Whether `element` sits in a suppressed subtree, stopping at `scope` so
 * a scope that is itself inside one still contributes its own content.
 */
function isSuppressed(element: Element, scope: ParentNode): boolean {
  const boundary = scopeElement(scope)
  let node: Element | null = element
  while (node !== null) {
    if (node.matches(SUPPRESSED_SELECTOR)) {
      return true
    }
    if (node === boundary) {
      return false
    }
    node = node.parentElement
  }
  return false
}

/** The first unsuppressed level-one heading in `scope`, the scope included. */
function firstLandingHeading(scope: ParentNode): HTMLElement | null {
  const own = scopeElement(scope)
  if (
    own !== null &&
    own.matches(HEADING_SELECTOR) &&
    !isSuppressed(own, scope)
  ) {
    const self = htmlElementOf(own)
    if (self !== null) {
      return self
    }
  }
  for (const candidate of scope.querySelectorAll(HEADING_SELECTOR)) {
    if (isSuppressed(candidate, scope)) {
      continue
    }
    const heading = htmlElementOf(candidate)
    if (heading !== null) {
      return heading
    }
  }
  return null
}

/** The first main landmark in `scope`, the scope itself included. */
function firstMain(scope: ParentNode): Element | null {
  const own = scopeElement(scope)
  if (own !== null && own.matches(MAIN_SELECTOR)) {
    return own
  }
  return scope.querySelector(MAIN_SELECTOR)
}

/**
 * Removes the temporary `tabindex` this rule set, and its blur listener.
 * The attribute is removed only while it still holds the value the rule
 * wrote, so a value the element's owner set meanwhile survives.
 */
function clearTemporaryTabindex(
  target: HTMLElement,
  listener: EventListener,
): void {
  target.removeEventListener('blur', listener)
  if (target.getAttribute('tabindex') === '-1') {
    target.removeAttribute('tabindex')
  }
}

/**
 * The landing rule, private to this package and mirrored clause for clause
 * by ui-kit's `focusPageLanding`: the first visible level-one heading inside
 * the scope's first `main` landmark, else that landmark, else the scope's
 * first visible level-one heading, else nothing (focus stays where it was).
 * A target that cannot take focus (a `display: none` subtree) also yields
 * null, with the temporary `tabindex` removed again.
 */
function focusPageLanding(scope: ParentNode): HTMLElement | null {
  const main = firstMain(scope)
  const target =
    main === null
      ? firstLandingHeading(scope)
      : (firstLandingHeading(main) ?? htmlElementOf(main))
  if (target === null) {
    return null
  }
  let temporary: EventListener | null = null
  if (
    !target.hasAttribute('tabindex') &&
    !target.matches(NATIVELY_FOCUSABLE_SELECTOR)
  ) {
    // The target is a heading or a landmark: focusable only programmatically,
    // and only while it holds focus, so it does not join the tab order.
    target.setAttribute('tabindex', '-1')
    temporary = () => {
      if (target.getAttribute('tabindex') === '-1') {
        target.removeAttribute('tabindex')
      }
    }
    target.addEventListener('blur', temporary, { once: true })
  }
  target.focus()
  if (target.ownerDocument.activeElement !== target) {
    if (temporary !== null) {
      clearTemporaryTabindex(target, temporary)
    }
    return null
  }
  return target
}
