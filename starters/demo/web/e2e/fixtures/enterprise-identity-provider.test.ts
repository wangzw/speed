/**
 * The enterprise provider's OIDC surface.
 *
 * The cases drive the provider over its real listeners: discovery and the
 * server-side endpoints over the HTTPS issuer, the authorization page and its
 * approval over the plain-http browser listener. Nothing here is a call into a
 * fixture function; every assertion is on the wire a product or a browser
 * would see.
 */
import { createHash } from 'node:crypto'
import { mkdtemp, readFile, rm } from 'node:fs/promises'
import { request as httpRequest, type IncomingHttpHeaders, type IncomingMessage } from 'node:http'
import { request as httpsRequest } from 'node:https'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

import { afterEach, beforeEach, describe, expect, it } from 'vitest'

import {
  enterpriseIdentityProviderClient,
  enterpriseIdentityProviderCopy,
  startEnterpriseIdentityProvider,
  type EnterpriseIdentityProvider,
} from './identity-provider'

/** One response read whole, as text. */
interface TextResponse {
  readonly status: number
  readonly headers: IncomingHttpHeaders
  readonly body: string
}

interface SendOptions {
  readonly method?: string
  readonly headers?: Record<string, string>
  readonly body?: string
  /** The run CA's PEM content the issuer's leaf is verified against. */
  readonly ca?: string
}

/** Send one request to a URL, over the transport its scheme names. */
function send(url: URL, options: SendOptions = {}): Promise<TextResponse> {
  return new Promise((resolve, reject) => {
    const onResponse = (response: IncomingMessage): void => {
      const chunks: Buffer[] = []
      response.on('data', (chunk: Buffer) => chunks.push(chunk))
      response.on('end', () => {
        resolve({
          status: response.statusCode ?? 0,
          headers: response.headers,
          body: Buffer.concat(chunks).toString('utf8'),
        })
      })
      response.on('error', reject)
    }
    const outgoing =
      url.protocol === 'https:'
        ? httpsRequest(
            url,
            {
              method: options.method ?? 'GET',
              headers: options.headers,
              ...(options.ca === undefined ? {} : { ca: options.ca }),
            },
            onResponse,
          )
        : httpRequest(
            url,
            { method: options.method ?? 'GET', headers: options.headers },
            onResponse,
          )
    outgoing.on('error', reject)
    if (options.body !== undefined) outgoing.write(options.body)
    outgoing.end()
  })
}

/** Post one form body, for the token endpoint. */
function postForm(
  url: URL,
  form: URLSearchParams,
  headers: Record<string, string>,
  ca: string,
): Promise<TextResponse> {
  const body = form.toString()
  return send(url, {
    method: 'POST',
    ca,
    headers: {
      'Content-Type': 'application/x-www-form-urlencoded',
      'Content-Length': String(Buffer.byteLength(body)),
      ...headers,
    },
    body,
  })
}

/** The discovery document as the product reads it. */
interface Discovery {
  readonly issuer: string
  readonly authorization_endpoint: string
  readonly token_endpoint: string
  readonly userinfo_endpoint: string
  readonly jwks_uri: string
  readonly id_token_signing_alg_values_supported: readonly string[]
}

/** The key set as the product reads it. */
interface KeySet {
  readonly keys: readonly { readonly kid?: string; readonly alg?: string }[]
}

/** The token endpoint's response, the fields these cases read. */
interface TokenResponse {
  readonly access_token?: string
  readonly id_token?: string
  readonly token_type?: string
}

/** The PKCE S256 challenge, matching the product's computation. */
function s256(verifier: string): string {
  return createHash('sha256').update(verifier).digest('base64url')
}

/** Decode a compact JWT's payload, without verifying it. */
function decodePayload(token: string): Record<string, unknown> {
  const parts = token.split('.')
  const payload = parts[1]
  if (payload === undefined) throw new Error('the token is not a compact JWT')
  return JSON.parse(Buffer.from(payload, 'base64url').toString('utf8')) as Record<
    string,
    unknown
  >
}

/** Read the run CA the provider minted, as the PEM content `ca` expects. */
function caOf(provider: EnterpriseIdentityProvider): Promise<string> {
  return readFile(provider.rootCAPath, 'utf8')
}

/** Read and parse a provider's discovery document. */
async function discoveryOf(
  provider: EnterpriseIdentityProvider,
  ca: string,
): Promise<Discovery> {
  const response = await send(
    new URL(`${provider.issuer}/.well-known/openid-configuration`),
    { ca },
  )
  expect(response.status).toBe(200)
  return JSON.parse(response.body) as Discovery
}

/** The callback the product would have registered for this provider. */
const redirectURI = 'http://localhost:41234/authn/gate/callback/provider-1'

/** The PKCE verifier the authorization request and the token request share. */
const codeVerifier = 'e2e-enterprise-code-verifier'

