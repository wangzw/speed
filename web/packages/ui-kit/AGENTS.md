# AGENTS.md — @speed/ui-kit

## What this package is

The first DOM-rendering package: the `createAppTheme` theme factory that
maps the merged token tree onto an MUI v9 theme and carries the
coarse-pointer 44px floor for the base controls, `AppThemeProvider`
(theme + MUI locale linkage + color mode + CssBaseline; the
`AppThemeModePreference` -- `'light'`/`'dark'`/`'system'` -- is held as a
preference and resolved to an `AppThemeMode` through `useAppThemeMode`,
`'system'` following the device while it is selected, the dark surface
reading the neutral ramp from its dark end), and the controlled
components (`PageLayout`, `PageHeader`, `SectionCard`, `EmptyState`,
`ConfirmDialog`, `SidePanel`,
`FormField`, `FormLayout`, `PhoneNumberField`, `DataTable`,
`FileUploader`, `InlineError`, `InlineSuccess`, `WriteFeedbackRegion`,
`ListSkeleton`, `AsyncSection`, `HumanVerification`, and the one action
row (`ActionRow` with its `ActionButton`, right-aligned, one spacing
token between adjacent controls, the primary action last, weight and
colour from `emphasis`/`tone` and size from the row)
that render only the
state hosts give them, plus the form family's zod validation bridge
(`zodFormResolver`), the phone parts' composition helpers (`toE164`,
`validatePhoneNumber`, `defaultPhoneCountry` and the grouping/parsing
functions in `src/phone-number.ts`), the email-address rule shared by the
surfaces that accept one (`parseEmailAddress` and
`EMAIL_ADDRESS_MAX_BYTES` in `src/email-address.ts`), the write-feedback scope
(`useWriteFeedback`, with the slot's renderer
`WriteFeedbackRegion`), the refresh pair (`RefreshButton` plus
`usePageRefresh` / `pageRefreshRead`, which re-reads the query handles the
host hands it, reading them structurally and importing no query client or
transport), the clipboard and hand-off pair (`CopyButton`,
`OneTimeValuePanel`), the TOTP enrolment pair (`TotpQrCode`,
`TotpEnrollmentPanel`, both fed by values the host parsed and translated),
the display-preference contexts
(`DisplayTimeZoneProvider` / `useDisplayTimeZone` and
`PhoneCountryDefaultProvider` / `usePhoneCountryDefault`) and the shared
state primitives (`errorCodeOf`, `httpStatusOf`, the
`visuallyHiddenSx` clip recipe, and the `touchTargetSx` recipe as the
fallback floor for interactive elements the theme does not reach). `FileUploader` is no carve-out: the queue renders from
host-owned `rows` props, every pick, cancel, retry and remove reports up
through a callback (`onSelectFiles` / `onCancel` / `onRetry` /
`onRemove`), and the upload transport — one logical transfer per picked
file, pre-flight validation (size, type, count) and any concurrency
limit included — is the host's own code. That boundary is what keeps
the package free of HTTP: the widget never fetches, never holds a File
past the event handler that reported it, and keeps no record of rows
that are not in the current `rows` prop. Built-in user-facing strings
live in the bilingual `ui-kit` namespace;
the repo's text discipline (both languages, identical key sets, nothing
inline) is enforced over this package's `src` by the workspace's own
`speed/no-literal-text` ESLint rule. The TOTP pair is the deliberate
exception with no built-in copy: its sentences, labels and accessible
names all arrive as props, because the enrolling surface owns the
authenticator vocabulary and the `otpauth://` parse.

`PhoneNumberField` is the package's one dependency on country data:
`libphonenumber-js` (its `mobile` metadata build, about 100 KB raw / 25 KB
gzipped plus the parser/formatter code) supplies calling codes, the
as-you-type grouping and the per-country number-plan validation. The
`mobile` plan patterns are why: a number of a legal length but an
impossible plan is refused rather than sent to the SMS channel. It is
a regular dependency rather than a peer -- no host module or instance is
shared -- and the package sets `sideEffects: false`, so a bundle that
does not use the phone control tree-shakes it away. Country names are
not a translation-key table: they come from `Intl.DisplayNames` at
render time.

