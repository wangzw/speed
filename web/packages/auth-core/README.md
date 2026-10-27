# @speed/auth-core

The browser session lifecycle as a headless state machine over the
generated authn flow surface of `@speed/api-sdk`, carried by the
server's session cookie. `createAuthSession` wires the generated
operations -- the login flow (start, answer, current, abandon), the
authenticated-purpose start for `link`, `step-up` and `enroll`, the
anonymous registration operation, the methods listing, the session read a
reload restores from, logout, and the token endpoint a token-delivered
refresh uses -- to one observable session.

The session is delivered as a cookie. The login flow is started with
`delivery: 'session'`, so a completion answers `{state: 'authenticated',
session: {id, expires_at}}` and the server sets its httpOnly cookie on
the same response, which the browser attaches to every same-origin
request by itself. A store-less session -- the platform's own
composition -- therefore holds no credential at all; `restore()` reads
`GET /api/v1/auth/session` to re-establish the snapshot, which is what keeps a
page reload logged in and what ends the local session when the cookie no
longer holds. A host that passes an `AccessTokenStore` keeps the token
path instead: a `delivery: 'token'` completion writes the access token
into that store (the same store `@speed/api-client` reads on every send)
and holds the minted refresh token only inside the session closure,
refreshed through `refresh()`. No storage is written either way. The
token path starts outside this package: `session.startFlow` always starts
a cookie session, so a token host calls the generated
`authnLoginStart({ delivery: 'token' })` operation itself and hands each
answer to `session.answerFlow` -- see "The token path". React hooks
(`useAuthState`, `useCurrentTenant`, `usePermission`) read one session the
host attaches with `attachSession`; react is a peer dependency, so only a
host that already renders React carries it.

The cookie this package relies on is same-site: `SameSite=Lax` (an
operator may tighten it to `Strict`; `None` is refused). In a browser
deployment the frontend and the API must therefore be same-site. A
cross-site frontend cannot carry the session cookie on its requests.

The deployment may also put a human-verification gate in front of every
verification-message send. `useHumanVerification(session)` reads the gate
from the methods document (a host that already read the document passes
it as `options.gate`) and holds the single-use proof a send presents.
For the `pow` mechanism it asks for a challenge and solves it off the
main thread, falling back to the calling thread where no `Worker`
exists; for `turnstile` the surface adopts the widget's token. The proof
is consumed after every send, so the next send obtains a fresh one. A
methods document that omits the gate is read as the off mechanism, so a
newer client keeps working against an older server.

The generated authn fragments declare their 2xx response schemas, so
orval emits the response models. This package re-exports those generated
types and guards each body at runtime, refusing a contract-violating 2xx
as a `client.protocol` error -- see "The response models and their
guards".

## What ships

| File | Exports |
|---|---|
| `session.ts` | `createAuthSession(store?)`, `AuthSession`, `AuthSnapshot`, `FeatureState`, `AuthSessionListener`, `AuthDomain`, `AuthPermissionSets`, `AuthnPrincipal`, `AuthnFlowState`, `AuthnStep`, `AuthnField`, `AuthnFactorOption`, `AuthnMethodList`, `AuthnIdentifier`, `AuthnLink`, `AuthnLoginRegisterBody`, `AuthnLoginRegistration`, `PasswordResetChannel`, `OperationSupersededError`, `isOperationSuperseded` |
| `hooks.ts` | `attachSession(session)`, `useAuthState()`, `usePermission(domain, permission)`, `useFeature(name)`, `useFeatureState()`, `featurePresence(state)`, `TenantProvider`, `useCurrentTenant()` |
| `query-key.ts` | `tenantQueryKey(tenantId, ...tail)`, `useTenantQueryKey(bareKey)`, `TenantQueryKey`, `evictTenantQueries(queryClient, tenantId)` |
| `step-up-gate.ts` | `useStepUpGate(onRetry, isShortfall?)`, `StepUpGate` |
| `human.ts` | `useHumanVerification(session, options?)`, `HumanVerification`, `HumanVerificationStatus`, `UseHumanVerificationOptions`, `humanGateNeedsProof(gate)`, `requestPowProof(requestChallenge?)`, `powProofFromChallenge(answer)` |
| `pow.ts` | `solvePowChallenge(challenge, difficulty)`, `POW_HASH` |
| `tenant-refusal.ts` | `isTenantAdmissionRefusal(error)` |
| `webauthn.ts` (the `@speed/auth-core/webauthn` subpath, not the main entry) | `runWebauthn(options, signal?)`, `webauthnStepOf(options)`, `WebauthnError`, `WebauthnStep`, `WebauthnFailure` |

