/**
 * The browser tier's read of the caller's `GATE_TIME_SCALE`.
 *
 * The browser page has no process global, so the environment read the unit
 * setup uses yields the default here and cannot carry the caller's value.
 * The tier's budget comes from its config instead
 * (web/vitest.browser.config.ts parses the variable in Node and injects it
 * through Vite's define). These cases pin both halves: the missing global,
 * and an async budget that follows the injected value.
 *
 * The scale here is the caller's, normally 1, so this file alone cannot
 * tell an injected value from a hardcoded one. The tier's second pass
 * (web/vitest.browser.injected-scale.config.ts) runs
 * `gate-time.injected-scale.browser.test.tsx` with 2.5 injected and
 * asserts the setup configured that budget.
 */
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
