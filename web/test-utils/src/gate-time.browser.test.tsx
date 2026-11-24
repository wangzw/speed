import { getConfig } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { asyncBudgetMs } from './gate-time.js'

declare const __SPEED_GATE_TIME_SCALE__: number

describe("the browser tier's time scale", () => {
  it('has no process global to read the scale from', () => {
    expect((globalThis as { process?: unknown }).process).toBeUndefined()
  })

  it('sizes the async budget from the value the config injected', () => {
    expect(Number.isFinite(__SPEED_GATE_TIME_SCALE__)).toBe(true)
    expect(__SPEED_GATE_TIME_SCALE__).toBeGreaterThanOrEqual(1)
    expect(getConfig().asyncUtilTimeout).toBe(
      asyncBudgetMs(__SPEED_GATE_TIME_SCALE__),
    )
  })
})
