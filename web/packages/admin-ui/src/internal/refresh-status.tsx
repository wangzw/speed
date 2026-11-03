/**
 * RefreshStatus: the update time and the completion announcement the audit
 * query page and the dashboard render in their refresh action group.
 *
 * The update time is static text before the refresh control: the oldest
 * successful settle time among the page's body reads, formatted in the
 * active language and explicitly in UTC with seconds and the zone name. It
 * renders nothing until a read has settled.
 *
 * The announcement is one visually hidden `role="status"` region mounted
 * with the page. It is emptied when the refresh action is clicked and
 * filled once every re-read read settles successfully, so repeating the
 * same text still reaches assistive technology. The clip recipe keeps the
 * region live: `display: none` would remove it from the accessibility tree.
 */

import Box from '@mui/material/Box'
import Typography from '@mui/material/Typography'
import { formatInstant } from '@speed/i18n'
import { visuallyHiddenSx } from '@speed/ui-kit'
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
  const { t, i18n } = useAdminUiTranslation()
  return (
    <>
      {updatedAt === undefined ? null : (
        <Typography component="span" variant="body2" color="text.secondary">
          {t('common.updatedAt', {
            time: formatInstant(updatedAt, i18n.language, {
              timeZone: 'UTC',
              style: 'long',
            }),
          })}
        </Typography>
      )}
      <Box component="span" role="status" sx={visuallyHiddenSx}>
        {announcements > 0 ? t('common.refreshed') : ''}
      </Box>
    </>
  )
}
