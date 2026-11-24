/**
 * The shared background-inert primitives for MUI's modal overlays. They are
 * a hook rather than theme wiring because MUI's ModalManager only sets
 * `aria-hidden`, and because `inert` must come off in the layout cleanup of
 * the closing commit, before the focus trap restores focus.
 */

import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from 'react'
import type { Ref, RefObject, SyntheticEvent } from 'react'

const INERT_ATTRIBUTE = 'inert'

// When another overlay is still exiting in the opening commit, the portal
// that is opening has not mounted yet. Wait for it in bounded timer steps
// instead of marking the page the exiting overlay still has to restore focus
// into. A miswired ref falls back to the null-root behaviour after this.
const MOUNT_WAIT_STEPS = 5

// The tags MUI's modal manager leaves out of its aria-hidden sweep, plus a
// hidden input: none of them renders, so inert would be meaningless there.
const NON_RENDERING_TAGS: ReadonlySet<string> = new Set([
  'SCRIPT',
  'STYLE',
  'TEMPLATE',
  'LINK',
  'MAP',
  'META',
  'NOSCRIPT',
  'PICTURE',
  'COL',
  'COLGROUP',
  'PARAM',
  'SLOT',
  'SOURCE',
  'TRACK',
])

// Holds per element rather than a boolean: stacked overlays each own one, and
// the attribute comes off only when the last holder releases it.
const HOLDS = new WeakMap<Element, number>()

function isBackgroundCandidate(element: Element): boolean {
  if (NON_RENDERING_TAGS.has(element.tagName)) {
    return false
  }
  return !(
    element.tagName === 'INPUT' &&
    element.getAttribute('type') === 'hidden'
  )
}

function hold(element: Element, held: Set<Element>): void {
  const count = HOLDS.get(element) ?? 0
  // Already inert with no hold of ours: the host set it, so this overlay
  // neither records nor later removes it.
  if (count === 0 && element.hasAttribute(INERT_ATTRIBUTE)) {
    return
  }
  HOLDS.set(element, count + 1)
  held.add(element)
  if (count === 0) {
    element.setAttribute(INERT_ATTRIBUTE, '')
  }
}

function release(held: Set<Element>): void {
  for (const element of held) {
    const count = HOLDS.get(element) ?? 0
    if (count > 1) {
      HOLDS.set(element, count - 1)
      continue
    }
    HOLDS.delete(element)
    if (count === 1) {
      element.removeAttribute(INERT_ATTRIBUTE)
    }
  }
  held.clear()
}

/**
 * Marks every sibling of the overlay's path to `body` inert, the same scope
 * `assertBackgroundAriaHidden` checks. The ancestors themselves are not
 * marked, and neither is the overlay's own subtree.
 */
function collectBackground(
  body: HTMLElement,
  overlay: Element | null,
  held: Set<Element>,
): void {
  const keepChain = new Set<Element>()
  if (overlay !== null) {
    for (
      let current = overlay.parentElement;
      current !== null && current !== body;
      current = current.parentElement
    ) {
      keepChain.add(current)
    }
  }

  const visit = (parent: Element): void => {
    for (const child of parent.children) {
      if (child === overlay || !isBackgroundCandidate(child)) {
        continue
      }
      if (keepChain.has(child)) {
        visit(child)
        continue
      }
      hold(child, held)
    }
  }
  visit(body)
}

function isRendered(element: HTMLElement): boolean {
  const style = window.getComputedStyle(element)
  return style.display !== 'none' && style.visibility !== 'hidden'
}

// MUI renders the focus trap's start sentinel with `tabindex=0` while a modal
// is open and `-1` once `open` turns false, so a modal root still in the DOM
// during its exit transition reads as closing.
function isClosingModalRoot(root: Element): boolean {
  const sentinel = root.querySelector<HTMLElement>(
    '[data-testid="sentinelStart"]',
  )
  return sentinel !== null && sentinel.getAttribute('tabindex') === '-1'
}

// A node inside such an overlay is about to leave the document; it must not
// become a restore target.
function isInsideClosingOverlay(focused: Element): boolean {
  const root = focused.closest('.MuiModal-root')
  return root !== null && isClosingModalRoot(root)
}

// Another overlay closing in this same commit still has to restore focus into
// the page; marking the page inert now would swallow that restore. A closed
// keepMounted overlay is not rendered, so only one that is still on screen
// counts.
function hasShownClosingOverlay(): boolean {
  for (const root of document.querySelectorAll<HTMLElement>(
    '.MuiModal-root',
  )) {
    if (isRendered(root) && isClosingModalRoot(root)) {
      return true
    }
  }
  return false
}

/**
 * Marks everything outside an open modal overlay inert and hands focus back
 * when it closes.
 *
 * It applies in the layout phase and releases in the layout cleanup of the
 * closing commit, before MUI's focus trap restores focus, so a focus call on
 * the opener does not land on an inert target. When another overlay is still
 * exiting in the opening commit, this overlay's portal has not mounted yet,
 * so the hook waits for it in bounded timer steps and scopes the background
 * to that root; marking the page then would swallow the exiting overlay's
 * focus restore. A `keepMounted` overlay's portal is already in the document,
 * so it never takes the wait path. The wait path runs on zero-delay timers,
 * so a jsdom test that installs fake timers must advance them before the
 * background appears.
 *
 * The restore contract: the focused element at the opening commit is recorded
 * unless it sits inside an overlay that is itself closing, in which case it
 * is about to leave the document and is not a restore target. On close the
 * hook focuses that element if it is still connected, and does nothing when
 * no target was recorded or the target has left the document; focus then
 * follows whatever MUI's focus trap restores or the host places. {@link
 * useInertSelectMenu} clears its open state when its menu root leaves the
 * document, so a select unmounted while open also releases the background.
 */
