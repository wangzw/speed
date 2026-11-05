/**
 * TenantSwitchAnnouncement: the one `role="status"` region that says which
 * tenant a selection switched to.
 *
 * It exists separately from TenantSwitcher because a host that renders the
 * rows inside its own menu (TenantMenuItems) closes that menu on a pick, and
 * a live region born and gone with a menu is not reliably announced. The host
 * mounts this region once, outside the menu, from its first render -- empty
 * until a selection fills it -- and hands it the switched-to tenant's name.
 *
 * The region is mounted empty and clipped from the start: a region that only
 * receives its text later is present before the text arrives, which is what
 * makes the first announcement reliable. `announcement` chooses whether the
 * confirmation is also painted (`inline`) or stays clipped for its whole
 * lifetime (`visually-hidden`); the region is in the accessibility tree either
 * way.
 */

import type { ReactElement } from 'react'
import Typography from '@mui/material/Typography'
import { visuallyHiddenSx } from '@speed/ui-kit'
import { useTenancyUiTranslation } from './internal/translation.js'

export interface TenantSwitchAnnouncementProps {
  /** The display name of the tenant the last selection switched to, or null
   * before any selection. */
  readonly tenantName: string | null
  /** Whether the confirmation is painted beside its host (`inline`) or stays
   * visually hidden (`visually-hidden`, the default). */
  readonly announcement?: 'inline' | 'visually-hidden'
}

export function TenantSwitchAnnouncement({
  tenantName,
  announcement = 'visually-hidden',
}: TenantSwitchAnnouncementProps): ReactElement {
  const { t } = useTenancyUiTranslation()
  return (
    <Typography
      component="span"
      role="status"
      sx={
        announcement === 'visually-hidden' || tenantName === null
          ? visuallyHiddenSx
          : { color: 'inherit' }
      }
    >
      {tenantName === null
        ? ''
        : t('tenantSwitcher.switchedTo', { tenant: tenantName })}
    </Typography>
  )
}