/** The authorization request's parameters, as the product sends them. */
function authorizationParams(): URLSearchParams {
  return new URLSearchParams({
    response_type: 'code',
    client_id: enterpriseIdentityProviderClient.id,
    redirect_uri: redirectURI,
    scope: 'openid email profile',
    state: 'state-1',
    nonce: 'nonce-1',
    code_challenge: s256(codeVerifier),
    code_challenge_method: 'S256',
  })
}

/**
 * Walk the browser half: read the authorization page, approve it, and answer
 * the code the redirect carries.
 */
async function approveAuthorization(
  provider: EnterpriseIdentityProvider,
  ca: string,
): Promise<{ readonly code: string; readonly discovery: Discovery }> {
  const discovery = await discoveryOf(provider, ca)
  const params = authorizationParams()

  const pageURL = new URL(discovery.authorization_endpoint)
  pageURL.search = params.toString()
  const page = await send(pageURL)
  expect(page.status).toBe(200)
  expect(page.body).toContain(enterpriseIdentityProviderCopy.heading)
  expect(page.body).toContain(provider.user.email)

  const approveURL = new URL(discovery.authorization_endpoint)
  approveURL.pathname = '/approve'
  approveURL.search = params.toString()
  const approved = await send(approveURL)
  expect(approved.status).toBe(302)
  const location = approved.headers.location
  if (typeof location !== 'string') {
    throw new Error('the approval answered no location')
  }
  const back = new URL(location)
  expect(back.searchParams.get('state')).toBe('state-1')
  expect(back.searchParams.get('iss')).toBe(provider.issuer)
  const code = back.searchParams.get('code')
  if (code === null) throw new Error('the approval carried no code')
  return { code, discovery }
}

/**
 * Run one authorization round trip and answer what it proved about the user:
 * the identity token's claims and the userinfo document.
 */
async function exchangeForUser(
  provider: EnterpriseIdentityProvider,
  ca: string,
): Promise<{
  readonly idToken: Record<string, unknown>
  readonly userinfo: Record<string, unknown>
}> {
  const { code, discovery } = await approveAuthorization(provider, ca)
  const basic = Buffer.from(
    `${enterpriseIdentityProviderClient.id}:${enterpriseIdentityProviderClient.secret}`,
  ).toString('base64')
  const token = await postForm(
    new URL(discovery.token_endpoint),
    new URLSearchParams({
      grant_type: 'authorization_code',
      code,
      redirect_uri: redirectURI,
      code_verifier: codeVerifier,
    }),
    { Authorization: `Basic ${basic}` },
    ca,
  )
  expect(token.status).toBe(200)
  const body = JSON.parse(token.body) as TokenResponse
  if (body.id_token === undefined || body.access_token === undefined) {
    throw new Error('the token response carried no identity token')
  }
  const userinfo = await send(new URL(discovery.userinfo_endpoint), {
    ca,
    headers: { Authorization: `Bearer ${body.access_token}` },
  })
  expect(userinfo.status).toBe(200)
  return {
    idToken: decodePayload(body.id_token),
    userinfo: JSON.parse(userinfo.body) as Record<string, unknown>,
  }
}

let dir: string
let provider: EnterpriseIdentityProvider | undefined

beforeEach(async () => {
  dir = await mkdtemp(join(tmpdir(), 'speed-e2e-enterprise-provider-'))
})

afterEach(async () => {
  await provider?.stop()
  provider = undefined
  await rm(dir, { recursive: true, force: true })
})

async function start(): Promise<EnterpriseIdentityProvider> {
  provider = await startEnterpriseIdentityProvider({ dir })
  return provider
}

