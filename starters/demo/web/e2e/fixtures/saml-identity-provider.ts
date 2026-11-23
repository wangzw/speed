/**
 * A SAML 2.0 identity provider on loopback, for the SP-initiated SAML journey.
 *
 * The product leaves for this provider with an AuthnRequest on the redirect
 * binding, and the provider answers the way a real one does: it shows a page
 * for one fixed user, and once the person approves it signs a Response for that
 * request's identifier and RelayState and serves a page that posts it to the
 * assertion consumer service. The browser performs every hop; the fixture never
 * calls the product.
 *
 * The provider has two listeners:
 *
 *   - The HTTPS listener is the identity provider itself. The product accepts
 *     only an https single sign-on address in a provider's configuration, so
 *     the address the operator registers is https. The certificate is minted
 *     into the run's temporary directory and is not in the browser's trust
 *     store; the one spec that drives this provider tells its browser context
 *     to accept it, which leaves every other journey's certificate checking as
 *     it is.
 *   - The attacker origin is plain http on a second loopback port. It stands
 *     for any other site on the web: it serves one page that posts a response to
 *     the product's assertion consumer service, so the browser sends that post
 *     cross-site, exactly as it would for a page that is not the identity
 *     provider.
 *
 * Both origins are on 127.0.0.1 while the product answers on localhost, so each
 * is cross-site to the product, the topology a deployed identity provider has.
 *
 * XML signing is the Go helper in starters/demo/server/e2e/samlsign, built by
 * the global setup. It signs with the library the product validates with, so the
 * fixture holds no XML-DSig implementation of its own.
 *
 * `holdNextResponse` is the attacker's side of the second test. An attacker who
 * signs in at the identity provider with a browser of their own receives a
 * response for the flow that browser started; the fixture keeps that response
 * instead of delivering it, and the attacker origin posts it. The response is
 * valid and signed, and it answers a request the victim's flow never made.
 *
 * Only fixtures may assemble the environment. This module spawns processes,
 * writes key material and listens on sockets; a spec or a helper may import its
 * constants and types but never these calls.
 */
import { spawn } from 'node:child_process'
import { randomBytes } from 'node:crypto'
import { mkdir, readFile } from 'node:fs/promises'
import {
  createServer as createHttpServer,
  type IncomingMessage,
  type ServerResponse,
} from 'node:http'
import { createServer as createHttpsServer } from 'node:https'
import { join } from 'node:path'
import { inflateRawSync } from 'node:zlib'

import {
  close,
  enterpriseIdentityProviderDomain,
  escapeHTML,
  freePorts,
  listen,
  mintServerCertificate,
  openssl,
  readBody,
} from './identity-provider'

/** The fixed user the provider authenticates. Its address is on the domain the DNS stub answers for. */
export const samlIdentityProviderUser = {
  subject: 'saml-signin-user',
  email: `saml-employee@${enterpriseIdentityProviderDomain}`,
  name: 'SAML Employee',
} as const

/**
 * The username the product derives from the asserted address when the provider
 * reads its username from the `email` claim: the address's local part.
 */
export const samlIdentityProviderUsername = 'saml-employee'

/**
 * The claim mapping a SAML provider row needs to find the user's address and
 * display name: the attribute names the provider sends, each mapped to the
 * product's own target.
 */
export const samlIdentityProviderClaims = {
  email: 'email',
  displayName: 'display_name',
} as const

/** The copy the provider's own pages render. */
export const samlIdentityProviderCopy = {
  heading: 'Sign in with your SAML account',
  approve: 'Continue',
  accountSentence: (email: string): string => `Continue as ${email}`,
  heldHeading: 'Response issued',
  heldSentence: 'The sign-in response was issued to the requesting browser.',
  attackerHeading: 'Claim your reward',
} as const

/** The route the attacker origin serves the held response on. */
const ATTACK_PATH = '/claim'

/** How long the signing helper may take. */
const SIGN_TIMEOUT_MS = 30_000

/** How long an issued response stays valid, which the product bounds at ten minutes. */
const RESPONSE_LIFETIME_MS = 5 * 60_000

/** What the provider reads out of one AuthnRequest. */
interface AuthnRequest {
  readonly id: string
  readonly acsURL: string
  readonly audience: string
  readonly relayState: string
}

/** One response the provider has signed and not yet delivered. */
interface IssuedResponse {
  readonly acsURL: string
  readonly samlResponse: string
  readonly relayState: string
}

