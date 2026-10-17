---
name: frontend-coding-standards
description: speed Frontend Coding Standards — Mandatory package boundaries, component, state, theming, i18n, accessibility and API-client rules for the React/TypeScript packages under web/, the platform's frontend package library. The coding rules live here; the design conclusions live in docs/design/modules/web/ (Chinese). Nothing here applies to the Go modules under pkg/, which follow CLAUDE.md and docs/design.
triggers:
  - writing frontend code
  - editing frontend code
  - creating React components
  - editing React components
  - adding hooks or stores
  - frontend code review
  - frontend refactoring
  - modifying MUI theme
  - adding i18n text
  - frontend styling
  - fixing frontend bugs
globs:
  - "web/**/*.ts"
  - "web/**/*.tsx"
  - "web/packages/*/locales/**/*.json"
  - "web/**/vite.config.ts"
---

# speed Frontend Coding Standards

**Scope: the `web/` tree** — the platform's frontend package library: a pnpm workspace whose `packages/*` publish as `@speed/*` into business projects. The frontend's own gates run from `web/` through `pnpm` (`pnpm -r lint | typecheck | test | build`, or `pnpm --filter @speed/<name> <script>`). (The earlier npm implementation is kept whole under `web-legacy/` as history; it is not built or checked, and this handbook does not govern it.)

**Coding rules here, design conclusions in `docs/design/modules/web/`.** The design documents (Chinese) are the authority for the shape — package layering and its criteria, the contract boundary, the assembly/content split, the extension points, the stability promises. This handbook is the authority for how the code is written. It does not restate a design conclusion: where it needs one it names the document. When the two disagree, fix whichever is stale — do not let them drift into two accounts of the same thing.

**The premise that overrides everything else**: these packages are installed into business projects via `npm install` — they are not an application. A component's props are public API, and changing a required prop breaks every delivered project. Every dependency bundled here ends up in someone else's build output.

---

## 1. Package Boundaries and Layering

Independent npm packages, each with its own `package.json`, developed together through the `web/` workspace. The dependency graph **only flows bottom-up**:

```
tokens / i18n -> ui-kit -> layout-kit
api-client -> api-sdk -> auth-core -> auth-ui / account-ui / tenancy-ui
the layers above -> product-shell (the composition root)
```

The diagram is a coarse ordering, not the edge list: each package's own `package.json` is the authority for its dependencies, and `web/pnpm-workspace.yaml` is the membership authority for the package set — do not consult a copy here.

**Required:**
- The pure UI layer (`ui-kit`) carries **no business, tenant or permission semantics**. Components are controlled and props-driven so they survive a different auth scheme.
- Headless packages expose state and hooks, never UI (`auth-core` is the one shipped today: the session, current-tenant and permission hooks); UI packages consume only the data structures from them.
- `react`, `react-dom`, `@mui/material` and `@emotion/*` are declared as `peerDependencies`. Shipping them as regular dependencies gives consumers duplicate React/MUI instances.
- Every package carries its own `locales/{zh-CN,en-US}.json` under a namespace named after the package.

**Prohibited:**
- **DO NOT** let same-layer packages depend on each other (`auth-ui` must not depend on `account-ui`).
- **The package entry is its public surface.** Every package exposes exactly one entry, `src/index.ts`, plus any documented subpath its `exports` map adds (e.g. `@speed/product-shell/bootstrap`, `@speed/api-sdk/runtime`). Consumers import `@speed/<name>`, so the entry re-exports what the package publishes — that is the package's API, not an avoidable convenience. Inside `src/`, import from the specific module; do not stack further re-export layers below the entry.
- **DO NOT** bundle brand assets (logos, font files) inside a package — reference the consuming project's `public/` through tokens.

## 2. API Calls: Generated Code Only

**This is the hardest rule on the frontend.**

```tsx
// Correct
import { useBillingListPlans } from '@speed/api-sdk';
const { data, isLoading } = useBillingListPlans();

// Wrong — rejected in review, and by the workspace's speed/no-direct-http rule
fetch('/api/v1/billing/plans')
axios.get('/api/v1/billing/plans')
const myRequest = (url) => fetch(...)     // a homegrown wrapper is equally wrong
```

- **DO NOT** hand-write any backend call. `fetch` / `axios` are permitted only inside `@speed/api-client`, enforced by an ESLint rule.
- **DO NOT** hand-write endpoint paths or response types — they come from the merged platform document `contracts/api.json` (the backend modules' OpenAPI fragments, merged and committed by the demo host's `gen` command) through the SDK generator (`web/orval.config.ts`).
- **DO NOT** edit any file in `@speed/api-sdk` — it is generated and overwritten wholesale on every release.
- When an endpoint does not fit your need, change the spec, not the frontend. The order is fixed: edit the spec first, regenerate, fix whatever the regenerated interfaces break, then consume — never the reverse.

