/**
 * Shared DOM harness for route-kit's tests.
 *
 * A provider mounts a BrowserRouter, which reads `window.location` at mount,
 * so the first match is only deterministic once the URL is pinned. This
 * pins it and mounts the unit inside a real RouteProvider -- the same
 * provider/hook composition a host assembles -- rather than re-wrapping the
 * provider by hand in every suite. jsdom keeps one window across a file, so
 * every render resets the path first.
 */

import { render } from '@testing-library/react'
import type { RenderResult } from '@testing-library/react'
import type { ReactNode } from 'react'
import { RouteProvider } from '../src/RouteProvider.js'
import type { RouteContext, RoutePresence, RouteTable } from '../src/types.js'

/** Move jsdom's address to `path` before a render reads it. */
export function setLocation(path: string): void {
  window.history.replaceState(null, '', path)
}

export interface RenderRouteOptions {
  /** The path the provider boots at; defaults to the root. */
  readonly path?: string
  /** The unmatched-URL view; defaults to a probe rendering the missed path. */
  readonly fallback?: (context: RouteContext) => ReactNode
  /** The host's feature-presence answer, for gated tables. */
  readonly presence?: RoutePresence
  /** The view a gated path renders while the predicate is pending. */
  readonly pending?: (context: RouteContext) => ReactNode
  /** The view a gated path renders when the predicate failed. */
  readonly failed?: (context: RouteContext) => ReactNode
  /** The path prefix the provider installs its router under. */
  readonly basename?: string
}

/** Mount `ui` inside a RouteProvider over `table`, at `path`. */
export function renderRouteTable(
  table: RouteTable,
  ui: ReactNode,
  options: RenderRouteOptions = {},
): RenderResult {
  const { path = '/', fallback, presence, pending, failed, basename } = options
  const fallbackView =
    fallback ??
    ((context: RouteContext): ReactNode => `no route matched: ${context.path}`)
  setLocation(path)
  return render(
    <RouteProvider
      table={table}
      fallback={fallbackView}
      presence={presence}
      pending={pending}
      failed={failed}
      basename={basename}
    >
      {ui}
    </RouteProvider>,
  )
}
