# @speed/auth-ui

The sign-in component family of a frontend built on speed: the
deployment's login methods, read from the discovery document and grouped
by kind -- the password and SMS-code forms behind a tab strip, the social
sign-in section and the callback handler that resumes its flow, the
enterprise email-first entry, the passkey ceremony -- plus the sign-out
action and the session-ended placeholder, assembled in `SignInScreen`.
Every piece is a controlled component over an `@speed/auth-core` session
passed in as a prop, at the same tier as `ui-kit` and `layout-kit`:
components never consume the auth-core hooks, never read session state,
never attach or persist a session, never navigate, and never touch the
network directly -- a successful sign-in fires the host's `onSignedIn`
callback once, and everything that happens next is the host's. Every host
callback runs only after the operation's verdict settled, and a throwing
callback is contained: it is not a failure of the operation it follows --
no error banner, no re-classified outcome, no unhandled rejection --
because the commit already happened. Every built-in string renders from
the bilingual `auth-ui` namespace registered through `@speed/i18n`, and
the form fields render through `ui-kit`'s `FormField`/`FormLayout`, the
same discipline the other packages established.

There is no registration form. An account has no signup endpoint of its
own: it is created inside a login flow's account-resolution step (under the
deployment's signup setting), or by an invitation, or by an administrator.
A "registration" is therefore a login flow's outcome, not a form this
package ships.

## What ships

| Module | Exports |
|---|---|
| `SignInScreen.tsx` | `SignInScreen`, `SignInScreenProps`, `SignInContext`, `SignInChannel`, `SignInMode`, `SocialSignInOptions` |
| `PasswordSignInForm.tsx` | `PasswordSignInForm`, `PasswordSignInFormProps` |
| `SMSSignInForm.tsx` | `SMSSignInForm`, `SMSSignInFormProps` |
| `SocialSignInSection.tsx` | `SocialSignInSection`, `SocialSignInSectionProps`, `SocialProvider`, `SocialProviderConfig` |
| `SocialCallbackHandler.tsx` | `SocialCallbackHandler`, `SocialCallbackHandlerProps` |
| `SignOutButton.tsx` | `SignOutButton`, `SignOutButtonProps` |
| `SessionEndedScreen.tsx` | `SessionEndedScreen`, `SessionEndedScreenProps` |
| `AcceptInvitationScreen.tsx` | `AcceptInvitationScreen`, `AcceptInvitationScreenProps`, `AcceptInvitationInput`, `InvitationOperations`, `InvitationSummary` |
| `ImpersonationBanner.tsx` | `ImpersonationBanner`, `ImpersonationBannerProps`, `ImpersonationBannerState` |
| `ImpersonationNotice.tsx` | `ImpersonationNoticeProvider`, `ImpersonationNoticeProviderProps`, `ImpersonationIndicator`, `ImpersonationEndMenuItem`, `ImpersonationEndMenuItemProps`, `useImpersonationNotice`, `ImpersonationNotice` |
| `resources.ts` | `AUTH_UI_NAMESPACE`, `authUiResources` |

Everything else (`src/internal/`) is shared plumbing -- the flow-stepping
driver, the step renderer, the code-to-text error resolver, the
whole-attempt failure banner, the translation hook -- and is deliberately
not exported.

## Quick start

A host composes the family over the session-and-client wiring
`@speed/auth-core`'s README documents. The bootstrap has four parts: the
bilingual i18n instance with both namespaces a rendered form can read, the
client and session over one memory access-token store, the session
attached to the hooks, and the host gate that switches between the sign-in
surface, the app and the session-ended placeholder on the `useAuthState`
snapshot.