`TotpQrCode` is the package's one dependency on QR encoding:
`qrcode-generator` turns the caller's `otpauth://` URI into the module
matrix the component draws as an SVG path. It is a regular dependency
rather than a peer -- no host module or instance is shared -- and the
package sets `sideEffects: false`, so a bundle that does not use the
enrolment pair tree-shakes it away.

The public surface is `src/index.ts`
plus `resources.ts`'s exports; everything else under `src/internal/` is
plumbing and deliberately not exported.

## Non-negotiable rules

- **Text renders from the `ui-kit` namespace or from host props — never
  inline in code.** The `speed/no-literal-text` rule (workspace config at
  `web/eslint.config.mjs`, implementation and rule tests in
  `web/eslint-rules/`) enforces this over `src/`; package tests and
  `test-utils/` are exempt by config because fixture strings are data.
  Do not weaken the config for a new component — route its text through
  `useUiKitTranslation` instead.
- **The two locale files stay a bilingual pair.** A new built-in string
  adds one key to both `src/locales/zh-CN.json` and `en-US.json` in the
  same commit, with identical nested structure. Registration rejects
  drift at runtime (`registerNamespace`'s leaf-parity and plural-coverage
  validation, `web/packages/i18n/src/register.ts`, called in the host and
  in `renderWithProviders`), and `src/resources.test.ts` exercises that
  validation over the shipped pair. Nothing in CI reads the raw files.
  Hosts reword the kit by registering their own identical-
  key bundle pair under `UI_KIT_NAMESPACE` at bootstrap — never by
  editing component text.
- **The namespace registers once, at host bootstrap — never inside the
  package.** Components and `AppThemeProvider` only consume the
  registered namespace; registering (or rendering `I18nextProvider`)
  from inside the package would make the double-registration guard and
  the provider tree a host's problem instead of a host's choice. The
  package never imports `@speed/i18n`'s `registerNamespace` outside
  test-utils.
- **Components are fully controlled.** No fetch, no data mutation, no
  business state, no implicit sorting, slicing or filtering of `rows` —
  every knob is a prop the host owns and a callback the component
  fires. Interaction-only state (a confirm's arming, a tooltip's open)
  is the allowed exception and must be provably interaction-local.
  `SidePanel` follows the same contract: `open` and `busy` are host
  props, the close button, Escape and backdrop clicks only report
  `onClose`, and the exit lifecycle reports `onExited` after the
  transition — the panel never closes itself and holds no state.
  `FileUploader` keeps that contract too: the queue is the host's
  `rows` state, interactions report up through its callbacks, and the
  upload transport is host code — never package code.
- **`useWriteFeedback` and `usePageRefresh` are the exported hooks that
  hold state, and their state belongs to the calling host component.**
  Every component above stays controlled; `useAppThemeMode`,
  `useDisplayTimeZone` and `usePhoneCountryDefault` read provider
  context and hold no state of their own (the mode hook also exposes the
  provider's `setPreference`). `useWriteFeedback` holds one feedback scope's slot and
  in-flight count for the component that calls it -- never provider or
  global state, never data from the network. `usePageRefresh` holds one
  surface's refresh bookkeeping (in-flight guard, settle times,
  announcement count); its `refetch` calls reach the network through the
  host's own reads, never through code in this package. Both hooks'
  callbacks keep their identity for the host component's lifetime.

- **The refresh seam never imports a query client or transport.**
  `src/use-page-refresh.ts` and `RefreshButton` read handles
  structurally (`isEnabled`, `isFetching`, `status`, `dataUpdatedAt`,
  `isPlaceholderData`, `refetch`) and depend only on React, MUI and this
  package's own translation seam (`RefreshButton` reads the `ui-kit`
  namespace through `src/internal/translation.ts`); they import no query
  client, api-client or transport. Adding
  `@tanstack/react-query`, `@speed/api-client` or a direct `fetch` here
  is forbidden -- the package's dependency list is the boundary, and
  the host passes its own handles. The same rule keeps `httpStatusOf`
  structural: it reads `status` / `client.http.<n>` and never imports
  the transport.

