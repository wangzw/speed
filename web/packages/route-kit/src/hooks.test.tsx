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
import { useHasRoute, useMatch, useNavItems, useNavigate } from './hooks.js'
import { presentDeclarations } from './presence.js'
import type { RoutePresence, RouteTable } from './types.js'

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

function MatchProbe(): ReactElement {
  const match = useMatch()
  return (
    <span data-testid="match">
      {match === undefined
        ? 'none'
        : `${match.declaration.path}|${match.context.params.id ?? ''}`}
    </span>
  )
}

describe('useMatch', () => {
  const table: RouteTable = [
    { path: '/console/x', view: () => null, label: 'X' },
    { path: '/console/x/:id', view: () => null },
  ]

  it('returns the detail declaration itself, not the prefix ancestor', () => {
    renderRouteTable(table, <MatchProbe />, { path: '/console/x/42' })
    expect(screen.getByTestId('match')).toHaveTextContent('/console/x/:id|42')
  })

  it('returns the declaration for its own exact path', () => {
    renderRouteTable(table, <MatchProbe />, { path: '/console/x' })
    expect(screen.getByTestId('match')).toHaveTextContent('/console/x|')
  })

  it('does not inherit an ancestor for a path with no declaration of its own', () => {
    renderRouteTable(table, <MatchProbe />, { path: '/console/x/42/extra' })
    expect(screen.getByTestId('match')).toHaveTextContent('none')
  })

  it('returns no match for a gated declaration whose features are absent', () => {
    const gatedTable: RouteTable = [
      { path: '/objects', view: () => null, requires: ['objectstore.objects'] },
    ]
    renderRouteTable(gatedTable, <MatchProbe />, {
      path: '/objects',
      presence: { state: 'ready', has: () => false },
      pending: () => null,
      failed: () => null,
    })
    expect(screen.getByTestId('match')).toHaveTextContent('none')
  })
})

function HasRouteProbe(): ReactElement {
  const parameterized = useHasRoute('/cases/42')
  const unregistered = useHasRoute('/nope')
  const absent = useHasRoute('/objects')
  return (
    <span data-testid="has">
      {`${String(parameterized)}|${String(unregistered)}|${String(absent)}`}
    </span>
  )
}

describe('useHasRoute', () => {
  const table: RouteTable = [
    { path: '/', view: () => null },
    { path: '/cases/:caseId', view: () => null },
    { path: '/objects', view: () => null, requires: ['objectstore.objects'] },
  ]

  it('answers true for a parameterized path and false off the table or for an absent page', () => {
    renderRouteTable(table, <HasRouteProbe />, {
      presence: { state: 'ready', has: () => false },
      pending: () => null,
      failed: () => null,
    })
    expect(screen.getByTestId('has')).toHaveTextContent('true|false|false')
  })

  it('answers false for a gated path while the predicate is pending', () => {
    renderRouteTable(table, <HasRouteProbe />, {
      presence: { state: 'pending' },
      pending: () => null,
      failed: () => null,
    })
    expect(screen.getByTestId('has')).toHaveTextContent('true|false|false')
  })
})

describe('navigation and matching share one narrowed surface', () => {
  const table: RouteTable = [
    { path: '/', view: () => null, label: 'Home' },
    {
      path: '/objects',
      view: () => null,
      label: 'Objects',
      requires: ['objectstore.objects'],
    },
    { path: '/cases', view: () => null, label: 'Cases' },
  ]

  const states: readonly RoutePresence[] = [
    { state: 'pending' },
    { state: 'failed' },
    { state: 'ready', has: () => true },
    { state: 'ready', has: () => false },
  ]

  function nameOf(presence: RoutePresence): string {
    if (presence.state !== 'ready') return presence.state
    return `ready, objects ${presence.has('objectstore.objects') ? 'present' : 'absent'}`
  }

  it.each(states.map((presence) => [nameOf(presence), presence] as const))(
    'derives the entries from presentDeclarations when %s',
    (_name, presence) => {
      const expected = presentDeclarations(table, presence)
        .filter((declaration) => declaration.label !== undefined)
        .map((declaration) => `nav-${declaration.path}`)

      renderRouteTable(table, <Nav />, {
        presence,
        pending: () => null,
        failed: () => null,
      })

      const rendered = screen
        .queryAllByTestId(/^nav-/)
        .map((element) => element.getAttribute('data-testid'))
      expect(rendered).toEqual(expected)
    },
  )

  it('shows an absent gated page in neither the navigation nor the exact match', () => {
    function SurfaceProbe(): ReactElement {
      const items = useNavItems()
      const match = useMatch()
      return (
        <span data-testid="surface">
          {`${items.map((item) => item.id).join(',')}|${
            match?.declaration.path ?? 'none'
          }`}
        </span>
      )
    }

    renderRouteTable(
      table,
      <>
        <Nav />
        <SurfaceProbe />
      </>,
      {
        path: '/objects',
        presence: { state: 'ready', has: () => false },
        pending: () => null,
        failed: () => null,
      },
    )

    expect(screen.getByTestId('surface')).toHaveTextContent('/,/cases|none')
    const selected = screen
      .queryAllByTestId(/^nav-/)
      .filter((element) => element.getAttribute('data-selected') === 'true')
    expect(selected).toHaveLength(0)
  })
})
