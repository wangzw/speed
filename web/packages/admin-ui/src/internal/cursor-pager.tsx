/**
 * CursorPager: the shared previous/next control for cursor-paged lists.
 *
 * Both the member list and the invitation list page by an opaque cursor the
 * server returns as `next_cursor`: there is no total page count to show, so
 * the control names the page number the operator is on and offers the two
 * directions. The page index is zero-based in state and one-based in the
 * label. The control renders nothing on a single-page list, so a caller may
 * place it unconditionally below the table.
 *
 * Both directions stay focusable in every state. A write in flight makes
 * both unavailable, because a page change would read a cursor that is not
 * the settled page's. A page read in flight makes the direction that started
 * it busy and the other unavailable, so the operator keeps focus on the
 * direction they activated. A list boundary makes that direction unavailable.
 *
 * The labels come from the admin-ui namespace's shared pager keys, so the
 * two lists page with one wording and the host overrides it once.
 */

import { useEffect, useState } from 'react'
import Box from '@mui/material/Box'
import Typography from '@mui/material/Typography'
import { ActionButton } from '@speed/ui-kit'
import { useAdminUiTranslation } from './translation.js'

export interface CursorPagerProps {
  /** The page the list is on, zero-based. */
  readonly page: number
  /** Whether the server reported a next page. */
  readonly hasNext: boolean
  /** Whether a write is in flight; both directions are then unavailable. */
  readonly busy?: boolean
  /**
   * Whether a page read is in flight; the direction that started it reports
   * busy while the other direction is unavailable.
   */
  readonly reading?: boolean
  /** Moves to the page before the current one. */
  readonly onPrevious: () => void
  /** Moves to the page the server's next cursor names. */
  readonly onNext: () => void
}

interface StartedRead {
  readonly direction: 'previous' | 'next'
  /** The page the activated direction moves to. */
  readonly page: number
}

export function CursorPager({
  page,
  hasNext,
  busy = false,
  reading = false,
  onPrevious,
  onNext,
}: CursorPagerProps) {
  const { t } = useAdminUiTranslation()
  const [startedRead, setStartedRead] = useState<StartedRead | null>(null)

  useEffect(() => {
    if (!reading) {
      setStartedRead(null)
    }
  }, [reading])

  if (page === 0 && !hasNext) {
    return null
  }
  // A read started from elsewhere (a filter change, an invalidation refetch)
  // names no direction; both directions are then unavailable.
  const readDirection =
    reading && startedRead !== null && startedRead.page === page
      ? startedRead.direction
      : null
  const previousBusy = !busy && readDirection === 'previous'
  const nextBusy = !busy && readDirection === 'next'
  const previousUnavailable = busy || page === 0 || (reading && !previousBusy)
  const nextUnavailable = busy || !hasNext || (reading && !nextBusy)

  return (
    <Box
      component="nav"
      aria-label={t('common.pager.label')}
      sx={{
        mt: 2,
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'flex-end',
        columnGap: 2,
      }}
    >
      <ActionButton
        type="button"
        size="small"
        busy={previousBusy}
        unavailable={previousUnavailable}
        onClick={() => {
          setStartedRead({ direction: 'previous', page: page - 1 })
          onPrevious()
        }}
      >
        {t('common.pager.previous')}
      </ActionButton>
      <Typography variant="body2" color="text.secondary" aria-live="polite">
        {t('common.pager.page', { page: page + 1 })}
      </Typography>
      <ActionButton
        type="button"
        size="small"
        busy={nextBusy}
        unavailable={nextUnavailable}
        onClick={() => {
          setStartedRead({ direction: 'next', page: page + 1 })
          onNext()
        }}
      >
        {t('common.pager.next')}
      </ActionButton>
    </Box>
  )
}