`src/index.ts` re-exports everything above except `webauthn.ts`; that file
is the one DOM-touching module in the package (it calls
`navigator.credentials`) and ships as its own subpath so a host that only
reads the session never loads it.

## Quick start

```ts
import { createClient } from '@speed/api-client'
import { bindRequestFn } from '@speed/api-sdk/runtime'
import { createAuthSession } from '@speed/auth-core'

// The cookie path: no store, the session rides the browser's cookie.
const session = createAuthSession()

bindRequestFn(
  createClient({
    baseUrl: window.location.origin,
    credentials: 'same-origin', // send the session cookie; make a 401 run restore()
    refreshAccessToken: (issue) => session.restore(issue),
  }),
)

// subscribe to state transitions once, at app bootstrap:
session.subscribe((snapshot) => render(snapshot))

// restore the session the cookie names, once, so a reload stays logged in:
void session.restore()

// render paths read the current snapshot:
const snapshot = session.getSnapshot() // { state: 'anonymous', principal: null, ... }
```

## Driving a login flow

A password (or SMS, or social) sign-in is a server-side flow the view
drives step by step. `startFlow` begins it and returns the first step;
the view dispatches on `next.kind`; `answerFlow` advances it and returns
the next step. When the flow reaches a completion that issued tokens
(`state: 'authenticated'` for a login, `state: 'elevated'` for a
step-up) the session commits -- writes the store, reads `GET /api/v1/auth/me`
for a login's principal, and notifies subscribers. Every other step
(live steps, `link`, `enroll`) changes nothing.

```ts
// Start a login flow for one method; the reply is the flow's first step.
const step = await session.startFlow({ method: 'password', purpose: 'login' })

// Answer the current step with the fields it asked for. A challenge
// step names its fields; a completion commits the session.
if (step.next.kind === 'challenge') {
  const done = await session.answerFlow({
    seq: 1,
    step: 'answer',
    input: { identifier: 'ada@example.com', password: 'pw' },
  })
  // done.state === 'authenticated' -> the session is signed in.
}

// After a page refresh, resume the flow the request is bound to:
const current = await session.readFlow() // null when there is no flow
// Abandon it (idempotent):
await session.abandonFlow()

// End the session:
await session.logout()
```

On the identity already signed in, `startAuthenticatedFlow` starts a
flow for `purpose: 'link'`, `'step-up'` or `'enroll'` on the same
machine. A `'step-up'` that completes keeps the principal and the
permission sets (the same session continues: the server rotated the
cookie for a cookie session, or minted a fresh access token for a token
one), and `link`/`enroll` completions leave the login state alone:

```ts
// Enroll a second factor: start with purpose 'enroll', drive the steps.
const enrollment = await session.startAuthenticatedFlow({ purpose: 'enroll' })
```

## Anonymous registration

Registration is not a login. `session.register` opens one account with a
single `POST /api/v1/auth/register` request and answers the generated
registration model: the literal `state: 'registered'`, the new
`account_id`, and the email address awaiting verification. It
establishes no session, writes no credential and no storage, and leaves
the snapshot, the permission sets and the feature state untouched. A
2xx that does not carry that shape rejects with `client.protocol`; a
refusal (a taken username, closed signup, an exhausted budget, an
unavailable dependency, a password the policy rejects) rejects the raw
`ApiError`.

```ts
const registration = await session.register({
  username: 'ada',
  email: 'ada@example.com',
  password: 's3cret-pass',
})
// registration.state === 'registered'
// registration.verification.state === 'pending'

// The account exists but nobody is signed in: the ordinary login flow
// opens the session.
const step = await session.startFlow({ method: 'password', purpose: 'login' })
```

The username is a login name; the email address is only the pending
contact a verification proves after signing in.

## The token path

`startFlow` starts every login flow as a cookie session (`delivery:
'session'`), whatever the request carries, so it can never produce a token
completion. A host that passes an `AccessTokenStore` starts the login flow
itself with the generated operation, then hands each answer to the
session:

