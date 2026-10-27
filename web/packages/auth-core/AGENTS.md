# AGENTS.md — @speed/auth-core

Guidance for AI tooling (and humans) working in this package. It ships
with the package to consuming projects; the discipline here is the
module-boundary rules from the workspace CLAUDE.md applied to one
browser package.

## What this package is

The session lifecycle of a browser client: `createAuthSession(store?)`
in `src/session.ts` turns the generated authn operations of
`@speed/api-sdk` into one observable state machine -- the login flow
(start, answer, current, abandon), the authenticated-purpose start for
`link`, `step-up` and `enroll`, the anonymous registration operation, the
methods listing, the session read `restore()` makes, logout, and the
token endpoint a token-delivered refresh uses. A login is a server-side
flow the view drives step by step; the flow operations never change the
session, and only `answerFlow` commits, and only when a step settles the
session. `register` is not a login: it opens an account in one request
and changes nothing in the session.

The session is delivered as a cookie by default. `startFlow` is always
started as a cookie session (`delivery: 'session'`), so a completion
answers `{state: 'authenticated', session: {id, expires_at}}`; the server
sets its httpOnly cookie, the browser attaches it, and `restore()` reads
`GET /api/v1/auth/session` to re-establish the snapshot after a reload (reading
`GET /api/v1/auth/me` for the principal). A store-less session holds no
credential at all. A host that passes an `AccessTokenStore` keeps the
token path: a `delivery: 'token'` login completion (state
'authenticated') writes the access token into it and holds the minted
refresh token in the closure, and a token step-up (state 'elevated')
re-mints the access token with the principal unchanged; a
`link`/`enroll` completion (`state: 'linked'`/`'enrolled'`) leaves the
login state alone either way.

The generated authn fragments declare their 2xx response schemas, so
orval emits the response models. This package's `AuthnFlowState`,
`AuthnStep`, `AuthnField`, `AuthnFactorOption`, `AuthnPrincipal` and
`AuthnMethodList` are those generated types (or a client-side narrowing
of them), and it guards each body at runtime (see the rules below).

It is headless: no UI, and the main entry touches no DOM. The one
exception is the `./webauthn` subpath (`src/webauthn.ts`), which drives a
passkey ceremony through `navigator.credentials` and is deliberately not
exported from the main entry, so a host that only reads the session never
loads it. The React hooks in `src/hooks.ts`
(`useAuthState`, `usePermission`, `useFeature`, `useFeatureState`,
`featurePresence`, plus the `attachSession` call that
binds them to one session, last bind wins) live on the same layer --
react is a peer dependency, never a regular one. The tenant is not the
session's: the server resolves it per request and the principal carries
none, so the host mounts its own current tenant on `TenantProvider` and
`useCurrentTenant` reads it back. The tenant-scoped query-key convention
in `src/query-key.ts` (`tenantQueryKey`, `useTenantQueryKey`,
`evictTenantQueries`) sits on the same layer too: it reads the tenant
through `useCurrentTenant` and shapes a TanStack Query key from it, with
`@tanstack/react-query` as a peer (the factory calls `removeQueries`,
never constructs a `QueryClient`). The human-verification client in
`src/human.ts` reads the deployment's gate from the methods document
(a host may pass a gate it already read), holds the single-use proof a
verification-message send presents, and solves a proof-of-work challenge
off the main thread through the worker in `src/pow-worker*.ts` (falling
back to the solver in `src/pow.ts` where no `Worker` exists). A methods
document that omits the gate is read as the off mechanism, and a caller
that holds an inactive surface passes `enabled: false` so nothing is read
or solved until it opens. The step-up hook in `src/step-up.ts` exposes
the gate and proof it obtains as `StepUpState.human` and presents the
proof on an answer that sends a verification message. The package sits directly above
`@speed/api-client` (an optional access-token store it receives is the
same store that package reads on every send for a token session; the
cookie path passes none) and `@speed/api-sdk` (the generated operations
it calls). Never import either of those from below this layer.

## Rules that are load-bearing here

- **Never hand-write a backend call in this package.** Every request
  goes through a generated operation of `@speed/api-sdk`, which routes
  through the single `bindRequestFn` binding. No `fetch`, no endpoint
  paths as strings. Response shapes are not hand-written either: the
  fragments declare them, the generated surface emits them, and this
  package re-exports the generated types rather than a parallel copy.
  The one client-side derivation is the flow union: the generated step
  is flat, so `AuthnStep`/`AuthnFlowState` narrow it by `kind`/`state`.
