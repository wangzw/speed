import { execFile, spawn, type ChildProcess } from 'node:child_process'
import { mkdir, mkdtemp, rm, stat } from 'node:fs/promises'
import { get as httpGet } from 'node:http'
import { createInterface } from 'node:readline'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

import { writeDeployConfig, type DeployConfig } from './config'
import {
  freePorts,
  shouldRetryStart,
  waitForProbe,
} from './loopback'
import { ProcessLog, waitForReady } from './log'
import { readHarnessManifest } from './server'
import { scaledTimeout } from './time-scale'


const SEED_TIMEOUT_MS = scaledTimeout(30_000)

const START_TIMEOUT_MS = scaledTimeout(30_000)

const STOP_TIMEOUT_MS = scaledTimeout(25_000)

const PROBE_REQUEST_TIMEOUT_MS = scaledTimeout(5_000)

const MAX_BIND_ATTEMPTS = 3

export interface HttpAnswer {
  readonly status: number
  readonly body: string
  readonly contentType: string
}

export interface DeployServer {
  readonly binary: string
  readonly dir: string
  readonly volume: string
  readonly url: string
  readonly opsUrl: string
  readonly backendPort: number
  readonly opsPort: number
  readonly config: DeployConfig
  readonly seedOutput: string
  readonly signingKeyMode: number
  readonly log: ProcessLog
  readonly stderr: string
  probe(path: string): Promise<HttpAnswer>
  probeOps(path: string): Promise<HttpAnswer>
  beforeStop(hook: () => Promise<void> | void): void
  stop(options?: { readonly keep?: boolean }): Promise<void>
}

