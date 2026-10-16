/**
 * EmptyState: the three stock empty/blocked/error placeholders.
 *
 * Variants are 'empty' (a space has no data yet), 'noPermission' (the
 * viewer is not allowed to see the content) and 'error' (the content
 * failed to load). Every variant ships built-in bilingual title and
 * description text from the ui-kit namespace (see the resource table in
 * the README); title, description and action are overridable props, and
 * the icon can be swapped wholesale. When a variant's text does not fit
 * the situation (an error with its own retry story), pass the props --
 * the namespace defaults are only ever the fallback.
 *
 * The icon is decorative by contract (aria-hidden): the text carries
 * the meaning, so a custom icon prop should stay decorative too, or
 * come with its own accessible label.
 *
 * The title renders as a REAL heading element (an `h1`-`h6`, never a
 * styled `<div>`), because EmptyState is routinely the only content of
 * a section whose own heading it stands in for (see, for one, every
 * account-ui section's empty/error branch). That heading's LEVEL is
 * therefore page-structure knowledge EmptyState cannot supply on its
 * own -- only the host knows what heading, if any, precedes it on the
 * real page -- so `headingLevel` is a required-in-spirit prop a host
 * embedding this under a real h1/h2 MUST set to the level that
 * continues the page's own order without a skip; it defaults to 'h6'
 * for backward compatibility, not because 'h6' is usually correct.
 * The visual size stays the fixed `variant="h6"` look regardless of
 * `headingLevel` -- MUI's
 * `component` override is what lets the semantic level and the visual
 * style vary independently, the same split this package's other
 * multi-level headings (PageHeader's h1, a section's own h2) already
 * rely on.
 *
 * An EMPTY placeholder is a status, not a section: when it stands under
 * a page heading that already names the space (a "Notifications" h1 with
 * a "No notifications" placeholder below it), a second heading there
 * would enter the page outline as a redundant chapter. A host in that
 * shape passes `headingLevel="none"`, which renders the title as a
 * non-heading element (a plain block, not a `<p>` -- a `<p>` styled like a
 * heading would itself trip axe's p-as-heading rule) and wraps the whole
 * placeholder in one `role="status"`
 * region -- the placeholder is then announced politely without claiming a
 * heading level, and the page's own heading order is left alone. The
 * default stays a heading, so a placeholder that genuinely stands in for
 * a hidden section header is unchanged.
 */

import type { ReactNode } from 'react'
import type { SxProps, Theme } from '@mui/material/styles'
import Box from '@mui/material/Box'
import Typography from '@mui/material/Typography'
import { useUiKitTranslation } from '../internal/translation.js'
import { EmptyBoxIcon, ErrorIcon, LockIcon } from '../internal/icons.js'

export type EmptyStateVariant = 'empty' | 'noPermission' | 'error'

/** The semantic heading levels EmptyState's title can render as. */
export type EmptyStateHeadingLevel = 'h1' | 'h2' | 'h3' | 'h4' | 'h5' | 'h6'

/**
 * The `headingLevel` value that renders the title as no heading at all:
 * a plain block inside one `role="status"` region, for a placeholder
 * that must not enter the page heading outline (see the file header).
 */
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
  /** Replaces the variant's stock icon. Keep it decorative (see header note). */
  readonly icon?: ReactNode
  /**
   * The real heading element (`h1`-`h6`) the title renders as -- whatever
   * level continues the real page's own heading order at the point this
   * EmptyState appears (see this file's header comment) -- or `'none'`
   * for a placeholder that must not enter the heading outline, which
   * renders the title as a plain block inside one `role="status"`
   * region. Defaults to 'h6' for backward compatibility.
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

/**
 * The stock placeholder block: centered icon, title and description
 * from the ui-kit namespace unless overridden, optional action slot.
 */
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
      // A headerless placeholder is one polite status region -- it appears
      // where a loading branch was, so a screen reader announces it without
      // it claiming a heading level (see the file header).
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
      {/* A headerless title renders as a `<div>`, never a `<p>`: a `<p>`
          styled like a heading (the h6 look this keeps) trips axe's
          p-as-heading rule -- a real violation, not the exemption the
          headerless form exists to earn. */}
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
