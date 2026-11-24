/**
 * The `role="status"` region that announces which tenant a selection
 * switched to. The host mounts it once, outside the menu, from its first
 * render.
 */

import type { ReactElement } from 'react'
import Typography from '@mui/material/Typography'
import { visuallyHiddenSx } from '@speed/ui-kit'
import { useTenancyUiTranslation } from './internal/translation.js'

/** The announcement region's props: the name of the tenant just switched
 * to, and whether the text is painted inline or kept visually hidden. */
export interface TenantSwitchAnnouncementProps {
  /** The display name of the tenant the last selection switched to, or null
   * before any selection. */
  readonly tenantName: string | null
  /** Whether the confirmation is painted beside its host (`inline`) or stays
   * visually hidden (`visually-hidden`, the default). */
  readonly announcement?: 'inline' | 'visually-hidden'
}

/** Renders the switch confirmation region. */
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
