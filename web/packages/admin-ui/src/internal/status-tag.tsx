/**
 * The shared status tag: one chip, optionally with a caption line under it.
 *
 * Both the member table's active/suspended state and the invitation table's
 * four-value state render through this component, so the two surfaces use
 * the same shape and tone vocabulary. A caption carries the instant a
 * terminal state was reached; it is the caller's translated text.
 *
 * The tone vocabulary is the three the management surfaces use: the neutral
 * default, success for a good end, and info for a state still in progress.
 * The invitation lifecycle maps pending to info, accepted to success, and
 * expired and revoked to the neutral default -- a terminal invitation is
 * history, so it never takes the warning or error tone.
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
