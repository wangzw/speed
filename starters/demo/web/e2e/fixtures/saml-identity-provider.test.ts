/**
 * The SAML identity provider's wire behaviour.
 *
 * The cases drive the provider over its real listeners: the https provider
 * receives an AuthnRequest on the redirect binding and answers a page that posts
 * a response, and the plain-http attacker origin serves the response the
 * provider kept back. The signing helper is replaced by a script that only
 * encodes its input, because the case is about what the provider puts into the
 * response and where it sends it; the signature itself is the Go helper's own
 * test and the journey's.
 */
import { chmod, mkdtemp, rm, writeFile } from 'node:fs/promises'
import { request as httpRequest, type IncomingMessage } from 'node:http'
import { request as httpsRequest } from 'node:https'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { deflateRawSync } from 'node:zlib'

import { afterEach, beforeEach, describe, expect, it } from 'vitest'

import {
  samlIdentityProviderCopy,
  startSamlIdentityProvider,
  type SamlIdentityProvider,
} from './saml-identity-provider'

const ACS = 'http://localhost:9/api/v1/auth/callback/provider-1'
const AUDIENCE = 'http://localhost:9/api/v1/auth/sso/saml/provider-1/metadata'
const REQUEST_ID = '_request-1'

/** One response read whole. */
interface Answer {
  readonly status: number
  readonly body: string
}

/** Send one request to a URL, over the transport its scheme names. */
function send(url: URL, init: { method?: string; body?: string } = {}): Promise<Answer> {
  return new Promise((resolve, reject) => {
    const onResponse = (response: IncomingMessage): void => {
      const chunks: Buffer[] = []
      response.on('data', (chunk: Buffer) => chunks.push(chunk))
      response.on('end', () =>
        resolve({
          status: response.statusCode ?? 0,
          body: Buffer.concat(chunks).toString('utf8'),
        }),
      )
      response.on('error', reject)
    }
    const headers =
      init.body === undefined
        ? undefined
        : { 'Content-Type': 'application/x-www-form-urlencoded' }
    const outgoing =
      url.protocol === 'https:'
        ? httpsRequest(url, { method: init.method ?? 'GET', headers, rejectUnauthorized: false }, onResponse)
        : httpRequest(url, { method: init.method ?? 'GET', headers }, onResponse)
    outgoing.on('error', reject)
    outgoing.end(init.body)
  })
}

/** The AuthnRequest URL a service provider's redirect names. */
function redirectTo(provider: SamlIdentityProvider): URL {
  const document =
    `<samlp:AuthnRequest xmlns:samlp="urn:oasis:names:tc:SAML:2.0:protocol" ` +
    `xmlns:saml="urn:oasis:names:tc:SAML:2.0:assertion" ID="${REQUEST_ID}" ` +
    `AssertionConsumerServiceURL="${ACS}" Version="2.0">` +
    `<saml:Issuer xmlns:saml="urn:oasis:names:tc:SAML:2.0:assertion">${AUDIENCE}</saml:Issuer>` +
    `</samlp:AuthnRequest>`
  const url = new URL(provider.ssoURL)
  url.searchParams.set('SAMLRequest', deflateRawSync(document).toString('base64'))
  url.searchParams.set('RelayState', 'relay-1')
  return url
}

/** The hidden fields of the page that posts a response. */
function postedFields(page: string): { action: string; response: string; relay: string } {
  const action = /<form method="post" action="([^"]*)"/.exec(page)?.[1]
  const response = /name="SAMLResponse" value="([^"]*)"/.exec(page)?.[1]
  const relay = /name="RelayState" value="([^"]*)"/.exec(page)?.[1]
  if (action === undefined || response === undefined || relay === undefined) {
    throw new Error(`the page posts no response: ${page}`)
  }
  return { action, response, relay }
}

describe('the SAML identity provider', () => {
  let dir: string
  let provider: SamlIdentityProvider

  beforeEach(async () => {
    dir = await mkdtemp(join(tmpdir(), 'speed-e2e-saml-idp-test-'))
    const signer = join(dir, 'signer.sh')
    await writeFile(signer, '#!/bin/sh\ncat | base64 | tr -d "\\n"\n', 'utf8')
    await chmod(signer, 0o700)
    provider = await startSamlIdentityProvider({ dir, signer })
  })

  afterEach(async () => {
    await provider.stop()
    await rm(dir, { recursive: true, force: true })
  })

  /** Walk the provider's page and approve, answering the page that follows. */
  async function approve(): Promise<Answer> {
    const page = await send(redirectTo(provider))
    expect(page.status).toBe(200)
    expect(page.body).toContain(samlIdentityProviderCopy.heading)
    const token = /name="token" value="([^"]*)"/.exec(page.body)?.[1] ?? ''
    return send(new URL('/approve', provider.ssoURL), {
      method: 'POST',
      body: new URLSearchParams({ token }).toString(),
    })
  }

  it('answers an approved request with a page that posts a response for that request to the ACS', async () => {
    const answer = await approve()
    expect(answer.status).toBe(200)
    const posted = postedFields(answer.body)
    expect(posted.action).toBe(ACS)
    expect(posted.relay).toBe('relay-1')
    const xml = Buffer.from(posted.response, 'base64').toString('utf8')
    expect(xml).toContain(`InResponseTo="${REQUEST_ID}"`)
    expect(xml).toContain(`Destination="${ACS}"`)
    expect(xml).toContain(`<saml:Audience>${AUDIENCE}</saml:Audience>`)
    expect(xml).toContain(`<saml:Issuer>${provider.entityID}</saml:Issuer>`)
  })

  it('keeps the next response back and serves it from the attacker origin', async () => {
    expect((await send(new URL(provider.attackURL))).status).toBe(404)

    provider.holdNextResponse()
    const held = await approve()
    expect(held.body).toContain(samlIdentityProviderCopy.heldHeading)
    expect(held.body).not.toContain('SAMLResponse')

    const attack = await send(new URL(provider.attackURL))
    expect(attack.status).toBe(200)
    const posted = postedFields(attack.body)
    expect(posted.action).toBe(ACS)
    expect(Buffer.from(posted.response, 'base64').toString('utf8')).toContain(
      `InResponseTo="${REQUEST_ID}"`,
    )

    // Only the one response is held: the next approval is delivered.
    expect(postedFields((await approve()).body).action).toBe(ACS)
  })

  it('refuses an approval for a request it never showed', async () => {
    const answer = await send(new URL('/approve', provider.ssoURL), {
      method: 'POST',
      body: new URLSearchParams({ token: 'unknown' }).toString(),
    })
    expect(answer.status).toBe(400)
  })
})
