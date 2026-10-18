import { describe, expect, it } from 'vitest'
import { OBJECTSTORE_UI_NAMESPACE, objectstoreUiResources } from './resources.js'

/** Every leaf key of a bundle, as dotted paths. */
function leafKeys(value: unknown, prefix = ''): string[] {
  if (typeof value === 'string') {
    return [prefix]
  }
  if (typeof value !== 'object' || value === null) {
    return []
  }
  return Object.entries(value as Record<string, unknown>).flatMap(([key, child]) =>
    leafKeys(child, prefix === '' ? key : `${prefix}.${key}`),
  )
}

describe('objectstoreUiResources', () => {
  it('registers under the bare objectstore-ui namespace', () => {
    expect(OBJECTSTORE_UI_NAMESPACE).toBe('objectstore-ui')
  })

  it('ships zh-CN and en-US with identical leaf key sets', () => {
    const zh = objectstoreUiResources['zh-CN']
    const en = objectstoreUiResources['en-US']
    expect(zh).toBeDefined()
    expect(en).toBeDefined()
    expect(leafKeys(zh).sort()).toEqual(leafKeys(en).sort())
  })

  it('carries the error section the resolver navigates', () => {
    const keys = leafKeys(objectstoreUiResources['en-US'])
    expect(keys).toContain('errors.client.network')
    expect(keys).toContain('errors.objectstore.bad_request')
    expect(keys).toContain('errors.objectstore.not_found')
    expect(keys).toContain('errors.objectstore.conflict')
    expect(keys).toContain('errors.authz.forbidden')
    expect(keys).toContain('errors.tenantUnavailable')
    expect(keys).toContain('errors.http')
    expect(keys).toContain('errors.unknown')
    expect(keys).toContain('tenantUnavailable.title')
    expect(keys).toContain('tenantUnavailable.description')
  })
})
