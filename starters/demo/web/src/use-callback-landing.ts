import { useCallback, useEffect, useState } from 'react'
import {
  clearCallbackFragment,
  readCallbackError,
} from './sign-in-landing.js'

export interface CallbackLanding {
  readonly code: string
  readonly email?: string
}

export interface CallbackLandingHandle {
  readonly landing: CallbackLanding | undefined
  readonly dismiss: () => void
}

export function useCallbackLanding(): CallbackLandingHandle {
  const [landing, setLanding] = useState<CallbackLanding | undefined>(() =>
    readCallbackError(),
  )
  useEffect(() => {
    clearCallbackFragment()
  }, [])
  const dismiss = useCallback(() => {
    setLanding(undefined)
  }, [])
  return { landing, dismiss }
}
