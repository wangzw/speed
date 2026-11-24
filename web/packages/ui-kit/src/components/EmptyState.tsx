/**
 * The three stock empty/blocked/error placeholders. `headingLevel` defaults
 * to 'h6' for compatibility, not because it is usually correct: the host
 * must pass the level that continues the page's order.
 */

import type { ReactNode } from 'react'
import type { SxProps, Theme } from '@mui/material/styles'
import Box from '@mui/material/Box'
import Typography from '@mui/material/Typography'
import { useUiKitTranslation } from '../internal/translation.js'
import { EmptyBoxIcon, ErrorIcon, LockIcon } from '../internal/icons.js'

/** Which stock placeholder to render. */
export type EmptyStateVariant = 'empty' | 'noPermission' | 'error'

/** The semantic heading levels EmptyState's title can render as. */
export type EmptyStateHeadingLevel = 'h1' | 'h2' | 'h3' | 'h4' | 'h5' | 'h6'

/** The `headingLevel` value that renders no heading, inside one `role="status"` region. */
export type EmptyStateNoHeading = 'none'

export interface EmptyStateProps {
  /** Which stock placeholder to render; defaults to 'empty'. */
  readonly variant?: EmptyStateVariant
  /** Overrides the variant's built-in title. */
  readonly title?: ReactNode
  /** Overrides the variant's built-in description. */
  readonly description?: ReactNode
  /** Optional action (a Button, a link); rendered under the description. */
  readonly action?: ReactNode
  /** Replaces the variant's stock icon; keep it decorative. */
  readonly icon?: ReactNode
  /**
   * The real heading element the title renders as, or `'none'` for one
   * `role="status"` region with no heading. Defaults to 'h6' for backward
   * compatibility.
   */
  readonly headingLevel?: EmptyStateHeadingLevel | EmptyStateNoHeading
  /** Extra styling applied to the placeholder box. */
  readonly sx?: SxProps<Theme>
}

const VARIANT_ICONS = {
  empty: EmptyBoxIcon,
  noPermission: LockIcon,
  error: ErrorIcon,
} as const

/** The stock placeholder block: icon, title, description and optional action. */
export function EmptyState({
  variant = 'empty',
  title,
  description,
  action,
  icon,
  headingLevel = 'h6',
  sx,
}: EmptyStateProps) {
  const { t } = useUiKitTranslation()
  const StockIcon = VARIANT_ICONS[variant]
  const headerless = headingLevel === 'none'
  return (
    <Box
      role={headerless ? 'status' : undefined}
      sx={{
        display: 'flex',
        flexDirection: 'column',
        alignItems: 'center',
        textAlign: 'center',
        gap: 1.5,
        paddingY: 6,
        paddingX: 3,
        ...sx,
      }}
    >
      <Box sx={{ color: 'text.secondary' }}>{icon ?? <StockIcon />}</Box>
      {/* Never a `<p>`: styled like a heading it trips axe's p-as-heading rule. */}
      <Typography
        variant="h6"
        component={headerless ? 'div' : headingLevel}
      >
        {title ?? t(`emptyState.${variant}.title`)}
      </Typography>
      <Typography variant="body2" color="text.secondary" sx={{ maxWidth: '46ch' }}>
        {description ?? t(`emptyState.${variant}.description`)}
      </Typography>
      {action !== undefined && <Box sx={{ marginTop: 1 }}>{action}</Box>}
    </Box>
  )
}
