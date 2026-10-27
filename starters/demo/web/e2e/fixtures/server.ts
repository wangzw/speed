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
import { get as httpGet } from 'node:http'
import { createServer } from 'node:net'
import { createInterface } from 'node:readline'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

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

// Every bound below is strictly inside the Playwright test timeout (120s in
// playwright.config.ts), with room left for the journey and for teardown. A
// fixture that used the whole test bound would be killed by Playwright first,
// and the harness's own diagnostics -- the log tail and standard error --
// would never be printed.

/** How long the seed command may run. */
const SEED_TIMEOUT_MS = 20_000

/** How long serve may take to write its ready record. */
const START_TIMEOUT_MS = 20_000

/** How long SIGTERM may take before SIGKILL follows. */
const STOP_TIMEOUT_MS = 15_000

/** How long the readiness probes may take to answer after the ready record. */
const PROBE_TIMEOUT_MS = 10_000

/** How many times a bind failure is retried with fresh ports. */
const MAX_BIND_ATTEMPTS = 3

interface HarnessManifest {
  readonly binary: string
  /** The deploy-tagged binary the deploy fixture starts. */
  readonly deployBinary: string
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
        `Run the tier through \`pnpm -C starters/demo/web exec playwright test ` +
        `--config e2e/playwright.config.ts\`: ${String(error)}`,
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
    probeRequest.setTimeout(5_000, () => {
      probeRequest.destroy(new Error(`${url} did not answer within 5s`))
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
        `the tier through \`pnpm -C starters/demo/web exec playwright test ` +
        `--config e2e/playwright.config.ts\`.`,
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
  /** The operations listener's origin. */
  readonly opsUrl: string
  readonly backendPort: number
  readonly opsPort: number
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
  /** Everything the child wrote to standard output, line by line. */
  readonly log: ProcessLog
  /** Everything the child wrote to standard error, joined. */
  readonly stderr: string
  /**
   * Wait for the review SMS carrier line recording one message. The carrier
   * line is the message itself, with its destination, template and language.
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
  /** Run a hook before SIGTERM; hooks run in reverse registration order. */
  beforeStop(hook: () => Promise<void> | void): void
  /** Request shutdown, wait for exit, and clean up unless kept. */
  stop(options?: { readonly keep?: boolean }): Promise<void>
}

/**
 * Start one review server: a fresh temporary directory, a fresh sqlite file
 * seeded by the binary itself, free ports, and the ops probes answered before
 * the fixture returns.
 *
 * Ports are allocated after the seed command (which does not listen), so the
 * window between asking the kernel for a port and binding it is just the
 * config write. A bind failure is still possible -- another process can take
 * the port in that window -- so it is retried with a fresh pair, bounded by
 * MAX_BIND_ATTEMPTS.
 *
 * `seedMember`, `seedImpersonation`, `seedInvitee`, `seedSocialAccount` and
 * `recentWindow` are passed through to the configuration the binary seeds; a
 * spec that needs none leaves them off, so the template's own seed stays the
 * baseline.
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
    readonly seedSocialAccount?: boolean
    readonly socialIdentityProvider?: boolean
    readonly enterpriseSSO?: boolean
    readonly tenantSSO?: boolean
    readonly recentWindow?: string
    readonly mfaRequired?: boolean
    readonly schedulerTick?: string
  } = {},
): Promise<ReviewServer> {
  const manifest = await readHarnessManifest()
  await ensureBinary(manifest.binary)
  const dir = await mkdtemp(join(tmpdir(), 'speed-e2e-server-'))
  let child: ChildProcess | undefined
  let identityProvider: IdentityProvider | undefined
  let enterpriseProvider: EnterpriseIdentityProvider | undefined
  let dnsStub: DNSStub | undefined
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
    const ssoProvider =
      enterpriseProvider === undefined || dnsStub === undefined
        ? undefined
        : {
            dnsResolver: dnsStub.address,
            rootCAFile: enterpriseProvider.rootCAPath,
          }
    // Seed with a provisional pair: the seed command does not listen, and the
    // serve attempt below writes its own configuration with the final ports.
    const [seedBackend, seedOps] = await freePorts(2)
    if (seedBackend === undefined || seedOps === undefined) {
      throw new Error('the harness could not allocate two free ports')
    }
    const seedConfig = await writeReviewConfig({
      dir,
      backendPort: seedBackend,
      opsPort: seedOps,
      seedMember: options.seedMember,
      seedImpersonation: options.seedImpersonation,
      seedInvitee: options.seedInvitee,
      seedSocialAccount: options.seedSocialAccount,
      tenantSSO: options.tenantSSO,
      socialProvider,
      ssoProvider,
      recentWindow: options.recentWindow,
      mfaRequired: options.mfaRequired,
      schedulerTick: options.schedulerTick,
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
      const [backendPort, opsPort] = await freePorts(2)
      if (backendPort === undefined || opsPort === undefined) {
        throw new Error('the harness could not allocate two free ports')
      }
      config = await writeReviewConfig({
        dir,
        backendPort,
        opsPort,
        seedMember: options.seedMember,
        seedImpersonation: options.seedImpersonation,
        seedInvitee: options.seedInvitee,
        seedSocialAccount: options.seedSocialAccount,
        tenantSSO: options.tenantSSO,
        socialProvider,
        ssoProvider,
        recentWindow: options.recentWindow,
        mfaRequired: options.mfaRequired,
        schedulerTick: options.schedulerTick,
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
        await waitForProbe(`${config.opsOrigin}/health`, log)
        await waitForProbe(`${config.opsOrigin}/ready`, log)
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
      if (options.keep !== true) {
        await rm(dir, { recursive: true, force: true })
      }
    }

    return {
      binary: manifest.binary,
      dir,
      url: reviewConfig.origin,
      opsUrl: reviewConfig.opsOrigin,
      backendPort: reviewConfig.backendPort,
      opsPort: reviewConfig.opsPort,
      config: reviewConfig,
      identityProvider,
      enterpriseIdentityProvider: enterpriseProvider,
      dnsStub,
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
    await rm(dir, { recursive: true, force: true })
    throw error
  }
}
