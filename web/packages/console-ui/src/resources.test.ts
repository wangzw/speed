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
})