- **New display/preference props default to their previous behaviour.**
  `AsyncSection.refusedState`, `DataTable.ariaLabel` /
  `ariaLabelledBy`, `DataTableFilter.label` / `placeholder`,
  `ConfirmDialog.children` / `confirmDisabled`,
  `PhoneNumberField.preferredCountries`, and the two contexts
  (`DisplayTimeZoneProvider`, `PhoneCountryDefaultProvider`, both
  defaulting to `undefined`) all leave the rendered output
  byte-identical when omitted. `RefreshButton.label` and `CopyButton.label` / `copiedLabel` are the
  same shape: the caller's string replaces a namespace default that no
  existing call site passes. `CopyButton.visibleLabel` is different:
  it switches the control from the icon face to a text-button face, so
  there is no namespace default behind it. `ConfirmDialog.children` is rendered
  outside `aria-describedby` by contract -- the message stays the
  description. `AsyncSection.refusedState` carries the caller's
  refusal copy and is never derived here: ui-kit inspects no status and
  knows no permission semantics.

- **The action row is the one primitive for page, dialog and row actions.**
  `ActionRow` lays its actions out as one right-aligned group with
  `theme.spacing(1)` between adjacent controls (the single spacing
  token) and the primary action last, so it closes the group at the
  right end. A call site must not separate buttons with
  `marginLeft: 'auto'` (or any automatic margin): the row is one group,
  never two ends. `ActionButton` derives its face from two independent
  slots -- `emphasis` (`primary` = contained, `secondary` = text) and
  `tone` (`default` = primary role, `danger` = error role, filled when
  primary and outlined when secondary) -- and deliberately accepts no
  `variant`/`color`. `size` is the surface's decision: `medium` for page
  forms and dialogs, `small` for table rows and menus; the row passes it
  to every button below it, and a standalone `ActionButton` takes it
  directly. `EmptyState.action` (a centred retry) and existing per-row
  inline action clusters are not action rows and keep their own layout.
- **The monospace stack is exposed by module augmentation.** The token
  tree's `typography.fontFamily.mono` maps to
  `theme.typography.fontFamilyMono` through a `declare module
  '@mui/material/styles'` augmentation in `src/theme/createAppTheme.ts`
  (MUI has no monospace slot). The factory always fills it; theme
  options keep it optional so a host composing its own theme without the
  factory is unaffected. Read it like `fontFamily`, including from `sx`
  callbacks.
- **The surface roles are exposed by module augmentation, declared in
  the factory's own module.** The token tree's `surface` section maps to
  `theme.surface` through a `declare module '@mui/material/styles'`
  augmentation in `src/theme/createAppTheme.ts` -- the file that also
  augments the monospace stack -- rather than in
  `src/theme/surface.ts`. A consumer resolving `@speed/ui-kit` to its
  `dist/` declarations loads only the modules the entry's `.d.ts` graph
  reaches, and `surface.ts` is internal: an augmentation declared there
  is invisible in that consumer's build, where `theme.surface` reads as
  an unknown property. Read `theme.surface` like `fontFamilyMono`,
  including from `sx` callbacks.
