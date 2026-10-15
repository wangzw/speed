# @speed/auth-core

The browser session lifecycle as a headless, memory-only state machine
over the generated authn flow surface of `@speed/api-sdk`.
`createAuthSession` wires the generated operations -- the login flow
(start, answer, current, abandon), the authenticated-purpose start for
`link`, `step-up` and `enroll`, the methods listing, logout and the
token endpoint a refresh uses -- to one observable session with a
single in-memory access-token store as the bridge into
`@speed/api-client`: a host that built its client with this same store
and `refreshAccessToken: () => session.refresh()` gets silent refresh
for free -- an expired-token 401 on any request runs one refresh, and
the retried request carries the fresh token.

Login is a server-side flow the client advances, not one request: the
session starts a flow, hands each step back to the view, and commits
only when a step settles a token issuance. No UI and no storage writes:
the access token lives in the caller-supplied store (in memory by
design -- no storage API exists here; see Known limitations); the
refresh token lives only inside the session closure and is never
written anywhere; there is no `restore` (see Known limitations). React
hooks (`useAuthState`, `useCurrentTenant`, `usePermission`) read one
session the host attaches with `attachSession`; react is a peer
dependency, so only a host that already renders React carries it.

The generated authn fragments declare no response schema, so orval types
every return as `void`. This package hand-writes the response models and
guards each body at runtime, refusing a contract-violating 2xx as a
`client.protocol` error -- see "The response models and their guards".

## What ships

| File | Exports |
|---|---|
| `session.ts` | `createAuthSession(store)`, `AuthSession`, `AuthSnapshot`, `AuthSessionListener`, `AuthDomain`, `AuthPermissionSets`, `AuthnPrincipal`, `AuthnFlowState`, `AuthnStep`, `AuthnField`, `AuthnFactorOption`, `AuthnMethodList`, `AuthnIdentifier`, `AuthnLink`, `OperationSupersededError`, `isOperationSuperseded` |
| `hooks.ts` | `attachSession(session)`, `useAuthState()`, `usePermission(domain, permission)`, `TenantProvider`, `useCurrentTenant()` |
| `query-key.ts` | `tenantQueryKey(tenantId, ...tail)`, `useTenantQueryKey(bareKey)`, `TenantQueryKey`, `evictTenantQueries(queryClient, tenantId)` |

`src/index.ts` re-exports these; everything else is internal.

## Quick start

```ts
import { createClient, createMemoryAccessTokenStore } from '@speed/api-client'
import { bindRequestFn } from '@speed/api-sdk/runtime'
import { createAuthSession } from '@speed/auth-core'

const accessTokenStore = createMemoryAccessTokenStore()
const session = createAuthSession(accessTokenStore)

bindRequestFn(
  createClient({
    baseUrl: 'https://api.example.com',
    accessTokenStore,
    refreshAccessToken: () => session.refresh(),
  }),
)

// subscribe to state transitions once, at app bootstrap:
session.subscribe((snapshot) => render(snapshot))

// render paths read the current snapshot:
const snapshot = session.getSnapshot() // { state: 'anonymous', principal: null, ... }
```

## Driving a login flow

A password (or SMS, or social) sign-in is a server-side flow the view
drives step by step. `startFlow` begins it and returns the first step;
the view dispatches on `next.kind`; `answerFlow` advances it and returns
the next step. When the flow reaches a completion that issued tokens
(`state: 'authenticated'` for a login, `state: 'elevated'` for a
step-up) the session commits -- writes the store, reads `GET /auth/me`
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
machine; a step-up completion re-mints the access token with the
principal unchanged, and `link`/`enroll` completions leave the login
state alone:

```ts
// Enroll a second factor: start with purpose 'enroll', drive the steps.
const enrollment = await session.startAuthenticatedFlow({ purpose: 'enroll' })
```

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
transition clears both. These checks are a UX affordance, never a
security boundary -- the server authorizes. Refetch a domain's lists
after a login: the session clears what must not survive, but it never
knows whether a list you attach still describes the principal it was
fetched under.

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

