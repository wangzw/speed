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

import { act, fireEvent, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { accountUiResources } from '@speed/account-ui'
import { bindRequestFn } from '@speed/api-sdk/runtime'
import type { RequestFn, RequestOptions } from '@speed/api-client'
import { createBackend } from '../a11y/backends.js'
import demoEnUS from '../locales/en-US.json' with { type: 'json' }
import { ACCOUNT_CALLBACK_PATH, ACCOUNT_PATH } from '../paths.js'
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

/** Renders the account page at ACCOUNT_PATH plus the landing's query. */
function renderAccount(search = ''): void {
  const backend = createBackend('data')
  const request = (async <T,>(
    path: string,
    options?: RequestOptions,
  ): Promise<T> => {
    calls.push({ method: options?.method ?? 'GET', path })
    return (await backend(path, options)) as T
  }) as RequestFn
  bindRequestFn(request)
  window.history.replaceState({}, '', `${ACCOUNT_PATH}${search}`)
  renderDemoApp({
    session: createTestSession(
      authenticatedSnapshot({ features: readyFeatures() }),
    ),
  })
}

describe('AccountSettingsPage', () => {
  it('does not send a verification code on an ordinary visit', async () => {
    // The data-mode profile is a pending registration with a target address,
    // so the verification section is on the page; an ordinary visit carries
    // no landing flag, which keeps it quiet.
    renderAccount()

    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: demoEnUS.account.title,
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
    // The landing sends once and the profile refetch that follows sends none.
    await waitFor(() => expect(sends()).toHaveLength(1))
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 0))
    })
    expect(sends()).toHaveLength(1)
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
    expect(
      screen.queryByRole('button', { name: bindingEnUS.retry ?? '' }),
    ).toBeNull()

    await userEvent
      .setup()
      .click(
        screen.getByRole('button', { name: bindingEnUS.backToAccount ?? '' }),
      )
    await waitFor(() => expect(window.location.pathname).toBe(ACCOUNT_PATH))
  })

  it('reads the flow back when the route carries no refusal', async () => {
    window.history.replaceState({}, '', ACCOUNT_CALLBACK_PATH)
    const { readFlow } = renderCallback()

    await waitFor(() => expect(readFlow).toHaveBeenCalled())
  })
})
