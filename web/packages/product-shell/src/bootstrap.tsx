/**
 * bootstrapSpeedApp -- the app-entry assembly of the shell tier: the
 * one call a delivered app's main module makes, wired from a single
 * declarative definition, so a new app composes the platform instead of
 * re-deriving it.
 *
 * The entry is a subpath export (`@speed/product-shell/bootstrap`), the
 * api-sdk `./runtime` precedent: the main entry keeps the component
 * package's dependency floor exactly as documented (auth-core, auth-ui,
 * layout-kit, i18n), while this entry additionally imports the pieces an
 * app entry point exists to drive -- @speed/api-client (the credential
 * store, the HTTP client), @speed/api-sdk's runtime seam
 * (bindRequestFn), @speed/ui-kit (the theme provider) and
 * @tanstack/react-query -- every one of them already in the main
 * entry's transitive closure (auth-core depends on api-client and
 * api-sdk; layout-kit and auth-ui depend on ui-kit), so the manifest
 * additions cost a consumer nothing it does not already install.
 *
 * What the assembly wires, in order:
 *
 *  1. i18n -- one fresh bilingual instance over the platform defaults
 *     (?lang= URL parameter, stored choice, navigator languages, en-US
 *     last), built by the exported createSpeedI18n. The namespaces of
 *     the four packages the assembly itself composes register
 *     automatically -- ui-kit, layout-kit, auth-ui and product-shell's
 *     own -- because the stack it renders always includes them; a host
 *     can no longer forget one and read raw keys. The platform error-copy
 *     bundle registers with them and is named as the instance's fallback
 *     namespace, so a backend error code no package namespace covers
 *     resolves to the module catalog's own words instead of the raw key.
 *     The definition's `namespaces` carry the app's own additions: the
 *     slot packages its views compose (tenancy-ui, account-ui, ...) and
 *     its own bundle. Listing one of the four framework namespaces there
 *     replaces its shipped bundle instead of being registered twice.
 *     `resourceOverrides` merges host resources into every namespace's
 *     base through overlayResources before that namespace's one
 *     registration, and the registered language set is then reduced to
 *     the instance's supported set. Registration happens before render;
 *     double registration throws, so each namespace appears exactly
 *     once.
 *
 *  2. The session -- the cookie-delivered auth-core session state
 *     machine over the generated authn operations, attached with
 *     attachSession. The credential is the server's httpOnly session
 *     cookie, which the client's credential mode carries; nothing is
 *     kept in memory here. The assembly calls restore() once at boot, so
 *     a page load re-reads GET /api/v1/auth/session and a still-valid cookie
 *     keeps the user signed in instead of dropping them back to the
 *     sign-in surface. The definition's view (step 5) renders only after
 *     that one restore settles: until the read answers, the page load's
 *     state is unknown rather than anonymous, and painting the anonymous
 *     snapshot first would flash the sign-in surface at every reload of
 *     a signed-in page.
 *
 *  3. The client -- createClient over the environment's own fetch, with
 *     `credentials: 'same-origin'` by default so the session cookie
 *     travels on every same-origin request (and a refused credentialed
 *     request engages the session's silent restore leg) and the i18n
 *     instance as the language source (every request announces the
 *     instance's current language through accept-language, read per
 *     attempt, so the backend's requester-language tiers see exactly what
 *     the frontend chain resolved). The definition's `client` table
 *     supplies any other createClient option -- the credential mode, a
 *     reporter, a retry policy, a timeout, a fetch implementation and the
 *     tenant header's name. The result is bound into the api-sdk runtime
 *     seam every generated operation calls through. All API traffic is
 *     generated-code traffic from here on.
 *
 *  4. The query client and the session-end strategy -- the no-retry
 *     policy below, and watchSessionEnd's shipped default: the moment
 *     the session ends, everything the departing principal's reads
 *     cached is evicted. The definition's `sessionEnded` overrides the
 *     default action.
 *
 *  5. The provider stack -- StrictMode around I18nextProvider around
 *     AppThemeProvider around QueryClientProvider, assembled by the
 *     exported SpeedAppProviders (so a view-level test bench and
 *     production share one construction), then the definition's
 *     `providers` (outermost first) around the definition's `view`, all
 *     of it behind the boot restore's gate (step 2). The
 *     reference composition: `view` is the app's shell composition
 *     (ProductShell with its host slots), and one provider hands the
 *     app's views the assembled session and client.
 *
 * The assembly also owns the document's language declaration: with
 * `documentLanguage` left on (the default), `<html lang>` is set to the
 * instance's language at boot and kept in step with language changes,
 * cleaned up when the page unmounts. `documentLanguage: false` opts out.
 * A document-less environment (a non-DOM test host) is tolerated.
 *
 * The returned handles (root, i18n, queryClient, session) are for
 * hosts and harnesses that act on the composition; unmount() tears the
 * page down. A page bootstraps exactly once, into one container.
 */

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { StrictMode, useEffect, useState } from 'react'
import type { ReactElement, ReactNode } from 'react'
import { createRoot } from 'react-dom/client'
import type { Root } from 'react-dom/client'
import { createClient } from '@speed/api-client'
import type { ClientOptions, SpeedClient } from '@speed/api-client'
import { bindRequestFn } from '@speed/api-sdk/runtime'
import { attachSession, createAuthSession } from '@speed/auth-core'
import type { AuthSession } from '@speed/auth-core'
import { AUTH_UI_NAMESPACE, authUiResources } from '@speed/auth-ui'
import {
  createI18n,
  I18nextProvider,
  overlayResources,
  readSupportedLanguages,
  registerNamespace,
} from '@speed/i18n'
import type {
  CreateI18nOptions,
  I18nInstance,
  ResourceBundle,
} from '@speed/i18n'
import {
  PLATFORM_ERRORS_NAMESPACE,
  assertPlatformErrorBundleKeySet,
  platformErrorResources,
  registerPlatformErrors,
} from '@speed/i18n/platform-errors'
import { LAYOUT_KIT_NAMESPACE, layoutKitResources } from '@speed/layout-kit'
import {
  AppThemeProvider,
  UI_KIT_NAMESPACE,
  uiKitResources,
} from '@speed/ui-kit'
import type { AppThemeProviderProps } from '@speed/ui-kit'
import { PRODUCT_SHELL_NAMESPACE, productShellResources } from './resources.js'

