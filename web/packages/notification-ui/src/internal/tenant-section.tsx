/**
 * TenantAsyncSection: the surface's binding of ui-kit's AsyncSection that
 * separates the tenant admission layer's refusal from every other failure.
 *
 * The notification routes are tenant-scoped, and the admission layer
 * answers a tenant the caller cannot enter with a bare 404 that
 * deliberately does not distinguish a tenant that does not exist, one that
 * is disabled and a caller who is not a member. That refusal is not a load
 * failure: the surface did answer, it refused. Rendering it as the same
 * generic retryable error placeholder both misdescribes it and invites a
 * retry that can only be refused again. This component
 * branches the error state on that answer: a bare 404 renders ui-kit's
 * no-permission EmptyState with this package's tenant refusal copy and no
 * action, and every other failure falls through to AsyncSection's
 * retryable error placeholder unchanged. The pending, empty and content
 * branches are AsyncSection's own -- only the failure branch differs.
 *
 * A non-refusal failure over an already settled payload is a failed
 * refresh: the content stays on screen and AsyncSection's `staleState`
 * announces that it may be out of date, with the section's own retry
 * label and callback. The caller passes `refreshing` while a read is in
 * flight, withdrawing the marker; a retry that fails again re-supplies it,
 * so the alert mounts anew and announces again. A refusal keeps priority:
 * once admission refuses the tenant the older data is meaningless, so it
 * is replaced rather than marked stale.
 *
 * A typed 404 is not the admission refusal: the notification module's own
 * refusals arrive as `notification.*` codes and keep their own error text.
 *
 * The refusal is detected from the answer, not a route-level permission
 * read: the server writes the 404, and the surface renders the answer.
 * @speed/auth-core's `isTenantAdmissionRefusal` owns the structural check
 * (a bare 404 with no module code, surfaced as the client.http.404 code),
 * so every consumer of the admission answer classifies it the same way.
 */

import type { ReactNode } from 'react'
import { EmptyState } from '@speed/ui-kit'
import {
  AsyncSection,
  type AsyncSectionEmptyState,
  type AsyncSectionErrorState,
} from '@speed/ui-kit'
import type {
  EmptyStateHeadingLevel,
  EmptyStateNoHeading,
} from '@speed/ui-kit'
import { isTenantAdmissionRefusal } from '@speed/auth-core'
import { useNotificationUiTranslation } from './translation.js'

export interface TenantAsyncSectionProps<Payload> {
  /** No answer yet: renders `loading`. */
  readonly pending: boolean
  /** The settled payload; `undefined` renders a failure branch. */
  readonly payload: Payload | undefined
  /** The read's failure, inspected for the admission refusal. */
  readonly error: unknown
  /** Whether the settled payload renders as empty. */
  readonly empty: boolean
  /** The loading announcement. */
  readonly loading: ReactNode
  /** The retryable error placeholder, for every non-refusal failure. */
  readonly errorState: AsyncSectionErrorState
  /** The empty placeholder. */
  readonly emptyState?: AsyncSectionEmptyState
  /** The heading element the placeholders' titles render as; defaults to 'h2'. */
  readonly headingLevel?: EmptyStateHeadingLevel
  /** The empty placeholder's own heading element, or `'none'`. */
  readonly emptyHeadingLevel?: EmptyStateHeadingLevel | EmptyStateNoHeading
  /**
   * Whether a read is in flight over content already on screen (a
   * refetch). A failed refresh keeps the content and marks it stale; the
   * marker is withdrawn while the read is in flight, so every caller
   * passes its read's fetching state here. Required: a section whose
   * caller dropped it would keep the marker standing over a read it is
   * already re-reading.
   */
  readonly refreshing: boolean
  /** The settled content, built from the narrowed payload. */
  readonly children: (payload: Payload) => ReactNode
}

/** The surface async section: an AsyncSection whose failure branch splits the tenant refusal out. */
export function TenantAsyncSection<Payload>({
  error,
  errorState,
  headingLevel = 'h2',
  refreshing,
  ...rest
}: TenantAsyncSectionProps<Payload>) {
  const { t } = useNotificationUiTranslation()
  // The tenant refusal replaces the content even when a cached payload is
  // still on screen: once admission refuses the tenant, the older data
  // describes a tenant the caller can no longer enter.
  if (!rest.pending && isTenantAdmissionRefusal(error)) {
    return (
      <EmptyState
        variant="noPermission"
        title={t('tenantUnavailable.title')}
        description={t('tenantUnavailable.description')}
        headingLevel={headingLevel}
      />
    )
  }
  // Every other failure over an already settled payload is a failed
  // refresh: the content stays with the stale marker above it.
  // `refreshing` withdraws the marker while the retry runs; a retry that
  // fails again re-supplies it, and the remounted alert announces again.
  const staleState =
    !refreshing &&
    rest.payload !== undefined &&
    error !== undefined &&
    error !== null
      ? {
          description: t('stale.description'),
          retryLabel: errorState.retryLabel,
          onRetry: errorState.onRetry,
        }
      : undefined
  return (
    <AsyncSection
      errorState={errorState}
      headingLevel={headingLevel}
      staleState={staleState}
      {...rest}
    />
  )
}
