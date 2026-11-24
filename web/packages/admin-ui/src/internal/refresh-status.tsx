/**
 * RefreshStatus: the update time and the completion announcement beside a
 * page's refresh control. The announcement region stays clipped rather than
 * `display: none`, which would remove it from the accessibility tree.
 */

import Box from '@mui/material/Box'
import Typography from '@mui/material/Typography'
import { useFormatInstant, visuallyHiddenSx } from '@speed/ui-kit'
import { useAdminUiTranslation } from './translation.js'

export type RefreshStatusProps = {
  /** The oldest successful settle time among the page's reads; absent when none settled. */
  readonly updatedAt: number | undefined
  /** How many refresh completions the page has announced; 0 empties the region. */
  readonly announcements: number
}

/** The refresh action group's update label and its live completion region. */
export function RefreshStatus({
  updatedAt,
  announcements,
}: RefreshStatusProps) {
  const { t } = useAdminUiTranslation()
  const formatTime = useFormatInstant()
  return (
    <>
      {updatedAt === undefined ? null : (
        <Typography component="span" variant="body2" color="text.secondary">
          {t('common.updatedAt', {
            time: formatTime(updatedAt, { style: 'long' }),
          })}
        </Typography>
      )}
      <Box component="span" role="status" sx={visuallyHiddenSx}>
        {announcements > 0 ? t('common.refreshed') : ''}
      </Box>
    </>
  )
}
