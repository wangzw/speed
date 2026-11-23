/**
 * The loopback port allocator and the readiness probe, from the browser's
 * point of view.
 *
 * A browser resolving `localhost` prefers ::1, while Node's resolver on the
 * machines the tier runs on answers 127.0.0.1. A port that answers HTTP on
 * 127.0.0.1 can therefore be held by another process on ::1 and the browser
 * sees an empty response. Every case here stands up the two addresses for real
 * and asks the harness's own functions, never a substitute for them.
 */
import {
  createServer as createHttpServer,
  type Server as HttpServer,
} from 'node:http'
import {
  createServer as createNetServer,
  type Server as NetServer,
  type Socket,
} from 'node:net'

import { afterEach, describe, expect, it } from 'vitest'

import { ProcessLog } from './log'
import {
  freePorts,
  LoopbackPortOccupiedError,
  nodeLoopbackSockets,
  shouldRetryStart,
  waitForProbe,
  type LoopbackListener,
  type LoopbackSockets,
} from './loopback'
import { scaledTimeout } from './time-scale'

/** Every listener a case opened, closed even when the case fails. */
const cleanups: Array<() => Promise<void>> = []

afterEach(async () => {
  const closing = cleanups.splice(0)
  await Promise.all(closing.map((close) => close()))
})

/** Close a listener, dropping the connections it still holds. */
function closeServer(server: NetServer | HttpServer): Promise<void> {
  if ('closeAllConnections' in server) server.closeAllConnections()
  return new Promise((resolve) => {
    server.close(() => resolve())
  })
}

/** Whether this machine has an IPv6 loopback the cases can bind. */
async function canBindIPv6Loopback(): Promise<boolean> {
  try {
    const listener = await nodeLoopbackSockets.bind('::1', 0)
    await listener.close()
    return true
  } catch {
    return false
  }
}

const ipv6LoopbackAvailable = await canBindIPv6Loopback()

/**
 * The first explicit port a case tries when it needs a number that is free on
 * both loopback families. It lies below this machine's ephemeral range
 * (20000-65535), so the kernel never hands it to another process's outgoing
 * connection, and the case owns the number rather than taking one the kernel
 * picked.
 */
const FREE_PORT_FIRST = 15_000

/** How many consecutive explicit ports a case may try. */
const FREE_PORT_ATTEMPTS = 256

/**
 * A port the case itself confirmed free on both loopback families.
 *
 * A case cannot take a candidate from `bind(address, 0)`: the kernel judges
 * only the address it was asked about, so on this machine it can hand out
 * 32769 for 127.0.0.1 while another process's wildcard listener already holds
 * [::]:32769, and the follow-up bind on ::1 then fails with `EADDRINUSE`. The
 * explicit number is bound on 127.0.0.1 and ::1 in turn and returned only
 * after both binds succeeded, so neither family is left unjudged.
 */
async function findFreeLoopbackPort(): Promise<number> {
  for (
    let port = FREE_PORT_FIRST;
    port < FREE_PORT_FIRST + FREE_PORT_ATTEMPTS;
    port += 1
  ) {
    const ipv4 = await nodeLoopbackSockets.bind('127.0.0.1', port).then(
      (listener) => listener,
      () => undefined,
    )
    if (ipv4 === undefined) continue
    const ipv6 = await nodeLoopbackSockets.bind('::1', port).then(
      (listener) => listener,
      () => undefined,
    )
    await ipv4.close()
    if (ipv6 === undefined) continue
    await ipv6.close()
    return port
  }
  throw new Error(
    `no port in ${FREE_PORT_FIRST}-${FREE_PORT_FIRST + FREE_PORT_ATTEMPTS - 1} ` +
      'was free on both 127.0.0.1 and ::1',
  )
}

/** An HTTP server on one literal loopback address answering a fixed status. */
async function startHttpOn(
  address: '127.0.0.1' | '::1',
  port: number,
  status: number,
): Promise<
  { readonly port: number; readonly close: () => Promise<void> }
