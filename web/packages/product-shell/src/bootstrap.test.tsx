/**
 * bootstrap.test.tsx -- the contracts of the app-entry assembly, each
 * proven at the layer it belongs to:
 *
 * bootstrapSpeedApp -- jsdom mounts of the whole composition: the
 * definition's namespaces register alongside the family's automatic
 * four, the provider chain wraps the declared view in declared order
 * with the assembled services, the client the assembly built answers
 * over the environment's own (stubbed) fetch, the session-end strategy
 * is wired (default and override), and unmount tears the page down.
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

import { QueryClient } from '@tanstack/react-query'
import { act, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { ReactElement } from 'react'
import { isApiError } from '@speed/api-client'
import { AUTH_UI_NAMESPACE } from '@speed/auth-ui'
import { switchLanguage, useTranslation } from '@speed/i18n'
import { PLATFORM_ERRORS_NAMESPACE } from '@speed/i18n/platform-errors'
import { LAYOUT_KIT_NAMESPACE } from '@speed/layout-kit'
import { UI_KIT_NAMESPACE } from '@speed/ui-kit'
import { PRODUCT_SHELL_NAMESPACE } from './resources.js'
import zhCN from './locales/zh-CN.json' with { type: 'json' }
import {
  bootstrapSpeedApp,
  createQueryClient,
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

describe('bootstrapSpeedApp', () => {
  let observed: Array<{
    readonly method: string
    readonly path: string
    readonly origin: string
    readonly acceptLanguage: string | null
    readonly tenant: string | null
    readonly credentials: RequestCredentials | null
  }>
  let realFetch: typeof globalThis.fetch
  let realLanguages: readonly string[] | undefined
  let realLocalStorage: PropertyDescriptor | undefined
  /** The responder the mounted client's fetch consults; tests override
   * it to script a remembered session or another boot answer. */
  let activeResponder: RealResponder

  beforeEach(() => {
    observed = []
    activeResponder = respond
    realFetch = globalThis.fetch
    realLanguages = window.navigator.languages
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
        observed.push({
          method: init?.method ?? 'GET',
          path: url.pathname,
          origin: url.origin,
          acceptLanguage: new Headers(init?.headers).get('accept-language'),
          tenant: new Headers(init?.headers).get('x-tenant'),
          credentials: init?.credentials ?? null,
        })
        return activeResponder({
          method: init?.method ?? 'GET',
          path: url.pathname,
          authorization: new Headers(init?.headers).get('authorization'),
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
    Object.defineProperty(globalThis, 'fetch', {
      value: realFetch,
      configurable: true,
    })
    if (realLocalStorage !== undefined) {
      Object.defineProperty(globalThis, 'localStorage', realLocalStorage)
    }
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
