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
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { accountUiResources } from '@speed/account-ui'
import { bindRequestFn } from '@speed/api-sdk/runtime'
import type { RequestFn, RequestOptions } from '@speed/api-client'
import { createBackend } from '../a11y/backends.js'
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

const ADD_PATH = '/api/v1/auth/identifiers'
const accountEnUS = accountUiResources['en-US'] as {
  readonly verification: Readonly<Record<string, string>>
  readonly bindingCallback: Readonly<Record<string, string>>
  readonly profile: {
    readonly title: string
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
  readonly appearance: { readonly theme: { readonly label: string } }
  readonly preferences: {
    readonly language: { readonly label: string }
    readonly timezone: { readonly label: string }
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
    readonly actionsAria: string
    readonly axes: {
      readonly signIn: string
      readonly defaultContact: string
    }
    readonly action: { readonly enable: string; readonly disable: string }
  }
}
const bindingEnUS = accountEnUS.bindingCallback

/** The calls the page issued in the current case, in order. */
let calls: { readonly method: string; readonly path: string }[] = []

beforeEach(() => {
  window.history.replaceState({}, '', '/')
  calls = []
})

/** The send calls the page issued so far. */
function sends() {
  return calls.filter((call) => call.method === 'POST' && call.path === ADD_PATH)
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
    calls.push({ method: requestOptions?.method ?? 'GET', path })
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
    ),
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
 * group address lands on the profile pane by default, a navigation entry
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
    await user.click(
      screen.getByRole('link', { name: demoEnUS.account.sections.security }),
    )
    await user.click(await screen.findByTestId('security-row-password'))

    expect(
      await screen.findByLabelText(accountEnUS.password.current.label),
    ).toBeInTheDocument()
    expect(
      screen.queryByLabelText(accountEnUS.profile.displayName.label),
    ).toBeNull()
  })

  it('lists the sign-in methods as rows with one action each', async () => {
    renderAccount('?section=security', { features: ['authn.mfa'] })

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

  it('shows the sign-in ways on the pane, without the contact axis', async () => {
    renderAccount('?section=security')

    // The email group carries the pane's enable/disable action; a username
    // is always a login name and offers none. The pane adds no new identifier
    // here.
    expect(
      await screen.findByRole('button', {
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
    // default-contact position only the contact side offers.
    expect(
      await screen.findByText(accountEnUS.identifiers.axes.defaultContact),
    ).toBeInTheDocument()
    expect(
      screen.queryByText(accountEnUS.identifiers.axes.signIn),
    ).toBeNull()
  })

  it('renders the preference controls directly', async () => {
    renderAccount('?section=preferences')

    // The appearance block is one card; its three controls render in place,
    // with no section to open first.
    const block = await screen.findByRole('region', {
      name: demoEnUS.account.blocks.appearance,
    })
    expect(
      within(block).getByText(demoEnUS.account.rows.label.theme),
    ).toBeInTheDocument()
    for (const name of [
      accountEnUS.appearance.theme.label,
      accountEnUS.preferences.language.label,
      accountEnUS.preferences.timezone.label,
    ]) {
      expect(
        await within(block).findByRole('combobox', { name }),
      ).toBeInTheDocument()
    }
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

  it('words a refused link from the fragment and returns to the account page', async () => {
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
    // The account's group address lands on its first pane.
    await waitFor(() =>
      expect(window.location.pathname).toBe(ACCOUNT_SECTION_PATHS.profile),
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

  it('keeps the phone removal behind the number row\'s trailing menu and its confirm', async () => {
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
      verification: { state: 'none', email: '', since: '' },
    })
    renderAccount('?section=profile', {
      features: ['authn.phone'],
      backend,
    })
    const phone = await screen.findByRole('region', {
      name: demoEnUS.account.rows.label.phone,
    })
    // The number's actions are one overflow menu button on the row, the
    // same form the email card's contact rows carry -- not an inline
    // delete button.
    const actions = await within(phone).findByRole('button', {
      name: accountEnUS.identifiers.actionsAria.replace(
        '{{value}}',
        '+14155550123',
      ),
    })
    expect(within(phone).getByRole('table')).toContainElement(actions)
    // The row carries no separate inline delete button beside the menu.
    expect(
      within(phone).queryByRole('button', {
        name: demoEnUS.account.action.delete,
      }),
    ).toBeNull()
    const user = userEvent.setup()
    await user.click(actions)
    await user.click(
      screen.getByRole('menuitem', {
        name: demoEnUS.account.action.delete,
      }),
    )
    const dialog = await screen.findByRole('dialog')
    expect(dialog).toHaveTextContent(demoEnUS.account.phoneRemove.removeTitle)
  })

  it('gives block titles the card heading role above their row labels', async () => {
    renderAccount('?section=security', { features: ['authn.mfa'] })
    const title = await screen.findByRole('heading', {
      level: 2,
      name: demoEnUS.account.rows.label.loginWays,
    })
    // The block title stays on the subtitle1 role, above the row labels
    // inside it.
    expect(title).toHaveClass('MuiTypography-subtitle1')
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
