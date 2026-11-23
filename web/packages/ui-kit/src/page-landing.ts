/**
 * The landing rule for a page that is replaced as a whole: an in-app
 * navigation or an authentication flip unmounts the old page, and without
 * a hand-off the browser leaves focus on the document body, so a keyboard
 * or screen-reader user starts again from the top.
 *
 * `focusPageLanding(scope)` hands focus to the new page itself:
 *
 * 1. The first `main` or `[role="main"]` in `scope`; when `scope` is
 *    itself one, that landmark.
 * 2. Inside the landmark, the first `h1` or
 *    `[role="heading"][aria-level="1"]`, skipping any candidate that sits
 *    in a `[hidden]`, `[aria-hidden="true"]` or `[inert]` subtree.
 * 3. No heading there: the landmark itself.
 * 4. No landmark in `scope`: the first level-one heading in `scope`,
 *    under the same suppression test.
 * 5. Neither: `null`, and the caller decides its fallback.
 * 6. A target with no `tabindex` attribute that is not natively focusable
 *    gets `tabindex="-1"`; the rule removes the attribute it set on the
 *    target's first `blur`, and leaves an attribute the target already
 *    carried alone (for example `AppShell`'s `main`). The target is then
 *    focused without `preventScroll`.
 *
 * The function returns the element that received focus. When nothing in
 * `scope` qualifies, or the focus call does not land (the target sits in a
 * `display: none` subtree, say), it returns `null`; in the latter case it
 * removes the temporary `tabindex` again, so a caller such as
 * `ProductShell` can fall back to focusing its own container.
 *
 * @speed/route-kit keeps a private copy of this rule in its own
 * `src/navigation-focus.tsx`, because that package imports no sibling
 * package. The two copies are the same rule clause for clause, the same
 * way `isPlainPrimaryClick` has two copies; a change to either lookup or
 * to the focus handling belongs in both, in one change.
 *
 * The temporary `tabindex` is what lets a heading take focus at all: an
 * `h1` is not focusable on its own. Removing it on the first blur keeps
 * the heading from staying a Tab stop or a place a mouse click can land
 * once the hand-off is done. An existing `tabindex` is the element
 * owner's choice and is not touched.
 *
 * `focus()` is called without `preventScroll`: the heading scrolls into
 * view, so the reader starts at the top of the new page, which is where a
 * load without the hand-off would have left them.
 */

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
 * Hands focus to the page landing inside `scope`: the first level-one
 * heading inside its main landmark, else that main landmark, else the
 * first level-one heading in scope. A target that is not focusable gets
 * tabindex="-1" for as long as it holds focus. Returns the element that
 * received focus, or null when nothing in scope qualifies or the focus
 * call does not land (focus is left where it was, and a temporary
 * tabindex is removed again).
 */
export function focusPageLanding(scope: ParentNode): HTMLElement | null {
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