/** One i18n namespace the app registers itself: a package family its
 * views compose (each sibling ships its own pair) or the app's own
 * bundle. The four namespaces of the packages the assembly composes
 * register automatically; listing one of them here replaces its shipped
 * bundle instead of registering it twice. */
export interface SpeedAppNamespace {
  readonly namespace: string
  readonly resources: Readonly<Record<string, ResourceBundle>>
}

/** Host resources merged into a namespace's base bundle before its one
 * registration, keyed by namespace and then by language. Every leaf an
 * overlay names must exist in the base bundle (a renamed or removed key
 * is an incompatible base), and a language the base does not carry is
 * taken whole; see overlayResources. A namespace key the assembly never
 * registers is refused at construction: the overlay would never be read,
 * so the host's copy would silently not apply. */
export type SpeedAppResourceOverrides = Readonly<
  Record<string, Readonly<Record<string, ResourceBundle>>>
>

/** The client options the definition may set. Everything else the
 * assembly passes to createClient itself: the base URL (the definition's
 * own `baseUrl`), the session's silent restore as the 401-refresh leg,
 * the i18n instance as the language source and the definition's
 * `tenantProvider` as the tenant source. */
export type SpeedAppClientOptions = Pick<
  ClientOptions,
  'reporter' | 'retryPolicy' | 'timeoutMs' | 'fetch' | 'tenantHeader'
> & {
  /**
   * The fetch credential mode, narrowed to the two cookie-carrying
   * values: the session cookie IS the credential, so `'omit'` would
   * silently end the session. Defaults to `'same-origin'` (the
   * same-origin deployment shape). A cross-origin *same-site*
   * deployment (api.example.com beside app.example.com) passes
   * `'include'` and the backend must accept credentials; a cross-site
   * deployment is not supported, because the session cookie refuses
   * `SameSite=None`.
   */
  readonly credentials?: 'same-origin' | 'include'
}

