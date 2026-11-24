import { act, fireEvent, screen, waitFor } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { useEffect, useRef } from 'react'
import type { ReactElement } from 'react'
import { renderRouteTable } from '../test-utils/render.js'
import { RouteLink } from './RouteLink.js'
import { RouteView } from './RouteView.js'
import { useNavigate, useNavItems, useRedirect } from './hooks.js'
import type { RouteTable } from './types.js'

function page(name: string): ReactElement {
  return (
    <main>
      <h1>{`Page ${name}`}</h1>
    </main>
  )
}

function Nav(): ReactElement {
  const items = useNavItems()
  return (
    <nav>
      {items.map((item) => (
        <RouteLink key={item.id} to={item.id}>
          {item.label}
        </RouteLink>
      ))}
    </nav>
  )
}

const linkedPages: RouteTable = [
  {
    path: '/a',
    view: () => (
      <main>
        <h1>Page A</h1>
        <RouteLink to="/b">to B</RouteLink>
      </main>
    ),
  },
  { path: '/b', view: () => page('B') },
]

const labeledPages: RouteTable = [
  { path: '/a', view: () => page('A'), label: 'Page A' },
  { path: '/b', view: () => page('B'), label: 'Page B' },
]

function RedirectOnMount(): null {
  const redirect = useRedirect()
  useEffect(() => {
    redirect('/b')
  }, [redirect])
  return null
}

function PushControl(): ReactElement {
  const navigate = useNavigate()
  return <button onClick={() => navigate('/b')}>push B</button>
}

function ReplaceControl(): ReactElement {
  const redirect = useRedirect()
  return <button onClick={() => redirect('/b')}>replace with B</button>
}

function PageBFocusesItself(): ReactElement {
  const field = useRef<HTMLInputElement>(null)
  useEffect(() => {
    field.current?.focus()
  }, [])
  return (
    <main>
      <h1>Page B</h1>
      <input ref={field} aria-label="verification code" />
    </main>
  )
}

function ModalPushControl(): ReactElement {
  const navigate = useNavigate()
  return (
    <div role="dialog" aria-modal="true" aria-label="Confirm">
      <button onClick={() => navigate('/b')}>confirm and go</button>
    </div>
  )
}

describe('navigation focus', () => {
  it('moves focus to the new page heading after a link inside the page', async () => {
    renderRouteTable(linkedPages, <RouteView />, { path: '/a' })
    const link = screen.getByRole('link', { name: 'to B' })
    link.focus()
    fireEvent.click(link)

    await waitFor(() => {
      expect(screen.getByRole('heading', { name: 'Page B' })).toHaveFocus()
    })
  })

  it('moves focus to the new page heading after a nav entry outside main', async () => {
    renderRouteTable(
      labeledPages,
      <>
        <Nav />
        <RouteView />
      </>,
      { path: '/a' },
    )
    const link = screen.getByRole('link', { name: 'Page B' })
    link.focus()
    fireEvent.click(link)

    await waitFor(() => {
      expect(screen.getByRole('heading', { name: 'Page B' })).toHaveFocus()
    })
  })

  it('moves focus to the new page heading after an imperative navigate', async () => {
    const table: RouteTable = [
      {
        path: '/a',
        view: () => (
          <>
            <PushControl />
            {page('A')}
          </>
        ),
      },
      { path: '/b', view: () => page('B') },
    ]
    renderRouteTable(table, <RouteView />, { path: '/a' })
    const control = screen.getByRole('button', { name: 'push B' })
    control.focus()
    fireEvent.click(control)

    await waitFor(() => {
      expect(screen.getByRole('heading', { name: 'Page B' })).toHaveFocus()
    })
  })

  it('moves focus to the previous page heading after the browser goes back', async () => {
    renderRouteTable(linkedPages, <RouteView />, { path: '/a' })
    fireEvent.click(screen.getByRole('link', { name: 'to B' }))
    await waitFor(() => {
      expect(screen.getByRole('heading', { name: 'Page B' })).toBeInTheDocument()
    })

    await act(async () => {
      window.history.back()
    })

    await waitFor(() => {
      expect(screen.getByRole('heading', { name: 'Page A' })).toHaveFocus()
    })
  })
})

