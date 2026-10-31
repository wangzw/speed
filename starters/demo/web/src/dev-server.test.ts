import { describe, expect, it } from 'vitest'

import {
  DEMO_WEB_DEFAULT_BACKEND_PROXY,
  DEMO_WEB_DEFAULT_PORT,
  devServerPlan,
  devServerPort,
} from './dev-server'

describe('the dev server settings', () => {
  it('defaults to port 8080 and the compiled backend listener', () => {
    expect(devServerPlan({})).toEqual({
      port: 8080,
      strictPort: true,
      proxyTarget: 'http://127.0.0.1:8081',
    })
    expect(DEMO_WEB_DEFAULT_PORT).toBe(8080)
    expect(DEMO_WEB_DEFAULT_BACKEND_PROXY).toBe('http://127.0.0.1:8081')
  })

  it('follows DEMO_WEB_PORT and DEMO_WEB_BACKEND_PROXY when they are set', () => {
    expect(
      devServerPlan({
        DEMO_WEB_PORT: '9080',
        DEMO_WEB_BACKEND_PROXY: 'http://127.0.0.1:9081',
      }),
    ).toEqual({
      port: 9080,
      strictPort: true,
      proxyTarget: 'http://127.0.0.1:9081',
    })
  })

  it('refuses a value that is not a port instead of falling back', () => {
    for (const raw of ['', '0', '-1', '8080.5', 'eighty', '70000', ' 8080']) {
      expect(() => devServerPort(raw), raw).toThrow(
        /DEMO_WEB_PORT must be a whole number/,
      )
    }
    expect(() => devServerPlan({ DEMO_WEB_PORT: 'eighty' })).toThrow(
      /DEMO_WEB_PORT must be a whole number/,
    )
  })
})
