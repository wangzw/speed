/**
 * The callback landing a sign-in surface consumes once on mount.
 *
 * A refused callback lands the browser back in the product with its problem
 * code, and for a link-required refusal the masked address whose account
 * collided, in the URL fragment. The read clears the fragment so a reload
 * does not repeat the message; that makes it a one-shot read, and
 * StrictMode mounts an effect twice in development. The first run consumes
 * the fragment, so an unguarded second run would read nothing and reset the
 * landing to empty -- the message the viewer came to see would vanish. The
 * ref keeps the read to once per mount.
 */

import { useEffect, useRef, useState } from 'react'
import { takeCallbackError } from './sign-in-landing.js'

/** A callback landing in the vocabulary the sign-in surface renders. */
export interface CallbackLanding {
  /** The error code (`authn.link_required`). */
  readonly code: string
  /** The masked address a link-required refusal named, when present. */
  readonly email?: string
}

/** Reads the callback landing once per mount and returns what it found. */
export function useCallbackLanding(): CallbackLanding | undefined {
  const [landing, setLanding] = useState<CallbackLanding | undefined>(undefined)
  const consumed = useRef(false)
  useEffect(() => {
    if (consumed.current) {
      return
    }
    consumed.current = true
    setLanding(takeCallbackError())
  }, [])
  return landing
}