```ts
import { authnLoginStart } from '@speed/api-sdk'
import { createAuthSession } from '@speed/auth-core'

const session = createAuthSession(store) // a host-held AccessTokenStore

// The host starts the flow with the token delivery the session forces away
// on its own startFlow path:
const step = await authnLoginStart({
  method: 'password',
  purpose: 'login',
  delivery: 'token',
})

// Every answer goes through the session, which commits a token completion
// into the store and holds the minted refresh token in its closure:
const next = await session.answerFlow({ seq: step.seq, step: 'answer', input })
```

A token session is not rebuilt by `restore()`. That method reads
`GET /api/v1/auth/session`, whose answer comes from the session cookie,
and a token host holds no such cookie: a reload starts anonymous. A host
that must keep a token session across reloads persists it itself and
re-establishes it before the first request; `refresh()` is the token
session's silent leg, not `restore()`.

In a browser deployment the frontend and the API must be same-site. The
session cookie is `SameSite=Lax` (an operator may tighten it to `Strict`;
`None` is refused by the server), so a cross-site frontend cannot carry
it. A deployment that needs a cross-site frontend uses the token path and
its own credentials.

## Step-up gating

A write whose route asks for a stronger or fresher authentication than
the session proves is refused `401 authn.insufficient_user_authentication`.
`useStepUpGate` remembers the refused action and replays it once the
challenge is won:

```ts
const gate = useStepUpGate<{ id: string }>((action) => {
  void save(action) // exactly the refused write, with the inputs it was told
})

// In a mutation's onError: arm and open the dialog only for the refusal.
onError: (error, action) => {
  if (gate.intercept(error, action)) {
    mutation.reset() // the dialog now owns the failure
  }
}
```

The default classifier recognises the platform's step-up code, not every
401: a 401 that ended the session leaves nothing to elevate. A caller
whose session already tells the two apart passes
`session.isStepUpShortfall` as the second argument.

## Tenant admission refusals

The admission layer refuses a tenant the caller may not enter with a bare
404. `isTenantAdmissionRefusal(error)` recognises that answer and leaves a
module's own typed 404 (`objectstore.not_found`) alone, so a surface can
render "this tenant is unavailable" without mistaking a missing row for
it.

## Restoring the session across a page load

The cookie outlives the page, so a reload is no longer a sign-out.
`restore()` reads `GET /api/v1/auth/session`: a cookie that still names a
session re-establishes the authenticated snapshot (reading the principal
from `GET /api/v1/auth/me`) and resolves `true`; a cookie that names nobody
clears the local snapshot and resolves `false`. Call it once at
bootstrap so a reload stays logged in. The same read carries the
session's `impersonating` flag into the snapshot (false when the answer
omits it); only `restore()` writes that field, so a host that starts or
ends an impersonation issues that write itself and re-reads the session
once it settles -- covering the cookie rotation a start performs with
`beginIdentityTransition()` (see "Identity transitions"). The snapshot
carries no initiator identity: during an impersonation the principal is
the impersonated account. It is also the
client's silent leg
for a refused request -- wire it as `refreshAccessToken` alongside
`credentials`, forwarding the round-scoped request function the hook is
handed, and a 401 re-reads the session (a still-valid cookie keeps the
request and it retries once; a dead one ends the local session):

```ts
const session = createAuthSession()
void session.restore() // at bootstrap: a valid cookie keeps the user in

createClient({
  baseUrl: window.location.origin,
  credentials: 'same-origin',
  refreshAccessToken: (issue) => session.restore(issue),
})
```

## Identity transitions

One host write replaces the credential the session rides while the
session itself continues: starting an impersonation rotates the session
cookie on the server. Requests issued under the outgoing cookie can
answer 401 while that write is still in flight, and the silent-401 leg
then runs `restore()` with a cookie the server has already detached --
a read that answers anonymous even though the session is not over, which
the host would render as a spurious sign-out in the middle of the
transition. A host covers that window with `beginIdentityTransition()`:
open it before the write goes out, and call the returned release once
the new identity has settled -- or at once when the write does not land.

While the window is open -- for a `restore()` that was already in
flight when it opened, and for one issued while it was open, whenever it
settles -- an anonymous answer is discarded: `restore()`
resolves `false` without changing the snapshot, so the refused request
keeps its 401 and no local sign-out is reported, and the next read after
the release settles the true state. A session that genuinely expired
during the window is settled by that next read, not by the read issued
under the outgoing credential. An authenticated answer is never
discarded, so the host's own re-read under the new credential commits
normally while the window is open.

