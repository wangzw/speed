/**
 * The per-test review server: one temporary directory, one sqlite file, one
 * process, real HTTP.
 *
 * Every spec starts its own server so a journey never inherits state from the
 * one before it. The fixture seeds the database, serves it on free loopback
 * ports, and waits for the startup record before answering; a run that exits or
 * hangs before that record fails with the child's log attached to the message.
 *
 * Stopping is ordered: hooks registered with `beforeStop` run first (the demo
 * fixture closes the browser context there, because the notification stream
 * would otherwise hold shutdown open), then SIGTERM, then SIGKILL if the
 * process has not left within the bound. The child is always run with the
 * temporary directory as its working directory, so every relative path in its
 * configuration resolves there and the token signing key is minted into the
 * temporary directory rather than the source tree.
 */
import { execFile, spawn, type ChildProcess } from 'node:child_process'
import { mkdtemp, readFile, rm, stat } from 'node:fs/promises'
import { get as httpGet, request as httpRequest } from 'node:http'
import { createServer } from 'node:net'
import { createInterface } from 'node:readline'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

import { totpCode } from '../helpers/totp'
import { writeReviewConfig, type ReviewConfig } from './config'
import { startDNSStub, type DNSStub } from './dns-stub'
import {
  enterpriseIdentityProviderRecordName,
  startEnterpriseIdentityProvider,
  startIdentityProvider,
  type EnterpriseIdentityProvider,
  type IdentityProvider,
} from './identity-provider'
import {
  countCarrierMessages,
  countOutboundRecords,
  ProcessLog,
  waitForCarrierMessage,
  waitForOutboundRecord,
  waitForReady,
  type CarrierMessage,
  type OutboundRecord,
} from './log'
import { MANIFEST_PATH } from './paths'
import {
  startSamlIdentityProvider,
  type SamlIdentityProvider,
} from './saml-identity-provider'
import { scaledTimeout } from './time-scale'

// Every bound below is strictly inside the Playwright test timeout (three
// minutes before GATE_TIME_SCALE, in playwright.config.ts), with room left for
// the journey and for teardown. A fixture that used the whole test bound would
// be killed by Playwright first, and the harness's own diagnostics -- the log
// tail and standard error -- would never be printed. Each bound is a wait, not
// work -- it returns as soon as the thing it waits for happens -- so it is
// generous, and it widens with the test timeout through the same
// GATE_TIME_SCALE.

/** How long the seed command may run. */
const SEED_TIMEOUT_MS = scaledTimeout(30_000)

/** How long serve may take to write its ready record. */
const START_TIMEOUT_MS = scaledTimeout(30_000)

/** How long SIGTERM may take before SIGKILL follows. */
const STOP_TIMEOUT_MS = scaledTimeout(25_000)

/** How long the readiness probes may take to answer after the ready record. */
const PROBE_TIMEOUT_MS = scaledTimeout(15_000)

/** How long one readiness probe request may take to answer. */
const PROBE_REQUEST_TIMEOUT_MS = scaledTimeout(5_000)

/** How long one API request the fixture makes may take to answer. */
const API_REQUEST_TIMEOUT_MS = scaledTimeout(10_000)

/** How many times a bind failure is retried with fresh ports. */
const MAX_BIND_ATTEMPTS = 3

interface HarnessManifest {
  readonly binary: string
  /** The default binary the deploy fixture starts. */
  readonly deployBinary: string
  /** The helper that signs the SAML identity provider's responses. */
  readonly samlSigner: string
  readonly repoRoot: string
  readonly serverDir: string
  readonly webDir: string
  readonly builtAt: string
}

/** The build the global setup left for the fixtures to run. */
export async function readHarnessManifest(): Promise<HarnessManifest> {
  let raw: string
  try {
    raw = await readFile(MANIFEST_PATH, 'utf8')
  } catch (error) {
    throw new Error(
      `the harness manifest ${MANIFEST_PATH} could not be read; the Playwright ` +
        `global setup builds the front end and the review binary and writes it. ` +
        `Run the tier through \`make e2e\`, or set GATE_JOBS and run \`pnpm -C web ` +
        `--filter demo-web exec playwright test --config e2e/playwright.config.ts\`: ` +
        `${String(error)}`,
    )
  }
  const parsed: unknown = JSON.parse(raw)
  if (
    typeof parsed !== 'object' ||
    parsed === null ||
    typeof (parsed as { binary?: unknown }).binary !== 'string'
  ) {
    throw new Error(`the harness manifest ${MANIFEST_PATH} names no binary`)
  }
  return parsed as HarnessManifest
}

/** Ask the kernel for ports that are free at this moment. */
async function freePorts(count: number): Promise<number[]> {
  const opened: import('node:net').Server[] = []
  const closeAll = async (): Promise<void> => {
    await Promise.all(
      opened.map(
        (listener) =>
          new Promise<void>((resolve) => {
            if (!listener.listening) {
              resolve()
              return
            }
            listener.close(() => resolve())
          }),
      ),
    )
  }

  try {
    for (let index = 0; index < count; index += 1) {
      const listener = await new Promise<import('node:net').Server>(
        (resolve, reject) => {
          const server = createServer()
          server.once('error', reject)
          server.listen(0, '127.0.0.1', () => resolve(server))
        },
      )
      opened.push(listener)
    }
    const ports = opened.map((listener) => {
      const address = listener.address()
      if (address === null || typeof address === 'string') {
        throw new Error('a free-port listener answered without a TCP address')
      }
      return address.port
    })
    return ports
  } finally {
    // A listener that failed leaves its siblings open unless they are all
    // closed here; a leaked listener holds a port for the rest of the worker.
    await closeAll()
  }
}

