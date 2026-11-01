/**
 * The error-code text convention: a listed code resolves to its own
 * leaf under the errors section, anything unlisted to the shared
 * unknown fallback, and the resolver is bound to the passed translation
 * function and whitelist alone.
 */

import { describe, expect, it, vi } from 'vitest'
import {
  CLIENT_TRANSPORT_ERROR_CODES,
  SESSION_LIFECYCLE_ERROR_CODES,
  WAIT_LONG_FROM_SECONDS,
  createErrorTextResolver,
  waitCountdownText,
  type ErrorTextTranslate,
} from './error-text.js'

describe('createErrorTextResolver', () => {
  it('resolves a listed code to its own errors-section leaf', () => {
    const t = vi.fn((key: string) => `translated:${key}`)
    const resolve = createErrorTextResolver(
      t,
      new Set(['authn.session_revoked']),
    )
    expect(resolve('authn.session_revoked')).toBe(
      'translated:errors.authn.session_revoked',
    )
    expect(t).toHaveBeenCalledWith('errors.authn.session_revoked')
  })

  it('resolves an unlisted code to the shared unknown fallback', () => {
    const t = vi.fn((key: string) => `translated:${key}`)
    const resolve = createErrorTextResolver(t, new Set(['authn.session_revoked']))
    expect(resolve('authn.future_code')).toBe('translated:errors.unknown')
  })

  it('treats an empty whitelist as listing nothing', () => {
    const t = vi.fn((key: string) => key)
    const resolve = createErrorTextResolver(t, new Set())
    expect(resolve('client.network')).toBe('errors.unknown')
  })
})

/** A translation function that records the key and options it receives. */
function recordingTranslate(): {
  t: (key: string, options?: { readonly status?: string }) => string
  calls: Array<{ key: string; options?: { readonly status?: string } }>
} {
  const calls: Array<{ key: string; options?: { readonly status?: string } }> = []
  const t = (key: string, options?: { readonly status?: string }): string => {
    calls.push({ key, options })
    return `translated:${key}`
  }
  return { t, calls }
}

describe('createErrorTextResolver httpFamily', () => {
  it('resolves client.http.<status> through the parameterized leaf with the status', () => {
    const { t, calls } = recordingTranslate()
    const resolve = createErrorTextResolver(t, new Set(), {
      httpFamily: { key: 'errors.client.http' },
    })
    expect(resolve('client.http.503')).toBe('translated:errors.client.http')
    expect(calls).toEqual([
      { key: 'errors.client.http', options: { status: '503' } },
    ])
  })

  it('prefers a per-status leaf when the status has one', () => {
    const { t, calls } = recordingTranslate()
    const resolve = createErrorTextResolver(t, new Set(), {
      httpFamily: {
        key: 'errors.client.http',
        byStatus: { '403': 'errors.client.httpForbidden' },
      },
    })
    expect(resolve('client.http.403')).toBe('translated:errors.client.httpForbidden')
    expect(calls).toEqual([{ key: 'errors.client.httpForbidden', options: undefined }])
    expect(resolve('client.http.500')).toBe('translated:errors.client.http')
    expect(calls[1]).toEqual({
      key: 'errors.client.http',
      options: { status: '500' },
    })
  })

  it('leaves a non-HTTP unlisted code to the shared unknown fallback', () => {
    const { t } = recordingTranslate()
    const resolve = createErrorTextResolver(t, new Set(), {
      httpFamily: { key: 'errors.client.http' },
    })
    expect(resolve('authn.future_code')).toBe('translated:errors.unknown')
  })

  it('keeps client.http.<status> on the unknown fallback when no family is configured', () => {
    const { t } = recordingTranslate()
    const resolve = createErrorTextResolver(t, new Set())
    expect(resolve('client.http.503')).toBe('translated:errors.unknown')
  })

  it('keeps the whitelist ahead of the family', () => {
    const { t, calls } = recordingTranslate()
    const resolve = createErrorTextResolver(t, new Set(['client.http.418']), {
      httpFamily: { key: 'errors.client.http' },
    })
    expect(resolve('client.http.418')).toBe('translated:errors.client.http.418')
    expect(calls).toEqual([
      { key: 'errors.client.http.418', options: undefined },
    ])
  })
})

