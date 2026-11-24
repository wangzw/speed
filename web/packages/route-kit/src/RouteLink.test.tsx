import { fireEvent, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { renderRouteTable } from '../test-utils/render.js'
import { RouteLink } from './RouteLink.js'
import { RouteView } from './RouteView.js'
import type { RouteTable } from './types.js'

describe('RouteLink', () => {
  it('renders an anchor whose href is the target path', () => {
    const table: RouteTable = [{ path: '/', view: () => null }]
    renderRouteTable(table, <RouteLink to="/cases">Cases</RouteLink>)
    expect(screen.getByRole('link', { name: 'Cases' })).toHaveAttribute(
      'href',
      '/cases',
    )
  })

  it('navigates on click without reloading the page', async () => {
    const table: RouteTable = [
      { path: '/', view: () => <span>home view</span> },
      { path: '/cases', view: () => <span>case list</span> },
    ]
    renderRouteTable(
      table,
      <>
        <RouteLink to="/cases">Cases</RouteLink>
        <RouteView />
      </>,
    )
    expect(screen.getByText('home view')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('link', { name: 'Cases' }))

    expect(await screen.findByText('case list')).toBeInTheDocument()
    expect(window.location.pathname).toBe('/cases')
  })

  it('includes the provider basename in the href', () => {
    const table: RouteTable = [{ path: '/', view: () => null }]
    renderRouteTable(table, <RouteLink to="/cases">Cases</RouteLink>, {
      path: '/console/',
      basename: '/console',
    })
    expect(screen.getByRole('link', { name: 'Cases' })).toHaveAttribute(
      'href',
      '/console/cases',
    )
  })

  it('forwards the remaining props to the anchor', () => {
    const table: RouteTable = [{ path: '/', view: () => null }]
    renderRouteTable(
      table,
      <RouteLink to="/cases" className="nav-link" aria-current="page">
        Cases
      </RouteLink>,
    )
    expect(screen.getByRole('link', { name: 'Cases' })).toHaveAttribute(
      'class',
      'nav-link',
    )
    expect(screen.getByRole('link', { name: 'Cases' })).toHaveAttribute(
      'aria-current',
      'page',
    )
  })

  it('runs a caller-supplied onClick before navigating', async () => {
    const table: RouteTable = [
      { path: '/', view: () => <span>home view</span> },
      { path: '/cases', view: () => <span>case list</span> },
    ]
    const onClick = vi.fn()
    renderRouteTable(
      table,
      <>
        <RouteLink to="/cases" onClick={onClick}>
          Cases
        </RouteLink>
        <RouteView />
      </>,
    )
    fireEvent.click(screen.getByRole('link', { name: 'Cases' }))
    expect(onClick).toHaveBeenCalledTimes(1)
    expect(await screen.findByText('case list')).toBeInTheDocument()
  })
})
