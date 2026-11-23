/**
 * A real OpenID Connect provider on loopback, for the social and enterprise
 * journeys.
 *
 * The review assembly used to carry fake client credentials and no provider, so
 * a social method could build an authorization URL but no callback could ever
 * reach it. This fixture stands up the provider the review configuration points
 * the Google method at: a discovery document, a key set, a token endpoint, a
 * userinfo endpoint, and an authorization page a person reaches and approves in
 * the browser. The round trip is genuine -- the browser leaves for this page,
 * clicks its control, and comes back to the product's own callback route.
 *
 * The same machinery serves the enterprise journey through
 * `startEnterpriseIdentityProvider`: a second provider with its own issuer,
 * certificate material and fixed user, whose work address is on the domain the
 * DNS stub fixture answers for. The enterprise provider is not a second
 * implementation; it is the same provider started from another definition, so
 * the TLS, discovery, authorization, token and userinfo halves have one
 * behaviour to keep true.
 *
 * The provider has two listeners:
 *
 *   - The HTTPS listener is the issuer and carries everything the product's
 *     restricted egress client reaches: discovery, the key set, the token
 *     exchange and userinfo. The product refuses plain http on that client, so
 *     this half is TLS. The certificate chains to a CA written into the run's
 *     temporary directory, and the review configuration names that CA file; the
 *     product appends its certificate to the system roots it verifies against.
 *   - The authorization page is served over plain http on a second loopback
 *     port, and the discovery document names it as `authorization_endpoint`.
 *     Only the browser reaches it: the product never fetches the authorization
 *     endpoint, it only hands its URL to the user agent. Serving that page over
 *     http is what keeps the tier from turning off Chromium's certificate
 *     checking for every journey; the server-to-server half stays TLS, where
 *     the trust decision actually is.
 *
 * The provider serves one fixed user. A journey that needs an existing account
 * to hold the same address seeds it through the binary's own seed command (the
 * fixture's configuration), never by writing a row. The authorization page's
 * visible copy belongs to the provider, not the product, so it is a constant
 * here rather than a locale-bundle read; a spec asserts it because a person at
 * the provider would see it.
 *
 * Only fixtures may assemble the environment. This module spawns openssl, writes
 * the certificates and listens on sockets; a spec or a helper may import its
 * constants and types but never these calls.
 */
import { execFile } from 'node:child_process'
import {
  createHash,
  generateKeyPairSync,
  randomBytes,
  sign as signPayload,
} from 'node:crypto'
import { mkdir, readFile, writeFile } from 'node:fs/promises'
import {
  createServer as createHttpServer,
  type IncomingMessage,
  type Server as HTTPServer,
  type ServerResponse,
} from 'node:http'
import { createServer as createHttpsServer } from 'node:https'
import { createServer as createNetServer, type Server as NetServer } from 'node:net'
import { join } from 'node:path'

/** The fixed user the provider authenticates. */
export const identityProviderUser = {
  subject: 'social-signin-user',
  email: 'social-signin@e2e.test',
  name: 'Social Signin',
} as const

/** The client the review configuration gives the Google method. */
export const identityProviderClient = {
  id: 'e2e-google-client',
  secret: 'e2e-google-secret',
} as const

/** The copy the provider's own authorization page renders. */
export const identityProviderCopy = {
  heading: 'Sign in with Google',
  approve: 'Continue',
  accountSentence: (email: string): string => `Continue as ${email}`,
} as const

/**
 * The fixed user the enterprise provider authenticates.
 *
 * The address is on `enterpriseIdentityProviderDomain`, the one domain the DNS
 * stub answers for. It is deliberately not the social user's address: the two
 * journeys must not be able to pass by each other's fixtures.
 */
export const enterpriseIdentityProviderUser = {
  subject: 'enterprise-signin-user',
  email: 'employee@enterprise.e2e.test',
  name: 'Enterprise Employee',
} as const

/** The enterprise user's email domain, and the TXT name the DNS stub answers. */
export const enterpriseIdentityProviderDomain = 'enterprise.e2e.test'
export const enterpriseIdentityProviderRecordName = `_speed-authn.${enterpriseIdentityProviderDomain}`

/** The enterprise provider's client, which a spec writes into the provider row. */
export const enterpriseIdentityProviderClient = {
  id: 'e2e-enterprise-client',
  secret: 'e2e-enterprise-secret',
} as const

