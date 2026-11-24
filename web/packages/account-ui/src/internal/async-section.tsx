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
  /** The read's failure, as the query reports it beside its settled data. */
  readonly error: unknown
  /**
   * Whether a read is in flight over content already on screen. A failed
   * refresh marks the content stale; every caller passes its read's
   * fetching state here.
   */
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
