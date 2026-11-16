# @speed/i18n

The platform's i18n layer for web packages: one react-i18next-wrapped
instance per host, a negotiated start language, per-package namespaces
registered with parity and coverage validation, and a missing-key
discipline with **no silent cross-language fallback** -- the frontend
counterpart of `pkg/i18n`'s catalog. The MUI locale helper lives at
`@speed/i18n/mui-locale`.

## Language negotiation

The start language is decided by a chain, first match wins, every source
validated against the instance's supported set and skipped when it matches
nothing:

1. **URL parameter** (`?lang=...`, override for share links; parameter name
   configurable, disable with `urlParameterName: null`).
2. **Persisted choice** -- the manual language switch writes through
   `switchLanguage`; stored under `SPEED_LOCALE_STORAGE_KEY` in the bound
   storage (browser localStorage by default, injectable, opt-out with
   `storage: null`).
3. **Profile language** -- the signed-in user's stored locale. The platform
   ships no profile feature yet: this slot exists for hosts that can
   resolve a profile locale, and it outranks the browser (the URL
   parameter and a persisted manual choice still outrank it, in the chain
   order above).
4. **Navigator languages**, in preference order.
5. **The default language** -- `en-US`, the platform default every chain
   in the stack (backend content chains included) terminates at.

### Applying the profile language after creation

`profileLanguage` is a creation-time input, but the profile locale's
usual source is a `/me` round trip that resolves only after the instance
exists. The one API that applies a language to a live instance is
`switchLanguage`, whose optional storage argument is three-state:
`undefined` uses the storage bound at creation, `null` persists nothing,
and a `StorageLike` persists to that store instead. Which state a caller
means is not a mechanics detail:

- the **manual language-switch UI** persists (the default): the stored
  slot is the manual-choice tier of the negotiation chain, and a manual
  choice is the most recent user intent;
- a host **applying a server-resolved profile locale** must use the
  non-persisting form -- `switchLanguage(i18n, locale, null)`. Persisting
  a profile application writes the manual slot, and the stored choice
  outranks the profile tier on every later visit by design: a subsequent
  profile change would then be shadowed in that browser until the manual
  slot is overwritten or cleared.

```ts
// host resolves the profile after creation (e.g. from /me):
await switchLanguage(i18n, profile.locale, null) // apply, never persist
```

Matching relaxes language subtags in both directions, never crossing
languages: an exact tag matches first, a subtagged request selects a
supported bare tag of the same language (`ja-JP` selects supported `ja`),
and a bare request selects the unique supported tag carrying it (`en`
selects `en-US` unless several supported tags share the primary subtag).

An unknown browser language resolving to the en-US default is a deliberate
negotiation default, documented and overridable (`defaultLanguage`, which
must remain a member of `supportedLanguages`). It is a different rule from
the missing-key rule below, which never falls back.

```ts
import { createI18n, switchLanguage } from '@speed/i18n'

const i18n = createI18n() // negotiates, synchronous; react-ready instance
// language switch UI:
await switchLanguage(i18n, 'en-US') // persists the canonical choice
```

`switchLanguage` writes the choice to storage first and then switches the
instance; persistence is best-effort by design. A storage write failure
(quota, disabled storage, embedded contexts) never fails or aborts the
switch -- it is reported by a `[speed-i18n]` admin warning and the
instance still switches, so the promise resolves exactly when the language
changed and a rejection always means the switch did not happen. Reads are
protected the same way: a throwing storage read at creation falls back to
"no stored choice", never to a failed creation.

An instance that carries no storage binding -- one not created by
`createI18n`, such as a framework wrapper -- gets the same visible
`[speed-i18n]` warning when a switch cannot be persisted, and still
switches. Opting out at creation with `storage: null` is deliberate and
raises no warning.

A surface that offers the languages the INSTANCE speaks (a language
picker, a settings select) reads them off the instance rather than the
package default, because a host may create the instance with a wider or
narrower set:

```ts
import { readSupportedLanguages } from '@speed/i18n'

const choices = readSupportedLanguages(i18n) // e.g. ['zh-CN', 'en-US']
```

`readSupportedLanguages` answers `[]` for an instance created without a
pinned set -- a state `registerNamespace` and `switchLanguage` both refuse
-- and filters out i18next's internal `cimode` meta language, so the
picker never offers a non-language.

## React bindings

