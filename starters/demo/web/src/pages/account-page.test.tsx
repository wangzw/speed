import userEvent from '@testing-library/user-event'
import {
  act,
  fireEvent,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import type { ReactElement, ReactNode } from 'react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { accountUiResources } from '@speed/account-ui'
import { bindRequestFn } from '@speed/api-sdk/runtime'
import type { RequestFn, RequestOptions } from '@speed/api-client'
import type { SpeedAppThemeOptions } from '@speed/product-shell/bootstrap'
import { languageDisplayName } from '@speed/i18n'
import type { AuthnMethodList } from '@speed/auth-core'
import { uiKitResources, useAppThemeMode } from '@speed/ui-kit'
import { createBackend } from '../a11y/backends.js'
import {
  COLOR_MODE_STORAGE_KEY,
  initialColorModePreference,
} from '../chrome.js'
import demoEnUS from '../locales/en-US.json' with { type: 'json' }
import {
  ACCOUNT_CALLBACK_PATH,
  ACCOUNT_PATH,
  ACCOUNT_SECTION_PATHS,
} from '../paths.js'
import { renderDemoApp } from '../test-utils/render.js'
import {
  authenticatedSnapshot,
  createTestSession,
  readyFeatures,
} from '../test-utils/session.js'

const SOCIAL_METHODS: AuthnMethodList = {
  methods: [
    { name: 'password', kind: 'form' },
    { name: 'google', kind: 'redirect' },
  ],
  signup: { enabled: false, password: { enabled: false } },
  mfa: { factors: [] },
  human: {
    phone: { required: false, provider: 'off' },
    other: { required: false, provider: 'off' },
  },
  password_reset: { enabled: false, methods: [] },
}

const ADD_PATH = '/api/v1/auth/identifiers'
const US_NATIONAL = '4155552671'
const US_E164 = '+14155552671'
const accountEnUS = accountUiResources['en-US'] as {
  readonly verification: Readonly<Record<string, string>>
  readonly bindingCallback: Readonly<Record<string, string>>
  readonly errors: {
    readonly unknown: string
    readonly authn: Readonly<Record<string, string>>
  }
  readonly profile: {
    readonly title: string
    readonly saved: string
    readonly displayName: { readonly label: string }
  }
  readonly password: {
    readonly title: string
    readonly submit: string
    readonly current: { readonly label: string }
  }
  readonly sessions: {
    readonly title: string
    readonly current: string
    readonly revokeOthers: { readonly label: string }
  }
  readonly appearance: {
    readonly theme: { readonly label: string }
    readonly colorMode: {
      readonly label: string
      readonly options: {
        readonly dark: string
        readonly light: string
        readonly system: string
      }
    }
  }
  readonly preferences: {
    readonly language: { readonly label: string; readonly saved: string }
    readonly timezone: { readonly label: string; readonly saved: string }
  }
  readonly bindings: {
    readonly empty: { readonly title: string }
  }
  readonly identifiers: {
    readonly verified: string
    readonly unverified: string
    readonly groups: { readonly signIn: string }
    readonly kind: {
      readonly username: string
      readonly email: string
      readonly phone: string
    }
    readonly add: { readonly open: string }
    readonly axes: {
      readonly signIn: string
      readonly defaultContact: string
    }
    readonly action: { readonly enable: string; readonly disable: string }
  }
  readonly phone: {
    readonly added: string
    readonly send: string
    readonly verify: string
    readonly phone: { readonly label: string }
    readonly code: { readonly label: string }
  }
}
const bindingEnUS = accountEnUS.bindingCallback
const CANCEL_LABEL = (
  uiKitResources['en-US'] as {
    readonly confirmDialog: { readonly cancelLabel: string }
  }
).confirmDialog.cancelLabel

let calls: {
  readonly method: string
  readonly path: string
  readonly body?: unknown
}[] = []

const themeProbe: {
  mode: string | null
  preference: string | null
} = { mode: null, preference: null }

function ThemeModeProbe(): null {
  const { mode, preference } = useAppThemeMode()
  themeProbe.mode = mode
  themeProbe.preference = preference
  return null
}

function withThemeModeProbe(app: ReactElement): ReactNode {
  return (
    <>
      {app}
      <ThemeModeProbe />
    </>
  )
}

beforeEach(() => {
  window.history.replaceState({}, '', '/')
  calls = []
  themeProbe.mode = null
  themeProbe.preference = null
})

function sends() {
  return calls.filter((call) => call.method === 'POST' && call.path === ADD_PATH)
}

async function chooseAccountSection(
  user: ReturnType<typeof userEvent.setup>,
  name: string,
): Promise<void> {
  await user.click(await screen.findByTestId('account-entry-trigger'))
  await user.click(await screen.findByRole('menuitem', { name }))
}

interface RenderAccountOptions {
  readonly features?: readonly string[]
  readonly backend?: RequestFn
  readonly path?: string
  readonly theme?: SpeedAppThemeOptions
  readonly wrap?: (app: ReactElement) => ReactNode
  readonly methods?: AuthnMethodList | (() => Promise<AuthnMethodList>)
}

function renderAccount(
  search = '',
  options: RenderAccountOptions = {},
): void {
  const backend = options.backend ?? createBackend('data')
  const request = (async <T,>(
    path: string,
    requestOptions?: RequestOptions,
  ): Promise<T> => {
    calls.push({
      method: requestOptions?.method ?? 'GET',
      path,
      body: requestOptions?.body,
    })
    return (await backend(path, requestOptions)) as T
  }) as RequestFn
  bindRequestFn(request)
  window.history.replaceState(
    {},
    '',
    `${options.path ?? ACCOUNT_PATH}${search}`,
  )
  renderDemoApp({
    session: createTestSession(
      authenticatedSnapshot({
        features: readyFeatures(...(options.features ?? [])),
      }),
      options.methods,
    ),
    theme: options.theme,
    wrap: options.wrap,
  })
}

describe('AccountSettingsPage', () => {
  it('does not send a verification code on an ordinary visit', async () => {
    renderAccount()

    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: demoEnUS.account.sections.profile,
      }),
    ).toBeInTheDocument()
    expect(
      await screen.findByText(
        (accountEnUS.verification.target ?? '').replace(
          '{{email}}',
          'user@example.com',
        ),
      ),
    ).toBeInTheDocument()
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 0))
    })
    expect(sends()).toHaveLength(0)
  })

  it('sends one code when the banner lands with verify=1', async () => {
    renderAccount('?verify=1')

    expect(
      await screen.findByLabelText(accountEnUS.verification.codeLabel ?? ''),
    ).toBeInTheDocument()
    expect(
      screen.queryByLabelText(accountEnUS.verification.changeLabel ?? ''),
    ).toBeNull()
    await waitFor(() => expect(sends()).toHaveLength(1))
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 0))
    })
    expect(sends()).toHaveLength(1)
  })

  it('words a refused step-up round trip that landed here', async () => {
    renderAccount('#authn_error=login-failed', {
      path: ACCOUNT_SECTION_PATHS.profile,
    })

    const errors = (accountUiResources['en-US'] as {
      readonly errors: { readonly authn: Readonly<Record<string, string>> }
    }).errors
    const text = errors.authn.login_failed ?? ''
    const notice = await screen.findByText(text)
    const heading = screen.getByRole('heading', {
      level: 1,
      name: demoEnUS.account.sections.profile,
    })
    expect(
      heading.compareDocumentPosition(notice) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy()
    expect(window.location.hash).toBe('')

    await userEvent
      .setup()
      .click(
        screen.getByRole('button', {
          name: demoEnUS.callbackNotice.dismiss,
        }),
      )
    await waitFor(() => expect(screen.queryByText(text)).toBeNull())
  })

  it('starts in the change-address form when the banner asked to change', async () => {
    renderAccount('?change=email')

    expect(
      await screen.findByLabelText(accountEnUS.verification.changeLabel ?? ''),
    ).toBeInTheDocument()
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 0))
    })
    expect(sends()).toHaveLength(0)
  })

  it('keeps sending nothing after the change form is closed', async () => {
    renderAccount('?change=email')

    fireEvent.click(
      await screen.findByRole('button', {
        name: accountEnUS.verification.changeCancel ?? '',
      }),
    )
    expect(
      await screen.findByLabelText(accountEnUS.verification.codeLabel ?? ''),
    ).toBeInTheDocument()
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 0))
    })
    expect(sends()).toHaveLength(0)
  })
})

