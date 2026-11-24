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
