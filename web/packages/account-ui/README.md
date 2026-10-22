# @speed/account-ui

The signed-in account-management component family of the speed frontend:
a set of sections a host composes into its account page, each over an
`@speed/auth-core` session and the generated authn operations of
`@speed/api-sdk`. The sections are controlled and read their data through
the generated react-query hooks over the host's `QueryClient` -- nothing
here reads storage, attaches a session, navigates or touches the network
directly.

The authn flow model is the package's substrate: a login, a social
binding, a two-factor enrollment and a step-up are all flows the server
drives, and the components that need to drive one take the session as a
prop and advance the flow (`session.startAuthenticatedFlow`,
`session.answerFlow`, `session.readFlow`). Everything else is a plain
generated read or write on the shared access token.

## What ships

| File | Export |
|---|---|
| `src/ProfileSection.tsx` | `ProfileSection`, `ProfileSectionProps` |
| `src/PasswordSection.tsx` | `PasswordSection`, `PasswordSectionProps` |
| `src/PhoneSection.tsx` | `PhoneSection`, `PhoneSectionProps` |
| `src/IdentifiersSection.tsx` | `IdentifiersSection`, `IdentifiersSectionProps` |
| `src/ContactVerificationSection.tsx` | `ContactVerificationSection`, `ContactVerificationSectionProps` |
| `src/PreferencesSection.tsx` | `PreferencesSection`, `PreferencesSectionProps` |
| `src/AppearanceSection.tsx` | `AppearanceSection`, `AppearanceSectionProps`, `ThemePresetChoice` |
| `src/SessionsSection.tsx` | `SessionsSection` |
| `src/SocialBindingsSection.tsx` | `SocialBindingsSection`, `SocialBindingsSectionProps` |
| `src/BindingCallbackHandler.tsx` | `BindingCallbackHandler`, `BindingCallbackHandlerProps` |
| `src/MfaSection.tsx` | `MfaSection`, `MfaSectionProps` |
| `src/internal/step-up-challenge.tsx` | `StepUpDialog` (re-exported as `StepUpDialog`/`StepUpDialogProps`), host use only |
| `src/resources.ts` | `ACCOUNT_UI_NAMESPACE`, `accountUiResources` |

`src/internal/` holds the family's shared pipeline (the response-model
narrowings, the flow-step renderer, the step-up dialog, the error-text
resolver, the inline error banner and the translation hook) and is
deliberately not exported apart from the `StepUpDialog` re-export, which
is the host's seam.

## Quick start

Register the two namespaces (account-ui's own and ui-kit's, because the
confirm dialogs speak ui-kit-namespace keys) on the host's i18n instance,
build the session over the host's access-token store, bind the client into
the api-sdk runtime, and compose the sections under the providers:

```tsx
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { registerNamespace } from '@speed/i18n'
import { AppThemeProvider, UI_KIT_NAMESPACE, uiKitResources } from '@speed/ui-kit'
import {
  ACCOUNT_UI_NAMESPACE,
  accountUiResources,
  ProfileSection,
  PasswordSection,
  PhoneSection,
  IdentifiersSection,
  ContactVerificationSection,
  PreferencesSection,
  AppearanceSection,
  SessionsSection,
  SocialBindingsSection,
  BindingCallbackHandler,
  MfaSection,
} from '@speed/account-ui'

const queryClient = new QueryClient()

function AccountPage({ session, onAuthorizeUrl }) {
  return (
    <QueryClientProvider client={queryClient}>
      <AppThemeProvider i18n={i18n}>
        <ProfileSection session={session} />
        <PasswordSection session={session} />
        <PhoneSection session={session} />
        <IdentifiersSection session={session} />
        <ContactVerificationSection session={session} />
        <PreferencesSection session={session} />
        <SessionsSection />
        <SocialBindingsSection
          session={session}
          returnTo="/account/callback"
          onAuthorizeUrl={onAuthorizeUrl}
        />
        <MfaSection session={session} />
      </AppThemeProvider>
    </QueryClientProvider>
  )
}
```

The host renders `BindingCallbackHandler` on the route its providers
redirect back to, with the same session:

```tsx
<BindingCallbackHandler session={session} onBound={() => navigate('/account')} />
```

`web/packages/account-ui/src/usage-example.test.tsx` compiles and runs
this assembly end to end.

## The self-service write surfaces

