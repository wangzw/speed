# AGENTS.md for `@speed/auth-ui`

The sign-in component family of a speed frontend, over an
`@speed/auth-core` session: the deployment's login methods, read from the
discovery document and grouped by kind -- the password and SMS-code forms
behind a tab strip, the redirect section and the callback handler that
resumes its flow, the enterprise email-first entry, the passkey ceremony
-- assembled in `SignInScreen`, plus `SignOutButton` and the
`SessionEndedScreen` placeholder. The package sits at the same
auth-agnostic tier as `@speed/ui-kit` and `@speed/layout-kit`: it depends
on nothing auth- or routing-shaped beyond auth-core's session and hooks,
and it never decides what happens after a sign-in. It ships one
registration form: `PasswordRegisterForm` opens an account through
`session.register` and then signs in with the same credentials. Like
every other component here it never touches HTTP directly, reads no
storage and navigates nowhere; registration remains an account-opening
the server performs, and a login flow can still open one as its
account-resolution outcome where the deployment enables that.

## What this package is

- **Controlled components over a session prop.** Every component takes
  the session it drives as a required prop. Nothing here consumes the
  auth-core hooks, attaches or persists a session, reads storage,
  navigates, or touches HTTP. A successful sign-in fires the host's
  `onSignedIn` callback exactly once; the host observes session
  transitions through its own auth-core hooks and navigates. The one
  read the surface makes on its own -- `SignInScreen`'s discovery of the
  deployment's methods (`session.listMethods`) and providers
  (`session.discoverSso`) -- goes through that same session prop, so it
  is a session operation, not a hook consumption; a host that injects a
  pre-fetched document passes `discovery` and the screen reads nothing.
- **Host callbacks are contained.** `onSignedIn` and `onAuthorizeUrl`
  fire only after the operation they report settled, and a throwing
  callback is not a failure of that operation (the tenancy-ui
  discipline): the commit already happened, so nothing here renders an
  error banner, re-classifies the outcome into a failure state or offers
  a retry of an operation that succeeded, and the containment keeps the
  throw out of the fire-and-forget promise as an unhandled rejection.
  Three callbacks report a control event instead: `onSignIn` fires when
  the viewer asks to sign in again (the host navigates; no operation
  precedes it), `onModeChange` fires when the mode switch is clicked --
  before any request, there is no operation behind it -- and only
  reports the selection, and `SignOutMenuItem`'s `onActivate` is called
  immediately before its `session.logout()`, where a throw is contained
  so the logout still runs.
  A submit whose login
  lost a concurrent sign-in race (auth-core's `OperationSupersededError`)
  is likewise not an error: the driver renders no failure banner and the
  form fires no callback -- the winning call fires its own exactly once,
  and the host observes the authenticated snapshot through its own hooks.
  `beforeStart` runs the other way round -- it decides whether a flow may
  begin, so it is consulted *before* the request: a false answer starts
  nothing and renders no error (nothing left the page, so there is no
  server answer to show), a throwing gate is a contained refusal, and only
  a channel's first step is gated (a parked step's answers are not).
  `onModeChange`, when given, also makes `mode` a controlled prop: the
  switch only reports, and the host re-renders with the selected mode.
  The SMS code step is the one exception: a phone-login code is single-use
  server-side, so the losing submit spent the very code its own answer
  verified, and the form must never advertise it as retryable. It renders
  the used-code notice (a local `smsSignIn.codeUsed` text, never an
  error-code whitelist entry -- the server answers a spent login code
  exactly like a wrong one, `authn.login_failed`) and clears the field,
  and re-submitting that exact code is refused locally with the same
  notice: only a fresh code can sign in.
- **A sign-in is a server-side flow, driven step by step.** Every channel
  begins one with `session.startFlow` and advances it with
  `session.answerFlow`; only auth-core commits the session. The parked
  steps (a forced password change, a second factor, a factor to register,
  a passkey ceremony) render through the shared step renderer
  (`src/internal/flow-step.tsx`) and the shared stepping driver
  (`src/internal/use-flow-driver.ts`). The renderer is one of three copies
  (`auth-ui`, `account-ui`, `console-ui`) kept in lockstep by hand: a
  change to how a step renders lands in all three in the same change. No
  compiler check or test compares the copies. All three label a factor or
  enroll kind the same way: a known kind renders its translated name, an
  unknown one renders the server's own kind value (the deployment's
  vocabulary, never a generic "other" label), and a factor option's
  server-supplied label wins over both. The `webauthn` step drives the
  browser's WebAuthn ceremony (`@speed/auth-core/webauthn`) from its
  public-key options and reports the credential it returns, while a
  `redirect` step reaching the renderer renders an unavailable notice. A
  step the server answers `authn.flow_invalid` returns the form to its
  channel's first step.
