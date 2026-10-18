# AGENTS.md — @speed/api-sdk

Guidance for AI tooling working in or against this package. The public
surface, generation mechanics and deferred items are documented in the
package `README.md`; this file records the invariants that keep the
package safe.

## What this module is

The generated, spec-first typed surface of speed APIs for the
frontend: orval output from each module's openapi fragment, calling the
`@speed/api-client` runtime through one hand-written binding
(`src/runtime.ts`). Generated code never performs HTTP itself; the
`speed/no-direct-http` ESLint rule whitelists only `@speed/api-client`,
and `src/` must keep passing it on every regeneration.

## Invariants (code review enforces; do not weaken)

- **`src/index.ts` is generated; never hand-edit it.** It is stamped
  with a DO-NOT-EDIT header from `web/orval.config.ts` that carries the
  pinned orval version -- tool drift manifests as a header diff. Change
  the generator config and regenerate; never patch the artifact.
- **`src/runtime.ts` is the only hand-written source file.** Never add
  a second hand-written file inside `src/` to bridge a generation gap.
  Tooling gaps are fixed in tooling: the extensionless-mutator-import
  problem is solved by `web/scripts/orval-nodenext-fixup.mjs`, a
  deterministic post-generation rewrite of every `'./runtime'` mutator
  import (one per mutator -- plain `speedRequest` plus one line per
  per-operation override such as `speedRequestCredentialless`) that is
  idempotent -- an already-fixed file (the imports already carrying
  `'.js'`) exits 0, so the fixup can be re-run freely -- and exits
  non-zero only when a regeneration ships no mutator import in either
  the bare or the explicit form, and by the tsconfig pair (`bundler`
  resolution for typecheck, `nodenext` for build), not by shipped
  source.
