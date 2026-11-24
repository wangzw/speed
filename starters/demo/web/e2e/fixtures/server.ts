import { execFile, spawn, type ChildProcess } from 'node:child_process'
import { mkdtemp, readFile, rm, stat } from 'node:fs/promises'
import { request as httpRequest } from 'node:http'
import { createInterface } from 'node:readline'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

import { totpCode } from '../helpers/totp'
import {
  e2eImpersonationFactors,
  e2eInvitee,
  e2eTenantAdmin,
  writeServerConfig,
  type ServerConfig,
} from './config'
import { startDNSStub, type DNSStub } from './dns-stub'
import { readSeededFactors } from './factor-seed'
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
  waitForVerificationCode,
  type CarrierMessage,
  type OutboundRecord,
} from './log'
import {
  freePorts,
  shouldRetryStart,
  waitForProbe,
} from './loopback'
import { MANIFEST_PATH } from './paths'
import {
  startSamlIdentityProvider,
  type SamlIdentityProvider,
} from './saml-identity-provider'
import { scaledTimeout } from './time-scale'


const SEED_TIMEOUT_MS = scaledTimeout(30_000)

const START_TIMEOUT_MS = scaledTimeout(30_000)

const STOP_TIMEOUT_MS = scaledTimeout(25_000)

const API_REQUEST_TIMEOUT_MS = scaledTimeout(10_000)

const MAX_BIND_ATTEMPTS = 3

interface HarnessManifest {
  readonly binary: string
  readonly factorSeed: string
  readonly samlSigner: string
  readonly repoRoot: string
  readonly serverDir: string
  readonly webDir: string
  readonly builtAt: string
}