The authn fragments declare no success schema, so orval generates
`void` return types. This package therefore hand-writes the response
models -- `AuthnFlowState`, `AuthnStep`, `AuthnField`,
`AuthnFactorOption`, `AuthnPrincipal`, `AuthnMethodList` -- and guards
every body-carrying 2xx at runtime: the guard checks that the body is an
object and that it carries the required fields, and refuses anything
else as a `status: 200`, `code: client.protocol` ApiError. A
contract-violating 2xx is never a successful login, and the guard runs
before any state change (and before the first property access, so an
empty 2xx never escapes as a native `TypeError`). These models are the
consuming side's hand-written contract: once a fragment declares a
schema, they converge with the generated surface.

## The failure contract

- Every request that fails rejects the raw `ApiError` from the request
  (tell it apart with `isApiError` from `@speed/api-client`), and a
  failed operation changes nothing -- **except `logout`**: a logout
  whose own request is refused still clears the local session and goes
  anonymous (a session it could not revoke is no longer trustworthy
  here either) and then rejects the raw `ApiError`.
- A token-issuing commit -- a login completion or a step-up completion
  -- has one rejection that is not an `ApiError`: when a concurrent
  commit landed while this one was in flight, the request's own 2xx was
  genuine but its answer no longer describes the session -- nothing of
  it was applied -- and the operation rejects with
  `OperationSupersededError` instead of resolving with the winner's
  snapshot. Tell it apart with `isOperationSuperseded` (exported from
  this package; `isApiError` is false for it). Treat the answer
  accordingly: no one-shot side effect (a post-login redirect) may fire
  for an identity the session is not actually running under. A
  superseded step-up carries an extra cost: its completion rotated the
  tokens and consumed, server-side, the challenge it verified, so the
  caller must never re-offer that same second factor -- only a fresh
  flow can retry. The error's `snapshot` field carries the session's
  current (winning) state.
- A body-carrying 2xx that violates the contract (missing tokens or
  required fields, malformed fields) rejects with an `ApiError` of
  `status: 200` and code `client.protocol`, again changing nothing.
- `readFlow()` resolves `null` for a 401 (no flow binding, or the flow
  is done or gone) and rethrows anything else. `abandonFlow()` resolves
  for a 401 (the flow is already gone -- the delete is idempotent) and
  rethrows anything else.
- `refresh()` is the silent path. It resolves `true` when the store
  holds a fresh token afterwards, `false` when there is nothing to
  refresh, the held token was refused (the session is over and signs
  out locally), or a concurrent commit replaced the session -- and a
  transport or server-side failure rethrows the raw `ApiError` with the
  store and the held tokens untouched (a refresh never clears them on
  the way out). Concurrent calls presenting the same held refresh token
  share a single in-flight request.
- A completed `logout` wins over a refresh that resolves after it, and a
  committed login/step-up wins over a stale refresh: the losing pair's
  access token and snapshot are never applied over the winner's -- with
  one exception, the rotated refresh token itself, adopted into the
  held slot when the winning commit kept the held token (a step-up
  mints no new one, and the server has already consumed the held token
  for that refresh).

## Known limitations

- **No persistence across page loads.** The session is memory-only and
  there is deliberately no `restore`: reloading the page starts
  anonymous. The authn token responses carry the refresh token in the
  body (the browser path delivers `delivery: 'token'`; no refresh cookie
  exists), so nothing outside the session closure outlives the page --
  a reload cannot re-establish the session, and the user signs in again.
- **The refresh token is JavaScript-visible in memory.** No refresh
  cookie hides it, so the session must hold the token its refresh
  request takes in the body. The access token is likewise readable by
  any script running in the page; no storage API exists here on purpose.
- **`delivery: 'session'` is not modeled.** The browser path delivers a
  token pair; a completion answered with a session document instead (a
  cookie-delivered login) is refused by the guard as `client.protocol`,
  because this session holds tokens, not a cookie session.
- **The refresh request's credential-less declaration depends on the
  generated artifact.** The design has the refresh travel without an
  Authorization header (`speedRequestCredentialless`). The
  `web/orval.config.ts` per-operation override now names the current
  operation id (`authn.token.issue`); a generated `api-sdk/src/index.ts`
  produced before that fixup routes the token operation through the
  bearer mutator instead. Regenerating the surface applies the
  declaration.