> {
  const server = createHttpServer((_request, response) => {
    response.writeHead(status, { 'Content-Type': 'text/plain' })
    response.end(String(status))
  })
  const bound = await new Promise<number>((resolve, reject) => {
    server.once('error', reject)
    server.listen(
      {
        port,
        host: address,
        ipv6Only: address === '::1' ? true : undefined,
      },
      () => {
        const boundAddress = server.address()
        if (boundAddress === null || typeof boundAddress === 'string') {
          reject(new Error('the listener answered without a TCP address'))
          return
        }
        resolve(boundAddress.port)
      },
    )
  })
  return { port: bound, close: () => closeServer(server) }
}

/** An HTTP server on 127.0.0.1 that answers 200 to every request. */
function startHealthyIPv4(): Promise<
  { readonly port: number; readonly close: () => Promise<void> }
> {
  return startHttpOn('127.0.0.1', 0, 200)
}

/**
 * A wildcard IPv6 listener on one port, configured the way a long-lived
 * occupant is: v6only, so the same port stays free on 127.0.0.1; it accepts
 * every connection and never answers.
 */
async function startSilentIPv6(
  port: number,
): Promise<{ readonly close: () => Promise<void> }> {
  const sockets = new Set<Socket>()
  const server = createNetServer((socket) => {
    sockets.add(socket)
    socket.once('close', () => sockets.delete(socket))
  })
  await new Promise<void>((resolve, reject) => {
    server.once('error', reject)
    server.listen({ port, host: '::', ipv6Only: true }, () => resolve())
  })
  return {
    close: () =>
      new Promise<void>((resolve) => {
        for (const socket of sockets) socket.destroy()
        server.close(() => resolve())
      }),
  }
}

/** The error a missing address family produces, with its system code. */
function simulatedSystemError(code: string): NodeJS.ErrnoException {
  return Object.assign(new Error(`simulated ${code}`), { code })
}

