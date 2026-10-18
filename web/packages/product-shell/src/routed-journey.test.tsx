/**
 * The host-side routing composition over the product shell, as package
 * evidence.
 *
 * ProductShell does not route: it renders the nav entries the host
 * computed and the children the host supplied, and it never reads or
 * writes the URL (its chrome props pass through to AppShell unchanged,
 * and `selected` state is host-supplied). Routing is @speed/route-kit, a
 * separate layer the shell neither imports nor is imported by
 * (design-web-route-kit.md); the two meet at the shell's slots. This
 * suite is the packaged proof of that composition, the routing twin of
 * the gated-journey suite next door:
 *
 *   - the fixture host declares its pages in one RouteTable (path, view,
 *     optional nav label -- the registry shape);
 *   - RouteProvider installs it, useNavItems feeds the shell's `navItems`
 *     slot, and RouteView renders the matched page as the shell's
 *     `children`;
 *   - the shell renders the frame with those nav entries and the matched
 *     view inside its main landmark, and leaves the URL to the router.
 *
 * @speed/route-kit is a test-only composition partner (a devDependency of
 * this package, like tenancy-ui), never an import of the package's own
 * code: the shell ships no routing edge. The NavItem that useNavItems
 * returns is structurally the AppShellNavItem the frame accepts (id,
 * label, href, selected), so the two need no shared type -- passing
 * useNavItems() straight into `navItems` compiles it.
 *
 * Host content (labels, view text) is English fixture data in a test
 * file, exempt from the no-literal-text rule; the frame's built-in
 * strings asserted here come from the shipped layout-kit bundle,
 * imported relatively.
 */

import { act } from '@testing-library/react'
import type { ReactNode } from 'react'
import { describe, expect, it } from 'vitest'
import { attachSession } from '@speed/auth-core'
import type { AuthSession } from '@speed/auth-core'
import { RouteProvider, RouteView, useNavItems } from '@speed/route-kit'
import type { RouteContext, RouteTable } from '@speed/route-kit'
import layoutKitZhCN from '../../layout-kit/src/locales/zh-CN.json' with { type: 'json' }
import { renderWithProviders } from '../test-utils/render.js'
import {
  authenticatedCompletion,
  jsonResponse,
  makeRealClientRig,
  openFlow,
  principal,
  type RealClientRig,
} from '../test-utils/real-client.js'
import { ProductShell } from './components/ProductShell.js'

const FLOWS_START = 'POST /api/v1/auth/flows'
const FLOWS_ANSWER = 'POST /api/v1/auth/flows/answer'
const PROFILE = 'GET /api/v1/auth/me'

/** The host's page registry -- the one place its product is named.
 * `/notes` (the list) carries a label and so joins the nav; the note
 * detail carries none, so it is reachable by URL but stays out of the nav
 * (hiding a page is a label's absence, not a permission). */
const ROUTES: RouteTable = [
  { path: '/overview', label: 'Overview', view: () => <p>overview page</p> },
  { path: '/notes', label: 'Notes', view: () => <p>notes list</p> },
  {
    path: '/notes/:noteId',
    view: ({ params }) => <p>note detail {params.noteId}</p>,
  },
]

/** The fixture host: it draws route-kit's two outputs and hands them to
 * the shell's slots -- useNavItems() into `navItems` (its NavItem is
 * structurally the frame's AppShellNavItem), RouteView into `children`.
 * It computes no selection itself and never touches the URL. */
function RoutedShell(): ReactNode {
  const navItems = useNavItems()
  return (
    <ProductShell
      navItems={navItems}
      header="Routed App"
      signIn={<p>sign-in view</p>}
    >
      <RouteView />
    </ProductShell>
  )
}

/** Renders the routed host at a deep-linked path (the router reads the
 * live location). */
function renderRouted(path: string): ReturnType<typeof renderWithProviders> {
  window.history.pushState({}, '', path)
  return renderWithProviders(
    <RouteProvider
      table={ROUTES}
      fallback={({ path: unmatched }: RouteContext) => (
        <p>no page at {unmatched}</p>
      )}
    >
      <RoutedShell />
    </RouteProvider>,
  )
}

function makeRig(): RealClientRig {
  return makeRealClientRig((call) => {
    const key = `${call.method} ${call.path}`
    if (key === FLOWS_START) {
      return jsonResponse(200, openFlow())
    }
    if (key === FLOWS_ANSWER) {
      return jsonResponse(200, authenticatedCompletion())
    }
    if (key === PROFILE) {
      return jsonResponse(200, principal())
    }
    throw new Error(`unexpected request: ${key}`)
  })
}

async function signInTo(session: AuthSession): Promise<void> {
  await act(async () => {
    const first = await session.startFlow({ method: 'password' })
    if (!('next' in first)) {
      return
    }
    await session.answerFlow({
      seq: first.seq,
      step: 'answer',
      input: { identifier: 'alice@example.com', password: 'password-1' },
    })
  })
}

describe('the host routing composition over the shell', () => {
  it('renders the registry nav and the deep-linked view', async () => {
    const rig = makeRig()
    attachSession(rig.session)
    const utils = renderRouted('/notes/42')
    await signInTo(rig.session)

    // The frame renders the nav route-kit collected: one item per labeled
    // declaration, and the current path's item selected (the detail route
    // under /notes selects its parent's entry).
    expect(
      utils.getByRole('navigation', { name: layoutKitZhCN.appShell.navLabel }),
    ).toBeInTheDocument()
    expect(utils.getByRole('link', { name: 'Overview' })).not.toHaveAttribute(
      'aria-current',
    )
    expect(utils.getByRole('link', { name: 'Notes' })).toHaveAttribute(
      'aria-current',
      'page',
    )

    // The matched declaration's view renders inside the frame's main
    // landmark: the deep link opened the note detail for its param.
    expect(utils.getByRole('main')).toHaveTextContent('note detail 42')
    // The router owns the URL; the shell navigates nowhere itself.
    expect(window.location.pathname).toBe('/notes/42')
  })

  it('renders the fallback view for a path no declaration matches', async () => {
    const rig = makeRig()
    attachSession(rig.session)
    const utils = renderRouted('/nowhere')
    await signInTo(rig.session)
    // No route matches: the fallback renders in the frame's main landmark,
    // and no nav item is selected (no declaration claimed the path).
    expect(utils.getByRole('main')).toHaveTextContent('no page at /nowhere')
    expect(utils.getByRole('link', { name: 'Overview' })).not.toHaveAttribute(
      'aria-current',
    )
    expect(utils.getByRole('link', { name: 'Notes' })).not.toHaveAttribute(
      'aria-current',
    )
  })
})
