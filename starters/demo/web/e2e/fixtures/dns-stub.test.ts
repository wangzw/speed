import { Resolver } from 'node:dns/promises'

import { afterEach, describe, expect, it } from 'vitest'

import { startDNSStub, type DNSStub } from './dns-stub'
import {
  enterpriseIdentityProviderDomain,
  enterpriseIdentityProviderRecordName,
} from './identity-provider'

function resolverAt(address: string): Resolver {
  const resolver = new Resolver({ timeout: 500, tries: 1 })
  resolver.setServers([address])
  return resolver
}

let started: DNSStub[] = []

afterEach(async () => {
  const stubs = started
  started = []
  await Promise.all(stubs.map((stub) => stub.stop()))
})

async function start(): Promise<{ stub: DNSStub; resolver: Resolver }> {
  const stub = await startDNSStub({
    recordName: enterpriseIdentityProviderRecordName,
  })
  started.push(stub)
  return { stub, resolver: resolverAt(stub.address) }
}

function lookup(resolver: Resolver): Promise<string[][]> {
  return resolver.resolveTxt(enterpriseIdentityProviderRecordName)
}

describe('the DNS stub', () => {
  it('answers the published TXT record for its one name', async () => {
    const { stub, resolver } = await start()
    stub.setTXT(['speed-authn=first-token'])

    await expect(lookup(resolver)).resolves.toEqual([['speed-authn=first-token']])
  })

  it('answers NXDOMAIN until a value is published, and again after it is cleared', async () => {
    const { stub, resolver } = await start()

    await expect(lookup(resolver)).rejects.toMatchObject({ code: 'ENOTFOUND' })

    stub.setTXT(['speed-authn=first-token'])
    await expect(lookup(resolver)).resolves.toEqual([['speed-authn=first-token']])

    stub.setTXT([])
    await expect(lookup(resolver)).rejects.toMatchObject({ code: 'ENOTFOUND' })
  })

  it('answers NXDOMAIN for a neighbouring domain', async () => {
    const { stub, resolver } = await start()
    stub.setTXT(['speed-authn=first-token'])

    await expect(
      resolver.resolveTxt(
        `_speed-authn.other.${enterpriseIdentityProviderDomain}`,
      ),
    ).rejects.toMatchObject({ code: 'ENOTFOUND' })
  })

  it('answers NXDOMAIN for a name that extends its name on either side', async () => {
    const { stub, resolver } = await start()
    stub.setTXT(['speed-authn=first-token'])

    await expect(
      resolver.resolveTxt(`${enterpriseIdentityProviderRecordName}.evil.test`),
    ).rejects.toMatchObject({ code: 'ENOTFOUND' })
    await expect(
      resolver.resolveTxt(`x${enterpriseIdentityProviderRecordName}`),
    ).rejects.toMatchObject({ code: 'ENOTFOUND' })
  })

  it('answers NXDOMAIN for a non-TXT query even on its own name', async () => {
    const { stub, resolver } = await start()
    stub.setTXT(['speed-authn=first-token'])

    await expect(
      resolver.resolve4(enterpriseIdentityProviderRecordName),
    ).rejects.toMatchObject({ code: 'ENOTFOUND' })
  })

  it('stops answering once stopped', async () => {
    const { stub, resolver } = await start()
    stub.setTXT(['speed-authn=first-token'])
    await stub.stop()

    await expect(lookup(resolver)).rejects.toThrow()
  })
})
