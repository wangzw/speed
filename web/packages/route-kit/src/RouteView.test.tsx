/**
 * RouteView.tsx -- rendering the matched page or the fallback. Each case
 * pins a distinct decision: which declaration renders, what context its view
 * receives (the captured params), that the first match wins, that an
 * unmatched URL reaches the fallback with its own path, and that `permission`
 * is inert data this package never acts on.
 */

import { screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import type { ReactElement } from 'react'
import { renderRouteTable } from '../test-utils/render.js'
import { RouteView } from './RouteView.js'
import type { RouteContext, RouteTable } from './types.js'

describe('RouteView', () => {
  it('renders the view of the declaration the URL matches', () => {
    const table: RouteTable = [
      { path: '/cases', view: () => <span>case list</span> },
      { path: '/reports', view: () => <span>reports</span> },
    ]
    renderRouteTable(table, <RouteView />, { path: '/reports' })
    expect(screen.getByText('reports')).toBeInTheDocument()
    expect(screen.queryByText('case list')).not.toBeInTheDocument()
  })

  it('passes the captured path params to the view', () => {
    const table: RouteTable = [
      {
        path: '/cases/:caseId',
        view: ({ params }: RouteContext) => (
          <span>{`case ${params.caseId ?? ''}`}</span>
        ),
      },
    ]
    renderRouteTable(table, <RouteView />, { path: '/cases/42' })
    expect(screen.getByText('case 42')).toBeInTheDocument()
  })

  it('renders the child route below a labeled parent, not the parent view', () => {
    const table: RouteTable = [
      { path: '/cases', view: () => <span>case list</span>, label: 'Cases' },
      { path: '/cases/:caseId', view: () => <span>case detail</span> },
    ]
    renderRouteTable(table, <RouteView />, { path: '/cases/42' })
    expect(screen.getByText('case detail')).toBeInTheDocument()
    expect(screen.queryByText('case list')).not.toBeInTheDocument()
  })

  it('renders the fallback with the unmatched path when nothing matches', () => {
    const table: RouteTable = [{ path: '/cases', view: () => null }]
    renderRouteTable(table, <RouteView />, {
      path: '/missing/deep',
      fallback: ({ path }: RouteContext): ReactElement => (
        <span>{`missed ${path}`}</span>
      ),
    })
    expect(screen.getByText('missed /missing/deep')).toBeInTheDocument()
  })

  it('ignores permission: a page requiring one still matches and renders', () => {
    const table: RouteTable = [
      {
        path: '/admin',
        view: () => <span>admin</span>,
        permission: 'admin:read',
      },
    ]
    renderRouteTable(table, <RouteView />, { path: '/admin' })
    expect(screen.getByText('admin')).toBeInTheDocument()
  })
})
