/**
 * Port allocation and readiness probing for the fixtures' loopback servers.
 *
 * The e2e and deploy fixtures each start a real binary on ports the kernel
 * hands out. A port that is free on 127.0.0.1 can still be held on ::1, and a
 * browser resolving `localhost` prefers ::1, so a port the fixture picked can
 * be unreachable in the browser while 127.0.0.1 answers. Both fixtures
 * therefore ask this module for their ports and for the answer that the
 * server is reachable, and the module judges the two address families the way
 * the browser does.
 *
 * The product itself still binds 127.0.0.1 only: one Go listener has one
 * address, and binding every interface is outside the harness. So a port
 * allocated here can be taken on ::1 between allocation and the child's bind;
 * the readiness probe catches that window through the fixtures' existing
 * terminate-and-retry loop. An occupant that appears after the probe has
 * passed cannot be caught without widening the product's listener.
 */
import { get as httpGet } from 'node:http'
import { createServer } from 'node:net'

import type { ProcessLog } from './log'
import { scaledTimeout } from './time-scale'

/** How long the readiness probes may take to answer after the ready record. */
const PROBE_TIMEOUT_MS = scaledTimeout(15_000)

/** How long one readiness probe request may take to answer. */
const PROBE_REQUEST_TIMEOUT_MS = scaledTimeout(5_000)

/** How long the readiness poll waits between answers that are not 200. */
const PROBE_RETRY_DELAY_MS = scaledTimeout(100)

/**
 * How many candidates one allocation may spend before it gives up. A free
 * 127.0.0.1 port is almost always free on ::1 too; the bound is what keeps a
 * machine whose ::1 is crowded from spinning forever, and what gives the
 * failure a place to list the rejected ports.
 */
const MAX_PORT_CANDIDATES = 64

/** The two literal loopback addresses this harness distinguishes. */
export type LoopbackAddress = '127.0.0.1' | '::1'

/** One listener the allocator holds until it has chosen every port. */
export interface LoopbackListener {
  readonly address: LoopbackAddress
  readonly port: number
  close(): Promise<void>
}

/**
 * The socket operations allocation and readiness perform. The default uses
 * real listeners and real HTTP; a test substitutes one to stand a machine
 * without an IPv6 loopback in front of both callers.
 */
export interface LoopbackSockets {
  /** Bind one literal loopback address; port 0 asks the kernel for one. */
  bind(address: LoopbackAddress, port: number): Promise<LoopbackListener>
  /** One HTTP GET to a literal loopback address; answer the status code. */
  request(
    address: LoopbackAddress,
    port: number,
    path: string,
    timeoutMs: number,
  ): Promise<number>
}

/** The system error code a failed bind or request carries, if any. */
function errorCode(error: unknown): string | undefined {
  if (typeof error !== 'object' || error === null) return undefined
  const code = (error as { readonly code?: unknown }).code
  return typeof code === 'string' ? code : undefined
}

/** Bind one listener on a literal loopback address. */
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
        // A specific `::1` address already excludes IPv4; the flag makes the
        // intent explicit and never asks for the wildcard.
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

/** One HTTP GET to a literal loopback address; the answer is the status. */
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

/** The real sockets: listeners the kernel grants and HTTP over them. */
export const nodeLoopbackSockets: LoopbackSockets = {
  bind: nodeBind,
  request: nodeRequest,
}

/** How one allocation gets its candidates and judges them. */
export interface PortAllocationOptions {
  /** The socket primitives. Defaults to {@link nodeLoopbackSockets}. */
  readonly sockets?: LoopbackSockets
  /**
   * Answers the next candidate port as a listener already holding it on
   * 127.0.0.1. Defaults to asking the kernel for 127.0.0.1:0. A test feeds a
   * specific port by binding it here.
   */
  readonly nextCandidate?: () => Promise<LoopbackListener>
  /**
   * Whether a candidate must also be free on ::1. Defaults to true; the
   * identity provider's origins are literal 127.0.0.1, so it is the one
   * caller that turns this off.
   */
  readonly requireIPv6?: boolean
  /** How many candidates the allocation may spend. */
  readonly maxCandidates?: number
}

