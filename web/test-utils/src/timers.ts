import { vi } from 'vitest'

const FRAME_INTERVAL_MS = 1000 / 60

// Two, not one: the tick that runs a pending focus fixup can arm jsdom's
// rendering interval again, so only the second, idle tick lets it clear.
const RESTORE_DRAIN_FRAMES = 2

// Advance the fake clock before restoring: jsdom 31 arms its
// requestAnimationFrame interval as a fake timer, and restoring with it
// armed strands every later frame in the worker.
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
