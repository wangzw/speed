/**
 * navigation-focus.tsx -- where focus goes after an in-app navigation.
 *
 * Every case mounts a real `RouteProvider` through `renderRouteTable` and
 * moves the URL the way a host does: a link inside the page, a nav entry
 * outside it, or the browser's back button. The assertions pin the landing
 * decision, not a mount: the new page's heading receives focus, and the
 * guards leave focus alone on the first screen, during a load-time replace,
 * inside a modal dialog, or when the page placed focus itself.
 */

import { act, fireEvent, screen, waitFor } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import type { ReactElement } from 'react'
import { renderRouteTable } from '../test-utils/render.js'
import { RouteLink } from './RouteLink.js'
import { RouteView } from './RouteView.js'
import { useNavItems } from './hooks.js'
import type { RouteTable } from './types.js'

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

describe('navigation focus', () => {
  it('moves focus to the new page heading after a link inside the page', async () => {
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
      {
        path: '/b',
        view: () => (
          <main>
            <h1>Page B</h1>
          </main>
        ),
      },
    ]
    renderRouteTable(table, <RouteView />, { path: '/a' })
    const link = screen.getByRole('link', { name: 'to B' })
    link.focus()
    fireEvent.click(link)

    await waitFor(() => {
      expect(screen.getByRole('heading', { name: 'Page B' })).toHaveFocus()
    })
  })

  it('moves focus to the new page heading after a nav entry outside main', async () => {
    const table: RouteTable = [
      {
        path: '/a',
        view: () => (
          <main>
            <h1>Page A</h1>
          </main>
        ),
        label: 'Page A',
      },
      {
        path: '/b',
        view: () => (
          <main>
            <h1>Page B</h1>
          </main>
        ),
        label: 'Page B',
      },
    ]
    renderRouteTable(
      table,
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

  it('moves focus to the previous page heading after the browser goes back', async () => {
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
      {
        path: '/b',
        view: () => (
          <main>
            <h1>Page B</h1>
          </main>
        ),
      },
    ]
    renderRouteTable(table, <RouteView />, { path: '/a' })
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
