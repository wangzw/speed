/**
 * isTenantAdmissionRefusal -- whether a failed request met the admission
 * layer's tenant refusal rather than a module's own 404.
 *
 * The admission layer answers a tenant the caller may not enter with a
 * bare 404 that carries no module code. @speed/api-client synthesizes its
 * reserved `client.http.404` code for exactly that answer (a non-2xx whose
 * body is not a module envelope), while a module's own typed refusal
 * (`objectstore.not_found`, `notification.not_found`, ...) keeps its
 * module code at the same status. A surface that tells the two apart must
 * therefore read the code, never the status alone.
 *
 * The check is structural, like isApiError and isOperationSuperseded: it
 * reads a shape, so an error that crossed a package boundary or a realm
 * still classifies. Two shapes are the refusal:
 *
 *   - a value whose `code` is `client.http.404`, the code the client
 *     synthesizes for the bare answer;
 *   - a value whose `status` is 404 and whose `code` is absent or in the
 *     reserved `client.` namespace -- the same answer before the client
 *     synthesized its code (a host-created error, a test double).
 *
 * A 404 carrying a module code is never the admission refusal.
 */

/** The reserved code the client synthesizes for an HTTP 404 whose body
 * carried no module envelope. */
const TENANT_ADMISSION_REFUSAL_CODE = 'client.http.404'

/** Whether a failure is the admission layer's bare-404 tenant refusal. */
export function isTenantAdmissionRefusal(error: unknown): boolean {
  if (typeof error !== 'object' || error === null) {
    return false
  }
  const candidate = error as { readonly status?: unknown; readonly code?: unknown }
  if (candidate.code === TENANT_ADMISSION_REFUSAL_CODE) {
    return true
  }
  if (candidate.status !== 404) {
    return false
  }
  const code = candidate.code
  return (
    code === undefined ||
    (typeof code === 'string' && code.startsWith('client.'))
  )
}
