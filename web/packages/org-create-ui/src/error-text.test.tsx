import { describe, expect, it } from 'vitest'
import { renderHook } from '@testing-library/react'
import { I18nextProvider } from '@speed/i18n'
import type { ReactNode } from 'react'
import { createOrgCreateUiI18n } from '../test-utils/render.js'
import {
  organizationCreateFailure,
  useOrganizationCreateErrorText,
} from './error-text.js'

function renderResolver(language: string = 'en-US') {
  const i18n = createOrgCreateUiI18n(language)
  const wrapper = ({ children }: { readonly children: ReactNode }) => (
    <I18nextProvider i18n={i18n}>{children}</I18nextProvider>
  )
  const { result } = renderHook(() => useOrganizationCreateErrorText(), {
    wrapper,
  })
  return result.current
}

describe('useOrganizationCreateErrorText', () => {
  it('resolves each creation refusal to its own sentence', () => {
    const resolve = renderResolver()
    expect(resolve('authn.contact_required')).toMatch(/Verify your email/)
    expect(resolve('org.organization_limit')).toMatch(/maximum number/)
    expect(resolve('org.creation_in_progress')).toMatch(/already in progress/)
    expect(resolve('org.identifier_unavailable')).toMatch(/already taken/)
    expect(resolve('authn.forbidden')).toMatch(/cannot create an organization/)
  })

  it('names the wait a throttled answer stated', () => {
    const resolve = renderResolver()
    expect(resolve('org.rate_limited', 45)).toBe(
      'Too many organization creations. Try again in 45 seconds.',
    )
    expect(resolve('org.rate_limited', 300)).toBe(
      'Too many organization creations. Try again in about 5 minutes.',
    )
    expect(resolve('org.rate_limited')).toMatch(/too quickly/)
  })

  it('maps a bare HTTP status to the actionable sentence', () => {
    const resolve = renderResolver()
    expect(resolve('client.http.403')).toBe(
      'You do not have permission to create an organization.',
    )
    expect(resolve('client.http.409')).toBe(
      'That change conflicts with an existing organization.',
    )
    expect(resolve('client.http.401')).toBe(
      'Your session has ended. Sign in again.',
    )
    expect(resolve('client.http.500')).toBe(
      'The server returned an error (500). Try again shortly.',
    )
  })

  it('falls back to the unknown text for an unlisted code', () => {
    const resolve = renderResolver()
    expect(resolve('something.else')).toBe(
      'Something went wrong. Try again shortly.',
    )
    expect(resolve('client.unknown')).toBe(
      'Something went wrong. Try again shortly.',
    )
  })
})

describe('organizationCreateFailure', () => {
  it('reads an ApiError-shaped code and wait', () => {
    expect(
      organizationCreateFailure({ code: 'org.rate_limited', retryAfterMs: 90_000 }),
    ).toEqual({ code: 'org.rate_limited', retryAfterSeconds: 90 })
  })

  it('collapses a value with no readable code', () => {
    expect(organizationCreateFailure(new Error('boom'))).toEqual({
      code: 'client.unknown',
    })
  })
})