/** A started SAML identity provider. */
export interface SamlIdentityProvider {
  /** The entity identifier a provider row names as `idp_entity_id`. */
  readonly entityID: string
  /** The https single sign-on address a provider row names as `idp_sso_url`. */
  readonly ssoURL: string
  /** The PEM certificate a provider row names in `idp_certs`. */
  readonly certificatePEM: string
  /** The attacker origin's page that posts the held response. */
  readonly attackURL: string
  /** Keep the next response the provider signs instead of delivering it. */
  holdNextResponse(): void
  /** Stop both listeners. */
  stop(): Promise<void>
}

export interface SamlIdentityProviderInput {
  /** The e2e server's temporary directory; the key material lives under it. */
  readonly dir: string
  /** The built signing helper, from the harness manifest. */
  readonly signer: string
}

/** The XML text of one attribute value, escaped. */
function xml(value: string): string {
  return value
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
}

/** Undo the escaping an XML attribute or text node carries. */
function unxml(value: string): string {
  return value
    .replace(/&lt;/g, '<')
    .replace(/&gt;/g, '>')
    .replace(/&quot;/g, '"')
    .replace(/&apos;/g, "'")
    .replace(/&amp;/g, '&')
}

/** A SAML instant: RFC 3339 in UTC, to the second. */
function instant(at: Date): string {
  return at.toISOString().replace(/\.\d{3}Z$/, 'Z')
}

/** A fresh SAML identifier, which an XML ID may not start with a digit. */
function identifier(): string {
  return `_${randomBytes(16).toString('hex')}`
}

/**
 * Read an AuthnRequest off the redirect binding: the SAMLRequest parameter is
 * base64 of the raw-deflated XML, and the RelayState is echoed back untouched.
 */
function readAuthnRequest(url: URL): AuthnRequest {
  const encoded = url.searchParams.get('SAMLRequest')
  if (encoded === null || encoded === '') {
    throw new Error('the request carries no SAMLRequest')
  }
  const document = inflateRawSync(Buffer.from(encoded, 'base64')).toString('utf8')
  const id = /<samlp:AuthnRequest\b[^>]*\sID="([^"]+)"/.exec(document)?.[1]
  const acsURL = /\sAssertionConsumerServiceURL="([^"]+)"/.exec(document)?.[1]
  const audience = /<saml:Issuer\b[^>]*>([^<]+)<\/saml:Issuer>/.exec(document)?.[1]
  if (id === undefined || acsURL === undefined || audience === undefined) {
    throw new Error(`the AuthnRequest names no ID, ACS address or issuer: ${document}`)
  }
  return {
    id: unxml(id),
    acsURL: unxml(acsURL),
    audience: unxml(audience),
    relayState: url.searchParams.get('RelayState') ?? '',
  }
}

/** The unsigned Response the provider answers one request with. */
function buildResponse(
  request: AuthnRequest,
  entityID: string,
  now: Date,
): string {
  const issued = instant(now)
  const notBefore = instant(new Date(now.getTime() - 60_000))
  const notOnOrAfter = instant(new Date(now.getTime() + RESPONSE_LIFETIME_MS))
  const attribute = (name: string, value: string): string =>
    `<saml:Attribute Name="${xml(name)}"><saml:AttributeValue>${xml(value)}</saml:AttributeValue></saml:Attribute>`
  const attributes = [
    attribute('email', samlIdentityProviderUser.email),
    attribute('displayName', samlIdentityProviderUser.name),
  ].join('')
  return (
    `<samlp:Response xmlns:samlp="urn:oasis:names:tc:SAML:2.0:protocol" ` +
    `xmlns:saml="urn:oasis:names:tc:SAML:2.0:assertion" ` +
    `Destination="${xml(request.acsURL)}" ID="${identifier()}" ` +
    `InResponseTo="${xml(request.id)}" IssueInstant="${issued}" Version="2.0">` +
    `<saml:Issuer>${xml(entityID)}</saml:Issuer>` +
    `<samlp:Status><samlp:StatusCode Value="urn:oasis:names:tc:SAML:2.0:status:Success"/></samlp:Status>` +
    `<saml:Assertion ID="${identifier()}" IssueInstant="${issued}" Version="2.0">` +
    `<saml:Issuer>${xml(entityID)}</saml:Issuer>` +
    `<saml:Subject>` +
    `<saml:NameID Format="urn:oasis:names:tc:SAML:2.0:nameid-format:persistent">${xml(samlIdentityProviderUser.subject)}</saml:NameID>` +
    `<saml:SubjectConfirmation Method="urn:oasis:names:tc:SAML:2.0:cm:bearer">` +
    `<saml:SubjectConfirmationData InResponseTo="${xml(request.id)}" ` +
    `NotOnOrAfter="${notOnOrAfter}" Recipient="${xml(request.acsURL)}"/>` +
    `</saml:SubjectConfirmation></saml:Subject>` +
    `<saml:Conditions NotBefore="${notBefore}" NotOnOrAfter="${notOnOrAfter}">` +
    `<saml:AudienceRestriction><saml:Audience>${xml(request.audience)}</saml:Audience></saml:AudienceRestriction>` +
    `</saml:Conditions>` +
    `<saml:AttributeStatement>${attributes}</saml:AttributeStatement>` +
    `</saml:Assertion></samlp:Response>`
  )
}