/** The theme options the definition may set, forwarded to the
 * AppThemeProvider the assembly renders. Tenant white-labelling is not
 * part of the definition: it is runtime host composition -- nest an
 * AppThemeProvider with `tenantOverrides` inside the application's
 * tenant provider (see the README). */
export type SpeedAppThemeOptions = Pick<
  AppThemeProviderProps,
  'projectTokens' | 'defaultMode' | 'presets' | 'defaultPreset'
>

/** The i18n options the definition may set, forwarded to createI18n. */
export type SpeedAppI18nOptions = Pick<
  CreateI18nOptions,
  | 'supportedLanguages'
  | 'defaultLanguage'
  | 'storageKey'
  | 'urlParameterName'
  | 'onMissingKey'
  | 'profileLanguage'
>

/** The options createSpeedI18n takes: everything createI18n takes, plus
 * the app's own namespaces and the host resource overlays. */
export type CreateSpeedI18nOptions = CreateI18nOptions & {
  readonly namespaces?: readonly SpeedAppNamespace[]
  readonly resourceOverrides?: SpeedAppResourceOverrides
}

/** The services the assembled composition passes to the definition's
 * providers. `api` is the client as a `SpeedClient` -- structurally the
 * `RequestFn` every generated operation reaches through the bound runtime
 * seam, plus the event-stream opener (`openEventStream`) a surface opens a
 * server-sent-event stream through. Both share the one set of seams (the
 * token store, the tenant and language providers, the refresh hook), so a
 * host reaches the stream opener here rather than wiring a second
 * transport. */
export interface SpeedAppServices {
  /** The attached session views drive sign-in, tenant switch and
   * sign-out through. */
  readonly session: AuthSession
  /** The client the assembly bound into the api-sdk runtime seam. */
  readonly api: SpeedClient
  /** The query client the provider stack serves. */
  readonly queryClient: QueryClient
}

/** An app-level provider wrapping the app's view inside the standard
 * stack (inside QueryClientProvider): a normal provider component's
 * body, given the assembled services. The definition's providers nest
 * outermost first. */
export type SpeedAppProvider = (
  services: SpeedAppServices,
  children: ReactNode,
) => ReactElement

/** The context of a session-end transition (an authenticated session
 * turning anonymous -- a sign-out or a session death, the same snapshot
 * flip). */
export interface SpeedSessionEndContext {
  readonly session: AuthSession
  readonly queryClient: QueryClient
  /** The shipped default action: removes every query. Total by design:
   * every row in the cache was fetched under the departing principal's
   * access token, and identity-domain rows live under bare spec-path
   * keys a tenant-scoped removal cannot reach -- a different account
   * signing in afterward must start from an empty cache. An override
   * that wants the default keeps this call in its own order. */
  readonly evictAllQueries: () => void
}

/** The session-end action: what happens the moment the session ends.
 * Absent from the definition, the shipped default (total query-cache
 * eviction) runs. */
export type SpeedSessionEndedHandler = (context: SpeedSessionEndContext) => void

/** The declarative definition of an app: everything that varies per
 * app, in one table. The machinery around it -- the session, the
 * client, the seam binding, the provider stack, the session-end
 * strategy -- is the assembly's. */
