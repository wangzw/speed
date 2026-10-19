/**
 * nav.ts -- navigation derivation from the table and the current path: which
 * declarations produce entries, and which single one the URL selects. Each
 * case pins a distinct rule (label presence, exact match, "sits below",
 * longest prefix, tie order) so a wrong selection rule fails loudly.
 */

import { describe, expect, it } from 'vitest'
import { deriveNavItems } from './nav.js'
import type { RouteTable } from './types.js'

const view = (): null => null

describe('deriveNavItems', () => {
  it('derives an entry only for a label-bearing declaration', () => {
    const table: RouteTable = [
      { path: '/', view, label: 'Home' },
      { path: '/cases', view },
    ]
    const items = deriveNavItems(table, '/')
    expect(items).toHaveLength(1)
    expect(items[0]?.id).toBe('/')
    expect(items[0]?.href).toBe('/')
    expect(items[0]?.label).toBe('Home')
  })

  it('returns no entries at all when nothing is labeled', () => {
    const table: RouteTable = [{ path: '/cases/:caseId', view }]
    expect(deriveNavItems(table, '/cases/123')).toEqual([])
  })

  it('selects the entry whose pattern matches the current path', () => {
    const table: RouteTable = [
      { path: '/', view, label: 'Home' },
      { path: '/cases', view, label: 'Cases' },
    ]
    const items = deriveNavItems(table, '/cases')
    expect(items.find((item) => item.id === '/cases')?.selected).toBe(true)
    expect(items.find((item) => item.id === '/')?.selected).toBe(false)
  })

  it('selects a parent while its unlabeled child route is open', () => {
    const table: RouteTable = [
      { path: '/cases', view, label: 'Cases' },
      { path: '/cases/:caseId', view },
    ]
    const items = deriveNavItems(table, '/cases/123')
    expect(items).toHaveLength(1)
    expect(items[0]?.selected).toBe(true)
    expect(items[0]?.id).toBe('/cases')
  })

  it('picks the longest literal prefix among several candidates', () => {
    const table: RouteTable = [
      { path: '/', view, label: 'Home' },
      { path: '/cases', view, label: 'Cases' },
      { path: '/cases/archive', view, label: 'Archive' },
    ]
    const items = deriveNavItems(table, '/cases/archive/2024')
    expect(items.find((item) => item.id === '/cases/archive')?.selected).toBe(
      true,
    )
    expect(items.find((item) => item.id === '/cases')?.selected).toBe(false)
    expect(items.find((item) => item.id === '/')?.selected).toBe(false)
  })

  it('lets an exact match outrank a prefix parent at the same literal depth', () => {
    const table: RouteTable = [
      { path: '/cases', view, label: 'Cases' },
      { path: '/cases/:caseId', view, label: 'Case' },
    ]
    const items = deriveNavItems(table, '/cases/123')
    expect(items.find((item) => item.id === '/cases/:caseId')?.selected).toBe(
      true,
    )
    expect(items.find((item) => item.id === '/cases')?.selected).toBe(false)
  })

  it('selects nothing when the current path matches no declaration', () => {
    const table: RouteTable = [
      { path: '/', view, label: 'Home' },
      { path: '/cases', view, label: 'Cases' },
    ]
    expect(deriveNavItems(table, '/reports').every((item) => !item.selected)).toBe(
      true,
    )
  })

  it('requires a segment boundary to treat a path as below a declaration', () => {
    const table: RouteTable = [{ path: '/cases', view, label: 'Cases' }]
    expect(deriveNavItems(table, '/cases/123')[0]?.selected).toBe(true)
    expect(deriveNavItems(table, '/casesx')[0]?.selected).toBe(false)
  })

  it('builds href from the declaration path, param template included', () => {
    const table: RouteTable = [{ path: '/cases/:caseId', view, label: 'Case' }]
    const items = deriveNavItems(table, '/')
    expect(items[0]?.href).toBe('/cases/:caseId')
    expect(items[0]?.id).toBe('/cases/:caseId')
  })

  it('puts the deployment prefix in href and leaves id on the path', () => {
    const table: RouteTable = [
      { path: '/', view, label: 'Home' },
      { path: '/cases', view, label: 'Cases' },
    ]
    const items = deriveNavItems(table, '/cases', '/console')
    expect(items.find((item) => item.id === '/cases')?.href).toBe(
      '/console/cases',
    )
    // The root path resolves to the prefix itself.
    expect(items.find((item) => item.id === '/')?.href).toBe('/console')
  })

  it('leaves href alone for a root or trailing-slash prefix', () => {
    const table: RouteTable = [{ path: '/cases', view, label: 'Cases' }]
    expect(deriveNavItems(table, '/cases', undefined)[0]?.href).toBe('/cases')
    expect(deriveNavItems(table, '/cases', '/')[0]?.href).toBe('/cases')
    expect(deriveNavItems(table, '/cases', '/console/')[0]?.href).toBe(
      '/console/cases',
    )
  })
})
