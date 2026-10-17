# @speed/api-client

The hand-written HTTP runtime of the speed frontend: one `createClient`
call wires injectable fetch, an optional memory-only access-token store,
the fetch credential mode (for a cookie-delivered session), a silent
single-flight 401 refresh, a timeout, a conservative retry policy and a
structured reporter into a single typed request function. It is the
HTTP layer the generated `@speed/api-sdk` surface (the orval output of
the merged platform document) calls into; other packages never hand-write HTTP
requests -- the `speed/no-direct-http` ESLint rule enforces that, and
this package is its single whitelist.

The package ships no UI, no i18n resources and no storage API: error
`code`s map to user-facing text in the consuming package's own
catalogs, and no credential is persisted (the bearer token lives in
memory only; a cookie session is the browser's own httpOnly cookie --
see below).

## Quick start

```ts
import {
  createClient,
  createMemoryAccessTokenStore,
  isApiError,
} from '@speed/api-client'

/** A note as the API returns it. */
interface Note {
  id: string
  title: string
}

/** Loads the current session's notes through the API client. */
export async function loadNotes(): Promise<Note[]> {
  const api = createClient({
    // Scheme + host + optional prefix, or a same-origin '/api/...'
    // path in development. The fetch implementation is injectable
    // (tests pass a deterministic stand-in); when omitted, the
    // environment's global fetch is captured at construction time.
    baseUrl: '/api/v1',
    // The fetch credential mode. A cookie-delivered session (the
    // platform default) relies on 'same-origin', which sends the
    // server's session cookie with every same-origin request and makes
    // a refused credentialed request eligible for the refresh hook
    // below. Without an accessTokenStore no Authorization header is
    // ever sent -- the cookie is the credential.
    credentials: 'same-origin',
    // Optional bearer store, for a host that authenticates with a
    // token instead: it starts empty (auth fills it in memory, never in
    // storage -- an access token in localStorage is a credential an XSS
    // walks away with). A cookie host leaves it out.
    accessTokenStore: createMemoryAccessTokenStore(),
    // Silent 401 refresh: @speed/auth-core supplies the real hook. A
    // cookie session wires the hook to session.restore (re-read
    // GET /auth/session; a still-valid cookie keeps the request and it
    // retries once, a dead one ends the session); a token session wires
    // it to session.refresh. It fires for a refused request that
    // presented a credential -- a bearer token, or, with a cookie
    // credential mode, the ambient cookie. The hook is called with the
    // round-scoped request function: reads the round itself performs
    // go through it, so a refusal among them surfaces to the hook
    // instead of joining the round that is awaiting it (a
    // restore-shaped hook reads GET /auth/session and then
    // GET /auth/me). With no session at all the 401 rejects an ApiError
    // with auth: true and hosts route it to sign-in.
    refreshAccessToken: async () => false,
    // Abort requests slower than 10s. Transient retries follow
    // DEFAULT_RETRY_POLICY: idempotent methods only (GET/HEAD/OPTIONS),
    // up to 3 attempts, 200ms doubling backoff capped at 4s.
    timeoutMs: 10_000,
  })

  try {
    return await api<Note[]>('/notes', { query: { page: 1 } })
  } catch (error) {
    if (isApiError(error)) {
      // error.code is the envelope's module code ('notes.…') or a
      // reserved client.* code when the API layer itself failed; map
      // codes to user-facing text through the i18n catalog, never here.
      console.error('loading notes failed', {
        code: error.code,
        trace_id: error.traceId,
      })
    }
    throw error
  }
}
```

## What it does

