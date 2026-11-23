/**
 * The focus landing two in-place page swaps share.
 *
 * A surface that replaces itself without changing the URL -- the invitation
 * landing's embedded sign-in, the admin area's session gate -- unmounts the
 * control the operator was on. Focus then falls to the document body, and the
 * page that replaces it has to take focus: its reader entry, but only when
 * the swap is what took the focus. A place the operator moved to keeps it.
 *
 * The lookup and focus handling follow @speed/ui-kit's page-landing rule:
 * a suppressed subtree (`hidden`, `aria-hidden`, `inert`) is skipped, with
 * the scan bounded by the landmark it searches, and the temporary `tabindex`
 * a heading needs is removed on its first blur. The one host difference is
 * the final fallback: the package returns null, while this copy focuses the
 * container itself, because a gate whose pending view mounts no page entry
 * still has to keep focus out of the body. A change to either copy belongs in
 * both until this one moves onto the package's `focusPageLanding`.
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

/**
 * Whether `element` sits in a suppressed subtree, stopping at `boundary` so
 * an element that is itself inside one still contributes its own content.
 */
function isSuppressed(element: Element, boundary: Element): boolean {
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
function firstHeading(scope: HTMLElement): HTMLElement | null {
  if (scope.matches(HEADING_SELECTOR) && !isSuppressed(scope, scope)) {
    return scope
  }
  for (const candidate of scope.querySelectorAll<HTMLElement>(
    HEADING_SELECTOR,
  )) {
    if (isSuppressed(candidate, scope)) {
      continue
    }
    return candidate
  }
  return null
}

/** The first main landmark in `scope`, the scope itself included. */
function firstMain(scope: HTMLElement): HTMLElement | null {
  if (scope.matches(MAIN_SELECTOR)) {
    return scope
  }
  return scope.querySelector<HTMLElement>(MAIN_SELECTOR)
}

/**
 * The level-one heading the landing would choose inside `container`: the
 * first one in its main landmark, else the first one in the container. Null
 * when the container holds no visible heading yet -- the surface is still
 * waiting, and `focusPageEntry` falls back to the landmark or the container.
 */
export function findPageHeading(
  container: HTMLElement | null,
): HTMLElement | null {
  if (container === null) {
    return null
  }
  const main = firstMain(container)
  return main === null ? firstHeading(container) : firstHeading(main)
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
 * Moves focus to the page entry inside `container`: the first level-one
 * heading in its main landmark, else the main landmark, else the first
 * level-one heading in the container itself, else the container (a gate's
 * pending view mounts no main and no heading yet). A target that is not
 * natively focusable and carries no tabindex gets a temporary `-1`, removed
 * on its first blur so the hand-off leaves no Tab stop behind. Returns the
 * element that received focus, or null when focus does not land (it is left
 * where it was, and a temporary `tabindex` is removed again), so a caller
 * can tell whether the page's own entry had arrived or only the fallback
 * did.
 */
export function focusPageEntry(
  container: HTMLElement | null,
): HTMLElement | null {
  if (container === null) {
    return null
  }
  const main = firstMain(container)
  const target =
    (main === null ? firstHeading(container) : firstHeading(main)) ??
    main ??
    container
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
