/**
 * The console page-layout primitives: the content-area width divergence
 * and the detail page's main/side split.
 *
 * The layout design gives the console pages two content widths: a
 * register or dashboard takes the whole content area (its width is its
 * information), while a form (the settings page) sits in a centred,
 * limited column so its controls do not stretch with the viewport. Both
 * live here so the pages do not each re-derive them.
 *
 * The detail page's skeleton -- a main column carrying the subject and a
 * fixed-width side column carrying its metadata -- is the same shape for
 * every detail page, so DetailLayout owns it: a flex row at wide widths
 * (main grows, side is fixed) that stacks to a single column below the
 * `md` breakpoint, the same viewport-driven split the shell uses.
 */

import type { ReactNode } from 'react'
import Box from '@mui/material/Box'
import Typography from '@mui/material/Typography'

/** The fixed side-column width at `md` and up (16rem ≈ 256px). */
const SIDE_COLUMN_WIDTH = '16rem'

export interface DetailLayoutProps {
  /** The main column: the page's subject and its controls. */
  readonly main: ReactNode
  /** The side column: the subject's metadata. */
  readonly side: ReactNode
}

/** A detail page's main + fixed-width side skeleton, stacked below `md`. */
export function DetailLayout({ main, side }: DetailLayoutProps) {
  return (
    <Box
      sx={{
        display: 'flex',
        flexDirection: { xs: 'column', md: 'row' },
        gap: 3,
        alignItems: 'flex-start',
      }}
    >
      <Box sx={{ flex: 1, minWidth: 0 }}>{main}</Box>
      <Box
        component="aside"
        sx={{
          width: { xs: '100%', md: SIDE_COLUMN_WIDTH },
          flexShrink: 0,
        }}
      >
        {side}
      </Box>
    </Box>
  )
}

export interface FormWidthProps {
  readonly children: ReactNode
}

export interface DefinitionRowProps {
  /** The field's label. */
  readonly label: string
  /** The field's value, rendered as text. */
  readonly value: ReactNode
}

/** One label/value row of a detail page's definition list. */
export function DefinitionRow({ label, value }: DefinitionRowProps) {
  return (
    <Box
      sx={{
        display: 'flex',
        columnGap: 2,
        rowGap: 0.5,
        flexWrap: 'wrap',
        paddingY: 1,
        borderBottom: 1,
        borderColor: 'divider',
      }}
    >
      <Typography
        component="dt"
        variant="body2"
        color="text.secondary"
        sx={{ minWidth: '8ch' }}
      >
        {label}
      </Typography>
      <Typography component="dd" variant="body2" sx={{ margin: 0, wordBreak: 'break-all' }}>
        {value}
      </Typography>
    </Box>
  )
}

/**
 * The limited-width container a console form sits in: it never exceeds
 * its column (a narrow viewport is 100%), and at `sm` and up it is capped
 * to a comfortable reading width so inputs do not stretch with the
 * viewport.
 */
export function FormWidth({ children }: FormWidthProps) {
  return <Box sx={{ maxWidth: { sm: '45rem' }, width: '100%' }}>{children}</Box>
}