/**
 * Ask for ports that are free on both loopback families.
 *
 * Each candidate is held on 127.0.0.1 while ::1 at the same port is tried. A
 * candidate held on ::1 is rejected and its 127.0.0.1 listener closed; a
 * machine with no IPv6 loopback reports `EADDRNOTAVAIL`, `ENETUNREACH` or
 * `EAFNOSUPPORT` and is judged on 127.0.0.1 alone. Every accepted listener is
 * kept until the whole batch is chosen, then all are closed together, so a
 * selected port is not handed to the next candidate's kernel call.
 */
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
            // Another process holds this port on ::1 even though 127.0.0.1
            // is free. Drop the candidate; the listener is closed so the
            // rejected port is not leaked for the rest of the worker.
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

/**
 * A readiness failure caused by another process holding the port on ::1: the
 * connection was accepted but the peer did not answer like the harness's own
 * server. The fixtures treat it as a taken port and retry with fresh ones.
 */
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

/** The codes that mean this machine has no usable ::1 address family. */
const IPV6_ADDRESS_UNAVAILABLE = new Set([
  'EADDRNOTAVAIL',
  'ENETUNREACH',
  'EAFNOSUPPORT',
])

/** The code that means ::1 exists but no process accepts a connection there. */
const IPV6_CONNECTION_REFUSED = new Set(['ECONNREFUSED'])

/**
 * Whether a failed start is worth another attempt with fresh ports: the child
 * could not bind its port, or another process answered on the port's ::1 side,
 * and the attempt bound has not run out. Every other failure is this server's
 * own and is reported at once.
 */
export function shouldRetryStart(
  error: unknown,
  bindFailure: boolean,
  attempt: number,
  maxAttempts: number,
): boolean {
  const portOccupied = error instanceof LoopbackPortOccupiedError
  return (bindFailure || portOccupied) && attempt < maxAttempts
}

/** How the readiness probe is bounded and, in a test, which sockets it uses. */
export interface ReadinessOptions {
  /** The socket primitives. Defaults to {@link nodeLoopbackSockets}. */
  readonly sockets?: LoopbackSockets
  /** How long the probe may keep polling. */
  readonly readyTimeoutMs?: number
  /** How long one request may take to answer. */
  readonly requestTimeoutMs?: number
  /** How long the poll waits between answers that are not 200. */
  readonly retryDelayMs?: number
}

/** The port and path of one readiness URL; its host is the browser's name. */
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

/**
 * Judge the ::1 side of one ready path. A refused connection or a missing
 * address family means nothing is there and the answer is a pass; an accepted
 * connection must answer 200 on the same path, or the port belongs to another
 * process. The refused case returns at once, so a normal start pays one
 * immediate connection attempt and no wait.
 */
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

/**
 * Poll a browser-facing origin until it answers 200 on both loopback families,
 * or fail after the bound.
 *
 * The URL is the origin the browser uses (`http://localhost:<port>/<path>`),
 * but its host is never resolved: the literal 127.0.0.1 and ::1 addresses are
 * probed instead. Node's resolver answers 127.0.0.1 for `localhost` on the
 * machines this tier runs on, while the browser prefers ::1, so a hostname
 * probe would report a server the browser cannot reach.
 */
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
  // The deadline bounds the 127.0.0.1 poll only: each of its requests may
  // spend at most the time left, so a poll that starts near the bound
  // overshoots it by at most one retry delay rather than by a whole request
  // timeout. The ::1 check that follows a 200 keeps its full request timeout,
  // because a 127.0.0.1 answer that lands just before the bound must not leave
  // it a budget too small for this process's own server to answer; a timeout
  // there is judged as another process holding the port.
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
