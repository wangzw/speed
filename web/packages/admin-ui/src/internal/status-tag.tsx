/**
 * The shared status tag: one chip with an optional caption line. The tone
 * vocabulary is neutral default, success for a good end, and info for a
 * state still in progress.
 */

import Box from '@mui/material/Box'
import Chip from '@mui/material/Chip'
import Typography from '@mui/material/Typography'
import type { ReactNode } from 'react'

/** The chip tones the tag offers, drawn from the theme palette. */
export type StatusTone = 'default' | 'success' | 'info'

export interface StatusTagProps {
  /** The state's translated label. */
  readonly label: string
  /** The palette role the chip takes; default is the neutral chip. */
  readonly tone?: StatusTone
  /** A second line under the chip, such as when the state was reached. */
  readonly note?: ReactNode
}

export function StatusTag({ label, tone = 'default', note }: StatusTagProps) {
  return (
    <Box
      sx={{
        display: 'inline-flex',
        flexDirection: 'column',
        alignItems: 'flex-start',
        gap: 0.25,
      }}
    >
      <Chip size="small" variant="outlined" color={tone} label={label} />
      {note !== undefined && (
        <Typography component="span" variant="caption" color="text.secondary">
          {note}
        </Typography>
      )}
    </Box>
  )
}