```ts
const release = session.beginIdentityTransition()
try {
  await startImpersonation()
} catch (error) {
  release() // the write did not land: the old identity still stands
  throw error
}
try {
  await session.restore() // reads the identity the new credential names
} finally {
  release()
}
```

The guard is identity-generic and counted: overlapping windows stay
open until every release has been called, and each release is
idempotent (calling it twice does not close another window). The package
never learns that an impersonation started or ended; it only knows that
the credential was being replaced.

## React hooks

```ts
import {
  attachSession,
  TenantProvider,
  useAuthState,
  useCurrentTenant,
  usePermission,
} from '@speed/auth-core'

attachSession(session) // once, at bootstrap (last bind wins)

// in a component (all re-render on every session transition):
const snapshot = useAuthState()
const currentTenant = useCurrentTenant() // { tenantId } -- null without a provider
const canCreateNotes = usePermission('tenant', 'notes:write')
```

Before any session is attached -- and after a logout -- every hook fails
closed: the anonymous snapshot, a null tenant, `false` for every
permission. Attaching another session later rebinds (last bind wins);
the previous session's transitions stop reaching the hooks.

**The tenant is the host's, not the session's.** The server resolves the
tenant per request (from the request host, a subdomain or a path) as a
routing fact; the principal carries no tenant. So the host mounts its
own current tenant and the hook reads it back:

```tsx
<TenantProvider tenantId={routeTenantId}>
  <App />
</TenantProvider>
```

`useCurrentTenant` returns the host-supplied `{ tenantId }`, or null
when no provider is mounted or the host passed null.

This session-and-hooks flow is compiled and executed by the package
suite (`src/usage-example.test.tsx`), so the documented usage cannot
drift from the API. The suite drives the flow through the scripted
request seam bound with the same `bindRequestFn` a host's real client
uses; the real-client composition itself (`createClient` over a live
fetch, silent-401 refresh included) is exercised in `session.test.ts`.

## Permission checks are set lookup only

`usePermission(domain, permission)` answers "is this string in the
host-attached list for that domain" -- the `tenant` domain for the
permissions the principal holds inside its current tenant, `system` for
platform-staff permissions that are tenant-independent. The host attaches
the lists through the session; nothing here fetches or evaluates them,
and a domain whose list is absent reads `false`.

```ts
session.setPermissionSet('tenant', ['notes:read', 'notes:write']) // after a /me fetch
session.setPermissionSet('system', ['users:manage'])             // platform staff
session.setPermissionSet('tenant', null)                         // clears a domain
```

The session applies the survival rules when a principal change commits:
a silent refresh or a step-up keeps both lists, `link` and `enroll`
keep them, and a login -- even by the same account -- or an anonymous
transition clears both. The feature state follows the same rules (see
"Feature state"). These checks are a UX affordance, never a security
boundary -- the server authorizes. Refetch a domain's lists after a
login: the session clears what must not survive, but it never knows
whether a list you attach still describes the principal it was fetched
under.

## Feature state

The snapshot carries the feature document the process serves
(`GET /api/v1/features`) beside the principal and the permission sets, as a
closed `FeatureState`:

```ts
type FeatureState =
  | { state: 'none' }                                  // anonymous: nothing was read
  | { state: 'pending' }                               // a retryFeatures() read is in flight
  | { state: 'failed'; error: unknown }                // the read failed
  | { state: 'ready'; names: ReadonlySet<string> }     // the served names
```

A login completion and an anonymous-start restore read the document in
parallel with `GET /api/v1/auth/me` and commit it with the same rules as the
permission sets: a login replaces it with the freshly read document, a
same-session refresh, step-up, `link` or `enroll` keeps it without a
request, and an anonymous transition resets it to `none`. A failed
feature read never fails the login: the session commits with the state
`failed`, which is neither "every feature absent" nor "every feature
present".

`retryFeatures()` is the explicit retry for a failed read. It only acts
while authenticated: the state turns `pending`, `GET /api/v1/features` is read,
and the answer lands as `ready` or `failed` only if the session the read
started under is still the current one (same generation, still
authenticated, same principal account). Concurrent calls of the same
session share one in-flight read; a read that outlived its session is
never shared with the next one.

The hooks read the state fail-closed: `useFeature(name)` answers true
only when the state is `ready` and the set contains a name that is
statically typed as the generated `FeatureName`, so a misspelled name
does not compile. `featurePresence(state)` maps the state onto a
route layer's presence shape (`none` and `pending` both read as
pending; `failed` stays failed), which is what a host hands its router
so a gated page disappears when its feature is absent.

