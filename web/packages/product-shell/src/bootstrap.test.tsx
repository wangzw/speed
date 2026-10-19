/**
 * bootstrap.test.tsx -- the contracts of the app-entry assembly, each
 * proven at the layer it belongs to:
 *
 * bootstrapSpeedApp -- jsdom mounts of the whole composition: the
 * definition's namespaces register alongside the family's automatic
 * four, a framework namespace listed in the definition replaces its
 * shipped bundle, resource overlays merge before registration (a leaf, a
 * replacement and a whole added language), every definition option
 * reaches what it configures (client, theme, i18n), the `<html lang>`
 * binding follows the instance and its opt-out, the provider chain wraps
 * the declared view in declared order with the assembled services, the
 * client the assembly built answers over the environment's own (stubbed)
 * fetch, the session-end strategy is wired (default and override), and
 * unmount tears the page down.
 *
 * createSpeedI18n -- the exported instance construction: the family four
 * and the platform error bundle registered, the definition namespaces and
 * overlays applied.
 *
 * SpeedAppProviders -- the exported provider stack serving translations,
 * theme and the supplied query client.
 *
 * createQueryClient -- the exported query policy: the read defaults are
 * written out on the client rather than inherited from the library.
 *
 * watchSessionEnd -- the session-ended default's transition matrix over
 * a real AuthSession (createAuthSession driven through the real-client
 * rig's scripted responder), a real QueryClient and no DOM tree: the
 * wiring is pure session-and-cache plumbing. The matrix pins the four
 * edges that matter: sign-out evicts, a silently refused refresh
 * (session death -- refresh() resolves false, the same authenticated ->
 * anonymous flip) evicts, a session that was never authenticated
 * evicts nothing, and a tenant switch (still authenticated throughout)
 * evicts nothing -- mid-session switch eviction is the host's own
 * concern.
 */