describe('the loopback readiness probe', () => {
  // T1. The defect: Node resolves `localhost` to 127.0.0.1 only, so the old
  // probe answered 200 while the browser's preferred ::1 was held by another
  // process. Skipped only on a machine with no IPv6 loopback, where there is
  // no second address to hold.
  it.skipIf(!ipv6LoopbackAvailable)(
    'names the ::1 occupant when 127.0.0.1 answers 200',
    async () => {
      // The port is confirmed free on both families by the case itself, so
      // the silent occupant below cannot land on a number this machine
      // already holds on ::1.
      const port = await findFreeLoopbackPort()
      const healthy = await startHttpOn('127.0.0.1', port, 200)
      cleanups.push(healthy.close)
      const silent = await startSilentIPv6(port)
      cleanups.push(silent.close)

      const outcome = await waitForProbe(
        `http://localhost:${healthy.port}/health`,
        new ProcessLog(),
        {
          readyTimeoutMs: scaledTimeout(5_000),
          requestTimeoutMs: scaledTimeout(500),
          retryDelayMs: scaledTimeout(50),
        },
      ).then(
        () => ({ passed: true as const }),
        (error: unknown) => ({ passed: false as const, error }),
      )

      expect(
        outcome.passed,
        'the probe passed although another process holds the port on ::1',
      ).toBe(false)
      if (!outcome.passed) {
        const message =
          outcome.error instanceof Error
            ? outcome.error.message
            : String(outcome.error)
        expect(message).toContain('::1')
        expect(message).toContain(String(healthy.port))
      }
    },
  )

  // T3. A machine with no IPv6 loopback must not be a failure: the allocator
  // judges on 127.0.0.1 alone and the probe treats the missing family as
  // "nothing there". The simulation goes through the same injection point the
  // allocator and the probe share.
  it.each(['EADDRNOTAVAIL', 'EAFNOSUPPORT', 'ENETUNREACH'])(
    'allocates and probes when ::1 reports %s',
    async (code) => {
      const sockets: LoopbackSockets = {
        bind: (address, port) =>
          address === '::1'
            ? Promise.reject(simulatedSystemError(code))
            : nodeLoopbackSockets.bind(address, port),
        request: (address, port, path, timeoutMs) =>
          address === '::1'
            ? Promise.reject(simulatedSystemError(code))
            : nodeLoopbackSockets.request(address, port, path, timeoutMs),
      }

      const ports = await freePorts(2, { sockets })
      expect(ports).toHaveLength(2)
      expect(new Set(ports).size).toBe(2)

      const healthy = await startHealthyIPv4()
      cleanups.push(healthy.close)
      await expect(
        waitForProbe(`http://localhost:${healthy.port}/health`, new ProcessLog(), {
          sockets,
          readyTimeoutMs: scaledTimeout(2_000),
          requestTimeoutMs: scaledTimeout(500),
          retryDelayMs: scaledTimeout(50),
        }),
      ).resolves.toBeUndefined()
    },
  )

  // The accepted-connection branch: ::1 answering the same path with 200 is
  // the harness's own server reached through the browser's family, and it
  // passes. Anything else on ::1 belongs to another process and is retried.
  it.skipIf(!ipv6LoopbackAvailable)(
    'passes when ::1 answers 200 on the same path',
    async () => {
      // Both servers share a port the case confirmed free on both families.
      const port = await findFreeLoopbackPort()
      const ipv4 = await startHttpOn('127.0.0.1', port, 200)
      cleanups.push(ipv4.close)
      const ipv6 = await startHttpOn('::1', port, 200)
      cleanups.push(ipv6.close)

      await expect(
        waitForProbe(`http://localhost:${ipv4.port}/health`, new ProcessLog(), {
          readyTimeoutMs: scaledTimeout(2_000),
          requestTimeoutMs: scaledTimeout(500),
          retryDelayMs: scaledTimeout(50),
        }),
      ).resolves.toBeUndefined()
    },
  )

  // The deadline bounds the 127.0.0.1 poll only. A 127.0.0.1 answer that
  // lands a few milliseconds before the bound must not squeeze the ::1 check
  // into a budget too small for this process's own server to answer: a
  // timeout there reads as another process holding the port, so the retry
  // loop would throw the attempt away and could report the port as occupied.
  it.skipIf(!ipv6LoopbackAvailable)(
    'gives the ::1 check its full request timeout when the deadline is nearly spent',
    async () => {
      // The examined port is one the case confirmed free on both families
      // itself, so the two servers below cannot land on a number another
      // process holds on ::1.
      const port = await findFreeLoopbackPort()
      const ipv4 = await startHttpOn('127.0.0.1', port, 200)
      cleanups.push(ipv4.close)
      const ipv6 = await startHttpOn('::1', port, 200)
      cleanups.push(ipv6.close)

      const readyTimeoutMs = scaledTimeout(300)
      const requestTimeoutMs = scaledTimeout(500)
      // The own ::1 server answers, but not instantly: the point is that the
      // budget it is handed is wide enough for it to be heard at all.
      const ipv6AnswerMs = scaledTimeout(25)
      const seenIPv6Timeouts: number[] = []

      const sockets: LoopbackSockets = {
        bind: nodeLoopbackSockets.bind,
        request: async (address, port, path, timeoutMs) => {
          if (address === '127.0.0.1') {
            // Come back 200 with only a few milliseconds left on the bound.
            await new Promise((resolve) =>
              setTimeout(resolve, readyTimeoutMs - scaledTimeout(10)),
            )
            return nodeLoopbackSockets.request(address, port, path, timeoutMs)
          }
          seenIPv6Timeouts.push(timeoutMs)
          if (timeoutMs < ipv6AnswerMs) {
            throw Object.assign(
              new Error(`[::1] did not answer within ${timeoutMs}ms`),
              { code: 'ETIMEDOUT' },
            )
          }
          await new Promise((resolve) => setTimeout(resolve, ipv6AnswerMs))
          return nodeLoopbackSockets.request(address, port, path, timeoutMs)
        },
      }

      await expect(
        waitForProbe(`http://localhost:${ipv4.port}/health`, new ProcessLog(), {
          sockets,
          readyTimeoutMs,
          requestTimeoutMs,
          retryDelayMs: scaledTimeout(50),
        }),
      ).resolves.toBeUndefined()

      expect(seenIPv6Timeouts).toEqual([requestTimeoutMs])
    },
  )

  it.skipIf(!ipv6LoopbackAvailable)(
    'treats a ::1 answer other than 200 as an occupied port',
    async () => {
      // Both servers share a port the case confirmed free on both families.
      const port = await findFreeLoopbackPort()
      const ipv4 = await startHttpOn('127.0.0.1', port, 200)
      cleanups.push(ipv4.close)
      const ipv6 = await startHttpOn('::1', port, 500)
      cleanups.push(ipv6.close)

      const outcome = await waitForProbe(
        `http://localhost:${ipv4.port}/health`,
        new ProcessLog(),
        {
          readyTimeoutMs: scaledTimeout(2_000),
          requestTimeoutMs: scaledTimeout(500),
          retryDelayMs: scaledTimeout(50),
        },
      ).then(
        () => ({ passed: true as const }),
        (error: unknown) => ({ passed: false as const, error }),
      )

      expect(
        outcome.passed,
        'the probe passed although ::1 answered 500',
      ).toBe(false)
      if (!outcome.passed) {
        expect(outcome.error).toBeInstanceOf(LoopbackPortOccupiedError)
        const message =
          outcome.error instanceof Error
            ? outcome.error.message
            : String(outcome.error)
        expect(message).toContain('::1')
        expect(message).toContain(String(ipv4.port))
        expect(message).toContain('HTTP 500')
      }
    },
  )

  // A reset means ::1 accepted the connection and then dropped it, which is
  // another process, never a missing address family: the probe must report
  // the port as occupied rather than pass it.
  it('treats a ::1 connection reset as an occupied port', async () => {
    const port = 41_234
    const sockets: LoopbackSockets = {
      bind: nodeLoopbackSockets.bind,
      request: (address) =>
        address === '::1'
          ? Promise.reject(simulatedSystemError('ECONNRESET'))
          : Promise.resolve(200),
    }

    const error = await waitForProbe(
      `http://localhost:${port}/health`,
      new ProcessLog(),
      {
        sockets,
        readyTimeoutMs: scaledTimeout(2_000),
        requestTimeoutMs: scaledTimeout(500),
        retryDelayMs: scaledTimeout(50),
      },
    ).then(
      () => undefined,
      (failure: unknown) => failure,
    )

    expect(error).toBeInstanceOf(LoopbackPortOccupiedError)
    const message = error instanceof Error ? error.message : String(error)
    expect(message).toContain('::1')
    expect(message).toContain(String(port))
    expect(message).toContain('ECONNRESET')
  })
})

