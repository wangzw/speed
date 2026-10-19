/**
 * The server-sent-event transport: a long-lived, server-pushed stream a
 * host opens through the client (the same fetch, token, tenant and
 * language seams every request uses) and reads frame by frame.
 *
 * It exists beside the request function because a streaming response is
 * not a request/response exchange: the request function resolves one
 * parsed body and applies a per-exchange timeout and an idempotent
 * retry budget, none of which a stream has. This opener instead keeps
 * the connection open, parses the `text/event-stream` framing into
 * discrete events, and hands each to the caller -- and it deliberately
 * neither times out nor reconnects: a dropped connection surfaces as an
 * `onClose` and reconnecting is the caller's own policy (the consumer
 * that wants to stay connected re-opens with its own backoff, so a
 * policy choice like "pause while the tab is hidden" lives where the
 * document is, not here).
 *
 * It is not part of the request function's contract (`RequestFn`): the
 * `SpeedClient` createClient returns carries it as its `openEventStream`
 * member, so the seams are the ones the client already holds and no
 * second wiring of fetch/token/tenant/language exists.
 *
 * Error vocabulary matches the request function: a non-2xx answer, a
 * transport failure or a protocol violation surfaces an `ApiError`
 * (`client.network`, `client.protocol`, the envelope's code or
 * `client.http.<status>`); the caller's own cancellation (its `signal`,
 * or `close()`) surfaces nothing.
 */

import {
  ApiError,
  ERROR_CODE_NETWORK,
  ERROR_CODE_PROTOCOL,
  httpErrorCode,
  moduleCodeFromProblemType,
} from './errors.js'
import { applySeamHeaders, type SeamProviders } from './seams.js'

/** One parsed server-sent event. `event` is the frame's `event:` name,
 * defaulting to the SSE spec's `message` when the frame carried none;
 * `data` is the frame's `data:` lines joined with newlines. */
export interface ServerSentEvent {
  readonly event: string
  readonly data: string
  readonly id?: string
  readonly retry?: number
}

/** One open stream's callbacks and cancellation. */
export interface EventStreamOptions {
  /** Called once per parsed event, in wire order. */
  readonly onEvent: (event: ServerSentEvent) => void
  /** Called when the response arrives with a streamable 2xx, before any
   * event. */
  readonly onOpen?: () => void
  /** Called when the connection fails (a non-2xx answer, a transport
   * failure, a protocol violation). Not called for the caller's own
   * cancellation. */
  readonly onError?: (error: ApiError) => void
  /** Called when the connection ends for any reason other than the
   * caller's own cancellation (`close()` or an aborted `signal`) -- a
   * clean server end or a failure. Reconnecting is the caller's policy. */
  readonly onClose?: () => void
  /** Caller cancellation: aborting ends the connection; no `onError` or
   * `onClose` fires for it. */
  readonly signal?: AbortSignal
}

/** A handle on an open stream; `close()` ends it. */
export interface EventStreamHandle {
  close(): void
}

/** Opens one event stream at an absolute path. */
export type EventStreamOpener = (
  path: string,
  options: EventStreamOptions,
) => EventStreamHandle

/** What the opener needs from the client's closure: the same seams and
 * fetch the request function uses, plus the single-flight refresh hook
 * so a 401 on the initial exchange can refresh and retry once, and the
 * client's fetch credential mode. */
export interface EventStreamContext extends SeamProviders {
  readonly baseUrl: string
  readonly fetch: typeof fetch
  readonly refresh?: () => Promise<boolean>
  /** The fetch `credentials` mode the client was configured with;
   * absent, fetch's own default (`same-origin`) applies. Forwarding it
   * keeps a cookie-delivered session presented on the stream's own
   * `GET`, exactly as it is on every request. */
  readonly credentials?: RequestCredentials
}

/** A per-message incremental parser for the `text/event-stream` framing
 * (W3C HTML, "Interpreting an event stream"): frames are separated by a
 * blank line, a line's field and value are split at the first colon, and
 * a frame with no `data:` line is not dispatched. */
