/**
 * The width the end-to-end tier's own builds run at.
 *
 * `make e2e` passes GATE_JOBS -- the CPU share the run was given -- into the
 * Playwright process, and the global setup hands it to each Go build as -p and
 * GOMAXPROCS. The tier never probes the host's core count: inside a container
 * a process sees the node's cores rather than the share it was given, so a
 * probe would oversubscribe whatever budget the caller allocated.
 *
 * The value is required. A missing one means the run did not enter through
 * `make e2e`, and falling back to a default would let a mistyped budget run
 * silently at the wrong width. A value that is not a positive integer is
 * refused for the same reason -- with a message that names the entry point,
 * rather than a bare parse error.
 */

/** The environment variable that carries the run's CPU budget. */
export const GATE_JOBS_ENV = 'GATE_JOBS'

/**
 * Parse the budget out of the environment. Only a decimal integer of at least
 * 1 is accepted; absent, empty, fractional, signed, exponent, hex and
 * out-of-safe-range values all throw.
 */
export function parseGateJobs(raw: string | undefined): number {
  const trimmed = raw?.trim() ?? ''
  if (!/^\d+$/.test(trimmed)) {
    throw refusal(raw)
  }
  const width = Number(trimmed)
  if (!Number.isSafeInteger(width) || width < 1) {
    throw refusal(raw)
  }
  return width
}

/** The error a refused budget carries, naming the entry point that supplies one. */
function refusal(raw: string | undefined): Error {
  const shown = raw === undefined ? '(unset)' : JSON.stringify(raw)
  return new Error(
    `${GATE_JOBS_ENV} must be a positive integer; got ${shown}. ` +
      'Run the end-to-end tier through `make e2e`, which passes the gate\'s CPU budget.',
  )
}
