/** One recorded request, as the client sent it. */
export interface StandinCall {
  readonly url: string
  /** The HTTP method as sent (uppercased by the client). */
  readonly method: string
  readonly headers: Headers
  /** Raw body text, when the client sent a string body. */
  readonly bodyText: string | undefined
  /** The body, when the client sent a binary (Blob) body. */
  readonly bodyBlob: Blob | null
  /** The body parsed as JSON, when bodyText carried JSON. */
  readonly bodyJson: unknown
  readonly signal: AbortSignal | null
}

/** An answered response, a network failure, or a promise that only ends by abort. */
export type StandinStep = Response | Error | Promise<Response | Error>

/** The per-call decision function behind a stand-in. */
export type StandinResponder = (call: StandinCall) => StandinStep

export interface StandinFetch {
  /** Inject as ClientOptions.fetch. */
  readonly fetch: typeof fetch
  /** Every call, in order, for assertions. */
  readonly calls: readonly StandinCall[]
}

/** A JSON response; an omitted body produces a body-less (204-style) response. */
export function jsonResponse(
  status: number,
  body?: unknown,
  extraHeaders?: Readonly<Record<string, string>>,
): Response {
  const text = body === undefined ? null : JSON.stringify(body)
  return new Response(text, {
    status,
    headers: { 'content-type': 'application/json', ...extraHeaders },
  })
}

/** A non-JSON response (an HTML error page, a plain-text body...). */
export function textResponse(
  status: number,
  text: string,
  extraHeaders?: Readonly<Record<string, string>>,
): Response {
  return new Response(text, { status, headers: { ...extraHeaders } })
}

/** A raw-byte response, like a content-download endpoint answers with. */
export function blobResponse(
  status: number,
  bytes: BlobPart,
  type: string,
): Response {
  return new Response(new Blob([bytes], { type }), {
    status,
    headers: { 'content-type': type },
  })
}

/** A step that never answers on its own; only a timeout or abort ends it. */
export const hang: StandinStep = new Promise<Response>(() => {})

/** Responds with each step in order; a further call throws as unscripted. */
export function sequence(...steps: StandinStep[]): StandinResponder {
  let index = 0
  return () => {
    const step = steps[index]
    if (step === undefined) {
      throw new Error(
        'fetch stand-in exhausted: the client made more requests than the test scripted',
      )
    }
    index += 1
    return step
  }
}

function abortError(): DOMException {
  return new DOMException('The operation was aborted.', 'AbortError')
}

/** A promise that rejects with AbortError when the signal aborts. */
function waitForAbort(signal: AbortSignal): Promise<never> {
  return new Promise((_resolve, reject) => {
    if (signal.aborted) {
      reject(abortError())
      return
    }
    signal.addEventListener('abort', () => reject(abortError()), { once: true })
  })
}

/** Builds a stand-in around a responder that records every call. */
export function createStandinFetch(responder: StandinResponder): StandinFetch {
  const calls: StandinCall[] = []

  const deliver = async (call: StandinCall): Promise<Response> => {
    const step = await responder(call)
    if (step instanceof Response) {
      return step
    }
    if (step instanceof Error) {
      throw step
    }
    throw new TypeError(
      'fetch stand-in responder produced neither Response nor Error',
    )
  }

  const fetchFn: typeof fetch = async (input, init) => {
    const url =
      typeof input === 'string'
        ? input
        : input instanceof URL
          ? input.href
          : input.url
    const headers = new Headers(init?.headers)
    const bodyText =
      typeof init?.body === 'string' ? init.body : undefined
    const bodyBlob =
      typeof Blob !== 'undefined' && init?.body instanceof Blob
        ? init.body
        : null
    let bodyJson: unknown = undefined
    if (
      bodyText !== undefined &&
      (headers.get('content-type') ?? '').includes('application/json')
    ) {
      try {
        bodyJson = JSON.parse(bodyText)
      } catch {
        bodyJson = undefined
      }
    }
    const call: StandinCall = {
      url,
      method: init?.method ?? 'GET',
      headers,
      bodyText,
      bodyBlob,
      bodyJson,
      signal: init?.signal ?? null,
    }
    calls.push(call)

    const signal = init?.signal ?? null
    if (signal === null) {
      return deliver(call)
    }
    return Promise.race([deliver(call), waitForAbort(signal)])
  }

  return { fetch: fetchFn, calls }
}

/** Convenience: a stand-in answering a scripted sequence. */
export function scriptedStandin(
  ...steps: StandinStep[]
): StandinFetch {
  return createStandinFetch(sequence(...steps))
}
