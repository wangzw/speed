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
 * falls through to AsyncSection's retryable error placeholder. A by-id read
 * that is answered 404 renders its own `notFoundState` placeholder instead,
 * still with a retry -- the server's bare 404 does not distinguish a deleted
 * record from a replica that does not know it -- and replaces any content on
 * screen, as a failure of meaning rather than a stale refresh. A page whose
 * route answers another status for a path identifier it cannot parse (a 400
 * on a malformed id) names that status in `notFoundStatuses`, because no
 * record can carry an identifier the route cannot read. The pending, empty
 * and content branches are AsyncSection's own.
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

/**
 * Whether a read's failure carries the given HTTP status: the answer's
 * `status`, or the bare-HTTP code the transport synthesizes when the
 * response carried no readable envelope. A transport failure, a bug-shaped
 * throw and any other status are not this answer.
 */
function carriesStatus(error: unknown, status: number): boolean {
  if (typeof error !== 'object' || error === null) {
    return false
  }
  const candidate = error as { readonly status?: unknown; readonly code?: unknown }
  return (
    candidate.status === status || candidate.code === `client.http.${status}`
  )
}

/**
 * Whether a read's failure is a permission refusal: the answer carried HTTP
 * 403, or a bare 403 surfaced as the client.http.403 code. Any other value
 * -- undefined, a transport failure, a bug-shaped throw -- is not a
 * refusal, so the caller renders the retryable error instead.
 */
export function isForbidden(error: unknown): boolean {
  return carriesStatus(error, 403)
}

/**
 * Whether a read's failure is "no such record": the answer carried HTTP 404,
 * or a bare 404 surfaced as the client.http.404 code. A by-id detail page
 * renders its not-found placeholder for this failure -- the server's "no such
 * record" is an untyped bare 404, indistinguishable from a replica that does
 * not know the record, so the placeholder still offers a retry. Every other
 * value is not this failure.
 */
export function isNotFound(error: unknown): boolean {
  return carriesStatus(error, 404)
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
  /**
   * The placeholder a by-id read answered 404 renders: "the record does not
   * exist or is temporarily unavailable", still with its retry, because the
   * server's bare 404 cannot be told apart from a replica that does not know
   * the record. Absent leaves every failure on `errorState`.
   */
  readonly notFoundState?: AsyncSectionErrorState
  /**
   * Extra HTTP statuses this read's not-found branch also covers. A by-id
   * route that answers 400 for an identifier it cannot parse (the path does
   * not shape an id, so no record can carry it) means the same thing to the
   * reader as a 404, and a page that knows its own route's refusal shape
   * names it here. Only consulted when `notFoundState` is set; the 404
   * answer is always covered and needs no entry.
   */
  readonly notFoundStatuses?: readonly number[]
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
  notFoundState,
  notFoundStatuses,
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
  // A by-id read answered 404 (or another status the page names as the same
  // answer) renders its own placeholder in place of the content -- a
  // refresh that answers so replaces what was on screen -- and keeps the
  // retry: the answer does not distinguish a deleted record from a replica
  // that does not know it yet.
  const notFound =
    isNotFound(error) ||
    (notFoundStatuses ?? []).some((status) => carriesStatus(error, status))
  if (!rest.pending && notFoundState !== undefined && notFound) {
    return (
      <AsyncSection
        {...rest}
        payload={undefined}
        errorState={notFoundState}
        headingLevel={headingLevel}
        staleState={undefined}
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
