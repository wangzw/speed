import { get as httpGet } from 'node:http'
import { createServer } from 'node:net'

import type { ProcessLog } from './log'
import { scaledTimeout } from './time-scale'

const PROBE_TIMEOUT_MS = scaledTimeout(15_000)

const PROBE_REQUEST_TIMEOUT_MS = scaledTimeout(5_000)

const PROBE_RETRY_DELAY_MS = scaledTimeout(100)

// Bound keeps a machine with a crowded ::1 from spinning; the rejected ports are listed in the failure.
const MAX_PORT_CANDIDATES = 64

export type LoopbackAddress = '127.0.0.1' | '::1'

export interface LoopbackListener {
  readonly address: LoopbackAddress
  readonly port: number
  close(): Promise<void>
}

export interface LoopbackSockets {
  bind(address: LoopbackAddress, port: number): Promise<LoopbackListener>
  request(
    address: LoopbackAddress,
    port: number,
    path: string,
    timeoutMs: number,
  ): Promise<number>
}

function errorCode(error: unknown): string | undefined {
  if (typeof error !== 'object' || error === null) return undefined
  const code = (error as { readonly code?: unknown }).code
  return typeof code === 'string' ? code : undefined
}

function nodeBind(
  address: LoopbackAddress,
  port: number,
): Promise<LoopbackListener> {
  return new Promise((resolve, reject) => {
    const server = createServer()
    server.once('error', reject)
    server.listen(
      {
        port,
        host: address,
        ipv6Only: address === '::1' ? true : undefined,
      },
      () => {
        const bound = server.address()
        if (bound === null || typeof bound === 'string') {
          server.close()
          reject(
            new Error(
              `a loopback listener on ${address} answered without a TCP address`,
            ),
          )
          return
        }
        resolve({
          address,
          port: bound.port,
          close: () =>
            new Promise<void>((done) => {
              if (!server.listening) {
                done()
                return
              }
              server.close(() => done())
            }),
        })
      },
    )
  })
}

function nodeRequest(
  address: LoopbackAddress,
  port: number,
  path: string,
  timeoutMs: number,
): Promise<number> {
  const host = address === '::1' ? '[::1]' : address
  const url = `http://${host}:${port}${path}`
  return new Promise((resolve, reject) => {
    const outgoing = httpGet(url, (response) => {
      response.resume()
      resolve(response.statusCode ?? 0)
    })
    outgoing.on('error', reject)
    outgoing.setTimeout(timeoutMs, () => {
      outgoing.destroy(
        new Error(`${url} did not answer within ${timeoutMs}ms`),
      )
    })
  })
}

export const nodeLoopbackSockets: LoopbackSockets = {
  bind: nodeBind,
  request: nodeRequest,
}

export interface PortAllocationOptions {
  readonly sockets?: LoopbackSockets
  readonly nextCandidate?: () => Promise<LoopbackListener>
  readonly requireIPv6?: boolean
  readonly maxCandidates?: number
}

// Each candidate is held on 127.0.0.1 while ::1 is tried, and every accepted listener is kept until the batch is chosen, so a selected port is never handed back to the kernel for the next candidate.
export async function freePorts(
  count: number,
  options: PortAllocationOptions = {},
): Promise<number[]> {
  const sockets = options.sockets ?? nodeLoopbackSockets
  const requireIPv6 = options.requireIPv6 ?? true
  const maxCandidates = options.maxCandidates ?? MAX_PORT_CANDIDATES
  const nextCandidate =
    options.nextCandidate ?? (() => sockets.bind('127.0.0.1', 0))

  const selected: number[] = []
  const kept: LoopbackListener[] = []
  const rejected: Array<{ readonly port: number; readonly reason: string }> = []

  try {
    let candidates = 0
    while (selected.length < count && candidates < maxCandidates) {
      candidates += 1
      const ipv4 = await nextCandidate()
      let ipv6: LoopbackListener | undefined
      if (requireIPv6) {
        try {
          ipv6 = await sockets.bind('::1', ipv4.port)
        } catch (error) {
          const code = errorCode(error)
          if (code === 'EADDRINUSE') {
            // Another process holds this port on ::1 even though 127.0.0.1 is free; drop the candidate and close its listener so the port is not leaked.
            rejected.push({ port: ipv4.port, reason: code })
            await ipv4.close()
            continue
          }
          if (code !== undefined && IPV6_ADDRESS_UNAVAILABLE.has(code)) {
            // This machine has no IPv6 loopback; 127.0.0.1 alone decides.
          } else {
            await ipv4.close()
            throw new Error(
              `the harness could not judge port ${ipv4.port} on ::1 ` +
                `(${code ?? String(error)})`,
            )
          }
        }
      }
      selected.push(ipv4.port)
      kept.push(ipv4)
      if (ipv6 !== undefined) kept.push(ipv6)
    }

    if (selected.length < count) {
      const denied =
        rejected.length === 0
          ? 'no candidate was rejected'
          : rejected
              .map(({ port, reason }) => `${port} (${reason})`)
              .join(', ')
      throw new Error(
        `the harness could not allocate ${count} free loopback port(s) ` +
          `within ${maxCandidates} candidates; rejected: ${denied}`,
      )
    }
    return selected
  } finally {
    await Promise.all(kept.map((listener) => listener.close()))
  }
}

