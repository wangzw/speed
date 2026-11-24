/** RouteLink: a clickable link to a basename-relative target path, forwarding every other prop to react-router-dom's Link. */

import { Link } from 'react-router-dom'
import type { LinkProps } from 'react-router-dom'
import type { ReactElement } from 'react'

/** The link's props: router-native props, minus the `to` narrowed to a path string. */
export interface RouteLinkProps extends Omit<LinkProps, 'to'> {
  /** The target path, basename-relative (a declaration's `path` or a nav item's `id`, not its `href`). */
  readonly to: string
}

/** Renders a react-router-dom Link to `to`, forwarding the rest. */
export function RouteLink({ to, ...rest }: RouteLinkProps): ReactElement {
  return <Link to={to} {...rest} />
}
