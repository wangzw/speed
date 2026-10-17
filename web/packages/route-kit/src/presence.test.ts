/**
 * presence.ts -- the effective registration surface. Each case pins one rule:
 * ungated declarations always stay, a gated one stays only when the predicate
 * is ready and every name it requires is present, and table order and
 * identity survive the narrowing.
 */

import { describe, expect, it } from 'vitest'
import { presentDeclarations } from './presence.js'
import type { RoutePresence } from './types.js'

interface Decl {
  readonly path: string
  readonly requires?: readonly string[]
}

const home: Decl = { path: '/' }
const objects: Decl = { path: '/objects', requires: ['objectstore.objects'] }
const reports: Decl = {
  path: '/reports',
  requires: ['reporting.exports', 'reporting.schedules'],
}
const ungatedAdmin: Decl = { path: '/admin' }
const table: readonly Decl[] = [home, objects, ungatedAdmin, reports]

function ready(names: readonly string[]): RoutePresence {
  return { state: 'ready', has: (name) => names.includes(name) }
}

describe('presentDeclarations', () => {
  it('keeps every ungated declaration while the predicate is absent', () => {
    expect(presentDeclarations(table, undefined)).toEqual([home, ungatedAdmin])
  })

  it('keeps every ungated declaration while the predicate is pending', () => {
    expect(presentDeclarations(table, { state: 'pending' })).toEqual([
      home,
      ungatedAdmin,
    ])
  })

  it('keeps every ungated declaration when the predicate failed', () => {
    expect(presentDeclarations(table, { state: 'failed' })).toEqual([
      home,
      ungatedAdmin,
    ])
  })

  it('keeps a gated declaration whose names are all present', () => {
    expect(
      presentDeclarations(table, ready(['objectstore.objects'])),
    ).toEqual([home, objects, ungatedAdmin])
  })

  it('drops a gated declaration when any required name is absent', () => {
    expect(
      presentDeclarations(table, ready(['objectstore.objects', 'reporting.exports'])),
    ).toEqual([home, objects, ungatedAdmin])
  })

  it('keeps a gated declaration when the predicate is ready and everything is present', () => {
    expect(
      presentDeclarations(
        table,
        ready([
          'objectstore.objects',
          'reporting.exports',
          'reporting.schedules',
        ]),
      ),
    ).toEqual(table)
  })

  it('preserves table order and declaration identity', () => {
    const present = presentDeclarations(table, ready(['objectstore.objects']))
    expect(present.map((declaration) => declaration.path)).toEqual([
      '/',
      '/objects',
      '/admin',
    ])
    expect(present[1]).toBe(objects)
  })

  it('treats an empty requirement list as ungated', () => {
    const empty: Decl = { path: '/empty', requires: [] }
    expect(presentDeclarations([empty], undefined)).toEqual([empty])
  })
})