function createSseParser(dispatch: (event: ServerSentEvent) => void) {
  let buffer = ''
  const parse = (block: string): void => {
    let name = ''
    let id: string | undefined
    let retry: number | undefined
    const data: string[] = []
    for (const rawLine of block.split(/\r\n|\n|\r/)) {
      if (rawLine === '' || rawLine.startsWith(':')) {
        continue
      }
      const colon = rawLine.indexOf(':')
      const field = colon === -1 ? rawLine : rawLine.slice(0, colon)
      let value = colon === -1 ? '' : rawLine.slice(colon + 1)
      if (value.startsWith(' ')) {
        value = value.slice(1)
      }
      switch (field) {
        case 'event':
          name = value
          break
        case 'data':
          data.push(value)
          break
        case 'id':
          id = value
          break
        case 'retry': {
          const parsed = Number(value)
          if (/^\d+$/.test(value) && Number.isFinite(parsed)) {
            retry = parsed
          }
          break
        }
        default:
          break
      }
    }
    if (data.length === 0) {
      return
    }
    dispatch({
      event: name === '' ? 'message' : name,
      data: data.join('\n'),
      ...(id === undefined ? {} : { id }),
      ...(retry === undefined ? {} : { retry }),
    })
  }
  return {
    push(chunk: string): void {
      buffer += chunk
      for (;;) {
        const boundary = /\r\n\r\n|\n\n|\r\r/.exec(buffer)
        if (boundary === null) {
          return
        }
        const block = buffer.slice(0, boundary.index)
        buffer = buffer.slice(boundary.index + boundary[0].length)
        parse(block)
      }
    },
  }
}

/** Reads the envelope's code out of an error body, or undefined when
 * the body is empty, not JSON, denotes no code, or resolves to a code in
 * the reserved `client.` namespace -- the same rule the request function's
 * envelope parse applies (the body's own `code`, or the code derived from
 * its RFC 9457 problem `type`), reduced to what a stream error needs. */
function envelopeCode(text: string): string | undefined {
  if (text.trim() === '') {
    return undefined
  }
  try {
    const parsed: unknown = JSON.parse(text)
    if (typeof parsed !== 'object' || parsed === null) {
      return undefined
    }
    const candidate = parsed as Record<string, unknown>
    const code =
      typeof candidate.code === 'string'
        ? candidate.code
        : typeof candidate.type === 'string'
          ? moduleCodeFromProblemType(candidate.type)
          : undefined
    return code !== undefined && !code.startsWith('client.')
      ? code
      : undefined
  } catch {
    return undefined
  }
}

/** The ApiError for a non-2xx stream answer: the envelope's code when
 * the body carries one, `client.http.<status>` otherwise. */
async function responseError(response: Response): Promise<ApiError> {
  let code: string | undefined
  try {
    code = envelopeCode(await response.text())
  } catch {
    code = undefined
  }
  return new ApiError({
    status: response.status,
    code: code ?? httpErrorCode(response.status),
    attempts: 1,
  })
}

/**
 * Builds the event-stream opener over the client's seams. The returned
 * opener opens one GET per call, applies the seam headers plus
 * `accept: text/event-stream`, refreshes-and-retries once on a bearer
 * 401, and reads frames until the connection ends or the caller closes
 * it.
 */
