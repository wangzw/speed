import { useEffect, useRef } from 'react'
import type { RefObject } from 'react'
import type { OpenFlowState } from './flow.js'

const FOCUSABLE = [
  'a[href]',
  'button:not([disabled])',
  'input:not([disabled]):not([type="hidden"])',
  'select:not([disabled])',
  'textarea:not([disabled])',
  '[tabindex]:not([tabindex="-1"])',
].join(', ')

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
   * -- a terminal notice or a failure page. Omitted, a `null` key moves no
   * focus.
   */
  readonly emptyTarget?: RefObject<HTMLElement | null>
}

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
 * Moves focus to the new step's first control when the step key changes, but
 * only when the replaced step's control held it (focus left on the body, the
 * document element, or still inside the replaced surface). Focus the viewer
 * moved elsewhere is left alone. When a step gives way to no step,
 * `emptyTarget` takes the dropped focus.
 */
export function useStepFocus(
  key: string | null,
  options: StepFocusOptions = {},
): RefObject<HTMLDivElement | null> {
  const containerRef = useRef<HTMLDivElement>(null)
  const previousKey = useRef(key)
  const sawStep = useRef(key !== null)
  const { focusFirstStep = true, emptyTarget } = options

  useEffect(() => {
    const changed = previousKey.current !== key
    previousKey.current = key
    if (!changed) {
      return
    }
    if (key === null) {
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
