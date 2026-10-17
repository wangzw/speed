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
| `src/PreferencesSection.tsx` | `PreferencesSection` |
| `src/SessionsSection.tsx` | `SessionsSection` |
| `src/SocialBindingsSection.tsx` | `SocialBindingsSection`, `SocialBindingsSectionProps`, `SocialProvider`, `SocialProviderConfig` |
| `src/BindingCallbackHandler.tsx` | `BindingCallbackHandler`, `BindingCallbackHandlerProps` |
| `src/MfaSection.tsx` | `MfaSection`, `MfaSectionProps` |
| `src/resources.ts` | `ACCOUNT_UI_NAMESPACE`, `accountUiResources` |

`src/internal/` holds the family's shared pipeline (the response-model
narrowings, the flow-step renderer, the step-up dialog, the error-text
resolver, the inline error banner and the translation hook) and is
deliberately not exported.

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
  PreferencesSection,
  SessionsSection,
  SocialBindingsSection,
  BindingCallbackHandler,
  MfaSection,
} from '@speed/account-ui'

const queryClient = new QueryClient()

function AccountPage({ session, providers, onAuthorizeUrl }) {
  return (
    <QueryClientProvider client={queryClient}>
      <AppThemeProvider i18n={i18n}>
        <ProfileSection session={session} />
        <PasswordSection session={session} />
        <PhoneSection session={session} />
        <IdentifiersSection session={session} />
        <PreferencesSection />
        <SessionsSection />
        <SocialBindingsSection
          session={session}
          providers={providers}
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

Four sections cover the account's own self-service writes. Each drives a
route that asks for a fresh authentication: an unelevated caller is
answered 401 `authn.insufficient_user_authentication`, which opens the
shared step-up dialog (`src/internal/step-up-challenge.tsx`, driving a
`purpose: 'step-up'` flow) with that request's inputs remembered; a won
challenge re-runs exactly that request once. Each takes the session as
its only prop (the dialog drives it).

```tsx
interface ProfileSectionProps {
  readonly session: AuthSession
}
interface PasswordSectionProps {
  readonly session: AuthSession
}
interface PhoneSectionProps {
  readonly session: AuthSession
}
interface IdentifiersSectionProps {
  readonly session: AuthSession
}
```

### ProfileSection

Edits the account's display name through the profile write
`PATCH /auth/me` (the same route the preferences section writes its
language and time zone through). It reads `GET /auth/me` for the stored
name, lets the field track that value until the user edits it, and
PATCHes a one-field body from the save button, which is disabled while
the field still holds the account's own name (a save with nothing changed
would be a no-op PATCH). A successful save fills a `role="status"` notice
and refetches the profile.

### PasswordSection

Changes the password through `PUT /auth/password`. The current-password
field renders only when the profile says a password is set (an account
opened through a provider holds none); a provider-only account changes to
its first password with the new-password fields alone. Two client-side
guards -- a non-empty new password, a matching confirmation -- render on
their fields before any request; a refusal from the server (a wrong
current password is the 401 `login-failed`) renders its code text. A
successful change clears the fields, refetches the profile and the
session list (the route rotates this session's token and revokes the
others) and reports the change.

### PhoneSection

Adds or replaces the account's phone number in two steps:
`POST /auth/phone` sends a code to the number and `POST /auth/phone/verify`
submits it. The sent notice names the number; the verify carries the code
alone, because the number is the server's own record keyed to the session.
A verified number refetches the profile (and the session list, since the
route revokes the account's other sessions) and reports the addition.

### IdentifiersSection

Lists the account's sign-in identifiers -- read off the profile's
`identifiers` field (`GET /auth/me`), because the spec ships no dedicated
list route -- each with its kind (email, phone or username) and its
verified or unverified state. A row's remove action sits behind the
ui-kit danger confirm and sends `DELETE /auth/identifiers/{id}`; a refusal
(the account's last way in is the 409 `conflict`) renders its code text.

Below the list an add face adds one identifier the caller chooses, its kind
`username` or `email` (a phone is added through its own two-step surface).
A username is a login name with no ownership to prove, so
`POST /auth/identifiers` writes it in one step and the answer reports the
addition. An email is a contact address, so the add sends a code to it (the
answer is state `pending`) and the code is submitted through
`POST /auth/identifiers/verify`; only the matching code writes the address.
The email branch is unavailable on a deployment with no email channel (a
503 `unavailable`). Every write -- add, verify and remove -- asks for fresh
authentication: a 401 `authn.insufficient_user_authentication` opens the
step-up dialog with the write remembered, and a won challenge re-runs that
one write. A write that lands -- a username add, an email's verification or a
removal, not the code-send step, which writes nothing -- refetches the
profile and the session list, because these routes revoke the account's
other sessions.

## PreferencesSection

The language and time zone selects, editing the account's own stored
values through the profile self-write route: it reads `GET /auth/me` for
the stored `locale` and `time_zone` and writes `PATCH /auth/me` with a
one-field body.

- Language options are the i18n instance's own supported set
  (`readSupportedLanguages`); option labels are the language names in the
  current UI language (`Intl.DisplayNames`, the raw tag as fallback). A
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
plus the resolved code text). The section takes no props.

## SessionsSection

The sessions-and-devices list, read from `GET /auth/sessions` -- the
server lists only live sessions, so a row is never revoked or expired.
The current session (the request's own) carries a badge and no revoke
affordance; every other row has a row-end sign-out button
(`DELETE /auth/sessions/{id}`). The section-top "sign out other devices"
action (`DELETE /auth/sessions`) revokes every other session at once and
sits behind the ui-kit danger confirm -- a single confirming click, not
the heavier two-step arm, because the action is recoverable (a signed-out
device signs in again); its answer's revoked count is surfaced in a
success notice. Every successful revoke invalidates the
list query, so the rows converge on the server's answer. The section
takes no props.

## SocialBindingsSection

The social-account bindings surface. The bound identities are the
profile's `links` field (`GET /auth/me`); each row names the provider and
its email. A row with an id has an unlink action
(`DELETE /auth/links/{id}`) behind the ui-kit danger confirm; a landed
unbind is confirmed in a `role="status"` notice above the list, and a
refused one renders its code text there.

The add area renders one button per configured provider that is not
already bound. A channel's label is the method's brand label where the
host supplies one, otherwise the bundle's name for a method the family
knows, otherwise the generic other label. Clicking a button starts a
`purpose: 'link'` flow on the session
(`session.startAuthenticatedFlow`); the flow's first `redirect` step
carries the provider's authorization URL, which is reported upward
through `onAuthorizeUrl` -- the section never navigates. The host
completes the flow at its callback route with `BindingCallbackHandler`.
One bind flow at a time: while a channel's flow is being started every
button is disabled.

```tsx
interface SocialBindingsSectionProps {
  readonly session: AuthSession
  readonly providers: readonly SocialProviderConfig[]
  readonly onAuthorizeUrl?: (url: string) => void
}
```

`SocialProvider` is a method-name `string` -- the set is the
deployment's, not a closed union (a social channel is a login-method
name such as `wechat.web`) -- and `SocialProviderConfig` carries the
method's brand label. Both are this package's own definitions, shaped
identically to `@speed/auth-ui`'s and kept in sync with them -- same-tier
packages never import each other.

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
remove factors (`GET /auth/factors`, `DELETE /auth/factors/{id}`);
registration is not among them -- the one entry is the login engine's
`enroll` purpose -- so the section renders the factors it reads and
offers the enrollment entries THIS deployment carries, each driving a
`purpose: 'enroll'` flow. Which kinds those are is the deployment's own
answer: the section reads `GET /auth/methods`' `mfa.factors` once through
`session.listMethods` and offers only the kinds it lists, so a deployment
whose assembly has no passkey relying party shows no passkey entry.

- Enroll. `session.startAuthenticatedFlow({purpose: 'enroll', input: {kind}})`
  opens the wizard; each reply's next step is rendered and answered
  (`session.answerFlow`). A `webauthn` step hands the factor its own JSON:
  TOTP's `otpauth_uri` renders as text (the package ships no QR
  dependency) beside a code field, and a passkey's creation options are
  driven through the browser's WebAuthn API by `src/internal/webauthn.ts`.
  The settled `enrolled` refetches the factors.
- Remove. A row's action sends `DELETE /auth/factors/{id}` behind the
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

The step-up dialog (`src/internal/step-up-challenge.tsx`) drives one
`purpose: 'step-up'` flow: an account with a usable factor enters at the
`factor` step and chooses one, where the flow then asks for its proof; a
completion in state `elevated` (or the login-shaped `authenticated` body
of a cookie-delivered session) fires its `onSuccess`, so the caller
re-runs the gated action. An account with no active factor gets the
dialog's text explaining that two-step verification must be registered
first, and no flow is started; the dialog renders text only and never a
link -- these sections are the account settings surface, so nothing here
navigates.

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
| `@speed/auth-core` | dependency | the `AuthSession` type and the flow operations (`startAuthenticatedFlow`, `answerFlow`, `readFlow`) |
| `@speed/api-sdk` | dependency | the generated react-query hooks, mutations and query-key constructors |
| `@speed/ui-kit` | dependency | `ConfirmDialog`, `EmptyState`, `AsyncSection`, `InlineError`/`errorCodeOf`, `InlineSuccess`, `ListSkeleton` |
| `@speed/i18n` | dependency | namespace registration and the translation hooks |
| `@tanstack/react-query` | peer | the host's QueryClient and its hooks |
| `react`, `react-dom`, `@mui/material`, `@emotion/*` | peer | the host's React/MUI tree |
| `@speed/api-client` | devDependency | public types do not reference it; only the test fixtures bind a real client |

## Known limitations and deferrals

- The passkey ceremony runs through the browser's WebAuthn API
  (`navigator.credentials.create`/`get`, driven by `src/internal/webauthn.ts`),
  so it needs a secure context with an authenticator; where the browser
  offers none, the step renders its own message and the flow cannot proceed.
- The step-up dialog covers the factor and challenge steps; a flow that
  starts at the first factor (an account with no usable strengthening
  factor) renders its challenge fields and is answerable, but the
  package's own callers only reach the factor step.
- The enrollment entries are the deployment's own answer: the surface
  reads `GET /auth/methods`' `mfa.factors` through `session.listMethods`
  and offers only the kinds it lists (an authenticator app, a passkey, and
  whatever else an assembly records).

## Development

From this package (`web/packages/account-ui/`), or workspace-wide from
`web/` with `pnpm -r`:

```sh
pnpm lint
pnpm typecheck
pnpm test
pnpm build
```