```ts
const canUseInbox = useFeature('notification.inbox')
const features = useFeatureState() // distinguish pending / failed / ready
```

## Tenant-scoped query keys

A cached row a signed-in principal reads is keyed under the tenant that
principal is acting in, so a tenant change can never serve the previous
tenant's rows. `tenantQueryKey` prepends the tenant id as the key's
outermost segment (one prefix per tenant, shared by all of that
tenant's reads); `useTenantQueryKey` derives the key and the value the
read gates `enabled` on from one hook call; `evictTenantQueries`
removes the departing tenant's whole subtree once a change commits. No
surface builds this convention itself -- it lives here, in one place,
because the tenant is this package's own fact (`@speed/api-client`
never carries a tenant, and `@speed/api-sdk`'s generated keys are
tenant-less bare spec paths).

```ts
import { useQuery } from '@tanstack/react-query'
import {
  evictTenantQueries,
  tenantQueryKey,
  useTenantQueryKey,
} from '@speed/auth-core'

// In a read: one hook call gives the key and the enablement operand.
// A null tenant (no route tenant, or no provider) still yields a key;
// `enabled` keeps the read from fetching it.
const { tenantId, queryKey } = useTenantQueryKey(['notes'])
useQuery({
  queryKey,
  queryFn: () => fetchNotes(),
  enabled: tenantId !== null,
})

// A key for a surface that builds its own tail, from the current
// tenant id (the factory only shapes the key -- it fetches nothing):
const membersKey = tenantQueryKey(tenantId, 'members', 'page-1')

// When the route tenant changes, evict the departing tenant's whole
// subtree. Pass the id read *before* the change -- the new id names
// nothing:
const departingTenantId = tenantId // captured before the route changes
evictTenantQueries(queryClient, departingTenantId)
```