- **Headless logic lives in `@speed/auth-core`.** `startFlow`,
  `answerFlow`, `startAuthenticatedFlow`, `readFlow`, `abandonFlow`,
  `register`, `listMethods`, `discoverSso`, `logout`, `refresh` -- the
  session contract this family renders -- are auth-core's, along with the
  flow response and discovery models (`AuthnFlowState`, `AuthnStep`,
  `AuthnField`, `AuthnMethodList`, `AuthnMethodDescriptor`,
  `AuthnSsoProvider`, `AuthnLoginRegistration`, ...) it re-exports from
  the generated surface. If an operation does not exist on the session,
  the right home for it is auth-core's contract, not a fetch in this
  package.
- **Registration is a one-request account opening followed by a login.**
  `PasswordRegisterForm` calls `session.register` (which establishes no
  session and writes no credential), then drives the ordinary
  `method: 'password'` flow with the same credentials. The register call
  reports no callback: only a settled auto-login fires `onSignedIn` once.
  A refused auto-login is not a registration failure -- the account
  exists -- so it renders the account-created notice, keeps the username
  and offers a retry of the login alone; a host that supplies
  `onSignInRequired` takes the username and shows its own sign-in
  surface. A settled auto-login, the first or the retry, replaces the
  fields and both notices with the signed-in line, so a mounted surface
  never shows "please sign in" over an authenticated session. The
  confirmation field is compared in the form and never travels. The email
  field is checked against the server's address rule through
  `parseEmailAddress` from `@speed/ui-kit` (trimmed, printable ASCII, exactly
  one `@` with a part on each side, at most 254 bytes): a malformed address
  renders `register.invalidEmail` in that field and sends nothing. A taken
  username is the username field's error and a taken email address
  (`authn.email_taken`) the email field's -- the registration reserves the
  address, so the refusal is that value's; the other mapped refusals
  (closed signup, throttle, unavailable) render their register-section
  text in one banner, and every other code -- a 400 included -- through
  the family's error-code banner. A 429 carrying `Retry-After` holds
  the submit for the stated wait (the ApiError shape is read
  structurally; this package does not import `@speed/api-client`) and
  re-enables it when the wait elapses; a 429 without the header leaves
  the submit available at once.
- **Every built-in string is bilingual and bundled.** All text renders
  from the `auth-ui` namespace (`AUTH_UI_NAMESPACE`, one `zh-CN.json` and
  one `en-US.json` under `src/locales/`, identical leaf key sets),
  registered by the host exactly once through `@speed/i18n` alongside the
  `ui-kit` namespace.
- **The error surface is a code whitelist.** Every submit path resolves
  its failure to one error code and renders that code's text in one
  `role="alert"` banner; a `client.http.<status>` answer resolves through
  the namespace's parameterized `errors.client.http` leaf, carrying the
  status in the text, and every other code outside the whitelist renders
  `errors.unknown`, so the bundle can never show a raw key.

## Rules that are load-bearing here

1. **Components stay controlled and hook-free.** A component in `src/`
   must not consume `useAuthState`/`useCurrentTenant`/`usePermission`,
   must not call `attachSession`, and must not navigate. Read state
   changes only through props, and report results only through the
   documented callbacks (`onSignedIn`, `onAuthorizeUrl`, `onSignIn`),
   each fired at most once per outcome.
2. **No text outside the namespace, and no key set drift.** User-facing
   strings are added to both locale files in the same commit, under their
   component's section; the workspace `speed/no-literal-text` rule refuses
   inline text in `src/`. A host rewording a string re-registers the whole
   namespace with an identical-key bundle pair -- never by editing
   component text here. A step's field label or factor-kind name is a
   server-supplied token rendered through a guarded closed set: the names
   the family has wording for resolve to a bundle key, anything else
   renders verbatim (a field name or an unknown factor kind), never a
   `t()` on an unlisted token.
3. **New reachable error codes join the whitelist and both locale files
   in one commit.** `src/internal/error-text.ts`'s `ERROR_TEXT_CODES` is
   the reachable-subset whitelist this family renders (the login-flow
   problem set the flow endpoints answer with, plus the
   `client.network`/`client.timeout`/`client.protocol` transport codes,
   with `errors.unknown` the fallback for anything else). When a code
   becomes reachable through a submit path of this family, it is added
   there and to both `errors.*` sections at once. The `client.http.<status>`
   family is the one exception: it is not listed in `ERROR_TEXT_CODES`,
   but the shared resolver's `httpFamily` option routes it to the
   parameterized `errors.client.http` leaf with the status carried in the
   text (a 405, 401 and 500 stay distinguishable), so that leaf must
   exist in both bundles. `client.unknown` (the classifier's landing slot
   for non-`ApiError` throws) is not whitelisted and renders
   `errors.unknown` by design.
