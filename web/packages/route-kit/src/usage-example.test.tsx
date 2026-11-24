import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import type { ReactNode } from 'react'
import { setLocation } from '../test-utils/render.js'
import {
  RouteProvider,
  RouteLink,
  RouteView,
  useNavItems,
  useNavigate,
} from './index.js'
import type { RouteContext, RouteTable } from './index.js'

const routes: RouteTable = [
  { path: '/', view: () => <Home />, label: 'Home' },
  { path: '/cases', view: () => <CaseList />, label: 'Cases' },
  {
    path: '/cases/:caseId',
    view: ({ params }) => <CaseDetail id={params.caseId ?? ''} />,
  },
]

function App(): ReactNode {
  return (
    <RouteProvider
      table={routes}
      fallback={({ path }: RouteContext) => <NotFound path={path} />}
    >
      <TopNav />
      <RouteView />
    </RouteProvider>
  )
}

function TopNav(): ReactNode {
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

function Home(): ReactNode {
  return <h1>Home</h1>
}

function CaseList(): ReactNode {
  return <h1>Cases</h1>
}

function CaseDetail({ id }: { readonly id: string }): ReactNode {
  const navigate = useNavigate()
  return (
    <section>
      <h1>{`Case ${id}`}</h1>
      <button onClick={() => navigate('/cases')}>Back</button>
    </section>
  )
}

function NotFound({ path }: { readonly path: string }): ReactNode {
  return <p>{`No page at ${path}`}</p>
}

describe('README Quick start', () => {
  it('renders the matched page and the derived navigation', () => {
    setLocation('/cases')
    render(<App />)
    expect(screen.getByRole('heading', { name: 'Cases' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Home' })).toHaveAttribute(
      'href',
      '/',
    )
    expect(screen.getByRole('link', { name: 'Cases' })).toHaveAttribute(
      'href',
      '/cases',
    )
  })

  it('matches a param route and navigates imperatively back', async () => {
    setLocation('/cases/42')
    render(<App />)
    expect(screen.getByRole('heading', { name: 'Case 42' })).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: 'Back' }))

    expect(
      await screen.findByRole('heading', { name: 'Cases' }),
    ).toBeInTheDocument()
    expect(window.location.pathname).toBe('/cases')
  })

  it('falls back for a deep link no declaration covers', () => {
    setLocation('/nowhere/at/all')
    render(<App />)
    expect(
      screen.getByText('No page at /nowhere/at/all'),
    ).toBeInTheDocument()
  })
})
