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
