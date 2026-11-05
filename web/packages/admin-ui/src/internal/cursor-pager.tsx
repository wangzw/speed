/**
 * CursorPager: the shared previous/next control for cursor-paged lists.
 *
 * Both the member list and the invitation list page by an opaque cursor the
 * server returns as `next_cursor`: there is no total page count to show, so
 * the control names the page number the operator is on and offers the two
 * directions. The page index is zero-based in state and one-based in the
 * label. The control renders nothing on a single-page list, so a caller may
 * place it unconditionally below the table; `busy` disables both directions
 * while a page change would read a cursor that is not the settled page's --
 * while the section's own writes are in flight, and while the page the control
 * was just moved to is still loading under a kept previous result.
 *
 * The labels come from the admin-ui namespace's shared pager keys, so the
 * two lists page with one wording and the host overrides it once.
 */

import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import Typography from '@mui/material/Typography'
import { useAdminUiTranslation } from './translation.js'

export interface CursorPagerProps {
  /** The page the list is on, zero-based. */
  readonly page: number
  /** Whether the server reported a next page. */
  readonly hasNext: boolean
  /** Whether a write is in flight; both directions are then disabled. */
  readonly busy?: boolean
  /** Moves to the page before the current one. */
  readonly onPrevious: () => void
  /** Moves to the page the server's next cursor names. */
  readonly onNext: () => void
}

export function CursorPager({
  page,
  hasNext,
  busy = false,
  onPrevious,
  onNext,
}: CursorPagerProps) {
  const { t } = useAdminUiTranslation()
  if (page === 0 && !hasNext) {
    return null
  }
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
      <Button
        type="button"
        variant="outlined"
        size="small"
        disabled={busy || page === 0}
        onClick={onPrevious}
      >
        {t('common.pager.previous')}
      </Button>
      <Typography variant="body2" color="text.secondary" aria-live="polite">
        {t('common.pager.page', { page: page + 1 })}
      </Typography>
      <Button
        type="button"
        variant="outlined"
        size="small"
        disabled={busy || !hasNext}
        onClick={onNext}
      >
        {t('common.pager.next')}
      </Button>
    </Box>
  )
}