describe('AccountSettingsPage sections', () => {
  it('opens the profile pane by default, with the display name editable in place', async () => {
    renderAccount()

    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: demoEnUS.account.sections.profile,
      }),
    ).toBeInTheDocument()
    const identity = screen.getByRole('region', {
      name: demoEnUS.account.blocks.identity,
    })
    expect(
      await within(identity).findByLabelText(
        accountEnUS.profile.displayName.label,
      ),
    ).toBeInTheDocument()
    expect(screen.queryByTestId('account-row-display-name')).toBeNull()
  })

  it('swaps to the security pane and opens a row action in place', async () => {
    const user = userEvent.setup()
    renderAccount()

    expect(
      await screen.findByLabelText(accountEnUS.profile.displayName.label),
    ).toBeInTheDocument()
    await chooseAccountSection(user, demoEnUS.account.sections.security)
    await user.click(await screen.findByTestId('security-row-password'))

    expect(
      await screen.findByLabelText(accountEnUS.password.current.label),
    ).toBeInTheDocument()
    expect(
      screen.queryByLabelText(accountEnUS.profile.displayName.label),
    ).toBeNull()
  })

  it('lists the sign-in methods as rows with one action each', async () => {
    renderAccount('?section=security', {
      features: ['authn.mfa'],
      methods: SOCIAL_METHODS,
    })

    for (const title of [
      demoEnUS.account.rows.label.password,
      demoEnUS.account.rows.label.mfa,
      demoEnUS.account.rows.label.social,
      demoEnUS.account.rows.label.loginWays,
    ]) {
      expect(await screen.findByText(title)).toBeInTheDocument()
    }
    expect(screen.getByTestId('security-row-password')).toBeInTheDocument()
    expect(screen.queryByTestId('security-row-mfa')).toBeNull()
    expect(screen.queryByTestId('security-row-social')).toBeNull()
    expect(screen.queryByTestId('security-row-identifiers')).toBeNull()
  })

  it('does not render the social card when the deployment serves no social sign-in', async () => {
    renderAccount('?section=security', { features: ['authn.mfa'] })

    expect(
      await screen.findByText(demoEnUS.account.rows.label.password),
    ).toBeInTheDocument()
    await waitFor(() =>
      expect(
        screen.queryByText(demoEnUS.account.rows.label.social),
      ).toBeNull(),
    )
    expect(
      screen.queryByText(accountEnUS.bindings.empty.title),
    ).toBeNull()
  })

  it('keeps the social card with its unbound providers when nothing is bound', async () => {
    renderAccount('?section=security', {
      features: ['authn.mfa'],
      methods: SOCIAL_METHODS,
    })

    const card = await screen.findByRole('region', {
      name: demoEnUS.account.rows.label.social,
    })
    expect(
      within(card).getByRole('button', { name: /google/i }),
    ).toBeInTheDocument()
    expect(
      within(card).queryByText(accountEnUS.bindings.empty.title),
    ).toBeNull()
  })

  it('keeps a bound identity visible when the discovery read fails', async () => {
    const base = createBackend('data')
    const backend: RequestFn = (async <T,>(
      path: string,
      requestOptions?: RequestOptions,
    ): Promise<T> => {
      const body = await base(path, requestOptions)
      if (
        path === '/api/v1/auth/me' &&
        (requestOptions?.method ?? 'GET') === 'GET'
      ) {
        return {
          ...(body as Record<string, unknown>),
          links: [
            {
              id: 'link-1',
              provider: 'google',
              created_at: '2026-01-01T00:00:00Z',
            },
          ],
        } as T
      }
      return body as T
    }) as RequestFn
    renderAccount('?section=security', {
      features: ['authn.mfa'],
      backend,
      methods: () => Promise.reject(new Error('the methods read failed')),
    })

    const card = await screen.findByRole('region', {
      name: demoEnUS.account.rows.label.social,
    })
    expect(
      within(card).getByRole('button', { name: /google/i }),
    ).toBeInTheDocument()
    expect(
      within(card).queryByText(accountEnUS.bindings.empty.title),
    ).toBeNull()
  })

  it('shows the sign-in ways on the pane, without the contact axis', async () => {
    renderAccount('?section=security')

    expect(
      await screen.findByRole('switch', {
        name: accountEnUS.identifiers.action.disable,
      }),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: accountEnUS.identifiers.add.open }),
    ).toBeNull()
    expect(
      await screen.findByRole('combobox', {
        name: accountEnUS.identifiers.kind.email,
      }),
    ).toBeInTheDocument()
    expect(
      screen.queryByText(accountEnUS.identifiers.axes.defaultContact),
    ).toBeNull()
    for (const kind of [
      accountEnUS.identifiers.kind.username,
      accountEnUS.identifiers.kind.email,
      accountEnUS.identifiers.kind.phone,
    ]) {
      expect(await screen.findByText(kind)).toBeInTheDocument()
    }
  })

  it('shows the contact axis in the profile pane, without the sign-in axis', async () => {
    renderAccount('?section=profile')

    const row = (await screen.findByText('user@example.com')).closest('tr')
    expect(row).not.toBeNull()
    expect(
      within(row as HTMLElement).getByText(
        accountEnUS.identifiers.axes.defaultContact,
      ),
    ).toBeInTheDocument()
    expect(
      within(row as HTMLElement).queryByRole('button', {
        name: accountEnUS.identifiers.axes.defaultContact,
      }),
    ).toBeNull()
    expect(
      screen.queryByText(accountEnUS.identifiers.axes.signIn),
    ).toBeNull()
  })

  it('renders the preference controls directly', async () => {
    renderAccount('?section=preferences')

    const block = await screen.findByRole('region', {
      name: demoEnUS.account.blocks.appearance,
    })
    expect(
      within(block).getByText(demoEnUS.account.rows.label.theme),
    ).toBeInTheDocument()
    expect(
      within(block).getByText(demoEnUS.account.rows.label.colorMode),
    ).toBeInTheDocument()
    for (const name of [
      accountEnUS.appearance.theme.label,
      accountEnUS.appearance.colorMode.label,
      accountEnUS.preferences.language.label,
      accountEnUS.preferences.timezone.label,
    ]) {
      expect(
        await within(block).findByRole('combobox', { name }),
      ).toBeInTheDocument()
    }
  })

  it('keeps only the later result when a language save is followed by a time zone save', async () => {
    renderAccount('?section=preferences')
    const zh = accountUiResources['zh-CN'] as {
      readonly preferences: {
        readonly language: { readonly label: string; readonly saved: string }
        readonly timezone: { readonly label: string; readonly saved: string }
      }
    }

    const language = await screen.findByRole('combobox', {
      name: accountEnUS.preferences.language.label,
    })
    const chinese = languageDisplayName('zh-CN', 'en-US', ['zh-CN', 'en-US'])
    fireEvent.mouseDown(language)
    fireEvent.click(screen.getByRole('option', { name: chinese }))

    const languageSaved = zh.preferences.language.saved.replace(
      '{{value}}',
      languageDisplayName('zh-CN', 'zh-CN', ['zh-CN', 'en-US']),
    )
    await screen.findByText(languageSaved, { exact: true })

    const timezone = screen.getByRole('combobox', {
      name: zh.preferences.timezone.label,
    })
    fireEvent.mouseDown(timezone)
    fireEvent.click(screen.getByRole('option', { name: 'Europe/Berlin' }))

    await screen.findByText(
      zh.preferences.timezone.saved.replace('{{value}}', 'Europe/Berlin'),
      { exact: true },
    )
    expect(screen.queryByText(languageSaved, { exact: true })).toBeNull()
  })

  it('applies a color-mode choice at once, writes the device key and sends only color_mode', async () => {
    window.localStorage.removeItem(COLOR_MODE_STORAGE_KEY)
    renderAccount('?section=preferences', { wrap: withThemeModeProbe })

    const select = await screen.findByRole('combobox', {
      name: accountEnUS.appearance.colorMode.label,
    })
    await waitFor(() =>
      expect(select).toHaveTextContent(
        accountEnUS.appearance.colorMode.options.light,
      ),
    )

    fireEvent.mouseDown(select)
    fireEvent.click(
      screen.getByRole('option', {
        name: accountEnUS.appearance.colorMode.options.dark,
      }),
    )

    await waitFor(() => expect(themeProbe.mode).toBe('dark'))
    expect(themeProbe.preference).toBe('dark')
    expect(window.localStorage.getItem(COLOR_MODE_STORAGE_KEY)).toBe('dark')

    const patches = calls.filter(
      (call) => call.method === 'PATCH' && call.path === '/api/v1/auth/me',
    )
    expect(patches).toHaveLength(1)
    expect(patches[0]?.body).toEqual({ color_mode: 'dark' })
    await waitFor(() =>
      expect(select).toHaveTextContent(
        accountEnUS.appearance.colorMode.options.dark,
      ),
    )
  })

  it('resolves a stored system preference to the dark palette', async () => {
    window.localStorage.setItem(COLOR_MODE_STORAGE_KEY, 'system')
    renderAccount('?section=preferences', {
      theme: { defaultMode: initialColorModePreference() },
      wrap: withThemeModeProbe,
    })

    const select = await screen.findByRole('combobox', {
      name: accountEnUS.appearance.colorMode.label,
    })
    await waitFor(() =>
      expect(select).toHaveTextContent(
        accountEnUS.appearance.colorMode.options.system,
      ),
    )
    expect(themeProbe.preference).toBe('system')
    expect(themeProbe.mode).toBe('dark')
  })
})

