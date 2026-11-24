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
  readonly pending: boolean
  readonly payload: Payload | undefined
  readonly error: unknown
  readonly empty: boolean
  readonly loading: ReactNode
  readonly errorState: AsyncSectionErrorState
  readonly emptyState?: AsyncSectionEmptyState
  readonly headingLevel?: EmptyStateHeadingLevel
  readonly emptyHeadingLevel?: EmptyStateHeadingLevel | EmptyStateNoHeading
  readonly refreshing: boolean
  readonly children: (payload: Payload) => ReactNode
}

export function TenantAsyncSection<Payload>({
  error,
  errorState,
  headingLevel = 'h2',
  refreshing,
  ...rest
}: TenantAsyncSectionProps<Payload>) {
  const { t } = useNotificationUiTranslation()
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
