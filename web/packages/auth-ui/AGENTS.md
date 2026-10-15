# AGENTS.md for `@speed/auth-ui`

The sign-in component family of a speed frontend, over an
`@speed/auth-core` session: the password and SMS-code channels, the
social section and the callback handler that resumes its flow, assembled
in `SignInScreen` behind a channel tab strip, plus `SignOutButton` and
the `SessionEndedScreen` placeholder. The package sits at the same
auth-agnostic tier as `@speed/ui-kit` and `@speed/layout-kit`: it depends
on nothing auth- or routing-shaped beyond auth-core's session and hooks,
and it never decides what happens after a sign-in. There is no
registration form -- an account is created inside a login flow's
account-resolution step (or by invite, or by an administrator), so a
"registration" is a login flow's outcome, not a form this package ships.

## What this package is

- **Controlled components over a session prop.** Every component takes
  the session it drives as a required prop. Nothing here consumes the
  auth-core hooks, attaches or persists a session, reads storage,
  navigates, or touches HTTP. A successful sign-in fires the host's
  `onSignedIn` callback exactly once; the host observes session
  transitions through its own auth-core hooks and navigates.
- **Host callbacks run after the verdict and are contained.** Every
  documented callback (`onSignedIn`, `onAuthorizeUrl`, `onSignIn`) fires
  only after the operation it follows settled, and a throwing callback is
  not a failure of that operation (the tenancy-ui discipline): the commit
  already happened, so nothing here renders an error banner, re-classifies
  the outcome into a failure state or offers a retry of an operation that
  succeeded, and the containment keeps the throw out of the
  fire-and-forget promise as an unhandled rejection. A submit whose login
  lost a concurrent sign-in race (auth-core's `OperationSupersededError`)
  is likewise not an error: the driver renders no failure banner and the
  form fires no callback -- the winning call fires its own exactly once,
  and the host observes the authenticated snapshot through its own hooks.
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
  steps (a forced password change, a second factor, a factor to register)
  render through the shared step renderer (`src/internal/flow-step.tsx`)
  and the shared stepping driver (`src/internal/use-flow-driver.ts`); a
  `webauthn` or `redirect` step this surface cannot finish renders an
  unavailable notice. A step the server answers `authn.flow_invalid`
  returns the form to its channel's first step.
- **Headless logic lives in `@speed/auth-core`.** `startFlow`,
  `answerFlow`, `startAuthenticatedFlow`, `readFlow`, `abandonFlow`,
  `listMethods`, `logout`, `refresh` -- the session contract this family
  renders -- are auth-core's, along with the hand-written flow response
  models (`AuthnFlowState`, `AuthnStep`, `AuthnField`, ...) it
  re-exports. If an operation does not exist on the session, the right
  home for it is auth-core's contract, not a fetch in this package.
- **Every built-in string is bilingual and bundled.** All text renders
  from the `auth-ui` namespace (`AUTH_UI_NAMESPACE`, one `zh-CN.json` and
  one `en-US.json` under `src/locales/`, identical leaf key sets),
  registered by the host exactly once through `@speed/i18n` alongside the
  `ui-kit` namespace.
- **The error surface is a code whitelist.** Every submit path resolves
  its failure to one error code and renders that code's text in one
  `role="alert"` banner; codes outside the whitelist render
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
   renders verbatim (a field name) or the generic "other" label (a factor
   kind), never a `t()` on an unlisted token.
3. **New reachable error codes join the whitelist and both locale files
   in one commit.** `src/internal/error-text.ts`'s `ERROR_TEXT_CODES` is
   the reachable-subset whitelist this family renders (the login-flow
   problem set the flow endpoints answer with, plus the
   `client.network`/`client.timeout`/`client.protocol` transport codes,
   with `errors.unknown` the fallback for anything else). When a code
   becomes reachable through a submit path of this family, it is added
   there and to both `errors.*` sections at once. Codes deliberately not
   whitelisted: any `client.http.*` answer and `client.unknown` (the
   classifier's landing slot for non-`ApiError` throws) -- those render
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
`SignInChannel` and `SocialSignInOptions`), the two channel components
(`PasswordSignInForm`, `SMSSignInForm`), the social pair
(`SocialSignInSection` with `SocialProvider`/`SocialProviderConfig`,
`SocialCallbackHandler`), `SignOutButton`, `SessionEndedScreen`, and the
resource pair (`AUTH_UI_NAMESPACE`, `authUiResources`). Prop tables and
behaviour live in the README; `tsc -p tsconfig.json` type-checks every
consumer-visible signature.

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
and Deferrals sections. The load-bearing ones for a contributor: a
registration is a login flow's outcome, not a form here; the
authenticated-purpose surfaces (binding, step-up, factor enrollment) live
in `@speed/account-ui`, and a `webauthn` step a login flow parks on
renders an unavailable notice; the server exposes no full channel
discovery, so the family renders exactly the channels the host composes;
and session state does not survive a page load (auth-core's limit,
inherited).