/** The environment the child starts with: this process's, less host and OTel. */
function childEnv(): NodeJS.ProcessEnv {
  const env: NodeJS.ProcessEnv = { ...process.env }
  for (const key of Object.keys(env)) {
    if (key.startsWith('DEMO_') || key.startsWith('OTEL_')) delete env[key]
  }
  return env
}

/** Run one one-shot command of the binary to completion. */
function runCommand(
  binary: string,
  args: readonly string[],
  cwd: string,
  what: string,
): Promise<void> {
  return new Promise((resolve, reject) => {
    execFile(
      binary,
      [...args],
      {
        cwd,
        env: childEnv(),
        timeout: SEED_TIMEOUT_MS,
        maxBuffer: 16 * 1024 * 1024,
      },
      (error, stdout, stderr) => {
        if (error === null) {
          resolve()
          return
        }
        reject(
          new Error(
            `${what} failed: ${error.message}\nstandard output:\n${stdout}\nstandard error:\n${stderr}`,
          ),
        )
      },
    )
  })
}

/** One unauthenticated GET; the answer is the status code. */
function probe(url: string): Promise<number> {
  return new Promise((resolve, reject) => {
    const probeRequest = httpGet(url, (response) => {
      response.resume()
      resolve(response.statusCode ?? 0)
    })
    probeRequest.on('error', reject)
    probeRequest.setTimeout(PROBE_REQUEST_TIMEOUT_MS, () => {
      probeRequest.destroy(
        new Error(`${url} did not answer within ${PROBE_REQUEST_TIMEOUT_MS}ms`),
      )
    })
  })
}

/** Poll a probe until it answers 200, or fail after the bound. */
async function waitForProbe(url: string, log: ProcessLog): Promise<void> {
  const deadline = Date.now() + PROBE_TIMEOUT_MS
  let last = ''
  while (Date.now() < deadline) {
    try {
      const status = await probe(url)
      if (status === 200) return
      last = `HTTP ${status}`
    } catch (error) {
      last = String(error)
    }
    await new Promise((resolve) => setTimeout(resolve, 100))
  }
  throw new Error(
    `the operations probe ${url} did not answer 200 within ${PROBE_TIMEOUT_MS}ms ` +
      `(last answer: ${last})\n--- process log (last lines) ---\n${log.tail()}`,
  )
}

/**
 * Wait for the child to leave, answering false on timeout. A spawn failure
 * raises 'error' instead of 'exit'; either one means the process will not
 * run, so both settle the wait.
 */
function waitForExit(child: ChildProcess, timeoutMs: number): Promise<boolean> {
  if (child.exitCode !== null || child.signalCode !== null) {
    return Promise.resolve(true)
  }
  return new Promise((resolve) => {
    const settle = (answer: boolean): void => {
      clearTimeout(timer)
      child.off('exit', onExit)
      child.off('error', onError)
      resolve(answer)
    }
    const onExit = (): void => settle(true)
    const onError = (): void => settle(true)
    const timer = setTimeout(() => settle(false), timeoutMs)
    child.once('exit', onExit)
    child.once('error', onError)
  })
}

/** Fail loudly when a manifest outlives the binary it names. */
async function ensureBinary(binary: string): Promise<void> {
  try {
    const info = await stat(binary)
    if (!info.isFile()) {
      throw new Error('not a regular file')
    }
  } catch (error) {
    throw new Error(
      `the harness manifest names the review binary ${binary}, but it cannot ` +
        `be used (${String(error)}). The binary is built into /tmp by the ` +
        `Playwright global setup; a stale manifest is rebuilt by rerunning ` +
        `the tier through \`make e2e\`, or with GATE_JOBS set, \`pnpm -C web ` +
        `--filter demo-web exec playwright test --config e2e/playwright.config.ts\`.`,
    )
  }
}

/** Whether a server that exited before ready was refused its listener. */
function looksLikeBindFailure(log: ProcessLog, stderr: readonly string[]): boolean {
  const text = `${log.tail(200)}\n${stderr.join('\n')}`
  return /EADDRINUSE|address already in use|cannot assign requested address|bind:/i.test(
    text,
  )
}

/** Whether a provider start failed because a loopback port was taken. */
function looksLikePortTaken(error: unknown): boolean {
  return /EADDRINUSE|address already in use/i.test(String(error))
}

/** One answer from the product's own API, as the fixture reads it. */
interface ApiAnswer {
  readonly status: number
  readonly body: string
  readonly setCookie: readonly string[]
}