import { QueryClient, useQueryClient } from '@tanstack/react-query'
import { useTheme } from '@mui/material/styles'
import { act, render, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { ReactElement } from 'react'
import { isApiError } from '@speed/api-client'
import { AUTH_UI_NAMESPACE, authUiResources } from '@speed/auth-ui'
import {
  DEFAULT_SUPPORTED_LANGUAGES,
  readSupportedLanguages,
  switchLanguage,
  useTranslation,
} from '@speed/i18n'
import type { ResourceBundle } from '@speed/i18n'
import {
  PLATFORM_ERRORS_NAMESPACE,
  platformErrorResources,
} from '@speed/i18n/platform-errors'
import {
  LAYOUT_KIT_NAMESPACE,
  layoutKitResources,
} from '@speed/layout-kit'
import {
  createAppTheme,
  UI_KIT_NAMESPACE,
  uiKitResources,
} from '@speed/ui-kit'
import {
  PRODUCT_SHELL_NAMESPACE,
  productShellResources,
} from './resources.js'
import zhCN from './locales/zh-CN.json' with { type: 'json' }
import {
  bootstrapSpeedApp,
  createQueryClient,
  createSpeedI18n,
  SpeedAppProviders,
  watchSessionEnd,
} from './bootstrap.js'
import type { SpeedAppBootstrap, SpeedAppProvider } from './bootstrap.js'
import type { SpeedAppDefinition, SpeedAppServices } from './bootstrap.js'
import {
  authenticatedCompletion,
  errorResponse,
  jsonResponse,
  makeRealClientRig,
  openFlow,
  principal,
  tokenResponse,
} from '../test-utils/real-client.js'
import type { RealResponder } from '../test-utils/real-client.js'
import type { AuthSession } from '@speed/auth-core'

/** The tenant the scripted happy-path answers name. */
const TENANT_ID = 'tenant-1'

/** A scripted answer table covering the flows both describes drive: a
 * password login (the flow start, its answer, the profile the completion
 * reads), a silent refresh's token endpoint, and logout. */
const respond: RealResponder = (call) => {
  const key = `${call.method} ${call.path}`
  if (key === 'POST /api/v1/auth/flows') {
    return jsonResponse(200, openFlow())
  }
  if (key === 'POST /api/v1/auth/flows/answer') {
    return jsonResponse(200, authenticatedCompletion())
  }
  if (key === 'GET /api/v1/auth/me') {
    return jsonResponse(200, principal())
  }
  if (key === 'GET /api/v1/auth/session') {
    // The boot restore's read. Nobody is signed in by default, so the
    // mounted view stays on the sign-in surface; a test that needs a
    // remembered session overrides this responder.
    return jsonResponse(200, { authenticated: false })
  }
  if (key === 'GET /api/v1/features') {
    // The boot restore's feature read, and every login commit's.
    return jsonResponse(200, { features: [] })
  }
  if (key === 'POST /api/v1/auth/token') {
    return jsonResponse(200, tokenResponse())
  }
  if (key === 'POST /api/v1/auth/logout') {
    return new Response(null, { status: 204 })
  }
  throw new Error(`unexpected request: ${key}`)
}

/** Drive a password login to completion on a session: the flow it starts,
 * the first answer, and the profile the completion reads. */
async function signIn(session: AuthSession): Promise<void> {
  const first = await session.startFlow({ method: 'password' })
  if (!('next' in first)) {
    return
  }
  await session.answerFlow({
    seq: first.seq,
    step: 'answer',
    input: {
      identifier: 'owner@example.test',
      password: 'correct-horse-battery-staple',
    },
  })
}

/** A query row under the app-side tenant-namespaced key convention
 * (['tenant', tenantId, ...]); the suffix is arbitrary -- the eviction
 * removes everything. */
function seedTenantCache(queryClient: QueryClient): void {
  queryClient.setQueryData(
    ['tenant', TENANT_ID, '/notes', {}],
    { notes: [{ id: 'note-1', text: 'cached from the departing session' }] },
  )
}

/** The identity-domain shape: bare spec-path keys nothing
 * tenant-namespaced, which only a total eviction reaches. */
function seedIdentityCache(queryClient: QueryClient): void {
  queryClient.setQueryData(['/api/v1/auth/sessions'], {
    sessions: [{ id: 'session-1' }],
  })
}

/** A row no eviction call site names -- the "remove everything" proof
 * that no future domain needs a new eviction site either. */
function seedUnrelatedCache(queryClient: QueryClient): void {
  queryClient.setQueryData(['preferences'], { theme: 'dark' })
}

/** Seeds every domain an authenticated session's reads populate. */
function seedAllDomains(queryClient: QueryClient): void {
  seedTenantCache(queryClient)
  seedIdentityCache(queryClient)
  seedUnrelatedCache(queryClient)
}

/** Mounts a definition into a fresh container and returns the definite
 * handles (the mount runs inside act; the guard keeps the callers'
 * types definite). */
async function mountApp(
  container: Element,
  definition: SpeedAppDefinition,
): Promise<SpeedAppBootstrap> {
  let boot: SpeedAppBootstrap | undefined
  await act(async () => {
    boot = bootstrapSpeedApp(container, definition)
  })
  if (boot === undefined) {
    throw new Error('the bootstrap must return its handles')
  }
  return boot
}

/** A copy of a bundle with every string leaf prefixed: a complete bundle
 * for a language a package does not ship, with the shipped key set (the
 * shape registration validates). */
function prefixedBundle(bundle: ResourceBundle, prefix: string): ResourceBundle {
  const copy: Record<string, string | ResourceBundle> = {}
  for (const [key, value] of Object.entries(bundle)) {
    copy[key] =
      typeof value === 'string' ? `${prefix}${value}` : prefixedBundle(value, prefix)
  }
  return copy
}

/** Unmount a mounted app and drop its container. */
async function teardown(boot: SpeedAppBootstrap, container: Element): Promise<void> {
  await act(async () => {
    boot.root.unmount()
  })
  container.remove()
}

describe('bootstrapSpeedApp', () => {
  let observed: Array<{
    readonly method: string
    readonly path: string
    readonly origin: string
    readonly acceptLanguage: string | null
    readonly tenant: string | null
    readonly credentials: RequestCredentials | null
    readonly headers: Headers
  }>
  let realFetch: typeof globalThis.fetch
  let realLanguages: readonly string[] | undefined
  let realLocalStorage: PropertyDescriptor | undefined
  let realHref: string
  /** The responder the mounted client's fetch consults; tests override
   * it to script a remembered session or another boot answer. */
  let activeResponder: RealResponder

  beforeEach(() => {
    observed = []
    activeResponder = respond
    realFetch = globalThis.fetch
    realLanguages = window.navigator.languages
    realHref = window.location.href
    // Deterministic language: pin the navigator-languages leg of
    // createI18n's negotiation so the mounted view speaks the zh-CN
    // copy the assertions name (jsdom's own languages answer en-US).
    Object.defineProperty(window.navigator, 'languages', {
      value: ['zh-CN'],
      configurable: true,
    })
    // createI18n reads the stored-language choice through a guarded
    // globalThis.localStorage access; under Node 26 that global is the
    // engine's experimental webstorage getter (it warns and offers
    // nothing without --localstorage-file). A memory-backed stub keeps
    // that leg exercised deterministically (an empty store reads back
    // null) without tripping the engine global.
    realLocalStorage = Object.getOwnPropertyDescriptor(
      globalThis,
      'localStorage',
    )
    Object.defineProperty(globalThis, 'localStorage', {
      value: {
        getItem: () => null,
        setItem: () => undefined,
      },
      configurable: true,
      writable: true,
    })
    // The environment fetch the assembly's client captures (no fetch
    // option: createClient captures globalThis.fetch at construction).
    // A fresh stub per test answers the scripted flows and fails loudly
    // on anything else -- an unexpected request is a composition
    // regression, not something to answer.
    Object.defineProperty(globalThis, 'fetch', {
      value: async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = new URL(String(input))
        const headers = new Headers(init?.headers)
        observed.push({
          method: init?.method ?? 'GET',
          path: url.pathname,
          origin: url.origin,
          acceptLanguage: headers.get('accept-language'),
          tenant: headers.get('x-tenant'),
          credentials: init?.credentials ?? null,
          headers,
        })
        return activeResponder({
          method: init?.method ?? 'GET',
          path: url.pathname,
          authorization: headers.get('authorization'),
          body: typeof init?.body === 'string' ? init.body : null,
        })
      },
      configurable: true,
    })
  })

  afterEach(() => {
    if (realLanguages !== undefined) {
      Object.defineProperty(window.navigator, 'languages', {
        value: realLanguages,
        configurable: true,
      })
    }
    window.history.replaceState({}, '', realHref)
    Object.defineProperty(globalThis, 'fetch', {
      value: realFetch,
      configurable: true,
    })
    if (realLocalStorage !== undefined) {
      Object.defineProperty(globalThis, 'localStorage', realLocalStorage)
    }
    document.documentElement.lang = ''
    vi.restoreAllMocks()
    document.body.innerHTML = ''
  })

  it('mounts the declared view under the standard stack, with the family and declared namespaces registered', async () => {
    const container = document.createElement('div')
    document.body.appendChild(container)
    function Probe(): ReactElement {
      const { t } = useTranslation(PRODUCT_SHELL_NAMESPACE)
      return <span data-testid="probe">{t('announcements.sessionEnded')}</span>
    }
    const boot = await mountApp(container, { view: <Probe /> })

    // The assembly's own four namespaces registered automatically --
    // the namespaces the packages it composes render under -- and the
    // view reads them.
    expect(boot.i18n.language).toBe('zh-CN')
    expect(boot.i18n.hasResourceBundle('zh-CN', UI_KIT_NAMESPACE)).toBe(true)
    expect(boot.i18n.hasResourceBundle('zh-CN', LAYOUT_KIT_NAMESPACE)).toBe(true)
    expect(boot.i18n.hasResourceBundle('zh-CN', AUTH_UI_NAMESPACE)).toBe(true)
    expect(
      boot.i18n.hasResourceBundle('zh-CN', PRODUCT_SHELL_NAMESPACE),
    ).toBe(true)
    expect(boot.queryClient).toBeInstanceOf(QueryClient)
    expect(container.textContent).toContain(zhCN.announcements.sessionEnded)

    await act(async () => {
      boot.root.unmount()
    })
    container.remove()
    expect(container.innerHTML).toBe('')
  })

  it('restores the session the cookie names at boot, announcing the cookie credential mode', async () => {
    const container = document.createElement('div')
    document.body.appendChild(container)
    // A page load with a still-valid session cookie: the boot read says
    // signed in, and the principal is re-read from /api/v1/auth/me.
    activeResponder = (call) => {
      if (call.path === '/api/v1/auth/session') {
        return jsonResponse(200, { authenticated: true, session_id: 's-1' })
      }
      return respond(call)
    }
    const boot = await mountApp(container, { view: <div /> })

    // The reload kept the user logged in -- the bug this model fixes.
    // The boot restore commits the principal and the feature document
    // together, so the assertion waits for that commit rather than for
    // the boot call itself (which returns before the read settles).
    await waitFor(() =>
      expect(boot.session.getSnapshot().state).toBe('authenticated'),
    )
    expect(boot.session.getSnapshot().principal).not.toBeNull()

    // The boot read and its /me follow both rode the cookie mode.
    const sessionRead = observed.find((call) => call.path === '/api/v1/auth/session')
    expect(sessionRead?.credentials).toBe('same-origin')
    const meRead = observed.find((call) => call.path === '/api/v1/auth/me')
    expect(meRead?.credentials).toBe('same-origin')

    await act(async () => {
      boot.root.unmount()
    })
    container.remove()
  })

  it('announces the same-origin credential mode on every request and sends no Authorization', async () => {
    const container = document.createElement('div')
    document.body.appendChild(container)
    const boot = await mountApp(container, { view: <div /> })
    await act(async () => {
      await boot.session.startFlow({ method: 'password' })
    })
    // Every observed request carried the credential mode; none carried a
    // bearer token (the cookie is the credential).
    for (const call of observed) {
      expect(call.credentials).toBe('same-origin')
    }
    expect(observed.some((call) => call.path === '/api/v1/auth/flows')).toBe(true)
    await act(async () => {
      boot.root.unmount()
    })
    container.remove()
  })

  it('settles a refused request even when the silent restore it triggers has its profile read refused', { timeout: 5000 }, async () => {
    // The wiring under test is the production one: the client's
    // refreshAccessToken hook is the session's own restore, reading
    // GET /api/v1/auth/session and then GET /api/v1/auth/me. The first read runs
    // inside the hook's synchronous invocation; the profile read
    // follows an await, so only the round-scoped request function the
    // hook is handed keeps it from joining the round that is awaiting
    // the hook -- a request awaiting itself never settles.
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const container = document.createElement('div')
    document.body.appendChild(container)
    let sessionReads = 0
    let profileReads = 0
    activeResponder = (call) => {
      if (call.method === 'GET' && call.path === '/api/v1/auth/session') {
        sessionReads += 1
        // The boot restore finds nobody signed in; the silent restore
        // that follows the refused request finds a still-valid cookie.
        return jsonResponse(
          200,
          sessionReads === 1
            ? { authenticated: false }
            : { authenticated: true, session_id: 's-1' },
        )
      }
      if (call.method === 'GET' && call.path === '/api/v1/auth/me') {
        profileReads += 1
        return errorResponse(401, 'authn.session_expired', 'trace-me')
      }
      if (call.method === 'GET' && call.path === '/notes') {
        return errorResponse(401, 'authn.session_expired', 'trace-notes')
      }
      return respond(call)
    }

    let api: SpeedAppServices['api'] | undefined
    const capture: SpeedAppProvider = (services, children) => {
      api = services.api
      return <>{children}</>
    }
    const boot = await mountApp(container, {
      view: <div />,
      providers: [capture],
    })
    expect(boot.session.getSnapshot().state).toBe('anonymous')
    const drive = api
    if (drive === undefined) {
      throw new Error('the provider must receive the assembled client')
    }

    // One credentialed request into the refused path.
    let settled = false
    let caught: unknown
    const driven = drive<unknown>('/notes').then(
      () => {
        settled = true
      },
      (error: unknown) => {
        settled = true
        caught = error
      },
    )
    // The never-settling regression is deterministic: without the bound
    // the profile read joins the round awaiting it. The bounded wait
    // turns that hang into an assertion failure.
    for (let i = 0; i < 64 && !settled; i += 1) {
      await Promise.resolve()
    }
    const deadline = Date.now() + 2000
    while (!settled && Date.now() < deadline) {
      await new Promise((resolve) => setTimeout(resolve, 2))
    }
    expect(settled).toBe(true)
    expect(isApiError(caught)).toBe(true)
    if (isApiError(caught)) {
      expect(caught.auth).toBe(true)
      expect(caught.code).toBe('authn.session_expired')
      expect(caught.traceId).toBe('trace-notes')
    }
    // Each restore read happened exactly once: the round ended as a
    // finite failed refresh instead of re-reading or retrying, and the
    // client reported that failure through its own diagnostic channel.
    expect(sessionReads).toBe(2)
    expect(profileReads).toBe(1)
    expect(warn).toHaveBeenCalledTimes(1)
    await driven

    await act(async () => {
      boot.root.unmount()
    })
    container.remove()
  })

  it('registers the platform error bundle as the fallback namespace', async () => {
    const container = document.createElement('div')
    document.body.appendChild(container)
    const boot = await mountApp(container, { view: <div /> })

    // A backend error code no package namespace covers resolves to the
    // platform bundle's own text -- the same string a direct lookup of
    // the bundle returns -- instead of rendering as the raw key.
    const code = 'admin.impersonation_reason_required'
    expect(boot.i18n.hasResourceBundle('zh-CN', PLATFORM_ERRORS_NAMESPACE)).toBe(
      true,
    )
    const throughFallback = boot.i18n.t(`ui-kit:errors.${code}`)
    expect(throughFallback).not.toBe(`errors.${code}`)
    expect(throughFallback).toBe(
      boot.i18n.t(`platform-errors:errors.${code}`),
    )

    await act(async () => {
      boot.root.unmount()
    })
    container.remove()
  })

  it('announces the i18n instance language on every request, and a switch moves it', async () => {
    const container = document.createElement('div')
    document.body.appendChild(container)
    const boot = await mountApp(container, { view: <div /> })

    // The navigator leg resolved zh-CN at creation (pinned above), so
    // the assembled client announces it -- the frontend chain's value,
    // transported for the backend's requester-language tiers. A real
    // flow drives the requests so the whole session wiring is on the
    // path.
    await act(async () => {
      await signIn(boot.session)
    })
    expect(observed.length).toBeGreaterThan(0)
    expect(observed.every((call) => call.acceptLanguage === 'zh-CN')).toBe(true)

    // A language switch moves what subsequent requests announce: the
    // provider reads the instance per attempt, so nothing goes stale.
    const beforeSwitch = observed.length
    await act(async () => {
      await switchLanguage(boot.i18n, 'en-US')
      await signIn(boot.session)
    })
    const afterSwitch = observed.slice(beforeSwitch)
    expect(afterSwitch.length).toBeGreaterThan(0)
    expect(afterSwitch.every((call) => call.acceptLanguage === 'en-US')).toBe(
      true,
    )

    await act(async () => {
      boot.root.unmount()
    })
    container.remove()
  })

  it('sends the definition tenantProvider value as X-Tenant, read per request, and no header without one', async () => {
    const container = document.createElement('div')
    document.body.appendChild(container)
    let tenant: string | null = 'acme'
    const boot = await mountApp(container, {
      view: <div />,
      tenantProvider: () => tenant,
    })

    // Every request the session wiring drives announces the host's current
    // tenant -- the routing fact the backend's tenant resolution layer reads
    // on the api endpoint.
    await act(async () => {
      await signIn(boot.session)
    })
    expect(observed.length).toBeGreaterThan(0)
    expect(observed.every((call) => call.tenant === 'acme')).toBe(true)

    // The provider is read per request, so a switch moves what subsequent
    // requests announce rather than going stale.
    const beforeSwitch = observed.length
    tenant = 'globex'
    await act(async () => {
      await signIn(boot.session)
    })
    const afterSwitch = observed.slice(beforeSwitch)
    expect(afterSwitch.length).toBeGreaterThan(0)
    expect(afterSwitch.every((call) => call.tenant === 'globex')).toBe(true)

    await act(async () => {
      boot.root.unmount()
    })
    container.remove()

    // A definition with no tenantProvider announces no tenant at all.
    observed = []
    const bare = document.createElement('div')
    document.body.appendChild(bare)
    const bareBoot = await mountApp(bare, { view: <div /> })
    await act(async () => {
      await signIn(bareBoot.session)
    })
    expect(observed.length).toBeGreaterThan(0)
    expect(observed.every((call) => call.tenant === null)).toBe(true)
    await act(async () => {
      bareBoot.root.unmount()
    })
    bare.remove()
  })

  it('registers the declared namespaces on top of the family four, and threads the assembled services through the providers in declared order', async () => {
    const container = document.createElement('div')
    document.body.appendChild(container)
    const probeNamespace = 'probe-app'
    const probeResources = {
      'zh-CN': { greeting: 'probe' },
      'en-US': { greeting: 'probe' },
    }
    const seen: Array<{ readonly api: unknown }> = []
    const outer: SpeedAppProvider = (services, children) => {
      seen.push({ api: services.api })
      return <div data-testid="outer">{children}</div>
    }
    const inner: SpeedAppProvider = (_services, children) => (
      <div data-testid="inner">{children}</div>
    )
    const boot = await mountApp(container, {
      namespaces: [{ namespace: probeNamespace, resources: probeResources }],
      providers: [outer, inner],
      view: <span data-testid="view">app</span>,
    })

    expect(boot.i18n.hasResourceBundle('zh-CN', probeNamespace)).toBe(true)
    // The provider fold, outermost first: outer wraps inner wraps the
    // view -- the declared order is the DOM nesting order.
    const outerNode = container.querySelector('[data-testid="outer"]')
    const innerNode = container.querySelector('[data-testid="inner"]')
    const viewNode = container.querySelector('[data-testid="view"]')
    expect(outerNode).not.toBeNull()
    expect(innerNode).not.toBeNull()
    expect(viewNode).not.toBeNull()
    expect(outerNode?.contains(innerNode)).toBe(true)
    expect(innerNode?.contains(viewNode)).toBe(true)
    // The providers receive the assembled services, and the api is the
    // client bound into the runtime seam: the session operations
    // travel over the environment fetch stub.
    expect(seen).toHaveLength(1)
    expect(typeof seen[0]?.api).toBe('function')
    await act(async () => {
      await signIn(boot.session)
    })
    expect(observed.some((call) => call.path === '/api/v1/auth/flows')).toBe(true)

    await act(async () => {
      boot.root.unmount()
    })
    container.remove()
  })

  it("honours a declared baseUrl for the assembled client", async () => {
    const container = document.createElement('div')
    document.body.appendChild(container)
    const boot = await mountApp(container, {
      baseUrl: 'https://api.example.test',
      view: <span>app</span>,
    })
    await act(async () => {
      await signIn(boot.session)
    })
    expect(observed[0]?.origin).toBe('https://api.example.test')

    await act(async () => {
      boot.root.unmount()
    })
    container.remove()
  })

  it('accepts the include credential mode from the definition client table', async () => {
    const container = document.createElement('div')
    document.body.appendChild(container)
    const boot = await mountApp(container, {
      view: <div />,
      client: { credentials: 'include' },
    })
    await act(async () => {
      await signIn(boot.session)
    })
    expect(observed.length).toBeGreaterThan(0)
    expect(observed.every((call) => call.credentials === 'include')).toBe(true)
    await teardown(boot, container)
  })

  it('uses the fetch implementation from the definition client table', async () => {
    const container = document.createElement('div')
    document.body.appendChild(container)
    let delegated = 0
    const customFetch: typeof globalThis.fetch = (input, init) => {
      delegated += 1
      return globalThis.fetch(input, init)
    }
    const boot = await mountApp(container, {
      view: <div />,
      client: { fetch: customFetch },
    })
    await act(async () => {
      await signIn(boot.session)
    })
    // Every request the assembly's client issued went through the
    // supplied implementation -- and reached the endpoint script.
    expect(delegated).toBeGreaterThan(0)
    expect(delegated).toBe(observed.length)
    expect(observed.some((call) => call.path === '/api/v1/auth/flows')).toBe(true)
    await teardown(boot, container)
  })

  it('announces the tenant under the definition client tenantHeader instead of x-tenant', async () => {
    const container = document.createElement('div')
    document.body.appendChild(container)
    const boot = await mountApp(container, {
      view: <div />,
      tenantProvider: () => 'acme',
      client: { tenantHeader: 'x-organization' },
    })
    await act(async () => {
      await signIn(boot.session)
    })
    expect(observed.length).toBeGreaterThan(0)
    expect(
      observed.every((call) => call.headers.get('x-organization') === 'acme'),
    ).toBe(true)
    // The default name is replaced, not duplicated.
    expect(observed.every((call) => call.headers.get('x-tenant') === null)).toBe(true)
    await teardown(boot, container)
  })

  it('applies timeoutMs from the definition client table', async () => {
    const container = document.createElement('div')
    document.body.appendChild(container)
    // A fetch that never answers on its own but honours the abort signal,
    // the way a real fetch does: only the client's own timeout can end
    // the exchange.
    const recordingFetch = globalThis.fetch
    Object.defineProperty(globalThis, 'fetch', {
      value: (input: RequestInfo | URL, init?: RequestInit) => {
        const url = new URL(String(input))
        if (url.pathname === '/notes') {
          return new Promise<Response>((_resolve, reject) => {
            init?.signal?.addEventListener('abort', () => {
              reject(new DOMException('The operation was aborted.', 'AbortError'))
            })
          })
        }
        return recordingFetch(input, init)
      },
      configurable: true,
    })
    let api: SpeedAppServices['api'] | undefined
    const capture: SpeedAppProvider = (services, children) => {
      api = services.api
      return <>{children}</>
    }
    const boot = await mountApp(container, {
      view: <div />,
      providers: [capture],
      client: { timeoutMs: 20 },
    })
    const drive = api
    if (drive === undefined) {
      throw new Error('the provider must receive the assembled client')
    }
    const caught = await drive<unknown>('/notes').then(
      () => undefined,
      (error: unknown) => error,
    )
    expect(isApiError(caught)).toBe(true)
    if (isApiError(caught)) {
      expect(caught.code).toBe('client.timeout')
    }
    await teardown(boot, container)
  })

  it('applies retryPolicy from the definition client table, and keeps the shipped policy when none is given', async () => {
    activeResponder = (call) => {
      if (call.method === 'GET' && call.path === '/notes') {
        return new Response(null, { status: 503 })
      }
      return respond(call)
    }
    const notesAttempts = async (definition: SpeedAppDefinition): Promise<number> => {
      const container = document.createElement('div')
      document.body.appendChild(container)
      let api: SpeedAppServices['api'] | undefined
      const capture: SpeedAppProvider = (services, children) => {
        api = services.api
        return <>{children}</>
      }
      const boot = await mountApp(container, { ...definition, providers: [capture] })
      const drive = api
      if (drive === undefined) {
        throw new Error('the provider must receive the assembled client')
      }
      const before = observed.filter((call) => call.path === '/notes').length
      await drive<unknown>('/notes').catch(() => undefined)
      const attempts =
        observed.filter((call) => call.path === '/notes').length - before
      await teardown(boot, container)
      return attempts
    }

    expect(
      await notesAttempts({
        view: <div />,
        client: {
          retryPolicy: { maxAttempts: 1, initialDelayMs: 1, maxDelayMs: 1 },
        },
      }),
    ).toBe(1)
    // No policy declared: the shipped default (three transient attempts)
    // is unchanged.
    expect(await notesAttempts({ view: <div /> })).toBe(3)
  })

  it('routes client diagnostics through the reporter from the definition client table', async () => {
    const consoleWarn = vi.spyOn(console, 'warn').mockImplementation(() => undefined)
    const reporter = { error: vi.fn(), warn: vi.fn() }
    const container = document.createElement('div')
    document.body.appendChild(container)
    let sessionReads = 0
    activeResponder = (call) => {
      if (call.method === 'GET' && call.path === '/api/v1/auth/session') {
        sessionReads += 1
        return jsonResponse(
          200,
          sessionReads === 1
            ? { authenticated: false }
            : { authenticated: true, session_id: 's-1' },
        )
      }
      if (call.method === 'GET' && call.path === '/api/v1/auth/me') {
        return errorResponse(401, 'authn.session_expired', 'trace-me')
      }
      if (call.method === 'GET' && call.path === '/notes') {
        return errorResponse(401, 'authn.session_expired', 'trace-notes')
      }
      return respond(call)
    }
    let api: SpeedAppServices['api'] | undefined
    const capture: SpeedAppProvider = (services, children) => {
      api = services.api
      return <>{children}</>
    }
    const boot = await mountApp(container, {
      view: <div />,
      providers: [capture],
      client: { reporter },
    })
    const drive = api
    if (drive === undefined) {
      throw new Error('the provider must receive the assembled client')
    }
    await drive<unknown>('/notes').catch(() => undefined)
    // The client's own diagnostic (the failed silent refresh) reached the
    // supplied sink, not the console default.
    expect(reporter.warn).toHaveBeenCalledTimes(1)
    expect(reporter.error).not.toHaveBeenCalled()
    expect(consoleWarn).not.toHaveBeenCalled()
    await teardown(boot, container)
  })

  it('forwards the definition theme options to the app theme provider', async () => {
    function ThemeProbe(): ReactElement {
      const theme = useTheme()
      return (
        <span data-testid="probe">{`${theme.palette.mode}:${theme.palette.primary.main}`}</span>
      )
    }
    const container = document.createElement('div')
    document.body.appendChild(container)
    const boot = await mountApp(container, {
      view: <ThemeProbe />,
      theme: {
        projectTokens: { color: { semantic: { primary: { main: '#123456' } } } },
        defaultMode: 'dark',
      },
    })
    expect(container.textContent).toBe('dark:#123456')
    await teardown(boot, container)
  })

  it('keeps the shipped theme when the definition sets none', async () => {
    function ThemeProbe(): ReactElement {
      const theme = useTheme()
      return (
        <span data-testid="probe">{`${theme.palette.mode}:${theme.palette.primary.main}`}</span>
      )
    }
    const container = document.createElement('div')
    document.body.appendChild(container)
    const boot = await mountApp(container, { view: <ThemeProbe /> })
    // The assembly's default: light mode over the shipped project tokens.
    const shipped = createAppTheme()
    expect(container.textContent).toBe(`light:${shipped.theme.palette.primary.main}`)
    await teardown(boot, container)
  })

  it('merges the definition i18n url parameter, storage key and profile language into instance creation', async () => {
    // The URL parameter name: a custom parameter resolves the language
    // even though the navigator leg is pinned to zh-CN.
    window.history.replaceState({}, '', '/?hl=en-US')
    const fromUrl = document.createElement('div')
    document.body.appendChild(fromUrl)
    const urlBoot = await mountApp(fromUrl, {
      view: <div />,
      i18n: { urlParameterName: 'hl' },
    })
    expect(urlBoot.i18n.language).toBe('en-US')
    await teardown(urlBoot, fromUrl)

    // The storage key: the stored choice is read under the configured
    // key, and a store answering only that key proves the key is used.
    window.history.replaceState({}, '', '/')
    Object.defineProperty(globalThis, 'localStorage', {
      value: {
        getItem: (key: string) => (key === 'app.locale' ? 'en-US' : null),
        setItem: () => undefined,
      },
      configurable: true,
      writable: true,
    })
    const fromStorage = document.createElement('div')
    document.body.appendChild(fromStorage)
    const storageBoot = await mountApp(fromStorage, {
      view: <div />,
      i18n: { storageKey: 'app.locale' },
    })
    expect(storageBoot.i18n.language).toBe('en-US')
    await teardown(storageBoot, fromStorage)

    // The profile language outranks the navigator leg (pinned zh-CN).
    const fromProfile = document.createElement('div')
    document.body.appendChild(fromProfile)
    const profileBoot = await mountApp(fromProfile, {
      view: <div />,
      i18n: { profileLanguage: 'en-US' },
    })
    expect(profileBoot.i18n.language).toBe('en-US')
    await teardown(profileBoot, fromProfile)
  })

  it('merges the definition i18n supported languages, default language and missing-key handler into instance creation', async () => {
    // A narrowed supported set: registration covers only en-US, and the
    // zh-CN bundles of the framework namespaces are reduced away.
    const narrowed = document.createElement('div')
    document.body.appendChild(narrowed)
    const narrowedBoot = await mountApp(narrowed, {
      view: <div />,
      i18n: { supportedLanguages: ['en-US'], defaultLanguage: 'en-US' },
    })
    expect(narrowedBoot.i18n.language).toBe('en-US')
    expect(
      narrowedBoot.i18n.hasResourceBundle('en-US', PRODUCT_SHELL_NAMESPACE),
    ).toBe(true)
    expect(
      narrowedBoot.i18n.hasResourceBundle('zh-CN', PRODUCT_SHELL_NAMESPACE),
    ).toBe(false)
    await teardown(narrowedBoot, narrowed)

    // The default language: an unsupported navigator language falls to
    // the configured one instead of the shipped en-US default.
    Object.defineProperty(window.navigator, 'languages', {
      value: ['fr-FR'],
      configurable: true,
    })
    const fallback = document.createElement('div')
    document.body.appendChild(fallback)
    const fallbackBoot = await mountApp(fallback, {
      view: <div />,
      i18n: { defaultLanguage: 'zh-CN' },
    })
    expect(fallbackBoot.i18n.language).toBe('zh-CN')
    await teardown(fallbackBoot, fallback)

    // The missing-key handler reaches the instance's lookups.
    const onMissingKey = vi.fn()
    const missing = document.createElement('div')
    document.body.appendChild(missing)
    const missingBoot = await mountApp(missing, {
      view: <div />,
      i18n: { onMissingKey },
    })
    missingBoot.i18n.t('product-shell:no.such.key')
    expect(onMissingKey).toHaveBeenCalledTimes(1)
    expect(onMissingKey.mock.calls[0]?.[0]).toMatchObject({
      namespace: 'product-shell',
      key: 'no.such.key',
    })
    await teardown(missingBoot, missing)
  })

  it('keeps the shipped bilingual instance and negotiation defaults when the definition sets no i18n options', async () => {
    // No source resolves (the navigator language is unsupported), so the
    // default fallback decides: en-US, the platform default -- not the
    // zh-CN the definition could have named.
    Object.defineProperty(window.navigator, 'languages', {
      value: ['fr-FR'],
      configurable: true,
    })
    const container = document.createElement('div')
    document.body.appendChild(container)
    const boot = await mountApp(container, { view: <div /> })
    expect(boot.i18n.language).toBe('en-US')
    expect(
      boot.i18n.hasResourceBundle('zh-CN', PRODUCT_SHELL_NAMESPACE),
    ).toBe(true)
    expect(
      boot.i18n.hasResourceBundle('en-US', PRODUCT_SHELL_NAMESPACE),
    ).toBe(true)
    await teardown(boot, container)
  })

  it('merges a resourceOverrides leaf into a namespace before its registration', async () => {
    const container = document.createElement('div')
    document.body.appendChild(container)
    const boot = await mountApp(container, {
      view: <div />,
      resourceOverrides: {
        [PRODUCT_SHELL_NAMESPACE]: {
          'zh-CN': { announcements: { sessionEnded: 'Host announcement' } },
        },
      },
    })
    expect(boot.i18n.t('product-shell:announcements.sessionEnded')).toBe(
      'Host announcement',
    )
    // The untouched language keeps the shipped text.
    expect(
      boot.i18n.t('product-shell:announcements.sessionEnded', { lng: 'en-US' }),
    ).not.toBe('Host announcement')
    await teardown(boot, container)
  })

  it('replaces a framework namespace listed in the definition, instead of registering it twice', async () => {
    const container = document.createElement('div')
    document.body.appendChild(container)
    const boot = await mountApp(container, {
      view: <div />,
      namespaces: [
        {
          namespace: UI_KIT_NAMESPACE,
          resources: {
            'zh-CN': { greeting: 'host zh' },
            'en-US': { greeting: 'host en' },
          },
        },
      ],
      // The overlay merges into the replacement, not the shipped bundle:
      // the host can still replace a leaf of what it supplied.
      resourceOverrides: {
        [UI_KIT_NAMESPACE]: { 'zh-CN': { greeting: 'overlaid zh' } },
      },
    })
    // The listed bundle is the namespace's registration (no double
    // registration throw), the overlay landed on it, and the shipped
    // bundle is gone.
    expect(boot.i18n.t('ui-kit:greeting')).toBe('overlaid zh')
    expect(boot.i18n.exists('ui-kit:components')).toBe(false)
    await teardown(boot, container)
  })

  it('adds a whole language through resourceOverrides, and leaves the default supported set untouched', async () => {
    const french = (
      base: Readonly<Record<string, ResourceBundle>>,
    ): ResourceBundle => {
      const shipped = base['zh-CN']
      if (shipped === undefined) {
        throw new Error('the shipped bundle must carry zh-CN')
      }
      return prefixedBundle(shipped, 'fr:')
    }
    const container = document.createElement('div')
    document.body.appendChild(container)
    const boot = await mountApp(container, {
      view: <div />,
      i18n: { supportedLanguages: ['zh-CN', 'en-US', 'fr-FR'] },
      resourceOverrides: {
        [UI_KIT_NAMESPACE]: { 'fr-FR': french(uiKitResources) },
        [LAYOUT_KIT_NAMESPACE]: { 'fr-FR': french(layoutKitResources) },
        [AUTH_UI_NAMESPACE]: { 'fr-FR': french(authUiResources) },
        [PRODUCT_SHELL_NAMESPACE]: { 'fr-FR': french(productShellResources) },
        [PLATFORM_ERRORS_NAMESPACE]: { 'fr-FR': french(platformErrorResources) },
      },
    })
    // Every registered namespace now carries the supplied language, and
    // its text is the host's.
    expect(boot.i18n.hasResourceBundle('fr-FR', UI_KIT_NAMESPACE)).toBe(true)
    expect(
      boot.i18n.hasResourceBundle('fr-FR', PLATFORM_ERRORS_NAMESPACE),
    ).toBe(true)
    expect(
      boot.i18n.t('product-shell:announcements.sessionEnded', { lng: 'fr-FR' }),
    ).toBe(`fr:${zhCN.announcements.sessionEnded}`)
    // The platform default set is a constant, not a side effect of the
    // instance's configuration.
    expect([...DEFAULT_SUPPORTED_LANGUAGES]).toEqual(['zh-CN', 'en-US'])
    await teardown(boot, container)
  })

  it('reduces an overlaid language the instance does not support', async () => {
    // The overlay supplies a whole third language, but the instance's
    // supported set is the shipped pair: the registration is reduced to
    // it, so the extra bundle is not dead weight and nothing throws.
    const container = document.createElement('div')
    document.body.appendChild(container)
    const boot = await mountApp(container, {
      view: <div />,
      resourceOverrides: {
        [PRODUCT_SHELL_NAMESPACE]: {
          'fr-FR': prefixedBundle(productShellResources['zh-CN']!, 'fr:'),
        },
      },
    })
    expect(boot.i18n.language).toBe('zh-CN')
    expect(
      boot.i18n.hasResourceBundle('fr-FR', PRODUCT_SHELL_NAMESPACE),
    ).toBe(false)
    expect(
      boot.i18n.hasResourceBundle('zh-CN', PRODUCT_SHELL_NAMESPACE),
    ).toBe(true)
    expect([...DEFAULT_SUPPORTED_LANGUAGES]).toEqual(['zh-CN', 'en-US'])
    await teardown(boot, container)
  })

  it('refuses a resourceOverrides leaf the base bundle does not carry', () => {
    const container = document.createElement('div')
    expect(() =>
      bootstrapSpeedApp(container, {
        view: <div />,
        resourceOverrides: {
          [PRODUCT_SHELL_NAMESPACE]: {
            'zh-CN': { announcements: { unknown: 'x' } },
          },
        },
      }),
    ).toThrow(/unknown/)
  })

  it('refuses a platform-error override that drops a packaged code', () => {
    // Reachable when the instance's supported set omits the shipped pair:
    // the framework namespaces are replaced wholesale, the platform bundle
    // arrives only through the override, and registerNamespace then sees a
    // single language -- so nothing but the packaged reference can catch a
    // bundle missing a code.
    const fr = { a: 'x' }
    expect(() =>
      createSpeedI18n({
        supportedLanguages: ['fr-FR'],
        defaultLanguage: 'fr-FR',
        namespaces: [
          { namespace: UI_KIT_NAMESPACE, resources: { 'fr-FR': fr } },
          { namespace: LAYOUT_KIT_NAMESPACE, resources: { 'fr-FR': fr } },
          { namespace: AUTH_UI_NAMESPACE, resources: { 'fr-FR': fr } },
          { namespace: PRODUCT_SHELL_NAMESPACE, resources: { 'fr-FR': fr } },
        ],
        resourceOverrides: {
          [PLATFORM_ERRORS_NAMESPACE]: {
            'fr-FR': { errors: { authn: { invalid_credentials: 'x' } } },
          },
        },
      }),
    ).toThrow(/packaged key set/)
  })

  it('refuses a framework namespace listed twice in the definition', async () => {
        expect(() =>
      createSpeedI18n({
        namespaces: [
          { namespace: PRODUCT_SHELL_NAMESPACE, resources: productShellResources },
          { namespace: PRODUCT_SHELL_NAMESPACE, resources: productShellResources },
        ],
      }),
    ).toThrow(/more than once/)
  })

  it('binds <html lang> to the instance language, follows a switch, and releases the binding on unmount', async () => {
    document.documentElement.lang = 'initial'
    const container = document.createElement('div')
    document.body.appendChild(container)
    const boot = await mountApp(container, { view: <div /> })
    expect(document.documentElement.lang).toBe('zh-CN')
    await act(async () => {
      await switchLanguage(boot.i18n, 'en-US')
    })
    expect(document.documentElement.lang).toBe('en-US')
    await teardown(boot, container)
    // The subscription went with the page: a later switch on the same
    // instance no longer touches the document.
    await act(async () => {
      await switchLanguage(boot.i18n, 'zh-CN')
    })
    expect(document.documentElement.lang).toBe('en-US')
  })

  it('leaves <html lang> untouched when the definition disables the binding', async () => {
    document.documentElement.lang = 'de-DE'
    const container = document.createElement('div')
    document.body.appendChild(container)
    const boot = await mountApp(container, {
      view: <div />,
      documentLanguage: false,
    })
    expect(document.documentElement.lang).toBe('de-DE')
    await act(async () => {
      await switchLanguage(boot.i18n, 'en-US')
    })
    expect(document.documentElement.lang).toBe('de-DE')
    await teardown(boot, container)
  })

  it('a declared sessionEnded replaces the shipped default, and evictAllQueries still empties the cache from the override', async () => {
    const container = document.createElement('div')
    document.body.appendChild(container)
    const contexts: Array<{
      readonly queryClient: QueryClient
      readonly evictAllQueries: () => void
    }> = []
    const boot = await mountApp(container, {
      view: <span>app</span>,
      sessionEnded: (context) => {
        contexts.push(context)
      },
    })
    await act(async () => {
      await signIn(boot.session)
    })
    seedAllDomains(boot.queryClient)
    await act(async () => {
      await boot.session.logout()
    })

    // The override ran -- and only the override: the declared handler
    // replaces the shipped default action rather than chaining onto it,
    // so the cache the override did not evict is still there, and the
    // context hands the default action back for an override that wants
    // it (asserted next).
    expect(contexts).toHaveLength(1)
    expect(contexts[0]?.queryClient).toBe(boot.queryClient)
    expect(boot.queryClient.getQueryCache().findAll().length).toBeGreaterThan(0)

    contexts[0]?.evictAllQueries()
    expect(boot.queryClient.getQueryCache().findAll()).toHaveLength(0)

    await act(async () => {
      boot.root.unmount()
    })
    container.remove()
  })

  it('with no override, the shipped default evicts the cache on the session-end transition', async () => {
    const container = document.createElement('div')
    document.body.appendChild(container)
    const boot = await mountApp(container, { view: <span>app</span> })
    await act(async () => {
      await signIn(boot.session)
    })
    seedAllDomains(boot.queryClient)
    expect(boot.queryClient.getQueryCache().findAll()).toHaveLength(3)
    await act(async () => {
      await boot.session.logout()
    })
    expect(boot.queryClient.getQueryCache().findAll()).toHaveLength(0)

    await act(async () => {
      boot.root.unmount()
    })
    container.remove()
  })
})