function childEnv(): NodeJS.ProcessEnv {
  const env: NodeJS.ProcessEnv = { ...process.env }
  for (const key of Object.keys(env)) {
    if (key.startsWith('DEMO_') || key.startsWith('OTEL_')) delete env[key]
  }
  return env
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

function runSeed(
  binary: string,
  config: DeployConfig,
  volume: string,
): Promise<string> {
  return new Promise((resolve, reject) => {
    execFile(
      binary,
      ['--config', `file://${config.path}`, 'sqlite', 'seed'],
      {
        cwd: volume,
        env: childEnv(),
        timeout: SEED_TIMEOUT_MS,
        maxBuffer: 16 * 1024 * 1024,
      },
      (error, stdout, stderr) => {
        const output = `${stdout}${stderr}`
        if (error === null) {
          resolve(output)
          return
        }
        reject(
          new Error(
            `seeding the deployment volume failed: ${error.message}\n` +
              `standard output:\n${stdout}\nstandard error:\n${stderr}`,
          ),
        )
      },
    )
  })
}

function fetchAnswer(url: string): Promise<HttpAnswer> {
  return new Promise((resolve, reject) => {
    const outgoing = httpGet(url, (response) => {
      const chunks: Buffer[] = []
      response.on('data', (chunk: Buffer) => chunks.push(chunk))
      response.on('end', () => {
        resolve({
          status: response.statusCode ?? 0,
          body: Buffer.concat(chunks).toString('utf8'),
          contentType: String(response.headers['content-type'] ?? ''),
        })
      })
      response.on('error', reject)
    })
    outgoing.on('error', reject)
    outgoing.setTimeout(PROBE_REQUEST_TIMEOUT_MS, () => {
      outgoing.destroy(
        new Error(`${url} did not answer within ${PROBE_REQUEST_TIMEOUT_MS}ms`),
      )
    })
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

function looksLikeBindFailure(
  log: ProcessLog,
  stderr: readonly string[],
): boolean {
  const text = `${log.tail(200)}\n${stderr.join('\n')}`
  return /EADDRINUSE|address already in use|cannot assign requested address|bind:/i.test(
    text,
  )
}

export async function startDeployServer(): Promise<DeployServer> {
  const manifest = await readHarnessManifest()
  const binary = manifest.binary
  if (typeof binary !== 'string' || binary === '') {
    throw new Error(
      'the harness manifest names no binary; the Playwright global setup ' +
        'builds the e2e binary and records it there',
    )
  }
  await ensureBinary(binary)

  const dir = await mkdtemp(join(tmpdir(), 'speed-e2e-deploy-'))
  const volume = join(dir, 'data')
  await mkdir(join(volume, 'var'), { recursive: true })
  await mkdir(join(volume, 'objects'), { recursive: true })

  const log = new ProcessLog()
  const stderrLines: string[] = []
  let child: ChildProcess | undefined
  let stopped = false

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
    // The seed does not listen; the serve attempt below renders its own configuration with the final ports.
    const [seedBackend, seedOps] = await freePorts(2)
    if (seedBackend === undefined || seedOps === undefined) {
      throw new Error('the harness could not allocate two free ports')
    }
    const seedConfig = await writeDeployConfig({
      volume,
      backendPort: seedBackend,
      opsPort: seedOps,
    })
    const seedOutput = await runSeed(binary, seedConfig, volume)

    // The first run mints the signing key before the command tree runs; its permission is part of the deployment contract.
    const keyInfo = await stat(seedConfig.signingKey)
    const signingKeyMode = keyInfo.mode & 0o777

    let config: DeployConfig | undefined
    let attempt = 0
    for (;;) {
      attempt += 1
      const [backendPort, opsPort] = await freePorts(2)
      if (backendPort === undefined || opsPort === undefined) {
        throw new Error('the harness could not allocate two free ports')
      }
      config = await writeDeployConfig({ volume, backendPort, opsPort })
      const from = log.mark()
      let spawnError: Error | undefined

      const spawned = spawn(
        binary,
        ['--config', `file://${config.path}`, 'sqlite', 'serve'],
        { cwd: volume, env: childEnv(), stdio: ['ignore', 'pipe', 'pipe'] },
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
        throw new Error('the deployment server started without piped output')
      }
      createInterface({ input: stdout }).on('line', (line) => log.append(line))
      createInterface({ input: stderr }).on('line', (line) =>
        stderrLines.push(line),
      )

      try {
        await waitForReady(log, START_TIMEOUT_MS, from)
        // The browser reaches the backend listener, so its /health is a reachability check; the operations surface is on its own listener.
        await waitForProbe(`${config.origin}/health`, log)
        await waitForProbe(`${config.opsOrigin}/health`, log)
        await waitForProbe(`${config.opsOrigin}/ready`, log)
        break
      } catch (error) {
        const exited =
          spawnError !== undefined ||
          spawned.exitCode !== null ||
          spawned.signalCode !== null
        const bindFailure = exited && looksLikeBindFailure(log, stderrLines)
        // A connection accepted on ::1 that does not answer like this server means another process holds the port; retry with fresh ports.
        await terminate()
        if (shouldRetryStart(error, bindFailure, attempt, MAX_BIND_ATTEMPTS)) {
          continue
        }
        if (spawnError !== undefined) {
          throw new Error(
            `the deployment server could not be started: ${String(spawnError)}` +
              `\n--- process log (last lines) ---\n${log.tail()}` +
              `\n--- standard error ---\n${stderrLines.join('\n')}`,
          )
        }
        if (exited) {
          throw new Error(
            `the deployment server exited before it was ready ` +
              `(code ${String(spawned.exitCode)}, signal ${String(spawned.signalCode)})` +
              `\n--- process log (last lines) ---\n${log.tail()}` +
              `\n--- standard error ---\n${stderrLines.join('\n')}`,
          )
        }
        throw error
      }
    }

    if (config === undefined) {
      throw new Error(
        'the deployment server finished its attempts without a configuration',
      )
    }
    const deployConfig = config

    const hooks: Array<() => Promise<void> | void> = []
    const stop = async (
      options: { readonly keep?: boolean } = {},
    ): Promise<void> => {
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
      if (options.keep !== true) {
        await rm(dir, { recursive: true, force: true })
      }
    }

    return {
      binary,
      dir,
      volume,
      url: deployConfig.origin,
      opsUrl: deployConfig.opsOrigin,
      backendPort: deployConfig.backendPort,
      opsPort: deployConfig.opsPort,
      config: deployConfig,
      seedOutput,
      signingKeyMode,
      log,
      get stderr(): string {
        return stderrLines.join('\n')
      },
      probe(path: string): Promise<HttpAnswer> {
        return fetchAnswer(`${deployConfig.origin}${path}`)
      },
      probeOps(path: string): Promise<HttpAnswer> {
        return fetchAnswer(`${deployConfig.opsOrigin}${path}`)
      },
      beforeStop(hook: () => Promise<void> | void): void {
        hooks.push(hook)
      },
      stop,
    }
  } catch (error) {
    process.removeListener('exit', reapOnExit)
    await terminate()
    await rm(dir, { recursive: true, force: true })
    throw error
  }
}
