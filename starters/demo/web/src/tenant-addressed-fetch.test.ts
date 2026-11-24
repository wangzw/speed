import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  tenantAddressedFetch,
  tenantFromRequestURL,
} from './tenant-addressed-fetch.js'

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('tenantFromRequestURL', () => {
  it('reads the identifier of a tenant-addressed path', () => {
    expect(tenantFromRequestURL('/api/v1/tenants/globex/sso/providers')).toBe(
      'globex',
    )
    expect(
      tenantFromRequestURL(
        'http://localhost:8081/api/v1/tenants/t_acme/members?after=x',
      ),
    ).toBe('t_acme')
    expect(tenantFromRequestURL('/api/v1/tenants/acme')).toBe('acme')
    expect(
      tenantFromRequestURL('https://host/app/api/v1/tenants/t_acme/members'),
    ).toBe('t_acme')
  })

  it('answers nothing for a path that is not tenant-addressed', () => {
    expect(tenantFromRequestURL('/api/v1/auth/me')).toBeNull()
    expect(tenantFromRequestURL('/api/v1/system/tenants')).toBeNull()
    expect(tenantFromRequestURL('/api/v1/tenants/')).toBeNull()
    expect(tenantFromRequestURL('not a url')).toBeNull()
  })
})

describe('tenantAddressedFetch', () => {
  it('announces the identifier a tenant-addressed path names', async () => {
    const fetchStub = vi.fn(async () => new Response(null, { status: 204 }))
    vi.stubGlobal('fetch', fetchStub)

    await tenantAddressedFetch('/api/v1/tenants/globex/sso/providers', {
      headers: new Headers({
        'x-tenant': 'acme',
        accept: 'application/json',
      }),
    })

    const called = fetchStub.mock.calls[0] as unknown as [string, RequestInit]
    const headers = new Headers(called[1].headers)
    expect(headers.get('x-tenant')).toBe('globex')
    expect(headers.get('accept')).toBe('application/json')
  })

  it('leaves every other request on the client value', async () => {
    const fetchStub = vi.fn(async () => new Response(null, { status: 204 }))
    vi.stubGlobal('fetch', fetchStub)

    const init: RequestInit = {
      headers: new Headers({ 'x-tenant': 'acme' }),
    }
    await tenantAddressedFetch('/api/v1/notifications/messages', init)

    const called = fetchStub.mock.calls[0] as unknown as [string, RequestInit]
    expect(called[1]).toBe(init)
    expect(new Headers(called[1].headers).get('x-tenant')).toBe('acme')
  })
})