describe('createQueryClient', () => {
  it('writes the read policy out explicitly instead of leaving it to the library', () => {
    const { queries, mutations } = createQueryClient().getDefaultOptions()

    // getDefaultOptions returns the options as constructed, without
    // merging the library's own defaults, so an exact equality pins the
    // keys the assembly wrote: an omitted trigger fails here even though
    // the library would have supplied the same value.
    expect(queries).toEqual({
      retry: false,
      staleTime: 0,
      refetchOnMount: true,
      refetchOnWindowFocus: true,
      refetchOnReconnect: true,
    })
    expect(mutations).toEqual({ retry: false })
  })
})

describe('watchSessionEnd', () => {
  it('empties every domain a sign-out leaves behind: the tenant rows, the identity-domain rows and any unrelated key', async () => {
    // The identity-domain rows (bare spec-path keys -- nothing in
    // @speed/api-sdk is tenant-namespaced) have no tenant segment for a
    // tenant-prefixed removal to reach, so without total eviction a
    // different account signing in afterward would inherit them. The
    // eviction is total: every domain is asserted gone, including a key
    // no eviction call site knows about -- a domain no eviction path
    // names cannot leak either.
    const rig = makeRealClientRig(respond)
    const queryClient = new QueryClient()
    watchSessionEnd(rig.session, queryClient)

    await signIn(rig.session)
    seedAllDomains(queryClient)
    expect(queryClient.getQueryCache().findAll()).toHaveLength(3)

    await rig.session.logout()

    expect(queryClient.getQueryCache().findAll()).toHaveLength(0)
    expect(
      queryClient.getQueryData(['tenant', TENANT_ID, '/notes', {}]),
    ).toBeUndefined()
    expect(queryClient.getQueryData(['/api/v1/auth/sessions'])).toBeUndefined()
    expect(queryClient.getQueryData(['preferences'])).toBeUndefined()
  })

  it('empties the same domains a session death (a refused silent refresh) leaves behind', async () => {
    // refresh() never rejects for a refused token -- it resolves false
    // and signs the session out locally (auth-core's own contract), the
    // same authenticated -> anonymous transition a manual sign-out
    // produces, so the default must fire on this path too.
    const rig = makeRealClientRig((call) => {
      if (call.method === 'POST' && call.path === '/api/v1/auth/token') {
        // The token endpoint refuses an unusable grant the way RFC 6749
        // §5.2 does -- a 400. auth-core reads it as terminal and signs
        // the session out locally; a refusal authored as a 401 would
        // instead re-enter the client's own refresh leg, because the
        // generated token operation presents the access token (it is not
        // the credential-less mutator the seam's design calls for).
        return errorResponse(400, 'authn.token_invalid')
      }
      return respond(call)
    })
    const queryClient = new QueryClient()
    watchSessionEnd(rig.session, queryClient)

    await signIn(rig.session)
    seedAllDomains(queryClient)

    expect(await rig.session.refresh()).toBe(false)

    expect(queryClient.getQueryCache().findAll()).toHaveLength(0)
    expect(
      queryClient.getQueryData(['tenant', TENANT_ID, '/notes', {}]),
    ).toBeUndefined()
    expect(queryClient.getQueryData(['/api/v1/auth/sessions'])).toBeUndefined()
  })

  it('does nothing while the session was never authenticated', async () => {
    const rig = makeRealClientRig(respond)
    const queryClient = new QueryClient()
    watchSessionEnd(rig.session, queryClient)
    seedAllDomains(queryClient)

    // No authenticated -> anonymous edge can occur without a login, so
    // every seeded row survives.
    expect(queryClient.getQueryCache().findAll()).toHaveLength(3)
    expect(
      queryClient.getQueryData(['tenant', TENANT_ID, '/notes', {}]),
    ).toBeDefined()
  })

  it('leaves the cache alone across a still-authenticated notify (a silent refresh)', async () => {
    // A refresh rotates the credentials but keeps the session
    // authenticated throughout, so the strategy -- which fires strictly
    // on the authenticated -> anonymous edge -- must not also fire here;
    // only a sign-out or a refused refresh (a session death) evicts.
    const rig = makeRealClientRig(respond)
    const queryClient = new QueryClient()
    watchSessionEnd(rig.session, queryClient)

    await signIn(rig.session)
    seedAllDomains(queryClient)

    expect(await rig.session.refresh()).toBe(true)

    expect(queryClient.getQueryCache().findAll()).toHaveLength(3)
    expect(
      queryClient.getQueryData(['tenant', TENANT_ID, '/notes', {}]),
    ).toBeDefined()
    expect(queryClient.getQueryData(['/api/v1/auth/sessions'])).toBeDefined()
    expect(queryClient.getQueryData(['preferences'])).toBeDefined()
  })
})