/**
 * The claim mapping an enterprise provider row needs to find the user's address
 * and display name. Without it the product reads the subject alone, and the
 * journey's email-domain matching has nothing to match.
 */
export const enterpriseIdentityProviderClaims = {
  email: 'email',
  name: 'display_name',
} as const

/** The copy the enterprise provider's own authorization page renders. */
export const enterpriseIdentityProviderCopy = {
  heading: 'Sign in with your enterprise account',
  approve: 'Continue',
  accountSentence: (email: string): string => `Continue as ${email}`,
} as const

/** What one started provider answers in common. */
export interface StartedIdentityProvider {
  /** The OpenID Connect issuer: the HTTPS listener the product discovers. */
  readonly issuer: string
  /** The CA certificate the product verifies the issuer's leaf against. */
  readonly rootCAPath: string
  /**
   * Replace the `preferred_username` claim the provider's next identity token
   * and userinfo answer carry. A claim-strategy just-in-time provider reads it
   * as the account's username, and a journey varying it across sign-ins is the
   * only thing that changes between attempts: the provider still serves its one
   * fixed user. `undefined` omits the claim, which is the provider's default.
   */
  setPreferredUsername(value: string | undefined): void
  stop(): Promise<void>
}

/** The provider a started review server points the Google method at. */
export interface IdentityProvider extends StartedIdentityProvider {
  readonly user: typeof identityProviderUser
}

/** The provider an enterprise journey registers in its provider row. */
export interface EnterpriseIdentityProvider extends StartedIdentityProvider {
  readonly user: typeof enterpriseIdentityProviderUser
  readonly clientID: string
  readonly claims: typeof enterpriseIdentityProviderClaims
}

export interface IdentityProviderInput {
  /** The review server's temporary directory; the certificates live under it. */
  readonly dir: string
}

/** One authorization request the page approved, keyed by its code. */
interface AuthorizationRequest {
  readonly challenge: string
  readonly nonce: string
  readonly redirectURI: string
}

/** How long openssl may take. */
const OPENSSL_TIMEOUT_MS = 30_000

/** Base64url of a buffer or string. */
function base64url(value: Buffer | string): string {
  return Buffer.from(value).toString('base64url')
}

/** The PKCE S256 challenge of a verifier, matching the product's computation. */
function s256(verifier: string): string {
  return createHash('sha256').update(verifier).digest('base64url')
}

/** Escape a value for an HTML attribute or text node. */
export function escapeHTML(value: string): string {
  return value.replace(/[&<>"']/g, (character) => {
    switch (character) {
      case '&':
        return '&amp;'
      case '<':
        return '&lt;'
      case '>':
        return '&gt;'
      case '"':
        return '&quot;'
      default:
        return '&#39;'
    }
  })
}

/** Run one openssl command, failing with its whole output. */
export function openssl(args: readonly string[]): Promise<void> {
  return new Promise((resolve, reject) => {
    execFile(
      'openssl',
      [...args],
      { timeout: OPENSSL_TIMEOUT_MS },
      (error, stdout, stderr) => {
        if (error === null) {
          resolve()
          return
        }
        reject(
          new Error(
            `openssl ${args[0] ?? ''} failed: ${error.message}\n` +
              `standard output:\n${stdout}\nstandard error:\n${stderr}`,
          ),
        )
      },
    )
  })
}

/** Ask the kernel for ports that are free at this moment. */
export async function freePorts(count: number): Promise<number[]> {
  const opened: NetServer[] = []
  try {
    for (let index = 0; index < count; index += 1) {
      const listener = await new Promise<NetServer>((resolve, reject) => {
        const server = createNetServer()
        server.once('error', reject)
        server.listen(0, '127.0.0.1', () => resolve(server))
      })
      opened.push(listener)
    }
    return opened.map((listener) => {
      const address = listener.address()
      if (address === null || typeof address === 'string') {
        throw new Error('a free-port listener answered without a TCP address')
      }
      return address.port
    })
  } finally {
    await Promise.all(
      opened.map(
        (listener) =>
          new Promise<void>((resolve) => {
            listener.close(() => resolve())
          }),
      ),
    )
  }
}

/** Listen on one loopback port, failing the fixture when the bind does not. */
export function listen(server: HTTPServer, port: number): Promise<void> {
  return new Promise((resolve, reject) => {
    server.once('error', reject)
    server.listen(port, '127.0.0.1', () => resolve())
  })
}