export function useInertBackground(
  open: boolean,
  overlayRef: RefObject<HTMLElement | null>,
): void {
  // The element to hand focus back to. The focus trap's own record of it does
  // not survive the StrictMode simulated unmount (see the passive cleanup),
  // so the hook keeps its own copy.
  const restoreTarget = useRef<HTMLElement | null>(null)
  const captured = useRef(false)
  // The holds this layer currently owns; released in the layout cleanup.
  const held = useRef<Set<Element> | null>(null)

  useLayoutEffect(() => {
    if (!open || typeof document === 'undefined') {
      captured.current = false
      return
    }
    if (!captured.current) {
      // Capture at the opening commit: the opener is still focused there,
      // while a portal mounts a commit later (after MUI may have moved focus
      // inside). A node inside an overlay closing in this same commit is on
      // its way out, and one already inside this overlay has nothing to
      // restore to; neither is the restore target.
      const focused = document.activeElement
      const root = overlayRef.current
      if (!(root !== null && root.contains(focused))) {
        restoreTarget.current =
          focused instanceof HTMLElement &&
          focused !== document.body &&
          !isInsideClosingOverlay(focused)
            ? focused
            : null
      }
      captured.current = true
    }
    let cancelled = false
    const apply = (): void => {
      if (cancelled) {
        return
      }
      const next = new Set<Element>()
      collectBackground(document.body, overlayRef.current, next)
      held.current = next
    }
    if (overlayRef.current !== null || !hasShownClosingOverlay()) {
      // The common case: no other overlay is closing this commit, so the
      // background is decided now (with a null root, every body child is
      // background, as a non-keepMounted portal has not mounted yet).
      apply()
    } else {
      // An overlay is exiting and still has to restore focus into the page.
      // Wait for this overlay's own portal, then scope the background to it
      // instead of marking the page that restore must reach.
      let steps = 0
      const waitForRoot = (): void => {
        if (cancelled) {
          return
        }
        if (overlayRef.current !== null || steps >= MOUNT_WAIT_STEPS) {
          apply()
          return
        }
        steps += 1
        setTimeout(waitForRoot, 0)
      }
      setTimeout(waitForRoot, 0)
    }
    return () => {
      cancelled = true
      // Re-arm the capture. StrictMode's simulated unmount runs this cleanup,
      // then the passive cleanup that clears the record, then this effect
      // again; without re-arming, a component that mounts with `open` already
      // true never records an opener and has nothing to restore on close.
      captured.current = false
      if (held.current !== null) {
        release(held.current)
        held.current = null
      }
    }
  }, [open, overlayRef])

  useEffect(() => {
    if (!open) {
      return
    }
    return () => {
      const target = restoreTarget.current
      restoreTarget.current = null
      // StrictMode's simulated unmount runs the focus trap's cleanup while
      // the background is still inert. Its `nodeToRestore.focus()` is
      // silently dropped and the record cleared; the re-mount never
      // recaptures it because focus never left the overlay, so without this
      // the closing commit has nothing left to restore. Passive, because a
      // layout-phase restore fires before the trap's enforce-focus listener
      // is removed and is pulled back inside; a child's passive cleanup runs
      // first, so by here that listener is gone.
      if (target !== null && target.isConnected) {
        target.focus()
      }
    }
  }, [open])
}

/** Spread into a TextField's `slotProps.select` or onto a `Select`. */
export interface InertSelectMenuProps {
  readonly open: boolean
  readonly onOpen: (event: SyntheticEvent) => void
  readonly onClose: (event: SyntheticEvent) => void
  readonly MenuProps: { readonly ref: Ref<HTMLDivElement> }
}

/**
 * The controlled open state a `Select` needs so the hook can watch its menu.
 * The caller passes the returned props to the select; the callback ref
 * reaches the menu's modal root through `MenuProps` and also clears `open`
 * when that root leaves the document, which happens without an `onClose` when
 * the select unmounts while its menu is open.
 */
export function useInertSelectMenu(): InertSelectMenuProps {
  const [open, setOpen] = useState(false)
  const menuRef = useRef<HTMLDivElement>(null)
  useInertBackground(open, menuRef)

  const onOpen = useCallback(() => {
    setOpen(true)
  }, [])
  const onClose = useCallback(() => {
    setOpen(false)
  }, [])
  const setMenuRef = useCallback((node: HTMLDivElement | null) => {
    menuRef.current = node
    if (node === null) {
      // A portal reports null for the commit before its root mounts, so a
      // single null does not mean the menu is gone. A root that is still
      // absent once that commit's synchronous re-render has run belongs to a
      // select that unmounted while its menu was open; clear `open` so the
      // background hook releases.
      queueMicrotask(() => {
        if (menuRef.current === null) {
          setOpen((current) => (current ? false : current))
        }
      })
    }
  }, [])
  const menuProps = useMemo(() => ({ ref: setMenuRef }), [setMenuRef])

  return useMemo(
    () => ({ open, onOpen, onClose, MenuProps: menuProps }),
    [open, onOpen, onClose, menuProps],
  )
}