```tsx
import { useEffect, useState } from 'react'
import {
  createClient,
  createMemoryAccessTokenStore,
} from '@speed/api-client'
import { bindRequestFn } from '@speed/api-sdk/runtime'
import { authnProfileGet } from '@speed/api-sdk'
import {
  createAuthSession,
  attachSession,
  useAuthState,
} from '@speed/auth-core'
import { createI18n, I18nextProvider, registerNamespace } from '@speed/i18n'
import {
  AppThemeProvider,
  UI_KIT_NAMESPACE,
  uiKitResources,
} from '@speed/ui-kit'
import {
  AUTH_UI_NAMESPACE,
  authUiResources,
  SessionEndedScreen,
  SignInScreen,
} from '@speed/auth-ui'

// 1. The session and the client share one memory access-token store.
//    refreshAccessToken is the session's silent refresh: an expired-token
//    401 on any request runs it once and retries with the fresh token.
const store = createMemoryAccessTokenStore()
const session = createAuthSession(store)
bindRequestFn(
  createClient({
    baseUrl: 'https://api.example.com',
    fetch: fetchImpl, // the host's fetch implementation
    accessTokenStore: store,
    refreshAccessToken: () => session.refresh(),
  }),
)

// 2. The hooks read the attached session (last bind wins).
attachSession(session)

// 3. The bilingual instance, both namespaces registered exactly once:
//    the auth-ui namespace for this package's strings, and the ui-kit
//    namespace because FormField's validation text lives there.
const i18n = createI18n({
  supportedLanguages: ['zh-CN', 'en-US'],
  defaultLanguage: 'zh-CN',
  storage: null,
  urlParameterName: null,
  navigatorLanguages: [],
})
registerNamespace(i18n, AUTH_UI_NAMESPACE, authUiResources)
registerNamespace(i18n, UI_KIT_NAMESPACE, uiKitResources)

// 4. The host's router in miniature: while the snapshot is authenticated
//    the app renders; an anonymous snapshot at a view that held an
//    authenticated one is the session-ended state; before the first
//    authentication it is the sign-in surface. The gate observes -- no
//    component in this family signals into the host's router.
function AppGate() {
  const snapshot = useAuthState()
  const [reachedApp, setReachedApp] = useState(false)
  useEffect(() => {
    if (snapshot.state === 'authenticated') {
      setReachedApp(true)
    }
  }, [snapshot.state])

  if (snapshot.state === 'authenticated') {
    return <ProtectedArea />
  }
  if (reachedApp) {
    return <SessionEndedScreen onSignIn={() => setReachedApp(false)} />
  }
  return <SignInScreen session={session} />
}

// The host's app view: protected content that reads the caller's own
// profile through the generated /me operation, routed through the bound
// client.
function ProtectedArea() {
  const [reachable, setReachable] = useState<boolean | null>(null)
  return (
    <div>
      <p>Signed in</p>
      <button
        type="button"
        onClick={() => void authnProfileGet().then(() => setReachable(true))}
      >
        Check session
      </button>
      {reachable === true ? <p>Session ok</p> : null}
    </div>
  )
}

export function App() {
  return (
    <I18nextProvider i18n={i18n}>
      <AppThemeProvider i18n={i18n}>
        <AppGate />
      </AppThemeProvider>
    </I18nextProvider>
  )
}
```

`registerNamespace` validates before it mutates (identical leaf key
sets across both languages) and must run exactly once per instance, same
as every other speed namespace. This exact composition is compiled and
executed by the package suite (`src/usage-example.test.tsx`) over a real
`@speed/api-client`: its fetch stand-in answers with genuine `Response`
objects, and the journey drives a password sign-in (a login flow started
and taken to its ready step's result, the store then holding the issued
access token), a protected request refused with `authn.token_expired` that
the client silently refreshes, and a later refusal that converges the
snapshot to anonymous -- the gate's anonymous snapshot at the app view is
the session-ended screen. Signing in again returns to the form, and a
`switchLanguage` leg re-renders the same surface in the other supported
language. The documented usage cannot drift from the API.

## How a sign-in is driven

A login is a server-side flow, not one request. Every channel form begins
one with `session.startFlow` and advances it with `session.answerFlow`
until it settles; only auth-core commits the session (a token-issuing
completion), so no component here holds login state.

A step that does not complete immediately -- a forced password change, a
required second factor, a factor to register, a passkey ceremony -- is
rendered by the shared step renderer (`src/internal/flow-step.tsx`): a
`challenge` step renders one input per field the server asks for, a
`factor` step renders one button per offered second factor, an `enroll`
step one button per registerable kind, a `webauthn` step drives the
browser's WebAuthn ceremony (`src/internal/webauthn.ts`) from its
public-key options and reports the credential it returns, and a `ready` step (whose
`next.kind` is `complete`) is
taken automatically with `step: 'complete'`. A `redirect` step reaching the
renderer (a callback route opened before the provider answered) renders an
unavailable notice; the host owns navigation. The answer
step name is a function of the record's live state (`answer` in `first`,
`factor` in `second`, `enroll` in `enroll`, `complete` in `ready`), never
of the rendered kind, so a factor's own challenge is still answered with
`factor` -- the driver carries the chosen factor id (and an enroll's
`kind`) into that answer.

A submit whose own completion lost a concurrent race is not a failure:
auth-core rejects it with `OperationSupersededError` and the session is
authenticated under the *winner's* identity, so the driver stays silent --
no error banner, no `onSignedIn`, no retry of a one-shot side effect for
an identity the session is not running under. The host observes the
winner's session through its own auth-core hooks.

A step the server answers `authn.flow_invalid` (the flow expired, was
consumed by a concurrent submit, or is bound to another browser) cannot be
answered any more: the form returns to its channel's first step to start a
fresh flow, with the code's text in the banner.