- **One error type.** Every failed request rejects an `ApiError`. A
  non-2xx with a readable error body surfaces that code plus its
  traceId/params/message/details verbatim: the code is the body's own
  `code` string, or the module code derived from the body's RFC 9457
  problem `type` (`urn:speed:<module>:<code>` -> `<module>.<code>`, each
  dash of the code part normalized to an underscore) -- the shape the
  backend writes a refusal with. `traceId` is optional and surfaced for
  correlation when a backend sends one; no backend sends one, so a body
  carrying a `code` or a `urn:speed:` `type` is a valid error body on its
  own (requiring `traceId` would discard every real module code into the
  `client.` fallback). Responses without a readable error body (a proxy
  error page, a 500 from an upstream that is not JSON, a body with
  neither a `code` nor a `urn:speed:` `type`, a timeout, a dead network)
  get a synthesized code in the reserved `client.` namespace:
  `client.network`, `client.timeout`, `client.protocol` (a 2xx whose
  body is not a JSON object or array -- or an empty 2xx when the
  request declared `requireJsonBody` -- or a request body that cannot
  be JSON-serialized, such as a circular structure or a top-level
  function/symbol, which rejects before anything is sent),
  `client.html` (a 2xx whose content type is an HTML document: an
  entry point that answers an unclaimed path with its front-end
  document, where an API-only deployment answers 404),
  `client.http.<status>`. `error.attempts` reports how many HTTP
  attempts were made. The namespace is reserved by mechanism, not just
  by convention: server codes are module-scoped (`authn.*`, `notes.*`)
  and `client` is no module's domain, so a code -- however it arrived, a
  literal `code` or a `urn:speed:client:*` type -- that starts with
  `client.` is refused at parse time and surfaces as the honest
  `client.http.<status>` instead -- a misbehaving backend or
  intermediary cannot make a session error read as "the request timed
  out". `isTransportFailure(error)` is the exported predicate that
  answers whether an error is genuinely this client's own transport
  failure (request never reached a usable response): its `status` is
  0, the one status no HTTP response can carry, so the answer is not
  forgeable by anything a server answers with.
- **Bearer auth without a storage API.** The token store is a plain
  two-method interface (`get(): string | null`, `set(token: string |
  null): void`); the memory implementation is the only one the package
  ships. The token is re-read before every attempt, so a retry after a
  refresh carries the fresh token. A request can declare
  `omitAccessToken` to skip the store entirely even while it holds a
  token -- the session-refresh operation is generated to do exactly
  that (see `@speed/api-sdk`); the request then carries only its own
  headers, so without a caller-supplied `authorization` it travels
  credential-less, and a caller-supplied one is the caller's own,
  untouched by the store. Tenant context does not travel in the token
  (the access token carries no tenant claim); the client announces it
  as the `X-Tenant` header, read per attempt from
  `ClientOptions.tenantProvider` -- see "Tenant announcement" below.
- **Silent 401 refresh, once per request -- credentialed-only.** When a
  request that *presented a credential* answers 401 and a
  `refreshAccessToken` hook is configured, the client runs one refresh
  -- concurrent 401s share a single in-flight refresh promise, so a
  burst of expired-session requests triggers exactly one refresh --
  and retries the original request exactly once, any method, outside
  the transient-retry budget. A credential is a bearer token from the
  store, or -- when `credentials` declares a cookie mode
  (`'same-origin'`/`'include'`) -- the ambient cookie of a request not
  declared `omitAccessToken`. Refresh failure rejects the original 401
  as an auth `ApiError` and reports `access token refresh failed`
  through the reporter. A 401 on a credential-less request means the
  endpoint demands authentication, which the hook cannot provide, so
  it surfaces untouched -- and that rule is load-bearing for the
  session wiring: a session's own refresh/restore request travels
  credential-less by declaration (`omitAccessToken`, set on the
  generated refresh operation by `@speed/api-sdk`), so a refused
  credential surfaces instead of re-entering the refresh path and
  awaiting itself -- and the store is never cleared to make it so, so
  concurrent requests keep presenting the token they hold throughout.
  A cookie session's credential is the host's business in the same way:
  @speed/auth-core holds nothing (the browser has the cookie) and wires
  the hook to `(issue) => session.restore(issue)`, so a 401 re-reads the
  session; this package only defines the seam. The refresh
  round is a separate exchange: `timeoutMs` bounds each HTTP exchange,
  never the time spent in the refresh hook, so a refresh that outlives
  the timeout cannot degrade the refused 401's own envelope (its code
  and trace id) into a synthetic `client.http.401`.
  **The hook receives the round-scoped request function.** A request
  issued through it belongs to the round, so a 401 among them surfaces
  to the hook instead of joining (or starting) the shared round -- the
  round may be the very one awaiting the hook, which would leave the
  request awaiting itself forever. A restore-shaped hook reads
  GET /auth/session and, after it answers authenticated, GET /auth/me;
  the second read is issued long after the round's synchronous
  invocation, so it must go through the round-scoped function. A hook
  that ignores the argument keeps compiling and running, but one that
  issues its round's reads through the client's ordinary request
  function can self-await when an endpoint refuses.