export interface SpeedAppDefinition {
  /** The app's own namespaces, beyond the family's automatic four (see
   * the header). Each registers exactly once, before render. Listing one
   * of the four framework namespaces (ui-kit, layout-kit, auth-ui,
   * product-shell's own) replaces that namespace's shipped bundle -- the
   * documented way to replace platform copy wholesale -- rather than
   * registering it twice. */
  readonly namespaces?: readonly SpeedAppNamespace[]
  /** Host resources merged into the base bundle of each namespace before
   * its single registration: the four framework namespaces, the
   * platform-errors namespace and every `namespaces` entry. The
   * registered language set is then reduced to the instance's supported
   * languages (a supported language the merged bundle lacks still
   * throws at registration). A namespace key outside that set throws
   * rather than being silently unread. */
  readonly resourceOverrides?: SpeedAppResourceOverrides
  /** App-level providers between the standard stack and the view,
   * outermost first. */
  readonly providers?: readonly SpeedAppProvider[]
  /** The app's view root: the shell composition (ProductShell with its
   * host slots) or any view the app renders instead. */
  readonly view: ReactNode
  /** Overrides the shipped session-end default (see
   * SpeedSessionEndedHandler). */
  readonly sessionEnded?: SpeedSessionEndedHandler
  /** The client's base URL. Defaults to the page's own origin, the
   * same-origin deployment shape. */
  readonly baseUrl?: string
  /** The client options merged into the createClient call (the credential
   * mode, a reporter, a retry policy, a timeout, a fetch implementation
   * and the tenant header's name). Absent, the shipped behaviour is
   * unchanged: `credentials: 'same-origin'` over the environment's fetch,
   * the client's own defaults for everything else. */
  readonly client?: SpeedAppClientOptions
  /** The theme options forwarded to the AppThemeProvider inside the
   * provider stack. Absent, the provider's defaults apply (light mode,
   * shipped tokens). */
  readonly theme?: SpeedAppThemeOptions
  /** The i18n options merged into the instance creation (the supported
   * languages, the negotiation fallback, the storage key, the URL
   * parameter name, the missing-key handler and the profile language). */
  readonly i18n?: SpeedAppI18nOptions
  /** The tenant every request announces as the tenant header (`X-Tenant`
   * unless `client.tenantHeader` renames it): the host returns the
   * tenant **alias** the backend's resolution layer looks up by name,
   * read per attempt so a request after a tenant switch carries the
   * tenant the user is now acting in. This is not the tenant id: the id
   * is what `@speed/auth-core`'s TenantProvider carries for query-key
   * scoping, while the transport announces the alias the resolution
   * layer maps to a tenant on each request (the tenant is not a claim of
   * the access token) -- so a host whose aliases differ from its ids
   * must answer with the alias here. Passed straight to the client's
   * `tenantProvider`; absent, or answering null/empty, no tenant header
   * is sent. */
  readonly tenantProvider?: () => string | null
  /** `false` disables the `<html lang>` binding: by default the assembly
   * sets the document's language from the i18n instance at boot and
   * keeps it in step with language changes, removing the subscription
   * when the page unmounts. */
  readonly documentLanguage?: false
}

/** What the assembly produced, for hosts and harnesses that act on the
 * composition. */
export interface SpeedAppBootstrap {
  /** The mounted root; unmount() tears the page down. */
  readonly root: Root
  /** The instance the tree renders with (language switching acts on
   * it). */
  readonly i18n: I18nInstance
  /** The query client the tree renders with, and the one the session-
   * end strategy evicts. */
  readonly queryClient: QueryClient
  /** The attached session, for hosts that drive its operations
   * directly. */
  readonly session: AuthSession
}

/**
 * The shipped session-end strategy. Watches the session for the one
 * transition that matters -- authenticated turning anonymous, whether
 * from a sign-out or a silently refused refresh (auth-core settles both
 * the same way) -- and on that edge runs the definition's handler, or,
 * absent one, the default: evictAllQueries(). Edges that only rotate
 * the principal (a tenant switch keeps state authenticated) fire
 * nothing; a session that was never authenticated fires nothing (the
 * first snapshot is the memory's baseline, not a transition). An
 * unauthenticated -> authenticated edge just moves the memory forward.
 *
 * The total eviction is not a coarse hammer. Two domains make it
 * necessary: the tenant-namespaced rows (['tenant', tenantId, ...])
 * keyed under the session's one tenant, and the identity-domain rows
 * (bare spec-path keys -- sessions, login history, bound identities --
 * nothing in @speed/api-sdk is tenant-namespaced), which carry no
 * tenant segment for a tenant-scoped removal to reach. Only a total
 * eviction stops a different account signing in afterward, into any
 * tenant, from inheriting the earlier account's rows out of the shared
 * query client's memory. (Mid-session tenant-switch eviction is the
 * host's own concern; this strategy deliberately does not fire there.)
 *
 * Returns the session's own unsubscribe function for a caller that
 * wants to tear this down; bootstrapSpeedApp does not hold onto it,
 * since the composed page never tears itself down before an unload a
 * fresh reload starts over from anyway.
 */
