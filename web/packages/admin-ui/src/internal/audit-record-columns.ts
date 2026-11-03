/**
 * The column priorities the two audit-record tables share -- the audit
 * query page and the dashboard's recent-activity list -- so both views
 * shed columns in the same order as their container narrows.
 *
 * The outcome is the column that must survive: triage starts from the
 * failures, so a failed record has to read as failed on the narrowest
 * table that still shows any prioritized column (a 768px overview). It
 * therefore ranks highest and stays visible from the container's `sm`
 * tier (600px). The object (the route the record acted on) is the first
 * column shed, below `lg` (1200px): the action already names what
 * happened, and the route is recoverable from the record's detail page.
 * The actor follows at `md` (900px). Time and action carry no priority
 * and never hide.
 */

import type { DataTableColumnPriority } from '@speed/ui-kit'

/** The shared priority per non-anchor audit-record column. */
export const AUDIT_RECORD_COLUMN_PRIORITY = {
  object: 'low',
  actor: 'medium',
  outcome: 'high',
} as const satisfies Record<string, DataTableColumnPriority>
