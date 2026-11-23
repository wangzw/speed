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
 * It takes only the focus the replaced step owned. A control removed from
 * the document leaves focus on the body -- that is also where a viewer who
 * never focused anything keeps it -- so those two states are the switch's
 * to use. Focus anywhere else is where the viewer moved it, and pulling it
 * away would undo their choice, so the switch leaves it alone.
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
  /**
   * The element that takes focus when a rendered step gives way to no step
   * -- a terminal notice or a failure page. Omitted, a `null` key takes no
   * focus, as before. The ownership rule is the same: only focus the
   * unmounted step held (which the browser drops on the body) is moved,
   * never focus the viewer placed elsewhere. A surface that was never a
   * step replaced nothing, so it moves no focus either.
   */
  readonly emptyTarget?: RefObject<HTMLElement | null>
}

/** Whether focus holds nowhere it was deliberately placed: the body, where
 * removing the focused control leaves it; the document element; or a node
 * no longer in the document. */
function focusWasDropped(): boolean {
  const active = document.activeElement
  return (
    active === null ||
    active === document.body ||
    active === document.documentElement ||
    !(active instanceof HTMLElement) ||
    !active.isConnected
  )
}

/**
 * A ref for the container of the currently rendered surface. When the
 * step key changes to another step, the container's first control takes
 * focus after the commit that rendered it. When a step gives way to no
 * step, `emptyTarget` takes the focus the unmounted control held.
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
  const { focusFirstStep = true, emptyTarget } = options

  useEffect(() => {
    const changed = previousKey.current !== key
    previousKey.current = key
    if (!changed) {
      return
    }
    if (key === null) {
      // A step gave way to a surface with no step. The heading that surface
      // carries is the landing for the focus the unmounted control held.
      // Only a transition from a step counts: a surface that was never a
      // step replaced nothing, and focus the viewer moved elsewhere (a
      // connected node other than the body) is left where it is.
      if (sawStep.current && focusWasDropped()) {
        emptyTarget?.current?.focus()
      }
      return
    }
    const firstStep = !sawStep.current
    sawStep.current = true
    if (firstStep && !focusFirstStep) {
      return
    }
    const container = containerRef.current
    if (container === null) {
      return
    }
    const active = document.activeElement
    // The replaced step's focused control is gone, so the browser has put
    // focus back on the body; a viewer who never focused anything has it
    // there too. Focus on the body, or still inside the surface the step
    // replaced, is the switch's to use. Focus on any other element is where
    // the viewer moved it, and taking it would undo their choice.
    const focusWasWithTheStep =
      active === null ||
      active === document.body ||
      active === document.documentElement ||
      container.contains(active)
    if (focusWasWithTheStep) {
      firstControl(container)?.focus()
    }
  }, [key, focusFirstStep, emptyTarget])

  return containerRef
}