The react-i18next bindings are re-exported from the main entry, so hosts
and component packages consume the whole i18n surface through one
`@speed` package (lockstep single-version shipping pins the module
identity -- no host can end up with two react-i18next copies, which is
what makes `useTranslation` inside third-party components safe):

```tsx
import { I18nextProvider, useTranslation } from '@speed/i18n'

export function App() {
  return <I18nextProvider i18n={i18n}>{/* ... */}</I18nextProvider>
}

function Greeting() {
  const { t } = useTranslation('welcome')
  return <p>{t('greeting.hello')}</p>
}
```

## Display time zones, instants and language names

The package carries the Intl-only helpers a surface formats timestamps and
language labels with; it holds no display-zone state, so which zone a
surface renders in stays the host's decision (a preferences store, a
display-time-zone context provider).

```ts
import {
  formatInstant,
  instantToWallClock,
  isSupportedTimeZone,
  languageDisplayName,
  resolveTimeZone,
  wallClockToInstant,
} from '@speed/i18n'

resolveTimeZone(storedPreference) // stored when Intl accepts it, else browser, else 'UTC'
isSupportedTimeZone('Asia/Tokyo') // true; an unknown zone answers false, never throws
formatInstant('2026-10-19T08:30:00Z', 'en-US', { timeZone: 'UTC' }) // medium date, short time
formatInstant(Date.now(), 'en-US', { timeZone: 'UTC', style: 'long' }) // medium date, long time with zone name
instantToWallClock('2026-10-19T08:30:00Z', 'Asia/Tokyo') // { year: 2026, month: 10, day: 19, hour: 17, minute: 30, second: 0 }
wallClockToInstant({ year: 2026, month: 10, day: 19, hour: 17, minute: 30 }, 'Asia/Tokyo') // 2026-10-19T08:30:00.000Z
languageDisplayName('zh-CN', 'en-US') // the language's name in the UI language
languageDisplayName('en-US', 'en-US', ['zh-CN', 'en-US']) // 'English' -- no region name for a lone variant
```

The display-timezone chain is three tiers, first usable one wins: the
stored value the host resolved (an empty or whitespace-only value means
"not chosen"), the browser zone, then `'UTC'`. Every Intl construction is
guarded: a value the engine rejects is skipped like an empty one, so one
stale preference never crashes the page that renders a session list. The
formatting styles map to one documented Intl pair each -- `short` (short
date and time), `medium` (medium date, short time; the default) and `long`
(medium date, long time with the zone name) -- and one formatter is cached
per language, style and zone. A value that carries no instant (empty
string, unparseable text, invalid `Date`, `NaN`) renders as the empty
string, and so does a language the engine refuses.

`instantToWallClock` and `wallClockToInstant` convert between a zone's wall
clock and an instant, the pair a date-time input needs: the input carries a
calendar-and-clock reading with no zone of its own, and the API carries an
instant. Both are guarded and answer `null` for an unknown zone, an
unparseable value, an out-of-range or fractional field, and a calendar
overflow such as 31 February. The two daylight-saving shapes are
deliberate: a reading inside a spring-forward gap never happens and answers
`null`, while a reading inside a fall-back overlap denotes two instants and
resolves to the earlier one, so the answer is deterministic. Resolution is
whole-second, and a `WallClock`'s absent `second` reads as 0.

`languageDisplayName` resolves through `Intl.DisplayNames` and falls back
to the raw tag when the engine cannot name it, so a language picker never
renders an empty label. It names a tag by its base language subtag, so
`en-US` reads `English` and not `American English`; the optional third
argument is the deployment's supported set, and when that set holds more
than one tag of the same base language the full tag is named instead, so
two regional variants still read differently (ADR-web-language-display-name).

## Namespaces and registration

One namespace per package, in bare, unscoped form: a name matches
`[A-Za-z][A-Za-z0-9_-]*` (`welcome`, `auth`...). Scoped npm-style names
are not namespaces: `registerNamespace` refuses `@speed/tokens` -- the
pattern allows no `@` or `/`, because a namespace is a short key hosts
write in `useTranslation('...')` and override, not a package identifier.
A package that ships locale resources registers under its base name.
Resources map canonical language tags to bundles; registration is atomic
and validated before anything lands:

- every language key must be a supported language, **every supported
  language must be present** (a namespace speaking fewer languages than the
  host forces the render-as-key path on users of the missing ones);