describe('createSpeedI18n', () => {
  it('creates the bilingual instance with the four framework namespaces and the platform error bundle registered', () => {
    // The exported constructor is the production instance: same defaults
    // as bootstrapSpeedApp's (the supported pair, the platform error
    // fallback namespace) with the family four already registered.
    const i18n = createSpeedI18n({
      storage: null,
      urlParameterName: null,
      navigatorLanguages: [],
      defaultLanguage: 'zh-CN',
    })
    expect(i18n.language).toBe('zh-CN')
    expect([...readSupportedLanguages(i18n)]).toEqual(['zh-CN', 'en-US'])
    expect(i18n.hasResourceBundle('zh-CN', UI_KIT_NAMESPACE)).toBe(true)
    expect(i18n.hasResourceBundle('zh-CN', LAYOUT_KIT_NAMESPACE)).toBe(true)
    expect(i18n.hasResourceBundle('zh-CN', AUTH_UI_NAMESPACE)).toBe(true)
    expect(i18n.hasResourceBundle('zh-CN', PRODUCT_SHELL_NAMESPACE)).toBe(true)
    expect(i18n.hasResourceBundle('zh-CN', PLATFORM_ERRORS_NAMESPACE)).toBe(true)
  })

  it('registers the definition namespaces and applies the overlays before that one registration', () => {
    const i18n = createSpeedI18n({
      storage: null,
      urlParameterName: null,
      navigatorLanguages: [],
      defaultLanguage: 'zh-CN',
      namespaces: [
        {
          namespace: 'app-probe',
          resources: {
            'zh-CN': { greeting: 'probe' },
            'en-US': { greeting: 'probe' },
          },
        },
      ],
      resourceOverrides: {
        'app-probe': { 'zh-CN': { greeting: 'overlaid' } },
      },
    })
    expect(i18n.t('app-probe:greeting')).toBe('overlaid')
    // The untouched language keeps the definition's text.
    expect(i18n.t('app-probe:greeting', { lng: 'en-US' })).toBe('probe')
  })
  it('refuses a resourceOverrides key for a namespace the assembly never registers', () => {
    // A misnamed namespace -- or a package whose override was written
    // but never listed in `namespaces` -- would otherwise never be read:
    // the host's copy would silently not apply.
    expect(() =>
      createSpeedI18n({
        storage: null,
        urlParameterName: null,
        navigatorLanguages: [],
        defaultLanguage: 'zh-CN',
        resourceOverrides: {
          ui_kit: { 'zh-CN': { greeting: 'host' } },
        },
      }),
    ).toThrow(/ui_kit/)
  })
})

describe('SpeedAppProviders', () => {
  it('renders the standard stack: translations, theme and the supplied query client', () => {
    const i18n = createSpeedI18n({
      storage: null,
      urlParameterName: null,
      navigatorLanguages: [],
      defaultLanguage: 'zh-CN',
    })
    const queryClient = createQueryClient()
    function Probe(): ReactElement {
      const { t } = useTranslation(PRODUCT_SHELL_NAMESPACE)
      const client = useQueryClient()
      const theme = useTheme()
      return (
        <span data-testid="probe">
          {`${t('announcements.sessionEnded')}|${client === queryClient}|${theme.palette.mode}`}
        </span>
      )
    }
    const utils = render(
      <SpeedAppProviders
        i18n={i18n}
        queryClient={queryClient}
        theme={{ defaultMode: 'dark' }}
      >
        <Probe />
      </SpeedAppProviders>,
    )
    expect(utils.getByTestId('probe').textContent).toBe(
      `${zhCN.announcements.sessionEnded}|true|dark`,
    )
  })
})
