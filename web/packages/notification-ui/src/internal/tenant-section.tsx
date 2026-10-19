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
  type AsyncSectionStaleState,
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
  /** The failed-refresh notice with earlier data still on screen. */
  readonly staleState?: AsyncSectionStaleState
  /** The settled content, built from the narrowed payload. */
  readonly children: (payload: Payload) => ReactNode
}

/** The surface async section: an AsyncSection whose failure branch splits the tenant refusal out. */
export function TenantAsyncSection<Payload>({
  error,
  headingLevel = 'h2',
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
  return <AsyncSection headingLevel={headingLevel} {...rest} />
}
