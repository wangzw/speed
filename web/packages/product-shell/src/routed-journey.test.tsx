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

const ROUTES: RouteTable = [
  { path: '/overview', label: 'Overview', view: () => <p>overview page</p> },
  { path: '/notes', label: 'Notes', view: () => <p>notes list</p> },
  {
    path: '/notes/:noteId',
    view: ({ params }) => <p>note detail {params.noteId}</p>,
  },
]

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

    expect(utils.getByRole('main')).toHaveTextContent('note detail 42')
    expect(window.location.pathname).toBe('/notes/42')
  })

  it('renders the fallback view for a path no declaration matches', async () => {
    const rig = makeRig()
    attachSession(rig.session)
    const utils = renderRouted('/nowhere')
    await signInTo(rig.session)
    expect(utils.getByRole('main')).toHaveTextContent('no page at /nowhere')
    expect(utils.getByRole('link', { name: 'Overview' })).not.toHaveAttribute(
      'aria-current',
    )
    expect(utils.getByRole('link', { name: 'Notes' })).not.toHaveAttribute(
      'aria-current',
    )
  })
})