export function watchSessionEnd(
  session: AuthSession,
  queryClient: QueryClient,
  handler?: SpeedSessionEndedHandler,
): () => void {
  let previous = session.getSnapshot()
  return session.subscribe((snapshot) => {
    if (previous.state === 'authenticated' && snapshot.state === 'anonymous') {
      const context: SpeedSessionEndContext = {
        session,
        queryClient,
        evictAllQueries: () => {
          queryClient.removeQueries()
        },
      }
      if (handler !== undefined) {
        handler(context)
      } else {
        context.evictAllQueries()
      }
    }
    previous = snapshot
  })
}

/**
 * The query client's retry policy, deliberate: nothing retries at this
 * layer -- neither queries nor mutations -- because transient retries
 * belong to the transport, not to this layer:
 *
 *  - @speed/api-client's frozen retry policy already retries exactly
 *    the transient classes a repetition could redeem -- 429 (honouring
 *    Retry-After) plus 502/503/504, network failures and timeouts, on
 *    idempotent methods only -- inside a single request. By the time a
 *    queryFn call rejects, that budget is spent; a react-query retry
 *    would only re-run it whole (up to a twelve-attempt worst case
 *    across the two layers) and add exponential backoff on top.
 *  - Every answer that carries a code -- an envelope answer such as a
 *    403 rbac.permission_denied, or a client.* transport code -- is a
 *    definitive answer a repetition cannot redeem.
 *  - A mutation must never re-fire after a lost response: a create
 *    whose answer timed out server-side may already have committed, and
 *    a retry would duplicate it.
 *
 * Recovery is not lost: the read defaults are written out explicitly
 * rather than left to the library -- staleTime 0, with refetchOnMount,
 * refetchOnWindowFocus and refetchOnReconnect each true -- so a remount,
 * the tab becoming visible again and a reconnect each re-read under the
 * current token, and the transport's own retries cover momentary
 * outages.
 *
 * Exported because the policy is the assembly's, not a bootstrap
 * internal: a host or suite that renders the app's views under the
 * same client outside bootstrapSpeedApp (a view-level test harness,
 * a custom composition) needs the identical policy rather than a
 * reconstructed copy that could drift from this one.
 */
export function createQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: {
        retry: false,
        // Explicit: every read is always stale, and no automatic re-read
        // trigger is off. The library's own defaults currently agree, but
        // the assembly states the policy instead of inheriting it.
        staleTime: 0,
        refetchOnMount: true,
        refetchOnWindowFocus: true,
        refetchOnReconnect: true,
      },
      mutations: { retry: false },
    },
  })
}

/** The four namespaces of the packages the assembly itself composes:
 * registered automatically, or replaced wholesale when the definition
 * lists one of them. */
const FRAMEWORK_NAMESPACES: ReadonlyArray<{
  readonly namespace: string
  readonly resources: Readonly<Record<string, ResourceBundle>>
}> = [
  { namespace: UI_KIT_NAMESPACE, resources: uiKitResources },
  { namespace: LAYOUT_KIT_NAMESPACE, resources: layoutKitResources },
  { namespace: AUTH_UI_NAMESPACE, resources: authUiResources },
  { namespace: PRODUCT_SHELL_NAMESPACE, resources: productShellResources },
]

/** Refuse an overlay keyed by a namespace the assembly never registers:
 * its copy would never be read, so the host's text would silently not
 * apply. The same "name drift surfaces immediately" discipline
 * overlayResources applies to leaves. */