describe('the enterprise provider', () => {
  it('serves discovery and a key set over its own https issuer', async () => {
    const started = await start()
    const ca = await caOf(started)

    const discovery = await discoveryOf(started, ca)
    expect(discovery.issuer).toBe(started.issuer)
    expect(discovery.token_endpoint.startsWith(started.issuer)).toBe(true)
    expect(discovery.userinfo_endpoint.startsWith(started.issuer)).toBe(true)
    expect(discovery.jwks_uri.startsWith(started.issuer)).toBe(true)
    expect(discovery.authorization_endpoint.startsWith('http://127.0.0.1:')).toBe(
      true,
    )
    expect(discovery.id_token_signing_alg_values_supported).toContain('ES256')

    const keys = await send(new URL(discovery.jwks_uri), { ca })
    expect(keys.status).toBe(200)
    const keySet = JSON.parse(keys.body) as KeySet
    expect(keySet.keys).toHaveLength(1)
    expect(keySet.keys[0]?.alg).toBe('ES256')
  })

  it('refuses the issuer without the run CA', async () => {
    const started = await start()

    await expect(
      send(new URL(`${started.issuer}/.well-known/openid-configuration`)),
    ).rejects.toThrow()
  })

  it('answers the code on the product callback and carries the issuer verbatim', async () => {
    const started = await start()
    const ca = await caOf(started)

    const { code } = await approveAuthorization(started, ca)
    expect(code).not.toBe('')
  })

  it('refuses to return a code to a redirect that is not loopback', async () => {
    const started = await start()
    const ca = await caOf(started)
    const discovery = await discoveryOf(started, ca)

    const params = authorizationParams()
    params.set('redirect_uri', 'https://example.test/callback')
    const approveURL = new URL(discovery.authorization_endpoint)
    approveURL.pathname = '/approve'
    approveURL.search = params.toString()

    const approved = await send(approveURL)
    expect(approved.status).toBe(400)
  })

  it('exchanges the code for an identity token for the fixed enterprise user', async () => {
    const started = await start()
    const ca = await caOf(started)
    const { code, discovery } = await approveAuthorization(started, ca)

    const basic = Buffer.from(
      `${enterpriseIdentityProviderClient.id}:${enterpriseIdentityProviderClient.secret}`,
    ).toString('base64')
    const token = await postForm(
      new URL(discovery.token_endpoint),
      new URLSearchParams({
        grant_type: 'authorization_code',
        code,
        redirect_uri: redirectURI,
        code_verifier: codeVerifier,
      }),
      { Authorization: `Basic ${basic}` },
      ca,
    )
    expect(token.status).toBe(200)
    const body = JSON.parse(token.body) as TokenResponse
    if (body.id_token === undefined || body.access_token === undefined) {
      throw new Error('the token response carried no identity token')
    }
    expect(decodePayload(body.id_token)).toMatchObject({
      iss: started.issuer,
      aud: enterpriseIdentityProviderClient.id,
      sub: started.user.subject,
      email: started.user.email,
      email_verified: true,
      name: started.user.name,
      nonce: 'nonce-1',
    })

    const userinfo = await send(new URL(discovery.userinfo_endpoint), {
      ca,
      headers: { Authorization: `Bearer ${body.access_token}` },
    })
    expect(userinfo.status).toBe(200)
    expect(JSON.parse(userinfo.body)).toMatchObject({
      sub: started.user.subject,
      email: started.user.email,
      email_verified: true,
      name: started.user.name,
    })
  })

  it('answers the preferred_username a claim-strategy journey sets, and omits it by default', async () => {
    const started = await start()
    const ca = await caOf(started)

    // The provider's default answer carries no preferred_username: a provider
    // started without a claim-strategy journey asserts nothing about it.
    const unset = await exchangeForUser(started, ca)
    expect(unset.idToken).not.toHaveProperty('preferred_username')
    expect(unset.userinfo).not.toHaveProperty('preferred_username')

    // A claim-strategy journey varies the claim between sign-ins. The very
    // next round trip carries it in both answers the product may read.
    started.setPreferredUsername('annsmith')
    const set = await exchangeForUser(started, ca)
    expect(set.idToken).toMatchObject({
      preferred_username: 'annsmith',
      email: started.user.email,
      sub: started.user.subject,
    })
    expect(set.userinfo).toMatchObject({
      preferred_username: 'annsmith',
      email: started.user.email,
      sub: started.user.subject,
    })
  })

  it('refuses a token request signed with another client secret', async () => {
    const started = await start()
    const ca = await caOf(started)
    const { code, discovery } = await approveAuthorization(started, ca)

    const wrong = Buffer.from(
      `${enterpriseIdentityProviderClient.id}:another-secret`,
    ).toString('base64')
    const token = await postForm(
      new URL(discovery.token_endpoint),
      new URLSearchParams({
        grant_type: 'authorization_code',
        code,
        redirect_uri: redirectURI,
        code_verifier: codeVerifier,
      }),
      { Authorization: `Basic ${wrong}` },
      ca,
    )
    expect(token.status).toBe(401)
    expect(JSON.parse(token.body)).toMatchObject({ error: 'invalid_client' })
  })

  it('refuses the code when the verifier does not match the challenge', async () => {
    const started = await start()
    const ca = await caOf(started)
    const { code, discovery } = await approveAuthorization(started, ca)

    const basic = Buffer.from(
      `${enterpriseIdentityProviderClient.id}:${enterpriseIdentityProviderClient.secret}`,
    ).toString('base64')
    const token = await postForm(
      new URL(discovery.token_endpoint),
      new URLSearchParams({
        grant_type: 'authorization_code',
        code,
        redirect_uri: redirectURI,
        code_verifier: 'another-verifier',
      }),
      { Authorization: `Basic ${basic}` },
      ca,
    )
    expect(token.status).toBe(400)
    expect(JSON.parse(token.body)).toMatchObject({ error: 'invalid_grant' })
  })

  it('serves no discovery on its browser listener and stops answering once stopped', async () => {
    const started = await start()
    const ca = await caOf(started)
    const discovery = await discoveryOf(started, ca)

    const browserOrigin = new URL(discovery.authorization_endpoint).origin
    const onBrowser = await send(
      new URL(`${browserOrigin}/.well-known/openid-configuration`),
    )
    expect(onBrowser.status).toBe(404)

    await started.stop()
    await expect(
      send(new URL(`${started.issuer}/.well-known/openid-configuration`), {
        ca,
      }),
    ).rejects.toThrow()
  })
})
