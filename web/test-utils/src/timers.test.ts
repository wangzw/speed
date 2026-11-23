// @vitest-environment jsdom
/**
 * The shared real-clock restore, and the jsdom failure it exists to
 * prevent.
 *
 * jsdom 31 serves requestAnimationFrame and its focus fixups from one
 * lazily created interval. A focus change made while fake timers are
 * installed arms that interval as a fake timer; restoring the real clock
 * with it still armed strands it, and no frame runs again in the worker.
 * The first case below reproduces that shape and only passes when the
 * helper drains the fake clock first.
 *
 * The package's own suite runs in the node environment and has no setup
 * file, so the file takes the jsdom environment for itself and configures
 * Testing Library's async budget the way every workspace setup does.
 */
import { configure, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { asyncBudgetMs, gateTimeScale } from './gate-time.js'
import { restoreRealTimers } from './timers.js'

configure({ asyncUtilTimeout: asyncBudgetMs(gateTimeScale()) })

afterEach(async () => {
  // A case that fails while fake timers are installed must still leave the
  // clock real for the next file in this worker.
  await restoreRealTimers()
})

describe('restoreRealTimers', () => {
  it('keeps animation frames running after a fake-timer case restores the real clock', async () => {
    // The shape every fake-timer case produces: a focus change while the
    // clock is fake leaves jsdom's rendering interval armed.
    vi.useFakeTimers({ shouldAdvanceTime: true })
    try {
      const button = document.createElement('button')
      button.textContent = 'focus target'
      document.body.append(button)
      button.focus()
      button.blur()
      button.remove()
    } finally {
      await restoreRealTimers()
    }

    // After the real clock is back, a frame requested now has to run.
    // jsdom serves requestAnimationFrame from one lazily created interval;
    // left behind on the fake clock, no frame ever runs again in this
    // worker and every later case that waits on one fails on the clock.
    let frameRan = false
    requestAnimationFrame(() => {
      frameRan = true
    })
    await waitFor(() => expect(frameRan).toBe(true))
  })

  it('does not throw when no fake timers are installed', async () => {
    expect(vi.isFakeTimers()).toBe(false)
    await expect(restoreRealTimers()).resolves.toBeUndefined()
    expect(vi.isFakeTimers()).toBe(false)
  })
})
