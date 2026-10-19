/**
 * RouteLink -- a clickable link to a target path.
 *
 * It renders react-router-dom's Link, so a click pushes the path onto the
 * history without a full page reload; RouteView then re-renders from the new
 * location. The package neither parses nor rewrites `to`: the caller passes
 * a basename-relative path -- a declaration's `path`, or a nav item's `id`
 * -- because the router applies the basename itself. A nav item's `href`
 * already includes it and is meant for a plain anchor.
 *
 * Every other prop is forwarded to the underlying Link -- `className`,
 * `style`, `onClick`, `aria-*`, the router's own `replace`/`state`/`prefetch`
 * and the rest -- so the component can serve as a MUI `component` target or
 * as the anchor in a frame that styles its own links.
 */

import { Link } from 'react-router-dom'
import type { LinkProps } from 'react-router-dom'
import type { ReactElement } from 'react'

/**
 * The link's props: its router-native props, minus the `to` this package
 * narrows to a path string.
 */
export interface RouteLinkProps extends Omit<LinkProps, 'to'> {
  /**
   * The target path, basename-relative like every path this package reads
   * (e.g. a declaration's `path` or a nav item's `id`, not its `href`).
   */
  readonly to: string
}

export function RouteLink({ to, ...rest }: RouteLinkProps): ReactElement {
  return <Link to={to} {...rest} />
}
