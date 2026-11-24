export const GATE_JOBS_ENV = 'GATE_JOBS'

// Only a decimal integer of at least 1 is accepted; everything else throws.
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

function refusal(raw: string | undefined): Error {
  const shown = raw === undefined ? '(unset)' : JSON.stringify(raw)
  return new Error(
    `${GATE_JOBS_ENV} must be a positive integer; got ${shown}. ` +
      'Run the end-to-end tier through `make e2e`, which passes the gate\'s CPU budget.',
  )
}
