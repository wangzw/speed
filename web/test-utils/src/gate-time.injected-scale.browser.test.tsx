/**
 * The browser setup applies the scale the tier's config injected.
 *
 * This file runs in the browser tier's second pass
 * (`web/vitest.browser.injected-scale.config.ts`), which injects a scale
 * of 2.5 rather than the caller's value (normally 1). At the default
 * scale a setup that hardcoded the unscaled budget would configure exactly
 * what the main case expects, so the two would be indistinguishable; with
 * 2.5 injected, a setup that ignores the injection -- or reads the
 * environment, where the page has no process global -- leaves the budget
 * at `asyncBudgetMs(1)` and fails here.
 */
import { getConfig } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { asyncBudgetMs } from './gate-time.js'
import { waitForCondition } from './wait.js'

declare const __SPEED_GATE_TIME_SCALE__: number

describe("the browser tier's injected scale", () => {
  it('runs at a scale other than the default', () => {
    expect(__SPEED_GATE_TIME_SCALE__).toBe(2.5)
  })

  it('configures the async budget from that injected scale', () => {
    expect(asyncBudgetMs(2.5)).not.toBe(asyncBudgetMs(1))
    expect(getConfig().asyncUtilTimeout).toBe(asyncBudgetMs(2.5))
  })

  it('gives waitForCondition that budget, not the unscaled default', async () => {
    // The helper reads Testing Library's configuration; in this tier the
    // page has no process global, so an environment read would silently
    // use the default scale and pass the one-second-scale budget instead.
    const spy = vi
      .spyOn(vi, 'waitFor')
      .mockResolvedValue(undefined as never)
    try {
      await waitForCondition(() => true)
      expect(spy).toHaveBeenCalledWith(expect.any(Function), {
        timeout: asyncBudgetMs(2.5),
      })
    } finally {
      spy.mockRestore()
    }
  })
})
