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
| `src/ProfileSection.tsx` | `ProfileSection` |
| `src/PasswordSection.tsx` | `PasswordSection`, `PasswordSectionProps` |
| `src/PhoneSection.tsx` | `PhoneSection`, `PhoneSectionProps` |
| `src/IdentifiersSection.tsx` | `IdentifiersSection`, `IdentifiersSectionProps` |
| `src/ContactVerificationSection.tsx` | `ContactVerificationSection`, `ContactVerificationSectionProps` |
| `src/PreferencesSection.tsx` | `PreferencesSection` |
| `src/AppearanceSection.tsx` | `AppearanceSection`, `AppearanceSectionProps`, `ThemePresetChoice` |
| `src/ColorModeSection.tsx` | `ColorModeSection`, `ColorModeSectionProps` |
| `src/SessionsSection.tsx` | `SessionsSection`, `SessionsSectionProps` |
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
  ColorModeSection,
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
        <ProfileSection />
        <PasswordSection session={session} />
        <PhoneSection session={session} />
        <IdentifiersSection session={session} />
        <ContactVerificationSection session={session} />
        <PreferencesSection />
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

These sections cover the account's own self-service writes. The writes that
change how the account signs in or what it proves -- the password change, the
username add, the sign-in way choice, the default contact and the identifier removal -- drive a route
that asks for a fresh authentication: an unelevated caller is answered 401
`authn.insufficient_user_authentication`, which arms the shared
`useStepUpGate` from `@speed/auth-core` and opens `StepUpDialog` (driving a
`purpose: 'step-up'` flow) with that request's inputs remembered; a won
challenge re-runs exactly that request once. `PasswordSection` and
`IdentifiersSection` take the session for that dialog (`IdentifiersSection`
also for the human-verification block its email send presents). Registering and proving
a contact address (the phone send and verify, the email send and verify) asks
for no fresh authentication of its own, so neither section drives the step-up
dialog: `PhoneSection` and `ContactVerificationSection` take the session for
the human-verification block their sends present, not for a step-up. A proof
that would change what recovers or proves the account -- an email that becomes
the default contact or the only address, a phone that replaces the active SMS
factor's target -- is answered 401 `authn.insufficient_user_authentication` and
rendered as a failure. The profile write does not ask for a
fresh authentication either (REQ-account-058), so `ProfileSection` takes no
session.

```tsx
interface PasswordSectionProps {
  readonly session: AuthSession
  readonly withoutLoginName?: 'form' | 'explain'
}
interface PhoneSectionProps {
  readonly session: AuthSession
  readonly humanGate?: AuthnHumanGate // a gate the host already read
}
interface IdentifiersSectionProps {
  readonly session: AuthSession
  readonly emailAvailable?: boolean
  readonly offerUsername?: boolean
  readonly humanGate?: AuthnHumanGate // a gate the host already read
  readonly returnPath?: string // the step-up round trip's return target
  readonly axis?: 'both' | 'signIn' | 'contact' // which grouping this occurrence renders
  readonly showAddButton?: boolean // false leaves the add entry to the host
  readonly openAddSignal?: number // incremented by the host to open the add form
}
interface ContactVerificationSectionProps {
  readonly session: AuthSession
  readonly initialChangeOpen?: boolean // the change-address form starts open
  readonly humanGate?: AuthnHumanGate // a gate the host already read
}
```

`humanGate` is the discovery document's `human` member. Omitted, each
section reads the methods document itself; a send that presents a
verification message renders the `@speed/ui-kit` proof block above its
send control, waits for a proof the deployment requires, and obtains a
fresh one after each send because the proof is single-use.

### ProfileSection

Edits the account's display name through the profile write
`PATCH /api/v1/auth/me` (the same route the preferences section writes its
language and time zone through). It reads `GET /api/v1/auth/me` for the stored
name, lets the field track that value until the user edits it, and
PATCHes a one-field body from the save button, which is disabled while
the field still holds the account's own name (a save with nothing changed
would be a no-op PATCH). A successful save fills a `role="status"` notice
and refetches the profile. The write needs no fresh authentication, so a
refusal renders its code text in that notice's region and no step-up
dialog opens.

```tsx
interface ProfileSectionProps {
  readonly inline?: boolean // commit on blur and Enter, no save button
}
```

### PasswordSection

Changes the password through `PUT /api/v1/auth/password`. The current-password
field renders only when the profile says a password is set (an account
opened through a provider holds none); a provider-only account changes to
its first password with the new-password fields alone, and that form's
helper states the policy instead of the change's "not used before"
sentence. Two client-side guards -- a non-empty new password, a matching
confirmation -- render on their fields before any request; a refusal from
the server renders its code text, except two codes the section answers in its
own words: a 400 `bad-request` is this route's password-policy refusal and
renders the section's policy sentence, and a 401 `login-failed` -- a wrong
current password, or a silent refusal while the account's back-off is in
force -- renders the section's own sentence, which names no cause and tells
the reader to wait after repeated failures, rather than the code's sign-in
text, which names identifiers and verification codes that do not apply here. A
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
A verified number refetches the profile and reports the addition; the write
rotates no credential and revokes no other session.