These sections cover the account's own self-service writes. Each drives a
route that asks for a fresh authentication: an unelevated caller is
answered 401 `authn.insufficient_user_authentication`, which arms the
shared `useStepUpGate` from `@speed/auth-core` and opens `StepUpDialog`
(driving a `purpose: 'step-up'` flow) with that request's inputs
remembered; a won challenge re-runs exactly that request once. Each takes
the session as its only required prop (the dialog drives it).

```tsx
interface ProfileSectionProps {
  readonly session: AuthSession
}
interface PasswordSectionProps {
  readonly session: AuthSession
  readonly withoutLoginName?: 'form' | 'explain'
}
interface PhoneSectionProps {
  readonly session: AuthSession
}
interface IdentifiersSectionProps {
  readonly session: AuthSession
  readonly emailAvailable?: boolean
  readonly offerUsername?: boolean
}
interface ContactVerificationSectionProps {
  readonly session: AuthSession
  readonly initialChangeOpen?: boolean // the change-address form starts open
}
```

### ProfileSection

Edits the account's display name through the profile write
`PATCH /api/v1/auth/me` (the same route the preferences section writes its
language and time zone through). It reads `GET /api/v1/auth/me` for the stored
name, lets the field track that value until the user edits it, and
PATCHes a one-field body from the save button, which is disabled while
the field still holds the account's own name (a save with nothing changed
would be a no-op PATCH). A successful save fills a `role="status"` notice
and refetches the profile.

### PasswordSection

Changes the password through `PUT /api/v1/auth/password`. The current-password
field renders only when the profile says a password is set (an account
opened through a provider holds none); a provider-only account changes to
its first password with the new-password fields alone, and that form's
helper states the policy instead of the change's "not used before"
sentence. Two client-side guards -- a non-empty new password, a matching
confirmation -- render on their fields before any request; a refusal from
the server (a wrong current password is the 401 `login-failed`) renders
its code text, except a 400 `bad-request`, which is this route's
password-policy refusal and renders the section's policy sentence. A
successful change clears the fields, refetches the profile and the
session list (the route rotates this session's token and revokes the
others) and reports the change -- a first password too, whose notice
therefore also names the other devices signed out.

`withoutLoginName` covers the state in which the route answers 409
`login-identifier-required` (the account holds neither a username nor an
email address). The default `'form'` keeps the form and lets the
server's refusal render the code's text; `'explain'` renders the
explanation up front and no form, for a page that already knows the
account cannot set a password yet.

### PhoneSection

Adds or replaces the account's phone number in two steps:
`POST /api/v1/auth/phone` sends a code to the number and `POST /api/v1/auth/phone/verify`
submits it. The number is entered through the shared `PhoneNumberField`
(country/region selector plus local number, `@speed/ui-kit`), assembled
into E.164 for the send and validated per country before any request --
the same control and rules the SMS sign-in form uses, so the two cannot
drift. The sent notice names the assembled number; the verify carries the code
alone, because the number is the server's own record keyed to the session.
A verified number refetches the profile (and the session list, since the
route revokes the account's other sessions) and reports the addition.

### IdentifiersSection

Lists the account's sign-in identifiers -- read off the profile's
`identifiers` field (`GET /api/v1/auth/me`), because the spec ships no dedicated
list route -- each with its kind (email, phone or username). A username is a
login name with no ownership to prove, so its row shows no verified/unverified
state; email and phone rows show the server's `proven` state. `proven` is true
only for the ways of establishing an identifier that prove ownership
(`email-code`, `sms-code`, `invite`, `idp:<provider>`); `admin`,
`import-unverified` and `self` only record an assertion, so `proven` is false
for them. A username row renders no state at all, so these values never show
for it. `verified_at` cannot answer that question -- every stored identifier
carries one. A row's remove action sits behind the ui-kit danger confirm and
sends `DELETE /api/v1/auth/identifiers/{id}`; a refusal (removing a password
account's last username or email address is the 409
`login-identifier-required`) renders its code text and re-reads the profile,
so a client guard that read a stale list converges on the server's answer.
An account with a password
signs in with a username or an email address: while the profile lists exactly
one of those, that row's remove action gives way to a note saying another login
name must be added first. Another username/email, or an account with no
password at all, keeps the action; the server still judges the last way in.

Below the list an add face adds one identifier the caller chooses, its kind
`username` or `email` (a phone is added through its own two-step surface).
A username is a login name with no ownership to prove, so
`POST /api/v1/auth/identifiers` writes it in one step and the answer reports the
addition; a username already in use is answered 409 `conflict` and
rendered as the taken-name sentence (the same code on a removal keeps its
generic text). An email is a contact address, so the add checks it against the
server's rule through `parseEmailAddress` (trimmed, printable ASCII, exactly one
`@` with a part on each side, at most 254 bytes) before sending anything: a
malformed address renders `identifiers.add.invalidEmail` in the field, and the
submitted value is the trimmed one. A 400 the server still answers for the email
kind lands in that same field rather than the banner; so does the 409
`email_taken` for an address another account has reserved or proved, because
the claim is atomic and the address is the value to change. The code's
verification answers the same code when the address was taken after the code
was requested, and the face returns to the value field with that message. A
username's 400
keeps the banner because the server's username rule also refuses shapes this
face does not restate. A well-formed email gets a code (the answer is state `pending`) and the
code is submitted through `POST /api/v1/auth/identifiers/verify`; only the
matching code writes the address. Closing the add face clears the failure
banner, the field error, the success notice and the sent-code step.
The email branch is unavailable on a deployment with no email channel (a
503 `unavailable`). Every write -- add, verify and remove -- asks for fresh
authentication: a 401 `authn.insufficient_user_authentication` opens the
step-up dialog with the write remembered, and a won challenge re-runs that
one write. A write that lands -- a username add, an email's verification or a
removal, not the code-send step, which writes nothing -- refetches the
profile and the session list, because these routes revoke the account's
other sessions.

