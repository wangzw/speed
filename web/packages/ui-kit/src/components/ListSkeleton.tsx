/**
 * The pending-state placeholder of a list read: the loading announcement and
 * the row envelope; the caller owns each row's content.
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