/** Close a listener, dropping the connections it still holds. */
export function close(server: HTTPServer): Promise<void> {
  return new Promise((resolve) => {
    server.closeAllConnections()
    server.close(() => resolve())
  })
}

/** Read a request's whole body. */
export async function readBody(request: IncomingMessage): Promise<string> {
  const chunks: Buffer[] = []
  for await (const chunk of request) {
    chunks.push(typeof chunk === 'string' ? Buffer.from(chunk) : chunk)
  }
  return Buffer.concat(chunks).toString('utf8')
}

/** Write one JSON response. */
function writeJSON(
  response: ServerResponse,
  status: number,
  body: unknown,
): void {
  const payload = JSON.stringify(body)
  response.writeHead(status, {
    'Content-Type': 'application/json',
    'Content-Length': Buffer.byteLength(payload),
    'Cache-Control': 'no-store',
  })
  response.end(payload)
}

/** The copy one provider's own authorization page renders. */
interface AuthorizePageCopy {
  readonly heading: string
  readonly approve: string
  readonly accountSentence: (email: string) => string
}

/** The authorization page: the fixed user and the one control to approve. */
function authorizePage(
  params: URLSearchParams,
  email: string,
  copy: AuthorizePageCopy,
): string {
  const hidden = [
    'redirect_uri',
    'state',
    'nonce',
    'code_challenge',
    'code_challenge_method',
    'scope',
  ]
    .map(
      (name) =>
        `<input type="hidden" name="${name}" value="${escapeHTML(
          params.get(name) ?? '',
        )}">`,
    )
    .join('\n      ')
  return `<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <title>${escapeHTML(copy.heading)}</title>
  </head>
  <body>
    <main>
      <h1>${escapeHTML(copy.heading)}</h1>
      <p>${escapeHTML(copy.accountSentence(email))}</p>
      <form method="get" action="/approve">
      ${hidden}
      <button type="submit">${escapeHTML(copy.approve)}</button>
      </form>
    </main>
  </body>
</html>
`
}

/**
 * Mint a private CA and a server leaf under `directory`, and answer the leaf
 * and the CA path.
 *
 * The CA file is what a review configuration names for the restricted egress
 * client, whose certificate verification is extended with it. The leaf's
 * subject alternative names cover the loopback host the issuer is written with.
 * Each provider gets its own directory, so one run's providers never overwrite
 * each other's material.
 */
export async function mintServerCertificate(
  directory: string,
): Promise<{ certificate: string; privateKeyPEM: string; caPath: string }> {
  await mkdir(directory, { recursive: true })
  const caKeyPath = join(directory, 'ca-key.pem')
  const caPath = join(directory, 'ca.pem')
  const leafKeyPath = join(directory, 'server-key.pem')
  const csrPath = join(directory, 'server.csr')
  const leafPath = join(directory, 'server.pem')
  const extensionPath = join(directory, 'server.ext')
  await writeFile(
    extensionPath,
    [
      'basicConstraints=CA:FALSE',
      'keyUsage=digitalSignature,keyEncipherment',
      'extendedKeyUsage=serverAuth',
      'subjectAltName=DNS:localhost,IP:127.0.0.1,IP:::1',
      '',
    ].join('\n'),
    { encoding: 'utf8' },
  )
  // A private CA and a leaf it signs: the configuration names the CA file, whose
  // certificate Go appends to the system roots, and the leaf's subject
  // alternative names cover the loopback host the issuer is written with.
  await openssl([
    'req',
    '-x509',
    '-newkey',
    'ec',
    '-pkeyopt',
    'ec_paramgen_curve:prime256v1',
    '-nodes',
    '-keyout',
    caKeyPath,
    '-out',
    caPath,
    '-days',
    '2',
    '-subj',
    '/CN=speed e2e identity provider CA',
    '-addext',
    'basicConstraints=critical,CA:TRUE',
    '-addext',
    'keyUsage=critical,keyCertSign,cRLSign',
  ])
  await openssl([
    'req',
    '-newkey',
    'ec',
    '-pkeyopt',
    'ec_paramgen_curve:prime256v1',
    '-nodes',
    '-keyout',
    leafKeyPath,
    '-out',
    csrPath,
    '-subj',
    '/CN=localhost',
  ])
  await openssl([
    'x509',
    '-req',
    '-in',
    csrPath,
    '-CA',
    caPath,
    '-CAkey',
    caKeyPath,
    '-CAcreateserial',
    '-out',
    leafPath,
    '-days',
    '2',
    '-sha256',
    '-extfile',
    extensionPath,
  ])
  const [certificate, privateKeyPEM] = await Promise.all([
    readFile(leafPath, 'utf8'),
    readFile(leafKeyPath, 'utf8'),
  ])
  return { certificate, privateKeyPEM, caPath }
}

