# AGENTS.md for `@speed/account-ui`

The account-management component family of a speed frontend, the
signed-in half of the account story: a set of surfaces compose a host
account page — the self-service write surfaces (`ProfileSection` over the
profile's `PATCH /auth/me`, `PasswordSection` over `PUT /auth/password`,
`PhoneSection` over the two-step `POST /auth/phone` and
`/auth/phone/verify`, and `IdentifiersSection` over the profile's own
identifiers and `DELETE /auth/identifiers/{id}`), `PreferencesSection`
(the language-and-timezone selects over the profile's own `PATCH
/auth/me`), `SessionsSection` (the live-sessions list with per-session
and bulk revocation), `SocialBindingsSection` plus
`BindingCallbackHandler` (the social bindings surface and the callback
route that completes a link flow), and `MfaSection` (the two-step
verification surface: the factors read from `GET /auth/factors`, their
removal, and the enroll flow). The package sits one step above
`@speed/auth-ui`'s tier: reads go through the react-query hooks generated
into `@speed/api-sdk` over the host's QueryClient (the shared-QueryClient
contract: one host-bound QueryClient, never package-constructed), and
writes through the generated mutations over the same `bindRequestFn`
binding. The surfaces that drive a flow take the `@speed/auth-core`
session as a required prop; the read-only ones take no props.

## What this package is

- **Controlled sections over a host-owned QueryClient and session.**
  Every read is a generated react-query hook selected through this
  package's response-model narrowing (`src/internal/queries.ts` over
  `src/internal/models.ts`), and every write is a generated mutation or
  the session's own flow operation. Nothing here consumes the auth-core
  hooks, attaches or persists a session, reads storage, navigates, or
  touches HTTP. The session arrives as a prop only where a flow
  operation exists that a plain generated call cannot express —
  `session.startAuthenticatedFlow` / `session.answerFlow` in the
  bindings add area, the enroll wizard and the step-up dialog, and
  `session.readFlow` in the callback handler. A surface with no flow to
  drive takes no session prop.
- **The generated-hooks tier, consumed.** Sign-in forms deliberately
  avoid react-query because their answers are one-shot; account lists are
  cacheable shared state that invalidates after its own mutations, which
  is why the generated-hooks tier (and therefore the host's
  QueryClientProvider) exists. Invalidations use the exported query-key
  builders (`getAuthnProfileGetQueryKey`, `getAuthnSessionListQueryKey`,
  `getAuthnMfaFactorsQueryKey`), never hand-written query keys.
- **The authn fragments declare their 2xx response schemas.** orval
  emits the response models; this package aliases them in
  `src/internal/models.ts` rather than own a parallel copy, and selects
  each generated read through a narrowing. A narrowing is a shape
  assertion, not a guard: an answer that omitted a collection reads as
  `undefined` — the caller's error variant — and never as an empty list,
  so "no sessions" is only ever claimed for an answer that genuinely
  listed none.
- **Display times carry the account's timezone chain, explicitly.**
  Every timestamp a surface renders is formatted with an explicit
  `timeZone` resolved through `resolveTimeZone` (internal/time-format:
  the stored `time_zone`, else the device zone, else UTC) — never Intl's
  implicit process-local default, which would render one account
  differently depending on which machine ran the code. The profile tier
  reads `GET /auth/me`; the query is shared and deduped across the
  sections by react-query, so adding another time-rendering surface costs
  no extra request. Every Intl construction in that helper is guarded.
- **Every built-in string is bilingual and bundled.** All text renders
  from the `account-ui` namespace (`ACCOUNT_UI_NAMESPACE`, one
  `zh-CN.json` and one `en-US.json` under `src/locales/`, identical leaf
  keys per language), registered by the host exactly once through
  `@speed/i18n` alongside the `ui-kit` namespace — the confirm-again
  label of the armed danger dialog and the `EmptyState` texts are
  ui-kit-namespace keys.
- **The error surface is a code whitelist with guarded vocabulary.**
  Every failure path resolves to one error code rendered in one
  `role="alert"` banner; codes outside the whitelist render
  `errors.unknown`, never a raw key. Server vocabulary that is not an
  error code — the session kind, the factor kind, the provider — renders
  only for values on the component's known list, anything else the
  generic other label.

## Rules that are load-bearing here

1. **The flow model is the substrate; a surface drives a flow only
   through the session.** A social binding, a factor enrollment and a
   step-up are flows the server drives one answer at a time; the session
   is the only thing that may advance one, and it commits the session
   only when a completion issues tokens. `SocialBindingsSection` starts a
   `purpose: 'link'` flow and reads the authorization URL off its first
   `redirect` step; `BindingCallbackHandler` reads the flow back
   (`readFlow`) and takes a ready flow with `step: 'complete'`;
   `MfaSection` starts `purpose: 'enroll'` and the step-up dialog
   `purpose: 'step-up'`. A flow completion that does not touch the login
   state (`linked`, `enrolled`) never signs anyone in.
2. **State is discovered through the spec's own answers; the package
   renders exactly the operations that exist.** Registration is not a
   factor route — it is the login engine's enroll purpose — so
   `MfaSection` never declares an enable operation; the factors are read
   from `GET /auth/factors` and removal is `DELETE /auth/factors/{id}`.
   The enrollment entries are the deployment's own answer too: the section
   reads `GET /auth/methods`' `mfa.factors` (through `session.listMethods`)
   and offers only those kinds, so it never offers a factor the assembly
   cannot complete. The spec ships no disable operation: the server answers
   it by listing only live sessions. Do not invent operations the spec
   does not ship, and do not let a section render a state the server never
   answers with.
3. **No text outside the namespace, no key set drift, and guarded
   server vocabulary.** User-facing strings are added to both locale
   files in the same commit under their component's section; the
   `speed/no-literal-text` rule refuses inline text in `src/`. Session
   kind, factor kind and provider values pass through `t()` only when on
   the component's known-value list. A host rewording a string
   re-registers the whole namespace with an identical-key bundle pair —
   never by editing component text here.
4. **New reachable error codes join the whitelist and both locale files
   in one commit.** `src/internal/error-text.ts`'s `ERROR_TEXT_CODES` is
   the reachable-subset whitelist this family renders (the authn problem
   code set of `pkg/authn/internal/mount` plus the three `client.*`
   transport codes), with `errors.unknown` for everything else. When a
   code becomes reachable through a path of this family, add it there and
   to both `errors.*` sections at once. Codes whose failure context
   matches the sign-in surface's reuse the auth-ui bundle's text
   verbatim — never divergent copy for the same server answer — and the
   suite pins that copy. Deliberately not whitelisted: `client.http.*`
   and `client.unknown` (the classifier's landing slot for non-`ApiError`
   throws) — they render `errors.unknown` by design.
5. **No direct HTTP in `src/`, and no react-query client of our own.**
   Every request flows through generated operations over the
   `bindRequestFn` binding; this package is not on the
   `speed/no-direct-http` whitelist. Components consume the generated
   hooks and the query-key builders; they never construct their own
   QueryClient (the host owns it) and never hand-write a query key.
   Tests bind their doubles through the same binding
   (`bindRequestFn` from `@speed/api-sdk/runtime`) — never by mocking a
   module or by importing another package's `dist/`.
6. **The public surface is the `index.ts` exports; dependencies follow
   public types.** Helpers live in `src/internal/` and are deliberately
   not exported; a component that cannot be built from public pieces
   does not exist. The prop tables reference `AuthSession` of
   `@speed/auth-core`, and every read is a generated hook of
   `@speed/api-sdk` — so both are declared **dependencies** (api-sdk a
   runtime one: the hooks, the mutations and the query-key builders all
   execute here, unlike auth-ui's type-only use). Consumers resolve the
   published `.d.ts` under pnpm's strict node_modules, and a public type
   reference or a runtime import demands a dependency entry, never a dev
   one. Do not move them back. `@speed/api-client` stays a
   devDependency: no public type references it; only the test-utils rig
   binds a real client.
7. **`@speed/auth-ui` is never imported.** Same-layer packages do not
   import each other. `SocialProvider`/`SocialProviderConfig` are this
   package's own definitions, copied to match auth-ui's and kept in sync
   with them — `SocialProvider` is a deployment login-method name (a
   `string`, not a closed set; the WeChat pair arrives as `wechat.web` /
   `wechat.mp`), and the same name lands in both copies together.
8. **The README's quick start is executed, not aspirational.**
   `src/usage-example.test.tsx` compiles and runs the documented
   composition (real api-client, genuine `Response` objects from a
   scripted fetch, real session, real i18n, the host's QueryClient). Any
   README composition change must keep that true; any component behaviour
   change ships with its README section updated in the same commit.
9. **Sections stay sections; nothing navigates, nothing signs anyone in
   or out on its own.** The surfaces are account-page content under host
   headings — empty and failure states hide the section header and render
   a ui-kit `EmptyState` with `headingLevel="h2"` (the hidden header's
   own level), so heading order never skips a level; this depends on the
   caller supplying `headingLevel` correctly, since `EmptyState` itself
   defaults to `h6` (see `@speed/ui-kit`'s own `EmptyState` doc comment).
   The bindings add area reports authorization URLs upward through
   `onAuthorizeUrl`; `BindingCallbackHandler` fires `onBound` after a
   `link` completion and never claims a login happened. The current
   session is never revocable from the list.

## Public surface

Everything in `src/index.ts` is public: the surfaces (`ProfileSection`
with `ProfileSectionProps`; `PasswordSection` with `PasswordSectionProps`;
`PhoneSection` with `PhoneSectionProps`; `IdentifiersSection` with
`IdentifiersSectionProps`; `PreferencesSection`; `SessionsSection`;
`SocialBindingsSection` with `SocialBindingsSectionProps`, `SocialProvider`
and `SocialProviderConfig`; `MfaSection` with `MfaSectionProps`),
`BindingCallbackHandler` with `BindingCallbackHandlerProps`, and the
resource pair (`ACCOUNT_UI_NAMESPACE`, `accountUiResources`). Prop tables and behaviour
live in the README; `tsc -p tsconfig.json` type-checks every
consumer-visible signature.

## Testing

Vitest + jsdom, one test file per source file under `src/`, shared
helpers only in `test-utils/`; the vitest config aliases the `@speed/*`
specifiers onto sibling packages' `src` entries, so no test imports
another package's `dist/`. Two helper layers, carrying the one provider
auth-ui's harness does not — the `QueryClientProvider`, because every
read is a react-query hook:

- `test-utils/render.tsx` — `renderWithProviders` mounts a unit under the
  real host tree (`I18nextProvider` around `AppThemeProvider` around
  `QueryClientProvider`), with a fresh bilingual instance per call
  registering both namespaces a rendered surface can read, and
  `createTestQueryClient` retrying nothing so an operation the test
  scripts to fail surfaces on the first attempt.
- `test-utils/real-client.ts` — the real-client rig: a real
  `@speed/api-client` over a scripted fetch answering genuine `Response`
  objects (so the 401-refresh leg runs in the client machinery itself),
  the memory access-token store, and the session bound through
  `bindRequestFn`, recording every request's method, path, query,
  authorization header and decoded body. A session-free variant
  (`makeRealClientRigWithoutSession`) exists for the read-only suites that
  want a raw 401 to surface directly. Every answer a surface can render —
  error codes included — is scriptable as a genuine `Response` carrying
  the API envelope.

`src/usage-example.test.tsx` executes the README quick start end to end
over the rig; every component test asserts axe (`expectNoAxeViolations`)
and the bilingual text of its states. The whitelist-to-bundle pairing is
pinned in both languages by `src/internal/error-text.test.ts`.

## Known limitations

Recorded, with the current disposition, in the README's Known
limitations and deferrals. The load-bearing ones for a contributor: the
package ships no QR, clipboard or WebAuthn integration, so a passkey
enrollment renders the browser's creation options as text and answers one
free-text field, and the authenticator's `otpauth_uri` is manual-entry
only; the enrollment entries are the deployment's discovered factor kinds
(`GET /auth/methods`' `mfa.factors`), so an assembly without a passkey
relying party shows no passkey entry; the session list renders the
server's list (only live sessions, the current one never revocable here);
session state does not survive a page load, the refresh token is
JavaScript-visible in memory, and step-up elevation outlives exactly the
session that earned it (auth-core's limits, inherited).
