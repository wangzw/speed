/**
 * The binding return seam checks the return_to composition and the callback's site-path read.
 */

import { describe, expect, it } from 'vitest'
import {
  bindingReturnTarget,
  isSitePath,
  withBindingReturn,
} from './binding-return.js'

describe('isSitePath', () => {
  it('accepts a single-slash site path', () => {
    expect(isSitePath('/account/security')).toBe(true)
    expect(isSitePath('/account/security?tab=bindings')).toBe(true)
    expect(isSitePath('/')).toBe(true)
  })

  it('refuses an origin or a scheme-relative value', () => {
    expect(isSitePath('https://elsewhere.example/account')).toBe(false)
    expect(isSitePath('//elsewhere.example/account')).toBe(false)
    expect(isSitePath('account/security')).toBe(false)
    expect(isSitePath('')).toBe(false)
  })

  it('refuses a backslash, a control character and "@"', () => {
    expect(isSitePath('/account\\evil')).toBe(false)
    expect(isSitePath('/account/security@elsewhere.example')).toBe(false)
    expect(isSitePath('/account/security\n')).toBe(false)
    expect(isSitePath('/account/security\u007f')).toBe(false)
  })

  it('refuses a target longer than the server bound', () => {
    expect(isSitePath(`/${'a'.repeat(2047)}`)).toBe(true)
    expect(isSitePath(`/${'a'.repeat(2048)}`)).toBe(false)
  })
})

describe('withBindingReturn', () => {
  it('appends the source page to a relative callback route', () => {
    expect(withBindingReturn('/account/callback', '/account/security')).toBe(
      '/account/callback?return=%2Faccount%2Fsecurity',
    )
  })

  it('keeps a query the callback route already carries', () => {
    expect(withBindingReturn('/account/callback?x=1', '/account/security')).toBe(
      '/account/callback?x=1&return=%2Faccount%2Fsecurity',
    )
  })

  it('writes the return parameter before a fragment the callback carries', () => {
    expect(
      withBindingReturn('/account/callback#offline', '/account/security'),
    ).toBe('/account/callback?return=%2Faccount%2Fsecurity#offline')
  })

  it('appends to an absolute callback URL under a trusted origin', () => {
    const composed = withBindingReturn(
      'https://app.example/account/callback',
      '/account/security',
    )
    const url = new URL(composed)
    expect(url.origin).toBe('https://app.example')
    expect(url.pathname).toBe('/account/callback')
    expect(url.searchParams.get('return')).toBe('/account/security')
  })

  it('leaves the callback untouched when the source is missing or unusable', () => {
    expect(withBindingReturn('/account/callback', undefined)).toBe(
      '/account/callback',
    )
    expect(withBindingReturn('/account/callback', '//elsewhere.example')).toBe(
      '/account/callback',
    )
    expect(
      withBindingReturn('/account/callback', 'https://elsewhere.example'),
    ).toBe('/account/callback')
  })
})

describe('bindingReturnTarget', () => {
  it('reads the source page back from the callback query', () => {
    expect(
      bindingReturnTarget('?return=%2Faccount%2Fsecurity'),
    ).toBe('/account/security')
    expect(bindingReturnTarget('?x=1&return=%2Faccount%2Fsessions')).toBe(
      '/account/sessions',
    )
  })

  it('reads a missing or unusable value as undefined', () => {
    expect(bindingReturnTarget('')).toBeUndefined()
    expect(bindingReturnTarget('?x=1')).toBeUndefined()
    expect(bindingReturnTarget('?return=%2F%2Felsewhere.example')).toBeUndefined()
    expect(
      bindingReturnTarget('?return=https%3A%2F%2Felsewhere.example'),
    ).toBeUndefined()
  })
})
