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
import { screen, within } from '@testing-library/react'
import {
  InlineError,
  errorCodeOf,
  httpStatusOf,
  retryAfterSecondsOf,
} from './InlineError.js'
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

  it('renders its detail inside the same alert, so it is announced with the message', () => {
    renderWithProviders(
      <InlineError
        code="authn.session_revoked"
        resolve={resolverOf()}
        detail="named detail"
      />,
    )
    const alert = screen.getByRole('alert')
    expect(alert).toHaveTextContent('authn.session_revoked text')
    expect(alert).toHaveTextContent('named detail')
  })

  it('renders the resolver fallback for a code outside the listing', () => {
    renderWithProviders(
      <InlineError code="authn.future_code" resolve={resolverOf()} />,
    )
    expect(screen.getByRole('alert')).toHaveTextContent('unknown text')
  })

  it('hands the wait a throttled answer stated to the resolver', () => {
    const resolver = vi.fn(resolverOf())
    renderWithProviders(
      <InlineError
        code="authn.throttled"
        resolve={resolver}
        retryAfterSeconds={45}
      />,
    )
    expect(screen.getByRole('alert')).toHaveTextContent('unknown text')
    expect(resolver).toHaveBeenCalledWith('authn.throttled', 45)
  })

  it('keeps the accessible sentence fixed while the visible one follows the live wait', () => {
    const resolver = (code: string, seconds?: number): string =>
      seconds === undefined ? `${code}:no-wait` : `${code}:${seconds}s`
    renderWithProviders(
      <InlineError
        code="authn.throttled"
        resolve={resolver}
        retryAfterSeconds={46}
        liveRetryAfterSeconds={45}
      />,
    )
    const alert = screen.getByRole('alert')
    // The stable sentence is in the accessibility tree but clipped from view.
    const stable = within(alert).getByText('authn.throttled:46s')
    expect(stable).toHaveStyle({
      position: 'absolute',
      width: '1px',
      height: '1px',
    })
    expect(stable).not.toHaveAttribute('aria-hidden')
    // The painted sentence is hidden from assistive technology and carries
    // the seconds still remaining.
    const visible = alert.querySelector('span[aria-hidden="true"]')
    expect(visible).toHaveTextContent('authn.throttled:45s')
  })

  it('drops the number from the visible sentence once the wait is over', () => {
    const resolver = (code: string, seconds?: number): string =>
      seconds === undefined ? `${code}:no-wait` : `${code}:${seconds}s`
    const view = renderWithProviders(
      <InlineError
        code="authn.throttled"
        resolve={resolver}
        retryAfterSeconds={2}
        liveRetryAfterSeconds={1}
      />,
    )
    const alert = view.getByRole('alert')
    const visible = alert.querySelector('span[aria-hidden="true"]')
    expect(visible).toHaveTextContent('authn.throttled:1s')

    view.rerender(
      <InlineError
        code="authn.throttled"
        resolve={resolver}
        retryAfterSeconds={2}
        liveRetryAfterSeconds={0}
      />,
    )
    // No stale "0 seconds": the visible sentence is the wait-free one, while
    // the accessible sentence still names the wait the answer stated.
    const after = view.getByRole('alert')
    expect(after.querySelector('span[aria-hidden="true"]')).toHaveTextContent(
      'authn.throttled:no-wait',
    )
    expect(within(after).getByText('authn.throttled:2s')).toHaveStyle({
      position: 'absolute',
    })
  })

  it('renders the initial wait in both sentences when no live value is passed', () => {
    const resolver = (code: string, seconds?: number): string =>
      seconds === undefined ? `${code}:no-wait` : `${code}:${seconds}s`
    renderWithProviders(
      <InlineError
        code="authn.throttled"
        resolve={resolver}
        retryAfterSeconds={30}
      />,
    )
    const alert = screen.getByRole('alert')
    const [stable, visible] = within(alert).getAllByText('authn.throttled:30s')
    expect(stable).not.toHaveAttribute('aria-hidden')
    expect(visible).toHaveAttribute('aria-hidden', 'true')
  })

  it('renders a failure with no stated wait unchanged', () => {
    renderWithProviders(
      <InlineError code="authn.session_revoked" resolve={resolverOf()} />,
    )
    const alert = screen.getByRole('alert')
    expect(alert).toHaveTextContent('authn.session_revoked text')
    expect(alert.querySelector('span[aria-hidden="true"]')).toBeNull()
  })

  it('takes its text from the passed resolver alone, never another source', () => {
    const first = vi.fn(resolverOf({ 'authn.session_revoked': 'first source' }))
    const firstView = renderWithProviders(
      <InlineError code="authn.session_revoked" resolve={first} />,
    )
    expect(screen.getByRole('alert')).toHaveTextContent('first source')
    // The wait the error states rides as the resolver's second argument, so
    // a surface that names it can; one that ignores it keeps its text. With
    // no wait stated the code is resolved without a seconds argument.
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

describe('retryAfterSecondsOf', () => {
  it('reads the wait an ApiError states, in whole seconds rounded up', () => {
    expect(retryAfterSecondsOf({ retryAfterMs: 60_000 })).toBe(60)
    expect(retryAfterSecondsOf({ retryAfterMs: 1_500 })).toBe(2)
    expect(retryAfterSecondsOf({ retryAfterMs: 200 })).toBe(1)
  })

  it('answers undefined when there is no usable wait', () => {
    expect(retryAfterSecondsOf({})).toBeUndefined()
    expect(retryAfterSecondsOf({ retryAfterMs: 0 })).toBeUndefined()
    expect(retryAfterSecondsOf({ retryAfterMs: -5 })).toBeUndefined()
    expect(retryAfterSecondsOf({ retryAfterMs: 'soon' })).toBeUndefined()
    expect(retryAfterSecondsOf(null)).toBeUndefined()
    expect(retryAfterSecondsOf(new Error('boom'))).toBeUndefined()
  })

  it('answers undefined rather than throwing on a hostile getter', () => {
    const hostile = Object.defineProperty({}, 'retryAfterMs', {
      get() {
        throw new Error('no')
      },
    })
    expect(retryAfterSecondsOf(hostile)).toBeUndefined()
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