/**
 * The one signing key a provider mints identity tokens with. Its public half is
 * the key set the product fetches; the private half never leaves this process.
 */
interface SigningKey {
  readonly jwks: unknown
  sign(payload: Record<string, unknown>): string
}

/** Create a fresh ES256 signing key under the given key identifier. */
function createSigningKey(keyID: string): SigningKey {
  const keyPair = generateKeyPairSync('ec', { namedCurve: 'P-256' })
  const publicJWK = keyPair.publicKey.export({ format: 'jwk' })
  if (publicJWK.x === undefined || publicJWK.y === undefined) {
    throw new Error(
      'the identity provider could not read the signing key public coordinates',
    )
  }
  const jwks = {
    keys: [
      {
        kty: 'EC',
        crv: 'P-256',
        x: publicJWK.x,
        y: publicJWK.y,
        kid: keyID,
        use: 'sig',
        alg: 'ES256',
      },
    ],
  }
  return {
    jwks,
    sign(payload: Record<string, unknown>): string {
      const header = base64url(
        JSON.stringify({ alg: 'ES256', kid: keyID, typ: 'JWT' }),
      )
      const body = base64url(JSON.stringify(payload))
      const signature = signPayload(
        'sha256',
        Buffer.from(`${header}.${body}`),
        { key: keyPair.privateKey, dsaEncoding: 'ieee-p1363' },
      )
      return `${header}.${body}.${base64url(signature)}`
    },
  }
}

/** What one provider serves: its user, its client, its page copy and its key. */
interface ProviderDefinition {
  readonly directory: string
  readonly user: {
    readonly subject: string
    readonly email: string
    readonly name: string
  }
  readonly client: { readonly id: string; readonly secret: string }
  readonly copy: AuthorizePageCopy
  readonly keyID: string
}

/**
 * Start one provider: certificates, two listeners, one fixed user.
 *
 * The certificate material is minted into `<dir>/<definition.directory>`, so
 * stopping the server's temporary directory cleans it up with everything else.
 * The CA path is what a review configuration names as the CA file; the issuer
 * is the HTTPS listener the product discovers.
 */
