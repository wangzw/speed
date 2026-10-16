/**
 * ObjectAsyncSection: the page's binding of ui-kit's AsyncSection that
 * separates a permission refusal from every other failure.
 *
 * A tenant-scoped read is answered 403 when the caller lacks the
 * objectstore permission the route is held to, and that refusal is not a
 * load failure: the surface did answer, it refused. Rendering it as the
 * same retryable "the surface did not answer" placeholder both
 * misdescribes it and offers a retry that can only ever be refused again.
 * This component branches the error state on the failure: a 403 renders
 * ui-kit's no-permission EmptyState with this package's forbidden copy and
 * no action, and every other failure falls through to AsyncSection's
 * retryable error placeholder unchanged. The pending, empty and content
 * branches are AsyncSection's own -- only the failure branch differs.
 *
 * The refusal is detected from the answer, not a route-level permission
 * read: the server writes the 403, and the page renders the answer. The
 * check is structural -- the answer's HTTP status is 403, or a bare non-2xx
 * with no readable code envelope surfaced as the client.http.403 code -- so
 * it reads a plain object and never imports the transport.
 */

import type { ReactNode } from 'react'
import { EmptyState } from '@speed/ui-kit'
import {
  AsyncSection,
  type AsyncSectionEmptyState,
  type AsyncSectionErrorState,
} from '@speed/ui-kit'
import type { EmptyStateHeadingLevel } from '@speed/ui-kit'
import { useObjectstoreUiTranslation } from './translation.js'

/** The bare-HTTP code the transport synthesizes for a 403 with no readable envelope. */
const FORBIDDEN_CODE = 'client.http.403'

/**
 * Whether a read's failure is a permission refusal: the answer carried
 * HTTP 403, or a bare 403 surfaced as the client.http.403 code. Any other
 * value is not a refusal, so the caller renders the retryable error.
 */
export function isForbidden(error: unknown): boolean {
  if (typeof error !== 'object' || error === null) {
    return false
  }
  const candidate = error as { readonly status?: unknown; readonly code?: unknown }
  return candidate.status === 403 || candidate.code === FORBIDDEN_CODE
}

export interface ObjectAsyncSectionProps<Payload> {
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
  /** The settled content, built from the narrowed payload. */
  readonly children: (payload: Payload) => ReactNode
}

/** The page async section: an AsyncSection whose failure branch splits a 403 out. */
export function ObjectAsyncSection<Payload>({
  error,
  errorState,
  headingLevel = 'h2',
  ...rest
}: ObjectAsyncSectionProps<Payload>) {
  const { t } = useObjectstoreUiTranslation()
  if (!rest.pending && rest.payload === undefined && isForbidden(error)) {
    return (
      <EmptyState
        variant="noPermission"
        title={t('list.forbidden.title')}
        description={t('list.forbidden.description')}
        headingLevel={headingLevel}
      />
    )
  }
  return (
    <AsyncSection errorState={errorState} headingLevel={headingLevel} {...rest} />
  )
}
