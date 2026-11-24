import type { DataTableColumnPriority } from '@speed/ui-kit'

/** The shared priority per non-anchor audit-record column. */
export const AUDIT_RECORD_COLUMN_PRIORITY = {
  object: 'low',
  actor: 'medium',
  outcome: 'high',
} as const satisfies Record<string, DataTableColumnPriority>
