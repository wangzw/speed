export interface SeededFactors {
  readonly totp: string
  readonly sms: string
}

// A zero exit is not enough: both factor identifiers must be present.
export function readSeededFactors(
  stdout: string,
  stderr: string,
  what: string,
): SeededFactors {
  const failure = (reason: string): Error =>
    new Error(
      `${what} ${reason}\nstandard output:\n${stdout}\nstandard error:\n${stderr}`,
    )
  let parsed: unknown
  try {
    parsed = JSON.parse(stdout.trim())
  } catch (error) {
    throw failure(`wrote no JSON line: ${String(error)}`)
  }
  if (typeof parsed !== 'object' || parsed === null) {
    throw failure('wrote a JSON line that is not an object')
  }
  const record = parsed as { readonly totp?: unknown; readonly sms?: unknown }
  const { totp, sms } = record
  if (typeof totp !== 'string' || totp === '') {
    throw failure('wrote no totp factor')
  }
  if (typeof sms !== 'string' || sms === '') {
    throw failure('wrote no sms factor')
  }
  return { totp, sms }
}
