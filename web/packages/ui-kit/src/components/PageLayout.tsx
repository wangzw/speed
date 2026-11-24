/**
 * The page frame: header, then content, inside an inline-size container so
 * the page's rules follow its own width rather than the viewport.
 */

import type { ReactNode } from 'react'
import type { SxProps, Theme } from '@mui/material/styles'
import Box from '@mui/material/Box'
import { PageHeader } from './PageHeader.js'
import type { PageHeaderBreadcrumb } from './PageHeader.js'

export interface PageLayoutProps {
  /** The page title; rendered as the page's single h1. */
  readonly title: ReactNode
  /** Optional supporting paragraph under the title. */
  readonly description?: ReactNode
  /** Optional breadcrumb trail above the title. */
  readonly breadcrumbs?: readonly PageHeaderBreadcrumb[]
  /** Optional trailing actions in the header row. */
  readonly actions?: ReactNode
  /** The page's content, rendered below the header. */
  readonly children?: ReactNode
  /** Extra styling for the frame's root. */
  readonly sx?: SxProps<Theme>
}

/** The page frame: the header, then the page content, inside an inline-size container. */
export function PageLayout({
  title,
  description,
  breadcrumbs,
  actions,
  children,
  sx,
}: PageLayoutProps) {
  return (
    <Box
      component="section"
      sx={{
        display: 'flex',
        flexDirection: 'column',
        gap: 4,
        width: '100%',
        containerType: 'inline-size',
        ...sx,
      }}
    >
      <PageHeader
        title={title}
        description={description}
        breadcrumbs={breadcrumbs}
        actions={actions}
      />
      {children}
    </Box>
  )
}
