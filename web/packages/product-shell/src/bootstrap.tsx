/**
 * The app-entry assembly of the shell tier, exported as the
 * `@speed/product-shell/bootstrap` subpath.
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

/** One i18n namespace the app registers itself. Listing one of the four
 * framework namespaces replaces its shipped bundle instead of registering
 * it twice. */
export interface SpeedAppNamespace {
  /** The namespace name. */
  readonly namespace: string
  /** The namespace's resources, keyed by language. */
  readonly resources: Readonly<Record<string, ResourceBundle>>
}

/** Host resources merged into a namespace's base bundle before its one
 * registration, keyed by namespace then language. Every leaf must exist in
 * the base bundle; an overlay on an unregistered namespace throws. */
export type SpeedAppResourceOverrides = Readonly<
  Record<string, Readonly<Record<string, ResourceBundle>>>
>

/** The client options the definition may set. The base URL, the
 * 401-refresh leg, the language source and the tenant source are the
 * assembly's own. */
export type SpeedAppClientOptions = Pick<
  ClientOptions,
  'reporter' | 'retryPolicy' | 'timeoutMs' | 'fetch' | 'tenantHeader'
> & {
  /**
   * The fetch credential mode, narrowed to the two cookie-carrying
   * values; `'omit'` would end the session. Defaults to `'same-origin'`.
   */
  readonly credentials?: 'same-origin' | 'include'
}

/** The theme options forwarded to the AppThemeProvider the assembly
 * renders. */
export type SpeedAppThemeOptions = Pick<
  AppThemeProviderProps,
  'projectTokens' | 'defaultMode' | 'presets' | 'defaultPreset'
>

/** The i18n options merged into the createI18n call. */
export type SpeedAppI18nOptions = Pick<
  CreateI18nOptions,
  | 'supportedLanguages'
  | 'defaultLanguage'
  | 'storageKey'
  | 'urlParameterName'
  | 'onMissingKey'
  | 'profileLanguage'
>

/** The options createSpeedI18n takes: createI18n's options plus the app's
 * own namespaces and the host resource overlays. */
export type CreateSpeedI18nOptions = CreateI18nOptions & {
  readonly namespaces?: readonly SpeedAppNamespace[]
  readonly resourceOverrides?: SpeedAppResourceOverrides
}

/** The services the assembled composition passes to the definition's
 * providers. */
export interface SpeedAppServices {
  /** The attached session. */
  readonly session: AuthSession
  /** The client bound into the api-sdk runtime seam, including its
   * event-stream opener. */
  readonly api: SpeedClient
  /** The query client the provider stack serves. */
  readonly queryClient: QueryClient
}

/** An app-level provider wrapping the view inside the standard stack.
 * The definition's providers nest outermost first. */
export type SpeedAppProvider = (
  services: SpeedAppServices,
  children: ReactNode,
) => ReactElement

/** The context of a session-end transition (authenticated turning
 * anonymous). */
export interface SpeedSessionEndContext {
  /** The session that ended. */
  readonly session: AuthSession
  /** The query client the strategy evicts. */
  readonly queryClient: QueryClient
  /** Removes every cached query. */
  readonly evictAllQueries: () => void
}

/** The session-end action. Absent, the shipped default (total query-cache
 * eviction) runs. */
export type SpeedSessionEndedHandler = (context: SpeedSessionEndContext) => void

/** The declarative definition of an app: everything that varies per
 * app. */