- all bundles must carry the **same leaf key set** (parity with a
  reference language; deterministic error messages) -- a key can never
  exist in one language and silently miss in another;
- leaves must be **non-empty strings**; nesting is plain records only. An
  empty translation renders as silence and never fires the missing-key
  discipline, so at runtime it is indistinguishable from a dropped key --
  `registerNamespace` refuses `""` the same way the Go catalog refuses
  empty translations;
- **plural forms are suffixed leaves** (`key_one`, `key_other`, ...,
  resolved per count) and count as ordinary keys for the parity rule. A
  family of such leaves must cover every count category the instance's
  supported languages can select -- registration validates this through
  the same `Intl.PluralRules` resolution the renderer uses -- because a
  count whose form is absent would render the raw key. Since bundles
  carry identical leaf sets, the forms one language needs ship in every
  language's bundle: zh-CN carries `_one` forms that en-US counts of 1
  select and that zh-CN itself never does;
- a namespace registers exactly once per instance (double registration
  usually means double init in tests or SSR).

```ts
import { registerNamespace } from '@speed/i18n'
import welcomeZh from './locales/welcome/zh-CN.json'
import welcomeEn from './locales/welcome/en-US.json'

registerNamespace(i18n, 'welcome', {
  'zh-CN': welcomeZh,
  'en-US': welcomeEn,
})
// components: useTranslation('welcome') / t('greeting.hello')
```

## Resource overlays

A host that changes a package's wording, or supplies a language the
package does not ship, overlays the exported bundle before registering it:

```ts
import { overlayResources, registerNamespace } from '@speed/i18n'
import { adminUiResources } from '@speed/admin-ui'

const resources = overlayResources(adminUiResources, {
  'en-US': { common: { refresh: 'Reload' } },
  'fr-FR': frResources, // a whole language the package does not carry
})
registerNamespace(i18n, 'admin-ui', resources)
```

A partial overlay for a language the package ships is deep-merged leaf by
leaf: each named leaf must already exist, and is replaced. Naming a leaf
the base bundle does not carry **throws** -- an overlay written against an
older key set would otherwise silently add a key the other languages lack,
and an upgrade that dropped a key must surface loudly instead of leaving a
stale override unnoticed. A language the package does not carry is taken
whole and its completeness (every key, every plural form the language
needs) is validated by `registerNamespace` together with the rest of the
namespace. Neither the base nor the overlay is mutated.

Resource-key compatibility follows from that rule: **adding a leaf is
compatible; deleting or renaming one is not**, because a host overlay
naming the old key stops registering. Do not ship a release that drops a
key without treating it as a breaking change.

## Fallback namespaces (opt-in)

A host can name namespaces i18next consults when the namespace a lookup
names carries no such key:

```ts
const i18n = createI18n({ fallbackNamespaces: ['platform-errors'] })
registerNamespace(i18n, 'platform-errors', platformErrorsResources)
// t('welcome:errors.authn.invalid_credentials') resolves through
// 'platform-errors' when the welcome bundle has no such leaf
```

The option is unset by default and an unset option configures no
fallback namespace at all, so resolution is exactly what it was before
the option existed. The platform-error bundle (`@speed/i18n/platform-errors`)
is the intended value: it carries every backend error code's own text, so
a code a package's own namespace does not cover resolves to the module
catalog's words instead of the raw key.

This is **namespace** fallback, never language fallback. Every consulted
namespace resolves in the same negotiated language (`fallbackLng` stays
false, `load` stays `currentOnly`), and a registered namespace always
covers the whole supported set -- so a fallback can never supply another
language's text. A key no consulted namespace carries still renders as
the key itself and fires the missing-key handler; a key a fallback
namespace answers is a hit, so the handler stays silent for it.

## Interpolation: values as text, never HTML

The instance initializes with `interpolation: { escapeValue: false }`.
React renders every translated string as a text node and escapes it when
it does; the engine escaping the interpolated values as well would show
the entities to the user -- a path's `/` as `&#x2F;`, a tenant name's
`&` as `&amp;`. Values are inserted verbatim, and the package renders
into React only, so no layer here consumes HTML.

## Missing keys: never another language's text

`createI18n` pins the guarantees on the underlying instance:
`fallbackLng: false`, `load: 'currentOnly'` (cross-language fallback is
impossible), `saveMissing: true` (i18next v26 only dispatches its
missing-key handler when this is on) and a handler that is always
installed. A key missing in the loaded language therefore:

