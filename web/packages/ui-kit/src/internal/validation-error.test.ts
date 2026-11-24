
import { describe, expect, it } from 'vitest'
import zhCN from '../locales/zh-CN.json' with { type: 'json' }
import { resolveValidationError } from './validation-error.js'
import type { ValidationErrorLookup } from './validation-error.js'

function lookup(
  keys: readonly string[],
  t: (key: string) => string = (key) => `[${key}]`,
): ValidationErrorLookup {
  return {
    exists: (key) => keys.includes(key),
    t,
  }
}

describe('resolveValidationError', () => {
  it('returns null when there is no error', () => {
    expect(resolveValidationError(undefined, lookup([]))).toBeNull()
    expect(resolveValidationError(null, lookup([]))).toBeNull()
    expect(resolveValidationError('', lookup([]))).toBeNull()
  })

  it('translates a message that names an existing ui-kit-namespace key', () => {
    const t = (key: string): string =>
      key === 'form.required' ? zhCN.form.required : key
    expect(resolveValidationError('form.required', lookup(['form.required'], t))).toBe(
      zhCN.form.required,
    )
  })

  it('passes through a message that is not a namespace key', () => {
    expect(resolveValidationError('Give it a name', lookup([]))).toBe('Give it a name')
    expect(resolveValidationError('authn:invalid_credentials', lookup([]))).toBe(
      'authn:invalid_credentials',
    )
  })

  it('passes through a message that names a key of a namespace it does not own', () => {
    expect(resolveValidationError('greeting.title', lookup(['form.required']))).toBe(
      'greeting.title',
    )
  })

  it('resolves every key the ui-kit form errors own, both languages', () => {
    const t = (key: string): string =>
      ({
        'form.required': 'REQUIRED',
        'form.invalid': 'INVALID',
      })[key] ?? key
    expect(resolveValidationError('form.invalid', lookup(['form.invalid'], t))).toBe(
      'INVALID',
    )
  })
})