**Exceptions** (not expressible in OpenAPI, and each must be confined to exactly one place): none ship in the workspace packages today. Uploads need no exception — they are server-relayed through spec-described endpoints (the merged document's `/api/v1/storage/...` operations), and ui-kit's `FileUploader` renders the queue the host owns as `rows` props while the upload call itself stays the host's own code.

## 3. Components

```tsx
type PlanCardProps = {
  plan: Plan;
  selected?: boolean;
  onSelect?: (planId: string) => void;
};

export function PlanCard({ plan, selected = false, onSelect }: PlanCardProps) {
  const { t } = useTranslation('billing');
  return (
    <Card sx={{ p: 2, borderColor: selected ? 'primary.main' : 'divider' }}>
      ...
    </Card>
  );
}
```

**Prohibited:**
- **DO NOT** use `React.FC` — annotate the props type directly.
- **DO NOT** use the inline `style` attribute — use `sx`.
- **DO NOT** hardcode pixel values in `sx` — use `theme.spacing()` or MUI spacing multiples.
- **DO NOT** hardcode colors — use palette tokens (`primary.main`, `text.secondary`, `divider`).
- **DO NOT** use `!important` — override through the theme.
- **DO NOT** call APIs directly in a component — use hooks.
- **DO NOT** hardcode user-visible text in any language (see §6).

## 4. State Management

**Server state** goes through TanStack Query — the hooks generated into `api-sdk`. **Client and session state** stay with the package that owns them, exposed as headless hooks: `auth-core` ships the browser session as a memory-only state machine (no storage writes; see its `README`) plus the React hooks that read the one host-attached session. **No global state library is imposed on the workspace** — do not pull a store library into a presentational package, and do not reach for one where a headless hook over the owner's own state already fits. Redux is not used.

### Query keys must be tenant-namespaced

```ts
['tenant', tenantId, 'members'] // correct: switching tenants changes the key, stale data invalidates itself
['members']                     // wrong: you will read the previous tenant's cache
```

After `switchTenant` succeeds, explicitly call `queryClient.removeQueries({ queryKey: ['tenant', oldId] })` so an operator switching tenants repeatedly does not accumulate every tenant's data in memory.

### The tenant is announced as a request header, never a token claim

- The tenant is a request-time routing fact: the backend resolves it from the request surface, and `@speed/api-client`'s `tenantProvider` seam announces the host's current tenant as the `X-Tenant` header on every attempt. It is not a claim of the access token — the server reads the header, not the token, for tenant-scoped endpoints.
- The session's current tenant (read from the owner's hook, `auth-core`'s `useCurrentTenant`) is used for exactly these: query key namespacing, UI display, the `tenantProvider` value wired into the assembled client, and as the argument to the switch-tenant call. It is not copied into a global store.

### Token storage

- The refresh token lives in an httpOnly, Secure, SameSite cookie.
- The access token stays in memory. **DO NOT** put it in `localStorage` — an XSS then walks away with a long-lived credential.

## 5. Theming and Multi-Brand

Token overlays merge in layers: the package ships `defaultTokens`, and each later layer passes a `TokensOverride` through `deepMerge` (later overlays win) — the consuming project's overlay, then the runtime tenant overlay for white-labeling. `@speed/tokens` is the authority for the exact names and types (`defaultTokens`, `deepMerge`, `TokensOverride`).

- **DO NOT** read brand-specific values inside components — always go through the theme.
- **DO NOT** assume light mode only — components must render correctly in light and dark.
- Consuming projects define only the tokens they change; everything else falls back to the defaults.

## 6. Internationalization

```tsx
const { t } = useTranslation('auth');
<Button aria-label={t('login.submit')}>{t('login.submit')}</Button>
```

- **DO NOT** leave literal user-facing text in a package, in any language — the workspace's `speed/no-literal-text` ESLint rule (see `web/eslint-rules/`) rejects text written inline in a package's `src`.
- New keys must be added to both `zh-CN` and `en-US` in the same change, with identical nested structure; the runtime registration guard rejects a namespace whose languages drift.
- Dates, numbers and currency always go through `Intl.DateTimeFormat` / `Intl.NumberFormat`. **DO NOT** format by hand — CNY and USD differ in symbol placement and decimal digits.
- Error text is resolved from the error code. **DO NOT** display the `message` field returned by the API — it is an English fallback meant for log triage.
- Layouts must not depend on fixed widths to fit text: English runs 30–50% longer than Chinese — check every component's rendering in both languages.

## 7. Permissions and Feature Flags

```tsx
const canManage = usePermission('billing:manage');   // auth-core
const billingEnabled = useFeature('billing');        // api-client
```

- Permissions arrive as a flat list already computed by the server; the frontend performs set lookup only. **DO NOT** reimplement policy evaluation client-side.
- Visibility is the host's composition: the host filters its own routes and derives its nav items from the permission hooks (`product-shell`'s gated-journey suite is the worked shape). The presentational layer stays permission-free by design — `layout-kit`'s `AppShellNavItem` carries no permission or feature fields (see §1's pure-UI rule); **DO NOT** push permission semantics into a presentational component.
- **Frontend permission checks are a UX affordance, not a security boundary** — the server validates independently.
- `product-shell` (tenant users) and any future platform-staff shell operate in different permission domains. During impersonation a persistent, highly visible indicator is mandatory.

