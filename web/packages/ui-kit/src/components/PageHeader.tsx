/**
 * The single page-level heading block. Visible text is host content; the
 * only built-in strings are the breadcrumb landmark's name and the
 * collapse-expand label, which MUI would otherwise ship as an English literal.
 */

import type { MouseEventHandler, ReactNode } from 'react'
import type { SxProps, Theme } from '@mui/material/styles'
import Box from '@mui/material/Box'
import Breadcrumbs from '@mui/material/Breadcrumbs'
import Link from '@mui/material/Link'
import Typography from '@mui/material/Typography'
import { useUiKitTranslation } from '../internal/translation.js'
import { ActionRow } from './ActionRow.js'

/**
 * One breadcrumb step. A crumb with `href` renders as a link, without it as
 * plain text; the last crumb is marked `aria-current="page"` either way.
 */
export interface PageHeaderBreadcrumb {
  /** The crumb's text; host content, already translated. */
  readonly label: ReactNode
  /**
   * The crumb's destination. Set to render the crumb as a link;
   * absent, the crumb renders as plain text.
   */
  readonly href?: string
  /** Click handler for link crumbs; runs when the crumb link is clicked. */
  readonly onClick?: MouseEventHandler<HTMLAnchorElement>
}

export interface PageHeaderProps {
  /** The page title; rendered as an h1 in the heading-4 role. */
  readonly title: ReactNode
  /** Optional supporting paragraph under the title. */
  readonly description?: ReactNode
  /** Optional breadcrumb trail above the title, inside a named navigation landmark. */
  readonly breadcrumbs?: readonly PageHeaderBreadcrumb[]
  /** Optional trailing actions (primary buttons, menus). */
  readonly actions?: ReactNode
  /** Extra styling applied to the header box. */
  readonly sx?: SxProps<Theme>
}

/** The page heading block: breadcrumbs, title, description and trailing actions. */
export function PageHeader({ title, description, actions, breadcrumbs, sx }: PageHeaderProps) {
  const { t } = useUiKitTranslation()
  const crumbs = breadcrumbs ?? []
  const lastCrumbIndex = crumbs.length - 1
  return (
    <Box component="header" sx={sx}>
      {crumbs.length > 0 && (
        <Breadcrumbs
          aria-label={t('pageHeader.breadcrumbNav')}
          // MUI's stock 'Show path' literal must never ship.
          expandText={t('pageHeader.showFullPath')}
          sx={{ marginBottom: 1.5 }}
        >
          {crumbs.map((crumb, index) => {
            const isCurrentPage = index === lastCrumbIndex
            const currentMark = isCurrentPage ? ('page' as const) : undefined
            if (crumb.href !== undefined) {
              return (
                <Link
                  key={index}
                  href={crumb.href}
                  onClick={crumb.onClick}
                  aria-current={currentMark}
                >
                  {crumb.label}
                </Link>
              )
            }
            return (
              <span key={index} aria-current={currentMark}>
                {crumb.label}
              </span>
            )
          })}
        </Breadcrumbs>
      )}
      <Box
        sx={{
          display: 'flex',
          alignItems: 'flex-start',
          justifyContent: 'space-between',
          gap: 2,
          flexWrap: 'wrap',
        }}
      >
        <Box sx={{ minWidth: 0 }}>
          <Typography component="h1" variant="h4">
            {title}
          </Typography>
          {description !== undefined && (
            <Typography
              variant="body2"
              color="text.secondary"
              sx={{ marginTop: 0.5, maxWidth: '70ch' }}
            >
              {description}
            </Typography>
          )}
        </Box>
        {actions !== undefined && (
          <ActionRow sx={{ flexShrink: 0 }}>{actions}</ActionRow>
        )}
      </Box>
    </Box>
  )
}