- **Transient retry, conservatively.** Only idempotent methods
  (GET/HEAD/OPTIONS) are retried, only on 429 (honouring `Retry-After`,
  capped at `maxDelayMs`), 502/503/504, network failures and timeouts.
  Delays are exponential full jitter (`retryDelayMs`), and the budget
  defaults to `DEFAULT_RETRY_POLICY` = 3 attempts / 200ms initial /
  4s ceiling. The budget bounds transient retries; the silent-401
  refresh round performs no transient retry and never consumes one
  (the "retry ... outside the transient-retry budget" wording in the
  refresh bullet above is literal). A response being discarded for a
  retry -- a retryable status, or a refused 401 whose refresh
  succeeded -- has its body cancelled first, so the connection is
  released instead of staying held by an unread response until
  garbage collection. Caller cancellation is never retried and never
  wrapped: aborting your `signal` rejects the raw `AbortError`, so
  query layers (TanStack Query) keep standard cancellation semantics.
  Cancellation and the timeout cover the whole attempt, the response
  body included: the per-attempt timer keeps running and your signal
  keeps aborting until the body has settled, so a server that answers
  headers and then stalls its body rejects with `client.timeout` (or
  the raw `AbortError`) instead of hanging the request on a half-open
  response. An abort during the retry backoff rejects just as
  promptly: the backoff sleep itself races the caller's signal, so a
  cancelled request never sits out its remaining delay.
- **Structured reporting.** The reporter sink receives a constant
  English message plus snake_case attributes. The default sink writes
  to `console.error`/`console.warn` -- the browser has no structured
  log backend, so reports reach whatever sink hosts install; hosts
  replace it through `ClientOptions.reporter`.
- **Language announcement (a transport channel, not a preference).**
  `ClientOptions.languageProvider` supplies the language every request
  announces as `accept-language`:

  ```ts
  const api = createClient({
    baseUrl: '/api/v1',
    // product-shell's bootstrap passes exactly this: the frontend
    // negotiation chain's resolved value, read per attempt.
    languageProvider: () => i18n.language,
  })
  ```

  The provider is read on EVERY attempt, so a request retried after a
  language switch announces the language the user is now looking at --
  the same per-attempt discipline the bearer-token read observes. It
  never overwrites a caller-supplied `accept-language` header, and a
  null/empty answer leaves the header absent. What the header is FOR is
  decided backend-side: the requester-language tiers of content chains
  (an SMS code's language, a contact verification message). API
  responses themselves are never localized -- they stay code+params for
  the client to translate.
- **Tenant announcement (a transport channel, not a claim of the
  token).** `ClientOptions.tenantProvider` supplies the tenant every
  request announces as the `X-Tenant` header:

  ```ts
  const api = createClient({
    baseUrl: '/api/v1',
    // The host returns its current tenant (its routing fact); the value
    // is announced on every request and read per attempt.
    tenantProvider: () => currentTenantId,
  })
  ```

  The tenant is a request-time routing fact, not a claim of the access
  token (the token carries none): the backend's tenant resolution layer
  reads the tenant name off the request surface (a header, a subdomain
  label or a path segment, per its configuration), so a request to a
  tenant-scoped endpoint must carry it -- the skeleton's stacks resolve
  it from this header. The provider is read on EVERY attempt, the same
  per-attempt discipline the bearer-token and language reads observe, so
  a request retried after a tenant switch carries the tenant the user is
  now acting in. It never overwrites a caller-supplied `x-tenant`
  header, and a null/empty answer (or no provider at all) leaves the
  header absent. A host with no tenant-scoped traffic leaves the
  provider out.

## Server-sent events (`openEventStream`)

`createClient` returns a `SpeedClient` -- the request function plus an
`openEventStream(path, options)` member built over the same seams (the
injected fetch, the memory token store, the tenant and language
providers, the single-flight refresh). It opens one `GET`, applies the
seam headers plus `accept: text/event-stream`, refreshes and retries once
on a bearer `401` (as the request loop does), then reads the
`text/event-stream` framing into `ServerSentEvent`s and calls `onEvent`
per frame. Non-2xx answers, transport failures and protocol violations
surface an `ApiError` through `onError`; the caller's own `close()` or an
aborted `signal` fires nothing.

It is deliberately **not** the request function: a streaming response is
not a request/response exchange, so it has no per-exchange timeout and no
idempotent retry budget. It also does **not** reconnect -- a dropped or
ended connection reports once through `onError`/`onClose` and reconnecting
is the caller's policy (so a choice like "pause while the tab is hidden"
lives in the consumer, not here).

```ts
const handle = api.openEventStream('/api/v1/notifications/stream', {
  onEvent: (event) => { if (event.event === 'message') refetch() },
  onError: (error) => reporter.error('notification stream failed', { code: error.code }),
  onClose: () => scheduleReconnect(),
})
// later: handle.close()
```

