import {
  ApiError,
  ERROR_CODE_NETWORK,
  ERROR_CODE_PROTOCOL,
  httpErrorCode,
  moduleCodeFromProblemType,
} from './errors.js'
import { applySeamHeaders, type SeamProviders } from './seams.js'

/** One parsed server-sent event. `event` defaults to the SSE spec's `message`. */
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
  /** Called when the response arrives with a streamable 2xx, before any event. */
  readonly onOpen?: () => void
  /** Called when the connection fails; not called for the caller's own cancellation. */
  readonly onError?: (error: ApiError) => void
  /** Called when the connection ends for any reason other than the caller's own cancellation. */
  readonly onClose?: () => void
  /** Caller cancellation: aborting ends the connection without onError or onClose. */
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

export interface EventStreamContext extends SeamProviders {
  readonly baseUrl: string
  readonly fetch: typeof fetch
  readonly refresh?: () => Promise<boolean>
  readonly credentials?: RequestCredentials
}

/**
 * A per-message incremental parser for the `text/event-stream` framing: frames
 * are separated by a blank line, a line's field and value are split at the
 * first colon, and a frame with no `data:` line is not dispatched.
 */
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
    // After the caller ends the stream, no onError/onClose fires.
    let cancelled = false
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
              void response.body?.cancel().catch(() => undefined)
              continue
            }
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
          settle(null)
          return
        }
      } catch (cause) {
        if (cancelled || callerSignal?.aborted === true) {
          return
        }
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