- **Write results go through exactly one feedback scope per control
  group.** The scope is one `useWriteFeedback` instance plus one
  `WriteFeedbackRegion` rendered directly under the controls. Each
  attempt starts with `reset`, and the slot holds at most one outcome --
  the one that settled last; overlapping attempts are never merged, and
  an old failure and a new success never coexist. `run` settles from the
  attempt's return or throw: a string (including an empty one) confirms --
  the empty string is stored verbatim and renders a banner with no text,
  so callers must pass non-empty copy; `null` does not touch the slot (a
  step-up refusal handed to the elevation gate; its replay is a new
  `run`; the slot is empty from the attempt's own reset, and a result
  filled later by an overlapping attempt or by `succeed` / `fail` stays),
  and a throw becomes a code
  through `errorCodeOf`; `run` never rejects. The success message is the string the host translated
  at `succeed` time and does not follow a later language switch; failure
  codes resolve at render time, so they do. The region carries no copy
  of its own: the host passes the success text and binds its package's
  error-code resolver into its own `internal/` wrapper, so call sites
  pass the slot and nothing else. The region's root container carries a
  default top margin of one spacing unit and the caller's `sx` applies
  after it; that is why `InlineSuccess` takes no `sx` and is not rendered
  separately.
- **`AsyncSection.staleState` marks a failed refresh that kept its
  data.** Supply it exactly while the latest refresh failed, the data is
  still on screen and no read is in flight; withdraw it when the retry
  starts (the alert unmounts) and supply it again if that retry fails
  (the alert mounts anew and re-announces through `role="alert"`). The
  notice renders above the settled content or, for an empty payload,
  above the empty placeholder; both settled branches keep the same
  fragment shape, so the notice's lifecycle never remounts the settled
  node or loses its local state. Its
  `description` and `retryLabel` are the caller's translated copy. A
  refusal such as 403 does not use `staleState`: the caller replaces
  the content with the no-permission placeholder. Omitting `staleState`
  renders the previous behaviour unchanged.
- **Host-content props are fallbacks over namespace defaults, or pure
  host content — never required translations.** `EmptyState` /
  `ConfirmDialog` ship namespace defaults with overridable
  `title`/`message`/label props; column headers and cell renderers are
  entirely the host's translation surface. `PageHeader`'s visible
  content (title, description, crumb labels, actions) is host content
  too; its only built-in strings are the breadcrumb nav landmark's
  accessible name and the collapse-expand button label
  (`pageHeader.*`), shipped through the namespace like any other. No
  component ever calls `t()` on a host-provided string.
- **Error messages follow the validation-error contract.** A message
  that is a ui-kit-namespace key renders as its translation; anything
  else renders verbatim. The form family resolves keys in the ui-kit
  namespace only and never guesses another namespace's codes —
  `src/internal/validation-error.ts` is the single resolution point.
  `REQUIRED_ERROR_KEY` is the exported key for hosts building their own
  rules.
- **Do not duplicate MUI chrome.** MUI controls already render labels,
  helper text and aria wiring; the form family's "uniform error display"
  is the render-state contract plus FormLayout's skeleton, not a second
  renderer. When a control is missing from MUI's v9 surface, build on
  its primitives — do not fork them.
- **Framework peers stay peers.** `react`, `react-dom`, `@mui/material`,
  `@emotion/*`, `react-hook-form` and `zod` are peer (required)
  dependencies — single copies in the host tree are what make
  theme/context/type identity work, and a host-built zod schema is only
  the same class as the package's `ZodType` when one zod is in the tree;
  the package never depends on them directly. New dependencies on
  framework-adjacent packages need the single-copy argument stated in
  the same commit. `@speed/tokens` and `@speed/i18n` are regular
  dependencies; a new @speed consumer joins them.
- **Accessibility is asserted, with recorded exceptions.** Component
  tests run axe through `test-utils/axe.ts`; `color-contrast` and
  `region` are disabled with the rationale recorded there (jsdom cannot
  compute contrast — contrast lives in the theme tokens; landmark
  containment is a page concern). Do not disable further rules without
  the same kind of recorded rationale. Decorative icons stay
  `aria-hidden`; semantics come from real elements (`h1` headers, real
  table structure, dialog labelling).
