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
import { TEST_BROWSER_TIME_ZONE } from '../../vitest.shared'
import { TEST_TIME_ZONE, createTestQueryClient } from './render'

describe('TEST_TIME_ZONE', () => {
  it('is neither the terminal fallback nor the zone the suite actually runs in', () => {
    // The zone is read from the engine rather than restated: comparing
    // TEST_TIME_ZONE to TEST_BROWSER_TIME_ZONE alone is a comparison of
    // two constants, which still holds when this suite's config stops
    // pinning TZ and the process then runs in the host's zone. The first
    // assertion is what fails when the pin is gone (GitHub's runners set
    // TZ=UTC); the other two keep the default distinct from both tiers
    // the display chain could reach by skipping the provider.
    const browserZone = Intl.DateTimeFormat().resolvedOptions().timeZone
    expect(browserZone).toBe(TEST_BROWSER_TIME_ZONE)
    expect(TEST_TIME_ZONE).not.toBe('UTC')
    expect(TEST_TIME_ZONE).not.toBe(browserZone)
  })
})

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
