/**
 * Navigation focus in a real browser: the half of the REPLACE guard that
 * jsdom cannot decide.
 *
 * The hand-off skips a REPLACE while the document has received no
 * interaction, because a load-time alias or tenant redirect is part of
 * loading. "Interaction" means an event the browser dispatched -- its
 * `isTrusted` flag set -- and jsdom cannot produce one: every event there
 * comes from `fireEvent`, so that suite pins only the negative half (a
 * synthetic click does not count). These cases click through Vitest's
 * browser-mode userEvent, whose input reaches Chromium's input pipeline and
 * arrives with `isTrusted` true, so the positive half is decided here.
 */

import { describe, expect, it } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import { userEvent } from 'vitest/browser'
import type { ReactElement } from 'react'
import { renderRouteTable } from '../test-utils/render.js'
import { RouteView } from './RouteView.js'
import { useRedirect } from './hooks.js'
import type { RouteTable } from './types.js'

/** Replaces the location with `/b` from a control outside `main`. */
function ReplaceControl(): ReactElement {
  const redirect = useRedirect()
  return <button onClick={() => redirect('/b')}>replace with B</button>
}

const table: RouteTable = [
  {
    path: '/a',
    view: () => (
      <main>
        <h1>Page A</h1>
        <ReplaceControl />
      </main>
    ),
  },
  { path: '/b', view: () => <main><h1>Page B</h1></main> },
]

describe('navigation focus after a trusted interaction', () => {
  it('moves focus after a replace once the user has clicked', async () => {
    renderRouteTable(table, <RouteView />, { path: '/a' })

    await userEvent.click(screen.getByRole('button', { name: 'replace with B' }))

    await waitFor(() => {
      expect(screen.getByRole('heading', { name: 'Page B' })).toHaveFocus()
    })
  })
})