## SignInScreen

The assembled sign-in surface. On mount it reads the deployment's login
methods from the discovery document (`session.listMethods`; pass a
`discovery` document to inject a pre-fetched one and skip the read) and
groups the descriptors by `kind`:

- `form` methods gather behind a tab strip (a single one renders without a
  strip -- a tablist with nothing to switch between is not a choice);
  password and SMS code keep their own forms, and any other form method is
  driven by its descriptor's `fields`. Switching tabs unmounts the previous
  form, so its half-typed state, parked step and whole-attempt error are
  gone with it -- a deliberate reset, including between two
  descriptor-driven forms: channel errors must not leak across surfaces.
- `redirect` methods render as buttons in the social block.
- `enterprise` methods render an email-first entry that discovers the
  viewer's identity providers (`session.discoverSso`).
- `webauthn` methods render a passkey button and its ceremony step.

An unknown `kind` is ignored; a method name the family does not know still
renders through its descriptor. A successful sign-in on a channel the screen
drives itself -- a form or the passkey ceremony -- fires `onSignedIn` once
with the mode and that channel's method name (`SignInContext`). Redirect and
enterprise methods complete through the callback route instead: the screen
hands their authorization URLs to the host and fires no `onSignedIn`. The
screen renders no heading of its own: the page above (branding, the heading)
is host content. `mode` is a presentational switch: a "register" entry
appears (with a "sign in instead" entry back) where the deployment lets a
first login open an account, but it runs the one sign-in flow -- the account
opens server-side. The register hint says an account is created on first use
only where the chosen method can create one; the password method does not
open accounts, so the hint does not claim it does. The register entry itself
counts only the methods the screen renders: a method with an unknown `kind`,
or a form channel narrowed away by the deprecated `channels` prop, cannot
make it appear.

While the read is in flight a `role="status"` notice shows; a failed read
renders its error code in a banner with a retry button. The tab strip is
wired to the mounted channel panel the ARIA tabs way: each `Tab` carries an
id and `aria-controls` naming its own panel, and the mounted channel
renders as the `role="tabpanel"` with that id and the tab as its
`aria-labelledby` -- only the active panel exists in the DOM, since
switching unmounts the previous form.

| Prop | Type | Notes |
|---|---|---|
| `session` | `AuthSession` (required) | the session every channel on the screen drives |
| `discovery` | `AuthnMethodList` | a pre-fetched discovery document to render instead of reading one; when given the screen fires no read |
| `mode` | `'sign-in' \| 'register'` | the presentational mode; defaults to `'sign-in'` |
| `social` | `SocialSignInOptions` | the redirect / enterprise block host config; omitted to run without one |
| `onSignedIn` | `(context: SignInContext) => void` | fired once after a sign-in commits on a channel the screen drives (a form or the passkey ceremony), with the mode and the channel's method name; redirect and enterprise methods complete through the callback route and fire none |
| `channels` | `readonly ('password' \| 'sms')[]` | **deprecated**: still narrows the form strip for one major |
| `defaultChannel` | `'password' \| 'sms'` | **deprecated**: the channel selected on first render |

`SocialSignInOptions` carries the host callback route (sent as a redirect or
enterprise flow's `return_to`) and the same `onAuthorizeUrl` seam
`SocialSignInSection` takes. Its deprecated `providers` still overrides the
redirect method set:

```ts
export interface SocialSignInOptions {
  readonly providers?: readonly SocialProviderConfig[] // deprecated: the set comes from discovery
  readonly returnTo?: string
  readonly onAuthorizeUrl?: (provider: string, authorizeUrl: string) => void
}
```

## The channel forms

Each form is a controlled react-hook-form flow over one method of the
login surface, rendered through `ui-kit`'s `FormLayout`/`FormField` (the
skeleton owns the `<form>`, validation-error rendering and the actions
row; the component owns the submission, the busy state and the
whole-attempt failure banner). Fields carry channel-appropriate
`autoComplete` values. A failed submit renders one `role="alert"` banner
resolving the answer's error code to current-language text (see Text and
i18n below), except the password channel's collapsed credential refusal,
which is the password field's own message (see PasswordSignInForm); a
successful submit fires `onSignedIn` once and the host navigates. None of
them reads the session snapshot or renders a title.

### PasswordSignInForm

One identifier field -- email or username, the two identifiers the account
contract accepts as a password login name; a phone number is not one, and
the SMS channel is where it belongs -- and a password field, sent as a
`method: 'password'` flow's first step. A forced password change or a
second factor the flow then parks on is rendered by the shared step
renderer in place of these fields.