describe('AccountSettingsPage profile pane', () => {
  it('renders the display name as a field, with no action to open first', async () => {
    renderAccount('')

    expect(
      await screen.findByRole('textbox', {
        name: accountEnUS.profile.displayName.label,
      }),
    ).toHaveValue('Test User')
    expect(screen.queryByTestId('account-row-display-name')).toBeNull()
    expect(screen.queryByTestId('account-identity-summary')).toBeNull()
  })

  it('lists the four profile facts in order, with no save button', async () => {
    renderAccount('?section=profile', { features: ['authn.phone'] })

    const identity = await screen.findByRole('region', {
      name: demoEnUS.account.blocks.identity,
    })
    expect(
      within(identity).getByText(demoEnUS.account.rows.label.username),
    ).toBeInTheDocument()
    expect(
      within(identity).getByText(demoEnUS.account.rows.label.displayName),
    ).toBeInTheDocument()
    expect(
      screen.getByRole('region', { name: demoEnUS.account.rows.label.email }),
    ).toBeInTheDocument()
    expect(
      screen.getByRole('region', { name: demoEnUS.account.rows.label.phone }),
    ).toBeInTheDocument()
    expect(within(identity).queryByRole('button')).toBeNull()
  })
})

describe('AccountBindingCallbackPage', () => {
  function renderCallback() {
    bindRequestFn(createBackend('data'))
    const session = createTestSession(authenticatedSnapshot())
    const readFlow = vi.fn(session.readFlow)
    session.readFlow = readFlow
    renderDemoApp({ session })
    return { readFlow }
  }

  it('returns to the security pane when the callback names no source', async () => {
    window.history.replaceState(
      {},
      '',
      `${ACCOUNT_CALLBACK_PATH}#authn_error=conflict`,
    )
    const { readFlow } = renderCallback()

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(bindingEnUS.conflict ?? '')
    expect(readFlow).not.toHaveBeenCalled()
    expect(window.location.hash).toBe('')
    expect(
      screen.queryByRole('button', {
        name: demoEnUS.callbackNotice.dismiss,
      }),
    ).toBeNull()
    expect(
      screen.queryByRole('button', { name: bindingEnUS.retry ?? '' }),
    ).toBeNull()

    await userEvent
      .setup()
      .click(
        screen.getByRole('button', { name: bindingEnUS.backToAccount ?? '' }),
      )
    await waitFor(() =>
      expect(window.location.pathname).toBe(ACCOUNT_SECTION_PATHS.security),
    )
  })

  it('returns to the pane the binding started from', async () => {
    window.history.replaceState(
      {},
      '',
      `${ACCOUNT_CALLBACK_PATH}?return=%2Faccount%2Fsessions#authn_error=conflict`,
    )
    renderCallback()

    await screen.findByRole('alert')
    await userEvent
      .setup()
      .click(
        screen.getByRole('button', { name: bindingEnUS.backToAccount ?? '' }),
      )
    await waitFor(() =>
      expect(window.location.pathname).toBe(ACCOUNT_SECTION_PATHS.sessions),
    )
  })

  it('ignores a source that names another origin', async () => {
    window.history.replaceState(
      {},
      '',
      `${ACCOUNT_CALLBACK_PATH}?return=https%3A%2F%2Felsewhere.example#authn_error=conflict`,
    )
    renderCallback()

    await screen.findByRole('alert')
    await userEvent
      .setup()
      .click(
        screen.getByRole('button', { name: bindingEnUS.backToAccount ?? '' }),
      )
    await waitFor(() =>
      expect(window.location.pathname).toBe(ACCOUNT_SECTION_PATHS.security),
    )
  })

  it('reads the flow back when the route carries no refusal', async () => {
    window.history.replaceState({}, '', ACCOUNT_CALLBACK_PATH)
    const { readFlow } = renderCallback()

    await waitFor(() => expect(readFlow).toHaveBeenCalled())
  })
})

