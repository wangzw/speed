/**
 * hooks.ts -- the read side (useNavItems) and the imperative handle
 * (useNavigate). useNavItems is asserted to follow the live URL (its
 * selection flips after a link click, driven by the same table the view
 * matches), useNavigate to push history and re-render the view, and both to
 * fail fast outside a provider.
 */

import { fireEvent, renderHook, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import type { MouseEventHandler, ReactElement, ReactNode } from 'react'
import { renderRouteTable } from '../test-utils/render.js'
import { RouteLink } from './RouteLink.js'
import { RouteView } from './RouteView.js'
import {
  useHasRoute,
  useLocationPath,
  useMatch,
  useNavItems,
  useNavigate,
  useRedirect,
  useRouteLinkProps,
} from './hooks.js'
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
          <RouteLink to={item.id}>{item.label}</RouteLink>
        </li>
      ))}
    </ul>
  )
}

/** A frame that renders each entry as a plain anchor, the README's recipe. */
function PlainNav(): ReactElement {
  const items = useNavItems()
  return (
    <ul>
      {items.map((item) => (
        <li key={item.id} data-testid={`plain-${item.id}`}>
          <a href={item.href} onClick={item.onClick}>
            {item.label}
          </a>
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

  it('includes the basename in href while navigating without doubling it', async () => {
    const table: RouteTable = [
      { path: '/', view: () => <span>home</span>, label: 'Home' },
      { path: '/cases', view: () => <span>case list</span>, label: 'Cases' },
    ]
    renderRouteTable(
      table,
      <>
        <PlainNav />
        <RouteView />
      </>,
      { path: '/console/', basename: '/console' },
    )

    // The anchor href is the served URL a new tab or copied link opens.
    expect(screen.getByRole('link', { name: 'Cases' })).toHaveAttribute(
      'href',
      '/console/cases',
    )
    expect(screen.getByRole('link', { name: 'Home' })).toHaveAttribute(
      'href',
      '/console',
    )

    // The in-app handler navigates the declaration path, so the router
    // applies the prefix exactly once.
    fireEvent.click(screen.getByRole('link', { name: 'Cases' }))
    await screen.findByText('case list')
    expect(window.location.pathname).toBe('/console/cases')
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

  // Expectations are literal, one per presence state: deriving them from
  // presentDeclarations would let a broken narrowing rule agree with itself.
  // The gated entry sits between the two ungated ones in table order.
  const cases: readonly (readonly [string, RoutePresence, readonly string[]])[] = [
    ['pending', { state: 'pending' }, ['nav-/', 'nav-/cases']],
    ['failed', { state: 'failed' }, ['nav-/', 'nav-/cases']],
    [
      'ready with the feature present',
      { state: 'ready', has: () => true },
      ['nav-/', 'nav-/objects', 'nav-/cases'],
    ],
    [
      'ready with the feature absent',
      { state: 'ready', has: () => false },
      ['nav-/', 'nav-/cases'],
    ],
  ]

  it.each(cases)(
    'derives the navigation entries when presence is %s',
    (_name, presence, expected) => {
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

describe('useNavItems click handler', () => {
  function NavButtons(): ReactElement {
    const items = useNavItems()
    return (
      <div>
        {items.map((item) => (
          <button
            key={item.id}
            data-testid={`go-${item.id}`}
            onClick={item.onClick}
          >
            {item.label}
          </button>
        ))}
      </div>
    )
  }

  const table: RouteTable = [
    { path: '/', view: () => <span>home</span>, label: 'Home' },
    { path: '/cases', view: () => <span>case list</span>, label: 'Cases' },
  ]

  it('navigates in-app on a plain primary click', async () => {
    renderRouteTable(
      table,
      <>
        <NavButtons />
        <RouteView />
      </>,
    )
    fireEvent.click(screen.getByTestId('go-/cases'))
    expect(await screen.findByText('case list')).toBeInTheDocument()
    expect(window.location.pathname).toBe('/cases')
  })

  it('leaves a modified click to the browser', () => {
    renderRouteTable(
      table,
      <>
        <NavButtons />
        <RouteView />
      </>,
    )
    // The return value is false when the event's default was prevented.
    expect(fireEvent.click(screen.getByTestId('go-/cases'), { metaKey: true })).toBe(
      true,
    )
    expect(window.location.pathname).toBe('/')
    expect(screen.getByText('home')).toBeInTheDocument()
  })

  /**
   * The entry shape a shell frame accepts, mirrored from
   * @speed/layout-kit's AppShellNavItem because this package imports no
   * sibling. A NavItem must stay assignable to it without a shared type
   * edge, which only the type checker can assert.
   */
  interface FrameNavItem {
    readonly id: string
    readonly label: ReactNode
    readonly icon?: ReactNode
    readonly href?: string
    readonly onClick?: MouseEventHandler<HTMLElement>
    readonly selected?: boolean
    readonly children?: readonly FrameNavItem[]
  }

  it('stays assignable to the frame entry shape', () => {
    function FrameProbe(): ReactElement {
      const frameItems: readonly FrameNavItem[] = useNavItems()
      return <span data-testid="frame-count">{frameItems.length}</span>
    }
    renderRouteTable(table, <FrameProbe />)
    expect(screen.getByTestId('frame-count')).toHaveTextContent('2')
  })
})

function HrefProbe({ to }: { readonly to: string }): ReactElement {
  const { href } = useRouteLinkProps(to)
  return (
    <a data-testid="href" href={href}>
      go
    </a>
  )
}

/**
 * The click side of the pair. A button, not an anchor: jsdom queues a real
 * navigation task for every anchor click it is not told to prevent, so a
 * click the handler correctly leaves to the browser would surface later as
 * a navigation error instead of an assertion. The judgement reads the
 * element's `target` and `download` attributes and nothing else, so the
 * tests set those attributes on this button where they apply.
 */
function ClickProbe({ to }: { readonly to: string }): ReactElement {
  const { onClick } = useRouteLinkProps(to)
  return (
    <button data-testid="link" onClick={onClick}>
      go
    </button>
  )
}

describe('useRouteLinkProps', () => {
  const table: RouteTable = [
    { path: '/', view: () => <span>home</span> },
    { path: '/cases', view: () => <span>case list</span> },
  ]

  it('derives the href from the target', () => {
    renderRouteTable(table, <HrefProbe to="/cases" />)
    expect(screen.getByTestId('href')).toHaveAttribute('href', '/cases')
  })

  it('includes the provider basename in the href', () => {
    renderRouteTable(table, <HrefProbe to="/cases" />, {
      path: '/console/cases',
      basename: '/console',
    })
    expect(screen.getByTestId('href')).toHaveAttribute('href', '/console/cases')
  })

  it('navigates in-app on a plain primary click', async () => {
    renderRouteTable(
      table,
      <>
        <ClickProbe to="/cases" />
        <RouteView />
      </>,
    )
    fireEvent.click(screen.getByTestId('link'))
    expect(await screen.findByText('case list')).toBeInTheDocument()
    expect(window.location.pathname).toBe('/cases')
  })

  const notPlain: readonly (readonly [string, MouseEventInit])[] = [
    ['meta', { metaKey: true }],
    ['ctrl', { ctrlKey: true }],
    ['shift', { shiftKey: true }],
    ['alt', { altKey: true }],
    ['non-primary button', { button: 1 }],
  ]

  it.each(notPlain)('leaves a %s click to the browser', (_name, init) => {
    renderRouteTable(
      table,
      <>
        <ClickProbe to="/cases" />
        <RouteView />
      </>,
    )
    // The return value is false when the event's default was prevented; the
    // handler must leave the event alone so the browser's own behaviour runs.
    expect(fireEvent.click(screen.getByTestId('link'), init)).toBe(true)
    expect(window.location.pathname).toBe('/')
    expect(screen.getByText('home')).toBeInTheDocument()
  })

  it('leaves a target other than _self to the browser', () => {
    renderRouteTable(
      table,
      <>
        <ClickProbe to="/cases" />
        <RouteView />
      </>,
    )
    const link = screen.getByTestId('link')
    link.setAttribute('target', '_blank')
    expect(fireEvent.click(link)).toBe(true)
    expect(window.location.pathname).toBe('/')
  })

  it('still navigates when the target is _self', async () => {
    renderRouteTable(
      table,
      <>
        <ClickProbe to="/cases" />
        <RouteView />
      </>,
    )
    const link = screen.getByTestId('link')
    link.setAttribute('target', '_self')
    fireEvent.click(link)
    expect(await screen.findByText('case list')).toBeInTheDocument()
    expect(window.location.pathname).toBe('/cases')
  })

  it('leaves a download link to the browser', () => {
    renderRouteTable(
      table,
      <>
        <ClickProbe to="/cases" />
        <RouteView />
      </>,
    )
    const link = screen.getByTestId('link')
    link.setAttribute('download', '')
    expect(fireEvent.click(link)).toBe(true)
    expect(window.location.pathname).toBe('/')
  })

  it('does nothing when an earlier handler prevented the default', () => {
    renderRouteTable(
      table,
      <div onClickCapture={(event) => event.preventDefault()}>
        <ClickProbe to="/cases" />
      </div>,
    )
    fireEvent.click(screen.getByTestId('link'))
    expect(window.location.pathname).toBe('/')
  })
})

describe('useRedirect', () => {
  function RedirectProbe(): ReactElement {
    const redirect = useRedirect()
    const navigate = useNavigate()
    return (
      <>
        <button onClick={() => redirect('/cases')}>redirect</button>
        <button onClick={() => navigate('/cases')}>push</button>
      </>
    )
  }

  const table: RouteTable = [
    { path: '/', view: () => <span>home</span> },
    { path: '/cases', view: () => <span>case list</span> },
  ]

  it('replaces the current history entry instead of pushing one', async () => {
    renderRouteTable(
      table,
      <>
        <RedirectProbe />
        <RouteView />
      </>,
    )
    const before = window.history.length
    fireEvent.click(screen.getByRole('button', { name: 'redirect' }))
    expect(await screen.findByText('case list')).toBeInTheDocument()
    expect(window.location.pathname).toBe('/cases')
    expect(window.history.length).toBe(before)
  })

  it('pushes an entry, which is what distinguishes it from a redirect', async () => {
    renderRouteTable(
      table,
      <>
        <RedirectProbe />
        <RouteView />
      </>,
    )
    const before = window.history.length
    fireEvent.click(screen.getByRole('button', { name: 'push' }))
    expect(await screen.findByText('case list')).toBeInTheDocument()
    expect(window.history.length).toBe(before + 1)
  })
})

describe('useLocationPath', () => {
  function LocationProbe(): ReactElement {
    const { path, search } = useLocationPath()
    return <span data-testid="location">{`${path}|${search}`}</span>
  }

  const table: RouteTable = [{ path: '/', view: () => null }]

  it('reads the raw path and query string of an unmatched URL', () => {
    renderRouteTable(table, <LocationProbe />, {
      path: '/cases/42?tab=audit&sort=name',
    })
    expect(screen.getByTestId('location')).toHaveTextContent(
      '/cases/42|?tab=audit&sort=name',
    )
  })

  it('reports an empty search when the URL has no query string', () => {
    renderRouteTable(table, <LocationProbe />, { path: '/cases/42' })
    expect(screen.getByTestId('location')).toHaveTextContent('/cases/42|')
  })
})
