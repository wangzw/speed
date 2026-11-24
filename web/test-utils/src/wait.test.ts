import { configure } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { ASYNC_UTIL_TIMEOUT_MS } from './gate-time.js'
import { waitForCondition } from './wait.js'

const SETUP_BUDGET_MS = ASYNC_UTIL_TIMEOUT_MS

configure({ asyncUtilTimeout: SETUP_BUDGET_MS })

afterEach(() => {
  configure({ asyncUtilTimeout: SETUP_BUDGET_MS })
  vi.restoreAllMocks()
})

describe('waitForCondition', () => {
  it('gives vi.waitFor the configured async budget, not its one-second default', async () => {
    const spy = vi.spyOn(vi, 'waitFor').mockResolvedValue(undefined as never)
    await waitForCondition(() => true)
    expect(spy).toHaveBeenCalledWith(expect.any(Function), {
      timeout: SETUP_BUDGET_MS,
    })
    expect(SETUP_BUDGET_MS).toBeGreaterThan(1_000)
  })

  it('follows a budget configured after the setup ran', async () => {
    configure({ asyncUtilTimeout: 75_000 })
    const spy = vi.spyOn(vi, 'waitFor').mockResolvedValue(undefined as never)
    await waitForCondition(() => true)
    expect(spy).toHaveBeenCalledWith(expect.any(Function), { timeout: 75_000 })
  })

  it('ignores a narrower timeout, so a call cannot undercut the shared budget', async () => {
    const spy = vi.spyOn(vi, 'waitFor').mockResolvedValue(undefined as never)
    await waitForCondition(() => true, { timeout: 500 })
    expect(spy).toHaveBeenCalledWith(expect.any(Function), {
      timeout: SETUP_BUDGET_MS,
    })
  })

  it('keeps polling past the one-second default until the condition settles', async () => {
    const start = Date.now()
    await waitForCondition(() => {
      if (Date.now() - start < 1_100) {
        throw new Error('not yet')
      }
    })
  })
})
