# AGENTS.md for `@speed/account-ui`

The account-management component family of a speed frontend, the
signed-in half of the account story: a set of surfaces compose a host
account page — the self-service write surfaces (`ProfileSection` over the
profile's `PATCH /api/v1/auth/me`, `PasswordSection` over `PUT /api/v1/auth/password`,
`PhoneSection` over the two-step `POST /api/v1/auth/phone` and
`/api/v1/auth/phone/verify`, and `IdentifiersSection` over the profile's own
identifiers, its add face's `POST /api/v1/auth/identifiers` and
`/api/v1/auth/identifiers/verify`, and `DELETE /api/v1/auth/identifiers/{id}`),
`ContactVerificationSection` (the self-registration email verification state
read from the profile's `verification`, over the same identifier write pair),
`PreferencesSection`
(the language-and-timezone selects over the profile's own `PATCH
/api/v1/auth/me`), `AppearanceSection` (the controlled theme-preset
picker: the host supplies the shelf and the labels, the section reports
the pick and reads no theme provider), `SessionsSection` (the live-sessions list with per-session
and bulk revocation), `SocialBindingsSection` plus
`BindingCallbackHandler` (the social bindings surface and the callback
route that completes a link flow), and `MfaSection` (the two-step
verification surface: the factors read from `GET /api/v1/auth/factors`, their
removal, and the enroll flow). The package sits one step above
`@speed/auth-ui`'s tier: reads go through the react-query hooks generated
into `@speed/api-sdk` over the host's QueryClient (the shared-QueryClient
contract: one host-bound QueryClient, never package-constructed), and
writes through the generated mutations over the same `bindRequestFn`
binding. The surfaces that drive a flow take the `@speed/auth-core`
session as a required prop; `PreferencesSection` takes it optionally
(the step-up gate only), and the read-only ones take no props.

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
  `timeZone` through `@speed/i18n`'s `formatInstant` — never Intl's
  implicit process-local default, which would render one account
  differently depending on which machine ran the code. The chain is the
  stored `time_zone`, then the host's `DisplayTimeZoneProvider` context
  (which replaces only the device tier), then the device zone, then UTC;
  every candidate is validated through `isSupportedTimeZone`/
  `resolveTimeZone`. The profile tier reads `GET /api/v1/auth/me`; the
  query is shared and deduped across the sections by react-query, so
  adding another time-rendering surface costs no extra request.
- **Every built-in string is bilingual and bundled.** All text renders
  from the `account-ui` namespace (`ACCOUNT_UI_NAMESPACE`, one
  `zh-CN.json` and one `en-US.json` under `src/locales/`, identical leaf
  keys per language), registered by the host exactly once through
  `@speed/i18n` alongside the `ui-kit` namespace — the confirm-again
  label of the armed danger dialog and the `EmptyState` texts are
  ui-kit-namespace keys.
- **Registration verification is a read state plus the identifier writes.**
  `ContactVerificationSection` reads `verification` off the same profile
  query the other surfaces read: `none` renders nothing, `verified` renders
  the completion state, `pending` with an address renders the target, the
  send/resend action, the code field and the change-address action, and
  `pending` with no address renders the add-email form (the legal state after
  the last proved address is removed -- the section is the only path back to a
  verifiable address, so the add cannot live only on the identifiers face).
  Sending is `POST /api/v1/auth/identifiers`; verification is
  `/api/v1/auth/identifiers/verify` with the code alone. The section never
  distinguishes a free address from one another account holds while sending
  (the answer is the same shape and no "already registered" copy exists); the
  taken-address text belongs to the verify step alone. A resend control
  addresses the target the section displays, is disabled for the server's
  `resend_after` (or a 429's Retry-After) and states why, and its countdown
  timer stops at expiry; the sent notice is announced once through a
  `role="status"` region that is mounted empty and filled on landing, and
  focus moves to the code field. It takes the session (its writes are step-up
  gated), the optional `initialChangeOpen`, which starts it in the
  change-address form so a host can land a "change address" action there, and
  the optional `autoSend`, which the demo account page sets only for a
  `?verify=1` landing (the banner's verify action and the post-registration
  landing): once per mount, a pending account with a target address and a
  closed change form sends one code itself, and re-renders and the post-write
  refetch keep to that one send. A mount that lands with the change form open
  (`initialChangeOpen`, the `?change=email` entry) never sends automatically,
  even after the form is closed: that visitor came to replace the address.
  The automatic send never opens the step-up dialog -- that is the visitor's
  to open -- so a 401 leaves the manual send action with an explanation; a
  429 still starts the resend countdown, and other failures render the
  failure banner.
- **The identifiers add face checks an email address before sending it.**
  For `kind=email` the value is checked against the server's rule through
  `parseEmailAddress` from `@speed/ui-kit` (trimmed, printable ASCII, exactly
  one `@` with a part on each side, at most 254 bytes); a malformed address
  renders `identifiers.add.invalidEmail` in the field and sends nothing, and
  the submitted value is the trimmed one. A 400 the server still answers for
  the email kind renders that same field message, never the account-state
  banner. An address another account has reserved or proved is answered 409
  `email_taken`; the claim is atomic, so that refusal is the address's too
  and renders `identifiers.add.emailTaken` in the same field. The code's
  verification answers `email_taken` as well when the address was taken
  after the code was requested; the face returns to the value field
  instead of asking for a code nothing can accept. A username's
  400 keeps the banner: the server's username rule also
  refuses shapes this face does not restate (the reserved static-credential
  prefix, a UUID-shaped name). Closing the add face clears the failure
  banner, the field error, the success notice and the sent-code step.
- **A username carries no ownership chip; an email or phone chip is one of
  two values, reading `proven` and never `verified_at`.** A username is a
  login name with no ownership to prove, so its row renders no ownership
  chip. An email or phone row reads `identifiers.verified` when `proven` is
  true and `identifiers.unverified` otherwise, whatever `verified_via` says:
  the chip answers only whether ownership has been proved, so an address an
  administrator asserted or an import recorded is an unproved address, not a
  third state. Every stored identifier carries a `verified_at`, including a
  username written at registration as `self`, so `verified_at` cannot answer
  the ownership question for the rows that show one. An
  account with a password signs in with a username or an email address:
  while the profile lists exactly one of those, that row's remove
  action gives way to a note (`identifiers.lastLoginName`) telling the user
  to add another login name first. Another username/email, or an account
  with no password at all, keeps the action; the server still judges the
  last way in on the write itself, and a refusal renders its 409
  `login-identifier-required` text and re-reads the profile, so a guard
  that read a stale list converges on the server's answer. While the profile's
  `verification.state` is `pending`, the identifiers add face does not offer
  the email kind: the pending contact is
  the verification section's, and a second pending address must not exist on
  one account.
- **An unverified email row carries its own verify action.** The action
  (`identifiers.verify`, named per address by `identifiers.verifyAria`) opens
  an inline flow on the address the row already shows: the value field is
  read-only, the send is `POST /api/v1/auth/identifiers` with that same
  address (the route accepts the account's own address as a re-add of its
  proof), and the code is submitted through
  `/api/v1/auth/identifiers/verify`; the landed write refetches the profile
  and the row's chip flips to `identifiers.verified`. The action appears even
  when the row's remove action is withheld as the only login name, and it is
  withheld while `verification.state` is `pending` (that state's single code
  record belongs to the verification section and the host's banner) and on a
  deployment with no email channel. The flow reuses the add face's sent-code
  step, failure slot, field-error and resend-countdown rules rather than a
  second mechanism; a refused code renders
  `identifiers.verifyCodeInvalid` in its field, and an address another account
  proved in between renders `verification.addressTaken` in the section's one
  banner. A phone is a sign-in method and an additional security factor;
  verifying it never lifts the account's verification restriction -- only a
  proved email address does.
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
   state (`linked`, `enrolled`) never signs anyone in. The step-up
   dialog's answers that send a verification message -- choosing an SMS
   second factor, or re-proving the SMS first factor with a phone -- render
   the deployment's proof block and present its proof (`useStepUp`'s
   `human`), consuming it; an enrollment sends nothing (the SMS factor
   binds a phone the account has already proved) and presents none. The step renderer
   (`src/internal/flow-step.tsx`) is one of three copies (`auth-ui`,
   `account-ui`, `console-ui`) kept in lockstep by hand: a change to how a
   step renders lands in all three in the same change. No compiler check
   or test compares the copies. All three label a factor or enroll kind
   the same way: a known kind renders its translated name, an unknown one
   renders the server's own kind value (the deployment's vocabulary, never
   a generic "other" label), and a factor option's server-supplied label
   wins over both. All three start an assertion passkey ceremony on the
   step's arrival, keying the arrival on the step number and the challenge
   so a re-sent challenge starts a fresh ceremony while an ordinary
   re-render does not, and start a registration ceremony on a click,
   because a creation ceremony needs the user activation some browsers
   require.
2. **State is discovered through the spec's own answers; the package
   renders exactly the operations that exist.** Registration is not a
   factor route — it is the login engine's enroll purpose — so
   `MfaSection` never declares an enable operation; the factors are read
   from `GET /api/v1/auth/factors` and removal is `DELETE /api/v1/auth/factors/{id}`.
   The enrollment entries are the deployment's own answer too: the section
   reads `GET /api/v1/auth/methods`' `mfa.factors` (through `session.listMethods`)
   and offers one entry per listed kind, so it never offers a factor the
   assembly cannot complete. A known kind renders `mfa.enroll.<kind>`; an
   unknown kind renders `mfa.enroll.other` and still starts that kind's
   flow. The spec ships no disable operation: the server answers
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
   import each other. Social channels are the deployment's login-method
   names, read from the discovery document (`session.listMethods()`,
   `kind: 'redirect'`); the package carries no host-supplied channel list
   and exports no channel-config type. A stored binding and a method's
   already-bound state both resolve through the descriptor's
   `binding_providers` (an exact entry or a namespace prefix; a
   descriptor that declares none matches by its name); a value no
   descriptor claims falls back to the package's closed vocabulary.
   `StepUpDialog` is exported for the
   host only — `@speed/console-ui` and `@speed/auth-ui` must not import
   it (the former keeps its own copy).
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
   session is never revocable from the list. The step-up dialog renders
   its "no active factor" state as text, and renders a link only when the
   host supplies `accountHref` (the package itself still never navigates,
   and the internal sections omit the prop): these sections are the
   account settings surface, so their copy is self-sufficient.

## Public surface

Everything in `src/index.ts` is public: the surfaces (`ProfileSection`
with `ProfileSectionProps`; `PasswordSection` with `PasswordSectionProps`;
`PhoneSection` with `PhoneSectionProps`; `IdentifiersSection` with
`IdentifiersSectionProps`; `ContactVerificationSection` with
`ContactVerificationSectionProps`; `PreferencesSection` with
`PreferencesSectionProps`; `SessionsSection`; `SocialBindingsSection`
with `SocialBindingsSectionProps`; `MfaSection` with `MfaSectionProps`),
`BindingCallbackHandler` with `BindingCallbackHandlerProps`,
`StepUpDialog` with `StepUpDialogProps` (the host seam; see rule 7), and
the resource pair (`ACCOUNT_UI_NAMESPACE`, `accountUiResources`). Prop
tables and behaviour live in the README; `tsc -p tsconfig.json`
type-checks every consumer-visible signature.

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
package ships no QR or clipboard integration, so the authenticator's
`otpauth_uri` is manual-entry only (the passkey ceremony itself runs
through the browser's WebAuthn API, driven by
`@speed/auth-core/webauthn`); the enrollment entries are the deployment's
discovered factor kinds (`GET /api/v1/auth/methods`' `mfa.factors`), one
entry per kind with an unknown kind under the generic label, so an
assembly without a passkey relying party shows no passkey entry; the session list renders the
server's list (only live sessions, the current one never revocable here);
session state does not survive a page load, the refresh token is
JavaScript-visible in memory, and step-up elevation outlives exactly the
session that earned it (auth-core's limits, inherited).
