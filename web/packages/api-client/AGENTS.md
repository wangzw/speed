# AGENTS.md — @speed/api-client

Guidance for AI tooling working in or against this package. The public
surface, semantics and deferred items are documented in the package
`README.md`; this file records the invariants that keep the package
safe to consume.

## What this module is

The one place in the web workspace where hand-written HTTP happens: a
typed request function built by `createClient`, with injectable fetch,
an optional memory-only access-token store, the fetch credential mode
(for a cookie-delivered session), silent single-flight 401 refresh,
timeout, conservative idempotent retry, and a structured reporter. The
generated `@speed/api-sdk` (orval output of `web/orval.config.ts`) calls into
this runtime through its `src/runtime.ts` binding; no package other than
this one may issue HTTP requests itself.

## Invariants (code review enforces; do not weaken)

- **No storage API exists in this package.** The access token is a
  bearer credential: it lives in memory (the default
  `createMemoryAccessTokenStore`), is re-read before every attempt, and
  never touches `localStorage`, `sessionStorage`, IndexedDB or cookies.
  Do not add a storage-backed token store; the refresh token's home is
  the session layer above this package (the authn API returns it in the
  token-issuing response bodies, never a cookie or this store).
- **The tenant travels as a transport header, read per attempt.**
  `ClientOptions.tenantProvider` (wired by the host -- product-shell's
  bootstrap passes its `SpeedAppDefinition.tenantProvider`) is read on
  every attempt and announced under the configured tenant header when
  set -- `X-Tenant` by default, renamable through
  `ClientOptions.tenantHeader`. The tenant is a
  request-time routing fact, NOT a claim of the access token (the token
  carries no tenant): the backend's tenant resolution layer reads the
  tenant name off the request surface, so a request to a tenant-scoped
  endpoint must carry it, and the skeleton's stacks resolve it from this
  header. The per-attempt read is the point: a retry issued after a
  tenant switch must announce the tenant the user is now acting in, the
  same discipline the bearer-token and language reads observe. Never
  overwrite a caller-supplied header of the configured name; a
  null/empty answer (or no provider) leaves the header absent.
- **fetch is injectable and never implicit at call time.** The client
  captures the environment's global `fetch` at construction (throwing a
  clear error when absent) and `ClientOptions.fetch` overrides it.
  Tests inject deterministic stand-ins (`test-utils/fetch-standin.ts`)
  and never touch a network.