describe('createErrorTextResolver fallthrough', () => {
  it('resolves errors.<code> when the caller confirms the bundle carries it', () => {
    const { t, calls } = recordingTranslate()
    const fallthrough = vi.fn((code: string) => code === 'notification.message_not_found')
    const resolve = createErrorTextResolver(t, new Set(), { fallthrough })
    expect(resolve('notification.message_not_found')).toBe(
      'translated:errors.notification.message_not_found',
    )
    expect(calls).toEqual([
      { key: 'errors.notification.message_not_found', options: undefined },
    ])
    expect(fallthrough).toHaveBeenCalledWith('notification.message_not_found')
  })

  it('keeps the unknown fallback when the caller declines the code', () => {
    const { t } = recordingTranslate()
    const resolve = createErrorTextResolver(t, new Set(), {
      fallthrough: () => false,
    })
    expect(resolve('notification.unknown_code')).toBe('translated:errors.unknown')
  })

  it('does not consult the fallthrough for a whitelisted code', () => {
    const { t } = recordingTranslate()
    const fallthrough = vi.fn(() => true)
    const resolve = createErrorTextResolver(t, new Set(['tenant.conflict']), {
      fallthrough,
    })
    expect(resolve('tenant.conflict')).toBe('translated:errors.tenant.conflict')
    expect(fallthrough).not.toHaveBeenCalled()
  })
})

describe('createErrorTextResolver wait', () => {
  const wait = {
    key: 'throttle.retryAfter',
    longKey: 'throttle.retryAfterLong',
    codes: ['authn.throttled'],
  }

  it('names a short wait in seconds through the surface leaf', () => {
    const t = vi.fn((key: string) => key)
    const resolve = createErrorTextResolver(
      t,
      new Set(['authn.throttled']),
      { wait },
    )
    expect(resolve('authn.throttled', 45)).toBe('throttle.retryAfter')
    expect(t).toHaveBeenCalledWith('throttle.retryAfter', { seconds: 45 })
  })

  it('names a long wait in whole minutes, rounded up', () => {
    const t = vi.fn((key: string) => key)
    const resolve = createErrorTextResolver(
      t,
      new Set(['authn.throttled']),
      { wait },
    )
    expect(resolve('authn.throttled', 3540)).toBe('throttle.retryAfterLong')
    expect(t).toHaveBeenCalledWith('throttle.retryAfterLong', { minutes: 59 })
    expect(resolve('authn.throttled', 120)).toBe('throttle.retryAfterLong')
  })

  it('keeps the code leaf when no wait is known or the code states none', () => {
    const t = vi.fn((key: string) => key)
    const resolve = createErrorTextResolver(
      t,
      new Set(['authn.throttled', 'authn.conflict']),
      { wait },
    )
    expect(resolve('authn.throttled')).toBe('errors.authn.throttled')
    expect(resolve('authn.conflict', 45)).toBe('errors.authn.conflict')
  })
})

describe('the shared code vocabulary', () => {
  it('names the session-lifecycle family every surface shares', () => {
    expect([...SESSION_LIFECYCLE_ERROR_CODES]).toEqual([
      'authn.session_not_found',
      'authn.session_revoked',
      'authn.token_expired',
      'authn.refresh_token_invalid',
      'authn.refresh_token_reused',
    ])
  })

  it('names the transport codes of the api-client contract', () => {
    expect([...CLIENT_TRANSPORT_ERROR_CODES]).toEqual([
      'client.network',
      'client.timeout',
      'client.protocol',
    ])
  })
})

describe('waitCountdownText', () => {
  const keys = { short: 'throttle.countdown', long: 'throttle.countdownLong' }
  type TranslateCall = Parameters<ErrorTextTranslate>

  function recordingTranslate(): {
    t: ErrorTextTranslate
    calls: TranslateCall[]
  } {
    const calls: TranslateCall[] = []
    const t: ErrorTextTranslate = (key, options) => {
      calls.push([key, options])
      return key
    }
    return { t, calls }
  }

  it('names the remaining seconds below the long threshold', () => {
    const { t, calls } = recordingTranslate()
    expect(waitCountdownText(t, keys, WAIT_LONG_FROM_SECONDS - 1)).toBe(
      'throttle.countdown',
    )
    expect(calls).toEqual([['throttle.countdown', { seconds: 119 }]])
  })

  it('names whole minutes from the long threshold', () => {
    const { t, calls } = recordingTranslate()
    expect(waitCountdownText(t, keys, WAIT_LONG_FROM_SECONDS)).toBe(
      'throttle.countdownLong',
    )
    expect(calls).toEqual([['throttle.countdownLong', { minutes: 2 }]])
  })

  it('keeps the seconds form when no long leaf is supplied', () => {
    const t = vi.fn((key: string) => key)
    expect(waitCountdownText(t, { short: 'throttle.countdown' }, 600)).toBe(
      'throttle.countdown',
    )
    expect(t).toHaveBeenCalledWith('throttle.countdown', { seconds: 600 })
  })
})