function assertKnownOverrideNamespaces(
  overrides: SpeedAppResourceOverrides,
  registered: ReadonlySet<string>,
): void {
  const unknown = Object.keys(overrides)
    .filter((namespace) => !registered.has(namespace))
    .sort()
  if (unknown.length === 0) {
    return
  }
  throw new Error(
    `[speed-product-shell] resourceOverrides names ${unknown
      .map((namespace) => `"${namespace}"`)
      .join(', ')}, which the assembly never registers. ` +
      'An overlay applies only to a registered namespace -- the four ' +
      'framework namespaces, the platform-errors namespace and every ' +
      'namespaces entry -- so this copy would never be read. Check the ' +
      'namespace name, or list the namespace in namespaces.',
  )
}

/** A namespace's base bundles with the host's overlay for it merged on
 * top, if the host supplied one. */
function resourcesWithOverrides(
  namespace: string,
  base: Readonly<Record<string, ResourceBundle>>,
  overrides: SpeedAppResourceOverrides,
): Readonly<Record<string, ResourceBundle>> {
  const overlay = overrides[namespace]
  return overlay === undefined ? base : overlayResources(base, overlay)
}

/** The bundles of the languages the instance supports. A base shipping a
 * language outside the set drops it; a supported language the result
 * lacks is left to registration, which refuses the namespace with the
 * missing-language message. */
function supportedLanguageBundles(
  resources: Readonly<Record<string, ResourceBundle>>,
  supported: readonly string[],
): Record<string, ResourceBundle> {
  const reduced: Record<string, ResourceBundle> = {}
  for (const language of supported) {
    const bundle = resources[language]
    if (bundle !== undefined) {
      reduced[language] = bundle
    }
  }
  return reduced
}

/**
 * Create the assembly's i18n instance: the createI18n instance (same
 * defaults, same negotiation chain), the four framework namespaces, the
 * platform error bundle named as the fallback namespace, the definition's
 * own namespaces, every namespace's host overlay merged in before its one
 * registration, and each namespace reduced to the instance's supported
 * languages.
 *
 * A definition namespace naming one of the four framework namespaces
 * replaces that namespace's shipped bundle (the documented way to replace
 * platform copy) instead of being registered twice; the platform error
 * bundle is host-modifiable through `resourceOverrides` only. An override
 * keyed by a namespace outside that registered set is refused, so a
 * misnamed namespace cannot leave the host's copy silently unread.
 * Exported so a view-level test bench builds the production instance
 * instead of a reconstructed copy.
 */
export function createSpeedI18n(
  options: CreateSpeedI18nOptions = {},
): I18nInstance {
  const { namespaces, resourceOverrides, ...createOptions } = options
  const overrides = resourceOverrides ?? {}
  assertKnownOverrideNamespaces(
    overrides,
    new Set([
      ...FRAMEWORK_NAMESPACES.map(({ namespace }) => namespace),
      PLATFORM_ERRORS_NAMESPACE,
      ...(namespaces ?? []).map(({ namespace }) => namespace),
    ]),
  )
  const i18n = createI18n({
    ...createOptions,
    fallbackNamespaces:
      createOptions.fallbackNamespaces ?? [PLATFORM_ERRORS_NAMESPACE],
  })
  const supported = readSupportedLanguages(i18n)
  const replacements = new Map<string, SpeedAppNamespace>()
  for (const entry of namespaces ?? []) {
    if (FRAMEWORK_NAMESPACES.some(({ namespace }) => namespace === entry.namespace)) {
      if (replacements.has(entry.namespace)) {
        // Every other duplicate is refused by registerNamespace; a
        // framework namespace is registered once here, so the duplicate
        // would otherwise silently ship the last bundle.
        throw new Error(
          `[speed-product-shell] the definition lists the framework ` +
            `namespace "${entry.namespace}" more than once; it is ` +
            'registered exactly once, so name it once with the bundle to ' +
            'register.',
        )
      }
      replacements.set(entry.namespace, entry)
    }
  }
  for (const framework of FRAMEWORK_NAMESPACES) {
    const base = replacements.get(framework.namespace)?.resources ?? framework.resources
    registerNamespace(
      i18n,
      framework.namespace,
      supportedLanguageBundles(
        resourcesWithOverrides(framework.namespace, base, overrides),
        supported,
      ),
    )
  }
  const platformOverrides = overrides[PLATFORM_ERRORS_NAMESPACE]
  if (platformOverrides === undefined) {
    registerPlatformErrors(i18n)
  } else {
    const platformBundles = supportedLanguageBundles(
      overlayResources(platformErrorResources, platformOverrides),
      supported,
    )
    // The registry path validates the packaged key set against the shipped
    // reference; an overlaid registration goes through registerNamespace
    // instead, so the same check runs here. Without it a host bundle
    // missing a code would register and every omitted code would render as
    // a raw key.
    for (const [language, bundle] of Object.entries(platformBundles)) {
      assertPlatformErrorBundleKeySet(language, bundle)
    }
    registerNamespace(i18n, PLATFORM_ERRORS_NAMESPACE, platformBundles)
  }
  for (const entry of namespaces ?? []) {
    if (replacements.has(entry.namespace)) {
      continue
    }
    registerNamespace(
      i18n,
      entry.namespace,
      supportedLanguageBundles(
        resourcesWithOverrides(entry.namespace, entry.resources, overrides),
        supported,
      ),
    )
  }
  return i18n
}