## Public surface

| Export | Kind | Purpose |
| --- | --- | --- |
| `createClient(options)` | function | Builds the client. Construction validates configuration (baseUrl, fetch availability, retry policy, timeout) and fails fast on mistakes. Returns a `SpeedClient`: the request function with the event-stream opener attached. |
| `RequestFn` | type | `<T>(path, options?) => Promise<T>` -- the returned function; call it as `api<T,>(path)` in TSX files. |
| `SpeedClient` | type | `RequestFn & { openEventStream }` -- what `createClient` returns. Assignable wherever a `RequestFn` is (the streaming member is additive). |
| `EventStreamOpener` | type | `(path, options) => EventStreamHandle` -- opens one server-sent-event stream over the client's seams. |
| `ServerSentEvent` | type | One parsed event: `event` (defaults to `message`), `data`, optional `id`/`retry`. |
| `EventStreamOptions` | type | `onEvent` (required), `onOpen`, `onError`, `onClose`, `signal`. |
| `EventStreamHandle` | type | `{ close() }` -- ends the stream; the caller's own cancellation fires no `onError`/`onClose`. |
| `ApiError` | class | The one error type. `status` (0 when no response arrived), `code` (the error body's own `code`, or the module code the client derives from its RFC 9457 problem `type`), `traceId?`, `params?`, `details?`, `attempts`, `auth` (true exactly for HTTP 401), `message` (an error body's message or an English diagnostic). |
| `isApiError(value)` | function | Type guard; accepts `instanceof` and structurally identical errors (a second copy of the library). |
| `isTransportFailure(value)` | function | Answers whether an error is genuinely this client's own transport-layer failure (`client.network`/`client.timeout` with `status` 0 -- the one status no HTTP response can carry). Consumer whitelists distinguishing "the request never completed" from server-answered errors should consult this, never a bare code match. |
| `ERROR_CODE_NETWORK` / `ERROR_CODE_TIMEOUT` / `ERROR_CODE_PROTOCOL` / `ERROR_CODE_HTML` | const | Reserved `client.` codes; a code that borrows the prefix -- a literal `code` or a `urn:speed:client:*` type -- is refused at parse time. |
| `httpErrorCode(status)` | function | Shapes a bare non-2xx status into `client.http.<status>`. |
| `FieldError` | type | `{ field, code, params? }` -- one field-level validation failure in `details`. |
| `AccessTokenStore` | type | The sync `get`/`set` seam hosts implement (or use the memory store). |
| `createMemoryAccessTokenStore()` | function | The memory-only implementation. |
| `RetryPolicy` / `DEFAULT_RETRY_POLICY` | type / const | `{ maxAttempts, initialDelayMs, maxDelayMs }`; default `{ 3, 200, 4000 }`. |
| `retryDelayMs(attempt, policy, random?)` | function | Pure full-jitter backoff maths (test-friendly: inject `random`). |
| `retryAfterDelayMs(header, now?)` | function | Pure `Retry-After` parser: delta-seconds or HTTP-date, ms delay or null. |
| `Reporter` / `createConsoleReporter()` | type / function | The diagnostics seam and its console-backed default. |

## What is deliberately not here

- **A consumer is not packaged here** -- `@speed/api-sdk`, the
  orval-generated typed surface, calls into this package through its
  `src/runtime.ts` seam, and `@speed/auth-core` compile-consumes both
  in-workspace (its session layer imports this package's
  `AccessTokenStore` seam and calls the generated authn operations
  through the bound request function). `starters/demo/web` binds this
  client at runtime: its app-entry assembly
  (`@speed/product-shell/bootstrap`'s `bootstrapSpeedApp`) binds one
  real `createClient` over the environment's own fetch into the api-sdk
  seam. The package still ships no browser tier of its own.
- **No React surface** -- the package has one entry and no subpaths; it
  imports no react and declares no react peer. Feature and configuration
  reads are the owned module's own discovery surface, consumed through
  the generated operations, not a hook layer here.
- **i18n resources** -- error codes map to bilingual text in the
  consuming package's catalogs; nothing here emits user-facing text.

## Development

From `web/packages/api-client/` (the whole web workspace installs and
tests it too):

```sh
pnpm lint
pnpm typecheck
pnpm test
pnpm build
```

Tests never touch a network: every request goes through a scripted
fetch stand-in (`test-utils/fetch-standin.ts`), and the README Quick
start above is executed verbatim by `src/usage-example.test.ts`.