// A connection accepted on ::1 that does not answer like this server means another process holds the port; the fixtures retry with fresh ports.
export class LoopbackPortOccupiedError extends Error {
  readonly address = '::1' as const
  readonly port: number

  constructor(port: number, detail: string) {
    super(
      `the ::1 side of port ${port} is held by another process: ` +
        `a connection was accepted but ${detail}`,
    )
    this.name = 'LoopbackPortOccupiedError'
    this.port = port
  }
}

const IPV6_ADDRESS_UNAVAILABLE = new Set([
  'EADDRNOTAVAIL',
  'ENETUNREACH',
  'EAFNOSUPPORT',
])

const IPV6_CONNECTION_REFUSED = new Set(['ECONNREFUSED'])

// Retries only a bind failure or a taken ::1 port, while attempts remain.
export function shouldRetryStart(
  error: unknown,
  bindFailure: boolean,
  attempt: number,
  maxAttempts: number,
): boolean {
  const portOccupied = error instanceof LoopbackPortOccupiedError
  return (bindFailure || portOccupied) && attempt < maxAttempts
}

export interface ReadinessOptions {
  readonly sockets?: LoopbackSockets
  readonly readyTimeoutMs?: number
  readonly requestTimeoutMs?: number
  readonly retryDelayMs?: number
}

function probeTarget(url: string): {
  readonly port: number
  readonly path: string
} {
  const parsed = new URL(url)
  if (parsed.port === '') {
    throw new Error(`the readiness probe URL ${url} names no explicit port`)
  }
  return {
    port: Number(parsed.port),
    path: `${parsed.pathname}${parsed.search}`,
  }
}

// A refused connection or a missing address family means nothing is there (a pass); an accepted connection must answer 200 on the same path, or the port belongs to another process.
async function checkIPv6(
  sockets: LoopbackSockets,
  port: number,
  path: string,
  timeoutMs: number,
): Promise<void> {
  let status: number
  try {
    status = await sockets.request('::1', port, path, timeoutMs)
  } catch (error) {
    const code = errorCode(error)
    if (
      code !== undefined &&
      (IPV6_CONNECTION_REFUSED.has(code) || IPV6_ADDRESS_UNAVAILABLE.has(code))
    ) {
      return
    }
    throw new LoopbackPortOccupiedError(
      port,
      `it did not answer (${code ?? String(error)})`,
    )
  }
  if (status !== 200) {
    throw new LoopbackPortOccupiedError(port, `it answered HTTP ${status}`)
  }
}

// The browser's localhost is never resolved here: the literal 127.0.0.1 and ::1 are probed because Node answers 127.0.0.1 while the browser prefers ::1.
export async function waitForProbe(
  url: string,
  log: ProcessLog,
  options: ReadinessOptions = {},
): Promise<void> {
  const sockets = options.sockets ?? nodeLoopbackSockets
  const readyTimeoutMs = options.readyTimeoutMs ?? PROBE_TIMEOUT_MS
  const requestTimeoutMs = options.requestTimeoutMs ?? PROBE_REQUEST_TIMEOUT_MS
  const retryDelayMs = options.retryDelayMs ?? PROBE_RETRY_DELAY_MS
  const { port, path } = probeTarget(url)

  const deadline = Date.now() + readyTimeoutMs
  // The deadline bounds the 127.0.0.1 poll only; the ::1 check keeps its full request timeout, so an answer just before the bound is not squeezed into a timeout read as an occupied port.
  const ipv4RequestBudget = (): number =>
    Math.max(1, Math.min(requestTimeoutMs, deadline - Date.now()))
  let last = ''
  while (Date.now() < deadline) {
    let status: number | undefined
    try {
      status = await sockets.request(
        '127.0.0.1',
        port,
        path,
        ipv4RequestBudget(),
      )
    } catch (error) {
      last = String(error)
    }
    if (status === 200) {
      await checkIPv6(sockets, port, path, requestTimeoutMs)
      return
    }
    if (status !== undefined) last = `HTTP ${status}`
    await new Promise((resolve) => setTimeout(resolve, retryDelayMs))
  }
  throw new Error(
    `the operations probe ${url} did not answer 200 within ${readyTimeoutMs}ms ` +
      `(last answer: ${last})\n--- process log (last lines) ---\n${log.tail()}`,
  )
}