/** The props of SpeedAppProviders. */
export interface SpeedAppProvidersProps {
  /** The instance the tree renders with. */
  readonly i18n: I18nInstance
  /** The query client the tree serves. */
  readonly queryClient: QueryClient
  /** Theme options for the AppThemeProvider inside the stack. */
  readonly theme?: SpeedAppThemeOptions
  readonly children?: ReactNode
}

/**
 * The assembly's provider stack: StrictMode around I18nextProvider around
 * AppThemeProvider around QueryClientProvider. Exported so a host's
 * view-level test bench and production render under one construction
 * instead of each rebuilding it (a drift there would make a test pass on
 * a tree the app never renders).
 */
export function SpeedAppProviders({
  i18n,
  queryClient,
  theme,
  children,
}: SpeedAppProvidersProps): ReactElement {
  return (
    <StrictMode>
      <I18nextProvider i18n={i18n}>
        <AppThemeProvider
          i18n={i18n}
          projectTokens={theme?.projectTokens}
          defaultMode={theme?.defaultMode}
          presets={theme?.presets}
          defaultPreset={theme?.defaultPreset}
        >
          <QueryClientProvider client={queryClient}>
            {children}
          </QueryClientProvider>
        </AppThemeProvider>
      </I18nextProvider>
    </StrictMode>
  )
}

/** The boot restore's gate: the page renders the definition's view only
 * once the page load's one restore has settled.
 *
 * A reload starts from the anonymous snapshot -- the credential is the
 * server's httpOnly cookie, which script cannot read, so the browser
 * knows nothing until the read answers -- and rendering the view before
 * then paints a signed-in user's page load as a fresh visitor: the
 * sign-in surface shows for the length of the read and is then replaced
 * by the app. Holding the view keeps one state on screen for the whole
 * load, whatever the answer turns out to be.
 *
 * A failed restore (a transport or server error; the snapshot stays
 * anonymous) settles the gate too, so the page falls through to the
 * sign-in surface the session's recovery policy already describes
 * instead of waiting forever on a read that will never commit. */
function SessionRestoreGate({
  restore,
  children,
}: {
  readonly restore: Promise<void>
  readonly children: ReactNode
}): ReactElement | null {
  const [settled, setSettled] = useState(false)
  useEffect(() => {
    let active = true
    const settle = (): void => {
      if (active) {
        setSettled(true)
      }
    }
    // Both outcomes settle: a rejected restore leaves the anonymous
    // snapshot standing, which is a decided state, not a pending one.
    void restore.then(settle, settle)
    return () => {
      active = false
    }
  }, [restore])
  return settled ? <>{children}</> : null
}

/** The document a browser environment provides, or null where there is
 * none (a Node test host). */
function currentDocument(): Document | null {
  return typeof document === 'undefined' ? null : document
}

/** Declares the instance's language on `<html lang>` now, at boot, before
 * the first render. */
function applyDocumentLanguage(i18n: I18nInstance): void {
  const doc = currentDocument()
  if (doc !== null) {
    doc.documentElement.lang = i18n.language
  }
}

