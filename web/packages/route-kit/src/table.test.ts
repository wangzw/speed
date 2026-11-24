import { describe, expect, it } from 'vitest'
import { assertRouteTable } from './table.js'
import type { RouteDeclaration, RouteTable } from './types.js'

const view = (): null => null
const fallback = (): null => null

describe('assertRouteTable', () => {
  it('passes a valid table silently', () => {
    const table: RouteTable = [
      { path: '/', view, label: 'Home' },
      { path: '/cases', view, label: 'Cases' },
      { path: '/cases/:caseId', view },
    ]
    expect(() => assertRouteTable(table, fallback)).not.toThrow()
  })

  it('rejects a missing fallback', () => {
    expect(() => assertRouteTable([], undefined)).toThrow(/fallback/)
  })

  it('rejects a path that does not start with a slash, naming the index', () => {
    const table: RouteTable = [{ path: 'cases', view }]
    expect(() => assertRouteTable(table, fallback)).toThrow(/#0/)
  })

  it('rejects a declaration with no view function, naming its path', () => {
    const table = [{ path: '/cases' }] as unknown as RouteTable
    expect(() => assertRouteTable(table, fallback)).toThrow(/\/cases/)
  })

  it('rejects a duplicate pattern, naming both declarations', () => {
    const table: RouteTable = [
      { path: '/cases', view },
      { path: '/cases', view },
    ]
    expect(() => assertRouteTable(table, fallback)).toThrow(
      /Duplicate route paths "\/cases" and "\/cases"/,
    )
  })

  it('rejects an ambiguous pattern pair', () => {
    const table: RouteTable = [
      { path: '/cases/:id', view },
      { path: '/cases/:caseId', view },
    ]
    expect(() => assertRouteTable(table, fallback)).toThrow(/Ambiguous/)
  })

  it('accepts a literal declared before the param pattern it narrows', () => {
    const table: RouteTable = [
      { path: '/cases/new', view },
      { path: '/cases/:id', view },
    ]
    expect(() => assertRouteTable(table, fallback)).not.toThrow()
  })

  it('rejects a param pattern declared before the literal it would shadow', () => {
    const table: RouteTable = [
      { path: '/cases/:id', view },
      { path: '/cases/new', view },
    ]
    expect(() => assertRouteTable(table, fallback)).toThrow(/Ambiguous/)
  })

  it('accepts distinct literals at the same depth', () => {
    const table: RouteTable = [
      { path: '/cases/new', view },
      { path: '/cases/old', view },
    ]
    expect(() => assertRouteTable(table, fallback)).not.toThrow()
  })

  it('accepts a parent and its own child route', () => {
    const table: RouteTable = [
      { path: '/cases', view, label: 'Cases' },
      { path: '/cases/:caseId/edit', view },
    ]
    expect(() => assertRouteTable(table, fallback)).not.toThrow()
  })

  it('does not inspect permission or label', () => {
    const withPerm: RouteDeclaration = {
      path: '/admin',
      view,
      label: 'Admin',
      permission: 'admin:read',
    }
    expect(() => assertRouteTable([withPerm], fallback)).not.toThrow()
  })

  it('accepts an ungated table without any gating inputs', () => {
    const table: RouteTable = [
      { path: '/', view, label: 'Home' },
      { path: '/cases', view },
    ]
    expect(() => assertRouteTable(table, fallback)).not.toThrow()
  })

  it('rejects a gated table with no presence predicate', () => {
    const table: RouteTable = [
      { path: '/objects', view, requires: ['objectstore.objects'] },
    ]
    expect(() =>
      assertRouteTable(table, fallback, {
        pending: fallback,
        failed: fallback,
      }),
    ).toThrow(/presence/)
  })

  it('rejects a gated table with no pending view', () => {
    const table: RouteTable = [
      { path: '/objects', view, requires: ['objectstore.objects'] },
    ]
    expect(() =>
      assertRouteTable(table, fallback, {
        presence: { state: 'pending' },
        failed: fallback,
      }),
    ).toThrow(/pending/)
  })

  it('rejects a gated table with no failed view', () => {
    const table: RouteTable = [
      { path: '/objects', view, requires: ['objectstore.objects'] },
    ]
    expect(() =>
      assertRouteTable(table, fallback, {
        presence: { state: 'pending' },
        pending: fallback,
      }),
    ).toThrow(/failed/)
  })

  it('accepts a gated table with the predicate and both views', () => {
    const table: RouteTable = [
      { path: '/objects', view, requires: ['objectstore.objects'] },
    ]
    expect(() =>
      assertRouteTable(table, fallback, {
        presence: { state: 'pending' },
        pending: fallback,
        failed: fallback,
      }),
    ).not.toThrow()
  })
})
