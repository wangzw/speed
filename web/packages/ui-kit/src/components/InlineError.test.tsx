
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
    const stable = within(alert).getByText('authn.throttled:46s')
    expect(stable).toHaveStyle({
      position: 'absolute',
      width: '1px',
      height: '1px',
    })
    expect(stable).not.toHaveAttribute('aria-hidden')
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
