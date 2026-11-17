/**
 * Vitest's `vi.waitFor` with the workspace's shared async budget.
 *
 * `vi.waitFor` does not read Testing Library's `asyncUtilTimeout` (the
 * budget `./setup.ts` configures) and has no global default: its timeout
 * is a fixed one second per call, which neither the setup's budget nor the
 * caller's `GATE_TIME_SCALE` reaches. A condition that settles after a
 * second under a loaded machine therefore fails on the clock, exactly the
 * failure the shared budgets exist to prevent, and raising the scale
 * cannot help. This helper supplies the budget the setup configured, so a
 * call site cannot fall back to the one-second default.
 *
 * The budget is read from Testing Library's configuration rather than
 * from the environment: in the browser tier the page has no process
 * global, and the setup there takes the caller's scale from the value its
 * config injected (see `./browser-setup.ts`). Reading the configured
 * value keeps the helper on the same budget the `findBy*` queries use in
 * both tiers.
 *
 * Package tests use this instead of `vi.waitFor` (and `vi.waitUntil`,
 * which shares the fixed default); the workspace guard
 * (`web/scripts/workspace-test-config.test.mjs`) rejects every member access
 * to either name in any test source it scans, whether it is a call, an alias,
 * an argument, a property value or a reference kept uncalled.
 */
import { getConfig } from '@testing-library/react'
import { vi } from 'vitest'

/** The `vi.waitFor` options whose timeout this helper owns. */
export interface WaitForConditionOptions {
  /** How often to poll, in milliseconds. */
  readonly interval?: number
  /**
   * A caller may widen the shared budget. A smaller value is ignored:
   * the shared value is the floor, so a call cannot narrow the wait below
   * it.
   */
  readonly timeout?: number
}

/**
 * Waits for `callback` to stop throwing or to resolve, bounded by the
 * budget the suite's setup configured for Testing Library (the shared
 * async budget scaled by the caller's `GATE_TIME_SCALE`). A condition
 * that never settles still fails when the budget expires.
 */
export function waitForCondition<T>(
  callback: () => T | Promise<T>,
  options: WaitForConditionOptions = {},
): Promise<T> {
  return vi.waitFor(callback, {
    ...options,
    timeout: Math.max(options.timeout ?? 0, getConfig().asyncUtilTimeout),
  })
}