async function startProvider(
  definition: ProviderDefinition,
  input: IdentityProviderInput,
): Promise<StartedIdentityProvider> {
  const tlsDir = join(input.dir, definition.directory)
  const { certificate, privateKeyPEM, caPath } =
    await mintServerCertificate(tlsDir)
  const signer = createSigningKey(definition.keyID)

  const [httpsPort, httpPort] = await freePorts(2)
  if (httpsPort === undefined || httpPort === undefined) {
    throw new Error('the identity provider could not allocate two free ports')
  }
  const issuer = `https://127.0.0.1:${httpsPort}`
  const authorizeOrigin = `http://127.0.0.1:${httpPort}`

  const codes = new Map<string, AuthorizationRequest>()
  const accessTokens = new Map<string, ProviderDefinition['user']>()
  // The claim-strategy journeys vary this between sign-ins; the fixed user and
  // every other claim stay the same. A provider started without a journey
  // touching it answers no preferred_username, which is its default.
  let preferredUsername: string | undefined

  /** The standard preferred_username claim, present only once it is set. */
  function preferredUsernameClaim(): Record<string, string> {
    return preferredUsername === undefined
      ? {}
      : { preferred_username: preferredUsername }
  }

  /** Mint one identity token for the fixed user. */
  function idToken(nonce: string): string {
    const now = Math.floor(Date.now() / 1000)
    return signer.sign({
      iss: issuer,
      sub: definition.user.subject,
      aud: definition.client.id,
      exp: now + 3600,
      iat: now,
      email: definition.user.email,
      email_verified: true,
      name: definition.user.name,
      ...preferredUsernameClaim(),
      nonce,
    })
  }

  /** The client credentials a token request carries. */
  function clientOf(
    request: IncomingMessage,
    form: URLSearchParams,
  ): { id: string; secret: string } {
    const authorization = request.headers.authorization
    if (authorization?.startsWith('Basic ')) {
      const decoded = Buffer.from(
        authorization.slice('Basic '.length),
        'base64',
      ).toString('utf8')
      const separator = decoded.indexOf(':')
      if (separator !== -1) {
        return {
          id: decodeURIComponent(decoded.slice(0, separator)),
          secret: decodeURIComponent(decoded.slice(separator + 1)),
        }
      }
    }
    return {
      id: form.get('client_id') ?? '',
      secret: form.get('client_secret') ?? '',
    }
  }

  /** The discovery document, naming the HTTPS half for the server calls. */
  function discovery(): unknown {
    return {
      issuer,
      authorization_endpoint: `${authorizeOrigin}/authorize`,
      token_endpoint: `${issuer}/token`,
      userinfo_endpoint: `${issuer}/userinfo`,
      jwks_uri: `${issuer}/jwks`,
      response_types_supported: ['code'],
      subject_types_supported: ['public'],
      id_token_signing_alg_values_supported: ['ES256'],
      scopes_supported: ['openid', 'email', 'profile'],
      token_endpoint_auth_methods_supported: [
        'client_secret_basic',
        'client_secret_post',
      ],
    }
  }

  /** The browser-facing half: the authorization page and its approval. */
  const httpServer = createHttpServer((request, response) => {
    void (async () => {
      const url = new URL(request.url ?? '/', authorizeOrigin)
      if (request.method !== 'GET') {
        response.writeHead(405).end()
        return
      }
      if (url.pathname === '/authorize') {
        const params = url.searchParams
        if (params.get('response_type') !== 'code') {
          response.writeHead(400).end('the code response type is required')
          return
        }
        if (
          params.get('code_challenge') === '' ||
          params.get('code_challenge') === null ||
          params.get('code_challenge_method') !== 'S256'
        ) {
          response.writeHead(400).end('a S256 code challenge is required')
          return
        }
        const page = authorizePage(params, definition.user.email, definition.copy)
        response.writeHead(200, {
          'Content-Type': 'text/html; charset=utf-8',
          'Content-Length': Buffer.byteLength(page),
          'Cache-Control': 'no-store',
        })
        response.end(page)
        return
      }
      if (url.pathname === '/approve') {
        const params = url.searchParams
        const challenge = params.get('code_challenge')
        const redirectURI = params.get('redirect_uri')
        if (challenge === null || redirectURI === null) {
          response.writeHead(400).end('the approval carries no request')
          return
        }
        // A real provider never sends a code anywhere but a registered
        // address. This one registers nothing, so it refuses anything that is
        // not loopback: the review browser is the only user agent it serves,
        // and a code must not leave the machine.
        const back = new URL(redirectURI)
        if (
          !['localhost', '127.0.0.1', '::1', '[::1]'].includes(back.hostname)
        ) {
          response.writeHead(400).end('the redirect address is not loopback')
          return
        }
        const code = `code-${base64url(randomBytes(24))}`
        codes.set(code, {
          challenge,
          nonce: params.get('nonce') ?? '',
          redirectURI,
        })
        back.searchParams.set('code', code)
        back.searchParams.set('state', params.get('state') ?? '')
        back.searchParams.set('iss', issuer)
        response.writeHead(302, { Location: back.toString() }).end()
        return
      }
      response.writeHead(404).end()
    })().catch(() => {
      if (!response.headersSent) {
        response.writeHead(500)
      }
      response.end()
    })
  })

  /** The server-facing half: discovery, key set, token, userinfo. */
  const httpsServer = createHttpsServer(
    { cert: certificate, key: privateKeyPEM },
    (request, response) => {
      void (async () => {
        const url = new URL(request.url ?? '/', issuer)
        if (url.pathname === '/.well-known/openid-configuration') {
          writeJSON(response, 200, discovery())
          return
        }
        if (url.pathname === '/jwks') {
          writeJSON(response, 200, signer.jwks)
          return
        }
        if (url.pathname === '/userinfo') {
          const authorization = request.headers.authorization
          const token = authorization?.startsWith('Bearer ')
            ? authorization.slice('Bearer '.length)
            : ''
          const user = accessTokens.get(token)
          if (user === undefined) {
            writeJSON(response, 401, { error: 'invalid_token' })
            return
          }
          writeJSON(response, 200, {
            sub: user.subject,
            email: user.email,
            email_verified: true,
            name: user.name,
            ...preferredUsernameClaim(),
          })
          return
        }
        if (url.pathname === '/token') {
          if (request.method !== 'POST') {
            writeJSON(response, 405, { error: 'invalid_request' })
            return
          }
          const form = new URLSearchParams(await readBody(request))
          if (form.get('grant_type') !== 'authorization_code') {
            writeJSON(response, 400, { error: 'unsupported_grant_type' })
            return
          }
          const client = clientOf(request, form)
          if (
            client.id !== definition.client.id ||
            client.secret !== definition.client.secret
          ) {
            writeJSON(response, 401, { error: 'invalid_client' })
            return
          }
          const code = form.get('code') ?? ''
          const record = codes.get(code)
          if (record === undefined) {
            writeJSON(response, 400, { error: 'invalid_grant' })
            return
          }
          codes.delete(code)
          if (
            form.get('redirect_uri') !== null &&
            form.get('redirect_uri') !== record.redirectURI
          ) {
            writeJSON(response, 400, { error: 'invalid_grant' })
            return
          }
          const verifier = form.get('code_verifier') ?? ''
          if (s256(verifier) !== record.challenge) {
            writeJSON(response, 400, { error: 'invalid_grant' })
            return
          }
          const accessToken = base64url(randomBytes(24))
          accessTokens.set(accessToken, definition.user)
          writeJSON(response, 200, {
            access_token: accessToken,
            token_type: 'Bearer',
            expires_in: 3600,
            id_token: idToken(record.nonce),
          })
          return
        }
        writeJSON(response, 404, { error: 'not_found' })
      })().catch(() => {
        if (!response.headersSent) {
          writeJSON(response, 500, { error: 'server_error' })
          return
        }
        response.end()
      })
    },
  )

  try {
    await Promise.all([
      listen(httpsServer, httpsPort),
      listen(httpServer, httpPort),
    ])
  } catch (error) {
    // One listener up and the other refused would leave a socket this fixture
    // never returns, so both are closed before the failure leaves here.
    await Promise.all([close(httpsServer), close(httpServer)])
    throw error
  }

  let stopped = false
  return {
    issuer,
    rootCAPath: caPath,
    setPreferredUsername(value: string | undefined): void {
      preferredUsername = value
    },
    async stop(): Promise<void> {
      if (stopped) return
      stopped = true
      await Promise.all([close(httpsServer), close(httpServer)])
    },
  }
}