/** Make one request against the running server and read the whole answer. */
function callApi(
  url: string,
  options: {
    readonly method: string
    readonly body?: string
    readonly headers?: Readonly<Record<string, string>>
  },
): Promise<ApiAnswer> {
  return new Promise((resolve, reject) => {
    const call = httpRequest(
      url,
      { method: options.method, headers: options.headers },
      (response) => {
        const chunks: Buffer[] = []
        response.on('data', (chunk: Buffer) => chunks.push(chunk))
        response.on('end', () => {
          resolve({
            status: response.statusCode ?? 0,
            body: Buffer.concat(chunks).toString('utf8'),
            setCookie: response.headers['set-cookie'] ?? [],
          })
        })
        response.on('error', reject)
      },
    )
    call.on('error', reject)
    call.setTimeout(API_REQUEST_TIMEOUT_MS, () => {
      call.destroy(
        new Error(`${url} did not answer within ${API_REQUEST_TIMEOUT_MS}ms`),
      )
    })
    if (options.body !== undefined) {
      call.write(options.body)
    }
    call.end()
  })
}

/** The Cookie request header a set of Set-Cookie values asks for. */
function cookieHeader(setCookie: readonly string[]): string {
  return setCookie
    .map((entry) => entry.split(';', 1)[0] ?? '')
    .filter((entry) => entry !== '')
    .join('; ')
}

/** The description of one open login flow, as the flow routes answer it. */
interface FlowReply {
  readonly seq: number
  readonly nextKind: string
  readonly factors: readonly { readonly id: string; readonly kind: string }[]
  readonly accessToken: string
}

/** Read the flow fields the API sign-in needs out of one flow answer. */
function parseFlowReply(body: string): FlowReply {
  const parsed = JSON.parse(body) as {
    readonly seq?: unknown
    readonly next?: {
      readonly kind?: unknown
      readonly factors?: unknown
    }
    readonly access_token?: unknown
  }
  const factors = Array.isArray(parsed.next?.factors)
    ? parsed.next.factors.flatMap((entry) => {
        if (entry === null || typeof entry !== 'object') return []
        const factor = entry as { readonly id?: unknown; readonly kind?: unknown }
        if (typeof factor.id !== 'string' || typeof factor.kind !== 'string') {
          return []
        }
        return [{ id: factor.id, kind: factor.kind }]
      })
    : []
  return {
    seq: typeof parsed.seq === 'number' ? parsed.seq : 0,
    nextKind: typeof parsed.next?.kind === 'string' ? parsed.next.kind : '',
    factors,
    accessToken:
      typeof parsed.access_token === 'string' ? parsed.access_token : '',
  }
}

/** The authenticator's step, matching the review register's fixed thirty seconds. */
const TOTP_STEP_MS = 30_000

/**
 * Sign one account in through the product's own password flow and answer the
 * access token. The flow keeps its state in a cookie, so the answer's cookie is
 * carried to the completing call; the token is what the API requests below use.
 *
 * An account holding a second factor parks the flow on that factor. When the
 * caller names the authenticator seed, the flow selects the authenticator
 * factor, answers its challenge with the code the review seed produces, and
 * completes with the elevated session; a flow that asks for a factor the caller
 * did not name fails loudly rather than completing at the wrong level.
 *
 * The authenticator refuses to match a counter it already accepted, and a
 * browser session of the same account may have used the current step just
 * before this call. The code is therefore asked for one step ahead, which the
 * factor's one-step window accepts and its replay check sees as fresh.
 */
async function signInThroughApi(
  origin: string,
  identifier: string,
  password: string,
  totpSecret?: string,
): Promise<string> {
  const start = await callApi(`${origin}/api/v1/auth/flows`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      method: 'password',
      delivery: 'token',
      binding: 'cookie',
      input: { identifier, password },
    }),
  })
  if (start.status !== 200) {
    throw new Error(
      `starting the API sign-in of ${identifier} answered ${start.status}:\n${start.body}`,
    )
  }
  const cookie = cookieHeader(start.setCookie)
  const headers = {
    'Content-Type': 'application/json',
    ...(cookie === '' ? {} : { Cookie: cookie }),
  }
  const started = parseFlowReply(start.body)
  let seq = started.seq
  if (started.nextKind === 'factor') {
    if (totpSecret === undefined) {
      throw new Error(
        `the API sign-in of ${identifier} asks for a second factor and no authenticator seed was given`,
      )
    }
    const totp = started.factors.find((factor) => factor.kind === 'totp')
    if (totp === undefined) {
      throw new Error(
        `the API sign-in of ${identifier} offers no authenticator factor`,
      )
    }
    const challenge = await callApi(`${origin}/api/v1/auth/flows/answer`, {
      method: 'POST',
      headers,
      body: JSON.stringify({ seq, step: 'factor', input: { factor: totp.id } }),
    })
    if (challenge.status !== 200) {
      throw new Error(
        `selecting the authenticator of ${identifier} answered ${challenge.status}:\n${challenge.body}`,
      )
    }
    seq = parseFlowReply(challenge.body).seq
    const passed = await callApi(`${origin}/api/v1/auth/flows/answer`, {
      method: 'POST',
      headers,
      body: JSON.stringify({
        seq,
        step: 'factor',
        input: {
          factor: totp.id,
          code: totpCode(totpSecret, new Date(Date.now() + TOTP_STEP_MS)),
        },
      }),
    })
    if (passed.status !== 200) {
      throw new Error(
        `answering the authenticator of ${identifier} answered ${passed.status}:\n${passed.body}`,
      )
    }
    seq = parseFlowReply(passed.body).seq
  }
  const answer = await callApi(`${origin}/api/v1/auth/flows/answer`, {
    method: 'POST',
    headers,
    body: JSON.stringify({ seq, step: 'complete' }),
  })
  if (answer.status !== 200) {
    throw new Error(
      `completing the API sign-in of ${identifier} answered ${answer.status}:\n${answer.body}`,
    )
  }
  const parsed = parseFlowReply(answer.body)
  if (parsed.accessToken === '') {
    throw new Error(`the API sign-in of ${identifier} answered no access token`)
  }
  return parsed.accessToken
}

