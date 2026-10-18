# @speed/ui-kit

The platform's first DOM-rendering package: the theme factory that turns
`@speed/tokens` into an MUI v9 theme (`createAppTheme`,
`AppThemeProvider`; the theme also carries the coarse-pointer 44x44
floor for MUI's base controls), and the controlled components (`PageHeader`,
`EmptyState`, `ConfirmDialog`, `SidePanel`, `FileUploader`, `FormField`,
`FormLayout`, `DataTable`, `InlineError`, `InlineSuccess`,
`WriteFeedbackRegion`, `ListSkeleton`, `AsyncSection`) that render only
the state hosts give them --
FileUploader renders the upload queue the host owns as `rows` props
while the transfers themselves run in the host's own code. The
cross-surface state primitives ship beside the components: the
whole-attempt failure banner and its classifier
(`InlineError` / `errorCodeOf`), its success twin (`InlineSuccess`, the
mount-empty-then-fill `role="status"` notice), the write-feedback scope
(`useWriteFeedback`, the one exported hook that holds state, paired with
`WriteFeedbackRegion`, which renders that slot), the
pending/error/empty/content branches of an async read (`ListSkeleton`,
`AsyncSection`) and the
shared style recipes (`visuallyHiddenSx` for hidden elements,
`touchTargetSx` for the coarse-pointer floor of elements the theme does
not reach). Every
built-in user-facing
string lives in the bilingual `ui-kit` namespace registered through
`@speed/i18n` -- components never ship text in code, and the workspace's
`no-literal-text` ESLint rule refuses it there.

## What ships

| Module | Exports |
|---|---|
| `theme/createAppTheme.ts` | `createAppTheme`, `AppTheme`, `AppThemeMode` |
| `theme/AppThemeProvider.tsx` | `AppThemeProvider`, `AppThemeProviderProps`, `useAppThemeMode`, `AppThemeModeControl` |
| `components/PageHeader.tsx` | `PageHeader`, `PageHeaderProps`, `PageHeaderBreadcrumb` |
| `components/EmptyState.tsx` | `EmptyState`, `EmptyStateProps`, `EmptyStateVariant`, `EmptyStateHeadingLevel`, `EmptyStateNoHeading` |
| `components/ConfirmDialog.tsx` | `ConfirmDialog`, `ConfirmDialogProps`, `ConfirmDialogVariant`, `CONFIRM_ARM_LOCKOUT_MS` |
| `components/SidePanel.tsx` | `SidePanel`, `SidePanelProps`, `SidePanelSize` |
| `components/FileUploader.tsx` | `FileUploader`, `FileUploaderProps`, `FileUploaderRow`, `FileUploaderRowStatus` |
| `components/FormField.tsx` | `FormField`, `FormFieldProps`, `FormFieldRenderState`, `REQUIRED_ERROR_KEY` |
| `components/FormLayout.tsx` | `FormLayout`, `FormLayoutProps`, `FormLayoutColumns` |
| `zod-form-resolver.ts` | `zodFormResolver`, `INVALID_ERROR_KEY` |
| `components/DataTable.tsx` | `DataTable`, `DataTableColumn`, `DataTableColumnCellWrap`, `DataTableColumnContentMaxWidth`, `DataTableColumnPriority`, `DataTableSort`, `DataTableSortDirection`, `DataTableFilter`, `DataTablePagination`, `DataTableProps`, `PRIMARY_COLUMN_MAX_WIDTH`, `PRIMARY_COLUMN_CONTENT_MAX_WIDTH`, `primaryColumnMaxWidthSx` |
| `components/InlineError.tsx` | `InlineError`, `InlineErrorProps`, `errorCodeOf` |
| `components/InlineSuccess.tsx` | `InlineSuccess`, `InlineSuccessProps` |
| `components/WriteFeedbackRegion.tsx` | `WriteFeedbackRegion`, `WriteFeedbackRegionProps` |
| `write-feedback.ts` | `useWriteFeedback`, `WriteFeedback`, `WriteOutcome` |
| `components/ListSkeleton.tsx` | `ListSkeleton`, `ListSkeletonProps` |
| `components/AsyncSection.tsx` | `AsyncSection`, `AsyncSectionProps`, `AsyncSectionErrorState`, `AsyncSectionEmptyState`, `AsyncSectionStaleState` |
| `visually-hidden.ts` | `visuallyHiddenSx` |
| `touch-target.ts` | `touchTargetSx` |
| `resources.ts` | `UI_KIT_NAMESPACE`, `uiKitResources` |

Everything else (`src/internal/`) is shared component plumbing and is
deliberately not exported. Each component family's contract is
documented below; the type-level contract is the source of truth.

## Quick start

A host bootstraps its i18n instance once, registers the ui-kit
namespace once, and wraps the app in the two providers:

```tsx
import { createI18n, I18nextProvider, registerNamespace } from '@speed/i18n'
import {
  AppThemeProvider,
  UI_KIT_NAMESPACE,
  uiKitResources,
} from '@speed/ui-kit'

const i18n = createI18n() // negotiates the start language
registerNamespace(i18n, UI_KIT_NAMESPACE, uiKitResources)

export function App() {
  return (
    <I18nextProvider i18n={i18n}>
      <AppThemeProvider i18n={i18n}>
        {/* screens compose PageHeader, DataTable, ConfirmDialog, ... */}
      </AppThemeProvider>
    </I18nextProvider>
  )
}
```

`registerNamespace` validates before it mutates: both languages must be
covered by bundles with identical leaf key sets, so a missing
translation is a boot-time error, never a silent gap. It must run
exactly once per instance -- never inside a component or provider, or
the double-registration guard throws. This exact composition is
compiled and executed by the package suite (`src/usage-example.test.tsx`),
so the documented usage cannot drift from the API.

An upload panel can share the same host screen under the same
contract. `FileUploader` is fully controlled like every component in
the family: the queue is host-owned `rows` state and every interaction
reports up through a callback (`onSelectFiles` for a pick or drop,
`onCancel` / `onRetry` / `onRemove` keyed by row id). The host below
owns the Files it picked (a retry hands the same bytes back), owns one
`AbortController` per transfer -- so cancelling a row or leaving the
screen aborts the round trip -- and runs its own transport code: one
logical POST per file to the host's storage endpoint, reporting
progress slices and translating an unsuccessful answer into the row's
error text. `FileUploader` never fetches, never holds a File past the
event handler that reported it, and renders exactly the rows it is
given:

