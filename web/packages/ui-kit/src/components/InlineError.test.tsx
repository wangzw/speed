/**
 * InlineError contract: the banner renders exactly when a code is
 * present, and its text comes only from the injected resolver -- a
 * listed code resolves to its own text, anything else to the family's
 * fallback, and a swapped resolver is what proves the component
 * guesses no namespace of its own. errorCodeOf is pinned beside it: an
 * ApiError-shaped failure keeps its code, anything else -- a bug-shaped
 * throw, a code that is not a non-empty string -- collapses to the
 * code no resolver lists. httpStatusOf reads the status structurally:
 * the numeric field when it is a valid HTTP status, the bare-HTTP code,
 * the field over the code when both are present, and null when neither
 * names one.
 */

import { createRef } from 'react'
import { describe, expect, it, vi } from 'vitest'
import { screen } from '@testing-library/react'
import { InlineError, errorCodeOf, httpStatusOf } from './InlineError.js'
import { renderWithProviders } from '../../test-utils/render.js'

/** A resolver in the surface families' convention: the listed code's
 * own text, everything else the family's unknown fallback. */
function resolverOf(overrides: Record<string, string> = {}) {
  const known: Record<string, string> = {
    'authn.session_revoked': 'authn.session_revoked text',
    ...overrides,
  }
  return (code: string): string => known[code] ?? 'unknown text'
}

describe('InlineError', () => {
  it('renders nothing while the code is null (no prior failure)', () => {
    const { container } = renderWithProviders(
      <InlineError code={null} resolve={resolverOf()} />,
    )
    expect(container.firstChild).toBeNull()
  })

  it('renders a listed code through the injected resolver in an alert', () => {
    renderWithProviders(
      <InlineError code="authn.session_revoked" resolve={resolverOf()} />,
    )
    expect(screen.getByRole('alert')).toHaveTextContent(
      'authn.session_revoked text',
    )
  })

  it('renders the resolver fallback for a code outside the listing', () => {
    renderWithProviders(
      <InlineError code="authn.future_code" resolve={resolverOf()} />,
    )
    expect(screen.getByRole('alert')).toHaveTextContent('unknown text')
  })

  it('takes its text from the passed resolver alone, never another source', () => {
    const first = vi.fn(resolverOf({ 'authn.session_revoked': 'first source' }))
    const firstView = renderWithProviders(
      <InlineError code="authn.session_revoked" resolve={first} />,
    )
    expect(screen.getByRole('alert')).toHaveTextContent('first source')
    expect(first).toHaveBeenCalledWith('authn.session_revoked')
    firstView.unmount()

    renderWithProviders(
      <InlineError
        code="authn.session_revoked"
        resolve={resolverOf({ 'authn.session_revoked': 'second source' })}
      />,
    )
    expect(screen.getByRole('alert')).toHaveTextContent('second source')
  })
})

describe('errorCodeOf', () => {
  it('keep the code of an ApiError-shaped failure', () => {
    expect(errorCodeOf({ code: 'authn.invalid_credentials' })).toBe(
      'authn.invalid_credentials',
    )
  })

  it('collapse a non-ApiError throw (and a thrown string) to the unknown code', () => {
    expect(errorCodeOf(new Error('boom'))).toBe('client.unknown')
    expect(errorCodeOf('boom')).toBe('client.unknown')
  })

  it('collapse an ApiError whose code is not a non-empty string', () => {
    expect(errorCodeOf({ code: 401 })).toBe('client.unknown')
    expect(errorCodeOf({})).toBe('client.unknown')
  })

  it('collapse a callable carrying a code, because it is not an object', () => {
    // `typeof` reports 'function' for a callable, so the object guard keeps
    // it out of the ApiError branch even when a `code` was assigned to it.
    const callable = Object.assign(() => {}, { code: 'tenant.future' })
    expect(errorCodeOf(callable)).toBe('client.unknown')
  })
})

describe('httpStatusOf', () => {
  it('reads a numeric status property', () => {
    expect(httpStatusOf({ status: 403 })).toBe(403)
  })

  it('parses a bare-HTTP code when no numeric status is present', () => {
    expect(httpStatusOf({ code: 'client.http.404' })).toBe(404)
  })

  it('prefers the numeric status when both are present', () => {
    expect(httpStatusOf({ status: 401, code: 'client.http.403' })).toBe(401)
  })

  it('returns null when neither shape names a status', () => {
    expect(httpStatusOf({ code: 'authn.invalid_credentials' })).toBeNull()
    expect(httpStatusOf({ status: '403' })).toBeNull()
    expect(httpStatusOf(new Error('boom'))).toBeNull()
    expect(httpStatusOf('boom')).toBeNull()
    expect(httpStatusOf(null)).toBeNull()
    expect(httpStatusOf(undefined)).toBeNull()
  })

  it('treats a transport failure status of 0 as no status', () => {
    // api-client writes exactly this shape for a network or timeout
    // failure; a caller branching on `=== null` must not read it as HTTP 0.
    expect(httpStatusOf({ status: 0, code: 'client.network' })).toBeNull()
    expect(httpStatusOf({ status: 0, code: 'client.timeout' })).toBeNull()
    expect(httpStatusOf({ status: 0 })).toBeNull()
  })

  it('ignores a numeric status outside the HTTP range', () => {
    expect(httpStatusOf({ status: 99 })).toBeNull()
    expect(httpStatusOf({ status: 600 })).toBeNull()
    expect(httpStatusOf({ status: -1 })).toBeNull()
  })

  it('returns null when reading the status throws', () => {
    const hostile = Object.defineProperty({}, 'status', {
      get() {
        throw new Error('revoked')
      },
    })
    expect(httpStatusOf(hostile)).toBeNull()
  })

  it('forwards sx, className and ref to the alert', () => {
    const ref = createRef<HTMLDivElement>()
    renderWithProviders(
      <InlineError
        code="authn.session_revoked"
        resolve={resolverOf()}
        ref={ref}
        className="host-error"
        sx={{ mt: 4 }}
      />,
    )
    const alert = screen.getByRole('alert')
    expect(alert).toHaveClass('host-error')
    expect(alert).toBe(ref.current)
    expect(alert).toHaveStyle({ marginTop: '32px' })
  })
})