A refused credential submit (`authn.login_failed`) is rendered as the
password field's own message (`passwordSignIn.loginFailed`): the server
collapses an unknown identifier and a wrong password into that one code, so
the text names the password without saying which half did not match, and
this surface never mentions a code field it does not have. The message is
the field's helper text -- `aria-invalid` on the input, `aria-describedby`
pointing at the message -- and focus moves to the password field when the
refusal lands. Every other code renders through the whole-attempt banner.

| Prop | Type | Notes |
|---|---|---|
| `session` | `AuthSession` (required) | the session the password login drives |
| `onSignedIn` | `() => void` | fired once after a password login commits |

### SMSSignInForm

A two-step flow over `method: 'sms'`: the phone step begins it (the
method sends the code and answers with a challenge asking for it), then
the code step answers that challenge. The `challenge` step is what tells
the form a code was issued, so the two steps are this form's own UI and
the sent notice announces the receiving number (`role="status"`); resend
begins the flow again against the same number; changing the phone returns
to the first step. A fresh code starts the code field empty: a successful
send (the first, a resend, or a request for a changed phone) resets any
code typed against the one it invalidates, so a stale code can never ride
along to a new code session. A wrong or expired code is refused as
`authn.login_failed` (a spent login code reads exactly like a wrong one,
deliberately).

The phone step uses the shared two-part control
(`PhoneNumberField` from `@speed/ui-kit`): a country/region selector and
a local number, assembled into the E.164 string the flow takes. The
selector starts on the country the browser locale names (zh-CN -> +86,
en-US -> +1) and searches by country name, ISO code or calling code; a
pasted international number switches it and fills the local part itself.
The number is validated per country before a request: a number too short
for its country is refused locally with the calling code named, and never
reaches the server. The server's refusal
copy still renders for the failures only it can judge (an unsupported
region, a send-policy refusal).

A code answer whose login lost a concurrent race answers
`OperationSupersededError`; the code it verified is single-use
server-side, so the losing submit spent it. The form renders the used-code
notice (`smsSignIn.codeUsed`), clears the code field, and refuses a
re-submission of that exact code locally -- the server would only answer
the spent code with the same collapsed refusal, and the form knows better.
Only a fresh code (a resend, or a request for a changed phone) can sign in.

| Prop | Type | Notes |
|---|---|---|
| `session` | `AuthSession` (required) | the session the SMS sign-in drives |
| `onSignedIn` | `() => void` | fired once after an SMS-code login commits |

## Social sign-in

### SocialSignInSection

One outlined button per configured provider, each answering the method's
brand label where the discovery document gives one (a provider's own name,
not translated), otherwise the bundle's name for it
(`social.provider.<provider>`). A brand whose deployment reaches it through
several sign-in modes carries the same label on each descriptor; the
bundle's name-keyed wording then wins over the label, so those buttons stay
distinguishable and each one's text is its accessible name. The provider
set is the deployment's;
`SignInScreen` derives it from the discovery document's redirect methods,
and a host may still drive this section directly with any method names.
Clicking a provider begins
that method's login flow (`session.startFlow`, the provider as the flow's
`method`); the flow's first step for a social method is a `redirect` step
carrying the provider's authorization URL, reported upward through
`onAuthorizeUrl` and the host decides what it is for (a redirect in the
host's router, a popup, a new tab). Each flight is tracked per provider:
while one request is in flight its own button disables and the others stay
live, and an earlier attempt's return can never re-enable a later
provider's button mid-flight. A refused start (an unknown or disabled
method, an account-unavailable answer) renders through the one banner --
but the banner belongs to the newest attempt only (attempts are numbered
at start; a failure writes its code only while it is still the newest
attempt, and each new attempt clears the banner), so an earlier attempt's
failure can never paint over a newer attempt's success or linger past it.

The provider's own redirect address is the server's: the method builds
`redirect_uri` from the deployment's public URL, so the client sends no
redirect_uri. What the config's `redirectUri` names is the host's own
callback route, and it is sent as the flow's `return_to` so the provider's
round trip lands the browser back on it; an empty `redirectUri` sends no
`return_to` and lets the server use its configured default.

```ts
export type SocialProvider = string // a method name; the set is the deployment's

export interface SocialProviderConfig {
  readonly provider: SocialProvider
  readonly label?: string   // the method's brand name, from the discovery document
  readonly redirectUri: string
}
```

| Prop | Type | Notes |
|---|---|---|
| `session` | `AuthSession` (required) | the session that begins each channel's flow |
| `providers` | `readonly SocialProviderConfig[]` (required) | the channels to render, in the order the host wants |
| `onAuthorizeUrl` | `(provider, authorizeUrl) => void` | receives the channel's authorization URL once the flow hands it over; the host navigates |

