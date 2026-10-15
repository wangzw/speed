/**
 * The route table's shape: the business slot at the root, the console area
 * mounted under its prefix, and the console-area predicate the shell switch
 * reads.
 */

import { describe, expect, it } from 'vitest'
import {
  businessRoutes,
  CONSOLE_PREFIX,
  consoleShellRoutes,
  isConsolePath,
  routes,
} from './routes.js'

describe('the route table', () => {
  it('names the business slot at the root, labelled', () => {
    expect(businessRoutes.map((route) => route.path)).toEqual(['/'])
    expect(businessRoutes[0]?.label).toBeDefined()
  })

  it('mounts every console page under the console prefix, labelled and permission-coded', () => {
    expect(consoleShellRoutes.map((route) => route.path)).toEqual([
      `${CONSOLE_PREFIX}/tenants`,
      `${CONSOLE_PREFIX}/settings`,
      `${CONSOLE_PREFIX}/audit`,
    ])
    for (const route of consoleShellRoutes) {
      expect(route.label, route.path).toBeDefined()
      expect(route.permission, route.path).toBeTruthy()
    }
  })

  it('combines both contributors into the product table', () => {
    expect(routes).toHaveLength(
      businessRoutes.length + consoleShellRoutes.length,
    )
  })
})

describe('isConsolePath', () => {
  it('recognizes the console prefix and its descendants', () => {
    expect(isConsolePath(CONSOLE_PREFIX)).toBe(true)
    expect(isConsolePath(`${CONSOLE_PREFIX}/tenants`)).toBe(true)
  })

  it('rejects the tenant paths and a prefix look-alike', () => {
    expect(isConsolePath('/')).toBe(false)
    expect(isConsolePath('/tenants')).toBe(false)
    expect(isConsolePath(`${CONSOLE_PREFIX}x`)).toBe(false)
  })
})
