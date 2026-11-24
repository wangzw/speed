import { vi } from 'vitest'

const FRAME_INTERVAL_MS = 1000 / 60

// Two frames, not one: jsdom clears its rendering interval only on a tick that finishes it.
const RESTORE_DRAIN_FRAMES = 2

export async function restoreRealTimers(): Promise<void> {
  if (!vi.isFakeTimers()) {
    vi.useRealTimers()
    return
  }
  try {
    await vi.advanceTimersByTimeAsync(RESTORE_DRAIN_FRAMES * FRAME_INTERVAL_MS)
  } finally {
    vi.useRealTimers()
  }
}
