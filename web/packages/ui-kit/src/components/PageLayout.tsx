/**
 * PageLayout: the page frame a routed page renders its content in.
 *
 * It owns what every page would otherwise restate, and owns nothing else:
 *
 *  - the page's vertical rhythm -- the header block, then the content at
 *    one theme spacing step;
 *  - the width contract -- the page fills the surface the host's shell
 *    gives it. A page never caps its own width here: the outer padding and
 *    any measure belong to the shell, so two pages of one product sit on
 *    the same rails and widen together when the shell's navigation
 *    collapses;
 *  - the containment context -- the frame is an inline-size container, so
 *    a page's own layout responds to the width actually available to it
 *    (the shell's navigation open or collapsed, a narrow panel) rather
 *    than to the viewport. Pages below use the `@` container breakpoints
 *    (the container's `sm`, `md`, ...), never the viewport's.
 *
 * The header is PageHeader: one page, one h1, with the description and the
 * trailing actions it already defines. A page with nothing but a header
 * passes no children.
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
  /** Extra styling for the frame's root (a page-specific exception, not
   * the place to restate the frame's own rules). */
  readonly sx?: SxProps<Theme>
}

/** The page frame: header, then content, inside the containment context
 * every page's own responsive rules resolve against. */
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