### SocialCallbackHandler

Resumes a social sign-in at the host's callback route. The server handed
the provider's redirect and answered it, so the browser lands back on the
flow's `return_to`; on mount the handler reads the flow back
(`session.readFlow`). A flow that is ready (`next.kind = complete`) is
taken with `step: 'complete'`, the login lands, and the handler fires
`onSignedIn` once; a flow still parked on a step (a required second
factor) is rendered by the shared step renderer and advanced in place. A
mount that finds no flow -- the binding is gone, the flow is done or
expired -- renders the failure banner with a retry button that reads once
more. A mount that finds the session already authenticated is a re-entry
to the callback route after a completed login: no read is started and the
handler fires `onSignedIn` again so the host bounces the viewer onward.
While the read is in flight the handler shows the pending notice
(`role="status"`). The callback route is the host's own; nothing here
navigates.

| Prop | Type | Notes |
|---|---|---|
| `session` | `AuthSession` (required) | the session that resumes the flow |
| `onSignedIn` | `() => void` | fired once after the login lands -- and again on a later mount that finds the session already authenticated |

## SignOutButton

The sign-out action over `session.logout()`. A click drives the logout;
while the request is in flight the button disables and shows the busy
label; a refused logout renders the answer's code text in one banner.
Logout is terminal either way: auth-core clears the local session and
flips the snapshot to anonymous whether or not the server's revocation
answered (a session it could not revoke is no longer trustworthy
locally), so the host's own auth-core hooks observe the same flip on both
paths -- the component never navigates and fires no completion callback.
It renders whether or not the snapshot is authenticated; the host mounts
it where a sign-out action belongs (typically app chrome). The button
renders with `color="inherit"`: mounted inside a coloured AppBar (whose
background is the primary colour and whose own text is its contrastText),
an inherited colour stays legible where a primary-coloured default would
read primary-on-primary on the very surface the control usually sits on.

| Prop | Type | Notes |
|---|---|---|
| `session` | `AuthSession` (required) | the session the sign-out drives |

## SessionEndedScreen

The pure placeholder a host renders once protected content is gone -- the
view's authenticated snapshot just turned anonymous. The session lifecycle
is observable, not imperative: there is no recovery and no refresh cookie,
so a page reload starts anonymous, and a server-side session death
converges through the api-client refresh leg -- `refresh()` resolving
`false` flips the snapshot to anonymous. The host watches its own
auth-core hooks and, at any view that just lost its authenticated
snapshot, mounts this screen; its action hands the host back to its
sign-in surface.

The screen is pure presentation: no session prop, no hooks, no network.
It renders `ui-kit`'s `EmptyState` in the `noPermission` variant -- the
lock icon, because the content is gated again until the user signs in --
with every text slot overridden from the auth-ui namespace, so nothing of
`ui-kit`'s built-in texts can leak through.

The screen replaces the whole authenticated page (product-shell's ended
branch mounts it with no frame and no ancestor heading), so its title is
the page's own heading: `ui-kit`'s `EmptyState` heading level is forwarded
through `headingLevel`, defaulting to `h1` -- the level that continues
nothing, which is what a whole-page placeholder needs -- and a host
embedding the screen under a heading of its own passes the level that
continues the page's order.

| Prop | Type | Notes |
|---|---|---|
| `onSignIn` | `() => void` (required) | fired when the viewer asks to sign in again; the host navigates |
| `headingLevel` | `EmptyStateProps['headingLevel']` | the real heading element the title renders as; defaults to `'h1'` |

## Impersonation notice

Four pieces over one host-owned state: the visible banner, the provider
that holds its dismissal, the header chip that toggles it, and the
account-menu item that ends the impersonation. All of them are pure
presentation: no session, no hook, no request. The host says whether an
impersonation is current (`impersonation`, or `null`), starts its one
end write attempt (`onEnd`), and reports that attempt's state
(`pending`, `failureCode`, `successMessage`). The banner, the chip and
the menu item are the same attempt's entry points: while `pending`, the
banner's control and the menu item are disabled and labelled busy.

One polite live region is mounted for the banner's whole life, never
conditionally: empty while idle, filled with the "acting as" announcement
when the impersonation appears, and with the end confirmation once the
attempt settles -- so the confirmation outlives the visible banner when
the host clears `impersonation` after re-reading the session. A visible
impersonation renders a named `region` landmark stating who the operator
is acting as, the tenant and expiry when the host read them, and that
management features are unavailable. The banner adds no live region of
its own, so the failure alert inside it is announced once.