/** Run the signing helper over one document and answer the base64 response. */
function signResponse(
  signer: string,
  directory: string,
  document: string,
): Promise<string> {
  return new Promise((resolve, reject) => {
    const child = spawn(signer, ['-dir', directory], {
      stdio: ['pipe', 'pipe', 'pipe'],
      timeout: SIGN_TIMEOUT_MS,
    })
    const out: Buffer[] = []
    const err: Buffer[] = []
    child.stdout.on('data', (chunk: Buffer) => out.push(chunk))
    child.stderr.on('data', (chunk: Buffer) => err.push(chunk))
    child.once('error', reject)
    child.once('close', (code) => {
      if (code === 0) {
        resolve(Buffer.concat(out).toString('utf8'))
        return
      }
      reject(
        new Error(
          `the SAML signing helper exited with ${String(code)}: ` +
            Buffer.concat(err).toString('utf8'),
        ),
      )
    })
    child.stdin.end(document)
  })
}

/** Write one HTML page. */
function writePage(
  response: ServerResponse,
  status: number,
  body: string,
): void {
  response.writeHead(status, {
    'Content-Type': 'text/html; charset=utf-8',
    'Content-Length': Buffer.byteLength(body),
    'Cache-Control': 'no-store',
  })
  response.end(body)
}

/** The page the provider shows for one request: the fixed user and one control. */
function approvePage(token: string): string {
  const copy = samlIdentityProviderCopy
  return (
    `<!doctype html><html lang="en"><head><meta charset="utf-8">` +
    `<title>${escapeHTML(copy.heading)}</title></head><body>` +
    `<h1>${escapeHTML(copy.heading)}</h1>` +
    `<p>${escapeHTML(copy.accountSentence(samlIdentityProviderUser.email))}</p>` +
    `<form method="post" action="/approve">` +
    `<input type="hidden" name="token" value="${escapeHTML(token)}">` +
    `<button type="submit">${escapeHTML(copy.approve)}</button>` +
    `</form></body></html>`
  )
}

/** The page that posts one response to the assertion consumer service. */
function postPage(title: string, response: IssuedResponse): string {
  return (
    `<!doctype html><html lang="en"><head><meta charset="utf-8">` +
    `<title>${escapeHTML(title)}</title></head><body>` +
    `<h1>${escapeHTML(title)}</h1>` +
    `<form method="post" action="${escapeHTML(response.acsURL)}">` +
    `<input type="hidden" name="SAMLResponse" value="${escapeHTML(response.samlResponse)}">` +
    `<input type="hidden" name="RelayState" value="${escapeHTML(response.relayState)}">` +
    `<noscript><input type="submit" value="Continue"></noscript>` +
    `</form><script>document.forms[0].submit()</script></body></html>`
  )
}

/**
 * Start the SAML identity provider: key material under `dir/saml-identity-provider`,
 * the https provider and the plain-http attacker origin on two free loopback
 * ports.
 */
