/**
 * The factors the factorseed command reports it wrote.
 *
 * The command writes one JSON line on success, carrying the identifier of each
 * factor it inserted. The impersonation fixture asks for both an authenticator
 * and an SMS factor, so a successful run that omitted either left the
 * precondition half-written: the journey would start from an operator that
 * cannot complete the step-up it exists to reach. Reading the line holds the
 * command to what the fixture asked for instead of trusting its exit status.
 */
export interface SeededFactors {
  readonly totp: string
  readonly sms: string
}

/**
 * Read the command's JSON line, requiring both factor identifiers. A line that
 * is not JSON, is not an object, or omits a factor is a failure, and the
 * message carries both streams so the failure shows what the command wrote
 * rather than only that it exited zero.
 */
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
