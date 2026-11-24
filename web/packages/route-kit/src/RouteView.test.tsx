import { screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import type { ReactElement } from 'react'
import { renderRouteTable } from '../test-utils/render.js'
import type { RenderRouteOptions } from '../test-utils/render.js'
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

describe('RouteView with feature gating', () => {
  const fallbackView = ({ path }: RouteContext): ReactElement => (
    <span>{`not found: ${path}`}</span>
  )

  it('renders the gated view when every required feature is present', () => {
    const table: RouteTable = [
      { path: '/objects', view: () => <span>objects</span>, requires: ['objectstore.objects'] },
    ]
    renderRouteTable(table, <RouteView />, {
      path: '/objects',
      presence: { state: 'ready', has: () => true },
      pending: () => <span>waiting</span>,
      failed: () => <span>failed</span>,
    })
    expect(screen.getByText('objects')).toBeInTheDocument()
  })

  it('renders the fallback and never calls the view when a feature is absent', () => {
    const view = vi.fn(() => <span>objects</span>)
    const table: RouteTable = [
      { path: '/objects', view, requires: ['objectstore.objects'] },
    ]
    renderRouteTable(table, <RouteView />, {
      path: '/objects',
      fallback: fallbackView,
      presence: { state: 'ready', has: () => false },
      pending: () => <span>waiting</span>,
      failed: () => <span>failed</span>,
    })
    expect(screen.getByText('not found: /objects')).toBeInTheDocument()
    expect(view).not.toHaveBeenCalled()
  })

  it('renders the fallback when only some required features are present', () => {
    const table: RouteTable = [
      {
        path: '/reports',
        view: () => <span>reports</span>,
        requires: ['reporting.exports', 'reporting.schedules'],
      },
    ]
    renderRouteTable(table, <RouteView />, {
      path: '/reports',
      fallback: fallbackView,
      presence: {
        state: 'ready',
        has: (name) => name === 'reporting.exports',
      },
      pending: () => <span>waiting</span>,
      failed: () => <span>failed</span>,
    })
    expect(screen.getByText('not found: /reports')).toBeInTheDocument()
  })

  it('renders the same fallback for an absent page and an unregistered path', () => {
    const contexts: RouteContext[] = []
    const fallback = (context: RouteContext): ReactElement => {
      contexts.push(context)
      return (
        <p data-testid="fallback">
          {`${context.path} ${JSON.stringify(context.params)}`}
        </p>
      )
    }
    const gated: RouteTable = [
      {
        path: '/reports/:reportId',
        view: () => <span>report</span>,
        requires: ['reporting.exports'],
      },
    ]
    const options: RenderRouteOptions = {
      path: '/reports/r1',
      fallback,
      presence: { state: 'ready', has: () => false },
      pending: () => <span>waiting</span>,
      failed: () => <span>failed</span>,
    }

    const absent = renderRouteTable(gated, <RouteView />, options)
    const absentHtml = absent.container.innerHTML
    absent.unmount()

    const unregistered = renderRouteTable([], <RouteView />, options)
    expect(unregistered.container.innerHTML).toBe(absentHtml)
    expect(contexts).toEqual([
      { path: '/reports/r1', params: {} },
      { path: '/reports/r1', params: {} },
    ])
  })

  it('renders the pending view for a gated path and never the fallback', () => {
    const fallback = vi.fn(fallbackView)
    const pendingView = vi.fn(() => <span>waiting</span>)
    const table: RouteTable = [
      { path: '/objects', view: () => <span>objects</span>, requires: ['objectstore.objects'] },
    ]
    renderRouteTable(table, <RouteView />, {
      path: '/objects',
      fallback,
      presence: { state: 'pending' },
      pending: pendingView,
      failed: () => <span>failed</span>,
    })
    expect(screen.getByText('waiting')).toBeInTheDocument()
    expect(fallback).not.toHaveBeenCalled()
    expect(pendingView).toHaveBeenCalledTimes(1)
  })

  it('still renders the fallback for an unregistered path while pending', () => {
    const fallback = vi.fn(fallbackView)
    const table: RouteTable = [
      { path: '/objects', view: () => <span>objects</span>, requires: ['objectstore.objects'] },
    ]
    renderRouteTable(table, <RouteView />, {
      path: '/nope',
      fallback,
      presence: { state: 'pending' },
      pending: () => <span>waiting</span>,
      failed: () => <span>failed</span>,
    })
    expect(screen.getByText('not found: /nope')).toBeInTheDocument()
    expect(fallback).toHaveBeenCalledTimes(1)
  })

  it('renders the failed view for a gated path', () => {
    const table: RouteTable = [
      { path: '/objects', view: () => <span>objects</span>, requires: ['objectstore.objects'] },
    ]
    renderRouteTable(table, <RouteView />, {
      path: '/objects',
      presence: { state: 'failed' },
      pending: () => <span>waiting</span>,
      failed: () => <span>failed</span>,
    })
    expect(screen.getByText('failed')).toBeInTheDocument()
  })

  it('leaves an ungated table unaffected by any presence value', () => {
    const table: RouteTable = [
      { path: '/', view: () => <span>home</span> },
      { path: '/cases', view: () => <span>cases</span> },
      { path: '/cases/:caseId', view: ({ params }: RouteContext) => <span>{`case ${params.caseId ?? ''}`}</span> },
      { path: '/objects', view: () => <span>objects</span>, permission: 'objects:read' },
    ]
    const paths = ['/', '/cases', '/cases/42', '/objects', '/nope']
    const presences = [
      undefined,
      { state: 'pending' },
      { state: 'failed' },
      { state: 'ready', has: () => true },
      { state: 'ready', has: () => false },
    ] as const

    for (const path of paths) {
      const baseline = renderRouteTable(table, <RouteView />, { path })
      const expected = baseline.container.innerHTML
      baseline.unmount()
      for (const presence of presences) {
        const rendered = renderRouteTable(table, <RouteView />, {
          path,
          presence,
          pending: () => <span>waiting</span>,
          failed: () => <span>failed</span>,
        })
        expect(rendered.container.innerHTML, `${path} with ${presence?.state ?? 'no presence'}`).toBe(expected)
        rendered.unmount()
      }
    }
  })
})
