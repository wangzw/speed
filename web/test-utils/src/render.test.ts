import { describe, expect, it } from 'vitest'
import { TEST_BROWSER_TIME_ZONE } from '../../vitest.shared'
import { TEST_TIME_ZONE, createTestQueryClient } from './render'

describe('TEST_TIME_ZONE', () => {
  it('is neither the terminal fallback nor the zone the suite actually runs in', () => {
    const browserZone = Intl.DateTimeFormat().resolvedOptions().timeZone
    expect(browserZone).toBe(TEST_BROWSER_TIME_ZONE)
    expect(TEST_TIME_ZONE).not.toBe('UTC')
    expect(TEST_TIME_ZONE).not.toBe(browserZone)
  })
})

describe('createTestQueryClient', () => {
  it('writes the production read defaults out explicitly', () => {
    const { queries, mutations } = createTestQueryClient().getDefaultOptions()

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
