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

const cleanups: Array<() => Promise<void>> = []

afterEach(async () => {
  const closing = cleanups.splice(0)
  await Promise.all(closing.map((close) => close()))
})

function closeServer(server: NetServer | HttpServer): Promise<void> {
  if ('closeAllConnections' in server) server.closeAllConnections()
  return new Promise((resolve) => {
    server.close(() => resolve())
  })
}

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

const FREE_PORT_FIRST = 15_000

const FREE_PORT_ATTEMPTS = 256

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

function startHealthyIPv4(): Promise<
  { readonly port: number; readonly close: () => Promise<void> }
> {
  return startHttpOn('127.0.0.1', 0, 200)
}

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

function simulatedSystemError(code: string): NodeJS.ErrnoException {
  return Object.assign(new Error(`simulated ${code}`), { code })
}

describe('the loopback readiness probe', () => {
  it.skipIf(!ipv6LoopbackAvailable)(
    'names the ::1 occupant when 127.0.0.1 answers 200',
    async () => {
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

  it.skipIf(!ipv6LoopbackAvailable)(
    'passes when ::1 answers 200 on the same path',
    async () => {
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

  it.skipIf(!ipv6LoopbackAvailable)(
    'gives the ::1 check its full request timeout when the deadline is nearly spent',
    async () => {
      const port = await findFreeLoopbackPort()
      const ipv4 = await startHttpOn('127.0.0.1', port, 200)
      cleanups.push(ipv4.close)
      const ipv6 = await startHttpOn('::1', port, 200)
      cleanups.push(ipv6.close)

      const readyTimeoutMs = scaledTimeout(300)
      const requestTimeoutMs = scaledTimeout(500)
      const ipv6AnswerMs = scaledTimeout(25)
      const seenIPv6Timeouts: number[] = []

      const sockets: LoopbackSockets = {
        bind: nodeLoopbackSockets.bind,
        request: async (address, port, path, timeoutMs) => {
          if (address === '127.0.0.1') {
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
  it.skipIf(!ipv6LoopbackAvailable)(
    'does not return a port another process holds on ::1',
    async () => {
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