/** The input of one settings write the fixture makes through the product. */
export interface TenantSettingWriteInput {
  readonly tenant: string
  readonly identifier: string
  readonly password: string
  /**
   * The authenticator seed the account's second-factor step needs. Omitted for
   * an account that signs in on the password alone.
   */
  readonly totpSecret?: string
  readonly namespace: string
  readonly key: string
  readonly value: string
}

/** What one settings write answered: its status and, for a problem, its type. */
export interface TenantSettingWriteAnswer {
  readonly status: number
  /** The problem type the refusal named; empty when the answer was not a problem. */
  readonly problemType: string
}

/** One declared setting item, by the key and the namespace that declared it. */
export interface SettingsItem {
  readonly key: string
  readonly namespace: string
}

/** One settings read: the status it answered and the items it returned. */
export interface SettingsReadAnswer {
  readonly status: number
  readonly items: readonly SettingsItem[]
}

/** The account an API settings session signs in as. */
export interface SettingsAccount {
  readonly identifier: string
  readonly password: string
}

/**
 * The input of the tenant surface's zero-visibility probe: the tenant
 * administrator's account, the platform items to address from the tenant, and
 * the namespaces to read as a namespace.
 */
export interface TenantSettingsProbeInput extends SettingsAccount {
  readonly tenant: string
  readonly items: readonly SettingsItem[]
  readonly namespaces: readonly string[]
}

/** What the tenant surface answered to one zero-visibility probe. */
export interface TenantSettingsProbeAnswer {
  readonly read: SettingsReadAnswer
  readonly writes: Readonly<Record<string, TenantSettingWriteAnswer>>
  readonly deletes: Readonly<Record<string, TenantSettingWriteAnswer>>
  readonly namespaceReads: Readonly<Record<string, number>>
}

/** Read the platform listing's items out of its top-level JSON array. */
function platformSettingsItems(body: string): readonly SettingsItem[] {
  const parsed = JSON.parse(body) as unknown
  if (!Array.isArray(parsed)) return []
  return parsed.flatMap((entry) => {
    if (entry === null || typeof entry !== 'object') return []
    const item = entry as { readonly key?: unknown; readonly namespace?: unknown }
    if (typeof item.key !== 'string' || typeof item.namespace !== 'string') {
      return []
    }
    return [{ key: item.key, namespace: item.namespace }]
  })
}

/**
 * Read the tenant envelope's items out of its `settings` array.
 *
 * The tenant item carries the full key and no separate namespace: the
 * declaring module is the key's leading segment, and the surface renders the
 * key rather than splitting it. The namespace field is therefore empty here;
 * the callers that address a write take their items from the platform listing,
 * which does carry one.
 */
function tenantSettingsItems(body: string): readonly SettingsItem[] {
  const parsed = JSON.parse(body) as { readonly settings?: unknown }
  if (!Array.isArray(parsed.settings)) return []
  return parsed.settings.flatMap((entry) => {
    if (entry === null || typeof entry !== 'object') return []
    const item = entry as { readonly key?: unknown }
    if (typeof item.key !== 'string') {
      return []
    }
    return [{ key: item.key, namespace: '' }]
  })
}

/** The problem type one refusal named; empty when it named none. */
function problemTypeOf(body: string): string {
  if (body === '') return ''
  try {
    const problem = JSON.parse(body) as { readonly type?: unknown }
    return typeof problem.type === 'string' ? problem.type : ''
  } catch {
    // A refusal that is not a problem document names no type.
    return ''
  }
}

/**
 * Start the local identity provider with the same fresh-pair retry the backend
 * port uses: the provider probes for two free ports and then binds them, so a
 * process can take one in the window between. The certificates are minted into
 * the run's directory again on each attempt and overwrite the same files, so a
 * retry only re-allocates the listeners.
 */
async function startSocialIdentityProvider(
  dir: string,
): Promise<IdentityProvider> {
  let lastError: unknown
  for (let attempt = 1; attempt <= MAX_BIND_ATTEMPTS; attempt += 1) {
    try {
      return await startIdentityProvider({ dir })
    } catch (error) {
      lastError = error
      if (!looksLikePortTaken(error)) throw error
    }
  }
  throw lastError
}

/**
 * Start the enterprise journey's local identity provider with the same fresh
 * pair retry the social provider uses: a process can take one of the two ports
 * between the provider's probe and its bind, and a retry re-allocates the
 * listeners while the certificate files are overwritten in place.
 */
async function startEnterpriseProvider(
  dir: string,
): Promise<EnterpriseIdentityProvider> {
  let lastError: unknown
  for (let attempt = 1; attempt <= MAX_BIND_ATTEMPTS; attempt += 1) {
    try {
      return await startEnterpriseIdentityProvider({ dir })
    } catch (error) {
      lastError = error
      if (!looksLikePortTaken(error)) throw error
    }
  }
  throw lastError
}