`emailAvailable` says whether the add surface offers the email kind (the
host passes the deployment's `authn.identifiers.email` feature, optionally
narrowed by its own product choice); `offerUsername` says whether it
offers the username kind. Both default to true, and a profile that
already lists a username never offers that kind. While the profile's
`verification.state` is `pending`, the email kind is withheld regardless:
the pending contact belongs to `ContactVerificationSection`, and one
account must not carry two pending addresses.

### ContactVerificationSection

The self-registration contact's verification state, read from
`GET /api/v1/auth/me`'s `verification` field:

- `none` (an account that never self-registered) renders nothing.
- `verified` renders the completion state, naming the proved address.
- `pending` with an address renders the target, a send/resend action, the
  code field and a change-address action.
- `pending` with no address renders the add-email form. That is a legal
  state (the account's last proved address was removed), and the section is
  the only path back to a verifiable address.

Sending (and changing the target) is `POST /api/v1/auth/identifiers` with
`{kind: 'email', value}`; verification is
`POST /api/v1/auth/identifiers/verify` with the code alone. A landed verify
refetches the profile (so `verification` flips to `verified`) and the session
list, because the route revokes the account's other sessions.

The send answer is the same shape whether or not another account holds the
address, and the section has no "already registered" copy. A conflict renders
its own sentence only on the verify step, after a code proved control of the
address, and points at the change action. The wrong-code answer renders the
unified invalid-or-expired sentence. A resend action addresses the same
target the section displays (the address this mount sent to, else the
profile's pending contact), is disabled for the server's `resend_after` (or a
429's `Retry-After`) and states the remaining seconds, and its countdown timer
stops at expiry. The sent notice is announced once through a `role="status"`
region mounted empty and filled on landing, and focus moves to the code
field. A 401 `authn.insufficient_user_authentication` arms the shared step-up
gate, and a won challenge re-runs exactly that write once.

`initialChangeOpen` starts the section in the change-address form instead of
the code step; a host whose persistent notice offers "change address" beside
"verify" lands the first on the flag and the second without it.

`autoSend` turns the section into a landing surface: once per mount, a pending
account with a target address and a closed change form sends one code on its
own, and re-renders and the post-write profile refetch keep to that one send. A
mount that starts with the change form open never sends automatically, even
after the form is closed: that visitor came to replace the address.
The automatic send never opens the step-up dialog -- a dialog is the visitor's
to open -- so a 401 leaves the manual send action with an explanation; a 429
still starts the resend countdown, and other failures render the failure
banner. The dedicated verification page sets it; the account page does not.

A phone number is a sign-in method and an additional security factor.
Verifying a phone does not lift the registration restriction; only a proved
email address turns `verification.state` to `verified`. `PhoneSection`'s copy
states that.

```tsx
interface ContactVerificationSectionProps {
  readonly session: AuthSession
  /** Starts the change-address form open (default false: the code step). */
  readonly initialChangeOpen?: boolean
  /** Sends one code on mount when pending with a target address (default
   * false). The automatic send never opens the step-up dialog. */
  readonly autoSend?: boolean
}
```

## PreferencesSection

The language and time zone selects, editing the account's own stored
values through the profile self-write route: it reads `GET /api/v1/auth/me` for
the stored `locale` and `time_zone` and writes `PATCH /api/v1/auth/me` with a
one-field body.

- Language options are the i18n instance's own supported set
  (`readSupportedLanguages`); option labels are the language names in the
  current UI language (`languageDisplayName`, the raw tag as fallback). A
  choice writes the account first and, only on success, runs the
  persisting `switchLanguage`; clearing the preference writes the empty
  string to the account alone (the empty string is not a language to
  switch the device to).
- Time zone options are the engine's IANA list plus an explicit "not
  chosen" (the empty string). A stored zone the engine's list omits is
  added as its own option, so the select never silently displays another
  zone.

Both writes are immediate and PATCH-first: a refusal leaves the controls
on the stored values and renders a two-line failure alert (the save line
plus the resolved code text).

The optional `session` prop arms the shared step-up gate: when given, a
401 `authn.insufficient_user_authentication` opens `StepUpDialog` and a
won challenge replays exactly that refused write once. Without the prop
the same refusal renders the failure banner; the host can elevate through
another section on the page (elevation applies to the whole session) or
sign in again.

```tsx
interface PreferencesSectionProps {
  readonly session?: AuthSession
}
```

## AppearanceSection

The theme-preset picker. It is fully controlled: the host owns the shelf
(each entry's stable id plus an already-translated label), the current id
and the switch action. The section neither imports `@speed/theme-presets`
nor reads a theme provider, so it renders the same whether the host's
presets come from the shipped package or from the application's own
tokens.

```tsx
interface ThemePresetChoice {
  readonly id: string
  readonly label: string
}

interface AppearanceSectionProps {
  readonly presets: readonly ThemePresetChoice[]
  readonly value: string
  readonly onChange: (id: string) => void
}
```

## SessionsSection

The sessions-and-devices list, read from `GET /api/v1/auth/sessions` -- the
server lists only live sessions, so a row is never revoked or expired.
The current session (the request's own) carries a badge and no revoke
affordance; every other row has a row-end sign-out button
(`DELETE /api/v1/auth/sessions/{id}`). The section-top "sign out other devices"
action (`DELETE /api/v1/auth/sessions`) revokes every other session at once and
sits behind the ui-kit danger confirm -- a single confirming click, not
the heavier two-step arm, because the action is recoverable (a signed-out
device signs in again); its answer's revoked count is surfaced in a
success notice. Every successful revoke invalidates the
list query, so the rows converge on the server's answer. The section
takes no props.

Row times render through the account's time-zone chain: the stored
profile `time_zone` wins, then the host's `DisplayTimeZoneProvider`
context (which replaces only the device tier), then the device zone, then
UTC. The formatter is `@speed/i18n`'s `formatInstant`, so the zone is
always explicit and never the engine's process-local default.

## SocialBindingsSection

The social-account bindings surface. The bound identities are the
profile's `links` field (`GET /api/v1/auth/me`); each row names the provider and
its email. A row with an id has an unlink action
(`DELETE /api/v1/auth/links/{id}`) behind the ui-kit danger confirm; a landed
unbind is confirmed in a `role="status"` notice above the list, and a
refused one renders its code text there.

The channels come from the deployment's discovery document
(`session.listMethods()`, `GET /api/v1/auth/methods`): every method whose
`kind` is `redirect` and that is not already bound gets one button,
labelled with the descriptor's own `label` (the method name when it
carries none). A method is already bound when any provider value it
declares in `binding_providers` matches a stored link's provider -- an
exact entry, or a stored value continuing the entry as a namespace prefix
-- and a descriptor that declares none matches by its name. A deployment
with no redirect method -- or whose discovery read failed -- shows no add
area (fail closed): the section never offers a channel the deployment
cannot finish. A bound row resolves its provider through the same
declaration: the descriptor that claims the value supplies its `label`,
and a value no descriptor claims falls back to the family's closed
vocabulary (the five social names, the enterprise-SSO family prefix and
the generic other label).

Clicking a button starts a `purpose: 'link'` flow on the session
(`session.startAuthenticatedFlow`) whose request carries `returnTo` as
the top-level `return_to`. The flow's first `redirect` step carries the
provider's authorization URL, which is reported upward through
`onAuthorizeUrl` -- the section never navigates. The backend lands the
browser on `returnTo` once the provider answers; the host completes the
flow there with `BindingCallbackHandler`. One bind flow at a time: while
a channel's flow is being started every button is disabled.

```tsx
interface SocialBindingsSectionProps {
  readonly session: AuthSession
  readonly returnTo: string
  readonly onAuthorizeUrl?: (url: string) => void
  readonly providerIcon?: (provider: string) => ReactNode
}
```

`returnTo` is the host's binding callback route. A split-origin
deployment passes an absolute URL under an origin the deployment trusts
(the server accepts a path or such a URL). `providerIcon` optionally
supplies a brand icon by method name; it renders beside the add-area
button and the bound row's label.

## BindingCallbackHandler

The handler the host renders on its providers' callback route. In the
flow model the server advances the `purpose: 'link'` flow when the
provider redirects back, so the handler holds no code or state: it reads
the flow back with `session.readFlow()` and, when the flow is ready
(`next.kind = 'complete'`), takes the result with `step: 'complete'`. A
`link` completion invalidates the profile read and fires `onBound`; a
flow still parked on a step holds the pending notice with a retry; a
failed read renders its code text under a retry. The `link` flow never
signs anyone in, so there is no signed-elsewhere outcome.

```tsx
interface BindingCallbackHandlerProps {
  readonly session: AuthSession
  readonly onBound?: () => void
}
```

## MfaSection

The two-step-verification surface. The authn factor routes list and
remove factors (`GET /api/v1/auth/factors`, `DELETE /api/v1/auth/factors/{id}`);
registration is not among them -- the one entry is the login engine's
`enroll` purpose -- so the section renders the factors it reads and
offers the enrollment entries THIS deployment carries, each driving a
`purpose: 'enroll'` flow. Which kinds those are is the deployment's own
answer: the section reads `GET /api/v1/auth/methods`' `mfa.factors` once through
`session.listMethods` and offers one entry per reported kind, so a
deployment whose assembly has no passkey relying party shows no passkey
entry. A known kind (`totp`, `webauthn`, `sms`) renders its translated
name under `mfa.enroll.<kind>`; an unknown kind gets the generic other
label but still starts that kind's own flow.

- Enroll. `session.startAuthenticatedFlow({purpose: 'enroll', input: {kind}})`
  opens the wizard; each reply's next step is rendered and answered
  (`session.answerFlow`). A `webauthn` step hands the factor its own JSON:
  TOTP's `otpauth_uri` renders as text (the package ships no QR
  dependency) beside a code field, and a passkey's creation options are
  driven through the browser's WebAuthn API by
  `@speed/auth-core/webauthn`. The settled `enrolled` refetches the
  factors.
- Remove. A row's action sends `DELETE /api/v1/auth/factors/{id}` behind the
  danger confirm. The route asks for a fresh multi-factor
  authentication: an unelevated caller is answered 401
  `authn.insufficient_user_authentication`, which opens the step-up
  dialog and, on success, re-runs that removal exactly once. A 409 (the
  last factor) renders its code text.

```tsx
interface MfaSectionProps {
  readonly session: AuthSession
}
```

## StepUpDialog

`StepUpDialog` is the package's step-up re-verification dialog, exported
for the host (`src/internal/step-up-challenge.tsx` internally; see the
ADR-web-auth-seams-2026-10-19 ruling). It drives one
`purpose: 'step-up'` flow: an account with a usable factor enters at the
`factor` step and chooses one, where the flow then asks for its proof; a
completion in state `elevated` (or the login-shaped `authenticated` body
of a cookie-delivered session) fires its `onSuccess`, so the caller
re-runs the gated action. An account with no active factor takes one of
three paths, decided by the session's own verdict: when the session
already satisfies a fresh first factor, the dialog renders the text
explaining that two-step verification must be registered first and
starts no flow; when the refusal asks for a fresh first factor, it
starts a first-factor step-up with the session's own method; when that
method cannot be re-proved, it offers a re-login.

```tsx
interface StepUpDialogProps {
  readonly open: boolean
  readonly session: AuthSession
  readonly onSuccess: () => void
  readonly onCancel: () => void
  readonly accountHref?: string
}
```

`accountHref` is the host's account-settings route: when given, the
missing-factor explanation renders it as a plain link; when omitted, the
dialog renders text alone and never navigates. The dialog pairs with
`useStepUpGate` from `@speed/auth-core` (arm a refused action, open the
dialog, and `succeed` replays it once). The dialog is a host seam only:
`@speed/console-ui` keeps its own copy and must not import it (a
same-layer edge); `@speed/auth-ui` has no step-up dialog.

## Text and i18n

Every built-in string lives in the account-ui namespace, in `zh-CN` and
`en-US` with identical leaf key sets (registration enforces parity).
Register it exactly once, right after `createI18n`:

```ts
registerNamespace(i18n, ACCOUNT_UI_NAMESPACE, accountUiResources)
```

### Error text: the reachable-code whitelist

Every failure path resolves its answer to one error code and renders the
matching text through one `role="alert"` banner; a code outside the
whitelist renders `errors.unknown`, never a raw key. The whitelist is the
authn problem code set (pkg/authn/internal/mount) plus the client
transport codes of `@speed/api-client`, and lives in
`src/internal/error-text.ts`; the suite beside it pins the whitelist to
the bundle leaves in both languages and pins the codes shared with the
sign-in surface to verbatim copies of auth-ui's text. Server vocabulary
that is not an error code -- the session kind, the factor kind, the
provider -- is rendered only for values on a known list, with a generic
"other" label otherwise.

## Accessibility

Lists are real lists (`role="list"` under a `list-style-none` ul, so
WebKit keeps the semantics), section headers are level-2 headings that
step aside for the `EmptyState` title when a settled read is empty, the
success notices are `role="status"` live regions, and every action that
names its own row (a session's revoke, a binding's unlink, a factor's
removal) carries an aria-label interpolated without HTML escaping so a
free-text value reaches the accessibility tree verbatim. Each suite runs
an axe scan at the end of its behavioural tests.

## Testing

`pnpm test` runs the vitest suites (jsdom). The component suites drive
each surface through the real-client rig (a real `@speed/api-client` over
a fetch stand-in answering genuine `Response` objects, bound into the
api-sdk runtime seam) and assert on the requests observed and the DOM
rendered; `src/usage-example.test.tsx` drives the composed page. The rig
is `test-utils/real-client.ts`, the render harness
`test-utils/render.tsx`.

## Dependencies

| Dependency | Kind | Use |
|---|---|---|
| `@speed/auth-core` | dependency | the `AuthSession` type, the flow operations (`startAuthenticatedFlow`, `answerFlow`, `readFlow`), the shared `useStepUpGate`, and the `./webauthn` passkey driver |
| `@speed/api-sdk` | dependency | the generated react-query hooks, mutations and query-key constructors |
| `@speed/ui-kit` | dependency | `ConfirmDialog`, `EmptyState`, `AsyncSection`, `InlineError`/`errorCodeOf`, `InlineSuccess`, `ListSkeleton`, `useDisplayTimeZone`, `usePhoneCountryDefault` |
| `@speed/i18n` | dependency | namespace registration, the translation hooks, `languageDisplayName`, `formatInstant`, `resolveTimeZone`/`isSupportedTimeZone` |
| `@tanstack/react-query` | peer | the host's QueryClient and its hooks |
| `react`, `react-dom`, `@mui/material`, `@emotion/*` | peer | the host's React/MUI tree |
| `@speed/api-client` | devDependency | public types do not reference it; only the test fixtures bind a real client |

## Known limitations and deferrals

- The passkey ceremony runs through the browser's WebAuthn API
  (`navigator.credentials.create`/`get`, driven by
  `@speed/auth-core/webauthn`), so it needs a secure context with an
  authenticator; where the browser offers none, the step renders its own
  message and the flow cannot proceed. The step waits visibly, can be
  cancelled (best effort, by aborting the ceremony) and retried, and
  abandons a pending ceremony on unmount.
- The step-up dialog covers the factor and challenge steps; a flow that
  starts at the first factor (an account with no usable strengthening
  factor) renders its challenge fields and is answerable, but the
  package's own callers only reach the factor step.
- The enrollment entries are the deployment's own answer: the surface
  reads `GET /api/v1/auth/methods`' `mfa.factors` through `session.listMethods`
  and offers one entry per listed kind (an authenticator app, a passkey, a
  text-message factor, and whatever else an assembly records). A kind
  this package has no wording for is offered under the generic label and
  still starts that kind's flow.

## Development

From this package (`web/packages/account-ui/`), or workspace-wide from
`web/` with `pnpm -r`:

```sh
pnpm lint
pnpm typecheck
pnpm test
pnpm build
```