`dismissed` unmounts the visible region but not the live region: the
host holds the dismissal, and `onDismiss` (the close control, named
"Hide impersonation notice") reports the click. `ImpersonationIndicator`
is the compact chip the host mounts in the header's user-menu slot: it
renders for the whole impersonation, exposes `aria-expanded` and
`aria-controls`, and toggles the banner; the host moves focus to it when
the banner is dismissed. `ImpersonationEndMenuItem` is the account menu's
end entry, reading the same provider state.

The visible notice does not depend on `tenant` or `expiresAt`: those are
optional, and the end control renders and works without them. A bare 404
meaning "there is no impersonation to end" is the host's concern -- its
attempt treats that as settled -- and these components inspect no HTTP
status.

| Prop | Type | Notes |
|---|---|---|
| `impersonation` | `ImpersonationBannerState \| null` (required) | `{ subject, tenant?, expiresAt?, grantId? }`; `subject` is the impersonated account's display name, the rest come from the host's status read. A new `grantId` is a new impersonation, which resets a dismissal |
| `onEnd` | `() => void` (required) | starts the host's one end-impersonation write attempt; its result arrives through the three props below |
| `pending` | `boolean` | whether the shared attempt is in flight; the end control is disabled and marked busy |
| `failureCode` | `string \| null` | the attempt's failure code, rendered inside the banner |
| `successMessage` | `string \| null` | the attempt's success message, announced in the permanent live region |
| `dismissed` | `boolean` | unmounts the visible region; the live region stays mounted and keeps announcing |
| `onDismiss` | `() => void` | renders the close control and reports its activation |
| `regionId` | `string` | the visible region's id, named by the indicator's `aria-controls` |

| Provider / consumer | Props | Notes |
|---|---|---|
| `ImpersonationNoticeProvider` | `impersonation`, `identityKey?`, `onEnd`, `pending?`, `failureCode?`, `successMessage?`, `children` | holds the dismissal and the region id; a new grant id between two known values, an identity change, an end or a failed attempt resets the dismissal |
| `ImpersonationIndicator` | `impersonation`, `expanded`, `controls?`, `onToggle`, `ref?` | the header chip; its visible "Impersonating" text is its accessible name (the tooltip is a description) |
| `ImpersonationEndMenuItem` | `onActivate?`, `divider?` | the account menu's end entry; renders nothing outside an impersonation or the provider |

## Text and i18n

All built-in strings live in the bilingual `auth-ui` namespace
(`src/locales/zh-CN.json` and `en-US.json`, identical leaf key sets,
enforced by registration):

| Section | Purpose |
|---|---|
| `passwordSignIn.*` | the password channel: title, field labels (identifier, password, new password), submit, the credential refusal |
| `smsSignIn.*` | the SMS channel: title, country/phone labels, the phone hint, send/resubmit/change-phone actions, the sent notice (the assembled E.164 number), the used-code notice. The per-country validation messages live in the `ui-kit` namespace (`phoneNumber.*`) |
| `signInFlow.*` | the step renderer: the continue action, the factor/enroll titles, the factor-kind names (a guarded closed set plus an "other" label), the passkey prompt for a login and for a registration, the waiting/cancel/retry controls, and its unsupported/cancelled/failed messages, the unavailable-step notice |
| `discovery.*` | the sign-in surface: the loading notice and the retry action |
| `mode.*` | the sign-in surface: the register / sign-in entries and the register hint |
| `enterprise.*` | the enterprise entry: the email label, the continue action, the provider-picker title and the no-match notice |
| `passkey.*` | the passkey entry: the divider title |
| `social.*` | the social section: the divider title and one name per provider (`social.provider.<provider>`), including the per-mode names for a brand with several sign-in modes |
| `socialCallback.*` | the callback handler: the pending notice, the retry action |
| `signOut.*` | the sign-out button: label and busy state |
| `sessionEnded.*` | the session-ended screen: title, description, sign-in-again action |
| `acceptInvitation.*` | the invitation landing: title, loading notice, tenant/email/expiry facts, the accept action, the retry action, and the accepted and not-found states |
| `impersonation.*` | the impersonation notice: the region name, the acting-as / tenant / expiry statements, the management-unavailable notice, the end action with its ending and ended messages, the hide control's name, and the header chip's visible text with its hide/show hints |
| `errors.*` | code-to-text answers (see below) |

Hosts reword the kit by registering their own identical-key bundle pair
under `AUTH_UI_NAMESPACE` at bootstrap -- never by editing component text.
`MUI` components themselves carry no speed text.

### Error text: the reachable-code whitelist

Every submit path of the family resolves its failure to one error code and
renders it through the same whole-attempt `role="alert"` banner
(`InlineError`), never per-field error prose. The resolver maps exactly
these codes to their `errors.*` keys -- the login-flow problem set the
login-flow endpoints answer with, plus the transport codes of the
`@speed/api-client` contract:

