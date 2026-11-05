/**
 * PermissionName: one permission rendered the way the role page presents
 * it -- the catalog's readable description as the primary text, the
 * machine-readable code beneath it as secondary information, and a
 * visible "high risk" badge when the declaring module marks the
 * permission dangerous.
 *
 * A permission the catalog does not describe -- a grant left from a
 * vocabulary this process no longer carries -- falls back to the bare
 * code, or to the `resource/action` pair the grant carries, so the row is
 * never blank.
 */

import Box from '@mui/material/Box'
import Chip from '@mui/material/Chip'
import Typography from '@mui/material/Typography'
import { useAdminUiTranslation } from '../translation.js'

export interface PermissionNameProps {
  /** The permission code, or the `resource/action` fallback for a grant
   * whose code the catalog cannot name. */
  readonly code: string
  /** The catalog's readable description; empty when the catalog knows no
   * entry for the code. */
  readonly description?: string
  /** Whether the permission is dangerous, as the catalog reports it. */
  readonly dangerous?: boolean
  /**
   * Render the code as the primary line even when a description exists.
   * The grant table keeps the readable name primary; a caller that must
   * lead with the code (a diagnostic) can ask for it.
   */
  readonly codeFirst?: boolean
}

export function PermissionName({
  code,
  description,
  dangerous = false,
  codeFirst = false,
}: PermissionNameProps) {
  const { t } = useAdminUiTranslation()
  const hasDescription = description !== undefined && description !== ''
  const primary = codeFirst || !hasDescription ? code : description
  return (
    <Box>
      <Box
        sx={{
          display: 'flex',
          flexWrap: 'wrap',
          alignItems: 'center',
          columnGap: 1,
          rowGap: 0.5,
        }}
      >
        <Typography variant="body2" sx={{ whiteSpace: 'normal' }}>
          {primary}
        </Typography>
        {dangerous && (
          <Chip
            size="small"
            variant="outlined"
            color="warning"
            label={t('permissions.catalog.dangerous')}
          />
        )}
      </Box>
      {hasDescription && !codeFirst && (
        <Typography
          variant="caption"
          color="text.secondary"
          sx={{ display: 'block', fontFamily: 'monospace' }}
        >
          {code}
        </Typography>
      )}
    </Box>
  )
}

/** The permission groups, in the order the catalog reference presents
 * them. A group the declaring module named differently is folded into
 * `other` rather than dropped. */
export const PERMISSION_GROUPS = [
  'authorization',
  'membership',
  'identity',
  'settings',
  'other',
] as const

export type PermissionGroup = (typeof PERMISSION_GROUPS)[number]

/** The group a permission belongs to, given the catalog's reported value. */
export function permissionGroupOf(group: string): PermissionGroup {
  const normalized = group.trim().toLowerCase()
  return (PERMISSION_GROUPS as readonly string[]).includes(normalized)
    ? (normalized as PermissionGroup)
    : 'other'
}

/** The admin-ui namespace key naming one group's heading. */
export function permissionGroupKey(group: PermissionGroup): string {
  return `permissions.catalog.group.${group}`
}