- **Every body-carrying 2xx is guarded, fail-closed, before any state
  change.** The generated seam sets no `requireJsonBody`, so an empty
  2xx resolves as undefined through it; a guard must refuse it (and any
  malformed body) as a `status: 200`, `code: client.protocol` ApiError
  *before* the first property access -- a native `TypeError` is not an
  ApiError and would escape the whole error contract. `parseFlowState`,
  `parseStep`, `parsePrincipal`, `parseSessionView`, `parseIssuedTokens`,
  `parseMethodList`, `parseMethodDescriptor`, `parseSsoProviders`,
  `parseRegistration` and `parseFeatureDocument` are the guards; every one
  of their refusal branches is pinned by a test. `parseRegistration`
  additionally requires the literal outcomes the contract promises --
  `state: 'registered'` and a `verification` whose state is `pending` --
  so a body that reports anything else is never treated as an opened
  account.
- **The session never writes storage, and (on the cookie path) holds no
  credential at all.** The credential is the server's httpOnly session
  cookie, which the browser manages and scripts cannot read; this package
  reaches it only through @speed/api-client's credential mode. On the
  opt-in token path the access token is a memory cell and the refresh
  token is closure-only; any persistence a host adds layers on top of
  this API, it does not reach into it.
- **A failed request rejects raw and changes nothing -- except
  `logout`.** `ApiError` from `@speed/api-client` is the one error type
  a caller must tell apart (`isApiError`), and `client.protocol` is
  reserved for contract-violating 2xx. `logout` is the deliberate
  exception: a logout whose own request is refused still clears locally
  and goes anonymous (a session it could not revoke is no longer
  trustworthy here either) and then rejects raw.
- **A session-establishing commit that lost a concurrent race rejects with
  `OperationSupersededError`.** A login completion or a step-up
  completion that lost the generation race retains the package's own
  `isOperationSuperseded` guard (its `snapshot` carries the winner's
  state). Consumers must handle it as the lost race it is -- no failure
  UI, and never a one-shot side effect (a redirect) for an identity the
  session is not actually running under; the winning call fires its own.
  A superseded token step-up carries a cost its caller must hear: its
  completion consumed, server-side, the challenge it verified, so the
  caller must never offer that same second factor again -- only a fresh
  flow.