- renders as the key itself -- never as the same key's text from another
  language;
- fires the handler with structured details
  (`MissingKeyDetails`: languages, namespace, key) -- the package's
  default handler warns visibly (`[speed-i18n]` prefixed admin warning),
  or the host supplies `onMissingKey` at creation.

The registration-time parity and coverage checks are the companion
guarantee: whole-language or whole-key gaps surface at registration as
errors. A gap that appears after registration cannot be driven through
this package's API -- registerNamespace validates key-set parity and
refuses a second registration, and i18next's own removal is per
language-and-namespace, never per key -- so the tests prove the render
path by removing the whole en-US bundle of a registered namespace
(instance.removeResourceBundle) while zh-CN still holds the key, then
rendering in en-US: the key renders as itself, the zh-CN text is asserted
absent, and the missing-key warning fires. A genuine single-key deletion
would take that same render path, but no package API can express it.

## MUI locale linkage

`createTheme` needs the MUI localization matching the active language.
Isolated in its own subpath so the main entry never imports MUI:

```ts
import { createTheme } from '@mui/material/styles'
import { muiLocaleFor } from '@speed/i18n/mui-locale'

const theme = createTheme(baseTheme, muiLocaleFor(i18n.language))
```

`muiLocaleFor` throws on unknown tags rather than silently pairing a
Chinese UI with English locale text. It is identity-stable per language.

## Platform error copy (`./platform-errors`)

The bundle (`src/platform-errors/locales/{zh-CN,en-US}.json`) is the
lookup table a host registers once at bootstrap; the fallback namespace
above makes it reachable from every package lookup. A code resolves to a
flat dotted leaf under its `errors` section (`errors.authn.login_failed`),
which is the `<module>.<code>` form @speed/api-client derives from an
RFC 9457 problem type (`urn:speed:<module>:<code>`, each dash in the code
part normalized to an underscore):

```ts
import { createI18n } from '@speed/i18n'
import {
  PLATFORM_ERRORS_NAMESPACE,
  platformErrorResources,
  registerPlatformErrors,
} from '@speed/i18n/platform-errors'

const i18n = createI18n({ fallbackNamespaces: [PLATFORM_ERRORS_NAMESPACE] })
registerPlatformErrors(i18n)
// an instance supporting a wider set supplies the whole language:
registerPlatformErrors(i18n, { 'fr-FR': frBundle })
```

The second argument supplies whole languages the package does not carry.
The key set of an extra bundle must match the packaged bundle's: it is
compared against the packaged reference directly (including an instance
whose supported set omits the packaged pair), and registration then
enforces the same key set across every registered language. Supplying a
language the package already ships is refused (whole-language supply and
per-leaf override are different operations; use `overlayResources` on
`platformErrorResources` for the latter). With no second argument,
registration behaves exactly as before. `platformErrorResources` exports
the shipped `zh-CN`/`en-US` pair for a host that overlays it before
registering.

Membership is the backend's writable set plus the legacy codes the web
packages still name. `tools/check_platform_errors.py` derives every
problem code the Go source under `pkg/` and `starters/` can write and
holds the catalog to it (`tools/README.md` has the derivation rules): a
written code with no entry is a finding, because the refusal would render
as the raw key; an entry no backend writes is a finding too, unless the
gate's `KEPT` list names it with the frontend files that still refer to
it. The list is what keeps a code the web packages name themselves —
the shared session-lifecycle vocabulary, a test that pins a fallback
lookup — from being deleted out from under them; an entry whose reference
is gone is reported as stale, so the list cannot rot silently. Both
language files must carry the same leaves and no duplicate key. Adding
copy for a new backend code therefore means adding its leaf to both
language files in one change; there is no generation command.

## Error-code text convention

The main entry also owns the one convention every error surface resolves
through: a whitelisted code resolves to its own leaf under the bundle's
`errors` section (`errors.<code>` — the same section the platform error
bundle ships), and anything unlisted to `errors.unknown`, so a
resolver can never render a raw key and a missing translation never
leaks another language's text.

