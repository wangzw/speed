/**
 * ListSkeleton: the pending-state placeholder of a list read.
 *
 * The component owns the announcement contract -- a role="status"
 * region carrying the caller's accessible name with aria-busy set, so
 * a screen reader hears one loading announcement, never a fake heading
 * -- and the row envelope: one row per entry with the list's own
 * spacing and the divider between rows. The caller owns what each row
 * looks like (the pass-through `rows` entries), because the placeholder
 * rows mirror the content rows of their own list.
 */

import { forwardRef } from 'react'
import type { ReactNode } from 'react'
import Box from '@mui/material/Box'
import type { RootStyleProps } from '../internal/root-style-props.js'

export interface ListSkeletonProps extends RootStyleProps {
  /** The loading announcement's accessible name, already translated. */
  readonly label: string
  /** One entry per placeholder row, contents of the row. */
  readonly rows: readonly ReactNode[]
}

/** The pending-state placeholder of a list read. */
export const ListSkeleton = forwardRef<HTMLDivElement, ListSkeletonProps>(
  function ListSkeleton({ label, rows, sx, className }, ref) {
    return (
      <Box
        ref={ref}
        className={className}
        sx={sx}
        role="status"
        aria-label={label}
        aria-busy="true"
      >
        {rows.map((row, index) => (
          <Box
            key={String(index)}
            sx={{
              py: 1.5,
              ...(index > 0
                ? { borderTop: '1px solid', borderColor: 'divider' }
                : {}),
            }}
          >
            {row}
          </Box>
        ))}
      </Box>
    )
  },
)
