/**
 * The one way a test hands the clock back to real timers.
 *
 * This is the demo host's copy of `@speed/test-utils/timers`: a copyable
 * skeleton declares every @speed dependency by registry version range and
 * does not import the workspace's private test-support package, so it
 * carries the helper under its own `src/test-utils/`. Keep the two copies
 * identical; the workspace guard exempts both by exact path.
 *
 * jsdom 31 serves requestAnimationFrame and its focus fixups from one
 * lazily created setInterval, started on first demand and cleared only when
 * a tick finds nothing left to do. A focus change made while the clock is
 * fake arms that interval as a fake timer. Restoring real timers while it
 * is still armed strands animation for the rest of the worker: the interval
 * never fires again, and jsdom keeps its non-null handle, so it never
 * schedules another and every later requestAnimationFrame goes unserved.
 * Advancing the fake clock first runs the pending fixup and lets the
 * interval go idle before the clock changes hands.
 */
import { vi } from 'vitest'

/** One jsdom rendering frame: the period of the interval jsdom starts for
 * requestAnimationFrame and its focus fixups (1000 / 60 ms). */
const FRAME_INTERVAL_MS = 1000 / 60

/**
 * How many rendering frames the fake clock is advanced before real timers
 * come back. Two, not one: jsdom clears its rendering interval only on a
 * tick that finds no pending frame callback and no pending focus fixup, and
 * the tick that runs a fixup can arm the interval again -- the fixup moves
 * focus, which asks for another update. The second tick is the idle one
 * that lets jsdom clear the interval.
 */
const RESTORE_DRAIN_FRAMES = 2

/**
 * Drains pending jsdom frames and focus fixups on the fake clock, then
 * restores real timers. A case that never installed fake timers only gets
 * the restore, which is a no-op there, and no error.
 */
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