export function createEventStreamOpener(
  context: EventStreamContext,
): EventStreamOpener {
  const baseUrl = context.baseUrl.replace(/\/+$/, '')
  const fetchFn = context.fetch
  const providers: SeamProviders = {
    accessTokenStore: context.accessTokenStore,
    languageProvider: context.languageProvider,
    tenantProvider: context.tenantProvider,
    tenantHeader: context.tenantHeader,
  }
  const refresh = context.refresh
  const credentials = context.credentials

  return (path: string, options: EventStreamOptions): EventStreamHandle => {
    if (typeof path !== 'string' || !path.startsWith('/')) {
      throw new Error(
        `[speed-api-client] event-stream path must be an absolute path starting with "/" (baseUrl carries host and prefix); got ${JSON.stringify(path)}.`,
      )
    }
    const controller = new AbortController()
    let reader: ReadableStreamDefaultReader<Uint8Array> | undefined = undefined
    // Set once the caller has ended the stream (its own close() or an
    // aborted signal): no onError/onClose fires afterwards, and a
    // cancellation is never reported as a failure.
    let cancelled = false
    // Set once the connection has settled (a callbacks-fired state), so
    // the terminal notification fires exactly once.
    let settled = false
    const callerSignal = options.signal
    const onCallerAbort = (): void => {
      cancelled = true
      controller.abort()
      void reader?.cancel().catch(() => undefined)
    }
    if (callerSignal !== undefined) {
      if (callerSignal.aborted === true) {
        return { close: () => undefined }
      }
      callerSignal.addEventListener('abort', onCallerAbort, { once: true })
    }

    const detach = (): void => {
      callerSignal?.removeEventListener('abort', onCallerAbort)
    }

    /** Fires the terminal callbacks once. `error` is reported through
     * onError; a clean end (or a cancelled close) reports nothing. */
    const settle = (error: ApiError | null): void => {
      if (settled) {
        return
      }
      settled = true
      detach()
      if (cancelled) {
        return
      }
      if (error !== null) {
        options.onError?.(error)
      }
      options.onClose?.()
    }

    const close = (): void => {
      if (cancelled) {
        return
      }
      cancelled = true
      settled = true
      detach()
      controller.abort()
      void reader?.cancel().catch(() => undefined)
    }

    void (async (): Promise<void> => {
      try {
        let retriedAfterRefresh = false
        for (;;) {
          if (cancelled) {
            return
          }
          const headers = new Headers({ accept: 'text/event-stream' })
          const attachedToken = applySeamHeaders(headers, providers)
          let response: Response
          try {
            response = await fetchFn(`${baseUrl}${path}`, {
              method: 'GET',
              headers,
              signal: controller.signal,
              // Only set when the client declared a mode: absent, fetch's
              // own default (same-origin) applies, the same shape the
              // request function sends.
              ...(credentials === undefined ? {} : { credentials }),
            })
          } catch (cause) {
            if (cancelled || callerSignal?.aborted === true) {
              return
            }
            settle(
              new ApiError({
                status: 0,
                code: ERROR_CODE_NETWORK,
                attempts: 1,
                cause,
              }),
            )
            return
          }
          if (cancelled) {
            void response.body?.cancel().catch(() => undefined)
            return
          }
          if (response.status === 401 && attachedToken && !retriedAfterRefresh && refresh !== undefined) {
            retriedAfterRefresh = true
            let refreshed = false
            try {
              refreshed = (await refresh()) === true
            } catch {
              refreshed = false
            }
            if (cancelled || callerSignal?.aborted === true) {
              void response.body?.cancel().catch(() => undefined)
              return
            }
            if (refreshed) {
              // The refused 401's body is not wanted: release it
              // deterministically, then retry with the fresh token.
              void response.body?.cancel().catch(() => undefined)
              continue
            }
            // Refresh failed: read the 401's envelope for the terminal
            // error (code and trace id preserved), as the request loop
            // does.
            settle(await responseError(response))
            return
          }
          if (response.status < 200 || response.status > 299) {
            settle(await responseError(response))
            return
          }
          const body = response.body
          if (body === null) {
            settle(
              new ApiError({
                status: response.status,
                code: ERROR_CODE_PROTOCOL,
                attempts: 1,
                cause: new TypeError('the event-stream response has no body'),
              }),
            )
            return
          }
          options.onOpen?.()
          reader = body.getReader()
          const decoder = new TextDecoder()
          const parser = createSseParser((event) => {
            if (!cancelled) {
              options.onEvent(event)
            }
          })
          for (;;) {
            const { value, done } = await reader.read()
            if (done) {
              break
            }
            if (value !== undefined) {
              parser.push(decoder.decode(value, { stream: true }))
            }
          }
          // The server ended the stream: report a clean close so the
          // caller can reconnect under its own policy.
          settle(null)
          return
        }
      } catch (cause) {
        if (cancelled || callerSignal?.aborted === true) {
          return
        }
        // A read that failed after headers (a dropped connection): a
        // transport failure, reported so the caller can retry.
        settle(
          cause instanceof ApiError
            ? cause
            : new ApiError({
                status: 0,
                code: ERROR_CODE_NETWORK,
                attempts: 1,
                cause,
              }),
        )
      }
    })()

    return { close }
  }
}