Tenant-scoped eviction reaches only the tenant segment; identity-domain
rows cached under bare keys carry no tenant segment and are out of its
reach -- clearing those is the whole-cache eviction at session end
(`@speed/product-shell`'s shipped default). This flow is compiled and
executed by the package suite (`src/usage-example.test.tsx`).

## The response models and their guards

The authn fragments declare their 2xx response schemas, so orval emits
the response models. This package re-exports them --
`AuthnFlowState`, `AuthnStep`, `AuthnField`, `AuthnFactorOption`,
`AuthnPrincipal`, `AuthnMethodList`, `AuthnLoginRegisterBody`,
`AuthnLoginRegistration` -- rather than hand-writing a parallel copy,
and guards every body-carrying 2xx at runtime: the guard
checks that the body is an object and that it carries the required
fields, and refuses anything else as a `status: 200`, `code:
client.protocol` ApiError. The discovery document's
`password_reset.methods` are narrowed to `PasswordResetChannel`
(`'email' | 'sms'`); a document that omits the whole section is read as
closed, so a client newer than its server keeps working. The same guard
covers the feature document:
`GET /api/v1/features` must answer an object carrying a string array in
`features`; anything else is a protocol violation, never an empty
document. A contract-violating 2xx is never a
successful login, and the guard runs before any state change (and before
the first property access, so an empty 2xx never escapes as a native
`TypeError`). `AuthnField`, `AuthnFactorOption`, `AuthnPrincipal` and
`AuthnMethodList` are the generated types outright; the generated step
is flat, so `AuthnStep` and `AuthnFlowState` narrow it by `kind`/`state`
into the discriminated view a form renders.

## The failure contract

- Every request that fails rejects the raw `ApiError` from the request
  (tell it apart with `isApiError` from `@speed/api-client`), and a
  failed operation changes nothing -- **except `logout`**: a logout
  whose own request is refused still clears the local session and goes
  anonymous (a session it could not revoke is no longer trustworthy
  here either) and then rejects the raw `ApiError`.
- A session-establishing commit -- a login completion or a step-up
  completion -- has one rejection that is not an `ApiError`: when a
  concurrent commit landed while this one was in flight, the request's
  own 2xx was genuine but its answer no longer describes the session --
  nothing of it was applied -- and the operation rejects with
  `OperationSupersededError` instead of resolving with the winner's
  snapshot. Tell it apart with `isOperationSuperseded` (exported from
  this package; `isApiError` is false for it). Treat the answer
  accordingly: no one-shot side effect (a post-login redirect) may fire
  for an identity the session is not actually running under. A
  superseded token step-up carries an extra cost: its completion rotated
  the tokens and consumed, server-side, the challenge it verified, so the
  caller must never re-offer that same second factor -- only a fresh
  flow can retry. The error's `snapshot` field carries the session's
  current (winning) state.
- A body-carrying 2xx that violates the contract (missing tokens or
  session document, required fields, malformed fields) rejects with an
  `ApiError` of `status: 200` and code `client.protocol`, again changing
  nothing.
- `readFlow()` resolves `null` for a 401 (no flow binding, or the flow
  is done or gone) and rethrows anything else. `abandonFlow()` resolves
  for a 401 (the flow is already gone -- the delete is idempotent) and
  rethrows anything else.
- A login or anonymous-start restore whose feature read fails still
  commits (or resolves) with the state `failed`, carrying the read's
  error: a deployment that cannot answer `GET /api/v1/features` must not be
  read as one that serves no features. `retryFeatures()` resolves once
  its read settles; a result that lands after its session moved on
  (another commit or a local clear) is discarded, and a retry issued
  while anonymous makes no request.
- `restore()` is the cookie session's silent path. It resolves `true`
  when the cookie still names a session (the snapshot is authenticated
  afterwards), `false` when nobody is signed in -- a snapshot that was
  authenticated goes anonymous (the session is over), and a 401 from the
  read is treated the same -- and rethrows the raw `ApiError` for a
  transport or server-side failure, leaving the local snapshot in place.
  An anonymous answer read while an identity transition is open, or by a
  restore issued before one opened or while one was open (whenever it
  settles), is discarded: it resolves `false` and changes nothing,
  because it describes the credential the transition is replacing (see
  "Identity transitions").
- `refresh()` is the token session's silent path. It resolves `true`
  when the store holds a fresh token afterwards, `false` when there is
  nothing to refresh, the held token was refused (the session is over
  and signs out locally), or a concurrent commit replaced the session --
  and a transport or server-side failure rethrows the raw `ApiError` with
  the store and the held tokens untouched (a refresh never clears them on
  the way out). Concurrent calls presenting the same held refresh token
  share a single in-flight request. A cookie session holds no refresh
  token, so `refresh()` resolves `false` without a request there.
- A completed `logout` wins over a refresh or restore that resolves
  after it, and a committed login/step-up wins over a stale one: the
  losing answer's snapshot is never applied over the winner's -- with
  one exception, the rotated refresh token itself, adopted into the held
  slot when the winning commit kept the held token (a token step-up
  mints no new one, and the server has already consumed the held token
  for that refresh).

## Known limitations

- **The token path is opt-in and starts outside this package.** The
  default (and the platform's own composition) is the cookie session:
  `createAuthSession()` with no store, `credentials` on the client and
  `restore()` at boot. A host that passes an `AccessTokenStore` must
  start its login flow with the generated
  `authnLoginStart({ delivery: 'token' })` itself -- `startFlow` always
  forces the cookie delivery -- and hand the answers to `answerFlow`. On
  that path the access token is a memory cell and the refresh token is
  readable by any script on the page (no refresh cookie hides it, and the
  token responses carry it in the body); the session is not rebuilt by
  `restore()` and is lost on reload. A cookie session holds neither: the
  credential is the server's httpOnly cookie, which scripts cannot read.
- **The cookie path requires a same-site frontend and API.** The session
  cookie is `SameSite=Lax` (or `Strict`), and the server refuses `None`:
  a cross-site frontend cannot send it.
- **The `delivery` a view passes is normalized.** The login flow
  (`startFlow`) is always started as a cookie session (`delivery:
  'session'`) whatever the request carries, so a view that still names a
  token delivery is overridden -- the browser path is the cookie one.
  The `startAuthenticatedFlow` operations leave the delivery to the
  caller, whose callers pass none (the server resolves its default, a
  cookie session).
- **The token refresh's credential-less declaration depends on the
  generated artifact.** A token host's refresh travels without an
  Authorization header (`speedRequestCredentialless`). The
  `web/orval.config.ts` per-operation override names the operation id
  (`authn.token.issue`); a generated `api-sdk/src/index.ts` produced
  before that fixup routes the token operation through the bearer
  mutator instead. Regenerating the surface applies the declaration.
