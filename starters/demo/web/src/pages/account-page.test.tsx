/**
 * The account page's own landing behaviour, and the social-binding
 * callback's refusal landing.
 *
 * The account page hosts account-ui's ContactVerificationSection, so email
 * verification is completed inside the profile rather than on a page of its
 * own. An ordinary visit leaves the section quiet; `?verify=1` (the banner's
 * verify action and the post-registration landing) sends one code on arrival;
 * `?change=email` starts in the change-address form and never sends on its own,
 * even after that form is closed.
 *
 * The binding callback route words a refused round trip from the code the
 * server put in the URL fragment: it reads no flow for it (the server already
 * consumed the flow), offers no repeat of the round trip, and its only action
 * returns to the account page. Without a fragment it still reads the flow
 * back, the way an accepted round trip lands.
 */

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
import { useAppThemeMode } from '@speed/ui-kit'
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

/** The deployment's method document with one social provider, for the
 * cases that expect the social card. */
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
/** The number the phone cases type into the local field and the E.164 form
 * the control assembles from it (jsdom's locale resolves to the US). */
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

/** The calls the page issued in the current case, in order. */
let calls: {
  readonly method: string
  readonly path: string
  readonly body?: unknown
}[] = []

/** The theme provider's resolution, recorded by the probe below. */
const themeProbe: {
  mode: string | null
  preference: string | null
} = { mode: null, preference: null }

/** Reads the provider's resolved mode and preference from inside the tree. */
function ThemeModeProbe(): null {
  const { mode, preference } = useAppThemeMode()
  themeProbe.mode = mode
  themeProbe.preference = preference
  return null
}

/** Mounts the probe beside the app, inside the same provider stack. */
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

/** The send calls the page issued so far. */
function sends() {
  return calls.filter((call) => call.method === 'POST' && call.path === ADD_PATH)
}

/**
 * Switches the pane through the account entry's menu, the surface the shell
 * offers for the account settings now that the navigation column carries the
 * home entry alone.
 */
async function chooseAccountSection(
  user: ReturnType<typeof userEvent.setup>,
  name: string,
): Promise<void> {
  await user.click(await screen.findByTestId('account-entry-trigger'))
  await user.click(await screen.findByRole('menuitem', { name }))
}

interface RenderAccountOptions {
  /** The feature names this deployment serves. Defaults to none. */
  readonly features?: readonly string[]
  /** The scripted reads, when a case needs an answer the canned data lacks. */
  readonly backend?: RequestFn
  /**
   * The page to open, when a case targets a pane rather than the group
   * address. Defaults to ACCOUNT_PATH, which lands on a pane through the
   * group address's redirect.
   */
  readonly path?: string
  /** The theme options the provider stack starts with (src/main.tsx's boot
   * options in production). */
  readonly theme?: SpeedAppThemeOptions
  /** A wrapper rendered inside the provider tree, for probes. */
  readonly wrap?: (app: ReactElement) => ReactNode
  /** The deployment's method document, when a case needs redirect
   * providers or a failing read. */
  readonly methods?: AuthnMethodList | (() => Promise<AuthnMethodList>)
}

