const LANDING_KEY = 'authn_error'

const LANDING_EMAIL_KEY = 'authn_email'

const AUTHN_MODULE = 'authn'

export function readCallbackError():
  | { code: string; email?: string }
  | undefined {
  const params = fragmentParams()
  if (params === null) {
    return undefined
  }
  const code = params.get(LANDING_KEY)
  if (code === null || code === '') {
    return undefined
  }
  const error: { code: string; email?: string } = {
    code: `${AUTHN_MODULE}.${code.replace(/-/g, '_')}`,
  }
  if (code === 'link-required') {
    const email = params.get(LANDING_EMAIL_KEY)
    if (email !== null && email !== '') {
      error.email = email
    }
  }
  return error
}

export function clearCallbackFragment(): void {
  const params = fragmentParams()
  if (params === null || !params.has(LANDING_KEY)) {
    return
  }
  const url = new URL(window.location.href)
  url.hash = ''
  window.history.replaceState(window.history.state, '', url.toString())
}

function fragmentParams(): URLSearchParams | null {
  const { hash } = window.location
  if (!hash.startsWith('#')) {
    return null
  }
  return new URLSearchParams(hash.slice(1))
}
