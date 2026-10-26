/**
 * Step-switch focus: the focus transfer a channel makes when a flow step
 * replaces the surface the viewer was working in.
 *
 * A channel renders one surface at a time -- its own first fields, then
 * whatever step the flow parks, then the next one -- and the control that
 * had focus unmounts with the old surface. Without an explicit transfer
 * keyboard focus falls to the document body and a keyboard user starts
 * tabbing from the top of the page. This module moves focus to the new
 * step's first control instead (design-web-auth-ui.md's "步骤切换后焦点落在
 * 新步骤的首个控件").
 *
 * Two appearances are not switches and move nothing:
 *
 *  - the first surface a component renders replaced nothing, so a viewer
 *    who loaded the page keeps whatever focus they had;
 *  - on the callback route the first step after readFlow plays that same
 *    first-render role even though it arrives a request later, so
 *    SocialCallbackHandler opts it out. A later step the route parks
 *    replaced the step the viewer answered and does take focus.
 */

import { useEffect, useRef } from 'react'
import type { RefObject } from 'react'
import type { OpenFlowState } from './flow.js'

/** The elements that can lead a rendered step. Disabled controls are not
 * tab stops, a hidden input is never focusable, and an explicit -1 marks a
 * programmatic target rather than the first stop. */
const FOCUSABLE = [
  'a[href]',
  'button:not([disabled])',
  'input:not([disabled]):not([type="hidden"])',
  'select:not([disabled])',
  'textarea:not([disabled])',
  '[tabindex]:not([tabindex="-1"])',
].join(', ')

/** The first keyboard-operable control inside a rendered step, if it has
 * one. */
export function firstControl(container: HTMLElement): HTMLElement | null {
  for (const candidate of container.querySelectorAll<HTMLElement>(FOCUSABLE)) {
    if (candidate.hasAttribute('hidden')) {
      continue
    }
    if (candidate.getAttribute('aria-hidden') === 'true') {
      continue
    }
    return candidate
  }
  return null
}

/**
 * A stable identity for the step being rendered, so "a different step
 * replaced this one" is a value comparison. The sequence number changes
 * with every reply the flow parks; the state and kind keep a reply that
 * reuses a number distinguishable.
 */
export function stepKey(step: OpenFlowState | null): string | null {
  return step === null ? null : `${step.state}:${step.seq}:${step.next.kind}`
}

export interface StepFocusOptions {
  /**
   * Whether the first step the component sees after mounting takes focus.
   * Defaults to true. SocialCallbackHandler sets it false: its first step
   * arrives after readFlow and plays the first-render role.
   */
  readonly focusFirstStep?: boolean
}

/**
 * A ref for the container of the currently rendered surface. When the
 * step key changes to another step, the container's first control takes
 * focus after the commit that rendered it. A `null` key (no step) focuses
 * nothing.
 */
export function useStepFocus(
  key: string | null,
  options: StepFocusOptions = {},
): RefObject<HTMLDivElement | null> {
  const containerRef = useRef<HTMLDivElement>(null)
  const previousKey = useRef(key)
  // Whether a step was ever rendered before this one: the first one is
  // not a switch, whatever the component rendered while it was absent.
  const sawStep = useRef(key !== null)
  const { focusFirstStep = true } = options

  useEffect(() => {
    const changed = previousKey.current !== key
    previousKey.current = key
    if (!changed || key === null) {
      return
    }
    const firstStep = !sawStep.current
    sawStep.current = true
    if (firstStep && !focusFirstStep) {
      return
    }
    const container = containerRef.current
    if (container !== null) {
      firstControl(container)?.focus()
    }
  }, [key, focusFirstStep])

  return containerRef
}