- **`restore()` and `refresh()` are the silent paths.** `restore()` (the
  cookie session) resolves `true` when the cookie still names a session,
  `false` (never rejects) when nobody is signed in -- a snapshot that was
  authenticated goes anonymous, and a 401 from the read is treated the
  same -- and rethrows raw only for a transport/server failure with the
  local snapshot in place. While an identity transition is open (the next
  rule), or for a restore already in flight when one opened or issued
  while one was open (whenever it settles), an anonymous answer is
  discarded instead: `false`, nothing changed. Its optional
  `issue` argument is the
  round-scoped request function `@speed/api-client` hands the
  `refreshAccessToken` hook: both reads then travel through it, so a
  refusal among them surfaces instead of joining the refresh round that
  is awaiting the call (the profile read follows an await, where the
  client's synchronous-invocation marker no longer covers it). `refresh()` (the token session) resolves
  `false` (never rejects) when there is nothing to refresh, when the held
  token was refused (the session signs out locally), or when a concurrent
  commit replaced the session; it rethrows raw only for transport/server
  failures, with the store and the held tokens untouched. Refusal
  detection keys on a 4xx (`400` invalid_grant / `401` invalid_client)
  or a contract-violating 2xx -- the token endpoint answers an unusable
  grant as `400`, not `401`. A cookie session holds no refresh token, so
  `refresh()` resolves `false` there without a request.
- **An identity transition suppresses an anonymous restore answer.** A
  host that replaces the credential the session rides -- an impersonation
  start rotates the session cookie -- opens
  `beginIdentityTransition()` before the write and calls the returned
  release once the new identity has settled (or at once when the write
  does not land). While the window is open, and for a restore already in
  flight when it opened or issued while one was open (whenever it
  settles), an anonymous answer resolves `false` and changes nothing: it
  describes the credential the transition is replacing, so
  the refused request keeps its 401 and no spurious sign-out is
  committed. The next read after the release settles the true state, a
  genuine expiry included. An authenticated answer still commits, which
  is what lets the host's own re-read under the new credential land
  mid-window. Releases are counted and idempotent. This is not the
  generation guard: a rotation is not a new session, and `restore` still
  never bumps it -- do not fold the two together.
- **The generation guard is the concurrency invariant.** A
  session-establishing commit captures the generation at entry and bumps
  it only on commit; `restore` and `refresh` never bump, and a stale
  answer applies nothing over the winner's state -- one adoption excepted:
  the rotated refresh token still lands in the held slot when the winning
  commit kept the held token (a token step-up mints no new one, and the
  server has already consumed the held token for that refresh). The
  adopted refresh's resolution reflects who won: a same-principal winner
  resolves `true`, so the api-client silent-401 hook retries safely. Do
  not "simplify" this into bump-on-entry or compare-free writes. The
  local clears are asymmetric on purpose: only logout bumps after its
  clear (a restore or refresh that resolves later must not resurrect the
  ended session), while the restore/refresh-side clears never bump -- the
  server's answer is the session's death, but a commit that started
  before it and settles after it is the session's legitimate successor
  and must still commit. Logout's own pre-emption is handled too: a commit
  that replaced the held token (a different login) is a session the
  revocation never touched and stands, while a commit that kept it (a
  token step-up) continues the revoked session and the local state
  converges to signed-out; a cookie session holds no token, so any commit
  that landed while the logout was in flight is a different session and
  stands.
- **The permission sets are host-attached data with survival rules,
  never evaluation.** The session only carries the per-domain lists
  (`setPermissionSet`) and applies the rules in its header: a silent
  restore or refresh or a step-up keeps both lists, `link`/`enroll` keep
  them (they do not change the login state), and a login -- even by the
  same account -- or an anonymous transition clears both, and a failed
  operation changes nothing. Set membership is decided by the hooks and
  the shells, never here -- and never fetched from a server here either:
  the /me-derived lists belong to the host to attach. Because a login
  clears what must not survive, the host discipline is to refetch a
  domain's lists after a login instead of reusing lists fetched under an
  earlier session: the session never correlates a list with the
  principal it was fetched under, so attaching a stale list is a host
  bug it cannot detect.
- **The feature state is a closed set with the permission sets'
  survival rules and its own commit check.** The snapshot carries
  `FeatureState` (`none`, `pending`, `failed`, `ready` with the served
  names); the session never interprets a name. A login completion and an
  anonymous-start restore read `GET /api/v1/features` beside `GET /api/v1/auth/me` and
  commit both; a failed feature read commits `failed` (with the error)
  rather than failing the login, because a failed read is neither every
  feature absent nor every feature present. A same-session refresh,
  step-up, `link`/`enroll` keeps the state without a request, an
  anonymous transition resets it to `none`. `retryFeatures()` is the one
  in-session writer: it turns the state `pending`, reads the document,
  and applies the result only while the captured generation, the
  authenticated snapshot and the principal account all still hold --
  the generation check alone is not enough because the restore/refresh
  clears do not bump it. Its single flight is keyed on that
  generation/account and is registered before `pending` is published, so
  a listener re-entering on `pending` shares the read and a read that
  outlived its session is never handed to the next one.
- **`impersonating` is a snapshot fact only `restore()` writes.** It
  mirrors `GET /api/v1/auth/session`'s same-named field; a missing field reads
  false and only a boolean value is accepted. Login completion, logout
  and every local clear set it false; a same-session refresh, step-up,
  `link`/`enroll` and `retryFeatures` keep it unchanged. The snapshot
  carries no initiator identity -- the host acts as the account in front
  of it -- and a host that changes identity calls `restore()` itself once
  its write settles.
- **The hooks read the attached session and never drive it.** A
  component that must sign in calls `session.startFlow` /
  `session.answerFlow` from an event handler; hooks never mutate state,
  never fetch, and fail closed (anonymous snapshot, `false` permissions,
  a feature answer only for `ready` + membership) before
  `attachSession` has been called.
- **The tenant comes from the host, read back through
  `TenantProvider`.** `useCurrentTenant` never derives a tenant from the
  principal (the principal has none) and never remembers a previous one;
  a null tenant reads back as null, and a read built on it passes
  `enabled: tenantId !== null` to fail closed rather than invent one.
- **The tenant-scoped query-key convention lives here, once.**
  `tenantQueryKey(tenantId, ...tail)` prepends the tenant id as a key's
  outermost segment, `useTenantQueryKey` derives the key and its
  `enabled` operand from one hook call, and `evictTenantQueries` removes
  a departing tenant's whole subtree -- called with the tenant id read
  *before* the change, since the new id names nothing. Do not build the
  key shape or the eviction anywhere else: a second copy is exactly the
  repetition this convention removes (ADR-web-tenant-query-key),
  and getting it wrong -- treating a key as cross-tenant stable, or
  missing a prefix -- leaves one tenant's rows under another's. The
  factory only shapes a key: it fetches nothing, evaluates nothing,
  decides nothing. `@speed/api-sdk`'s generated keys stay tenant-less and
  `@speed/api-client` stays tenant-ignorant; `@tanstack/react-query` is a
  peer here -- the factory calls `removeQueries`, never constructs a
  `QueryClient`.
- **Refresh is single-flight per held refresh token.** Presentations of
  the same held token share one in-flight request; a second parallel
  presentation of it reads as token theft to the authn server, so the
  session serialises them itself. A call made after a token step-up
  presents the same held token and still shares that flight.
- **A token host's refresh request travels credential-less by
  declaration.** The generated token operation is selected to carry
  `omitAccessToken` (the `web/orval.config.ts` per-operation override
  names the current operation id, `authn.token.issue`), and `runRefresh`
  never reads, clears or restores the token store -- there is nothing to
  restore on a rethrowable failure. Keep it that way: a clear-and-restore
  ordering would re-arm the api-client bearer-only refresh rule against
  the session's own refresh request. Note that the checked-in generated
  surface must be regenerated for the override to take effect.

## Public surface

`src/index.ts` exports `createAuthSession(store?)`; the step-up gate
(`useStepUpGate(onRetry, isShortfall?)` and its `StepUpGate` type) and the
tenant-refusal predicate (`isTenantAdmissionRefusal(error)`); the session
types
(`AuthSession`, `AuthSnapshot`, `FeatureState`, `AuthSessionListener`,
plus `AuthDomain` and `AuthPermissionSets`, the two names that
parameterise the host-attached permission lists); the response models
(`AuthnPrincipal`, `AuthnFlowState`, `AuthnStep`, `AuthnField`,
`AuthnFactorOption`, `AuthnMethodList`, `AuthnIdentifier`, `AuthnLink`,
`AuthnSessionView`, `AuthnLoginRegisterBody`, `AuthnLoginRegistration`);
the supersession rejection pair
(`OperationSupersededError` and its `isOperationSuperseded` guard -- the
one non-`ApiError` rejection of the session-establishing commits); the
hooks (`attachSession`, `useAuthState`, `usePermission`, `useFeature`,
`useFeatureState`, `featurePresence`, `TenantProvider`,
`useCurrentTenant`); and the
tenant-scoped query-key convention (`tenantQueryKey`,
`useTenantQueryKey`, `TenantQueryKey`, `evictTenantQueries`); and the
human-verification client (`useHumanVerification(session, options?)`,
its `HumanVerification` / `HumanVerificationStatus` /
`UseHumanVerificationOptions` types,
`humanGateNeedsProof`, `requestPowProof`, `powProofFromChallenge`, and
the solver's `solvePowChallenge` / `POW_HASH`). The
`./webauthn` subpath exports its own surface (`runWebauthn(options,
signal?)`, `webauthnStepOf(options)`, `WebauthnError`, `WebauthnStep`,
`WebauthnFailure`); those names are not on the main entry.
`AuthSession` also carries `beginIdentityTransition()`, the
credential-rotation guard documented above. Anything
else that grows here stays unexported until a consumer proves it needs
to be public.

## Testing

- `src/session.test.ts` (the state machine, plain node environment, no
  DOM), `src/hooks.test.ts` (the React bindings and the tenant context,
  per-file jsdom pragma, explicit `afterEach(cleanup)` -- vitest runs
  without globals here, which disables @testing-library/react's
  auto-cleanup) and `src/query-key.test.tsx` (the tenant-scoped key
  factory, the switch-time eviction over a real QueryClient -- a client
  holds two tenants' subtrees plus an identity row, and after the
  departing tenant's eviction exactly that subtree is gone -- and the
  hook's key-and-enablement over a host-mounted TenantProvider) are the
  behaviour suites. `src/step-up-gate.test.ts` pins the gate's arm /
  cancel / replay-once contract and both shortfall classifiers;
  `src/tenant-refusal.test.ts` pins the admission-refusal predicate
  against module-coded 404s; `src/pow.test.ts` pins the SHA-256 vectors
  and checks that the solver's proof hashes to the difficulty the
  provider requires; `src/human.test.ts` (jsdom) pins the
  challenge-to-proof path and the hook's discovery, off fallback,
  Turnstile token and fresh-proof-after-consume states;
  `src/webauthn.test.ts` (jsdom, with
  `navigator.credentials` stubbed) pins the ceremony driver's decoding,
  serialization and failure classification. `src/usage-example.test.tsx` compiles and runs the
  README's session-and-hooks flow and its tenant-scoped query-key flow
  through the same scripted harness (the real `createClient` composition
  is proven in session.test.ts), so the documented usage cannot drift
  from the API. All of them drive sessions through the shared scripted
  harness in `@speed/test-utils/session-harness` -- tests never touch a
  real server and never mock package internals; they drive the exported
  API and assert observable state (store contents, snapshots,
  notification order, request bodies).
- The failure contract, the response guards (every refusal branch of
  every parser, the registration's literal-outcome guard included), the
  generation guard (a failed completion leaves an
  in-flight refresh intact; a logout cannot be resurrected by a refresh;
  a newer login is never overwritten; a pre-empted logout converges or
  stands aside), single-flight refresh, the identity-transition guard
  (an anonymous answer during the window leaves the snapshot
  authenticated and the refused request's 401 stands; a restore in
  flight when the window opened is discarded too, as is one issued while
  the window was open when it settles after the release; the next read after
  the release settles the real state; a transport failure still
  rethrows), the refresh refusal detection
  and the real-client composition (silent-401 refresh through
  `createClient`) each have dedicated tests. The hooks' fail-closed reads
  before attach, the tenant read-back from `TenantProvider`, the
  permission selectors over a scripted session, domain separation, the
  permission-set survival rules and the attach rebinding semantics have
  theirs in the hooks and query-key suites. `src/features.test.ts` pins
  the feature state: the login/restore reads, the failed-read rule, the
  `pending` transition, the retry's commit check (a read that outlived a
  server-ended clear is discarded), the pending-marker restore after a
  superseding step-up, the keyed single flight (a listener re-entering on
  `pending` shares the read; a new session does not inherit an old
  flight) and the compile-time name check. A change to any of those
  behaviours must come with a test that pins the new behaviour and fails
  on the old.
- No test file may import from another package's `dist/`; the vitest
  aliases map the `@speed/*` specifiers onto sibling sources.

## Known limitations

- The token path is opt-in. The default (and the platform's own
  composition) is the cookie session: `createAuthSession()` with no
  store, `credentials` on the client and `restore()` at boot -- a reload
  stays logged in. A host that passes an `AccessTokenStore` gets the
  token path instead, where the refresh token is JavaScript-visible in
  memory (the authn token responses set no refresh cookie to hide it in)
  and a reload starts anonymous unless the host restores it itself. The
  host must also start the login flow itself with the generated
  `authnLoginStart({ delivery: 'token' })` and hand its answers to
  `answerFlow`: `startFlow` always forces the cookie delivery.
- The browser cookie path requires the frontend and the API to be
  same-site. The session cookie is `SameSite=Lax` (or `Strict`), and the
  server refuses `None`; a cross-site frontend cannot send it.
- The `delivery` a view passes to `startFlow` is normalized to the cookie
  session; a view that still names a token delivery is overridden.
  `startAuthenticatedFlow` leaves the delivery to the caller, whose
  callers pass none (the server resolves its default, a cookie session).
- A token host's refresh request's credential-less declaration lives in
  the generated surface: the `web/orval.config.ts` override names the
  current operation id (`authn.token.issue`), but a checked-in generated
  `api-sdk/src/index.ts` produced before that fixup still routes the
  token operation through the bearer mutator. Regenerating applies it.