- **The public API is frozen by convention.** Lockstep versioning makes
  an exported-signature change a breaking release; extend the surface
  only intentionally. A public change ships, in one commit: the code,
  its tests, this AGENTS.md, the README (contract prose and the
  resource table when keys change), and the compiled usage example when
  the documented composition changes. `FormLayout`'s `columns?: 1 | 2`
  is the template for how an additive prop stays additive: it defaults
  to `1`, exactly the unconditional single-column flow, so every
  existing consumer that omits it is byte-for-byte unaffected; only
  passing `columns={2}` opts into the new CSS Grid flow.
  `DataTableColumn`'s `priority?: 'high' | 'medium' | 'low'` follows
  the identical template: undefined by default, exactly the
  always-visible column, so an existing consumer that sets no column's
  priority is byte-for-byte unaffected; only a column that opts in
  gets the container-query-keyed hide/show. `DataTableColumn`'s
  `cellWrap?: 'wrap' | 'nowrap'` follows it as well: omitting it (or
  passing `'wrap'`) is normal text flow, and only `'nowrap'` emits the
  single-line declaration. The color mode follows the same template
  where it can: `createAppTheme`'s third `mode` argument and
  `AppThemeProvider`'s `defaultMode` both default to `'light'`. The
  preference is three-valued (`AppThemeModePreference`), and
  `AppThemeModeControl` exposes `mode`, `preference` and
  `setPreference`.

## Testing

Unit tests are vitest + jsdom, one file per source file under `src/`,
shared helpers only in `test-utils/` (`renderWithProviders` mounts the
unit under the real host tree — fresh i18n instance per call so the
double-registration guard never fires across tests — and
`expectNoAxeViolations` runs axe; always `await` it: axe-core refuses
concurrent runs). Bilingual assertions import the shipped bundles
(`../locales/zh-CN.json`, `en-US.json`) — never inline a language
literal. `src/usage-example.test.tsx` compiles and executes BOTH the
README's Quick-start composition and its "Write feedback" section, so
the documented usage cannot drift from the API; when either README
composition changes, that file changes with it.
The write-feedback suites pin the slot's rules -- overlapping attempts
settling in landing order, a `null` settlement not touching the slot,
a rejected or synchronously thrown attempt captured as a failure while
`run` itself resolves, `reset` clearing the slot without cancelling an
attempt, `pending` holding until every overlapping attempt settles, and
the success message staying the translation captured at `succeed` time
while a failure code follows the active language. `WriteFeedbackRegion`
is scanned with axe for its success state, and `AsyncSection`'s stale
cases drive the withdrawal and remount through a host that owns the
marker, so a sticky alert cannot pass.
The `FileUploader` suite reaches every interaction state through
host-owned rows and recorded callbacks — picks and drops reporting the
files in order, cancel/retry/remove keyed by row id, each row status
rendered as given, progress folding (indeterminate when absent,
determinate clamped when present, NaN and out-of-range fractions
included) and the settle announcements of the live region (mount quiet,
rows appended already settled announce without an uploading phase, a
retry clears so an identical later failure re-announces, and a second
same-name row reaching the same outcome as a standing announcement
clears and re-announces on the next tick) — and its a11y assertions run
through the same `expectNoAxeViolations` used everywhere here. The TOTP
pair's suites drive the symbol, the manual disclosure and the copy
control entirely from caller props -- they assert those props, never a
bundle -- and pin the code field's numeric six-character contract and
the secondary-before-primary action order. The action row's suite pins
the row's one-group layout (`flex-end`, the gap equal to the token's
spacing unit) and the button's two slots -- text/contained by
`emphasis`, error fill/outline by `tone` -- plus size inheritance from
the row and a standalone button's medium default; its browser case
measures the rendered boxes in Chromium: both controls on one line,
exactly one spacing unit between them, and the primary closing the group
at the row's right edge. The usage example's upload panel drives the documented host
composition — host-owned queue, host AbortControllers, host transport
— over a scripted fetch answering genuine `Response` objects; scripted
transports and their fixture URLs live in test files only, and the
workspace's `no-direct-http` rule keeps every fetch out of `src`.

