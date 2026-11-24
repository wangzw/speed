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

export const samlIdentityProviderUser = {
  subject: 'saml-signin-user',
  email: `saml-employee@${enterpriseIdentityProviderDomain}`,
  name: 'SAML Employee',
} as const

export const samlIdentityProviderUsername = 'saml-employee'

export const samlIdentityProviderClaims = {
  email: 'email',
  displayName: 'display_name',
} as const

export const samlIdentityProviderCopy = {
  heading: 'Sign in with your SAML account',
  approve: 'Continue',
  accountSentence: (email: string): string => `Continue as ${email}`,
  heldHeading: 'Response issued',
  heldSentence: 'The sign-in response was issued to the requesting browser.',
  attackerHeading: 'Claim your reward',
} as const

const ATTACK_PATH = '/claim'

const SIGN_TIMEOUT_MS = 30_000

// The product bounds a response at ten minutes; this stays well inside.
const RESPONSE_LIFETIME_MS = 5 * 60_000

interface AuthnRequest {
  readonly id: string
  readonly acsURL: string
  readonly audience: string
  readonly relayState: string
}

interface IssuedResponse {
  readonly acsURL: string
  readonly samlResponse: string
  readonly relayState: string
}

export interface SamlIdentityProvider {
  readonly entityID: string
  readonly ssoURL: string
  readonly certificatePEM: string
  readonly attackURL: string
  holdNextResponse(): void
  stop(): Promise<void>
}

export interface SamlIdentityProviderInput {
  readonly dir: string
  readonly signer: string
}

function xml(value: string): string {
  return value
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
}

function unxml(value: string): string {
  return value
    .replace(/&lt;/g, '<')
    .replace(/&gt;/g, '>')
    .replace(/&quot;/g, '"')
    .replace(/&apos;/g, "'")
    .replace(/&amp;/g, '&')
}

function instant(at: Date): string {
  return at.toISOString().replace(/\.\d{3}Z$/, 'Z')
}

// An XML ID may not start with a digit.
function identifier(): string {
  return `_${randomBytes(16).toString('hex')}`
}

// The SAMLRequest is base64 of the raw-deflated XML on the redirect binding; RelayState is echoed untouched.
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

export async function startSamlIdentityProvider(
  input: SamlIdentityProviderInput,
): Promise<SamlIdentityProvider> {
  const directory = join(input.dir, 'saml-identity-provider')
  const signingDirectory = join(directory, 'signing')
  const tlsDirectory = join(directory, 'tls')
  await mkdir(signingDirectory, { recursive: true })
  // The signing identity is an RSA key and a self-signed certificate; the certificate is what the operator registers.
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
    // A half-open pair would leak a socket, so both listeners are closed before the failure leaves.
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