| Area | Codes with dedicated text |
|---|---|
| Login flow (a refused credential, an expired or consumed flow, a disabled method, an unavailable account, a conflict, a throttle, an unavailable dependency, an unsupported phone region, a malformed body) | `authn.login_failed`, `authn.flow_invalid`, `authn.link_required`, `authn.method_disabled`, `authn.account_unavailable`, `authn.conflict`, `authn.throttled`, `authn.unavailable`, `authn.unsupported_region`, `authn.bad_request` |
| Transport (the api-client contract) | `client.network`, `client.timeout`, `client.protocol` |

Anything outside the whitelist -- a future authn code, a
`client.http.<status>` answer, a non-`ApiError` throw -- renders the
`errors.unknown` fallback, so the bundle can never show a raw key, and a
missing translation never leaks another language's text or an English
fallback. The family's classifier (`errorCodeOf`) collapses non-`ApiError`
shaped throws to `client.unknown`, a code deliberately not whitelisted, so
the fallback is where they land: a submit that throws at all always has a
code to show. The SMS channel's used-code notice is a bundle key of its
own, never a server code: the server cannot tell a spent login code apart
from a wrong one without becoming an oracle, so the honest "used" verdict
lives only in the form's own memory.

## Accessibility

Component tests run axe over the rendered document and fail on any
violation, with two recorded exceptions shared with `ui-kit`:
`color-contrast` is disabled because jsdom computes no layout or color (a
contrast result there is neither trustworthy nor actionable -- the theme
owns contrast and is verified browser-side), and `region` is disabled
because the units under test are components, not full app pages.
Interaction semantics are asserted by the tests rather than left to axe:
sent notices and the social-callback and passkey-waiting states announce
through `role="status"` containers mounted empty and filled a commit later
(the pending spinners are `aria-hidden`, so the notice is not read twice;
entering the passkey waiting state moves focus to its cancel control),
every failure renders in one `role="alert"`,
buttons disable while their request is in flight, the channel strip is MUI
`Tabs` (arrow-key navigation, labelled by the channel titles) whose tabs
each carry an `id` and the `aria-controls` of their own panel -- the
mounted channel renders as the `role="tabpanel"` with that id and the tab
as its `aria-labelledby`, so the tabs never point at nothing -- and each
field carries the `autoComplete` value matching its channel (`username`,
`current-password`, `tel`, `one-time-code`) plus `inputMode="numeric"`
for the SMS code.

## Testing

Unit tests are vitest + jsdom, one file per source file under `src/`,
shared helpers only in `test-utils/`. The vitest config aliases the
`@speed/*` specifiers onto the sibling packages' `src` entries, so tests
run against live sources -- no test file imports another package's
`dist/`. Three layers of helpers, mirroring `@speed/auth-core`'s:

- `render.tsx` -- `renderWithProviders` mounts a unit under the tree a
  real host builds (`I18nextProvider` around `AppThemeProvider`), with a
  fresh bilingual instance per call (the double-registration guard never
  fires across tests) and both namespaces a rendered form can read
  registered; `createAuthUiI18n` is the shared instance factory. Bilingual
  assertions import the shipped bundles (`../locales/zh-CN.json`,
  `en-US.json`), never inline a language literal.
- `@speed/test-utils/session-harness` -- `makeHarness` drives component
  tests' sessions: a scripted fake `RequestFn` bound through the same
  `bindRequestFn` seam a host's real client binds, a real session over a
  fresh memory store, and assertions on observable state only (store
  tokens, request bodies, raw `ApiError`s). Its endpoint constants and
  reply builders (`openFlow`, `completeFlow`, `authenticatedCompletion`,
  `principal`, ...) are the flow model's own.
- `real-client.ts` + `session-gate.tsx` -- the journey rig:
  `makeRealClientRig` builds a real `@speed/api-client` `createClient`
  whose fetch stand-in answers from a script with genuine `Response`
  objects, recording every request's method, path, authorization header
  and raw body; because the transport is real api-client machinery, the
  401-refresh leg is exercised by the client itself rather than scripted
  around. `SessionGate` is the host's router in miniature, reading the
  snapshot of the attached session and switching between host-provided app
  content, the sign-in surface and the session-ended screen.

`src/usage-example.test.tsx` compiles and executes the Quick start
composition above end to end over the real-client rig (the requests
pinned in order: the password flow's start, its ready step's result, the
profile read, a refused and refreshed protected call, a refused refresh
converging to the session-ended screen, and the sign-in-again turn), and
`src/session-journey.test.tsx` drives the journeys beyond the example --
the SMS channel, an explicit sign-out and the social callback route --
over the same rig and gate. Every component test asserts axe and the
bilingual text of its states.

