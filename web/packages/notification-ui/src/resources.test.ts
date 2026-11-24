import { describe, expect, it } from 'vitest'
import enUS from './locales/en-US.json' with { type: 'json' }
import zhCN from './locales/zh-CN.json' with { type: 'json' }
import { ERROR_TEXT_CODES } from './internal/error-text.js'

function leafKeys(value: unknown, prefix = ''): string[] {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) {
    return [prefix]
  }
  return Object.entries(value as Record<string, unknown>)
    .flatMap(([key, child]) =>
      leafKeys(child, prefix === '' ? key : `${prefix}.${key}`),
    )
    .sort()
}

describe('notification-ui resources', () => {
  it('carries identical leaf keys in zh-CN and en-US', () => {
    expect(leafKeys(zhCN)).toEqual(leafKeys(enUS))
  })

  it('resolves every whitelisted error code to a bundle leaf', () => {
    const zhLeaves = new Set(leafKeys(zhCN))
    const enLeaves = new Set(leafKeys(enUS))
    for (const code of ERROR_TEXT_CODES) {
      const key = `errors.${code}`
      expect(zhLeaves.has(key), `zh-CN missing ${key}`).toBe(true)
      expect(enLeaves.has(key), `en-US missing ${key}`).toBe(true)
    }
    expect(zhLeaves.has('errors.unknown')).toBe(true)
    expect(enLeaves.has('errors.unknown')).toBe(true)
    for (const leaves of [zhLeaves, enLeaves]) {
      expect(leaves.has('errors.tenantUnavailable')).toBe(true)
      expect(leaves.has('tenantUnavailable.title')).toBe(true)
      expect(leaves.has('tenantUnavailable.description')).toBe(true)
    }
  })
})