### IdentifiersSection

Lists the account's identifiers -- read off the profile's `identifiers` field
(`GET /api/v1/auth/me`), because the spec ships no dedicated list route --
grouped the way the design gives them: the sign-in ways and the contact
addresses. Every registered email address and phone number is a contact
address, and being one is not a choice; the sign-in way is a separate choice.
Whether an email or phone signs in is a group decision: the sign-in ways list
is grouped by kind and each group carries its controls -- the username group's
static statement that the name is permanent, the email group's toggle plus a
picker of the proved addresses (an off note takes the picker's place while it
is off), and the phone group's toggle of its representative row. The contacts
list carries no sign-in control; a proved email there still offers the
default-contact choice. A username is a login name with no ownership to prove,
so its group shows no ownership state and no per-row choice. A proved phone that does
not sign in has its group say so ("This phone number is not used to sign
in.") instead of naming it as a sign-in way, so the sentence and the enable
action agree. The `axis` prop narrows an
occurrence to one grouping ('signIn' or 'contact') so a host can split the two
across two panes, and `showAddButton`/`openAddSignal` let the host own the add
entry. An email or phone row carries a two-value
ownership chip read off `proven`: true shows Verified, false shows Not
verified. The chip answers only whether ownership has
been proved, so `verified_via` does not change the answer -- an address an
administrator asserted or an import recorded is an unproved address, not a
third state. `verified_at` cannot answer the ownership question -- every stored
identifier carries one. An unverified email row carries a verify action beside
its remove action, even when the remove action is withheld as the only login
name: the flow shows the row's address in a read-only field, sends a code to
that same address through `POST /api/v1/auth/identifiers`, submits it through
`POST /api/v1/auth/identifiers/verify`, and the refetched row's chip flips to
Verified. The action is withheld while the account's `verification.state` is
`pending`, because that section's flow owns the one pending code, and on a
deployment that serves no email channel. A row's remove action sits behind
the ui-kit danger confirm and
sends `DELETE /api/v1/auth/identifiers/{id}`; a refusal (removing a password
account's last email address is the 409 `login-identifier-required`, and a
removal of the username requested outside this surface is the 409
`authn.username_immutable`) renders its code text and re-reads the profile,
so a client guard that read a stale list converges on the server's answer.
The username itself is never removable: its group states that the name cannot
be changed or removed and renders no action. An account with a password
signs in with a username or an email address: while the profile lists exactly
one of those, that email row's remove action gives way to a note saying
another login name must be added first. Another username/email, or an account
with no password at all, keeps the email action enabled; the server still
judges the last way in.

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
503 `unavailable`). A username add and a removal ask for fresh
authentication -- a 401 `authn.insufficient_user_authentication` opens the
step-up dialog with the write remembered, and a won challenge re-runs that
one write. Registering and proving an email address does not: the send and the
code's verification neither replace the session's credential nor revoke the
account's other sessions. A username add or a removal refetches the profile and
the session list, because those routes revoke the account's other sessions; an
email's verification refetches the profile alone.

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
refetches the profile, so `verification` flips to `verified`; it leaves the
session list alone, because the route revokes no session.

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
field. These writes drive no step-up dialog: a refusal renders as the step's own
failure text -- the automatic send with the `verification.autoStepUp`
explanation -- rather than opening a dialog. A verify that would change what
recovers or proves the account -- an email that becomes the default contact or
the only address -- is answered 401 `authn.insufficient_user_authentication` and
renders the same way, as a failure.

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
banner. The demo account page sets it only for a `?verify=1` landing (the
banner's verify action and the post-registration landing); an ordinary
account visit does not.

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
plus the resolved code text). The write needs no fresh authentication
(REQ-account-058), so the section takes no session and opens no step-up
dialog.

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
  readonly hideLabel?: boolean // the host names the control, e.g. with a row title
}
```

## ColorModeSection

The color-mode picker: one select over the fixed three-valued set --
dark, light, and follow-device. Unlike `AppearanceSection`, the section
owns its read and write. It reads the account's `color_mode` from
`GET /api/v1/auth/me` and writes it back with `PATCH /api/v1/auth/me`,
PATCH-first like `PreferencesSection`: a refusal leaves the control on the
stored value and reports through the section's own feedback region.

The host supplies the preference currently in effect as `effective`,
shown when the account has never saved one, and receives every successful
write through `onApplied` -- which is where the host hands the value to
its theme provider and updates whatever device mirror it keeps for the
first paint.

```tsx
import type { AppThemeModePreference } from '@speed/ui-kit'