- **The mutator surface is a binding, not an import.** `speedRequest`
  adapts orval's axios-shaped call to the request function a host bound
  with `bindRequestFn(createClient(...))`, attaching the bearer token
  from the host's api-client store. `speedRequestCredentialless` is
  the second mutator, selected per operation by `web/orval.config.ts`
  for requests that must go out without a bearer token (the authn
  session-refresh operation, which authenticates with the refresh
  token in its body and whose 401 must stay terminal -- see
  `@speed/api-client`'s bearer-only rule): it forwards the call with
  `omitAccessToken` declared and never sets the plain mutator's shape.
  Calls made through either mutator while unbound throw a programmer
  error (`[speed-api-sdk] no request function bound: ...`); rebinding
  replaces the previous function, last bind wins, no once-guard (tests
  and hot reload rebind).
- **No tenant header and no tenant query key exist in generated
  code.** Tenant context is attached by the transport, never by a
  generated operation (`@speed/api-client`'s `tenantProvider` value
  travels as the `X-Tenant` header), and query keys are bare spec paths.
  No `tenant_id` field
  appears in the generated types either: the tenant a request acts in is
  a server-resolved routing fact, not a body or response field, so no
  fragment carries a `tenant_id` -- the identity surface's login, SMS and
  social-callback bodies and the profile response alike. Where `tenant`
  does appear it is an ordinary path parameter on the admin- and
  authorization-scoped routes (`/auth/admin/tenants/{tenant}/...`),
  naming the route's subject, never a data scope. Tenant query-key
  namespacing is a consumer-shell discipline, recorded in
  `web/orval.config.ts` and the README.
- **orval stays out of the lockfile.** Every runner
  invokes `pnpm dlx orval@8.17.0 --config orval.config.ts` from `web/`
  followed by the fixup script. A version bump lands in both
  places (config comment, every caller's command) at once.
- **No user-facing text, no i18n resources.** Errors are typed as the
  spec's `{code, params}` envelope; codes map to bilingual text in
  consumer catalogs. The runtime binding's programmer errors are constant
  English strings.
- **The peer family is the QueryClient contract.** `react` and
  `@tanstack/react-query` v5 are peers so hosts share one
  QueryClient/Provider; the package never constructs one. Its own
  hooks are exercised in tests with a `QueryClientProvider` around
  `renderHook`.

## What ships vs. what is deferred

The generated surface over the merged document (`src/index.ts` --
orval input is `contracts/api.json`, the demo host's `gen` command
merging the OpenAPI fragments the platform modules declare -- each
module's `http.Spec` resource, through `pkg/ops/openapi.Merge` -- every
platform module with an HTTP fragment is a merge member; a consuming
app's own fragments are members too, and their operations carry the
application-owner tag `gen` writes from the declaring module's Go
package, which `web/orval.config.ts`'s `input.filters` excludes, so this
surface stays platform-only: the app's own API is generated into the
app's own SDK through the app-owned flow -- the starter's personal-tenant
ensure is the app fragment that exercises that leg), the
runtime binding, the regeneration
tooling (config + fixup script), and a unit case that holds the committed
surface to the committed document's platform operations in both directions:
an operation added to or removed from that half without regenerating fails
the package's tests. Regeneration itself is a manual step -- the command in
README.md, run from `web/` -- so a change that alters only shapes is caught
by rerunning it, not by that case. `@speed/auth-core` compile-consumes the
authn surface in-workspace: its unit suite binds a scripted request function
through `bindRequestFn` -- the same binding a host's real client binds --
so a spec change whose regenerated surface outgrows auth-core's calls
fails that package's typecheck.

`starters/demo/web` binds this surface at runtime: its app-entry assembly
(`@speed/product-shell/bootstrap`'s `bootstrapSpeedApp`) binds one real
`@speed/api-client` client through `bindRequestFn`, and the app's console
area drives the generated operations over it. The package still ships no
browser-tier suite of its own: its unit tests bind a fake request function
and never touch a network, and the in-workspace consumers
(`@speed/auth-core`, `@speed/account-ui`, `@speed/console-ui`) reach the
generated surface at compile time. Tenant query-key namespacing stays a
consumer-shell discipline by contract.

Deferred with reasons:

- The oasdiff breaking-change gate -- not implemented: a
  breaking-change gate needs a release baseline to diff against. The
  first baseline is derivable -- v0.0.1's publish commit (fbaaaf98) is
  in this repository's history and its tree carries the complete merged
  document and every fragment; the module proxy's cache covers only 17
  of the 21 modules -- but nothing is wired yet.
- Release-time packaging of the merged spec into the published SDK --
  not implemented (release machinery); do not claim it lives here.

## Public surface

Three entry points:

- `.` (generated `src/index.ts`) -- the operation functions, hooks and
  response models orval derives from the merged document. The shape
  is orval's, not ours: expect it to change only through the generator.
- `./runtime` (`src/runtime.ts`) -- `bindRequestFn`, the `speedRequest`
  mutator and the per-operation `speedRequestCredentialless` mutator
  (selected by `web/orval.config.ts`), the stable hand-written binding
  hosts bind.
- `./features` (generated `src/features.ts`) -- `FeatureNameRegistry`,
  the `FeatureName` key type and `FEATURE_NAMES`, written by
  `web/scripts/feature-names.mjs` from `contracts/features.json`. The
  file registers the platform-owned entries only; an application adds
  its own names through the module augmentation the same generator
  writes into the application (the demo writes
  `starters/demo/web/src/app-api/features.ts`). A feature name written
  in frontend code takes this type, so a misspelled name fails to
  compile; `FEATURE_NAMES` is the run-time membership list of the
  platform names.

`src/runtime.test.ts` pins the binding's behaviour; `src/index.test.ts`
pins the generated surface's observable behaviour (hooks issue the
expected calls through the mutator with the expected query keys).
Extend `runtime.ts` deliberately; the generated file only via
`web/orval.config.ts`.

## Development

From this directory (`web/packages/api-sdk/`), or workspace-wide from
`web/` with `pnpm -r`:

```sh
pnpm lint        # eslint; generated src/ must pass, incl. speed/no-direct-http
pnpm typecheck   # strict; bundler resolution maps sibling sources via tsconfig paths
pnpm test        # vitest (jsdom); colocated unit tests, no network
pnpm build       # nodenext tsc build to dist/ (gitignored); pre-builds @speed/api-client
```

Regenerate (from `web/`):

```sh
pnpm dlx orval@8.17.0 --config orval.config.ts
node scripts/orval-nodenext-fixup.mjs
```

The committed artifact is the post-fixup file; CI reproduces both steps
and diffs.
