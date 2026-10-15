/**
 * hooks.ts -- the read side (useNavItems) and the imperative handle
 * (useNavigate). useNavItems is asserted to follow the live URL (its
 * selection flips after a link click, driven by the same table the view
 * matches), useNavigate to push history and re-render the view, and both to
 * fail fast outside a provider.
 */

import { fireEvent, renderHook, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import type { ReactElement } from 'react'
import { renderRouteTable } from '../test-utils/render.js'
import { RouteLink } from './RouteLink.js'
import { RouteView } from './RouteView.js'
import { useNavItems, useNavigate } from './hooks.js'
import type { RouteTable } from './types.js'

function Nav(): ReactElement {
  const items = useNavItems()
  return (
    <ul>
      {items.map((item) => (
        <li
          key={item.id}
          data-testid={`nav-${item.id}`}
          data-selected={String(item.selected)}
        >
          <RouteLink to={item.href}>{item.label}</RouteLink>
        </li>
      ))}
    </ul>
  )
}

describe('useNavItems', () => {
  it('derives entries from labeled declarations and marks the current one', () => {
    const table: RouteTable = [
      { path: '/', view: () => null, label: 'Home' },
      { path: '/cases', view: () => null, label: 'Cases' },
      { path: '/cases/:caseId', view: () => null },
    ]
    renderRouteTable(table, <Nav />, { path: '/cases' })
    expect(screen.getByTestId('nav-/cases')).toHaveAttribute(
      'data-selected',
      'true',
    )
    expect(screen.getByTestId('nav-/')).toHaveAttribute('data-selected', 'false')
    // The unlabeled child route contributes no entry.
    expect(screen.queryByTestId('nav-/cases/:caseId')).not.toBeInTheDocument()
  })

  it('re-derives the selection after navigation', async () => {
    const table: RouteTable = [
      { path: '/', view: () => <span>home</span>, label: 'Home' },
      { path: '/cases', view: () => <span>case list</span>, label: 'Cases' },
    ]
    renderRouteTable(
      table,
      <>
        <Nav />
        <RouteView />
      </>,
    )
    expect(screen.getByTestId('nav-/')).toHaveAttribute('data-selected', 'true')

    fireEvent.click(screen.getByRole('link', { name: 'Cases' }))
    await screen.findByText('case list')

    expect(screen.getByTestId('nav-/cases')).toHaveAttribute(
      'data-selected',
      'true',
    )
    expect(screen.getByTestId('nav-/')).toHaveAttribute('data-selected', 'false')
  })

  it('throws outside a RouteProvider', () => {
    const errorSpy = vi.spyOn(console, 'error').mockImplementation(() => {})
    try {
      expect(() => renderHook(() => useNavItems())).toThrow(/RouteProvider/)
    } finally {
      errorSpy.mockRestore()
    }
  })
})

describe('useNavigate', () => {
  function Go(): ReactElement {
    const navigate = useNavigate()
    return <button onClick={() => navigate('/cases')}>go to cases</button>
  }

  it('pushes the target path and re-renders the view', async () => {
    const table: RouteTable = [
      { path: '/', view: () => <span>home</span> },
      { path: '/cases', view: () => <span>case list</span> },
    ]
    renderRouteTable(
      table,
      <>
        <Go />
        <RouteView />
      </>,
    )
    expect(screen.getByText('home')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: 'go to cases' }))

    expect(await screen.findByText('case list')).toBeInTheDocument()
    expect(window.location.pathname).toBe('/cases')
  })
})