describe('navigation focus guards', () => {
  it('leaves focus at the document start on the first screen', () => {
    renderRouteTable(linkedPages, <RouteView />, { path: '/a' })
    expect(document.body).toHaveFocus()
  })

  it('leaves focus alone during a load-time redirect, before any interaction', async () => {
    const table: RouteTable = [
      {
        path: '/a',
        view: () => (
          <main>
            <h1>Page A</h1>
            <RedirectOnMount />
          </main>
        ),
      },
      { path: '/b', view: () => page('B') },
    ]
    renderRouteTable(table, <RouteView />, { path: '/a' })

    await waitFor(() => {
      expect(screen.getByRole('heading', { name: 'Page B' })).toBeInTheDocument()
    })
    expect(document.body).toHaveFocus()
  })

  it('does not count a synthetic click as the interaction a replace needs', async () => {
    const table: RouteTable = [
      {
        path: '/a',
        view: () => (
          <>
            <ReplaceControl />
            {page('A')}
          </>
        ),
      },
      { path: '/b', view: () => page('B') },
    ]
    renderRouteTable(table, <RouteView />, { path: '/a' })
    fireEvent.click(screen.getByRole('button', { name: 'replace with B' }))

    await waitFor(() => {
      expect(screen.getByRole('heading', { name: 'Page B' })).toBeInTheDocument()
    })
    expect(document.body).toHaveFocus()
  })

  it('leaves focus alone when only the query string changes', async () => {
    const table: RouteTable = [
      { path: '/a', view: () => page('A'), label: 'Page A' },
      { path: '/b', view: () => page('B'), label: 'Page B' },
    ]
    function QueryNav(): ReactElement {
      return (
        <nav>
          <RouteLink to="/a?tab=2">Page A, tab 2</RouteLink>
        </nav>
      )
    }
    renderRouteTable(
      table,
      <>
        <QueryNav />
        <RouteView />
      </>,
      { path: '/a' },
    )
    const link = screen.getByRole('link', { name: 'Page A, tab 2' })
    link.focus()
    fireEvent.click(link)

    await waitFor(() => {
      expect(window.location.search).toBe('?tab=2')
    })
    expect(link).toHaveFocus()
  })

  it('does not override focus the page placed inside its own main', async () => {
    const table: RouteTable = [
      {
        path: '/a',
        view: () => (
          <main>
            <h1>Page A</h1>
            <RouteLink to="/b">to B</RouteLink>
          </main>
        ),
      },
      { path: '/b', view: () => <PageBFocusesItself /> },
    ]
    renderRouteTable(table, <RouteView />, { path: '/a' })
    fireEvent.click(screen.getByRole('link', { name: 'to B' }))

    await waitFor(() => {
      expect(
        screen.getByRole('textbox', { name: 'verification code' }),
      ).toHaveFocus()
    })
    expect(screen.getByRole('heading', { name: 'Page B' })).not.toHaveFocus()
  })

  it('leaves focus inside a modal dialog that stays open across the navigation', async () => {
    renderRouteTable(
      labeledPages,
      <>
        <ModalPushControl />
        <RouteView />
      </>,
      { path: '/a' },
    )
    const control = screen.getByRole('button', { name: 'confirm and go' })
    control.focus()
    fireEvent.click(control)

    await waitFor(() => {
      expect(screen.getByRole('heading', { name: 'Page B' })).toBeInTheDocument()
    })
    expect(control).toHaveFocus()
  })

  it('never moves focus when the host disables the hand-off', async () => {
    renderRouteTable(
      labeledPages,
      <>
        <Nav />
        <RouteView />
      </>,
      { path: '/a', navigationFocus: 'none' },
    )
    const link = screen.getByRole('link', { name: 'Page B' })
    link.focus()
    fireEvent.click(link)

    await waitFor(() => {
      expect(screen.getByRole('heading', { name: 'Page B' })).toBeInTheDocument()
    })
    expect(link).toHaveFocus()
  })
})