/** Keeps `<html lang>` in step with the instance for as long as the page
 * is mounted: the effect applies the current language and subscribes to
 * languageChanged, and the cleanup removes the subscription when the page
 * unmounts (the document's value is left as it is -- the page belongs to
 * the host). */
function DocumentLanguageBinding({
  i18n,
}: {
  readonly i18n: I18nInstance
}): null {
  useEffect(() => {
    const doc = currentDocument()
    if (doc === null) {
      return undefined
    }
    const apply = (): void => {
      doc.documentElement.lang = i18n.language
    }
    apply()
    i18n.on('languageChanged', apply)
    return () => {
      i18n.off('languageChanged', apply)
    }
  }, [i18n])
  return null
}

/**
 * Builds the whole app composition into the given container (the
 * header lists what is wired, in order). Calling it twice into one
 * container double-mounts; a page bootstraps exactly once.
 */
export function bootstrapSpeedApp(
  container: Element,
  definition: SpeedAppDefinition,
): SpeedAppBootstrap {
  const i18n = createSpeedI18n({
    ...(definition.i18n ?? {}),
    namespaces: definition.namespaces,
    resourceOverrides: definition.resourceOverrides,
  })

  const session = createAuthSession()
  attachSession(session)

  const clientOptions = definition.client ?? {}
  const client = createClient({
    baseUrl: definition.baseUrl ?? window.location.origin,
    ...clientOptions,
    // The session cookie is the credential: same-origin carries it on
    // every request, and declaring the mode also makes a refused
    // credentialed request engage the session hook below.
    credentials: clientOptions.credentials ?? 'same-origin',
    // The silent-401 leg: on a refused credentialed request, re-read the
    // session (GET /api/v1/auth/session). A cookie that still holds keeps the
    // session and the request is retried once; one that does not ends the
    // local session, so the shells fall back to sign-in. The client hands
    // the hook its round-scoped request function; restore runs both of
    // its reads through it, so the follow-up GET /api/v1/auth/me cannot join
    // the round that is awaiting this very call.
    refreshAccessToken: (issue) => session.restore(issue),
    // The frontend negotiation chain's resolved value, transported: the
    // provider reads the instance per attempt (createClient's own
    // contract), so a request retried after a language switch announces
    // the language the user is looking at, and a request the backend
    // serves as "requester is recipient" content (an SMS code, the type
    // directory) renders in it.
    languageProvider: () => i18n.language,
    // The host's current tenant alias, announced as the tenant header on
    // every request, read per attempt. Absent from the definition, no
    // tenant header is sent -- a host with no tenant-scoped traffic leaves
    // it out.
    tenantProvider: definition.tenantProvider,
  })
  bindRequestFn(client)

  // Restore the session the cookie names, once, at boot: a valid cookie
  // keeps the user signed in across a reload. The read is fire-and-forget
  // for the session itself -- a transport failure at startup leaves the
  // anonymous snapshot and the sign-in surface, which the next successful
  // request or reload corrects -- but its settlement gates the first
  // render (see SessionRestoreGate): before it answers, the page load's
  // state is unknown, not anonymous.
  const restored = session.restore().then(
    () => undefined,
    () => undefined,
  )

  const queryClient = createQueryClient()
  watchSessionEnd(session, queryClient, definition.sessionEnded)

  const services: SpeedAppServices = { session, api: client, queryClient }
  let view: ReactNode = definition.view
  for (const provide of [...(definition.providers ?? [])].reverse()) {
    view = provide(services, view)
  }

  const documentLanguage = definition.documentLanguage !== false
  if (documentLanguage) {
    applyDocumentLanguage(i18n)
  }

  const root = createRoot(container)
  root.render(
    <SpeedAppProviders
      i18n={i18n}
      queryClient={queryClient}
      theme={definition.theme}
    >
      {documentLanguage ? <DocumentLanguageBinding i18n={i18n} /> : null}
      <SessionRestoreGate restore={restored}>{view}</SessionRestoreGate>
    </SpeedAppProviders>,
  )
  return { root, i18n, queryClient, session }
}