export interface SpeedAppDefinition {
  /** The app's own namespaces, beyond the framework's four. Listing one
   * of the framework namespaces replaces its shipped bundle. */
  readonly namespaces?: readonly SpeedAppNamespace[]
  /** Host resources merged into each namespace's base bundle before its
   * one registration. */
  readonly resourceOverrides?: SpeedAppResourceOverrides
  /** App-level providers between the standard stack and the view,
   * outermost first. */
  readonly providers?: readonly SpeedAppProvider[]
  /** The app's view root: the shell composition or any view the app
   * renders instead; mounted once the boot restore settles. */
  readonly view: ReactNode
  /** Overrides the shipped session-end default. */
  readonly sessionEnded?: SpeedSessionEndedHandler
  /** The client's base URL. Defaults to the page's own origin. */
  readonly baseUrl?: string
  /** The client options merged into the createClient call. */
  readonly client?: SpeedAppClientOptions
  /** The theme options forwarded to the AppThemeProvider. */
  readonly theme?: SpeedAppThemeOptions
  /** The i18n options merged into the instance creation. */
  readonly i18n?: SpeedAppI18nOptions
  /** Returns the current tenant name, sent as the tenant header (read per
   * attempt). Not the tenant id. Absent or null sends no header. */
  readonly tenantProvider?: () => string | null
  /** `false` disables the `<html lang>` binding. */
  readonly documentLanguage?: false
}

/** The handles the assembly produced. */
export interface SpeedAppBootstrap {
  /** The mounted root; `unmount()` tears the page down. */
  readonly root: Root
  /** The i18n instance the tree renders with. */
  readonly i18n: I18nInstance
  /** The query client the tree renders with. */
  readonly queryClient: QueryClient
  /** The attached session. */
  readonly session: AuthSession
}

/**
 * Runs the definition's handler, or the default total cache eviction, on
 * the authenticated-to-anonymous transition. Returns an unsubscribe
 * function.
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
 * Returns a QueryClient with queries and mutations both non-retrying.
 * A mutation must never re-fire after a lost response.
 */
export function createQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: {
        retry: false,
        staleTime: 0,
        refetchOnMount: true,
        refetchOnWindowFocus: true,
        refetchOnReconnect: true,
      },
      mutations: { retry: false },
    },
  })
}

const FRAMEWORK_NAMESPACES: ReadonlyArray<{
  readonly namespace: string
  readonly resources: Readonly<Record<string, ResourceBundle>>
}> = [
  { namespace: UI_KIT_NAMESPACE, resources: uiKitResources },
  { namespace: LAYOUT_KIT_NAMESPACE, resources: layoutKitResources },
  { namespace: AUTH_UI_NAMESPACE, resources: authUiResources },
  { namespace: PRODUCT_SHELL_NAMESPACE, resources: productShellResources },
]

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

function resourcesWithOverrides(
  namespace: string,
  base: Readonly<Record<string, ResourceBundle>>,
  overrides: SpeedAppResourceOverrides,
): Readonly<Record<string, ResourceBundle>> {
  const overlay = overrides[namespace]
  return overlay === undefined ? base : overlayResources(base, overlay)
}

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
 * Creates the assembly's i18n instance: the framework's four namespaces,
 * the platform error bundle as fallback, the definition's namespaces, and
 * the host overlays merged before each single registration.
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

/** Props for the standard provider stack: the i18n instance and query
 * client it mounts, optional theme options, and the subtree it wraps. */
export interface SpeedAppProvidersProps {
  /** The instance the tree renders with. */
  readonly i18n: I18nInstance
  /** The query client the tree serves. */
  readonly queryClient: QueryClient
  /** Theme options for the AppThemeProvider inside the stack. */
  readonly theme?: SpeedAppThemeOptions
  /** The subtree the stack wraps. */
  readonly children?: ReactNode
}

/**
 * Renders StrictMode around I18nextProvider, AppThemeProvider and
 * QueryClientProvider.
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
    void restore.then(settle, settle)
    return () => {
      active = false
    }
  }, [restore])
  return settled ? <>{children}</> : null
}

function currentDocument(): Document | null {
  return typeof document === 'undefined' ? null : document
}

function applyDocumentLanguage(i18n: I18nInstance): void {
  const doc = currentDocument()
  if (doc !== null) {
    doc.documentElement.lang = i18n.language
  }
}

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
 * Builds the whole app composition into the given container. A page
 * bootstraps exactly once.
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
    credentials: clientOptions.credentials ?? 'same-origin',
    refreshAccessToken: (issue) => session.restore(issue),
    languageProvider: () => i18n.language,
    tenantProvider: definition.tenantProvider,
  })
  bindRequestFn(client)

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
