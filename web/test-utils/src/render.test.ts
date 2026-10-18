/**
 * render.test.ts -- the shared test client's read policy.
 *
 * A consuming suite's behaviour can depend on when a read re-runs, so
 * createTestQueryClient must carry the same explicit defaults the
 * assembled app's createQueryClient writes. This package sits below
 * product-shell and imports none of it, so the values are stated here
 * independently and pinned independently. Constructing the client needs
 * no DOM, so this stays in the package's node-environment suite.
 */

import { describe, expect, it } from 'vitest'
import { createTestQueryClient } from './render'

describe('createTestQueryClient', () => {
  it('writes the production read defaults out explicitly', () => {
    const { queries, mutations } = createTestQueryClient().getDefaultOptions()

    // getDefaultOptions returns the options as constructed, without
    // merging the library's defaults, so an exact equality proves the
    // keys were written: a value left to the library is absent here.
    expect(queries).toEqual({
      retry: false,
      staleTime: 0,
      refetchOnMount: true,
      refetchOnWindowFocus: true,
      refetchOnReconnect: true,
    })
    expect(mutations).toEqual({ retry: false })
  })
})