4. **No direct HTTP in `src/`.** Every request flows through the
   session's generated operations over the `bindRequestFn` binding; this
   package is not on the `speed/no-direct-http` whitelist. Tests bind
   their doubles through the same binding (`bindRequestFn` from
   `@speed/api-sdk/runtime`) -- never by mocking a module.
5. **The public surface is the `index.ts` exports.** Helpers shared
   between components live in `src/internal/` and are deliberately not
   exported; a component that cannot be built from public pieces does not
   exist. No public type references `@speed/api-sdk` or
   `@speed/api-client`: both are **devDependencies**, used only by the
   executed README example and the test rigs. Do not promote either to a
   dependency without a public type that needs it.
6. **The README's quick start is executed, not aspirational.**
   `src/usage-example.test.tsx` compiles and runs the documented
   composition (real api-client, genuine `Response` objects from a
   scripted fetch, real session, real i18n instance, the host gate) in the
   order the README shows. Any README composition change must keep that
   true; any component behaviour change ships with its README section
   updated in the same commit.
7. **The sign-in surface and the screens stay separate.**
   `SessionEndedScreen` never signs in -- it hands back to the host. The
   screens that exist in this package are `SignInScreen` and
   `SessionEndedScreen`; everything else is a section, form or action the
   host composes. The authenticated-purpose surfaces (`link`, `step-up`,
   `enroll`) are `@speed/account-ui`'s, not this package's.
8. **`SessionEndedScreen`'s title is a real page heading.** The screen
   replaces the whole authenticated page (product-shell's ended branch
   mounts it with no ancestor heading), so its `EmptyState` title renders
   as an `h1` by default; `headingLevel` is forwarded for a host embedding
   the screen under a heading of its own.

## Public surface

Everything in `src/index.ts` is public: `SignInScreen` (with
`SignInScreenProps`, `SignInContext`, `SignInChannel`, `SignInMode`,
`SignInGroup`, `SignInScreenSlots`, `SignInBeforeStart`, `SignInStartContext`
and `SocialSignInOptions`), the channel
components (`PasswordSignInForm`, `PasswordRegisterForm`,
`SMSSignInForm`), the social pair
(`SocialSignInSection` with `SocialProvider` -- a method-name `string`,
not a closed set -- and `SocialProviderConfig`, `SocialCallbackHandler`),
`SignOutButton`, `SignOutMenuItem`, `SessionEndedScreen`,
`AcceptInvitationScreen` with its operation shapes, the impersonation
surface (`ImpersonationBanner`, the notice provider and its consumers --
including the provider-reading `ImpersonationNoticeBanner` and
`ImpersonationNoticeIndicator`), and the resource pair
(`AUTH_UI_NAMESPACE`, `authUiResources`). Prop tables and behaviour live in
the README; `tsc -p tsconfig.json` type-checks every consumer-visible
signature.

## Testing

Vitest + jsdom, one test file per source file under `src/`, shared
helpers only in `test-utils/`; the vitest config aliases the `@speed/*`
specifiers onto sibling packages' `src` entries, so no test imports
another package's `dist/`. Three helper layers:

- `test-utils/render.tsx` -- `renderWithProviders` mounts a unit under
  the real host tree (fresh bilingual instance per call with both
  namespaces registered; `I18nextProvider` around `AppThemeProvider`).
  Bilingual assertions import the shipped locale files, never inline a
  language literal.
- `@speed/test-utils/session-harness` -- `makeHarness`, the scripted fake
  `RequestFn` bound through `bindRequestFn`, for tests that must script
  raw `ApiError`s or inspect request bodies. Its endpoint constants and
  reply builders (`openFlow`, `completeFlow`, `authenticatedCompletion`,
  `principal`, ...) are the flow model's own.
- `test-utils/real-client.ts` + `test-utils/session-gate.tsx` -- the
  journey rig: a real `@speed/api-client` over a scripted fetch answering
  genuine `Response` objects (so the 401-refresh leg runs in the client
  machinery itself), and the host gate miniature reading the snapshot of
  the attached session.

`src/usage-example.test.tsx` executes the README quick start end to end
over the real-client rig; `src/session-journey.test.tsx` drives the
journeys beyond it (the SMS channel, explicit sign-out, the social
callback route). Every component test asserts axe
(`expectNoAxeViolations`) and the bilingual text of its states.

## Known limitations

Recorded, with the current disposition, in the README's Known limitations
and Deferrals sections. The load-bearing ones for a contributor: the
password registration form opens an account but establishes no session
itself, so the viewer is signed in only if the auto-login succeeds; the
authenticated-purpose surfaces (binding, step-up, factor enrollment) live
in `@speed/account-ui`; the login channels are the deployment's, read
from the discovery document, so the family renders exactly the methods
the running deployment enables; and session state does not survive a page
load (auth-core's limit, inherited).
