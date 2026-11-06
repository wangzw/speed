/**
 * The org-create-ui error-text resolver.
 *
 * A creation refusal arrives as the api-client's ApiError, whose `code`
 * is either a module answer (`org.organization_limit`) or one of the
 * reserved `client.*` families. The resolver follows @speed/i18n's
 * convention: a listed code resolves to its own `errors.<code>` leaf, a
 * bare `client.http.<status>` answer resolves through the parameterized
 * leaf (with actionable replacements for the statuses a creation most
 * often meets), a throttled answer names the wait it stated, and anything
 * else falls back to `errors.unknown`. Text always comes from this
 * package's namespace, never from an API's English fallback.
 */

import { useMemo } from 'react'
import {
  CLIENT_TRANSPORT_ERROR_CODES,
  SESSION_LIFECYCLE_ERROR_CODES,
  createErrorTextResolver,
  useTranslation,
} from '@speed/i18n'
import { errorCodeOf, retryAfterSecondsOf } from '@speed/ui-kit'
import { ORG_CREATE_UI_NAMESPACE } from './resources.js'

/**
 * The codes a creation attempt can be answered with and that carry their
 * own copy: the shared session and transport families, the email-proof
 * and forbidden refusals, the two shape refusals, and the four org-create
 * codes.
 */
export const ORGANIZATION_CREATE_ERROR_CODES = [
  ...SESSION_LIFECYCLE_ERROR_CODES,
  ...CLIENT_TRANSPORT_ERROR_CODES,
  'authn.contact_required',
  'authn.forbidden',
  'authn.bad_request',
  'tenant.bad_request',
  'org.organization_limit',
  'org.rate_limited',
  'org.creation_in_progress',
  'org.identifier_unavailable',
] as const

const KNOWN_CODES: ReadonlySet<string> = new Set(
  ORGANIZATION_CREATE_ERROR_CODES,
)

/** The leaves that name a throttled creation's wait. */
const WAIT_KEY = 'errors.org.rate_limited_wait'
const WAIT_LONG_KEY = 'errors.org.rate_limited_wait_long'

/** The code set that states a wait. */
const WAIT_CODES = ['org.rate_limited'] as const

/** The statuses a bare HTTP refusal maps to an actionable sentence. */
const HTTP_BY_STATUS: Readonly<Record<string, string>> = {
  '400': 'errors.badRequest',
  '401': 'errors.sessionEnded',
  '403': 'errors.forbidden',
  '409': 'errors.conflict',
  '429': 'errors.rateLimited',
}

/**
 * The resolver a page binds to its error region: reads the current
 * language reactively (a language switch recreates it), and turns the
 * stated wait into seconds or whole minutes.
 */
export function useOrganizationCreateErrorText(): (
  code: string,
  retryAfterSeconds?: number,
) => string {
  const { t } = useTranslation(ORG_CREATE_UI_NAMESPACE)
  return useMemo(
    () =>
      createErrorTextResolver(t, KNOWN_CODES, {
        httpFamily: { key: 'errors.client.http', byStatus: HTTP_BY_STATUS },
        wait: {
          key: WAIT_KEY,
          longKey: WAIT_LONG_KEY,
          codes: WAIT_CODES,
        },
      }),
    [t],
  )
}

/** One failed creation attempt's code and stated wait, ready to resolve. */
export interface OrganizationCreateFailure {
  readonly code: string
  readonly retryAfterSeconds?: number
}

/** Classify an arbitrary thrown value into the failure shape the page
 * renders. Reads defensively through ui-kit's structural helpers. */
export function organizationCreateFailure(
  error: unknown,
): OrganizationCreateFailure {
  const code = errorCodeOf(error)
  const retryAfterSeconds = retryAfterSecondsOf(error)
  return retryAfterSeconds === undefined
    ? { code }
    : { code, retryAfterSeconds }
}
