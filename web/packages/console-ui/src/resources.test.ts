/**
 * The console-ui namespace bundle's shape contract.
 *
 * The two language files must carry identical leaf key sets -- a key in
 * one language and not the other is a missing translation the host would
 * otherwise discover at runtime -- and the namespace must be the bare
 * "console-ui" registerNamespace keys it under. The bundle is read
 * through the exported consoleUiResources so the test pins what ships,
 * not what the files happen to contain on disk.
 */

import { describe, expect, it } from 'vitest'
import { CONSOLE_UI_NAMESPACE, consoleUiResources } from './resources.js'

/** The dotted leaf key paths of a nested resource object. */
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

/** The dotted key paths and string values of a nested resource object. */
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

describe('console-ui resources', () => {
  it('registers under the bare "console-ui" namespace', () => {
    expect(CONSOLE_UI_NAMESPACE).toBe('console-ui')
  })

  it('ships the two supported languages', () => {
    expect(Object.keys(consoleUiResources).sort()).toEqual(['en-US', 'zh-CN'])
  })

  it('carries identical leaf key sets across languages', () => {
    const zh = [
      ...leafKeys(consoleUiResources['zh-CN'] as Record<string, unknown>),
    ].sort()
    const en = [
      ...leafKeys(consoleUiResources['en-US'] as Record<string, unknown>),
    ].sort()
    expect(en).toEqual(zh)
  })

  it('carries the errors.unknown fallback every unlisted code resolves to', () => {
    expect(
      leafKeys(consoleUiResources['en-US'] as Record<string, unknown>),
    ).toContain('errors.unknown')
  })

  it('states a read failure as content that did not load, offering a retry', () => {
    // A 429 or a 5xx is an answer: the retryable placeholder must not
    // claim the server was silent, in either language.
    const errorDescriptions = (
      entries: ReadonlyArray<readonly [string, string]>,
    ): ReadonlyArray<readonly [string, string]> =>
      entries.filter(([key]) => key.endsWith('.error.description'))
    const en = errorDescriptions(
      leafStrings(consoleUiResources['en-US'] as Record<string, unknown>),
    )
    const zh = errorDescriptions(
      leafStrings(consoleUiResources['zh-CN'] as Record<string, unknown>),
    )
    expect(en.length).toBeGreaterThan(0)
    expect(zh.length).toBe(en.length)
    // One sentence for every surface: a surface that describes the failure
    // its own way is how the wording drifts apart.
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
