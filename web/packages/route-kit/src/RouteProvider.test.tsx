/**
 * RouteProvider.tsx -- the wiring between the registration surface and the
 * router. The positive case proves a router is actually installed over the
 * children (a consumer's useLocation reads the pinned URL); the negative
 * cases prove the consistency check runs at assembly, before children render.
 */

import { render, screen } from '@testing-library/react'
import { useLocation } from 'react-router-dom'
import { describe, expect, it, vi } from 'vitest'
import type { ReactElement, ReactNode } from 'react'
import { renderRouteTable } from '../test-utils/render.js'
import { RouteProvider } from './RouteProvider.js'
import type { RouteContext, RouteTable } from './types.js'

const view = (): null => null
const fallback = (): null => null

function LocationProbe(): ReactElement {
  const { pathname } = useLocation()
  return <span data-testid="path">{pathname}</span>
}

describe('RouteProvider', () => {
  it('installs a router the children read the current URL from', () => {
    const table: RouteTable = [{ path: '/cases', view }]
    renderRouteTable(table, <LocationProbe />, { path: '/cases' })
    expect(screen.getByTestId('path')).toHaveTextContent('/cases')
  })

  it('renders the children', () => {
    const table: RouteTable = [{ path: '/', view }]
    renderRouteTable(table, <span data-testid="child">hello</span>)
    expect(screen.getByTestId('child')).toBeInTheDocument()
  })

  it('installs the router under a basename while paths stay relative to it', () => {
    const table: RouteTable = [{ path: '/cases', view }]
    renderRouteTable(table, <LocationProbe />, {
      path: '/console/cases',
      basename: '/console',
    })
    expect(screen.getByTestId('path')).toHaveTextContent('/cases')
  })

  it('throws at assembly on a duplicate pattern, before rendering children', () => {
    const table: RouteTable = [
      { path: '/cases', view },
      { path: '/cases', view },
    ]
    // React reports a render-phase throw to console.error even when no error
    // boundary catches it; this test's assertion is the throw itself, so the
    // report is silenced for the one call rather than the assertion dropped.
    const errorSpy = vi.spyOn(console, 'error').mockImplementation(() => {})
    try {
      expect(() =>
        render(
          <RouteProvider table={table} fallback={fallback}>
            <span>never</span>
          </RouteProvider>,
        ),
      ).toThrow(/Duplicate route paths/)
    } finally {
      errorSpy.mockRestore()
    }
  })

  it('throws at assembly on an ambiguous pattern', () => {
    const table: RouteTable = [
      { path: '/cases/:id', view },
      { path: '/cases/:caseId', view },
    ]
    const errorSpy = vi.spyOn(console, 'error').mockImplementation(() => {})
    try {
      expect(() =>
        render(
          <RouteProvider table={table} fallback={fallback}>
            <span>never</span>
          </RouteProvider>,
        ),
      ).toThrow(/Ambiguous/)
    } finally {
      errorSpy.mockRestore()
    }
  })

  it('throws at assembly when the fallback is missing', () => {
    const table: RouteTable = [{ path: '/', view }]
    // A JS host can omit the required prop; the check catches it at runtime.
    const missing = undefined as unknown as (context: RouteContext) => ReactNode
    const errorSpy = vi.spyOn(console, 'error').mockImplementation(() => {})
    try {
      expect(() =>
        render(
          <RouteProvider table={table} fallback={missing}>
            <span>never</span>
          </RouteProvider>,
        ),
      ).toThrow(/fallback/)
    } finally {
      errorSpy.mockRestore()
    }
  })

  describe('feature-gated tables', () => {
    const gated: RouteTable = [
      { path: '/objects', view, requires: ['objectstore.objects'] },
    ]
    const presence = { state: 'ready', has: () => true } as const
    const pending = (): null => null
    const failed = (): null => null

    function expectAssemblyThrow(
      element: ReactElement,
      pattern: RegExp,
    ): void {
      const errorSpy = vi.spyOn(console, 'error').mockImplementation(() => {})
      try {
        expect(() => render(element)).toThrow(pattern)
      } finally {
        errorSpy.mockRestore()
      }
    }

    it('throws when the presence predicate is missing', () => {
      expectAssemblyThrow(
        <RouteProvider
          table={gated}
          fallback={fallback}
          pending={pending}
          failed={failed}
        >
          <span>never</span>
        </RouteProvider>,
        /presence/,
      )
    })

    it('throws when the pending view is missing', () => {
      expectAssemblyThrow(
        <RouteProvider
          table={gated}
          fallback={fallback}
          presence={presence}
          failed={failed}
        >
          <span>never</span>
        </RouteProvider>,
        /pending/,
      )
    })

    it('throws when the failed view is missing', () => {
      expectAssemblyThrow(
        <RouteProvider
          table={gated}
          fallback={fallback}
          presence={presence}
          pending={pending}
        >
          <span>never</span>
        </RouteProvider>,
        /failed/,
      )
    })

    it('mounts when the predicate and both views are given', () => {
      render(
        <RouteProvider
          table={gated}
          fallback={fallback}
          presence={presence}
          pending={pending}
          failed={failed}
        >
          <span>mounted</span>
        </RouteProvider>,
      )
      expect(screen.getByText('mounted')).toBeInTheDocument()
    })
  })
})
