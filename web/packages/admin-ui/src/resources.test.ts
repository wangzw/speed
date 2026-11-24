import { describe, expect, it } from 'vitest'
import { ADMIN_UI_NAMESPACE, adminUiResources } from './resources.js'

function leafKeys(
  value: Record<string, unknown>,
  prefix = '',
): readonly string[] {
  const keys: string[] = []
  for (const [key, child] of Object.entries(value)) {
    const path = prefix === '' ? key : `${prefix}.${key}`
    if (child !== null && typeof child === 'object' && !Array.isArray(child)) {
      keys.push(...leafKeys(child as Record<string, unknown>, path))
    } else {
      keys.push(path)
    }
  }
  return keys
}

function leafStrings(
  value: Record<string, unknown>,
  prefix = '',
): ReadonlyArray<readonly [string, string]> {
  const leaves: Array<readonly [string, string]> = []
  for (const [key, child] of Object.entries(value)) {
    const path = prefix === '' ? key : `${prefix}.${key}`
    if (child !== null && typeof child === 'object' && !Array.isArray(child)) {
      leaves.push(...leafStrings(child as Record<string, unknown>, path))
    } else if (typeof child === 'string') {
      leaves.push([path, child])
    }
  }
  return leaves
}

describe('admin-ui resources', () => {
  it('registers under the bare "admin-ui" namespace', () => {
    expect(ADMIN_UI_NAMESPACE).toBe('admin-ui')
  })

  it('ships the two supported languages', () => {
    expect(Object.keys(adminUiResources).sort()).toEqual(['en-US', 'zh-CN'])
  })

  it('carries identical leaf key sets across languages', () => {
    const zh = [
      ...leafKeys(adminUiResources['zh-CN'] as Record<string, unknown>),
    ].sort()
    const en = [
      ...leafKeys(adminUiResources['en-US'] as Record<string, unknown>),
    ].sort()
    expect(en).toEqual(zh)
  })

  it('carries the errors.unknown fallback every unlisted code resolves to', () => {
    expect(
      leafKeys(adminUiResources['en-US'] as Record<string, unknown>),
    ).toContain('errors.unknown')
  })

  it('states a read failure as content that did not load, offering a retry', () => {
    const errorDescriptions = (
      entries: ReadonlyArray<readonly [string, string]>,
    ): ReadonlyArray<readonly [string, string]> =>
      entries.filter(([key]) => key.endsWith('.error.description'))
    const en = errorDescriptions(
      leafStrings(adminUiResources['en-US'] as Record<string, unknown>),
    )
    const zh = errorDescriptions(
      leafStrings(adminUiResources['zh-CN'] as Record<string, unknown>),
    )
    expect(en.length).toBeGreaterThan(0)
    expect(zh.length).toBe(en.length)
    const enSentence = en[0]?.[1] ?? ''
    for (const [key, text] of en) {
      expect(text, key).toBe(enSentence)
    }
    expect(enSentence).toContain('Try again')
    const zhSentence = zh[0]?.[1] ?? ''
    for (const [key, text] of zh) {
      expect(text, key).toBe(zhSentence)
    }
    expect(zhSentence).toContain('重试')
  })
})
