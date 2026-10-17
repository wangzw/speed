/**
 * The column priorities the two audit-record tables share -- the audit
 * query page and the dashboard's recent-activity list -- so both views
 * shed columns in the same order as their container narrows.
 *
 * A record's identity is its action together with its object: the journal
 * records the HTTP method as the action and the route as the object, so
 * either alone does not say what happened. The object therefore ranks
 * above the actor and stays visible from the container's `sm` tier
 * (600px); the actor follows at `md` (900px), and the outcome -- almost
 * always a success on these surfaces -- is the first column shed, at `lg`
 * (1200px). Time and action carry no priority and never hide.
 */

import type { DataTableColumnPriority } from '@speed/ui-kit'

/** The shared priority per non-anchor audit-record column. */
export const AUDIT_RECORD_COLUMN_PRIORITY = {
  object: 'high',
  actor: 'medium',
  outcome: 'low',
} as const satisfies Record<string, DataTableColumnPriority>
