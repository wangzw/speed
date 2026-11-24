// @vitest-environment jsdom
import { configure, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { asyncBudgetMs, gateTimeScale } from './gate-time.js'
import { restoreRealTimers } from './timers.js'

configure({ asyncUtilTimeout: asyncBudgetMs(gateTimeScale()) })

afterEach(async () => {
  await restoreRealTimers()
})

describe('restoreRealTimers', () => {
  it('keeps animation frames running after a fake-timer case restores the real clock', async () => {
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