export async function readHarnessManifest(): Promise<HarnessManifest> {
  let raw: string
  try {
    raw = await readFile(MANIFEST_PATH, 'utf8')
  } catch (error) {
    throw new Error(
      `the harness manifest ${MANIFEST_PATH} could not be read; the Playwright ` +
        `global setup builds the front end and the e2e binary and writes it. ` +
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

function childEnv(): NodeJS.ProcessEnv {
  const env: NodeJS.ProcessEnv = { ...process.env }
  for (const key of Object.keys(env)) {
    if (key.startsWith('DEMO_') || key.startsWith('OTEL_')) delete env[key]
  }
  return env
}

function runCommand(
  binary: string,
  args: readonly string[],
  cwd: string,
  what: string,
): Promise<{ readonly stdout: string; readonly stderr: string }> {
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
          resolve({ stdout, stderr })
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

async function ensureBinary(binary: string): Promise<void> {
  try {
    const info = await stat(binary)
    if (!info.isFile()) {
      throw new Error('not a regular file')
    }
  } catch (error) {
    throw new Error(
      `the harness manifest names the e2e binary ${binary}, but it cannot ` +
        `be used (${String(error)}). The binary is built into /tmp by the ` +
        `Playwright global setup; a stale manifest is rebuilt by rerunning ` +
        `the tier through \`make e2e\`, or with GATE_JOBS set, \`pnpm -C web ` +
        `--filter demo-web exec playwright test --config e2e/playwright.config.ts\`.`,
    )
  }
}

function looksLikeBindFailure(log: ProcessLog, stderr: readonly string[]): boolean {
  const text = `${log.tail(200)}\n${stderr.join('\n')}`
  return /EADDRINUSE|address already in use|cannot assign requested address|bind:/i.test(
    text,
  )
}

function looksLikePortTaken(error: unknown): boolean {
  return /EADDRINUSE|address already in use/i.test(String(error))
}

interface ApiAnswer {
  readonly status: number
  readonly body: string
  readonly setCookie: readonly string[]
}

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

function cookieHeader(setCookie: readonly string[]): string {
  return setCookie
    .map((entry) => entry.split(';', 1)[0] ?? '')
    .filter((entry) => entry !== '')
    .join('; ')
}

interface FlowReply {
  readonly seq: number
  readonly nextKind: string
  readonly factors: readonly { readonly id: string; readonly kind: string }[]
  readonly accessToken: string
}

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

const TOTP_STEP_MS = 30_000

// An account with a second factor parks the flow there. The code is asked for one step ahead: the authenticator refuses a counter it already accepted, and a browser session of the same account may have used the current step.
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

async function proveEmailThroughProduct(
  origin: string,
  account: {
    readonly username: string
    readonly password: string
    readonly identifier: string
  },
  log: ProcessLog,
): Promise<void> {
  const token = await signInThroughApi(origin, account.username, account.password)
  const headers = {
    'Content-Type': 'application/json',
    Authorization: `Bearer ${token}`,
  }
  const after = log.mark()
  const asked = await callApi(`${origin}/api/v1/auth/identifiers`, {
    method: 'POST',
    headers,
    body: JSON.stringify({ kind: 'email', value: account.identifier }),
  })
  if (asked.status !== 200) {
    throw new Error(
      `asking for a code to prove ${account.identifier} answered ${asked.status}:\n${asked.body}`,
    )
  }
  const { code } = await waitForVerificationCode(
    log,
    'mail',
    account.identifier,
    { after },
  )
  const verified = await callApi(`${origin}/api/v1/auth/identifiers/verify`, {
    method: 'POST',
    headers,
    body: JSON.stringify({ code }),
  })
  if (verified.status !== 200) {
    throw new Error(
      `proving ${account.identifier} answered ${verified.status}:\n${verified.body}`,
    )
  }
}

export interface TenantSettingWriteInput {
  readonly tenant: string
  readonly identifier: string
  readonly password: string
  readonly totpSecret?: string
  readonly namespace: string
  readonly key: string
  readonly value: string
}

export interface TenantSettingWriteAnswer {
  readonly status: number
  readonly problemType: string
}

export interface SettingsItem {
  readonly key: string
  readonly namespace: string
}

export interface SettingsReadAnswer {
  readonly status: number
  readonly items: readonly SettingsItem[]
}

export interface SettingsAccount {
  readonly identifier: string
  readonly password: string
}

export interface TenantSettingsProbeInput extends SettingsAccount {
  readonly tenant: string
  readonly items: readonly SettingsItem[]
  readonly namespaces: readonly string[]
}

export interface TenantSettingsProbeAnswer {
  readonly read: SettingsReadAnswer
  readonly writes: Readonly<Record<string, TenantSettingWriteAnswer>>
  readonly deletes: Readonly<Record<string, TenantSettingWriteAnswer>>
  readonly namespaceReads: Readonly<Record<string, number>>
}

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

// The tenant item carries the full key and no separate namespace; callers that address a write take their items from the platform listing, which carries one.
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

function problemTypeOf(body: string): string {
  if (body === '') return ''
  try {
    const problem = JSON.parse(body) as { readonly type?: unknown }
    return typeof problem.type === 'string' ? problem.type : ''
  } catch {
    return ''
  }
}

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

export interface E2EServer {
  readonly binary: string
  readonly dir: string
  readonly url: string
  readonly backendPort: number
  readonly config: ServerConfig
  readonly identityProvider?: IdentityProvider
  readonly enterpriseIdentityProvider?: EnterpriseIdentityProvider
  readonly dnsStub?: DNSStub
  readonly samlIdentityProvider?: SamlIdentityProvider
  readonly log: ProcessLog
  readonly stderr: string
  waitForCarrierMessage(
    options: Parameters<typeof waitForCarrierMessage>[1],
  ): Promise<CarrierMessage>
  countCarrierMessages(
    options: Parameters<typeof countCarrierMessages>[1],
  ): number
  waitForOutboundRecord(
    options: Parameters<typeof waitForOutboundRecord>[1],
  ): Promise<OutboundRecord>
  countOutboundRecords(
    options: Parameters<typeof countOutboundRecords>[1],
  ): number
  writeTenantSetting(
    input: TenantSettingWriteInput,
  ): Promise<TenantSettingWriteAnswer>
  readPlatformSettings(input: SettingsAccount): Promise<SettingsReadAnswer>
  probeTenantSettings(
    input: TenantSettingsProbeInput,
  ): Promise<TenantSettingsProbeAnswer>
  beforeStop(hook: () => Promise<void> | void): void
  stop(options?: { readonly keep?: boolean }): Promise<void>
}

export async function startE2EServer(
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
    readonly platformRequireMFA?: boolean
    readonly tenantRequireMFA?: boolean
    readonly tenantAdmins?: Readonly<Record<string, readonly string[]>>
    readonly recentWindow?: string
    readonly mfaRequired?: boolean
    readonly identityQuota?: string
  } = {},
): Promise<E2EServer> {
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

  // An abandoned setup promise never runs its finally; the worker's exit hook is the last chance to reap the child.
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
    // Start before the seed: a configured CA file that cannot be read stops the start, so the order is load-bearing.
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
    // These start before the seed for the same reason: a configured CA file must be readable when the server starts.
    if (
      options.enterpriseSSO === true ||
      options.tenantSSO === true
    ) {
      enterpriseProvider = await startEnterpriseProvider(dir)
      dnsStub = await startDNSStub({
        recordName: enterpriseIdentityProviderRecordName,
      })
    }
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
    // The seed does not listen; the serve attempt below renders its own configuration with the final port.
    const [seedBackend] = await freePorts(1)
    if (seedBackend === undefined) {
      throw new Error('the harness could not allocate a free port')
    }
    const seedConfig = await writeServerConfig({
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
      platformRequireMFA: options.platformRequireMFA,
      tenantRequireMFA: options.tenantRequireMFA,
      tenantAdmins: options.tenantAdmins,
      socialProvider,
      ssoProvider,
      recentWindow: options.recentWindow,
      mfaRequired: options.mfaRequired,
      identityQuota: options.identityQuota,
    })
    await runCommand(
      manifest.binary,
      ['--config', `file:${seedConfig.path}`, 'sqlite', 'seed'],
      dir,
      'seeding the e2e database',
    )
    if (options.seedImpersonation === true) {
      const what = 'writing the impersonation operator factors'
      const seeded = await runCommand(
        manifest.factorSeed,
        [
          '--config',
          `file:${seedConfig.path}`,
          '--account',
          e2eImpersonationFactors.account,
          '--totp-secret',
          e2eImpersonationFactors.totpSeed,
          '--totp-age',
          e2eImpersonationFactors.totpAge,
          '--sms-age',
          e2eImpersonationFactors.smsAge,
        ],
        dir,
        what,
      )
      // A zero exit without both identifiers is a half-seeded operator, not a success.
      readSeededFactors(seeded.stdout, seeded.stderr, what)
    }

    let config: ServerConfig | undefined
    let attempt = 0
    for (;;) {
      attempt += 1
      const [backendPort] = await freePorts(1)
      if (backendPort === undefined) {
        throw new Error('the harness could not allocate a free port')
      }
      config = await writeServerConfig({
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
        platformRequireMFA: options.platformRequireMFA,
        tenantRequireMFA: options.tenantRequireMFA,
        tenantAdmins: options.tenantAdmins,
        socialProvider,
        ssoProvider,
        recentWindow: options.recentWindow,
        mfaRequired: options.mfaRequired,
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
        throw new Error('the e2e server started without piped output')
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
        // A connection accepted on ::1 that does not answer like this server means another process holds the port; retry with fresh ports even though the child is running.
        await terminate()
        if (shouldRetryStart(error, bindFailure, attempt, MAX_BIND_ATTEMPTS)) {
          // The log keeps both attempts; `from` keeps the ready wait from matching the failed attempt's record.
          continue
        }
        if (spawnError !== undefined) {
          throw new Error(
            `the e2e server could not be started: ${String(spawnError)}` +
              `\n--- process log (last lines) ---\n${log.tail()}` +
              `\n--- standard error ---\n${stderrLines.join('\n')}`,
          )
        }
        if (exited) {
          throw new Error(
            `the e2e server exited before it was ready ` +
              `(code ${String(spawned.exitCode)}, signal ${String(spawned.signalCode)})` +
              `\n--- process log (last lines) ---\n${log.tail()}` +
              `\n--- standard error ---\n${stderrLines.join('\n')}`,
          )
        }
        throw error
      }
    }

    if (config === undefined) {
      throw new Error('the e2e server finished its attempts without a configuration')
    }
    const serverConfig = config

    if (options.seedTenantAdmin === true) {
      await proveEmailThroughProduct(serverConfig.origin, e2eTenantAdmin, log)
    }
    if (options.seedInvitee === true) {
      await proveEmailThroughProduct(serverConfig.origin, e2eInvitee, log)
    }

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
          // A hook is a courtesy; its failure must not leave the process running.
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
      url: serverConfig.origin,
      backendPort: serverConfig.backendPort,
      config: serverConfig,
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
          serverConfig.origin,
          input.identifier,
          input.password,
          input.totpSecret,
        )
        const answer = await callApi(
          `${serverConfig.origin}/api/v1/tenants/${input.tenant}/settings/` +
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
          serverConfig.origin,
          input.identifier,
          input.password,
        )
        const answer = await callApi(
          `${serverConfig.origin}/api/v1/system/settings?layer=global`,
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
          serverConfig.origin,
          input.identifier,
          input.password,
        )
        const headers = {
          Authorization: `Bearer ${token}`,
          'X-Tenant': input.tenant,
        }
        const base =
          `${serverConfig.origin}/api/v1/tenants/` +
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
