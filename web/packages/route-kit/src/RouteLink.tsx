/**
 * RouteLink -- a clickable link to a target path.
 *
 * It renders react-router-dom's Link, so a click pushes the path onto the
 * history without a full page reload; RouteView then re-renders from the new
 * location. The package neither parses nor rewrites `to`: the caller passes
 * the path a declaration (or a nav item's `href`) already carries.
 */

import { Link } from 'react-router-dom'
import type { ReactElement, ReactNode } from 'react'

export interface RouteLinkProps {
  /** The target path, e.g. a nav item's `href`. */
  readonly to: string
  readonly children: ReactNode
}

export function RouteLink({ to, children }: RouteLinkProps): ReactElement {
  return <Link to={to}>{children}</Link>
}