- **The language travels as a transport header, read per attempt.**
  `ClientOptions.languageProvider` (the host's i18n instance via
  product-shell's bootstrap) is read on every attempt and announced as
  `accept-language` when set -- never a response-localization switch
  (the backend keeps answering code+params), and never an override of a
  caller-supplied header. The per-attempt read is the point: a retry
  issued after a language switch must announce the language the user is
  now looking at, the same discipline the bearer-token read observes.
  Null/empty means "nothing to announce" and the header stays absent.
- **Every failure rejects an `ApiError`** -- except caller
  cancellation, which rejects the raw `AbortError` (never retried,
  never wrapped). A non-2xx response with a readable error body surfaces
  its code -- the body's own `code` string, or the code derived from its
  RFC 9457 problem `type` (`urn:speed:<module>:<code>` ->
  `<module>.<code>`, each dash of the code part normalized to an
  underscore) -- plus its traceId/params/message/details whenever the
  backend sent them (`parseEnvelope` must never demand `traceId`, which
  no backend sends); a body with neither a `code` nor a `urn:speed:`
  `type` synthesizes a reserved `client.*` code (`client.network`,
  `client.timeout`, `client.protocol`, `client.html`,
  `client.http.<status>`). The
  reservation is a parse-time mechanism, not a convention: server codes
  are module-scoped and `client` is no module's domain, so
  `parseEnvelope` refuses any code -- however it arrived, a literal
  `code` or a `urn:speed:client:*` type -- starting with `client.`; a
  backend or intermediary cannot forge an envelope that reads as this
  client's own transport diagnosis. `isTransportFailure` is the exported
  predicate consumers use to distinguish this client's genuine transport
  failures (`status` 0 -- unanswerable by any HTTP response) from
  server-answered errors.
- **Refresh is once per request, single-flight -- and credentialed-only.**
  The hook fires only for a refused request that itself presented a
  credential: a bearer token from the store, or -- when
  `ClientOptions.credentials` declares a cookie mode
  (`'same-origin'`/`'include'`) -- the ambient cookie of a request not
  declared `omitAccessToken`. A 401 on a credential-less request means
  the endpoint demands authentication that the hook cannot provide, so it
  surfaces untouched (never retried, no false `refresh failed`
  warning) -- which is what keeps a session's own refresh/restore request
  from re-entering the refresh path and awaiting itself. A 401 with a
  configured hook triggers one refresh shared by concurrent 401s, then
  one retry of the original request (any method), outside the
  transient-retry budget. Hook failure reports `access token refresh
  failed` and rejects the original 401 as an auth `ApiError`.
- **A refresh hook is called with the round-scoped request function,
  and requests issued through it are the round's own.** Their 401
  surfaces to the hook instead of joining or starting the shared round
  -- the round may be the very one awaiting the hook, so a joining
  request would await itself forever. This is what a restore-shaped
  hook needs: it reads GET /api/v1/auth/session and then, after that answers
  authenticated, GET /api/v1/auth/me, which is issued after an await and is
  therefore beyond the synchronous-invocation marker the 401 guard also
  honours. A hook that ignores the argument still runs; one that issues
  its round's reads through the client's ordinary request function can
  self-await when an endpoint refuses.
- **Credential-less-ness is declared per request, never manufactured
  by clearing the store.** `RequestOptions.omitAccessToken` skips the
  store read entirely, so no store token is attached and the request
  carries only its own headers -- without a caller-supplied
  `authorization` that means it travels without an Authorization
  header. The session-refresh operation is generated to carry the
  declaration (orval's `speedRequestCredentialless` mutator in
  @speed/api-sdk). Clearing the store instead would momentarily strip
  the token from concurrent requests that still hold a valid one,
  turning their 401s into spurious auth failures under the
  credentialed-only rule above; do not reintroduce a store-clearing
  refresh wiring. The
  caller's own headers are never stripped by the declaration: a
  caller-supplied `authorization` header is the caller's, and survives.
- **Retry is idempotent-only and transient-only.** GET/HEAD/OPTIONS on
  429 (honouring Retry-After, capped by the policy) / 502 / 503 / 504 /
  network failure / timeout. Full-jitter backoff via `retryDelayMs`;
  the frozen `DEFAULT_RETRY_POLICY` is the default. The budget and
  timing are pure functions (`retryDelayMs`, `retryAfterDelayMs`) --
  keep them pure.
- **No user-facing text, no i18n resources.** Report messages are
  constant English strings with snake_case attributes; error `code`s
  are data that consuming packages map to bilingual text in their own
  catalogs.
- **The `speed/no-direct-http` rule whitelists this package.** New
  HTTP-touching code belongs here; keep the whitelist single.
- **The event-stream opener is a member of the client, not a second
  transport.** `createClient` returns a `SpeedClient` (a `RequestFn` plus
  `openEventStream`); the opener is built inside the same closure so it
  reads the identical seams (fetch, token store, tenant and language
  providers with the configured tenant header, the fetch credential
  mode, the single-flight refresh). Do not build a second wiring of
  those seams, and do not fold streaming back into the request function:
  a stream has no per-exchange timeout and no idempotent retry budget.
  The opener applies the seams per connection, forwards the client's
  `credentials` mode on its `GET`, refreshes and retries once
  on a bearer `401`, parses the `text/event-stream` framing, and -- by
  contract -- neither times out nor reconnects; reconnecting is the
  caller's policy.
- **The package is React-free.** It has one entry, imports no react,
  declares no react peer and adds no subpath. Feature and configuration
  reads belong to the module that owns them, consumed through the
  generated operations; do not add a hook layer here.

## What ships vs. what is deferred

The runtime above (client, errors, retry, reporter, token store) plus
the `speed/no-direct-http` ESLint rule that routes all other package
HTTP through it. The package has no config-fetching surface and no
React surface of its own.

The runtime leg is real in-workspace: `@speed/api-sdk`, the
orval-generated typed surface, calls into this runtime through its
`src/runtime.ts` binding, and `@speed/auth-core` compile-consumes both
(its session layer imports this package's `AccessTokenStore` interface
for the opt-in token path, calls the generated authn operations through
the bound request function, and fills the `refreshAccessToken` hook --
`(issue) => session.restore(issue)` on the cookie path, `() => session.refresh()`
on the token one).
`starters/demo/web` binds this client at runtime: its app-entry assembly
(`@speed/product-shell/bootstrap`'s `bootstrapSpeedApp`) binds one real
`createClient` over the environment's own fetch. The package ships no
browser-tier suite of its own.

## Transports

- Binary bodies and raw-byte responses travel the request function (a
  `Blob`/`ArrayBuffer`/typed array body goes verbatim; a request declaring
  `responseType: 'blob'` reads a 2xx body as a `Blob`), and server-sent event
  streams open through the separate `SpeedClient.openEventStream` member -- a
  stream is not a request/response exchange, so it does not ride the JSON
  request function with its per-exchange timeout and idempotent retry budget.

## Known limitations

- **No backend sends `traceId`, so an `ApiError` never carries one
  against the real API.** A refusable body's code comes from its own
  `code` string or from the RFC 9457 problem `type` every module's error
  writer actually writes (`urn:speed:<module>:<code>`); `traceId` is
  optional (parsed and surfaced when present, omitted from reporter
  attributes when absent), so those bodies keep their codes end to end.
  `client.test.ts`'s `{code, params}` envelope test pins the literal
  wire shape. The correlation half -- a backend-wide change for every module's
  error writer to emit a real `traceId` (sourced from request tracing), so
  user reports can be tied to server logs -- is not implemented.

## Public surface

The runtime exports are pinned by `src/index.test.ts`
(`ApiError`, `DEFAULT_RETRY_POLICY`,
`ERROR_CODE_HTML`, `ERROR_CODE_NETWORK`, `ERROR_CODE_PROTOCOL`,
`ERROR_CODE_TIMEOUT`, `createClient`, `createConsoleReporter`,
`createMemoryAccessTokenStore`, `httpErrorCode`, `isApiError`,
`isTransportFailure`, `retryAfterDelayMs`, `retryDelayMs`), with
compile-time shape-drift
guards for the type exports (`RequestFn`, `ClientOptions`,
`RequestOptions`, `AccessTokenStore`, `RetryPolicy`, `Reporter`,
`FieldError`, `HttpMethod`, `ApiErrorInit`). See the README's
public-surface table for semantics. Removing or renaming an export
breaks the pin tests and the typecheck; extend the surface
deliberately, with the README table updated in the same commit.

## Development

From this directory (`web/packages/api-client/`), or workspace-wide
from `web/` with `pnpm -r`:

```sh
pnpm lint        # eslint; the speed/no-direct-http whitelist covers this package
pnpm typecheck   # strict; relative imports in src/ carry .js extensions (nodenext)
pnpm test        # vitest; colocated unit tests plus test-utils/ helpers
pnpm build       # tsc ESM build (nodenext); dist/ is gitignored build output
```

Test layout: one file per source file (`errors.ts` -> `errors.test.ts`)
plus a behavior file (`usage-example.test.ts` executes the README Quick
start against a stubbed global fetch). Shared helpers live in
`test-utils/` (`fetch-standin.ts` scripted responders, abort-aware the
way real fetch is; `memory-reporter.ts` capture sinks). Tests run in
the default `node` environment and never require Docker or a network.