/** A started server. */
export interface ReviewServer {
  /** The review binary that was built. */
  readonly binary: string
  /** The server's temporary directory. */
  readonly dir: string
  /** The browser-facing origin, http://localhost:<backend-port>. */
  readonly url: string
  /**
   * The backend listener's port: the browser origin, the API and the
   * operations probes all answer here.
   */
  readonly backendPort: number
  readonly config: ReviewConfig
  /**
   * The local OpenID Connect provider the social methods are pointed at, when
   * this server's spec opted into it; undefined otherwise.
   */
  readonly identityProvider?: IdentityProvider
  /**
   * The enterprise journey's local OpenID Connect provider, when this server's
   * spec opted into it; undefined otherwise. Its fixed user's address is on the
   * test domain the DNS stub answers for.
   */
  readonly enterpriseIdentityProvider?: EnterpriseIdentityProvider
  /**
   * The DNS stub that answers TXT queries for the one record name on the
   * enterprise journey's test domain, when this server's spec opted into it;
   * undefined otherwise. A spec publishes the record the product renders
   * through `setTXT`, the way an operator publishes it at the domain's DNS.
   */
  readonly dnsStub?: DNSStub
  /**
   * The local SAML identity provider and the attacker origin beside it, when
   * this server's spec opted into them; undefined otherwise.
   */
  readonly samlIdentityProvider?: SamlIdentityProvider
  /** Everything the child wrote to standard output, line by line. */
  readonly log: ProcessLog
  /** Everything the child wrote to standard error, joined. */
  readonly stderr: string
  /**
   * Wait for a review carrier line recording one message. The SMS line names
   * its destination, template and language; the mail line names its
   * destination and renders the subject and body, so its template and language
   * are selected together by the subject the caller passes.
   */
  waitForCarrierMessage(
    options: Parameters<typeof waitForCarrierMessage>[1],
  ): Promise<CarrierMessage>
  /** Count the carrier lines a filter names, at or after `after`. */
  countCarrierMessages(
    options: Parameters<typeof countCarrierMessages>[1],
  ): number
  /** Wait for one external-send record the delivery pipeline wrote. */
  waitForOutboundRecord(
    options: Parameters<typeof waitForOutboundRecord>[1],
  ): Promise<OutboundRecord>
  /** Count the external-send records a filter names, at or after `after`. */
  countOutboundRecords(
    options: Parameters<typeof countOutboundRecords>[1],
  ): number
  /**
   * Make one settings write through the product's own API, as the named
   * account, and answer the status and problem type.
   *
   * The settings surface renders no control for every refusal this drives: a
   * read-only tenant has no switch, a platform-enforced policy's switch is
   * disabled, and a platform-level key is not on the page at all. This is the
   * assembly layer's product request: it signs the account in through the
   * public password flow (with the authenticator step when the account holds a
   * second factor) and sends the same PUT the page would. A spec asserts the
   * rendered state first and this answer beside it, never in place of it.
   */
  writeTenantSetting(
    input: TenantSettingWriteInput,
  ): Promise<TenantSettingWriteAnswer>
  /**
   * Read the platform settings listing as the named platform administrator.
   *
   * The platform listing is the dynamic answer to "which keys exist at all";
   * the tenant-surface probe below takes this answer as its item set rather
   * than a hardcoded list, so a declaration that disappears from the build is
   * visible as a missing key.
   */
  readPlatformSettings(input: SettingsAccount): Promise<SettingsReadAnswer>
  /**
   * Probe the tenant settings surface as the named tenant administrator in one
   * session: read the full listing, PUT and DELETE each named platform item,
   * and read each named namespace.
   *
   * The tenant surface has no per-namespace read route, so the namespace read
   * is answered by the router (404 or 405) rather than by the settings module.
   * The PUT and DELETE targets are platform-level items, which have no tenant
   * access; both must answer the same bare 404 an undeclared key gets.
   */
  probeTenantSettings(
    input: TenantSettingsProbeInput,
  ): Promise<TenantSettingsProbeAnswer>
  /** Run a hook before SIGTERM; hooks run in reverse registration order. */
  beforeStop(hook: () => Promise<void> | void): void
  /** Request shutdown, wait for exit, and clean up unless kept. */
  stop(options?: { readonly keep?: boolean }): Promise<void>
}