## 8. Accessibility

- Icon-only buttons **must** carry `aria-label={t(...)}`.
- Every interactive element is keyboard operable; focus styles must never be removed.
- Dialogs and drawers need correct focus trapping and `aria-labelledby`.
- Color contrast meets WCAG AA (4.5:1 body, 3:1 large text). **DO NOT** convey state through color alone — pair it with an icon or text.
- Support browser font scaling. **DO NOT** define font sizes in `px` — use rem or MUI typography variants.

### The route-level accessibility gate

Two tiers of mechanical accessibility scanning ship. Each package's suites scan
their own components (`@speed/test-utils/axe`). Above them, the demo host's
`starters/demo/web/src/a11y/routes.test.tsx` scans **whole routes**: it walks the
application's route table, renders the assembled app at every path — the tenant
shell and the console shell alike — in three data states (populated, empty,
error), and runs axe-core over each through the harness in `src/a11y/scan.ts`. A
product gains coverage for a new route by adding it to the route table; the sweep
needs no change.

- **It is its own frontend-gate leg.** The root Makefile's `web-a11y` (the demo
  Makefile's `web-a11y`), beside lint / typecheck / test, runs
  `pnpm --filter demo-web test:a11y`. The demo's ordinary `test` run excludes
  `src/a11y/**` and this leg runs only it (`vitest.a11y.config.ts`), so the sweep
  runs once.
- **Rule set**: WCAG 2.1 level AA — the tags `wcag2a`, `wcag2aa`, `wcag21a`,
  `wcag21aa` — **plus the page-structure rules** (`page-has-heading-one`,
  `landmark-one-main`, `region`, `heading-order`). Those four carry only axe's
  best-practice tag and no `wcag*` tag, so the gate runs them by rule id and
  fails on them by rule: a landmark or heading defect that appears only in the
  composition (the frame and the page together) is exactly what this tier is
  for. The remaining best-practice rules run as a separate advisory pass,
  reported and never failing.
- **Threshold**: a `serious` or `critical` axe violation fails the gate, naming
  the route and state and listing the findings by rule id, impact and node count;
  a `moderate`, `minor` or null-impact violation is warned and registered in the
  run's report — **never silently dropped** (warnings are first-class). The
  page-structure rules fail regardless of impact (axe rates them only moderate,
  so an impact-only threshold would warn and pass), and an indeterminate
  (`incomplete`) result is warned, not failed.
- **The two tiers fail differently, deliberately.** The component tier
  (`@speed/test-utils/axe`) fails on *any* violation; the route tier above it
  fails only on serious/critical findings plus any page-structure violation, and
  treats `incomplete` as a warning. So the broader, page-level gate is looser on
  impact than the component scans beneath it — **not** a superset of their
  threshold — because it scans a whole assembled document where a mid-level
  finding from the frame's chrome is noise the component scan would not raise.
- **What it proves, and what it does not.** axe is a mechanical check covering
  only the decidable subset of WCAG — the industry working estimate is 30–40%. It
  cannot judge reading order, whether an accessible name is a *good* name,
  whether a heading level fits *its page*, or whether two inline controls share a
  confusing name. This repository's own history is the proof: the component scans
  were green when a human review still found a heading-order inversion and
  duplicate inline-control names those scans could not see. The gate's value is
  **regression** — a defect once removed cannot silently return — never a claim
  that a page is accessible. Automated scanning does **not** replace manual
  review.

## 9. TypeScript