## Dependencies

| Package | Kind | Why |
|---|---|---|
| `react`, `react-dom` | peer (required, ^18 or ^19) | the host owns the React tree |
| `@mui/material` | peer (required, ^9) | the `TextField`/`Button`/`Tabs`/`Alert` primitives and the ambient theme |
| `@emotion/react`, `@emotion/styled` | peer (required, ^11) | MUI's own runtime requirements |
| `react-hook-form` | peer (required, ^7) | every form is a controlled react-hook-form flow; RHF is the host's single instance |
| `@speed/auth-core` | dependency | the session every component drives, its flow operations, and the flow response models it re-exports from the generated surface |
| `@speed/i18n` | dependency | the namespace registration and translation hook every component renders through |
| `@speed/ui-kit` | dependency | the `FormLayout`/`FormField` form skeleton and `EmptyState` (the session-ended screen) |

`@speed/api-sdk` and `@speed/api-client` are devDependencies: no public
type of this package references them, and only the README's quick start
(executed by `src/usage-example.test.tsx`) binds a real client through the
api-sdk runtime seam. No direct HTTP exists anywhere in `src/` -- every
request goes through the session's generated operations over the shared
seam, and the workspace's `speed/no-direct-http` rule enforces that (this
package is simply absent from the rule's one whitelist,
`packages/api-client`); the `speed/no-literal-text` rule enforces the
namespace discipline over `src/` the same way it does in every package.

## Known limitations

- **No registration form.** An account has no signup endpoint of its own
  and is created inside a login flow's account-resolution step (or by an
  invite, or by an administrator), so a "registration" is a login flow's
  outcome under the deployment's signup setting. `SignInScreen` presents a
  register entry over the one flow where the deployment lets a first login
  open an account; a host that wants more composes it from the same
  sign-in surface.
- **Account binding, step-up and factor enrollment are not this
  package's.** This family drives the login purpose; the authenticated
  purposes (`link`, `step-up`, `enroll`) and their screens live in
  `@speed/account-ui` (`SocialBindingsSection` + `BindingCallbackHandler`,
  `MfaSection`). A passkey (`webauthn`) step a login flow parks on drives
  its ceremony here (`src/internal/webauthn.ts`);
  the authenticated passes render it in `@speed/account-ui`.
- **The channels are the deployment's, not the host's.** `SignInScreen`
  reads the discovery document (`session.listMethods`) and renders the
  methods it names, so a host no longer declares a channel set; the
  enterprise provider list is read per email (`session.discoverSso`) and
  does not appear in the discovery document. Sign-up copy above a real
  sign-in page is host content, not a package export.
- **Session state does not survive a page load, and the refresh token is
  JavaScript-visible in memory** -- auth-core's own known limitations,
  inherited by any host of this family: reloading starts anonymous and the
  user signs in again. See `@speed/auth-core`'s README for the full
  account.

## Deferrals and recorded decisions

- **Consumer**: `starters/demo/web` renders this family through
  product-shell: the app passes the family's sign-in view into the shell's
  `signIn` slot and its sign-out action into the `userMenu` slot, over the
  session the shell's bootstrap binds to one real `@speed/api-client`. The
  package-level proof is the in-form leg --
  `src/usage-example.test.tsx` drives the composed family over a real
  `@speed/api-client` bound through the same seam a host binds, with a
  scripted fetch answering genuine `Response` objects.
- **Auth-core hooks stay host-side by design.** Components in this family
  take the session as a prop and fire callbacks; the host observes session
  transitions with `useAuthState` and friends. A component that needed the
  hooks would have to be mounted under the host's `attachSession` anyway --
  this family is the reason the host gate exists, not a consumer of it.
- **Storybook / browser-side visual verification**: no preview harness
  exists (the same deferral `ui-kit` and `layout-kit` carry);
  `color-contrast` stays axe-disabled for the same jsdom reason, and
  contrast is asserted at the theme-value level in this package's
  component suites (see `SignOutButton`'s suite).

## Development

From `web/packages/auth-ui`: `pnpm lint`, `pnpm typecheck`, `pnpm test`,
`pnpm build`. The build compiles `@speed/auth-core` and `@speed/ui-kit`
first (their sources are what the aliases and the published `.d.ts` files
reference) and then emits this package's `dist/`; `pnpm build` from the
`web/` root does the same for every package. Shared test helpers live in
`test-utils/`, bilingual fixtures are the shipped locale files under
`src/locales/`, and `src/usage-example.test.tsx` compiles and executes the
Quick start composition above, so the documented usage cannot drift from
the API.
