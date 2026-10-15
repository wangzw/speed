/**
 * pathing.ts -- the pure path algebra behind matching and selection:
 * segmentation, literal prefixes, the "sits below" test, the pattern
 * conflict test, and the first-match-wins declaration match. Asserted
 * against concrete inputs so a wrong branch (a missing segment boundary, an
 * end-anchored vs prefix match, a param-vs-literal confusion) is what fails.
 */

import { describe, expect, it } from 'vitest'
import {
  isParamSegment,
  isUnder,
  literalPath,
  matchDeclaration,
  matchesPattern,
  patternsConflict,
  splitPath,
} from './pathing.js'
import type { RouteTable } from './types.js'

const view = (): null => null

describe('splitPath', () => {
  it('drops the empty segments a leading or trailing slash leaves', () => {
    expect(splitPath('/cases/123')).toEqual(['cases', '123'])
    expect(splitPath('/cases/')).toEqual(['cases'])
    expect(splitPath('/')).toEqual([])
  })
})

describe('isParamSegment', () => {
  it('is true only for a :name segment', () => {
    expect(isParamSegment(':caseId')).toBe(true)
    expect(isParamSegment('cases')).toBe(false)
    expect(isParamSegment('caseId')).toBe(false)
  })
})

describe('literalPath', () => {
  it('keeps the leading literal segments and drops everything from the first param on', () => {
    expect(literalPath('/cases/:caseId')).toBe('/cases')
    expect(literalPath('/cases/:caseId/edit')).toBe('/cases')
    expect(literalPath('/cases')).toBe('/cases')
    expect(literalPath('/cases/archive')).toBe('/cases/archive')
  })

  it('reduces a pattern with no literal segment to the root', () => {
    expect(literalPath('/:id')).toBe('/')
    expect(literalPath('/')).toBe('/')
  })
})

describe('isUnder', () => {
  it('is true for a segment-boundary descendant', () => {
    expect(isUnder('/cases/123', '/cases')).toBe(true)
    expect(isUnder('/cases/123/edit', '/cases')).toBe(true)
  })

  it('is false for the base itself, a non-boundary prefix, and a sibling', () => {
    expect(isUnder('/cases', '/cases')).toBe(false)
    expect(isUnder('/casesx', '/cases')).toBe(false)
    expect(isUnder('/other', '/cases')).toBe(false)
  })

  it('does not treat the root as an ancestor', () => {
    expect(isUnder('/cases/123', '/')).toBe(false)
    expect(isUnder('/anything/at/all', '/')).toBe(false)
  })
})

describe('matchesPattern', () => {
  it('matches a param segment but requires the whole path (end-anchored)', () => {
    expect(matchesPattern('/cases/:caseId', '/cases/123')).toBe(true)
    expect(matchesPattern('/cases/:caseId', '/cases')).toBe(false)
    expect(matchesPattern('/cases', '/cases/123')).toBe(false)
    expect(matchesPattern('/cases', '/cases')).toBe(true)
  })
})

describe('patternsConflict', () => {
  it('flags a literal duplicate', () => {
    expect(patternsConflict('/cases', '/cases')).toBe(true)
    expect(patternsConflict('/cases/:id', '/cases/:id')).toBe(true)
  })

  it('flags a structural ambiguity at equal depth', () => {
    expect(patternsConflict('/cases/:id', '/cases/:caseId')).toBe(true)
    expect(patternsConflict('/cases/new', '/cases/:id')).toBe(true)
  })

  it('passes two distinct literals and two different depths', () => {
    expect(patternsConflict('/cases/new', '/cases/old')).toBe(false)
    expect(patternsConflict('/cases/:id', '/cases/:id/edit')).toBe(false)
    expect(patternsConflict('/cases', '/reports')).toBe(false)
  })
})

describe('matchDeclaration', () => {
  it('returns the first matching declaration and its captured params', () => {
    const table: RouteTable = [
      { path: '/cases', view },
      { path: '/cases/:caseId', view },
    ]
    const match = matchDeclaration(table, '/cases/123')
    expect(match?.declaration.path).toBe('/cases/:caseId')
    expect(match?.context.params).toEqual({ caseId: '123' })
    expect(match?.context.path).toBe('/cases/123')
  })

  it('takes the first of two declarations that both match', () => {
    const first = { path: '/cases/new', view }
    const second = { path: '/cases/:caseId', view }
    const table: RouteTable = [first, second]
    expect(matchDeclaration(table, '/cases/new')?.declaration).toBe(first)
  })

  it('returns undefined with no full match', () => {
    const table: RouteTable = [{ path: '/cases', view }]
    expect(matchDeclaration(table, '/cases/123')).toBeUndefined()
    expect(matchDeclaration(table, '/reports')).toBeUndefined()
  })

  it('captures no params for a literal match', () => {
    const table: RouteTable = [{ path: '/cases', view }]
    expect(matchDeclaration(table, '/cases')?.context.params).toEqual({})
  })
})