```ts
import {
  CLIENT_TRANSPORT_ERROR_CODES,
  SESSION_LIFECYCLE_ERROR_CODES,
  createErrorTextResolver,
} from '@speed/i18n'

// The surface's whitelist composes the shared families with the codes
// its own operations reach, and the resolver binds the namespace's `t`:
const ERROR_TEXT_CODES = [
  'billing.invoice_not_found',
  ...SESSION_LIFECYCLE_ERROR_CODES,
  ...CLIENT_TRANSPORT_ERROR_CODES,
] as const
const resolve = createErrorTextResolver(
  t,
  new Set<string>(ERROR_TEXT_CODES),
)
resolve('billing.invoice_not_found') // -> t('errors.billing.invoice_not_found')
resolve('authn.future_code') // -> t('errors.unknown')
```

The two shared families ship here because every protected surface can be
answered with them: `SESSION_LIFECYCLE_ERROR_CODES` (a session that dies
mid-flight surfaces through the refresh leg) and
`CLIENT_TRANSPORT_ERROR_CODES` (the api-client contract's transport
failures). Text for the shared codes stays in each consuming package's
own bundle — same-tier packages never import one another's catalogs.

### The bare-HTTP family and the platform fallback

Two optional seams extend the same resolver; without them its behaviour is
exactly as above.

```ts
import { PLATFORM_ERRORS_NAMESPACE } from '@speed/i18n/platform-errors'

const resolve = createErrorTextResolver(t, new Set(ERROR_TEXT_CODES), {
  // client.http.<status> resolves through this namespace's leaf, with the
  // status interpolated; a listed status gets its own leaf instead.
  httpFamily: {
    key: 'errors.client.http',
    byStatus: { '403': 'errors.client.httpForbidden' },
  },
  // An unlisted code the platform error bundle carries resolves through
  // errors.<code> (the fallback namespace answers it), not errors.unknown.
  fallthrough: (code) =>
    i18n.exists(`${PLATFORM_ERRORS_NAMESPACE}:errors.${code}`),
})
```

`httpFamily` resolves the `client.http.<status>` code the api-client
synthesizes for a non-2xx with no readable envelope: the status has an
entry in `byStatus` -> that leaf; otherwise the `key` leaf with
`{ status }` passed to `t`. `fallthrough` is consulted only for a code
outside the whitelist; it answers true when the surface's registered
fallback namespace carries the code, and only then does the resolver ask
for `errors.<code>`. A code nobody carries still resolves to
`errors.unknown` — the default never becomes a raw key.

## Dependencies

| Package | Kind | Why |
|---|---|---|
| `react` | peer (required, ^18 or ^19) | the main entry installs `initReactI18next`, so the instance is the react-i18next instance hosts render with; react-i18next imports React at runtime. devDependency for the test suite. |
| `@mui/material` | peer (optional, ^9.5) | only `./mui-locale` imports `@mui/material/locale`. Optional-peer means consumers without MUI never install or resolve it; consumers importing the mui-locale subpath must have MUI in their own dependencies. devDependency pins the tested version. |
| `i18next` / `react-i18next` | dependencies | the engine; pinned ranges are the tested compatibility window. |

## Error index

All validation failures throw `Error` messages prefixed `[speed-i18n]` with
the actionable fix inline: a non-canonical entry in the supported set at
creation names its canonical spelling (or says it is not a language tag);
an unsupported language names the supported set; a parity gap lists the
missing or extra leaf paths; an empty translation names its leaf path; an
incomplete plural family names the family, the languages whose counts
would spill the raw key and the missing `_<category>` forms; a switch to
an unsupported language lists the supported tags; registering on a bare
i18next instance (no pinned supported set) names `createI18n` as the fix;
an overlay naming a leaf or a subtree the base bundle does not carry names
the path and the compatibility rule; supplying a platform-error language
the bundle already ships, or one outside the supported set, names the
language and says which operation fits. Storage is deliberately
best-effort, by design: a failing write warns
(`[speed-i18n]` admin warning) without failing the switch, and a failing
read at creation silently means "no stored choice". The missing-key
handler is the other non-throwing surface (a production lookup must
degrade visibly, not crash). The display helpers never throw either: a
zone the engine rejects falls through the chain, a value carrying no
instant renders as the empty string, and an unnamed language tag renders
as the raw tag.

## Development

From `web/packages/i18n`: `pnpm lint`, `pnpm typecheck`, `pnpm test`,
`pnpm build`. Bilingual fixtures live under
`test-utils/locales/` (repo CJK-scanner exemption); sources and tests
assert against imported fixtures. `test-utils/` is test-only and never
emitted into `dist/`.
