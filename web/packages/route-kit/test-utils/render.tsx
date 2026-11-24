import { render } from '@testing-library/react'
import type { RenderResult } from '@testing-library/react'
import type { ReactNode } from 'react'
import { RouteProvider } from '../src/RouteProvider.js'
import type { RouteNavigationFocus } from '../src/RouteProvider.js'
import type { RouteContext, RoutePresence, RouteTable } from '../src/types.js'

export function setLocation(path: string): void {
  window.history.replaceState(null, '', path)
}

export interface RenderRouteOptions {
  readonly path?: string
  readonly fallback?: (context: RouteContext) => ReactNode
  readonly presence?: RoutePresence
  readonly pending?: (context: RouteContext) => ReactNode
  readonly failed?: (context: RouteContext) => ReactNode
  readonly basename?: string
  readonly navigationFocus?: RouteNavigationFocus
}

export function renderRouteTable(
  table: RouteTable,
  ui: ReactNode,
  options: RenderRouteOptions = {},
): RenderResult {
  const {
    path = '/',
    fallback,
    presence,
    pending,
    failed,
    basename,
    navigationFocus,
  } = options
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
      navigationFocus={navigationFocus}
    >
      {ui}
    </RouteProvider>,
  )
}