export async function startSamlIdentityProvider(
  input: SamlIdentityProviderInput,
): Promise<SamlIdentityProvider> {
  const directory = join(input.dir, 'saml-identity-provider')
  const signingDirectory = join(directory, 'signing')
  const tlsDirectory = join(directory, 'tls')
  await mkdir(signingDirectory, { recursive: true })
  // The identity provider's signing identity: an RSA key and a self-signed
  // certificate, the two files the signing helper reads. The certificate is
  // what the operator registers with the product.
  await openssl([
    'req',
    '-x509',
    '-newkey',
    'rsa:2048',
    '-nodes',
    '-keyout',
    join(signingDirectory, 'key.pem'),
    '-out',
    join(signingDirectory, 'cert.pem'),
    '-days',
    '2',
    '-subj',
    '/CN=speed e2e SAML identity provider',
  ])
  const certificatePEM = await readFile(join(signingDirectory, 'cert.pem'), 'utf8')
  const { certificate, privateKeyPEM } = await mintServerCertificate(tlsDirectory)

  const [httpsPort, attackerPort] = await freePorts(2)
  if (httpsPort === undefined || attackerPort === undefined) {
    throw new Error('the SAML identity provider could not allocate two free ports')
  }
  const origin = `https://127.0.0.1:${httpsPort}`
  const entityID = `${origin}/entity`
  const ssoURL = `${origin}/sso`
  const attackOrigin = `http://127.0.0.1:${attackerPort}`

  const pending = new Map<string, AuthnRequest>()
  let holdNext = false
  let held: IssuedResponse | undefined

  const fail = (response: ServerResponse, error: unknown): void => {
    writePage(response, 400, `<!doctype html><p>${escapeHTML(String(error))}</p>`)
  }

  const provider = async (
    request: IncomingMessage,
    response: ServerResponse,
  ): Promise<void> => {
    const url = new URL(request.url ?? '/', origin)
    if (request.method === 'GET' && url.pathname === '/sso') {
      const parsed = readAuthnRequest(url)
      const token = randomBytes(16).toString('hex')
      pending.set(token, parsed)
      writePage(response, 200, approvePage(token))
      return
    }
    if (request.method === 'POST' && url.pathname === '/approve') {
      const token = new URLSearchParams(await readBody(request)).get('token') ?? ''
      const parsed = pending.get(token)
      if (parsed === undefined) {
        writePage(response, 400, '<!doctype html><p>unknown request</p>')
        return
      }
      pending.delete(token)
      const signed = await signResponse(
        input.signer,
        signingDirectory,
        buildResponse(parsed, entityID, new Date()),
      )
      const issued: IssuedResponse = {
        acsURL: parsed.acsURL,
        samlResponse: signed,
        relayState: parsed.relayState,
      }
      if (holdNext) {
        holdNext = false
        held = issued
        const copy = samlIdentityProviderCopy
        writePage(
          response,
          200,
          `<!doctype html><html lang="en"><head><meta charset="utf-8">` +
            `<title>${escapeHTML(copy.heldHeading)}</title></head><body>` +
            `<h1>${escapeHTML(copy.heldHeading)}</h1>` +
            `<p>${escapeHTML(copy.heldSentence)}</p></body></html>`,
        )
        return
      }
      writePage(response, 200, postPage(samlIdentityProviderCopy.heading, issued))
      return
    }
    writePage(response, 404, '<!doctype html><p>not found</p>')
  }

  const attacker = (request: IncomingMessage, response: ServerResponse): void => {
    const url = new URL(request.url ?? '/', attackOrigin)
    if (request.method === 'GET' && url.pathname === ATTACK_PATH && held !== undefined) {
      writePage(response, 200, postPage(samlIdentityProviderCopy.attackerHeading, held))
      return
    }
    writePage(response, 404, '<!doctype html><p>not found</p>')
  }

  const httpsServer = createHttpsServer(
    { key: privateKeyPEM, cert: certificate },
    (request, response) => {
      provider(request, response).catch((error: unknown) => fail(response, error))
    },
  )
  const attackerServer = createHttpServer(attacker)
  try {
    await Promise.all([
      listen(httpsServer, httpsPort),
      listen(attackerServer, attackerPort),
    ])
  } catch (error) {
    // One listener up and the other refused would leave a socket this fixture
    // never returns, so both are closed before the failure leaves here.
    await Promise.all([close(httpsServer), close(attackerServer)])
    throw error
  }

  let stopped = false
  return {
    entityID,
    ssoURL,
    certificatePEM,
    attackURL: `${attackOrigin}${ATTACK_PATH}`,
    holdNextResponse(): void {
      holdNext = true
    },
    async stop(): Promise<void> {
      if (stopped) return
      stopped = true
      await Promise.all([close(httpsServer), close(attackerServer)])
    },
  }
}
