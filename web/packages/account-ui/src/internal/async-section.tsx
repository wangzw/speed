/**
 * AccountAsyncSection: the account surfaces' binding of ui-kit's
 * AsyncSection that marks a failed refresh over retained data.
 */

import {
  AsyncSection,
  type AsyncSectionProps,
} from '@speed/ui-kit'
import { useAccountUiTranslation } from './translation.js'

export interface AccountAsyncSectionProps<Payload>
  extends Omit<AsyncSectionProps<Payload>, 'staleState'> {
  readonly error: unknown
  readonly refreshing: boolean
}

export function AccountAsyncSection<Payload>({
  error,
  refreshing,
  ...rest
}: AccountAsyncSectionProps<Payload>) {
  const { t } = useAccountUiTranslation()
  const staleState =
    !refreshing &&
    rest.payload !== undefined &&
    error !== undefined &&
    error !== null
      ? {
          description: t('stale.description'),
          retryLabel: rest.errorState.retryLabel,
          onRetry: rest.errorState.onRetry,
        }
      : undefined
  return <AsyncSection staleState={staleState} {...rest} />
}