describe('AccountSettingsPage block consistency', () => {
  function patchedBackend(patch: Record<string, unknown>): RequestFn {
    const backend = createBackend('data')
    return (async (path: string, requestOptions?: RequestOptions) => {
      const answer = await backend(path, requestOptions)
      if (
        path === '/api/v1/auth/me' &&
        (requestOptions?.method ?? 'GET') === 'GET'
      ) {
        return { ...(answer as Record<string, unknown>), ...patch }
      }
      return answer
    }) as RequestFn
  }

  it('puts the sessions bulk action on the block title row, not in the list', async () => {
    const backend = createBackend('data')
    const twoSessions = (async (
      path: string,
      requestOptions?: RequestOptions,
    ) => {
      if (
        path === '/api/v1/auth/sessions' &&
        (requestOptions?.method ?? 'GET') === 'GET'
      ) {
        const one = {
          kind: 'browser',
          created_at: '2026-01-01T00:00:00Z',
          last_seen_at: '2026-01-01T00:00:00Z',
          expires_at: '2026-02-01T00:00:00Z',
          client_ip: '127.0.0.1',
          user_agent: 'test',
        }
        return {
          sessions: [
            { ...one, id: 'session-1', current: true },
            { ...one, id: 'session-2', current: false },
          ],
        }
      }
      return backend(path, requestOptions)
    }) as RequestFn
    renderAccount('?section=sessions', { backend: twoSessions })

    const bulk = await screen.findByRole('button', {
      name: accountEnUS.sessions.revokeOthers.label,
    })
    const block = screen.getByRole('region', {
      name: demoEnUS.account.blocks.devices,
    })
    expect(
      within(block).getByTestId('sessions-block-action'),
    ).toContainElement(bulk)
    const list = screen.getByRole('region', {
      name: accountEnUS.sessions.title,
    })
    expect(
      within(list).queryByRole('button', {
        name: accountEnUS.sessions.revokeOthers.label,
      }),
    ).toBeNull()
  })

  it('keeps the email add action out while the account is still pending proof', async () => {
    renderAccount('?section=profile', {
      features: ['authn.identifiers.email'],
    })
    await screen.findByRole('region', {
      name: demoEnUS.account.rows.label.email,
    })
    await waitFor(() =>
      expect(
        screen.queryByRole('button', { name: demoEnUS.account.action.add }),
      ).toBeNull(),
    )
  })

  it('removes the phone through the row\'s direct delete button and its confirm', async () => {
    const backend = patchedBackend({
      identifiers: [
        {
          id: 'id-phone',
          kind: 'phone',
          value: '+14155550123',
          proven: true,
          contact: true,
          default_contact: false,
          sign_in: false,
          verified_at: '2026-01-01T00:00:00Z',
          verified_via: 'sms-code',
        },
      ],
      verification: { state: 'none', email: '', since: null },
    })
    renderAccount('?section=profile', {
      features: ['authn.phone'],
      backend,
    })
    const phone = await screen.findByRole('region', {
      name: demoEnUS.account.rows.label.phone,
    })
    const remove = await within(phone).findByRole('button', {
      name: demoEnUS.account.action.delete,
    })
    expect(within(phone).getByRole('table')).toContainElement(remove)
    expect(
      within(phone)
        .queryAllByRole('button')
        .some((button) => button.hasAttribute('aria-haspopup')),
    ).toBe(false)
    const user = userEvent.setup()
    await user.click(remove)
    const dialog = await screen.findByRole('dialog')
    expect(dialog).toHaveTextContent(demoEnUS.account.phoneRemove.removeTitle)
    await user.click(
      within(dialog).getByRole('button', {
        name: demoEnUS.account.phoneRemove.removeConfirm,
      }),
    )
    await waitFor(() =>
      expect(
        calls.some(
          (call) =>
            call.method === 'DELETE' &&
            call.path === '/api/v1/auth/identifiers/id-phone',
        ),
      ).toBe(true),
    )
  })

  function refusedPhoneDeleteBackend(
    code = 'authn.unavailable',
  ): RequestFn {
    const backend = patchedBackend({
      identifiers: [
        {
          id: 'id-phone',
          kind: 'phone',
          value: '+14155550123',
          proven: true,
          contact: true,
          default_contact: false,
          sign_in: false,
          verified_at: '2026-01-01T00:00:00Z',
          verified_via: 'sms-code',
        },
      ],
      verification: { state: 'none', email: '', since: null },
    })
    return (async (path: string, requestOptions?: RequestOptions) => {
      if (
        path === '/api/v1/auth/identifiers/id-phone' &&
        requestOptions?.method === 'DELETE'
      ) {
        throw Object.assign(new Error('refused'), {
          status: 503,
          code,
        })
      }
      return backend(path, requestOptions)
    }) as RequestFn
  }

  async function refusePhoneRemoval(
    user: ReturnType<typeof userEvent.setup>,
  ): Promise<HTMLElement> {
    const phone = await screen.findByRole('region', {
      name: demoEnUS.account.rows.label.phone,
    })
    await user.click(
      await within(phone).findByRole('button', {
        name: demoEnUS.account.action.delete,
      }),
    )
    const dialog = await screen.findByRole('dialog')
    await user.click(
      within(dialog).getByRole('button', {
        name: demoEnUS.account.phoneRemove.removeConfirm,
      }),
    )
    return within(phone).findByRole('alert')
  }

  it('reports a refused phone removal in the block\'s one result region', async () => {
    renderAccount('?section=profile', {
      features: ['authn.phone'],
      backend: refusedPhoneDeleteBackend(),
    })
    const user = userEvent.setup()

    const alert = await refusePhoneRemoval(user)
    const sentence = accountEnUS.errors.authn.unavailable ?? ''
    expect(sentence).not.toBe('')
    expect(alert).toHaveTextContent(sentence)
  })

  it('renders the unknown sentence for a removal code the bundle lacks', async () => {
    renderAccount('?section=profile', {
      features: ['authn.phone'],
      backend: refusedPhoneDeleteBackend('authn.not_in_the_bundle'),
    })
    const user = userEvent.setup()

    const alert = await refusePhoneRemoval(user)
    expect(alert).toHaveTextContent(accountEnUS.errors.unknown)
    expect(alert).not.toHaveTextContent('errors.')
  })

  const PHONE_IDENTIFIER = {
    id: 'id-phone',
    kind: 'phone',
    value: '+14155550123',
    proven: true,
    contact: true,
    default_contact: false,
    sign_in: false,
    verified_at: '2026-01-01T00:00:00Z',
    verified_via: 'sms-code',
  } as const

  function phoneDeleteBackend(
    options: {
      readonly holdDelete?: boolean
      readonly holdRefresh?: boolean
    } = {},
  ): {
    readonly request: RequestFn
    readonly releaseDelete: () => void
    readonly releaseRefresh: () => void
  } {
    const backend = patchedBackend({
      identifiers: [PHONE_IDENTIFIER],
      verification: { state: 'none', email: '', since: null },
    })
    let deleted = false
    let releaseDelete = (): void => undefined
    const deleteHeld = new Promise<void>((resolve) => {
      releaseDelete = resolve
    })
    let releaseRefresh = (): void => undefined
    const refreshHeld = new Promise<void>((resolve) => {
      releaseRefresh = resolve
    })
    const request = (async (
      path: string,
      requestOptions?: RequestOptions,
    ) => {
      const method = requestOptions?.method ?? 'GET'
      if (path === '/api/v1/auth/identifiers/id-phone' && method === 'DELETE') {
        if (options.holdDelete === true) {
          await deleteHeld
        }
        deleted = true
        return undefined
      }
      if (path === '/api/v1/auth/me' && method === 'GET' && deleted) {
        if (options.holdRefresh === true) {
          await refreshHeld
        }
        const answer = (await backend(path, requestOptions)) as Record<
          string,
          unknown
        >
        return { ...answer, identifiers: [] }
      }
      return backend(path, requestOptions)
    }) as RequestFn
    return {
      request,
      releaseDelete: () => releaseDelete(),
      releaseRefresh: () => releaseRefresh(),
    }
  }

  function phoneDeletes() {
    return calls.filter(
      (call) =>
        call.method === 'DELETE' &&
        call.path === '/api/v1/auth/identifiers/id-phone',
    )
  }

  async function confirmPhoneRemoval(
    user: ReturnType<typeof userEvent.setup>,
  ): Promise<{ readonly remove: HTMLElement; readonly dialog: HTMLElement }> {
    const phone = await screen.findByRole('region', {
      name: demoEnUS.account.rows.label.phone,
    })
    const remove = await within(phone).findByRole('button', {
      name: demoEnUS.account.action.delete,
    })
    await user.click(remove)
    const dialog = await screen.findByRole('dialog')
    await user.click(
      within(dialog).getByRole('button', {
        name: demoEnUS.account.phoneRemove.removeConfirm,
      }),
    )
    return { remove, dialog }
  }

  it('marks the phone delete control busy and holds the dialog cancel without a reason', async () => {
    const user = userEvent.setup()
    const backend = phoneDeleteBackend({ holdDelete: true })
    renderAccount('?section=profile', {
      features: ['authn.phone'],
      backend: backend.request,
    })

    const { remove, dialog } = await confirmPhoneRemoval(user)
    await waitFor(() => expect(remove).toHaveAttribute('aria-busy', 'true'))
    expect(remove).toHaveAttribute('aria-disabled', 'true')
    const cancel = within(dialog).getByRole('button', { name: CANCEL_LABEL })
    expect(cancel).toHaveAttribute('aria-disabled', 'true')
    expect(cancel).not.toHaveAttribute('aria-busy')
    expect(cancel).not.toHaveAccessibleDescription()
    await user.click(cancel)
    cancel.focus()
    await user.keyboard('{Enter}')
    await user.keyboard(' ')
    expect(screen.getByRole('dialog')).toBeInTheDocument()
    expect(phoneDeletes()).toHaveLength(1)

    await act(async () => {
      backend.releaseDelete()
    })
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  })

  it('returns focus to the delete control when the confirmation is cancelled', async () => {
    const user = userEvent.setup()
    const backend = phoneDeleteBackend()
    renderAccount('?section=profile', {
      features: ['authn.phone'],
      backend: backend.request,
    })

    const phone = await screen.findByRole('region', {
      name: demoEnUS.account.rows.label.phone,
    })
    const remove = await within(phone).findByRole('button', {
      name: demoEnUS.account.action.delete,
    })
    await user.click(remove)
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: CANCEL_LABEL }))

    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(remove).toHaveFocus()
    expect(phoneDeletes()).toHaveLength(0)
  })

  it('lands focus in the phone block when the removed row takes its delete control', async () => {
    const user = userEvent.setup()
    const backend = phoneDeleteBackend({ holdRefresh: true })
    renderAccount('?section=profile', {
      features: ['authn.phone'],
      backend: backend.request,
    })

    const phone = await screen.findByRole('region', {
      name: demoEnUS.account.rows.label.phone,
    })
    const remove = await within(phone).findByRole('button', {
      name: demoEnUS.account.action.delete,
    })
    await user.click(remove)
    const dialog = await screen.findByRole('dialog')
    await user.click(
      within(dialog).getByRole('button', {
        name: demoEnUS.account.phoneRemove.removeConfirm,
      }),
    )

    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(remove).toHaveFocus()

    await act(async () => {
      backend.releaseRefresh()
    })
    await waitFor(() => expect(remove).not.toBeInTheDocument())
    expect(phone).toHaveFocus()
    expect(phone).toHaveAttribute('tabindex', '-1')
  })

  it('leaves focus where the operator put it while the removed row lands', async () => {
    const user = userEvent.setup()
    const backend = phoneDeleteBackend({ holdRefresh: true })
    renderAccount('?section=profile', {
      features: ['authn.phone', 'authn.identifiers.email'],
      backend: backend.request,
    })

    const phone = await screen.findByRole('region', {
      name: demoEnUS.account.rows.label.phone,
    })
    const remove = await within(phone).findByRole('button', {
      name: demoEnUS.account.action.delete,
    })
    await user.click(remove)
    const dialog = await screen.findByRole('dialog')
    await user.click(
      within(dialog).getByRole('button', {
        name: demoEnUS.account.phoneRemove.removeConfirm,
      }),
    )
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(remove).toHaveFocus()

    const elsewhere = screen.getByRole('button', {
      name: demoEnUS.account.action.add,
    })
    elsewhere.focus()
    expect(elsewhere).toHaveFocus()

    await act(async () => {
      backend.releaseRefresh()
    })
    await waitFor(() => expect(remove).not.toBeInTheDocument())
    expect(elsewhere).toHaveFocus()
  })

  it('leaves focus in an unrelated dialog while the removed row lands', async () => {
    const user = userEvent.setup()
    const backend = phoneDeleteBackend({ holdRefresh: true })
    renderAccount('?section=profile', {
      features: ['authn.phone'],
      backend: backend.request,
    })

    const phone = await screen.findByRole('region', {
      name: demoEnUS.account.rows.label.phone,
    })
    const remove = await within(phone).findByRole('button', {
      name: demoEnUS.account.action.delete,
    })
    await user.click(remove)
    const dialog = await screen.findByRole('dialog')
    await user.click(
      within(dialog).getByRole('button', {
        name: demoEnUS.account.phoneRemove.removeConfirm,
      }),
    )
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(remove).toHaveFocus()

    const otherDialog = document.createElement('div')
    otherDialog.setAttribute('role', 'dialog')
    const inside = document.createElement('button')
    inside.textContent = 'another surface'
    otherDialog.appendChild(inside)
    document.body.appendChild(otherDialog)
    try {
      inside.focus()
      expect(inside).toHaveFocus()

      await act(async () => {
        backend.releaseRefresh()
      })
      await waitFor(() => expect(remove).not.toBeInTheDocument())
      expect(inside).toHaveFocus()
      expect(phone).not.toHaveFocus()
    } finally {
      otherDialog.remove()
    }
  })

  it('lands in the phone block when the row leaves before the confirmation releases focus', async () => {
    const user = userEvent.setup()
    const backend = phoneDeleteBackend()
    renderAccount('?section=profile', {
      features: ['authn.phone'],
      backend: backend.request,
    })

    const phone = await screen.findByRole('region', {
      name: demoEnUS.account.rows.label.phone,
    })
    const { remove } = await confirmPhoneRemoval(user)

    await waitFor(() => expect(remove).not.toBeInTheDocument())
    await waitFor(() => expect(phone).toHaveFocus())
  })

  it('returns focus to the delete control when a removal is refused', async () => {
    const user = userEvent.setup()
    renderAccount('?section=profile', {
      features: ['authn.phone'],
      backend: refusedPhoneDeleteBackend(),
    })

    const phone = await screen.findByRole('region', {
      name: demoEnUS.account.rows.label.phone,
    })
    const remove = await within(phone).findByRole('button', {
      name: demoEnUS.account.action.delete,
    })
    await user.click(remove)
    const dialog = await screen.findByRole('dialog')
    await user.click(
      within(dialog).getByRole('button', {
        name: demoEnUS.account.phoneRemove.removeConfirm,
      }),
    )

    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(remove).toHaveFocus()
    expect(await within(phone).findByRole('alert')).toBeInTheDocument()
  })

  it('clears the phone-removal failure when the visitor leaves the pane', async () => {
    renderAccount('?section=profile', {
      features: ['authn.phone'],
      backend: refusedPhoneDeleteBackend(),
    })
    const user = userEvent.setup()

    expect(await refusePhoneRemoval(user)).toBeInTheDocument()

    await chooseAccountSection(user, demoEnUS.account.sections.security)
    await screen.findByRole('heading', {
      level: 1,
      name: demoEnUS.account.sections.security,
    })
    await chooseAccountSection(user, demoEnUS.account.sections.profile)
    const phone = await screen.findByRole('region', {
      name: demoEnUS.account.rows.label.phone,
    })
    expect(within(phone).queryByRole('alert')).toBeNull()
  })

  function phoneAddBackend(
    options: {
      readonly holdRefresh?: boolean
      readonly bindOnRefresh?: boolean
    } = {},
  ): {
    readonly request: RequestFn
    readonly settle: () => void
    readonly refreshReads: () => number
  } {
    const backend = createBackend('data')
    const bindOnRefresh = options.bindOnRefresh ?? true
    let bound = false
    let refreshReads = 0
    let release = (): void => undefined
    const held = new Promise<void>((resolve) => {
      release = resolve
    })
    const request = (async (path: string, requestOptions?: RequestOptions) => {
      const method = requestOptions?.method ?? 'GET'
      if (path === '/api/v1/auth/phone' && method === 'POST') {
        return { expires_in: 300, resend_after: 60 }
      }
      if (path === '/api/v1/auth/phone/verify' && method === 'POST') {
        bound = true
        return undefined
      }
      if (path === '/api/v1/auth/me' && method === 'GET' && bound) {
        refreshReads += 1
        if (options.holdRefresh === true) {
          await held
        }
        const answer = await backend(path, requestOptions)
        if (!bindOnRefresh) {
          return answer
        }
        const profile = answer as { identifiers?: unknown[] }
        return {
          ...(answer as Record<string, unknown>),
          identifiers: [
            ...(profile.identifiers ?? []),
            {
              id: 'id-phone-new',
              kind: 'phone',
              value: US_E164,
              proven: true,
              contact: true,
              default_contact: false,
              sign_in: false,
              verified_at: '2026-01-01T00:00:00Z',
              verified_via: 'sms-code',
            },
          ],
        }
      }
      return backend(path, requestOptions)
    }) as RequestFn
    return {
      request,
      settle: () => release(),
      refreshReads: () => refreshReads,
    }
  }

  async function addPhone(
    user: ReturnType<typeof userEvent.setup>,
    phone: HTMLElement,
  ): Promise<void> {
    await user.click(
      within(phone).getByRole('button', { name: demoEnUS.account.action.add }),
    )
    await user.type(
      await within(phone).findByLabelText(accountEnUS.phone.phone.label, {
        selector: 'input',
      }),
      US_NATIONAL,
    )
    await user.click(
      within(phone).getByRole('button', { name: accountEnUS.phone.send }),
    )
    await user.type(
      await within(phone).findByLabelText(accountEnUS.phone.code.label),
      '123456',
    )
    await user.click(
      within(phone).getByRole('button', { name: accountEnUS.phone.verify }),
    )
  }

  it('closes the add form and confirms the number once the phone is verified', async () => {
    renderAccount('?section=profile', {
      features: ['authn.phone'],
      backend: phoneAddBackend().request,
    })
    const user = userEvent.setup()
    const phone = await screen.findByRole('region', {
      name: demoEnUS.account.rows.label.phone,
    })
    expect(within(phone).getByRole('status')).toBeInTheDocument()

    await addPhone(user, phone)

    const sentence = accountEnUS.phone.added.replace('{{phone}}', US_E164)

    await waitFor(() =>
      expect(
        within(phone).queryByRole('button', { name: accountEnUS.phone.send }),
      ).toBeNull(),
    )
    expect(
      within(phone).queryByLabelText(accountEnUS.phone.phone.label),
    ).toBeNull()
    expect(await within(phone).findByText(US_E164)).toBeInTheDocument()
    await waitFor(() =>
      expect(within(phone).getByRole('status')).toHaveTextContent(sentence),
    )
    await act(async () => {})
    expect(within(phone).getAllByText(sentence)).toHaveLength(1)
    expect(
      within(phone).queryByText(demoEnUS.account.rows.value.notBound),
    ).toBeNull()
  })

  it('holds the add form and the placeholder back while the refresh after verify is in flight', async () => {
    const backend = phoneAddBackend({ holdRefresh: true })
    renderAccount('?section=profile', {
      features: ['authn.phone'],
      backend: backend.request,
    })
    const user = userEvent.setup()
    const phone = await screen.findByRole('region', {
      name: demoEnUS.account.rows.label.phone,
    })

    await addPhone(user, phone)

    await waitFor(() => expect(backend.refreshReads()).toBeGreaterThanOrEqual(1))

    const sentence = accountEnUS.phone.added.replace('{{phone}}', US_E164)

    expect(await within(phone).findByText(sentence)).toBeInTheDocument()
    expect(
      within(phone).queryByRole('button', { name: accountEnUS.phone.send }),
    ).toBeNull()
    expect(
      within(phone).queryByText(demoEnUS.account.rows.value.notBound),
    ).toBeNull()

    await act(async () => {
      backend.settle()
      await Promise.resolve()
    })
    expect(await within(phone).findByText(US_E164)).toBeInTheDocument()
    await act(async () => {})
    expect(within(phone).getAllByText(sentence)).toHaveLength(1)
    expect(
      within(phone).queryByText(demoEnUS.account.rows.value.notBound),
    ).toBeNull()
  })

  it('clears the phone confirmation when a sibling write on the pane succeeds', async () => {
    renderAccount('?section=profile', {
      features: ['authn.phone'],
      backend: phoneAddBackend().request,
    })
    const user = userEvent.setup()
    const phone = await screen.findByRole('region', {
      name: demoEnUS.account.rows.label.phone,
    })

    await addPhone(user, phone)
    const sentence = accountEnUS.phone.added.replace('{{phone}}', US_E164)
    await waitFor(() =>
      expect(within(phone).getByRole('status')).toHaveTextContent(sentence),
    )

    const nameField = screen.getByRole('textbox', {
      name: accountEnUS.profile.displayName.label,
    })
    await user.clear(nameField)
    await user.type(nameField, 'Renamed User')
    await user.tab()

    await screen.findByText(accountEnUS.profile.saved)
    expect(within(phone).queryByText(sentence)).toBeNull()
  })

  it('keeps the placeholder back while a confirmation stands on a stale read', async () => {
    renderAccount('?section=profile', {
      features: ['authn.phone'],
      backend: phoneAddBackend({ bindOnRefresh: false }).request,
    })
    const user = userEvent.setup()
    const phone = await screen.findByRole('region', {
      name: demoEnUS.account.rows.label.phone,
    })

    await addPhone(user, phone)

    expect(
      await within(phone).findByText(
        accountEnUS.phone.added.replace('{{phone}}', US_E164),
      ),
    ).toBeInTheDocument()
    expect(within(phone).queryByText(US_E164)).toBeNull()
    expect(
      within(phone).queryByText(demoEnUS.account.rows.value.notBound),
    ).toBeNull()
    expect(
      within(phone).queryByRole('button', { name: accountEnUS.phone.send }),
    ).toBeNull()
  })

  it('gives block titles the card heading role above their row labels', async () => {
    renderAccount('?section=security', { features: ['authn.mfa'] })
    const title = await screen.findByRole('heading', {
      level: 2,
      name: demoEnUS.account.rows.label.loginWays,
    })
    expect(title).toHaveClass('MuiTypography-h5')
  })

  it('paints every security block icon with the same color', async () => {
    renderAccount('?section=security', { features: ['authn.mfa'] })
    const password = await screen.findByRole('region', {
      name: demoEnUS.account.rows.label.password,
    })
    const loginWays = screen.getByRole('region', {
      name: demoEnUS.account.rows.label.loginWays,
    })
    const passwordIcon = password.querySelector('svg')?.parentElement
    const loginWaysIcon = loginWays.querySelector('svg')?.parentElement
    expect(passwordIcon).not.toBeNull()
    expect(loginWaysIcon).not.toBeNull()
    expect(getComputedStyle(loginWaysIcon as Element).color).toBe(
      getComputedStyle(passwordIcon as Element).color,
    )
  })
})