interface ColorModeSectionProps {
  readonly effective: AppThemeModePreference
  readonly onApplied: (value: AppThemeModePreference) => void
  readonly hideLabel?: boolean // the host names the control, e.g. with a row title
}
```

The section never reads the theme provider and never touches storage:
applying the preference is the host's half.

## SessionsSection

The sessions-and-devices list, read from `GET /api/v1/auth/sessions` -- the
server lists only live sessions, so a row is never revoked or expired.
The current session (the request's own) carries a badge and no revoke
affordance; every other row has a row-end sign-out button
(`DELETE /api/v1/auth/sessions/{id}`). The "sign out other devices" bulk
action (`DELETE /api/v1/auth/sessions`) revokes every other session at once and
sits behind the ui-kit danger confirm -- a single confirming click, not
the heavier two-step arm, because the action is recoverable (a signed-out
device signs in again); its answer's revoked count is surfaced in a
success notice. Every successful revoke invalidates the
list query, so the rows converge on the server's answer.

The section's one prop is optional. `renderHeaderAction` replaces the
section's own action row with whatever the host returns: the section calls
it with the "sign out other devices" control and renders the returned
node, so a host that owns the surrounding block can lift the action into
the block title's action area -- typically by returning a portal into a
header slot of its own. The slot is called only while the action would
render at all (a settled list holding another live session); without it
the action keeps the section's own placement above the list, so the
default behaviour is unchanged. Handing the control over does not change
when it is busy: the host renders the very control the section would have
rendered, disabled while a revoke is in flight.

```tsx
interface SessionsSectionProps {
  readonly renderHeaderAction?: (action: ReactNode) => ReactNode
}
```

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
unbind is confirmed in a `role="status"` notice below the list, and a
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

The two-step-verification surface. The authn factor routes list,
rename and remove factors (`GET /api/v1/auth/factors`, `PATCH
/api/v1/auth/factors/{id}`, `DELETE /api/v1/auth/factors/{id}`);
registration is not among them -- the one entry is the login engine's
`enroll` purpose -- so the section renders the factors it reads and
offers the enrollment entries THIS deployment carries, each driving a
`purpose: 'enroll'` flow. Which kinds those are is the deployment's own
answer: the section reads `GET /api/v1/auth/methods`'s `mfa.factors` once through
`session.listMethods` and offers one entry per reported kind, so a
deployment whose assembly has no passkey relying party shows no passkey
entry. A known kind (`totp`, `webauthn`, `sms`) renders its translated
name under `mfa.enroll.<kind>`; an unknown kind gets the generic other
label but still starts that kind's own flow.

Each row shows the name the server stored (the kind name when the label
is empty), its status, its creation time and its last successful
verification (a row that has never passed one says so); both times are
formatted with the account's display-timezone chain.

- Enroll. The entry first shows a name form, prefilled with the
  browser/OS summary the session rows show and editable; an empty name is
  allowed and sent as empty, and the row then reads as its kind. The name
  travels with the flow's first input
  (`session.startAuthenticatedFlow({purpose: 'enroll', input: {kind, label}})`,
  because later steps do not read it), and each reply's next step is
  rendered and answered (`session.answerFlow`). A `webauthn` step hands the
  factor its own JSON: TOTP's `otpauth_uri` renders as a scannable QR symbol
  with a manual-entry disclosure that shows the parsed base32 secret and a
  copy control, and a passkey's creation options are driven through the
  browser's WebAuthn API by `@speed/auth-core/webauthn`. The settled
  `enrolled` refetches the factors.
- Rename. A row's inline editor sends `PATCH /api/v1/auth/factors/{id}`
  with the new label. The label is metadata, so no step-up gates it; an
  empty value clears the name and the row reads as its kind. A landed
  rename confirms below the list, naming the name the server stored, and
  refetches the list; a refusal renders its error code there.
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
ADR-web-auth-seams ruling). It drives one
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
| `qrcode-generator` | dependency | encodes the otpauth URI into the QR module matrix the enrolment step draws |
| `@tanstack/react-query` | peer | the host's QueryClient and its hooks |
| `react`, `react-dom`, `@mui/material`, `@emotion/*` | peer | the host's React/MUI tree |
| `@speed/api-client` | devDependency | public types do not reference it; only the test fixtures bind a real client |

## Known limitations and deferrals

- The passkey ceremony runs through the browser's WebAuthn API
  (`navigator.credentials.create`/`get`, driven by
  `@speed/auth-core/webauthn`), so it needs a secure context with an
  authenticator; where the browser offers none, the step renders its own
  message and the flow cannot proceed. An assertion step starts the
  ceremony as it arrives -- the passkey entry was the click -- and starts
  it again when the flow re-sends the same challenge in its next step,
  while a registration step waits for a click. The step waits visibly,
  can be
  cancelled (best effort, by aborting the ceremony) and retried, and
  abandons a pending ceremony on unmount. A device that already holds the
  account's passkey is refused by the browser as `InvalidStateError`; the
  step reports that separately, telling the viewer to use another device
  or an authenticator app rather than retrying the same one, and its
  generic failure sentence names the ceremony that did not complete.
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
