/**
 * Programmatic focus for a section card's heading, resolved through the section's
 * `aria-labelledby` and given a negative tabindex.
 */

import { useCallback, useRef } from 'react'
import type { RefCallback } from 'react'

export interface SectionHeadingFocus {
  /** Attach to the section card's root (`SectionCard` forwards the ref). */
  readonly sectionRef: RefCallback<HTMLElement>
  /** Move focus to the section's heading; a no-op with no heading mounted. */
  readonly focusHeading: () => void
}

function headingOf(section: HTMLElement | null): HTMLElement | null {
  const id = section?.getAttribute('aria-labelledby') ?? ''
  return id === '' ? null : document.getElementById(id)
}

export function useSectionHeadingFocus(): SectionHeadingFocus {
  const sectionRef = useRef<HTMLElement | null>(null)

  const setSection = useCallback<RefCallback<HTMLElement>>((element) => {
    sectionRef.current = element
    const heading = headingOf(element)
    if (heading !== null) {
      heading.tabIndex = -1
    }
  }, [])

  const focusHeading = useCallback(() => {
    headingOf(sectionRef.current)?.focus()
  }, [])

  return { sectionRef: setSection, focusHeading }
}

/**
 * Whether the focus a settled write started with is on its way out: it is on
 * the body, or still inside the confirmation playing its exit transition.
 */
export function removedControlHeldFocus(): boolean {
  const active = document.activeElement
  return (
    active === null ||
    active === document.body ||
    active.closest('[role="dialog"]') !== null
  )
}
