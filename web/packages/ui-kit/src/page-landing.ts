/** The landing rule for a page that is replaced as a whole. */

// @speed/route-kit keeps the same rule privately in src/navigation-focus.tsx;
// a change to either copy belongs in both, in one change.

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

function scopeElement(scope: ParentNode): Element | null {
  const candidate = scope as Partial<Element>
  return typeof candidate.matches === 'function' ? (scope as Element) : null
}

function htmlElementOf(element: Element): HTMLElement | null {
  const candidate = element as Partial<HTMLElement>
  return typeof candidate.focus === 'function' ? (element as HTMLElement) : null
}

/** The walk stops at `scope`, so a hidden ancestor above it does not suppress its content. */
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

function firstMain(scope: ParentNode): Element | null {
  const own = scopeElement(scope)
  if (own !== null && own.matches(MAIN_SELECTOR)) {
    return own
  }
  return scope.querySelector(MAIN_SELECTOR)
}

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
 * heading inside its main landmark, else that landmark, else the first
 * level-one heading in scope. A target that is not focusable gets
 * `tabindex="-1"` for as long as it holds focus. Returns the focused
 * element, or `null` when nothing qualifies or the focus call does not land.
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
