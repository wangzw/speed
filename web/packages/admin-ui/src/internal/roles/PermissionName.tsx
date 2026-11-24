/**
 * PermissionName: description primary, code secondary, and a high-risk badge;
 * an undescribed permission falls back to its code or `resource/action` pair.
 */

import Box from '@mui/material/Box'
import Chip from '@mui/material/Chip'
import Typography from '@mui/material/Typography'
import { useAdminUiTranslation } from '../translation.js'

export interface PermissionNameProps {
  /** The permission code, or the `resource/action` fallback the catalog cannot name. */
  readonly code: string
  /** The catalog's readable description; empty when the catalog knows no entry. */
  readonly description?: string
  /** Whether the permission is dangerous, as the catalog reports it. */
  readonly dangerous?: boolean
  /** Render the code as the primary line even when a description exists. */
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

/** The permission groups in display order; an unknown group folds into `other`. */
export const PERMISSION_GROUPS = [
  'authorization',
  'membership',
  'identity',
  'settings',
  'profile',
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
