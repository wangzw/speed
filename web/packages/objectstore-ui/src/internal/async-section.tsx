/**
 * ObjectAsyncSection: the page's binding of ui-kit's AsyncSection that
 * separates the refusals of the tenant's admission layer and of the
 * authorization guard from every other failure.
 *
 * A read of a tenant the caller cannot enter is answered by the admission
 * layer with a bare 404 that deliberately does not distinguish a tenant
 * that does not exist, one that is disabled and a caller who is not a
 * member. That refusal is not a load failure: the surface did answer, it
 * refused. Rendering it as the same retryable "the surface did not answer"
 * placeholder both misdescribes it and invites a retry that can only be
 * refused again. The bare 404 renders ui-kit's no-permission EmptyState
 * with this package's tenant refusal copy and no action.
 *
 * A tenant-scoped read is answered 403 when the caller lacks the
 * objectstore permission the route is held to; that refusal likewise
 * renders the no-permission EmptyState with the forbidden copy and no
 * action. Every other failure falls through to AsyncSection's retryable
 * error placeholder unchanged. The pending, empty and content branches are
 * AsyncSection's own -- only the failure branch differs.
 *
 * A typed 404 is not the admission refusal: the objectstore module answers
 * an object that does not exist with a problem object whose type derives
 * `objectstore.not_found`, and that code keeps its own leaf rather than
 * rendering the tenant state.
 *
 * The refusals are detected from the answer, not a route-level permission
 * read: the server writes the status, and the page renders the answer. The
 * checks are structural -- the answer's HTTP status is 403, or a bare
 * non-2xx with no readable code envelope surfaced as the client.http.403 /
 * client.http.404 code -- so they read a plain object and never import the
 * transport.
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
 * The bare-HTTP code the transport synthesizes for a bare 404: a 404 whose
 * body carries neither a module `code` nor a `urn:speed:` problem type. The
 * admission layer's tenant refusal is exactly that; the objectstore
 * module's own missing-object refusal carries `objectstore.not_found`
 * instead, so its status alone never marks the tenant refusal.
 */
const TENANT_REFUSAL_CODE = 'client.http.404'

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

/**
 * Whether a read's failure is the admission layer's tenant refusal: a bare
 * 404 surfaced as the client.http.404 code. Status alone is not enough --
 * a typed `objectstore.not_found` 404 also carries status 404 -- so the
 * check reads the code, which the api-client synthesizes only when the
 * answer carried no module code.
 */
export function isTenantRefusal(error: unknown): boolean {
  if (typeof error !== 'object' || error === null) {
    return false
  }
  const candidate = error as { readonly code?: unknown }
  return candidate.code === TENANT_REFUSAL_CODE
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
  // The tenant refusal replaces the content even when a cached payload is
  // still on screen: once admission refuses the tenant, the older data
  // describes a tenant the caller can no longer enter.
  if (!rest.pending && isTenantRefusal(error)) {
    return (
      <EmptyState
        variant="noPermission"
        title={t('tenantUnavailable.title')}
        description={t('tenantUnavailable.description')}
        headingLevel={headingLevel}
      />
    )
  }
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