/**
 * Start the social provider: the Google method the review configuration points
 * at, one fixed user, its certificate material under `dir/identity-provider`.
 */
export async function startIdentityProvider(
  input: IdentityProviderInput,
): Promise<IdentityProvider> {
  const started = await startProvider(
    {
      directory: 'identity-provider',
      user: identityProviderUser,
      client: identityProviderClient,
      copy: identityProviderCopy,
      keyID: 'e2e-identity-provider-key',
    },
    input,
  )
  return {
    issuer: started.issuer,
    rootCAPath: started.rootCAPath,
    user: identityProviderUser,
    setPreferredUsername: started.setPreferredUsername,
    stop: started.stop,
  }
}

/**
 * Start the enterprise provider: an OIDC issuer on a concrete https loopback
 * URL with one fixed user whose work address is on the domain the DNS stub
 * answers for.
 *
 * Its certificate material lives under `dir/enterprise-identity-provider`, so
 * it neither shares nor overwrites the social provider's material when both
 * run in one server. The client identity and claim mapping it answers are the
 * values an enterprise provider row is created with.
 */
export async function startEnterpriseIdentityProvider(
  input: IdentityProviderInput,
): Promise<EnterpriseIdentityProvider> {
  const started = await startProvider(
    {
      directory: 'enterprise-identity-provider',
      user: enterpriseIdentityProviderUser,
      client: enterpriseIdentityProviderClient,
      copy: enterpriseIdentityProviderCopy,
      keyID: 'e2e-enterprise-identity-provider-key',
    },
    input,
  )
  return {
    issuer: started.issuer,
    rootCAPath: started.rootCAPath,
    user: enterpriseIdentityProviderUser,
    clientID: enterpriseIdentityProviderClient.id,
    claims: enterpriseIdentityProviderClaims,
    setPreferredUsername: started.setPreferredUsername,
    stop: started.stop,
  }
}
