/**
 * The callback landing a host surface consumes once on mount.
 *
 * A refused callback lands the browser back in the product with its problem
 * code, and for a link-required refusal the masked address whose account
 * collided, in the URL fragment. The landing is read on the first render, so
 * a host can decide what to mount before anything reads a flow: a callback
 * route that has a refusal to show never reaches the flow operation. The
 * read itself changes nothing, so StrictMode's double render sees the same
 * landing; the fragment is cleared afterwards, once the read has committed,
 * so a reload does not repeat the message.
 */

import { useEffect, useState } from 'react'
import {
  clearCallbackFragment,
  readCallbackError,
} from './sign-in-landing.js'

/** A callback landing in the vocabulary the surface renders. */
export interface CallbackLanding {
  /** The error code (`authn.link_required`). */
  readonly code: string
  /** The masked address a link-required refusal named, when present. */
  readonly email?: string
}

/** Reads the callback landing on the first render and clears it after mount. */
export function useCallbackLanding(): CallbackLanding | undefined {
  const [landing] = useState<CallbackLanding | undefined>(() =>
    readCallbackError(),
  )
  useEffect(() => {
    clearCallbackFragment()
  }, [])
  return landing
}