/** Renders the account page at ACCOUNT_PATH plus the landing's query. */
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
    // The data-mode profile is a pending registration with a target address,
    // so the verification section is on the profile pane's email card; an
    // ordinary visit carries no landing flag, which keeps it quiet.
    renderAccount()

    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: demoEnUS.account.sections.profile,
      }),
    ).toBeInTheDocument()
    // The verification surface is on the pane itself: an ordinary visit
    // reaches it without opening anything.
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
    // The landing sends once and the profile refetch that follows sends none.
    await waitFor(() => expect(sends()).toHaveLength(1))
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 0))
    })
    expect(sends()).toHaveLength(1)
  })

  it('words a refused step-up round trip that landed here', async () => {
    // A step-up flow started from this page sends the page as its
    // `return_to`: the provider's refusal lands back on the pane with its
    // code in the fragment, and this page is the surface that words it.
    renderAccount('#authn_error=login-failed', {
      path: ACCOUNT_SECTION_PATHS.profile,
    })

    const errors = (accountUiResources['en-US'] as {
      readonly errors: { readonly authn: Readonly<Record<string, string>> }
    }).errors
    const text = errors.authn.login_failed ?? ''
    const notice = await screen.findByText(text)
    // The page itself is the landing surface: its notice sits with the page
    // content, under the page heading, not in the shell's banner above it.
    const heading = screen.getByRole('heading', {
      level: 1,
      name: demoEnUS.account.sections.profile,
    })
    expect(
      heading.compareDocumentPosition(notice) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy()
    // The fragment is one-shot: a reload does not repeat the notice.
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

/**
 * The settings layout: one pane per route, each a page of its own. The
 * group address lands on the profile pane by default, an account-menu entry
 * swaps the pane, and a verification landing opens on the profile pane.
 */
describe('AccountSettingsPage sections', () => {
  it('opens the profile pane by default, with the display name editable in place', async () => {
    renderAccount()

    // The account's group address lands on the profile pane, whose h1 names
    // the pane.
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
    // The password and the identifiers are rows; the two-step block and the
    // social bindings are stated sections that never collapse.
    expect(screen.getByTestId('security-row-password')).toBeInTheDocument()
    expect(screen.queryByTestId('security-row-mfa')).toBeNull()
    expect(screen.queryByTestId('security-row-social')).toBeNull()
    expect(screen.queryByTestId('security-row-identifiers')).toBeNull()
  })

  it('does not render the social card when the deployment serves no social sign-in', async () => {
    renderAccount('?section=security', { features: ['authn.mfa'] })

    // The password card proves the pane rendered; the social card is absent
    // whole -- no title, no empty state -- because the assembly carries no
    // redirect method and the account holds no binding.
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

    // The card is the answer to "what could I link": with a provider and no
    // binding it renders that provider's row, not an empty placeholder.
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

    // The deployment's document is unreadable, but the account's own binding
    // still renders: a failed read never hides what the user already has.
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

    // The email group carries the pane's enable/disable action; a username
    // is always a login name and offers none. The pane adds no new identifier
    // here.
    expect(
      await screen.findByRole('switch', {
        name: accountEnUS.identifiers.action.disable,
      }),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: accountEnUS.identifiers.add.open }),
    ).toBeNull()
    // The email sign-in way picks the chosen address from its own select.
    expect(
      await screen.findByRole('combobox', {
        name: accountEnUS.identifiers.kind.email,
      }),
    ).toBeInTheDocument()
    expect(
      screen.queryByText(accountEnUS.identifiers.axes.defaultContact),
    ).toBeNull()
    // Three categories, one per kind of login name.
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

    // The contact axis lists the address in its own table, carrying the
    // default-contact position only the contact side offers. The row's chip
    // states the position; the row offers no set-default action for one it
    // already holds.
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

    // The appearance block is one card; its four controls render in place,
    // with no section to open first.
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

    // The language write switches the instance, so its confirmation and the
    // rest of the page speak zh-CN.
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
    // The page carries one result: the earlier language confirmation is
    // gone the moment the time zone attempt settles.
    expect(screen.queryByText(languageSaved, { exact: true })).toBeNull()
  })

  it('applies a color-mode choice at once, writes the device key and sends only color_mode', async () => {
    // The account has never saved a mode, so the control reads the provider's
    // current preference; the suite boots at light.
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

    // The confirmed write lands the preference in the provider and mirrors it
    // to the device, so the next boot starts dark.
    await waitFor(() => expect(themeProbe.mode).toBe('dark'))
    expect(themeProbe.preference).toBe('dark')
    expect(window.localStorage.getItem(COLOR_MODE_STORAGE_KEY)).toBe('dark')

    // One write, carrying the account's color_mode alone: the theme preset is
    // a device-level choice and sends no PATCH of its own.
    const patches = calls.filter(
      (call) => call.method === 'PATCH' && call.path === '/api/v1/auth/me',
    )
    expect(patches).toHaveLength(1)
    expect(patches[0]?.body).toEqual({ color_mode: 'dark' })
    // The control shows the applied choice once the write's invalidation has
    // converged.
    await waitFor(() =>
      expect(select).toHaveTextContent(
        accountEnUS.appearance.colorMode.options.dark,
      ),
    )
  })

  it('resolves a stored system preference to the dark palette', async () => {
    // A device whose stored preference is 'system', and a matchMedia stub
    // that reports the dark query as matching (the suite default): the boot
    // preference is 'system' and the provider resolves it to dark.
    window.localStorage.setItem(COLOR_MODE_STORAGE_KEY, 'system')
    renderAccount('?section=preferences', {
      theme: { defaultMode: initialColorModePreference() },
      wrap: withThemeModeProbe,
    })

    const select = await screen.findByRole('combobox', {
      name: accountEnUS.appearance.colorMode.label,
    })
    // The account has saved no mode, so the control shows the preference in
    // effect.
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

    // The username and display name share the identity block; the email and
    // phone are blocks of their own, in that order.
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
    // The display name saves itself; the identity block renders no submit
    // control.
    expect(within(identity).queryByRole('button')).toBeNull()
  })
})

/**
 * The binding callback route's refusal: a round trip the server refused
 * lands here with its problem code in the URL fragment, and the page hands
 * it to the handler instead of reading a flow the server already consumed.
 */
describe('AccountBindingCallbackPage', () => {
  /** Renders the callback route over the app's scripts, counting flow reads. */
  function renderCallback() {
    bindRequestFn(createBackend('data'))
    const session = createTestSession(authenticatedSnapshot())
    const readFlow = vi.fn(session.readFlow)
    session.readFlow = readFlow
    renderDemoApp({ session })
    return { readFlow }
  }

  it('returns to the security pane when the callback names no source', async () => {
    // The callback route carries the pane it started from in its `return`
    // query. Without one the safe landing is the security pane -- where the
    // binding surface lives -- not the group address's first (profile) pane.
    window.history.replaceState(
      {},
      '',
      `${ACCOUNT_CALLBACK_PATH}#authn_error=conflict`,
    )
    const { readFlow } = renderCallback()

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(bindingEnUS.conflict ?? '')
    // The server consumed the flow: the refusal is not read as one.
    expect(readFlow).not.toHaveBeenCalled()
    // The fragment is one-shot: the reload that follows repeats nothing.
    expect(window.location.hash).toBe('')
    // The page consumed the fragment, so the shell's fallback notice never
    // appears beside it.
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
    // The link flow carried `/account/security` inside its return_to, and the
    // server landed the refused round trip back on the callback route with
    // that source in the query. The callback returns the viewer there.
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
    // A crafted `return` naming another origin is not a site path, so the
    // callback falls back to its safe landing instead of navigating there.
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
  /** The data-mode backend with one profile field replaced. */
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
    // The data profile signs in on one device only; a second live session
    // is the state that renders the bulk action at all.
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
    // The block's title-row slot holds the action ...
    const block = screen.getByRole('region', {
      name: demoEnUS.account.blocks.devices,
    })
    expect(
      within(block).getByTestId('sessions-block-action'),
    ).toContainElement(bulk)
    // ... and the session list does not: no action row of its own.
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
    // The data profile is pending; the deployment does serve the email
    // channel. The button would open nothing, so it is not rendered.
    renderAccount('?section=profile', {
      features: ['authn.identifiers.email'],
    })
    await screen.findByRole('region', {
      name: demoEnUS.account.rows.label.email,
    })
    // The profile read settles to "pending"; the button goes with it.
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
          verified_via: 'sms',
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
    // The number's action is the delete button itself, on the row, not an
    // overflow menu trigger hiding it.
    const remove = await within(phone).findByRole('button', {
      name: demoEnUS.account.action.delete,
    })
    expect(within(phone).getByRole('table')).toContainElement(remove)
    // No menu trigger remains on the row.
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
    // The same confirm drives the same protected delete.
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

  /** The profile with one phone number, whose deletion the server refuses
   * with the given problem code. */
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
          verified_via: 'sms',
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

  /** Remove the bound phone and return the block's one result region. */
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
    // The same region that confirms an add carries the refusal, worded by
    // the account-ui error vocabulary for the code the backend answered.
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
    // A code without a leaf never renders as a raw resource key.
    expect(alert).not.toHaveTextContent('errors.')
  })

  it('clears the phone-removal failure when the visitor leaves the pane', async () => {
    // The pane's route change reuses this page instance, so the block's
    // result must be settled on leaving rather than carried back to it.
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

  /** The data backend whose profile gains the added number once the verify
   * lands; the two phone writes answer their own paths. `holdRefresh` keeps
   * the profile read that follows a landed verify pending until `settle`;
   * `bindOnRefresh: false` answers that read without the number, as a read
   * that raced the write. `refreshReads` counts the bound profile reads the
   * invalidation issued, so a case can synchronize on the refetch actually
   * starting rather than guessing at timings. */
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
              verified_via: 'sms',
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

  /** Types the number and the code, then submits the verify. */
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
    // The block's success region stands in the tree before there is anything
    // to confirm: a region born together with its text is unreliable. The
    // phone block is the only one with a standing status region here.
    expect(within(phone).getByRole('status')).toBeInTheDocument()

    await addPhone(user, phone)

    const sentence = accountEnUS.phone.added.replace('{{phone}}', US_E164)

    // The add form is gone: neither the number field nor the send action
    // remains in the block.
    await waitFor(() =>
      expect(
        within(phone).queryByRole('button', { name: accountEnUS.phone.send }),
      ).toBeNull(),
    )
    expect(
      within(phone).queryByLabelText(accountEnUS.phone.phone.label),
    ).toBeNull()
    // The bound number stands on the block's row ...
    expect(await within(phone).findByText(US_E164)).toBeInTheDocument()
    // ... the region carries the one confirmation that names it ...
    await waitFor(() =>
      expect(within(phone).getByRole('status')).toHaveTextContent(sentence),
    )
    // ... and the sentence appears exactly once once the queue drains.
    await act(async () => {})
    expect(within(phone).getAllByText(sentence)).toHaveLength(1)
    // ... and the not-linked line is gone with the number bound.
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

    // Synchronize on the invalidated profile read actually starting: the
    // verify landed and the refetch is open.
    await waitFor(() => expect(backend.refreshReads()).toBeGreaterThanOrEqual(1))

    const sentence = accountEnUS.phone.added.replace('{{phone}}', US_E164)

    // The confirmation stands while the page has not read the number back:
    // the add form is gone and the block makes no claim about a number it
    // has not seen.
    expect(await within(phone).findByText(sentence)).toBeInTheDocument()
    expect(
      within(phone).queryByRole('button', { name: accountEnUS.phone.send }),
    ).toBeNull()
    expect(
      within(phone).queryByText(demoEnUS.account.rows.value.notBound),
    ).toBeNull()

    // Settling the read lands the number's row; the confirmation still
    // stands once and the placeholder never returns.
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
    // The page carries one result: the add confirmation lives in a feedback
    // scope, so a later success in the identity block clears it. A banner
    // outside the page's coordination would stand beside the new result.
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

    // The identity block saves the display name on blur; its success is the
    // later result on the page.
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
    // The refreshed read answers without the number (it raced the write):
    // the confirmation must not stand beside a not-linked line.
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
    // The card title takes the h5 role (20px), not the same subtitle1 size
    // as the row labels inside it.
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