describe('navigation landing rule', () => {
  function linkedTo(view: ReactElement): RouteTable {
    return [
      {
        path: '/a',
        view: () => (
          <main>
            <h1>Page A</h1>
            <RouteLink to="/b">to B</RouteLink>
          </main>
        ),
      },
      { path: '/b', view: () => view },
    ]
  }

  async function navigateToB(): Promise<void> {
    const link = screen.getByRole('link', { name: 'to B' })
    link.focus()
    fireEvent.click(link)
  }

  it('gives the heading a temporary tabindex and removes it when focus moves away', async () => {
    renderRouteTable(
      linkedTo(
        <main>
          <h1>Page B</h1>
          <button type="button">Next</button>
        </main>,
      ),
      <RouteView />,
      { path: '/a' },
    )
    await navigateToB()
    const heading = screen.getByRole('heading', { name: 'Page B' })
    await waitFor(() => expect(heading).toHaveFocus())
    expect(heading.getAttribute('tabindex')).toBe('-1')

    screen.getByRole('button', { name: 'Next' }).focus()
    expect(heading.hasAttribute('tabindex')).toBe(false)
  })

  it('lands on the main landmark when it holds no level-one heading', async () => {
    renderRouteTable(linkedTo(<main><h2>Section</h2></main>), <RouteView />, {
      path: '/a',
    })
    await navigateToB()
    const main = screen.getByRole('main')
    await waitFor(() => expect(main).toHaveFocus())
    expect(main.getAttribute('tabindex')).toBe('-1')
  })

  it('falls back to the scope heading when the page has no main landmark', async () => {
    renderRouteTable(
      linkedTo(
        <section>
          <h1>Page B</h1>
        </section>,
      ),
      <RouteView />,
      { path: '/a' },
    )
    await navigateToB()
    const heading = screen.getByRole('heading', { name: 'Page B' })
    await waitFor(() => expect(heading).toHaveFocus())
    expect(heading.getAttribute('tabindex')).toBe('-1')
  })

  it('skips a heading inside a hidden subtree of the landmark', async () => {
    renderRouteTable(
      linkedTo(
        <main>
          <div aria-hidden="true"><h1>Aria hidden</h1></div>
          <div hidden><h1>Hidden attribute</h1></div>
          <h1>Page B</h1>
        </main>,
      ),
      <RouteView />,
      { path: '/a' },
    )
    await navigateToB()
    await waitFor(() => {
      expect(screen.getByRole('heading', { name: 'Page B' })).toHaveFocus()
    })
  })

  it('keeps the hidden check at main, so an ancestor outside it does not suppress the heading', async () => {
    renderRouteTable(
      linkedTo(
        <div aria-hidden="true">
          <main><h1>Page B</h1></main>
        </div>,
      ),
      <RouteView />,
      { path: '/a' },
    )
    await navigateToB()
    const heading = screen.getByText('Page B')
    await waitFor(() => expect(heading).toHaveFocus())
  })

  it('clears the temporary tabindex when the heading cannot take focus', async () => {
    renderRouteTable(
      linkedTo(
        <div style={{ display: 'none' }}>
          <main><h1>Page B</h1></main>
        </div>,
      ),
      <RouteView />,
      { path: '/a' },
    )
    await navigateToB()
    await waitFor(() => expect(screen.getByText('Page B')).toBeInTheDocument())
    const heading = screen.getByText('Page B')
    expect(document.body).toHaveFocus()
    expect(heading.hasAttribute('tabindex')).toBe(false)
  })

  it('moves off a persistent main that itself holds focus', async () => {
    const table: RouteTable = [
      { path: '/start', view: () => null, label: 'Start' },
      { path: '/a', view: () => null, label: 'Empty A' },
      { path: '/b', view: () => <h1>Page B</h1>, label: 'Page B' },
    ]
    function PushB(): ReactElement {
      const navigate = useNavigate()
      return <button onClick={() => navigate('/b')}>push B</button>
    }
    renderRouteTable(
      table,
      <>
        <Nav />
        <PushB />
        <main tabIndex={-1}>
          <RouteView />
        </main>
      </>,
      { path: '/start' },
    )
    const emptyLink = screen.getByRole('link', { name: 'Empty A' })
    emptyLink.focus()
    fireEvent.click(emptyLink)
    const main = screen.getByRole('main')
    await waitFor(() => expect(main).toHaveFocus())

    fireEvent.click(screen.getByRole('button', { name: 'push B' }))
    await waitFor(() => {
      expect(screen.getByRole('heading', { name: 'Page B' })).toHaveFocus()
    })
  })
})
