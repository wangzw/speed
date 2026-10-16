/**
 * The route table's shape: the business slot at the root, the console area
 * mounted under its prefix, and the console-area predicate the shell switch
 * reads.
 */

import { describe, expect, it } from 'vitest'
import {
  ACCOUNT_CALLBACK_PATH,
  ACCOUNT_PATH,
  AUTH_CALLBACK_PATH,
  authCallbackRoutes,
  businessRoutes,
  CONSOLE_LANDING_PATH,
  CONSOLE_PREFIX,
  consoleShellRoutes,
  isConsolePath,
  routes,
} from './routes.js'

describe('the route table', () => {
  it('names the business slot at the root and the account page, both labelled, beside the unlabelled callback', () => {
    expect(businessRoutes.map((route) => route.path)).toEqual([
      '/',
      ACCOUNT_PATH,
      ACCOUNT_CALLBACK_PATH,
    ])
    const [home, account, callback] = businessRoutes
    expect(home?.label).toBeDefined()
    expect(account?.label).toBeDefined()
    // The callback is a completion point, not a navigation destination.
    expect(callback?.label).toBeUndefined()
  })

  it('mounts every console page under the console prefix, labelled and permission-coded', () => {
    expect(consoleShellRoutes.map((route) => route.path)).toEqual([
      `${CONSOLE_PREFIX}/tenants`,
      `${CONSOLE_PREFIX}/accounts`,
      `${CONSOLE_PREFIX}/sessions`,
      `${CONSOLE_PREFIX}/impersonations`,
      `${CONSOLE_PREFIX}/permissions`,
      `${CONSOLE_PREFIX}/invitations`,
      `${CONSOLE_PREFIX}/sso`,
      `${CONSOLE_PREFIX}/settings`,
      `${CONSOLE_PREFIX}/sms`,
      `${CONSOLE_PREFIX}/audit`,
    ])
    for (const route of consoleShellRoutes) {
      expect(route.label, route.path).toBeDefined()
      expect(route.permission, route.path).toBeTruthy()
    }
  })

  it('lands the console prefix itself on the console’s first page, unlabelled', () => {
    const landing = routes.find((route) => route.path === CONSOLE_PREFIX)
    // The landing carries no label, so it adds no navigation entry.
    expect(landing).toBeDefined()
    expect(landing?.label).toBeUndefined()
    expect(CONSOLE_LANDING_PATH).toBe(`${CONSOLE_PREFIX}/tenants`)
  })

  it('mounts the auth callback route at its own path, unlabelled', () => {
    expect(authCallbackRoutes.map((route) => route.path)).toEqual([
      AUTH_CALLBACK_PATH,
    ])
    // Unlabelled, so it adds no navigation entry.
    expect(authCallbackRoutes[0]?.label).toBeUndefined()
  })

  it('combines the business slot, the callback route, the console landing and the console pages', () => {
    expect(routes).toHaveLength(
      businessRoutes.length +
        authCallbackRoutes.length +
        consoleShellRoutes.length +
        1,
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
