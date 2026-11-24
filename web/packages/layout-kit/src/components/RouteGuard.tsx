import { useEffect, useRef } from 'react'
import type { ReactNode } from 'react'
import Box from '@mui/material/Box'
import CircularProgress from '@mui/material/CircularProgress'
import { EmptyState } from '@speed/ui-kit'
import type { EmptyStateProps } from '@speed/ui-kit'
import { useLayoutKitTranslation } from '../internal/translation.js'

/** The host-computed gate decision; the three states are mutually exclusive. */
export type RouteGuardStatus = 'allowed' | 'denied' | 'pending'

/** Props for RouteGuard. */
export interface RouteGuardProps {
  /** The host-computed authorization decision for this render. */
  readonly status: RouteGuardStatus
  /** Rendered only when `status` is `'allowed'`. */
  readonly children?: ReactNode
  /** Overrides the default `'pending'` placeholder (a labelled spinner). */
  readonly pendingFallback?: ReactNode
  /** Overrides the default `'denied'` placeholder (ui-kit's `noPermission` EmptyState). */
  readonly deniedFallback?: ReactNode
  /** The heading level of the default `'denied'` EmptyState; defaults to `'h1'` and is ignored when `deniedFallback` is supplied. */
  readonly headingLevel?: EmptyStateProps['headingLevel']
  /** Fires once per transition into `'denied'` -- never on every re-render. */
  readonly onDenied?: () => void
}

/** Gates `children` on `status`: renders them only when `'allowed'`, otherwise the matching fallback. */
export function RouteGuard({
  status,
  children,
  pendingFallback,
  deniedFallback,
  headingLevel = 'h1',
  onDenied,
}: RouteGuardProps) {
  const { t } = useLayoutKitTranslation()

  // Fires onDenied once per transition into 'denied'; two renders at 'denied' in a row do not.
  const firedForThisDenial = useRef(false)
  useEffect(() => {
    if (status === 'denied') {
      if (!firedForThisDenial.current) {
        firedForThisDenial.current = true
        onDenied?.()
      }
    } else {
      firedForThisDenial.current = false
    }
  }, [status, onDenied])

  if (status === 'allowed') {
    return <>{children}</>
  }

  if (status === 'pending') {
    return (
      pendingFallback ?? (
        <Box
          sx={{
            display: 'flex',
            justifyContent: 'center',
            alignItems: 'center',
            paddingY: 6,
          }}
        >
          <CircularProgress aria-label={t('routeGuard.pending')} />
        </Box>
      )
    )
  }

  return (
    <>
      {deniedFallback ?? (
        <EmptyState variant="noPermission" headingLevel={headingLevel} />
      )}
    </>
  )
}