/**
 * Start one review server: a fresh temporary directory, a fresh sqlite file
 * seeded by the binary itself, a free port, and the ops probes answered before
 * the fixture returns.
 *
 * The port is allocated after the seed command (which does not listen), so the
 * window between asking the kernel for a port and binding it is just the
 * config write. A bind failure is still possible -- another process can take
 * the port in that window -- so it is retried with a fresh port, bounded by
 * MAX_BIND_ATTEMPTS.
 *
 * `seedMember`, `seedImpersonation`, `seedInvitee`, `invitationMail`,
 * `seedSocialAccount`, `seedTenantAdmin`, `seedUnprovedTenantAdmin`, `seedSystemTenantAdmin`,
 * `seedProtectedTenant`, `platformRequireMFA`, `tenantAdmins` and
 * `recentWindow` are passed through to the configuration the binary seeds; a
 * spec that needs none leaves them off, so the template's own seed stays the
 * baseline. The tenant-admin opt-ins seed through the binary's own seed
 * command: the proved administrator, the unproved one that the write gate
 * refuses, a system tenant administrator outside the platform role, and the
 * review build's protected business tenant with its proved administrator.
 * `tenantAdmins` overrides the administrator list of a seeded tenant, so a
 * journey can pin a tenant to exactly one tenant-admin binding.
 * `platformRequireMFA` writes the admission policy into the global layer, the
 * platform floor every tenant reads as enforced.
 *
 * A spec that drives a social method turns on `socialIdentityProvider`. Only
 * then does the fixture start the local OpenID Connect provider, which mints
 * certificates with openssl and binds two more loopback ports. The provider
 * starts before the seed and lives as long as this server: the configuration
 * the binary reads names the provider's issuer and the CA file it minted, and a
 * configured CA file that cannot be read stops the start. It is part of the
 * environment the social methods run against, not a request the fixture answers
 * on the product's behalf. Every other spec runs without it and keeps the
 * template's public provider defaults.
 *
 * The enterprise journey opts in separately, with `enterpriseSSO`; the
 * tenant-SSO journey uses the same fixtures through `tenantSSO`, which also
 * seeds its tenant administrator and that tenant's own SSO requirement. Only
 * then does the fixture start the enterprise provider — the same OIDC machinery
 * with its own issuer, certificate material and one fixed user whose address is
 * on the test domain — and the UDP DNS stub beside it. The configuration the
 * binary reads names the stub as `authn.sso.dns-resolver`, the provider's CA as
 * `authn.sso.oidc-ca-file` and turns the loopback relaxation on; the stub
 * answers TXT queries for the one record name and NXDOMAINs everything else.
 * The social option is untouched by this one: a spec may turn on either, both,
 * or neither.
 */