```tsx
import { useEffect, useRef, useState } from 'react'
import { FileUploader } from '@speed/ui-kit'
import type { FileUploaderRow } from '@speed/ui-kit'

/**
 * The upload endpoint the host's transport sends its one round trip
 * per file to. A real host's upload code calls a generated api-sdk
 * storage operation here -- the merged API document ships no storage
 * fragment, so no such operation exists -- and this is an explicitly
 * labelled fixture placeholder: ui-kit ships no endpoint, and the
 * suite pins the transport shape without pretending to know a wire
 * protocol.
 */
const STORAGE_UPLOAD_URL = 'https://uploads.example.test/objects'

/** True when the given rejection is an abort signal's AbortError. */
function isAbortError(error: unknown): boolean {
  if (error instanceof DOMException) {
    return error.name === 'AbortError'
  }
  return error instanceof Error && error.name === 'AbortError'
}

/**
 * The host's upload transport: one logical POST of the picked bytes per
 * file to the host's storage endpoint, reporting progress slices around
 * the round trip and translating an unsuccessful answer into the row's
 * error text. The fetch is the host's own call -- FileUploader never
 * sees a URL, a signal or a body.
 */
async function uploadToStorage(
  file: File,
  signal: AbortSignal,
  onProgress: (fraction: number) => void,
): Promise<void> {
  onProgress(0.25)
  const body = await file.arrayBuffer()
  const response = await fetch(STORAGE_UPLOAD_URL, {
    method: 'POST',
    headers: { 'content-type': file.type },
    body,
    signal,
  })
  if (!response.ok) {
    throw new Error(`The upload answered ${response.status}.`)
  }
  onProgress(0.75)
}

/**
 * The example upload panel: the host that owns the queue. `rows` is host
 * state -- FileUploader renders exactly what this component writes --
 * and each picked File stays in a host map, because a retry hands the
 * same bytes back to the transport. One AbortController per transfer
 * gives the host both cancellations (abort + drop the row) and the
 * unmount abort; FileUploader itself contributes no state, no fetch and
 * no transfer.
 */
function UploadPanel() {
  const [rows, setRows] = useState<readonly FileUploaderRow[]>([])
  const filesRef = useRef(new Map<string, File>())
  const controllersRef = useRef(new Map<string, AbortController>())
  const nextIdRef = useRef(1)

  // Leaving the screen aborts every transfer this panel owns.
  useEffect(() => {
    const controllers = controllersRef.current
    return () => {
      for (const controller of controllers.values()) {
        controller.abort()
      }
    }
  }, [])

  const patchRow = (id: string, patch: Partial<FileUploaderRow>): void => {
    setRows((current) =>
      current.map((row) => (row.id === id ? { ...row, ...patch } : row)),
    )
  }

  const dropRow = (id: string): void => {
    setRows((current) => current.filter((row) => row.id !== id))
  }

  const transfer = (
    id: string,
    file: File,
    controller: AbortController,
  ): void => {
    uploadToStorage(file, controller.signal, (progress) =>
      patchRow(id, { progress }),
    )
      .then(() => {
        controllersRef.current.delete(id)
        filesRef.current.delete(id)
        patchRow(id, { status: 'succeeded' })
      })
      .catch((error: unknown) => {
        controllersRef.current.delete(id)
        if (isAbortError(error)) {
          // Cancelled (its row was already dropped) or the panel
          // unmounted: there is nothing left to render.
          return
        }
        patchRow(id, {
          status: 'failed',
          error: error instanceof Error ? error.message : String(error),
        })
      })
  }

  const handleSelectFiles = (files: readonly File[]): void => {
    for (const file of files) {
      const id = `upload-${nextIdRef.current}`
      nextIdRef.current += 1
      filesRef.current.set(id, file)
      const controller = new AbortController()
      controllersRef.current.set(id, controller)
      setRows((current) => [
        ...current,
        { id, name: file.name, status: 'uploading' },
      ])
      transfer(id, file, controller)
    }
  }

  const handleCancel = (id: string): void => {
    controllersRef.current.get(id)?.abort()
    filesRef.current.delete(id)
    dropRow(id)
  }

  const handleRetry = (id: string): void => {
    const file = filesRef.current.get(id)
    if (file === undefined) {
      return
    }
    const controller = new AbortController()
    controllersRef.current.set(id, controller)
    patchRow(id, {
      status: 'uploading',
      progress: undefined,
      error: undefined,
    })
    transfer(id, file, controller)
  }

  const handleRemove = (id: string): void => {
    filesRef.current.delete(id)
    dropRow(id)
  }

  return (
    <FileUploader
      multiple
      rows={rows}
      onSelectFiles={handleSelectFiles}
      onCancel={handleCancel}
      onRetry={handleRetry}
      onRemove={handleRemove}
    />
  )
}
```

A transport that rejects before touching the network -- pre-flight
validation (size, type, count) is the host's job -- puts the row into
the failed state showing the rejection's message (host-written and
host-translated, the same contract as the form family's error text);
the row's retry affordance reports up and the host hands the same
`File` back to its transport with a fresh context. Settles announce
once each in a single polite live region -- a row the host appends
already failed (no uploading phase was ever rendered) announces like a
settle that passed through uploading -- and a retry clears the region
first, so an identical later failure re-announces instead of sitting
silent; a second same-name row reaching the same outcome as a standing
announcement re-announces through a clear-then-refill on the next tick.
This exact composition -- the `UploadPanel` above rendered beside the
`MembersPage` of `src/usage-example.test.tsx` (a `DataTable` screen
whose sorting, selection, filtering and pagination are all controlled
from host state), the panel's transport a scripted fetch stub answering
one genuine `Response` per round trip in the order the files were
picked -- is compiled and executed by the package suite, so the
documented usage cannot drift from the API.

## The theme factory

Token layers are diffs, applied over the built-in defaults in order:

```ts
const appTheme = createAppTheme(projectTokens, tenantOverrides, mode)
// appTheme.tokens -- the merged token tree
// appTheme.theme  -- the locale-free MUI theme built from it
```

The third argument is the color mode (`'light' | 'dark'`, default
`'light'`), which sets `palette.mode`. The token tree defines one set of
surface colors (a light palette) and the neutral ramp is a full scale, so
the dark mode reads the ramp from its dark end for background / text /
divider rather than carrying a second palette; the six semantic roles are
shared by both modes. `AppThemeProvider` holds the active mode in its own
state (starting at its `defaultMode` prop, `'light'`) and exposes the mode
with a setter and a toggle through `useAppThemeMode` -- a host renders its
own mode control against that hook and the whole subtree re-renders with
the new palette:

```tsx
import { useAppThemeMode } from '@speed/ui-kit'

function ModeToggle() {
  const { mode, toggleMode } = useAppThemeMode()
  return (
    <button type="button" onClick={toggleMode}>
      {mode === 'dark' ? 'Light' : 'Dark'}
    </button>
  )
}
```

Each layer is optional and merges copy-on-write through the tokens
package's `deepMerge` (no input mutation; untouched branches stay shared
by identity). The default tree those branches stay shared with is
deep-frozen at assembly in `@speed/tokens`, so a write through a shared
branch of the merged result throws in strict mode rather than polluting
the defaults; only branches an override rebuilt are writable, and a write
there is local to that result. The factory then maps the merged tree onto
MUI's theme surface:

