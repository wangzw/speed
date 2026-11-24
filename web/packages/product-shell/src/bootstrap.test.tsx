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

const TENANT_ID = 'tenant-1'

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
    return jsonResponse(200, { authenticated: false })
  }
  if (key === 'GET /api/v1/features') {
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

function seedTenantCache(queryClient: QueryClient): void {
  queryClient.setQueryData(
    ['tenant', TENANT_ID, '/notes', {}],
    { notes: [{ id: 'note-1', text: 'cached from the departing session' }] },
  )
}

function seedIdentityCache(queryClient: QueryClient): void {
  queryClient.setQueryData(['/api/v1/auth/sessions'], {
    sessions: [{ id: 'session-1' }],
  })
}

function seedUnrelatedCache(queryClient: QueryClient): void {
  queryClient.setQueryData(['preferences'], { theme: 'dark' })
}

function seedAllDomains(queryClient: QueryClient): void {
  seedTenantCache(queryClient)
  seedIdentityCache(queryClient)
  seedUnrelatedCache(queryClient)
}

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

function prefixedBundle(bundle: ResourceBundle, prefix: string): ResourceBundle {
  const copy: Record<string, string | ResourceBundle> = {}
  for (const [key, value] of Object.entries(bundle)) {
    copy[key] =
      typeof value === 'string' ? `${prefix}${value}` : prefixedBundle(value, prefix)
  }
  return copy
}

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
  let activeResponder: RealResponder

  beforeEach(() => {
    observed = []
    activeResponder = respond
    realFetch = globalThis.fetch
    realLanguages = window.navigator.languages
    realHref = window.location.href
    Object.defineProperty(window.navigator, 'languages', {
      value: ['zh-CN'],
      configurable: true,
    })
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
    activeResponder = (call) => {
      if (call.path === '/api/v1/auth/session') {
        return jsonResponse(200, { authenticated: true, session_id: 's-1' })
      }
      return respond(call)
    }
    const boot = await mountApp(container, { view: <div /> })

    await waitFor(() =>
      expect(boot.session.getSnapshot().state).toBe('authenticated'),
    )
    expect(boot.session.getSnapshot().principal).not.toBeNull()

    const sessionRead = observed.find((call) => call.path === '/api/v1/auth/session')
    expect(sessionRead?.credentials).toBe('same-origin')
    const meRead = observed.find((call) => call.path === '/api/v1/auth/me')
    expect(meRead?.credentials).toBe('same-origin')

    await act(async () => {
      boot.root.unmount()
    })
    container.remove()
  })

  it('holds the declared view until the boot restore settles, so a reload never flashes the sign-in surface', async () => {
    const container = document.createElement('div')
    document.body.appendChild(container)
    let release!: () => void
    const answer = new Promise<void>((resolve) => {
      release = resolve
    })
    activeResponder = (call) => {
      if (call.path === '/api/v1/auth/session') {
        return answer.then(() => jsonResponse(200, { authenticated: false }))
      }
      return respond(call)
    }
    const boot = await mountApp(container, {
      view: <span data-testid="probe">app</span>,
    })

    expect(container.querySelector('[data-testid="probe"]')).toBeNull()

    release()
    await waitFor(() => {
      expect(container.querySelector('[data-testid="probe"]')).not.toBeNull()
    })

    await teardown(boot, container)
  })

  it('renders the declared view after a failed boot restore instead of holding the page blank', async () => {
    const container = document.createElement('div')
    document.body.appendChild(container)
    activeResponder = (call) => {
      if (call.path === '/api/v1/auth/session') {
        return jsonResponse(400, { code: 'authn.invalid_request' })
      }
      return respond(call)
    }
    const boot = await mountApp(container, {
      view: <span data-testid="probe">app</span>,
    })

    await waitFor(() => {
      expect(container.querySelector('[data-testid="probe"]')).not.toBeNull()
    })

    await teardown(boot, container)
  })

  it('announces the same-origin credential mode on every request and sends no Authorization', async () => {
    const container = document.createElement('div')
    document.body.appendChild(container)
    const boot = await mountApp(container, { view: <div /> })
    await act(async () => {
      await boot.session.startFlow({ method: 'password' })
    })
    for (const call of observed) {
      expect(call.credentials).toBe('same-origin')
    }
    expect(observed.some((call) => call.path === '/api/v1/auth/flows')).toBe(true)
    await act(async () => {
      boot.root.unmount()
    })
    container.remove()
  })

  it('settles a refused request even when the silent restore it triggers has its profile read refused', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const container = document.createElement('div')
    document.body.appendChild(container)
    let sessionReads = 0
    let profileReads = 0
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
    for (let i = 0; i < 64 && !settled; i += 1) {
      await Promise.resolve()
    }
    const deadline = Date.now() + 30_000
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

    await act(async () => {
      await signIn(boot.session)
    })
    expect(observed.length).toBeGreaterThan(0)
    expect(observed.every((call) => call.acceptLanguage === 'zh-CN')).toBe(true)

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

    await act(async () => {
      await signIn(boot.session)
    })
    expect(observed.length).toBeGreaterThan(0)
    expect(observed.every((call) => call.tenant === 'acme')).toBe(true)

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
    const outerNode = container.querySelector('[data-testid="outer"]')
    const innerNode = container.querySelector('[data-testid="inner"]')
    const viewNode = container.querySelector('[data-testid="view"]')
    expect(outerNode).not.toBeNull()
    expect(innerNode).not.toBeNull()
    expect(viewNode).not.toBeNull()
    expect(outerNode?.contains(innerNode)).toBe(true)
    expect(innerNode?.contains(viewNode)).toBe(true)
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
    expect(observed.every((call) => call.headers.get('x-tenant') === null)).toBe(true)
    await teardown(boot, container)
  })

  it('applies timeoutMs from the definition client table', async () => {
    const container = document.createElement('div')
    document.body.appendChild(container)
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
    const projectTokens = {
      color: { semantic: { primary: { main: '#123456', light: '#89ABCD' } } },
    }
    const darkContainer = document.createElement('div')
    document.body.appendChild(darkContainer)
    const darkBoot = await mountApp(darkContainer, {
      view: <ThemeProbe />,
      theme: { projectTokens, defaultMode: 'dark' },
    })
    expect(darkContainer.textContent).toBe('dark:#89ABCD')
    await teardown(darkBoot, darkContainer)

    const lightContainer = document.createElement('div')
    document.body.appendChild(lightContainer)
    const lightBoot = await mountApp(lightContainer, {
      view: <ThemeProbe />,
      theme: { projectTokens },
    })
    expect(lightContainer.textContent).toBe('light:#123456')
    await teardown(lightBoot, lightContainer)
  })

  it('forwards the definition theme presets and default preset to the app theme provider', async () => {
    function ThemeProbe(): ReactElement {
      const theme = useTheme()
      return <span data-testid="probe">{String(theme.shape.borderRadius)}</span>
    }
    const container = document.createElement('div')
    document.body.appendChild(container)
    const boot = await mountApp(container, {
      view: <ThemeProbe />,
      theme: {
        presets: [
          { id: 'sharp', tokens: { shape: { borderRadius: 2 } } },
          { id: 'round', tokens: { shape: { borderRadius: 18 } } },
        ],
        defaultPreset: 'round',
      },
    })
    expect(container.textContent).toBe('18')
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
    const shipped = createAppTheme()
    expect(container.textContent).toBe(`light:${shipped.theme.palette.primary.main}`)
    await teardown(boot, container)
  })

  it('merges the definition i18n url parameter, storage key and profile language into instance creation', async () => {
    window.history.replaceState({}, '', '/?hl=en-US')
    const fromUrl = document.createElement('div')
    document.body.appendChild(fromUrl)
    const urlBoot = await mountApp(fromUrl, {
      view: <div />,
      i18n: { urlParameterName: 'hl' },
    })
    expect(urlBoot.i18n.language).toBe('en-US')
    await teardown(urlBoot, fromUrl)

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
      resourceOverrides: {
        [UI_KIT_NAMESPACE]: { 'zh-CN': { greeting: 'overlaid zh' } },
      },
    })
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
    expect(boot.i18n.hasResourceBundle('fr-FR', UI_KIT_NAMESPACE)).toBe(true)
    expect(
      boot.i18n.hasResourceBundle('fr-FR', PLATFORM_ERRORS_NAMESPACE),
    ).toBe(true)
    expect(
      boot.i18n.t('product-shell:announcements.sessionEnded', { lng: 'fr-FR' }),
    ).toBe(`fr:${zhCN.announcements.sessionEnded}`)
    expect([...DEFAULT_SUPPORTED_LANGUAGES]).toEqual(['zh-CN', 'en-US'])
    await teardown(boot, container)
  })

  it('reduces an overlaid language the instance does not support', async () => {
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
    const rig = makeRealClientRig((call) => {
      if (call.method === 'POST' && call.path === '/api/v1/auth/token') {
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

    expect(queryClient.getQueryCache().findAll()).toHaveLength(3)
    expect(
      queryClient.getQueryData(['tenant', TENANT_ID, '/notes', {}]),
    ).toBeDefined()
  })

  it('leaves the cache alone across a still-authenticated notify (a silent refresh)', async () => {
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
    expect(i18n.t('app-probe:greeting', { lng: 'en-US' })).toBe('probe')
  })
  it('refuses a resourceOverrides key for a namespace the assembly never registers', () => {
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
