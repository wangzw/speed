/**
 * WriteFeedbackRegion: the console's binding of ui-kit's feedback region.
 *
 * A console page reports one control group's writes through one
 * `useWriteFeedback` slot and renders this region directly under the
 * controls. The region itself -- the success notice always mounted as the
 * polite live region, and the failure alert mounted only for a failure --
 * is ui-kit's shared `WriteFeedbackRegion`. This module is the surface's
 * binding of it to the console-ui namespace's error-text resolver, so call
 * sites pass the slot and nothing else: a failure code resolves only
 * through the console whitelist, and a code outside it renders the shared
 * `errors.unknown` fallback instead of a raw key.
 *
 * The module also carries the two pieces every page's scopes share: the
 * resolved text of a slot's failure, so a row's inline marker and the
 * region render the one message through the one resolver, and a ref that
 * brings a region into view when its scope settles a failure, so a failure
 * reported below a long table is still seen.
 */

import { useEffect, useRef } from 'react'
import type { RefObject } from 'react'
import type { SxProps, Theme } from '@mui/material/styles'
import { WriteFeedbackRegion as SharedWriteFeedbackRegion } from '@speed/ui-kit'
import type { WriteOutcome } from '@speed/ui-kit'
import { useConsoleUiErrorText } from './error-text.js'

export interface WriteFeedbackRegionProps {
  /** The scope's slot; `null` renders no visible feedback. */
  readonly outcome: WriteOutcome | null
  /** Styles for the region's root container; spacing adjustments. */
  readonly sx?: SxProps<Theme>
}

/** The console's feedback region: one scope's slot rendered with its resolver. */
export function WriteFeedbackRegion({ outcome, sx }: WriteFeedbackRegionProps) {
  const resolve = useConsoleUiErrorText()
  return (
    <SharedWriteFeedbackRegion outcome={outcome} resolve={resolve} sx={sx} />
  )
}

/**
 * The text a scope's failure resolves to, or null when the slot holds no
 * failure. A page passes the same resolver the region binds, so an inline
 * row marker and the region show the one message.
 */
export function failureTextOf(
  outcome: WriteOutcome | null,
  resolve: (code: string) => string,
): string | null {
  return outcome !== null && outcome.kind === 'failure'
    ? resolve(outcome.code)
    : null
}

/**
 * A ref for a scope's region container that brings the region into view
 * when its scope settles a failure. The region sits directly under a
 * control group whose table may be taller than the viewport, so a row's
 * failure could otherwise be reported out of sight. Success never moves
 * the page, and neither does a cleared slot. An operator who asks for
 * reduced motion gets an instant jump instead of an animation.
 */
export function useFailureScroll(
  outcome: WriteOutcome | null,
): RefObject<HTMLDivElement | null> {
  const ref = useRef<HTMLDivElement | null>(null)
  useEffect(() => {
    if (outcome === null || outcome.kind !== 'failure') {
      return
    }
    const region = ref.current
    if (region === null) {
      return
    }
    const reduceMotion =
      window.matchMedia?.('(prefers-reduced-motion: reduce)').matches ?? false
    region.scrollIntoView?.({
      behavior: reduceMotion ? 'auto' : 'smooth',
      block: 'center',
    })
  }, [outcome])
  return ref
}