describe('the loopback port allocator', () => {
  // T2. The candidate source is injectable so the case can hand the allocator
  // a port that is held on ::1 before one that is free, and can then show the
  // occupant was left alone.
  it.skipIf(!ipv6LoopbackAvailable)(
    'does not return a port another process holds on ::1',
    async () => {
      // The occupied number and the free one are both explicit ports the case
      // confirmed free on the two families first. Neither may come from
      // `bind(..., 0)`: the kernel could hand out a number another process
      // holds on ::1, failing the case on this machine's environment rather
      // than on the allocator's judgment.
      const occupiedPort = await findFreeLoopbackPort()
      const occupant = await nodeLoopbackSockets.bind('::1', occupiedPort)
      cleanups.push(occupant.close)
      const freePort = await findFreeLoopbackPort()

      const candidates: Array<() => Promise<LoopbackListener>> = [
        () => nodeLoopbackSockets.bind('127.0.0.1', occupant.port),
        () => nodeLoopbackSockets.bind('127.0.0.1', freePort),
      ]
      let index = 0
      const ports = await freePorts(1, {
        nextCandidate: () => {
          const next = candidates[index]
          index += 1
          if (next === undefined) {
            throw new Error(
              'the allocator asked for more candidates than the case supplies',
            )
          }
          return next()
        },
        maxCandidates: candidates.length,
      })

      expect(ports).toHaveLength(1)
      expect(ports[0]).not.toBe(occupant.port)
      // The occupant is still listening: the rejected candidate closed the
      // allocator's own 127.0.0.1 listener, never the process's ::1 one. A
      // rebind that unexpectedly succeeds joins the cleanups so the case
      // never leaks it either.
      const rebound = await nodeLoopbackSockets
        .bind('::1', occupant.port)
        .then(
          (listener) => ({ bound: true as const, listener }),
          (error: unknown) => ({ bound: false as const, error }),
        )
      if (rebound.bound) cleanups.push(rebound.listener.close)
      expect(
        rebound.bound,
        'the allocator closed the ::1 occupant it was supposed to leave alone',
      ).toBe(false)
      if (!rebound.bound) {
        expect(rebound.error).toMatchObject({ code: 'EADDRINUSE' })
      }
    },
  )

  // Any other ::1 bind error means the allocator cannot judge the port; it
  // must fail loudly and name both the code and the port rather than return a
  // port whose ::1 side it never checked.
  it('names the code and port when ::1 cannot be judged', async () => {
    const attempted: number[] = []
    const sockets: LoopbackSockets = {
      bind: (address, port) => {
        if (address === '::1') {
          attempted.push(port)
          return Promise.reject(simulatedSystemError('EACCES'))
        }
        return nodeLoopbackSockets.bind(address, port)
      },
      request: nodeLoopbackSockets.request,
    }

    const outcome = await freePorts(1, { sockets }).then(
      (ports) => ({ allocated: ports }),
      (error: unknown) => ({ error }),
    )

    expect(
      'error' in outcome,
      'the allocator returned a port whose ::1 side it could not judge',
    ).toBe(true)
    expect(attempted).toHaveLength(1)
    if ('error' in outcome) {
      const message =
        outcome.error instanceof Error
          ? outcome.error.message
          : String(outcome.error)
      expect(message).toContain('EACCES')
      expect(message).toContain(String(attempted[0]))
    }
  })

  // Running out of candidates must report every rejected port and the reason,
  // and must close each candidate's 127.0.0.1 listener on the way out.
  it('lists the rejected ports and reasons when candidates run out', async () => {
    const bound: number[] = []
    const closed: number[] = []
    let nextPort = 41_000
    const sockets: LoopbackSockets = {
      bind: (address, port) => {
        if (address === '::1') {
          return Promise.reject(simulatedSystemError('EADDRINUSE'))
        }
        const actual = port === 0 ? nextPort++ : port
        bound.push(actual)
        return Promise.resolve({
          address,
          port: actual,
          close: async () => {
            closed.push(actual)
          },
        })
      },
      request: nodeLoopbackSockets.request,
    }

    const outcome = await freePorts(1, {
      sockets,
      maxCandidates: 3,
    }).then(
      (ports) => ({ allocated: ports }),
      (error: unknown) => ({ error }),
    )

    expect('error' in outcome).toBe(true)
    expect(bound).toEqual([41_000, 41_001, 41_002])
    expect(closed).toEqual([41_000, 41_001, 41_002])
    if ('error' in outcome) {
      const message =
        outcome.error instanceof Error
          ? outcome.error.message
          : String(outcome.error)
      expect(message).toContain('within 3 candidates')
      for (const port of bound) {
        expect(message).toContain(`${port} (EADDRINUSE)`)
      }
    }
  })
})

describe('the fixture retry decision', () => {
  // The ::1 occupant reaches the fixtures as a LoopbackPortOccupiedError
  // thrown by the probe, with the child still running. Dropping that branch
  // would turn a port another process grabbed between allocation and startup
  // into a failed journey, so the decision is pinned here: the fixture loop
  // itself needs a built binary and is not reachable from a unit test.
  const occupied = new LoopbackPortOccupiedError(41_234, 'it did not answer')

  it('retries a port another process holds on ::1 while attempts remain', () => {
    expect(shouldRetryStart(occupied, false, 1, 3)).toBe(true)
    expect(shouldRetryStart(occupied, false, 3, 3)).toBe(false)
  })

  it('still retries a bind failure', () => {
    expect(shouldRetryStart(new Error('exit 1'), true, 1, 3)).toBe(true)
    expect(shouldRetryStart(new Error('exit 1'), true, 3, 3)).toBe(false)
  })

  it('reports any other failure without retrying', () => {
    expect(shouldRetryStart(new Error('ready timeout'), false, 1, 3)).toBe(
      false,
    )
  })
})
