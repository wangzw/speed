/**
 * ConsoleAsyncSection: the console's binding of ui-kit's AsyncSection that
 * separates a permission refusal from every other failure.
 *
 * A console read is answered 403 when the caller lacks the platform
 * permission the surface is held to, and that refusal is not a load
 * failure: the surface did answer, it refused. Rendering it as the same
 * retryable "the surface did not answer" placeholder both misdescribes it
 * and offers a retry that can only ever be refused again. This component
 * branches the error state on the failure: a 403 renders ui-kit's
 * no-permission EmptyState with this package's forbidden copy and no
 * action, replacing whatever content was on screen -- a refusal makes the
 * older data meaningless, so it never becomes a stale marker. Every other
 * failure over an already settled payload is a failed refresh: the content
 * stays and AsyncSection's `staleState` announces that it may be out of
 * date, with the section's own retry label and callback. The caller passes
 * `refreshing` while the retry is in flight, withdrawing the marker; a
 * retry that fails again re-supplies it, so the alert mounts anew and
 * announces again. Without a settled payload the same non-403 failure
 * falls through to AsyncSection's retryable error placeholder. The
 * pending, empty and content branches are AsyncSection's own.
 *
 * The refusal is detected from the answer, not from a route-level
 * permission read: the server writes the 403, and the console renders the
 * answer (the route-level guard is the host's composition). The check is
 * structural -- the answer's HTTP status is 403, or a bare non-2xx with no
 * readable envelope surfaced as the client.http.403 code -- so it reads a
 * plain object and never imports the transport.
 */

import type { ReactNode } from 'react'
import { EmptyState } from '@speed/ui-kit'
import {
  AsyncSection,
  type AsyncSectionEmptyState,
  type AsyncSectionErrorState,
} from '@speed/ui-kit'
import type { EmptyStateHeadingLevel } from '@speed/ui-kit'
import { useConsoleUiTranslation } from './translation.js'

/** The bare-HTTP code the transport synthesizes for a 403 with no readable envelope. */
const FORBIDDEN_CODE = 'client.http.403'

/**
 * Whether a read's failure is a permission refusal: the answer carried HTTP
 * 403, or a bare 403 surfaced as the client.http.403 code. Any other value
 * -- undefined, a transport failure, a bug-shaped throw -- is not a
 * refusal, so the caller renders the retryable error instead.
 */
export function isForbidden(error: unknown): boolean {
  if (typeof error !== 'object' || error === null) {
    return false
  }
  const candidate = error as { readonly status?: unknown; readonly code?: unknown }
  if (candidate.status === 403) {
    return true
  }
  return candidate.code === FORBIDDEN_CODE
}

export interface ConsoleAsyncSectionProps<Payload> {
  /** No answer yet: renders `loading`. */
  readonly pending: boolean
  /** The settled payload; `undefined` renders a failure branch. */
  readonly payload: Payload | undefined
  /** The read's failure, inspected for a permission refusal. */
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
  /**
   * Whether a read is in flight over content already on screen (a refetch).
   * A failed refresh keeps the content and marks it stale; the marker is
   * withdrawn while the retry is in flight, so every caller passes its
   * read's fetching state here. Required: a section whose caller dropped it
   * would keep the marker standing over a read it is already re-reading.
   */
  readonly refreshing: boolean
  /** The settled content, built from the narrowed payload. */
  readonly children: (payload: Payload) => ReactNode
}

/** The console async section: an AsyncSection whose failure branch splits a 403 out. */
export function ConsoleAsyncSection<Payload>({
  error,
  errorState,
  headingLevel = 'h2',
  refreshing,
  ...rest
}: ConsoleAsyncSectionProps<Payload>) {
  const { t } = useConsoleUiTranslation()
  // A refusal replaces the content whatever the payload is: the older data
  // no longer means anything, so it is never marked stale and no retry is
  // offered. A first load still pending renders the loading announcement.
  if (!rest.pending && isForbidden(error)) {
    return (
      <EmptyState
        variant="noPermission"
        title={t('forbidden.title')}
        description={t('forbidden.description')}
        headingLevel={headingLevel}
      />
    )
  }
  // Every other failure over an already settled payload is a failed
  // refresh: the content stays on screen with the stale marker above it.
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