| Token section | MUI theme slot | Adapter decision |
|---|---|---|
| `color.semantic.<role>` | `palette.<role>` (main/light/dark/contrastText) | key-for-key |
| `color.neutral` | `palette.grey` | steps 50-900 by number; A100/A200/A400/A700 alias the same-number steps (MUI's own grey aliasing); tone 950 has no MUI slot and stays token-only |
| `color.text/background/divider` | same-named slots | direct |
| `typography.fontFamily.sans` | `typography.fontFamily` | direct; the stack ends in CJK-capable fallbacks |
| `typography` sizes/weights | `typography` variant roles | h1-h6 from 5xl..lg (tight line height, semibold); subtitle1/2 from md/sm (normal, medium); body1/2 from md/sm (normal, regular); button from sm (medium); caption from xs; overline from xs (semibold, wide spacing). Converted to rem on MUI's 16px default base; `fontWeight.light` has no token and keeps MUI's 300 |
| `spacing.unit` | the theme spacing unit | the token unit IS the MUI unit (probe-verified against MUI 9.4.0: a numeric spacing option sets the base, so `theme.spacing(n) === n x unit`) |
| `shape.borderRadius` | `shape.borderRadius` | direct |
| `breakpoints.values` | `breakpoints.values` | direct (slot names are MUI's own) |
| `zIndex.values` | `zIndex` | direct (slot names are MUI's own) |
| `shadows` (6 elevation slots) | `theme.shadows` (25-entry ramp) | each elevation i uses the nearest token slot at or below i, floored, never exceeding the design scale; `none` at 0 |
| touch-target floor | `components.*.styleOverrides` | each MUI base control gets a 44x44 minimum width and height under `@media (pointer: coarse)`; a precise pointer gets no declaration |

**The theme carries the touch-target floor.** On a coarse pointer every
MUI base control reaches 44x44 through the theme alone: `Button`,
`IconButton`, `Checkbox`, `Radio`, `Switch`, `ListItemButton`, the
remaining controls rooted in `ButtonBase` (`Tab`, `MenuItem`,
`ToggleButton`, `CardActionArea`, `TableSortLabel`), `InputBase`'s root
and input slot, `Link`, and a clickable `Chip` (a status chip keeps its
compact size). The `Link` rule also makes the inline anchor an
`inline-flex` box, because a minimum height has no effect on a
non-replaced inline box. A precise pointer emits none of these
declarations, so desktop rendering is unchanged. The rules live in
`src/theme/touch-floor.ts`; `touchTargetSx` remains the fallback for
interactive elements outside MUI's controls.

The returned theme is locale-free on purpose: MUI's built-in texts
(table pagination, tooltips) are language-bearing, so the locale merge
happens at render time. `AppThemeProvider` does it for you -- it merges
the MUI locale of `i18n.language` over the base theme, subscribes to
`languageChanged` so MUI texts follow every switch, and renders the
theme-aware `CssBaseline` once. A language outside the MUI locale table
never throws mid-render: the i18n supported set is host-chosen and may
include languages the two MUI mappings do not cover, and MUI's built-in
texts then fall back to the en-US MUI locale while the app's own
translations keep rendering in the active language. Its props are the
i18n instance, the optional two token layers, the initial color mode
(`defaultMode`, default `'light'`), and children. Hosts composing their
own theme tree do the same merge explicitly:

```ts
import { createTheme } from '@mui/material/styles'
import { muiLocaleFor } from '@speed/i18n/mui-locale'
const theme = createTheme(appTheme.theme, muiLocaleFor(i18n.language))
```

The provider deliberately renders no `I18nextProvider` and registers no
namespace: both belong to the host bootstrap.

## Components

All components share one contract: **fully controlled**. Every piece of
state is owned by the host and flows through props; a component echoes
state back and fires change callbacks. Components never fetch, never
mutate data, never keep business state -- so the same component works in
a form, a modal and a server-rendered screen without surprises.
`FileUploader` follows the same contract: its queue renders from
host-owned `rows` props, and every pick, cancel, retry and remove
reports up through a callback -- an upload in flight is host state,
running in the host's own transport code, and the component never holds
a File past the event handler that received it. The one
interaction-local detail anywhere in the family is ConfirmDialog's
double-confirm arming (interaction state that resets whenever the
dialog closes); nothing else is local, and no component fetches or
keeps business state.

### PageHeader

The page-level heading block: title as a semantic `h1` (one per page),
an optional breadcrumb trail above it, an optional description in
secondary body text, an optional trailing action area.

The breadcrumb trail is a `breadcrumbs?: PageHeaderBreadcrumb[]` of
`{ label, href?, onClick? }` steps rendered inside MUI's `Breadcrumbs`
within a `nav` landmark. The link contract: a crumb with `href`
renders as a link (attach navigation-interception handlers through
`onClick`); a crumb without `href` renders as plain text and is never
interactive. The last crumb stands for the current page and is marked
`aria-current="page"` whether it links or not. Labels are caller
content -- render them already translated, matching the rest of this
component's props. The nav's accessible name (`pageHeader.breadcrumbNav`)
and the label of MUI's collapse-expand button, shown once a trail
exceeds eight crumbs (`pageHeader.showFullPath`), are the component's
only built-in strings, taken from the ui-kit namespace like every other
component's text -- MUI's stock expand label is an English literal and
never ships unreplaced. Props: `title` (ReactNode, required),
`breadcrumbs?`, `description?`, `actions?`, `sx?`.

### EmptyState

The three stock placeholders, chosen by `variant`: `'empty'` (no data
yet), `'noPermission'` (viewer not allowed) and `'error'` (load
failed). Each variant ships built-in bilingual title and description
from the namespace; `title`, `description`, `action` and `icon` props
override or extend them, for when the stock wording does not fit (an
error with its own retry story). The icon is decorative by contract
(`aria-hidden`) -- text carries the meaning, and a custom `icon` should
stay decorative or come with its own label.

The title renders as a real heading element, and `headingLevel`
(`'h1'`-`'h6'`, default `'h6'`) picks WHICH one: `EmptyState` is
routinely the sole content of a section whose own heading it stands in
for (an empty list, a failed load), so it has no page-structure
knowledge of its own -- only the host knows what heading, if any,
precedes it on the real page. Pass the level that continues that page's
own order without a skip; the `'h6'` default only preserves the
behavior every caller had before this prop existed, it is not usually
the right level. The visual size stays fixed regardless of
`headingLevel` -- MUI's `component` override changes the semantic tag,
never the `variant="h6"` look, the same style/semantics split
`PageHeader`'s `h1` and every section's own `h2` already rely on. The
same obligation extends to an empty `DataTable`'s stock placeholder:
`DataTable` forwards the level through its own `emptyHeadingLevel`
prop (default `'h6'`, unchanged for hosts that do not pass it).

A placeholder that must NOT enter the heading outline -- an empty state
sitting under a page heading that already names the space ("No
notifications" under a "Notifications" h1) -- passes
`headingLevel="none"`: the title then renders as a non-heading block (a
`<div>`, never a `<p>` -- a styled `<p>` would itself trip axe's
p-as-heading rule) and the whole placeholder becomes one `role="status"`
region, announced politely without claiming a heading level.

### ConfirmDialog

The controlled confirmation modal. `open` shows it, `onConfirm` /
`onCancel` report the two exits; Escape and backdrop clicks call
`onCancel`, never `onConfirm`. `confirmLoading` renders the confirm
button busy and freezes both exits. Variants: `'default'`, and
`'danger'` for destructive confirmations, which paints the confirm
button in the error palette role and -- paired with `doubleConfirm` --
re-labels the button with the built-in "click again" text on the first
click, firing `onConfirm` only on the second. The arming click also
holds the confirm button inert for `CONFIRM_ARM_LOCKOUT_MS` (600ms):
the window outlasts the platform double-click threshold, so a double
click -- two clicks, the second one landing on the re-labelled button
~100-200ms after the first -- cannot skip the guard, while a user who
read the new label clicks after the window by construction. The two-step
guard is interaction state only and resets whenever the dialog closes
(which also cancels the lockout's pending re-enable timer); the lockout
covers the confirm button alone -- Cancel, Escape and the backdrop stay
live throughout it. Hosts should pass the real business content as
`title`/`message` (any ReactNode, so their own translations flow
naturally); buttons fall back to namespace defaults.

### SidePanel

The controlled modal side panel for a secondary task that needs more
room than a dialog but still lets the operator refer to the page behind
it -- a list row's detail, a create form wider than a confirmation.
`open` shows it; the close button, Escape and a backdrop click all report
`onClose` and nothing closes itself, so the host may refuse a close (an
unsaved-content guard, a confirmation first). `busy` makes all three
close paths inert: the close button is disabled and Escape and backdrop
clicks do not report. `onExited` fires once when the exit transition
ends: by then the page behind is reachable again and the panel is still
in the document, and the content unmounts in the render commit
immediately after. During the exit the panel renders whatever children
the host gave it, so a host that clears its content in the same state
batch as the close gets a blank slide-out, and one that needs the
content until the end clears it in `onExited`.

The panel is anchored to the right edge and fills the viewport height.
Its header (title, optional description, close control) is fixed and the
content area is the only scroll layer. `size` picks the width tier from
the viewport's `sm` up -- `'medium'` (the default) is 36rem, `'wide'` is
52rem -- and below `sm` the panel fills the viewport width. The panel
stacks at `theme.zIndex.modal`, above the AppShell AppBar (which sits at
`theme.zIndex.drawer + 1`); dialogs use the same tier and sit above an
open panel by mounting later.

The title renders as an `h2` (the page heading is `PageHeader`'s `h1`,
so the panel's own sections continue at `h3`) and names the dialog;
`description`, when given, becomes its accessible description. `title`
and `description` are host content -- pass them already translated. The
close control's accessible name comes from `closeLabel`, defaulting to
the namespace's `sidePanel.closeLabel`. Focus follows MUI's modal
behaviour: opening moves focus into the panel, Tab loops inside it, and
closing restores focus to the opener; if the opener has left the
document, focus falls back to `body` and the host places it in
`onExited`.

```tsx
<SidePanel
  open={selectedId !== null}
  title="Account details"
  description="Review the account and its sessions."
  size="wide"
  busy={saving}
  onClose={() => setSelectedId(null)}
  onExited={() => setSavedNotice(null)}
>
  <AccountDetail id={selectedId} />
</SidePanel>
```

Props: `open`, `title` (required), `description?`, `size?`, `busy?`,
`closeLabel?`, `children`; callbacks `onClose` (required), `onExited`
(required).

### The form family (FormField + FormLayout)

The form family's shape: screens own a react-hook-form `useForm`
instance, `FormLayout` supplies the skeleton and context, `FormField`
adapts one field. Validation runs through a zod schema wired in as the
form's resolver by `zodFormResolver` (below); a screen with no zod
schema can still pass RHF `rules` to `FormField` directly. Automatic
derivation of a schema from the generated types is not implemented (see
Deferrals).

**FormLayout** -- the skeleton: installs the `FormProvider` context (so
`FormField` children need no `control` prop), renders the `<form>` with
RHF's `handleSubmit` wired when `onSubmit` is given (native browser
validation off -- `noValidate` -- required markers come from RHF rules,
which speak the ui-kit error contract), the field flow with uniform
`spacing` (default 2 theme units) and the right-aligned `actions` row.
`maxWidth` (px, default 600; `false` widens to the parent) constrains
the flow; the form element itself carries no styling beyond layout.
Pass no `onSubmit` to render a bare field flow (a section inside a
larger form, a filters panel). Props: `form` (the host's
`UseFormReturn`, required), `children`, `onSubmit?`, `actions?`,
`spacing?`, `maxWidth?`, `columns?`.

`columns?: 1 | 2` (default `1`) is the opt-in responsive addition:
omitting it (or passing `1` explicitly) renders the exact single-column
flex flow -- zero behavior change for every existing consumer. Passing
`columns={2}` switches the flow to a CSS Grid with two responsive
column tracks: one track (a single column) below the form's container
`sm` (600px), two equal tracks from it up. Each direct child (typically
one `FormField` per cell) becomes one grid cell in source order; the
`actions` row spans every column track in grid mode
(`gridColumn: '1 / -1'`) so it always reads as one full-width row,
matching its single-column appearance.

The trigger is the form's container, not the viewport: FormLayout wraps
its flow in a box that declares `container-type: inline-size`, and the
two-track rule is a container query against that box -- a form filling
the content region and the same form in a narrow side panel collapse at
their own widths, independent of the viewport. `sm` is the deliberate
container width, not `md`: a two-field row (name and email side by
side, say) still has legible field widths as soon as the form clears
the phone/tablet-portrait boundary, and a form whose fields genuinely
need the full row width can still pass `columns={1}` regardless of
container -- there is no per-field override to a wider span.

```tsx
<FormLayout
  form={form}
  onSubmit={onSubmit}
  columns={2}
  actions={<Button type="submit">Save</Button>}
>
  <FormField name="firstName" render={(s) => <TextFieldSlot state={s} label="First name" />} />
  <FormField name="lastName" render={(s) => <TextFieldSlot state={s} label="Last name" />} />
</FormLayout>
```

**FormField** -- one field's RHF plumbing collapsed: a `Controller`
bound to `name` under `control` (or the context FormLayout installs),
with the validation-error text contract applied at render time. It
renders no chrome of its own: the host's control renders through the
`render` prop and receives the bound field state plus the resolved
error text -- the common wiring being MUI TextField's `error`/`helperText`:

```tsx
<FormField
  name="email"
  control={control}
  label="Email"
  required
  render={({ field, invalid, errorText, required }) => (
    <TextField
      {...field}
      label="Email"
      required={required}
      error={invalid}
      helperText={errorText ?? undefined}
    />
  )}
/>
```

`required` is a convenience switch: it injects the `form.required`
rule (unless `rules` already define one) and surfaces itself on the
render state for host asterisks. The render state
(`FormFieldRenderState`) carries `field`, `invalid`, `isTouched`,
`required`, `errorMessage` (raw) and `errorText` (resolved display
text). Error messages follow the validation-error contract: a message
that is a ui-kit-namespace key (`form.required`, `form.invalid`, or any
other ui-kit key) renders as its translation in the current language;
anything else -- already-localized host text, a host-specific code --
renders verbatim. ui-kit resolves keys in its own namespace only and
never guesses another namespace's codes. `REQUIRED_ERROR_KEY` exports
the built-in key for hosts that build rules of their own.

MUI's own controls already render label + helper text with the aria
wiring, so the family deliberately ships no second label/error
renderer: the "uniform error display" is this state contract plus
FormLayout's spacing skeleton.

**zodFormResolver** -- the zod bridge: `zodFormResolver(schema)` returns
the react-hook-form resolver for a zod schema, so the whole form
validates through one schema instead of per-field rules:

```tsx
import { useForm } from 'react-hook-form'
import { z } from 'zod'
import { zodFormResolver } from '@speed/ui-kit'

const schema = z.object({ email: z.string().email() })
const form = useForm<z.infer<typeof schema>>({
  resolver: zodFormResolver(schema),
})
```

The bridge emits ui-kit-namespace keys, never zod's own English text: a
value that is missing (zod reports `invalid_type` with no input) becomes
`form.required`; any other failure becomes `form.invalid`. Both render
through `FormField` in the active language, like any other namespace
key. Issues fold into react-hook-form's nested error object by their
zod path, so a nested field (`address.city`) surfaces on that field.
`INVALID_ERROR_KEY` exports the non-required key. zod is a peer (see
Dependencies): the host builds the schema, so the schema instance and
the package's `ZodType` are one class.

### DataTable

The fully controlled table view. `rows` is by contract the set of rows
the host wants shown right now -- the component never re-sorts or
re-slices, because an implicit second sort would corrupt the
server-side page a host just fetched and slicing would hide rows the
pagination labels still count. Sorting and filtering therefore appear
as state echo plus callbacks:

- `sort: DataTableSort | null` + `onSortChange` -- renders the
  `aria-sort` state on the header cell and makes each sortable header
  the click target (click cycle: a new column ascends, the active
  column flips); the host applies the sort to the data it passes in.
- `filter: DataTableFilter { value, onValueChange }` -- renders the
  labeled filter input; the field-to-field filtering logic stays with
  the host.

Selection is enabled by passing `onSelectionChange`; `selectedRowKeys`
then holds `rowKey` results (index-keyed by default -- pass an id-based
`rowKey` once a table can reorder). The header checkbox toggles exactly
the currently rendered rows and leaves other pages' keys untouched.
`pagination: DataTablePagination` renders the MUI TablePagination
footer with namespace labels; `count: -1` (unknown total, infinite
scroll) switches the counter to the no-total wording. A `loading` table
shows a status row only while `rows` is empty (never blanks content
mid-read); an empty table renders the stock EmptyState placeholder,
overridable via `emptyTitle`/`emptyDescription`/`emptyAction`, whose
title level the host continues the page's heading order through
`emptyHeadingLevel?` (default `'h6'` -- see the EmptyState section for
why the stock default cannot be right for every page). The loading
announcement is a live region mounted empty for the whole empty phase
and filled on refresh (same rule as FileUploader's region: a live
region announces content changes that follow its own existence, never
text that mounts with it), so the empty-list-then-refresh path is
audible. Extra props: `rowKey?`, `size?` ('small' | 'medium'), `sx?`.

**Horizontal-scroll container is a stated, tested contract, not an
accident of implementation**: the table always renders inside MUI's
`TableContainer`, whose default styling gives the wrapper
`overflow-x: auto`. More or wider columns than the host's container
scroll horizontally inside that wrapper rather than overflowing the
page or squeezing column widths -- true regardless of column count or
width, and needs no prop. `DataTable.test.tsx`'s
"horizontal-scroll container" test pins this by rendering a
deliberately over-wide column set and asserting the wrapper is a real
`TableContainer` with `overflow-x: auto`, so a refactor that drops
`TableContainer` would fail a test instead of silently regressing.

**Cell wrapping is per-column and opt-in, and it is what keeps the
scroll fallback reachable.** Under normal text flow a cell breaks
mid-token, so the table shrinks back to its container and the wrapper
never overflows. A column's `cellWrap?: 'wrap' | 'nowrap'` therefore
lets each column state its own line policy: `'nowrap'` renders the
header and every body cell on one line, so a short value (`'Enabled'`,
an action label) cannot split across two rows and an identifier cannot
break in the middle; the column then contributes its full unbroken
width to the table, which the `TableContainer` above scrolls when the
host's container is narrower. Omitting the field -- or passing
`'wrap'` -- keeps normal text flow, which is what prose-like cells
need, so the policy is a per-column choice rather than a table-wide
switch and a host that sets it nowhere is byte-for-byte unaffected.
`DataTable.test.tsx`'s "cell wrap policy" cases pin the declared
`white-space: nowrap` on an opted-in column's header and body cells,
its absence on a column that does not opt in, and that an explicit
`'wrap'` emits no declaration; jsdom does no layout, so this proves
the declarations, not a rendered width.

**An unbroken cell is not by itself a reachable value.** When a nowrap
column is wider than the table's container, the complete text lives
past the scroll edge. A column's `cellFullValue?: (row) => string |
undefined` is the second channel: it returns a row's complete value,
which the table renders as the body cell's `title` (hover) and beside
the cell content as a focusable copy control (keyboard and touch, its
copy announced in the active language). Both channels are per-column
opt-ins like the line policy -- a column that omits the accessor renders
exactly the cell its renderer returns, and a row it answers `undefined`
for gets neither. Identifier columns are where a host sets both
`cellWrap: 'nowrap'` and `cellFullValue`. `DataTable.test.tsx`'s
"cell full-value affordance" cases pin the title and the copy control's
clipboard write and announcement; the clipboard stub is the test's own,
since jsdom ships no `navigator.clipboard`.

**A copy control widens the cell it sits in, so cap the whole cell.** A
column's `contentMaxWidth?: number | string | { '@': ..., '@md': ... }`
bounds the rendered content and the `cellFullValue` copy control
together: the text truncates inside the cap while the control keeps its
own width and is never clipped. The responsive form answers to the table
container's width, like `priority` (`'@'` is the 0px baseline, `'@md'`
from 900px). Only capping the text would leave every such column one
control wider than declared. Omitting the field leaves the column's
rendering byte-identical to the one it had before the field existed; a
row whose `cellFullValue` answers `undefined` is still capped but gains
no control. The shared primary-column cap is exported both as the pair
`PRIMARY_COLUMN_MAX_WIDTH` and in this field's container-query form
`PRIMARY_COLUMN_CONTENT_MAX_WIDTH`; pages pass the latter.
`DataTable.test.tsx`'s "whole-cell content cap" cases pin the wrapper's
container-query declaration, the truncating sub-box, and the control
outside it; the README's Deferrals record why a jsdom suite cannot prove
a rendered width.

**Column priority is the opt-in reflow alternative to that scroll
fallback, and it is container-triggered.** The table's root box declares
`container-type: inline-size`, so every responsive rule inside the table
answers to the table's own width -- how wide the host placed it -- not
the viewport: the same table filling the content region and squeezed
into a narrow side panel sheds its columns at their own widths. A
column's `priority?: 'high' | 'medium' | 'low'` hides it below that
tier's container width on the same shared scale every other responsive
surface in this repo reads from (`@speed/tokens`' `breakpoints.values`:
xs:0/sm:600/md:900/lg:1200/xl:1536):

| Priority | Visible from | Hidden below |
|---|---|---|
| `low` | `lg` (1200px) | `lg` -- the first column dropped as the container narrows |
| `medium` | `md` (900px) | `md` |
| `high` | `sm` (600px) | `sm` -- the last column dropped, only below the narrowest panel widths |
| unset (default) | always | never -- the exact behavior |

A narrowing container therefore sheds the least important columns
first and keeps shedding until only the always-visible columns (no
`priority` set) remain, even at the container's narrow end. Omitting
`priority` on every column renders with no visibility override at all --
a host that sets no column's priority sees byte-identical rendering at
every container width, and the `TableContainer` scroll fallback above
still applies to it. The filter/toolbar row degrades by the same
container: the filter input is 20rem wide from the container's `sm` up
and fills the row below it.

The hiding is pure CSS -- a container-query-keyed `display` value
(`none` at the container's narrow end, `table-cell` -- a table cell's
own default display, restored explicitly -- at and above the tier's
width) on the header and body cell, never a JS layout decision. Unlike
`AppShell`'s drawer-variant switch (`@speed/layout-kit`, a
`useMediaQuery` boolean), a hidden table cell has no interactive state
to preserve across the switch, so an `sx` container query does
everything a JS re-render would, at no extra cost. `DataTable.test.tsx`'s
"column priority" tests pin the container declaration, the three tiers'
generated CSS (the same generated-CSS-text technique `FormLayout`'s
`columns={2}` test uses, documented under Testing below) and that a
prioritized column stays a real DOM column rather than being
conditionally rendered away -- jsdom evaluates neither real layout nor
container queries, so this proves the right declarations, and the
container that makes them live, were wired in, not that a real container
hides or shows the column correctly.

**Pointer capability is CSS-only and additive.** The row highlight is a
hover affordance, so it is enabled only under
`@media (hover: hover) and (pointer: fine)` -- a coarse pointer, even at
a wide viewport, gets no highlight that would stick after a tap. The
selection controls raise their hit area to the 44x44 touch-target floor
under `@media (pointer: coarse)`. Both media features are spelled once in
`src/internal/pointer.ts`.

```tsx
const columns: DataTableColumn<Member>[] = [
  { id: 'name', header: 'Name', sortable: true, cell: (row) => row.name },
  // Visible everywhere -- same as every column before this feature.
  {
    id: 'email',
    header: 'Email',
    // An identifier-like value: never break it mid-token; let the
    // container scroll instead, and keep the complete value reachable
    // by hover, keyboard and touch.
    cellWrap: 'nowrap',
    cellFullValue: (row) => row.email,
    // Bound the text and the copy control together. 160px below the
    // table container's `md`, 200px from it up.
    contentMaxWidth: PRIMARY_COLUMN_CONTENT_MAX_WIDTH,
    cell: (row) => row.email,
  },
  // Dropped first as the container narrows (hidden below the container `lg`).
  {
    id: 'joinedAt',
    header: 'Joined',
    priority: 'low',
    cell: (row) => row.joinedAt,
  },
  // Dropped last, only below the narrowest panel widths (the container `sm`).
  {
    id: 'plan',
    header: 'Plan',
    priority: 'high',
    cell: (row) => row.plan,
  },
]
```

### FileUploader

The file picker and transfer-queue view, fully controlled like every
component in the family. `rows: readonly FileUploaderRow[]` is the
queue the host wants shown right now -- one row per transfer, with the
row's `status` ('uploading' | 'succeeded' | 'failed'), an optional
`progress` fraction within [0, 1] (absent means an indeterminate bar;
an out-of-range or non-finite fraction folds into [0, 1] at render
time) and an optional `error` string rendered verbatim on failed rows.
The component never fetches, never holds a File past the event handler
that reported it and keeps no record of rows that are not in the
current `rows` prop: an upload in flight is host state, executed by
host code.

Every interaction reports up through a callback: `onSelectFiles` (a
pick's or drop's files, in order, the presence of which also renders
the picker trigger), `onCancel(rowId)` on uploading rows,
`onRetry(rowId)` on failed rows and `onRemove(rowId)` on settled rows
-- aborting is the host's own controller to perform, and a retry
re-launches the host's transport with the same `File`. Without
`onSelectFiles` the component is a pure queue view. Failed rows' error
text is host-written and host-translated (the same contract as the form
family's validation-error text); pre-flight validation (size, type,
count) is the host's job, done before its transport starts. A real
host's transport typically calls a generated api-sdk storage operation
(the merged API document carries no storage fragment, so no generated
storage operation exists); ui-kit ships no endpoint, and the usage
example's scripted fetch stub stands in for that call.

Other props: `multiple` (several files per selection; false by
default), `accept` (forwarded to the picker; advisory only -- real
validation is the host's upload code), `allowDrop` (makes the same
selection zone accept a drop, reported through the same
`onSelectFiles`; the keyboard path never depends on it), `disabled`
(renders the affordance inert and ignores picks and drops),
`chooseFilesLabel` (zone label; the built-in bilingual text by default,
naming dragging too when `allowDrop` is on). The selection affordance is
one zone: a label wrapping a visually hidden but focusable file input --
one click target and one drop target for the whole control, one tab
stop, focus shown through the label's `:focus-within` rule. Each row's
action-button group
(Cancel while uploading; Retry/Remove once settled) is a responsive
`flexWrap: 'wrap'` flex row, so a long file name plus several buttons
wraps onto a second line instead of overflowing a narrow queue card --
purely additive CSS, no prop or behavior change. Settles announce in a single polite
live region (`role="status"`, rendered only while the queue has
content), stored structurally and rendered in the language active at
render time: a row leaving `uploading` announces, and so does a row the
host appends already settled (its transfer ran entirely in host code,
with no uploading commit in between); rows the host seeds on mount,
rows appended as `uploading` and progress-only changes stay quiet. A
retry clears the region first so an identical later failure
re-announces, and a settle whose announcement text would repeat the
standing one (a second same-name row reaching the same outcome) empties
the region and re-announces a tick later -- a live region only speaks
when its text changes.
Props: `rows` (required), `onSelectFiles?`, `onCancel?`, `onRetry?`,
`onRemove?`, `multiple?`, `accept?`, `allowDrop?`, `disabled?`,
`chooseFilesLabel?`, `sx?`.

### InlineError (the failure banner)

The one rendering of a whole-attempt failure: a `role="alert"` error
Alert for a non-null code, nothing for `null`. The banner is
namespace-agnostic -- text comes from the `resolve` prop, which a
surface family binds to its own namespace's error-text resolver -- so
no API message and no cross-language fallback can reach a user.
`errorCodeOf` is the companion classifier: an ApiError-shaped failure
keeps its code, anything else collapses to a code no resolver lists
(rendered as the family's unknown fallback).
Props: `code` (required, `string | null`), `resolve` (required).

### InlineSuccess (the success banner)

The positive twin of `InlineError`: the confirmation a landed write
shows where a failure would show a banner. It is one `role="status"`
region, mounted for the whole settled phase whether or not there is
anything to confirm -- visually silent and clipped (never `display:none`,
which would not be live) while the message is `null`, and filled when a
write lands, so a screen reader announces a change to a region it already
knows rather than one born together with its text. The text comes from
the `message` prop (the surface family's own namespace); nothing renders
for `null`. The banner takes no styling props: `WriteFeedbackRegion`
renders it inside the region's root and owns the spacing, and call sites
render the region, never this banner on its own.
Props: `message` (required, `string | null`).

### Write feedback (useWriteFeedback + WriteFeedbackRegion)

One feedback scope per control group: the `useWriteFeedback` instance
holds the group's single result slot, and `WriteFeedbackRegion` renders
it directly under the controls. The slot holds at most one outcome, so a
success and a failure never stand beside each other, and the attempt
that settles last occupies it.

`run(attempt)` is one operator-initiated action: it clears the slot,
counts the attempt in flight (`pending` stays true until every attempt
settles), awaits `attempt` and settles the slot from it -- a returned
string (including an empty one) becomes the success message (an empty
string is stored verbatim and renders a banner with no text, so callers
must pass non-empty copy), `null` does not touch the slot (a write
refused for step-up is handed to the elevation gate and its replay is a
new `run`; the slot is empty from the attempt's own reset, and a result
filled later by an overlapping attempt or by `succeed` / `fail` stays),
and a throw becomes a failure classified through `errorCodeOf`. `run`
never rejects. `succeed`, `fail` and `reset` serve
flows that are not one promise; `reset` clears the slot without
cancelling attempts in flight. Callbacks keep their identity for the
component's lifetime.

The success message is the string the host translated when it called
`succeed` -- the slot stores it, so a later language switch does not
rewrite it. Failure codes are stored as codes and resolved by the
region's `resolve`, so they follow the active language.

```tsx
import { useState } from 'react'
import Button from '@mui/material/Button'
import TextField from '@mui/material/TextField'
import {
  CLIENT_TRANSPORT_ERROR_CODES,
  createErrorTextResolver,
  useTranslation,
} from '@speed/i18n'
import { useWriteFeedback, WriteFeedbackRegion } from '@speed/ui-kit'

// The codes the tenant namespace carries text for: its own refusal codes
// plus the transport codes every write can reach. Anything else resolves
// to errors.unknown, so a raw key never reaches the alert.
const ERROR_TEXT_CODES: ReadonlySet<string> = new Set([
  ...CLIENT_TRANSPORT_ERROR_CODES,
  'tenant.name_required',
])

function TenantNameSection() {
  const feedback = useWriteFeedback()
  const { t } = useTranslation('tenant')
  const [name, setName] = useState('Acme')

  return (
    <section>
      <TextField
        label={t('nameLabel')}
        value={name}
        onChange={(event) => setName(event.target.value)}
      />
      <Button
        loading={feedback.pending}
        disabled={feedback.pending}
        onClick={() => {
          void feedback.run(async () => {
            if (name.trim() === '') {
              throw { code: 'tenant.name_required' }
            }
            await renameTenant(name)
            return t('renamed')
          })
        }}
      >
        {t('save')}
      </Button>
      <WriteFeedbackRegion
        outcome={feedback.outcome}
        resolve={createErrorTextResolver(t, ERROR_TEXT_CODES)}
        sx={{ marginTop: 2 }}
      />
    </section>
  )
}
```

`renameTenant` is the host's own API call, and the `tenant` namespace is
registered by the host at bootstrap; ui-kit ships no endpoint and no
copy for this scope. The namespace must carry `errors.unknown` beside
the codes in its whitelist. The region's `sx` applies to its root
container after the root's default top margin, and the success and
failure banners share that one margin.

### ListSkeleton (the pending-state placeholder)

The loading announcement of a list read: a `role="status"` region with
the passed accessible name and `aria-busy`, one envelope row per entry
with the list's spacing and the divider between rows. The caller passes
what each row looks like (`rows` entries mirror the content rows); the
announcement contract and the row envelope are the component's.
Props: `label` (required), `rows` (required).

### AsyncSection (the four-state branch)

The fixed rendering of the four states of an async read: `pending`
renders the `loading` announcement, an absent `payload` renders the
retryable error placeholder (`errorState`), an `empty` payload renders
the empty placeholder (`emptyState`, optional for surfaces whose
payload cannot be empty), otherwise the content callback renders with
the narrowed payload. The placeholders are `EmptyState` at
`headingLevel` (default `'h2'`, the section-title level they stand in
for); `emptyHeadingLevel` (default `headingLevel`) overrides the EMPTY
placeholder's own level, so a host whose empty placeholder must not
enter the heading outline passes it `'none'` while the error placeholder
keeps `headingLevel`.

`staleState` marks a refresh that failed while earlier data is still on
screen. The caller supplies `{ description, retryLabel, onRetry }`
exactly while that is the state -- the latest refresh failed, the data
is still on screen and no read is in flight -- and withdraws it when the
retry starts. The settled rendering -- the content, or the empty
placeholder when the payload is empty -- keeps rendering and a
`role="alert"` notice with the retry action renders above it;
withdrawing `staleState` unmounts the notice, and a retry that fails
again re-supplies it, so the alert mounts anew and announces again. Both
settled branches keep the same fragment shape, so the notice appearing
or withdrawing never unmounts the settled node or loses the local state
it holds (a draft input, an open row). `description` and `retryLabel`
are the caller's translated copy and should say the shown content may be
out of date. A refusal such as HTTP 403 does not use `staleState` -- the
old data no longer means anything, so the caller renders the
no-permission placeholder in place of the content. The console package's
`AsyncSection` wrapper does this on any 403; the object-store wrapper
does it only when the read has no payload yet, and otherwise leaves the
content on screen. Omitting `staleState` renders exactly the previous
behaviour.

Props: `pending`, `payload`, `empty`, `loading`, `errorState`,
`emptyState?`, `headingLevel?`, `emptyHeadingLevel?`, `staleState?`,
`children` (all required except the marked ones).

### visuallyHiddenSx (the hidden-element recipe)

The one style object for elements that must stay in the accessibility
tree while painting nothing -- the clip technique, never
`display: none`, so live regions stay live and a hidden input stays
focusable. Usable wherever MUI takes styles (`sx={visuallyHiddenSx}`,
`styled('input')(visuallyHiddenSx)`). It serves the kit's own hidden
elements and the consuming packages' hidden live regions.

### touchTargetSx (the coarse-pointer touch-target floor)

The one style object that raises an interactive control to a
finger-sized hit area. It keys on the `pointer: coarse` media feature --
not a breakpoint, so a touchscreen laptop or a keyboarded tablet at a
wide viewport is gated too -- and contributes a 44x44 minimum width and
height on a coarse pointer, nothing on a precise one. Applied as
`sx={touchTargetSx}` on a control, or inside an sx array beside the
control's own styles.

The theme already carries this floor for MUI's base controls
(`src/theme/touch-floor.ts`, spread into the theme by `createAppTheme`),
so a surface built from those controls inherits it without adding the
recipe. The recipe is the fallback for an interactive element the theme
cannot reach -- a bare `<a>`, a host-drawn clickable element, a widget
outside MUI's classes -- and no shipped package surface applies it
today.

## Text and i18n

Every string a component renders by itself comes from the `ui-kit`
namespace (components take their text through a hook bound to
`UI_KIT_NAMESPACE`, so wording follows the active language and switches
live). The full key set:

| Key | Purpose | Notes |
|---|---|---|
| `dataTable.ariaLabel` | table aria-label | |
| `dataTable.loading` | status-row text while loading | |
| `dataTable.rowsPerPage` | pagination footer label | |
| `dataTable.displayedRows` | "from-to of count" counter | interpolates `{{from}}`, `{{to}}`, `{{count}}` |
| `dataTable.displayedRowsUnknown` | counter for unknown totals | interpolates `{{from}}`, `{{to}}` |
| `dataTable.selectAllRows` | header checkbox label | |
| `dataTable.selectRow` | row checkbox label | interpolates `{{row}}` |
| `dataTable.filterLabel` | filter input label and placeholder | |
| `dataTable.copyValue` | full-value copy control label | interpolates `{{value}}` |
| `dataTable.copiedValue` | copy announcement | interpolates `{{value}}` |
| `emptyState.empty.title` / `.description` | stock "no data yet" | |
| `emptyState.noPermission.title` / `.description` | stock "no access" | |
| `emptyState.error.title` / `.description` | stock "load failed" | |
| `confirmDialog.title` | generic confirm heading | host `title` overrides |
| `confirmDialog.message` | generic caution text | host `message` overrides |
| `confirmDialog.confirmLabel` / `cancelLabel` | button defaults | host labels override |
| `confirmDialog.confirmAgainLabel` | danger double-confirm second click | |
| `sidePanel.closeLabel` | close control's accessible name | `closeLabel` overrides |
| `form.required` | required-rule message | `REQUIRED_ERROR_KEY` |
| `form.invalid` | generic invalid-value message | |
| `pageHeader.breadcrumbNav` | breadcrumb nav landmark accessible name | |
| `pageHeader.showFullPath` | breadcrumb collapse-expand button label | replaces MUI's stock English "Show path" |
| `fileUploader.chooseFiles` | selection-zone label without `allowDrop` | `chooseFilesLabel` overrides |
| `fileUploader.dropOrChoose` | selection-zone label with `allowDrop` | `chooseFilesLabel` overrides |
| `fileUploader.statusUploading` | uploading row status text and progress bar aria-label | |
| `fileUploader.statusSucceeded` | succeeded row status text | |
| `fileUploader.statusFailed` | failed row status text | |
| `fileUploader.actionRetry` | failed row retry button label | |
| `fileUploader.actionRemove` | settled row remove button label | |
| `fileUploader.actionCancel` | uploading row cancel button label | |
| `fileUploader.announceUploaded` | live-region announcement when a file uploads | interpolates `{{name}}` |
| `fileUploader.announceFailed` | live-region announcement when a file fails | interpolates `{{name}}` |

The two files (`src/locales/zh-CN.json` and `en-US.json`) carry
identical leaf key sets, enforced by registration and by
`tools/check_i18n_keys.py` in CI. The write-feedback scope adds no key:
the success message is host copy passed to `succeed`, and a failure code
resolves through the region's `resolve` prop in the calling package's
namespace. `AsyncSection`'s `staleState` likewise takes the caller's
`description` and `retryLabel`. Host content (a PageHeader title, a
ConfirmDialog message, a column header) is the host's own translation
surface, and hosts reword the kit by registering their own bilingual
bundle pair -- identical key structure, their wording -- under
`UI_KIT_NAMESPACE` at bootstrap; `uiKitResources` is simply the shipped
default.

## Accessibility

Every component test runs axe over the rendered unit
(`test-utils/axe.ts`), with two deliberate exceptions: `color-contrast`
is disabled because jsdom does no layout or color computation -- a
contrast result there is neither trustworthy nor actionable, and
contrast lives in the theme (palette roles over surfaces), which the
tokens package pins and the factory maps; and `region` is disabled
because the units under test are components, not full pages. Structure
choices worth knowing: PageHeader renders a real `h1` and wraps its
breadcrumb trail in a labeled `nav` landmark with the current page
marked `aria-current`; EmptyState icons
are `aria-hidden` (text carries meaning); ConfirmDialog wires
`aria-labelledby`/`aria-describedby` ids; DataTable uses real table
semantics with `columnheader` sort buttons and `aria-sort`, selection
checkboxes labeled from the namespace (the partial-page header is
`aria-checked="mixed"`), and a filter input whose accessible name
equals its visible text. Do not disable further axe rules without the
same rationale documented here.

## Dependencies

| Package | Kind | Why |
|---|---|---|
| `react`, `react-dom` | peer (required, ^18 or ^19) | the host owns the React tree; one copy makes `useTranslation` and hooks inside shipped components safe. devDependencies pin the tested React 19 |
| `@mui/material` | peer (required, ^9) | theme and components; two MUI copies would split the theme context. devDependency 9.4.0 is the tested point |
| `@emotion/react`, `@emotion/styled` | peer (required, ^11) | MUI's own runtime requirements -- the emotion context must be shared with the host |
| `react-hook-form` | peer (required, ^7) | the form family's `FormProvider`/`Controller` identity needs one RHF in the tree, and `FormLayout`'s `UseFormReturn` must be the host's own type. devDependency 7.87.0 is the tested point (the peer range is the ^7 API family) |
| `zod` | peer (required, ^4) | the host builds the schema `zodFormResolver` consumes, so the schema instance and this package's `ZodType` must be one class -- one zod in the tree, exactly like `react-hook-form`. devDependency 4.6.7 is the tested point |
| `@speed/tokens`, `@speed/i18n` | dependencies | the token tree this package maps, and the i18n instance/bindings every component renders through |

## Deferrals and recorded decisions

- **No browser test runner**: the suite is jsdom plus axe. Rendered
  geometry -- including the whole-cell cap (`contentMaxWidth`) counting
  the copy control at 600/900/1200px containers -- is measured in
  Chromium ad hoc, not by a committed gate; a CI browser leg would need a
  runner dependency and a shared harness.
- **DataTable column-hiding / priority-reflow for narrow containers**:
  shipped (see the DataTable section's Column priority subsection) --
  an opt-in `priority` field on `DataTableColumn` hides lower-priority
  columns first as the table's container narrows through the shared
  breakpoint scale, alongside the unaffected horizontal-scroll fallback
  for columns that don't opt in.
- **Validation from generated types**: a zod resolver ships
  (`zodFormResolver`), and the validation-error contract is the seam it
  plugs into -- the resolver emits ui-kit keys, the family renders them.
  Automatic *derivation* of a zod schema from the API-generated types is
  still not implemented; hosts author the schema.
- **Error-code mapping**: backend error codes render verbatim unless
  they happen to be ui-kit keys. A code-to-text resolver (which
  namespace owns which codes) is not implemented; the form family
  resolves keys in the ui-kit namespace only.
- **Shadows adapter**: the floored 25-entry ramp (nearest token slot at
  or below each elevation) is a deliberate decision over interpolation,
  recorded in `createAppTheme.ts` and pinned by its tests.
- **no-literal-text scope**: the workspace rule catches the direct
  routes (bare JSX text, text-bearing attribute literals, plain-literal
  children) over package `src`. Indirect routes (a ternary whose
  branches are literals, a literal passed into a component across a
  boundary) are not flagged -- hosts own their literals; the rule is
  the floor, not the ceiling.
- **Storybook**: no preview harness exists; components are covered by
  jsdom unit tests plus axe scans, and color-contrast verification is
  not implemented (see Accessibility for what axe can and cannot
  determine under jsdom).
- **Tone 950**: the neutral ramp's 950 step has no MUI grey slot and is
  deliberately not mapped (see the factory table).

## Development

From `web/packages/ui-kit`: `pnpm lint`, `pnpm typecheck`, `pnpm test`,
`pnpm build`. The test suite runs in jsdom
(`vitest.config.ts`); shared helpers live in `test-utils/`
(`renderWithProviders` builds the host tree -- fresh i18n instance per
call, namespace registered -- `expectNoAxeViolations` runs axe -- and
`@speed/test-utils/emitted-css`'s `emittedStyleText()` reads the CSS text emotion has
injected into the document, used by the container- and media-keyed sx
assertions the responsive tests need: jsdom evaluates neither real
layout nor container queries nor `@media` conditions, so those tests are property/snapshot
proofs
that the intended declaration was wired into the render, not proofs
that either side of a breakpoint looks correct at a real viewport or
container width). Bilingual fixtures are the shipped locale files under `src/locales/`,
imported by sources and tests; `src/usage-example.test.tsx` compiles and
executes both the Quick start composition and the "Write feedback"
section above, so the documented usage cannot drift from the API. The workspace `no-literal-text` rule and its
rule tests live at `web/eslint-rules/` (rule tests run from the web
workspace root: `pnpm exec vitest run eslint-rules/no-literal-text.test.mjs`);
the rule applies to this package's `src` but not its test files --
fixture strings are data, not rendered product text.
