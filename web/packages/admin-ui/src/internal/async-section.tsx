/**
 * AdminAsyncSection: splits a 403 (no retry), the tenant-admission sign-in exits,
 * a by-id 404 and every other failure into their own branches.
 */

import type { ReactNode } from 'react'
import Button from '@mui/material/Button'
import { stepUpReasonOf } from '@speed/auth-core'
import { EmptyState, errorCodeOf, httpStatusOf } from '@speed/ui-kit'
import {
  AsyncSection,
  type AsyncSectionEmptyState,
  type AsyncSectionErrorState,
} from '@speed/ui-kit'
import type { EmptyStateHeadingLevel } from '@speed/ui-kit'
import { useAdminAccountHref } from './account-href.js'
import { useAdminUiErrorText } from './error-text.js'
import { useNoMfaFactor } from './mfa-refusal.js'
import { AdminLink } from './navigation.js'
import { useAdminUiTranslation } from './translation.js'

function carriesStatus(error: unknown, status: number): boolean {
  return httpStatusOf(error) === status
}

/** Whether a read's failure is a permission refusal (HTTP 403). */
export function isForbidden(error: unknown): boolean {
  return carriesStatus(error, 403)
}

/** Whether a read's failure is "no such record" (HTTP 404). */
export function isNotFound(error: unknown): boolean {
  return carriesStatus(error, 404)
}

export interface AdminAsyncSectionProps<Payload> {
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
   * The placeholder a by-id read answered 404 renders, still with its retry
   * because a bare 404 cannot be told apart from a replica that lacks the
   * record. Absent leaves every failure on `errorState`.
   */
  readonly notFoundState?: AsyncSectionErrorState
  /**
   * Extra HTTP statuses the not-found branch also covers, such as a 400 on a
   * path identifier the route cannot parse. Only consulted when
   * `notFoundState` is set; 404 is always covered.
   */
  readonly notFoundStatuses?: readonly number[]
  /** The empty placeholder. */
  readonly emptyState?: AsyncSectionEmptyState
  /** The heading element the placeholders' titles render as; defaults to 'h2'. */
  readonly headingLevel?: EmptyStateHeadingLevel
  /**
   * Whether a read is in flight over content already on screen. Required: the
   * stale marker withdraws while the retry runs, so a caller must pass its
   * read's fetching state.
   */
  readonly refreshing: boolean
  /** The settled content, built from the narrowed payload. */
  readonly children: (payload: Payload) => ReactNode
  /**
   * The re-login exit for the tenant admission layer's SSO refusal; any other
   * failure, or no exit, keeps the branches above.
   */
  readonly ssoRelogin?: () => void
  /**
   * The sign-in exit for the multi-factor refusal: with an enabled factor it
   * renders this action; with none it names the missing two-step verification
   * and points at the host's account settings.
   */
  readonly mfaRelogin?: () => void
}

/** The console async section: an AsyncSection whose failure branch splits a 403 out. */
export function AdminAsyncSection<Payload>({
  error,
  errorState,
  notFoundState,
  notFoundStatuses,
  headingLevel = 'h2',
  refreshing,
  ssoRelogin,
  mfaRelogin,
  ...rest
}: AdminAsyncSectionProps<Payload>) {
  const { t } = useAdminUiTranslation()
  const resolve = useAdminUiErrorText()
  // No host path is held here, so the no-factor state is text-only when the
  // host supplies no account settings page.
  const accountHref = useAdminAccountHref()
  const failureCode = errorCodeOf(error)
  // Hooks run on every render, so the mfa branch is read before the early
  // returns; only a surface that offers the exit takes it.
  const mfaRefused =
    !rest.pending &&
    mfaRelogin !== undefined &&
    stepUpReasonOf(failureCode) === 'mfa'
  const noMfaFactor = useNoMfaFactor(mfaRefused)
  // A refusal makes the older data meaningless, so it is never marked stale
  // and no retry is offered.
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
  // No retry can clear the SSO refusal while the session stays local; only a
  // surface that offers the sign-in exit takes this branch.
  if (
    !rest.pending &&
    ssoRelogin !== undefined &&
    stepUpReasonOf(failureCode) === 'sso'
  ) {
    return (
      <EmptyState
        variant="error"
        title={errorState.title}
        description={resolve(failureCode)}
        action={
          <Button onClick={ssoRelogin}>{t('stepUp.relogin')}</Button>
        }
        headingLevel={headingLevel}
      />
    )
  }
  // A sign-in clears the multi-factor refusal only for an account with a
  // factor; an account with none must register one first, so the branch
  // names the missing verification and points at enrollment.
  if (mfaRefused) {
    return (
      <EmptyState
        variant="error"
        title={errorState.title}
        description={
          noMfaFactor
            ? t('stepUp.mfaEnrollRequired')
            : resolve(failureCode)
        }
        action={
          noMfaFactor
            ? accountHref === undefined
              ? undefined
              : (
                  <AdminLink href={accountHref}>
                    {t('stepUp.goToAccount')}
                  </AdminLink>
                )
            : <Button onClick={mfaRelogin}>{t('stepUp.relogin')}</Button>
        }
        headingLevel={headingLevel}
      />
    )
  }
  // A by-id 404 replaces the content and keeps the retry: a bare 404 does
  // not distinguish a deleted record from a replica that does not know it.
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
  // Every other failure over settled content is a failed refresh: the content
  // stays with the stale marker, which `refreshing` withdraws while a retry runs.
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
