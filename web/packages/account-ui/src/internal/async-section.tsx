/**
 * AccountAsyncSection: the account surfaces' binding of ui-kit's
 * AsyncSection that marks a failed refresh over retained data.
 *
 * Every section reads through the host's QueryClient, and a refetch that
 * fails keeps the last settled payload in the cache. Rendering that
 * payload with no signal leaves the reader unable to tell it came from an
 * earlier read, so this binding derives ui-kit's `staleState` from the
 * read's failure over a settled payload: the old content stays on screen,
 * an alert above it states that it may be out of date, and the retry
 * action is the section's own retry. The marker is withdrawn while a read
 * is in flight, so a retry unmounts it and a retry that fails again
 * mounts it anew, announcing again.
 *
 * The pending, empty and content branches are AsyncSection's own; only the
 * derivation of `staleState` differs, and the stale copy is this
 * package's own key so the sections cannot drift apart.
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
   * Whether a read is in flight over content already on screen (a
   * refetch). A failed refresh keeps the content and marks it stale; the
   * marker is withdrawn while the read is in flight, so every caller
   * passes its read's fetching state here.
   */
  readonly refreshing: boolean
}

/** The async section the account surfaces render their reads through. */
export function AccountAsyncSection<Payload>({
  error,
  refreshing,
  ...rest
}: AccountAsyncSectionProps<Payload>) {
  const { t } = useAccountUiTranslation()
  // A failure over an already settled payload is a failed refresh: the
  // content stays with the stale marker above it. `refreshing` withdraws
  // the marker while the retry runs; a retry that fails again re-supplies
  // it, so the remounted alert announces again.
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