- The single source of truth for backend-related types is `@speed/api-sdk`. **DO NOT** hand-write response types.
- Strict mode is on. **DO NOT** use `any` (`@typescript-eslint/no-explicit-any` is an error).
- Use `type` rather than `interface` for component props, avoiding accidental declaration merging.
- Public packages export complete type declarations; each package's own `tsc`/build scripts validate the published artifact.

## 10. Forms

The form family is react-hook-form — the host owns the `useForm` instance, `FormLayout` supplies the skeleton and context, `FormField` adapts one field — with validation derived from a **zod** schema.

- Wire the schema in as the form's resolver through `zodFormResolver` from `@speed/ui-kit`: it validates the whole form and emits `ui-kit`-namespace messages (`form.required`, `form.invalid`), which `FormField` renders translated under the validation-error contract. **DO NOT** hand-roll a resolver or emit raw zod text.
- Derive the zod schema from the `api-sdk` types where a field maps to one. **DO NOT** maintain a second rule set beside the contract — the schema is the one rule set, derived from the generated types.
- Validation errors render through `FormField`. **DO NOT** lay out error text per form.

## 11. Write Feedback and Refresh State

The design is `docs/design/modules/web/design-web-ui-kit.md` ("写入的反馈", "异步读的状态分支"); this section holds the coding rules it implies.

**Required:**
- Report the result of every operator-initiated write through a feedback scope: one `useWriteFeedback` instance and one `WriteFeedbackRegion` per control group — the controls that share one result location, usually a titled section with its forms and its table row actions — with the region directly under them. Success is rendered only by the region; never render `InlineSuccess` beside it.
- Run each attempt through `run`; for a flow that is not one promise (a sign-in step), call `reset` when the attempt starts and `succeed` or `fail` when it settles. Start every attempt with `reset`, a submit rejected by client-side validation included. Call `reset` on any context change after which the previous result would read as describing a different context: a tenant or selected-object change, a page change, a refresh.
- Confirm every operator-initiated action that settles without a failure with a translated success message that names what changed. A value handed out once (a token, a secret) gets, in addition, a display panel next to the region: its state is held by the host outside the slot, it has an explicit close control, and its accessible name includes its owner.
- Treat one action as one attempt. An action that issues several requests (a batch save) settles through `mutateAsync` and `Promise.allSettled` inside one `run`, and a partial landing is reported as a failure. Overlapping actions never share one react-query observer's per-call `mutate` callbacks, `isPending` or `variables`.
- Disable and mark busy the control that started a write while it is in flight (MUI `Button` `loading`, as `ConfirmDialog` does), disable the controls that change the scope's context (selectors, paging, refresh) while the scope is `pending`, and make a modal panel `busy` while a write started inside it is in flight.
- Hand a write refused 401 for step-up to the step-up gate: its attempt settles `null` and leaves the slot empty, and the replay is a new `run`.
- Bind the package's own error-code resolver once, in the package's `internal/` wrapper around `WriteFeedbackRegion`; call sites pass the slot and nothing else.
- Close a `ConfirmDialog` no later than the moment its write settles and report the failure in the region after it closes. A modal that stays open (the step-up dialog, a side panel) renders its failure inside itself.
- Give `AsyncSection` a `staleState` exactly while a refetch has failed with data on screen and no read is in flight, and `undefined` otherwise. A refused read (403) replaces the content with the no-permission placeholder instead.
- Give a queue of independent per-item transfers (the `FileUploader` rows) one result per row, not one slot.

**Prohibited:**
- **DO NOT** aggregate the errors of several mutations into one page banner (a "first non-null error" array), and **DO NOT** keep a per-mutation `success` string beside the shared slot.
- **DO NOT** hand-roll a success `Alert`.
- **DO NOT** report a failure only in a region an open modal hides: while a modal panel or dialog is open, the page behind it is hidden from assistive technology.
- **DO NOT** expect a standing success message to follow a language switch: it is the translation made when `succeed` was called.

## 12. Diagnostics and Logging

The browser has no structured log backend, so the equivalent discipline is **structured reporting**: anything worth diagnosing in production goes through the reporter in `@speed/api-client`, not `console`.

```ts
// Correct
reporter.error('checkout failed', { planId, provider, code: err.code, traceId: err.traceId });

// Wrong
console.log('checkout failed for plan ' + planId);
```

- **DO NOT** use `console.log`. `console.warn` / `console.error` are acceptable for local development only; production-relevant signals go through the reporter.
- Keep the message a constant string and put variables in the attributes object — the same rule as the backend, for the same reason: concatenated messages cannot be grouped or filtered.
- Attribute keys mirror the backend (`tenant_id`, `user_id`, `trace_id`, `duration_ms`) so a frontend report can be correlated with server traces.
- **DO NOT** report PII, tokens or full form contents.
- Always include the `traceId` from the API error envelope — it is the only way to connect a user report to server logs.