export async function startReviewServer(
  options: {
    readonly seedMember?: boolean
    readonly seedImpersonation?: boolean
    readonly seedInvitee?: boolean
    readonly invitationMail?: boolean
    readonly seedSocialAccount?: boolean
    readonly socialIdentityProvider?: boolean
    readonly enterpriseSSO?: boolean
    readonly samlIdentityProvider?: boolean
    readonly tenantSSO?: boolean
    readonly seedTenantAdmin?: boolean
    readonly seedUnprovedTenantAdmin?: boolean
    readonly seedSystemTenantAdmin?: boolean
    readonly seedProtectedTenant?: boolean
    readonly platformRequireMFA?: boolean
    readonly tenantRequireMFA?: boolean
    readonly tenantAdmins?: Readonly<Record<string, readonly string[]>>
    readonly recentWindow?: string
    readonly mfaRequired?: boolean
    readonly schedulerTick?: string
    readonly identityQuota?: string
  } = {},
): Promise<ReviewServer> {
  const manifest = await readHarnessManifest()
  await ensureBinary(manifest.binary)
  const dir = await mkdtemp(join(tmpdir(), 'speed-e2e-server-'))
  let child: ChildProcess | undefined
  let identityProvider: IdentityProvider | undefined
  let enterpriseProvider: EnterpriseIdentityProvider | undefined
  let dnsStub: DNSStub | undefined
  let samlProvider: SamlIdentityProvider | undefined
  const log = new ProcessLog()
  const stderrLines: string[] = []
  let stopped = false

  // If the setup promise is abandoned (a Playwright timeout before `use`),
  // its finally never runs. The worker's exit hook is the last chance to
  // reap a server that is still running.
  const reapOnExit = (): void => {
    if (
      child !== undefined &&
      child.exitCode === null &&
      child.signalCode === null
    ) {
      child.kill('SIGKILL')
    }
  }
  process.once('exit', reapOnExit)

  /** Stop the current child, if any, and clear the handle. */
  const terminate = async (): Promise<void> => {
    const running = child
    child = undefined
    if (running === undefined) return
    if (await waitForExit(running, 0)) return
    running.kill('SIGTERM')
    if (!(await waitForExit(running, STOP_TIMEOUT_MS))) {
      running.kill('SIGKILL')
      await waitForExit(running, 5_000)
    }
  }

  try {
    // The provider is the environment a social spec's methods run against. A
    // spec that does not drive one leaves it off, so its server depends neither
    // on openssl nor on the provider's two extra loopback ports. When it is on
    // it starts before the seed so the configuration the binary reads already
    // names a reachable issuer and an existing CA file: a configured root CA
    // that cannot be read stops the start, so the order is load-bearing.
    identityProvider =
      options.socialIdentityProvider === true
        ? await startSocialIdentityProvider(dir)
        : undefined
    const socialProvider =
      identityProvider === undefined
        ? undefined
        : {
            issuer: identityProvider.issuer,
            rootCAFile: identityProvider.rootCAPath,
          }
    // The enterprise journey's fixtures are the environment its own seams
    // need: the provider answers on loopback, and the DNS stub is the one name
    // server a domain-ownership proof can reach. They start before the seed for
    // the same reason as the social provider: a configured CA file that cannot
    // be read stops the start, so the order is load-bearing.
    if (
      options.enterpriseSSO === true ||
      options.tenantSSO === true
    ) {
      enterpriseProvider = await startEnterpriseProvider(dir)
      dnsStub = await startDNSStub({
        recordName: enterpriseIdentityProviderRecordName,
      })
    }
    // The SAML identity provider needs neither seam: the product never calls
    // it, the browser does, and the provider row carries its certificate. It
    // starts for the spec that drives it and is stopped with the server.
    if (options.samlIdentityProvider === true) {
      samlProvider = await startSamlIdentityProvider({
        dir,
        signer: manifest.samlSigner,
      })
    }
    const ssoProvider =
      enterpriseProvider === undefined || dnsStub === undefined
        ? undefined
        : {
            dnsResolver: dnsStub.address,
            rootCAFile: enterpriseProvider.rootCAPath,
          }
    // Seed with a provisional port: the seed command does not listen, and the
    // serve attempt below writes its own configuration with the final port.
    const [seedBackend] = await freePorts(1)
    if (seedBackend === undefined) {
      throw new Error('the harness could not allocate a free port')
    }
    const seedConfig = await writeReviewConfig({
      dir,
      backendPort: seedBackend,
      seedMember: options.seedMember,
      seedImpersonation: options.seedImpersonation,
      seedInvitee: options.seedInvitee,
      invitationMail: options.invitationMail,
      seedSocialAccount: options.seedSocialAccount,
      tenantSSO: options.tenantSSO,
      seedTenantAdmin: options.seedTenantAdmin,
      seedUnprovedTenantAdmin: options.seedUnprovedTenantAdmin,
      seedSystemTenantAdmin: options.seedSystemTenantAdmin,
      seedProtectedTenant: options.seedProtectedTenant,
      platformRequireMFA: options.platformRequireMFA,
      tenantRequireMFA: options.tenantRequireMFA,
      tenantAdmins: options.tenantAdmins,
      socialProvider,
      ssoProvider,
      recentWindow: options.recentWindow,
      mfaRequired: options.mfaRequired,
      schedulerTick: options.schedulerTick,
      identityQuota: options.identityQuota,
    })
    await runCommand(
      manifest.binary,
      ['--config', `file:${seedConfig.path}`, 'sqlite', 'seed'],
      dir,
      'seeding the review database',
    )

    let config: ReviewConfig | undefined
    let attempt = 0
    for (;;) {
      attempt += 1
      const [backendPort] = await freePorts(1)
      if (backendPort === undefined) {
        throw new Error('the harness could not allocate a free port')
      }
      config = await writeReviewConfig({
        dir,
        backendPort,
        seedMember: options.seedMember,
        seedImpersonation: options.seedImpersonation,
        seedInvitee: options.seedInvitee,
        invitationMail: options.invitationMail,
        seedSocialAccount: options.seedSocialAccount,
        tenantSSO: options.tenantSSO,
        seedTenantAdmin: options.seedTenantAdmin,
        seedUnprovedTenantAdmin: options.seedUnprovedTenantAdmin,
        seedSystemTenantAdmin: options.seedSystemTenantAdmin,
        seedProtectedTenant: options.seedProtectedTenant,
        platformRequireMFA: options.platformRequireMFA,
        tenantRequireMFA: options.tenantRequireMFA,
        tenantAdmins: options.tenantAdmins,
        socialProvider,
        ssoProvider,
        recentWindow: options.recentWindow,
        mfaRequired: options.mfaRequired,
        schedulerTick: options.schedulerTick,
        identityQuota: options.identityQuota,
      })
      const configLocator = `file:${config.path}`
      const from = log.mark()
      let spawnError: Error | undefined

      const spawned = spawn(
        manifest.binary,
        ['--config', configLocator, 'sqlite', 'serve'],
        { cwd: dir, env: childEnv(), stdio: ['ignore', 'pipe', 'pipe'] },
      )
      child = spawned
      spawned.once('error', (spawnFailure) => {
        spawnError = spawnFailure
        stderrLines.push(`spawn: ${String(spawnFailure)}`)
      })
      const stdout = spawned.stdout
      const stderr = spawned.stderr
      if (stdout === null || stderr === null) {
        await terminate()
        throw new Error('the review server started without piped output')
      }
      createInterface({ input: stdout }).on('line', (line) => log.append(line))
      createInterface({ input: stderr }).on('line', (line) =>
        stderrLines.push(line),
      )

      try {
        await waitForReady(log, START_TIMEOUT_MS, from)
        await waitForProbe(`${config.origin}/health`, log)
        await waitForProbe(`${config.origin}/ready`, log)
        break
      } catch (error) {
        const exited =
          spawnError !== undefined ||
          spawned.exitCode !== null ||
          spawned.signalCode !== null
        const bindFailure = exited && looksLikeBindFailure(log, stderrLines)
        await terminate()
        if (bindFailure && attempt < MAX_BIND_ATTEMPTS) {
          // The port was taken between allocation and bind. The log keeps
          // both attempts, and `from` keeps the ready wait from matching a
          // record written by the failed attempt.
          continue
        }
        if (spawnError !== undefined) {
          throw new Error(
            `the review server could not be started: ${String(spawnError)}` +
              `\n--- process log (last lines) ---\n${log.tail()}` +
              `\n--- standard error ---\n${stderrLines.join('\n')}`,
          )
        }
        if (exited) {
          throw new Error(
            `the review server exited before it was ready ` +
              `(code ${String(spawned.exitCode)}, signal ${String(spawned.signalCode)})` +
              `\n--- process log (last lines) ---\n${log.tail()}` +
              `\n--- standard error ---\n${stderrLines.join('\n')}`,
          )
        }
        throw error
      }
    }

    if (config === undefined) {
      throw new Error('the review server finished its attempts without a configuration')
    }
    const reviewConfig = config

    const hooks: Array<() => Promise<void> | void> = []
    const stop = async (options: { readonly keep?: boolean } = {}): Promise<void> => {
      if (stopped) return
      stopped = true
      process.removeListener('exit', reapOnExit)
      while (hooks.length > 0) {
        const hook = hooks.pop()
        if (hook === undefined) continue
        try {
          await hook()
        } catch {
          // A hook is a courtesy (closing the browser context); its failure
          // must not leave the process running.
        }
      }
      await terminate()
      await identityProvider?.stop()
      await enterpriseProvider?.stop()
      await dnsStub?.stop()
      await samlProvider?.stop()
      if (options.keep !== true) {
        await rm(dir, { recursive: true, force: true })
      }
    }

    return {
      binary: manifest.binary,
      dir,
      url: reviewConfig.origin,
      backendPort: reviewConfig.backendPort,
      config: reviewConfig,
      identityProvider,
      enterpriseIdentityProvider: enterpriseProvider,
      dnsStub,
      samlIdentityProvider: samlProvider,
      log,
      get stderr(): string {
        return stderrLines.join('\n')
      },
      waitForCarrierMessage(options) {
        return waitForCarrierMessage(log, options)
      },
      countCarrierMessages(options) {
        return countCarrierMessages(log, options)
      },
      waitForOutboundRecord(options) {
        return waitForOutboundRecord(log, options)
      },
      countOutboundRecords(options) {
        return countOutboundRecords(log, options)
      },
      async writeTenantSetting(
        input: TenantSettingWriteInput,
      ): Promise<TenantSettingWriteAnswer> {
        const token = await signInThroughApi(
          reviewConfig.origin,
          input.identifier,
          input.password,
          input.totpSecret,
        )
        const answer = await callApi(
          `${reviewConfig.origin}/api/v1/tenants/${input.tenant}/settings/` +
            `${input.namespace}/${input.key}`,
          {
            method: 'PUT',
            headers: {
              'Content-Type': 'application/json',
              Authorization: `Bearer ${token}`,
              'X-Tenant': input.tenant,
            },
            body: JSON.stringify({ value: input.value }),
          },
        )
        return {
          status: answer.status,
          problemType:
            answer.status >= 400 ? problemTypeOf(answer.body) : '',
        }
      },
      async readPlatformSettings(
        input: SettingsAccount,
      ): Promise<SettingsReadAnswer> {
        const token = await signInThroughApi(
          reviewConfig.origin,
          input.identifier,
          input.password,
        )
        const answer = await callApi(
          `${reviewConfig.origin}/api/v1/system/settings?layer=global`,
          {
            method: 'GET',
            headers: { Authorization: `Bearer ${token}` },
          },
        )
        return {
          status: answer.status,
          items: answer.status === 200 ? platformSettingsItems(answer.body) : [],
        }
      },
      async probeTenantSettings(
        input: TenantSettingsProbeInput,
      ): Promise<TenantSettingsProbeAnswer> {
        const token = await signInThroughApi(
          reviewConfig.origin,
          input.identifier,
          input.password,
        )
        const headers = {
          Authorization: `Bearer ${token}`,
          'X-Tenant': input.tenant,
        }
        const base =
          `${reviewConfig.origin}/api/v1/tenants/` +
          `${encodeURIComponent(input.tenant)}/settings`
        const read = await callApi(base, { method: 'GET', headers })
        const writes: Record<string, TenantSettingWriteAnswer> = {}
        const deletes: Record<string, TenantSettingWriteAnswer> = {}
        for (const item of input.items) {
          const url =
            `${base}/${encodeURIComponent(item.namespace)}/` +
            encodeURIComponent(item.key)
          const write = await callApi(url, {
            method: 'PUT',
            headers: { ...headers, 'Content-Type': 'application/json' },
            body: JSON.stringify({ value: true }),
          })
          writes[item.key] = {
            status: write.status,
            problemType:
              write.status >= 400 ? problemTypeOf(write.body) : '',
          }
          const removed = await callApi(url, { method: 'DELETE', headers })
          deletes[item.key] = {
            status: removed.status,
            problemType:
              removed.status >= 400 ? problemTypeOf(removed.body) : '',
          }
        }
        const namespaceReads: Record<string, number> = {}
        for (const namespace of input.namespaces) {
          const namespaceRead = await callApi(
            `${base}/${encodeURIComponent(namespace)}`,
            { method: 'GET', headers },
          )
          namespaceReads[namespace] = namespaceRead.status
        }
        return {
          read: {
            status: read.status,
            items: read.status === 200 ? tenantSettingsItems(read.body) : [],
          },
          writes,
          deletes,
          namespaceReads,
        }
      },
      beforeStop(hook: () => Promise<void> | void): void {
        hooks.push(hook)
      },
      stop,
    }
  } catch (error) {
    process.removeListener('exit', reapOnExit)
    await terminate()
    await identityProvider?.stop()
    await enterpriseProvider?.stop()
    await dnsStub?.stop()
    await samlProvider?.stop()
    await rm(dir, { recursive: true, force: true })
    throw error
  }
}
