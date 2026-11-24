/**
 * Hand focus to the new page's first level-one heading (or its main) after an
 * in-app navigation. This mirrors ui-kit's `focusPageLanding`; a change to one
 * must be reviewed against the other.
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

export function NavigationFocus(): null {
  const { key, pathname } = useLocation()
  const navigationType = useNavigationType()
  const firstCommit = useRef(true)
  const previousKey = useRef(key)
  const previousPathname = useRef(pathname)
  const interacted = useRef(false)

  useEffect(() => {
    const markInteraction = (event: Event): void => {
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
    // An unchanged key means a re-render, not a navigation; a query/hash-only change keeps the pathname.
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

function shouldMoveFocus(): boolean {
  const active = document.activeElement
  if (active === null || active === document.body || !document.contains(active)) {
    return true
  }
  if (active.closest('[aria-modal="true"]') !== null) return false
  const main = active.closest(MAIN_SELECTOR)
  return main === null || main === active
}

function scopeElement(scope: ParentNode): Element | null {
  const candidate = scope as Partial<Element>
  return typeof candidate.matches === 'function' ? (scope as Element) : null
}

function htmlElementOf(element: Element): HTMLElement | null {
  const candidate = element as Partial<HTMLElement>
  return typeof candidate.focus === 'function' ? (element as HTMLElement) : null
}

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
    // Focusable only while it holds focus, so it does not join the tab order.
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