## 13. Testing

| Tier | Tooling | Scope |
|---|---|---|
| Unit / component | Vitest + Testing Library | hook logic, component behaviour |
| Page / route accessibility | Vitest + axe-core (jsdom) | the assembled app at every route, both shells, in each data state — the demo host's `web-a11y` leg (§8) |
| End-to-end | Playwright | the host application's journeys (the host lives outside `web/`, as a delivered consumer project does) |

**File and directory layout — not optional:**
- **What a unit test is, is a tier — not a file mapping.** A unit test is a same-package, no-external-dependency test the plain unit run executes (the app's own scripted-fetch rig, never a live server). Unit tests alone sit next to the code they test, one file per target — `PlanCard.tsx` is tested by the one `PlanCard.test.tsx`. That includes behaviour suites that span sources rather than mapping 1:1 to one file: they stay beside the sources they exercise, named for the behaviour they verify, never a generic word (`misc.test.ts`, `extra.test.ts`). The backend rule that relocates no-single-target unit suites into a module-level `unittest/` directory is **Go-centric and does not apply here**: it exists because Go's package boundary (internal vs external test packages) is where white-box access lives, so a relocated Go suite must be black-box. TypeScript has no such boundary — a TS suite with no single target stays co-located or in its purpose-named place, with no forced TS analogue.
- **The extension states what the suite renders**: a suite that renders components is `.test.tsx`; a pure-logic suite over a `.ts` module is `.test.ts` — no JSX-less `.tsx`, no JSX-hiding `.ts`.
- **Every non-unit test class lives in its own dedicated directory; nothing non-unit is ever mixed into a source directory.** Browser end-to-end journeys go under `e2e/` (Playwright's own convention), never into a package's `src/` — name each spec for the journey it covers (`checkout-alipay.spec.ts`), not `e2e.spec.ts`. A non-unit suite that fits no existing dedicated directory gets a new purpose-named one, never a seat beside the code.
- Within the co-located unit tier, keep **one test file per source file, full stop**: `PlanCard.tsx` is tested by the one `PlanCard.test.tsx`, however many scenarios that covers — do not split one target's tests by theme. Splitting is a habit to resist, not a style choice; the only legitimate reason is a genuinely unwieldy file (a high bar, not a soft trigger), and even then the split file keeps the target's name as a prefix (`PlanCard.pricing.test.tsx`, not bare `pricing.test.tsx`). If a unit-tier suite genuinely spans targets (a cross-cutting behaviour over several components or modules), it may live as one behaviour-named file (`session-journey.test.tsx`) beside the sources it exercises — never as a theme-split of any single target's file. If you find an existing target already fragmented into multiple theme-split files with no real justification, consolidate next time you touch that area.
- Shared test helpers, fixtures and custom render utilities go in a dedicated `test-utils/` directory per package, never duplicated across test files.

- Test behaviour, not implementation: assert what the user sees. **DO NOT** assert how many times an internal function was called.
- **Every bug fix ships with a test that reproduces the bug.**
- **DO NOT** ignore warnings — React console warnings, a11y warnings and lint warnings are all first-class issues.

## 14. Language

All code comments, TSDoc and package `README`s are written in **English**. The design and decision documents under `docs/design/modules/web/` (and `docs/adr/`) are in Chinese.

Comments and TSDoc describe the code's **current state only**: no history or change records, no implementation-round or finding references, no internal doc-filename citations as authority, no TODO or future promises, no restating what the code says, no counts that go stale as the code evolves. External normative references (RFC, W3C, language specs) and cross-references to code itself are allowed.

## 15. Pre-Commit Checklist

- [ ] No hand-written `fetch` / `axios`; every call comes from `@speed/api-sdk`
- [ ] API change started from the spec, generated artifacts committed
- [ ] No hardcoded text; both zh-CN and en-US present
- [ ] No hardcoded colors or pixel values; everything through theme tokens
- [ ] Query keys are tenant-namespaced
- [ ] Icon-only buttons have `aria-label`
- [ ] The route-level accessibility gate is green (`make web-a11y`); a new route is swept automatically because the sweep walks the route table
- [ ] New public components are checked rendering in both languages and carry prop docs
- [ ] Correct in both light and dark mode
- [ ] Every write reports through a feedback scope, and every action that settles without a failure confirms with a success message
- [ ] peerDependencies not accidentally moved into dependencies
- [ ] Bug fix includes a reproducing test
- [ ] Diagnostics go through the reporter with a constant message and structured attributes
- [ ] No new console warnings