**Responsive sx assertions**: a plain, non-gated sx value
(`flexWrap: 'wrap'`, a fixed
`width`) computes fine through jsdom's `getComputedStyle`, so those
stay ordinary `toHaveStyle` assertions (`FileUploader`'s row action
groups, `PageHeader`'s title/actions row). A gated sx value — a
breakpoint-keyed or container-query-keyed one, or one behind a
`@media (pointer: ...)`/`(hover: ...)` feature — compiles to a base
rule plus an at-rule that jsdom cannot evaluate: there is
no real viewport or container for either side to be "active" at — so
those assert
against `@speed/test-utils/emitted-css`'s `emittedStyleText()` instead: the
actual generated CSS text, read across every `<style>` tag in the
document. Both forms are property/snapshot proofs that the intended
declaration was wired into the render; neither proves a layout looks
correct at a real viewport or container width, which this package's
jsdom suite
cannot render at all (the README's Deferrals record the same
browser-side visual gap).

**DataTable and FormLayout responsiveness is container-triggered**, so
their suites pin both halves of the wiring with `emittedStyleText()`: the
*container declaration* — `container-type:inline-size` on the box that
makes the queries live, the failure the layout design records being a
container query with no ancestor container, which never fires, so this
assertion is the guard for it — and the *queries* (`@container
(min-width:...)` with the tier's value). DataTable's column priority
asserts the tier's exact container query and the
`table-cell` restore value, proves the viewport `@media (min-width:1200px)`
rule absent (so the display is keyed on the container, not the viewport),
and proves a prioritized column stays a genuine DOM column
rather than being conditionally rendered away (which the loading/empty
rows' `colSpan` depends on). The priority values sit on a `display`
property and the filter row's width on a `width` property, so the two
features' generated CSS does not collide; the filter test renders no
prioritized column, so the only `@container` rule there is the filter's
own. DataTable's pointer rules assert the `@media (pointer: coarse)`
44x44 touch-target floor and the `@media (hover: hover) and (pointer:
fine)` row-hover gate. The cell-wrap cases assert the declared
`white-space: nowrap` on an opted-in column's header and body cells and
its absence on a column that does not opt in -- the declaration jsdom
can show, not a rendered width. The whole-cell cap cases
(`contentMaxWidth`) assert the cap's container-query `max-width` on the
wrapper that holds the rendered text and the `cellFullValue` control
together, the `min-width: 0`/overflow/ellipsis declarations on the text
sub-box alone, and the control outside that box refusing to shrink; a
column without the field carries no cap rule and keeps the prior
inline-flex shape, and a row without a full value is capped without a
control. As with every `emittedStyleText()` assertion in this package,
these prove the right declarations were wired in, not that a real
viewport or container actually hides a column, scrolls a table, or
renders a cap at its declared width.

## Deferrals (recorded, do not re-open silently)

- **Validation from generated types** (zod-from-generated-types) is
  not implemented; the validation-error contract is the interface it would
  plug into.
- **Error-code mapping** (which namespace turns which backend code into
  text) is not implemented; verbatim passthrough is the contract.
- **Storybook**: no preview harness exists; components are covered by
  jsdom tests + axe, and color-contrast is verified against the theme
  values, never a rendered browser.
- **The `no-literal-text` rule** catches the direct literal routes but
  not indirect ones (ternary branches, literals crossing component
  boundaries) — documented partial enforcement; hosts own their
  literals.
- **No in-workspace host wires uploads over the generated storage
  operations** — the storage operations are generated into
  `@speed/api-sdk` from the merged document, which carries
  `pkg/objectstore/metadata`'s fragment like every platform module's, so a
  host's upload code can pair with `FileUploader`; no consumer page does
  so yet, and this package ships no transport either way. The wire
  contract's authority is
  `pkg/objectstore/metadata/openapi/metadata.json` itself; the gap is
  recorded in `pkg/objectstore/AGENTS.md` and the generation config's
  header comment as well, so no single doc owns the claim.
