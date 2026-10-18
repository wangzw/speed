/**
 * The account setup page's states.
 *
 * The page is the host half of a registration: it reads the account's own
 * profile and decides what the account still needs. A phone alone is not a
 * login name, so the password section stays withheld behind an explanatory
 * line until a username (or a verified email) is present; once the account
 * has both a login name and a password the page states that setup is
 * complete. The tests script the one read the page makes (GET /auth/me) and
 * read their expected copy from the shipped bundles.
 */

import { beforeEach, describe, expect, it } from 'vitest'
import { screen } from '@testing-library/react'
import { bindRequestFn } from '@speed/api-sdk/runtime'
import { accountUiResources } from '@speed/account-ui'
import type { RequestFn } from '@speed/api-client'
import type { AuthnProfileProfile } from '../app-api/index.js'
import demoEnUS from '../locales/en-US.json' with { type: 'json' }
import { ACCOUNT_SETUP_PATH } from '../paths.js'
import { renderDemoApp } from '../test-utils/render.js'
import {
  authenticatedSnapshot,
  createTestSession,
  readyFeatures,
} from '../test-utils/session.js'

/** The account-ui password label the setup page renders after a login name
 * exists, taken from the package's real bundle. */
const accountEn = accountUiResources['en-US'] as unknown as {
  readonly password: { readonly new: { readonly label: string } }
}

/** The one read the page (and both its sections) make. */
function profileOf(
  overrides: Pick<AuthnProfileProfile, 'identifiers' | 'password_set'>,
): AuthnProfileProfile {
  return {
    account_id: 'account-1',
    kind: 'user',
    display_name: 'New Member',
    mfa_required: false,
    password_set: overrides.password_set,
    created_at: '2026-01-01T00:00:00Z',
    attributes: {},
    locale: 'en-US',
    time_zone: 'UTC',
    identifiers: overrides.identifiers,
    links: [],
  }
}

/** Binds a request function that answers the profile read and refuses the
 * rest: the page reads nothing else (the tenant frame's gated reads are
 * disabled by the empty feature set below). */
function bindProfile(profile: AuthnProfileProfile): void {
  const request: RequestFn = async <T,>(path: string): Promise<T> => {
    if (path === '/auth/me') {
      return profile as T
    }
    throw new Error(`the suite binds only the profile read: ${path}`)
  }
  bindRequestFn(request)
}

/** Renders the app on the setup route for an account with an empty feature
 * document (no roster read, no notification stream). */
function renderSetup(): void {
  renderDemoApp({
    session: createTestSession(
      authenticatedSnapshot({ features: readyFeatures() }),
    ),
  })
}

beforeEach(() => {
  window.history.replaceState({}, '', ACCOUNT_SETUP_PATH)
})

describe('the account setup page', () => {
  it('withholds the password section behind an explanation until a login name exists', async () => {
    bindProfile(
      profileOf({
        password_set: false,
        identifiers: [
          {
            id: 'id-1',
            kind: 'phone',
            value: '+14155550123',
            verified_at: '2026-01-01T00:00:00Z',
            verified_via: 'sms-code',
          },
        ],
      }),
    )
    renderSetup()

    // A verified phone signs in but is not a login name: the page says what
    // to add first, and the password form stays out of reach.
    expect(
      await screen.findByText(
        demoEnUS.accountSetup.password.loginNameRequired,
      ),
    ).toBeInTheDocument()
    expect(
      screen.queryByLabelText(accountEn.password.new.label),
    ).not.toBeInTheDocument()
    // The way out is offered, and skipping revokes nothing.
    expect(
      screen.getByRole('link', { name: demoEnUS.accountSetup.later }),
    ).toBeInTheDocument()
  })

  it('offers the password section once the account has a username', async () => {
    bindProfile(
      profileOf({
        password_set: false,
        identifiers: [
          {
            id: 'id-1',
            kind: 'phone',
            value: '+14155550123',
            verified_at: '2026-01-01T00:00:00Z',
            verified_via: 'sms-code',
          },
          {
            id: 'id-2',
            kind: 'username',
            value: 'new-member',
            verified_at: '',
            verified_via: '',
          },
        ],
      }),
    )
    renderSetup()

    expect(
      await screen.findByLabelText(accountEn.password.new.label),
    ).toBeInTheDocument()
    expect(
      screen.queryByText(demoEnUS.accountSetup.password.loginNameRequired),
    ).not.toBeInTheDocument()
  })

  it('states that setup is complete once the account has a login name and a password', async () => {
    bindProfile(
      profileOf({
        password_set: true,
        identifiers: [
          {
            id: 'id-2',
            kind: 'username',
            value: 'new-member',
            verified_at: '',
            verified_via: '',
          },
        ],
      }),
    )
    renderSetup()

    expect(
      await screen.findByText(demoEnUS.accountSetup.complete),
    ).toBeInTheDocument()
    // The header reflects the same state: the page never tells an account
    // that already has a username and a password to add them.
    expect(
      screen.getByRole('heading', {
        level: 1,
        name: demoEnUS.accountSetup.completeTitle,
      }),
    ).toBeInTheDocument()
    expect(
      screen.queryByText(demoEnUS.accountSetup.title),
    ).not.toBeInTheDocument()
    expect(
      screen.queryByText(demoEnUS.accountSetup.description),
    ).not.toBeInTheDocument()
    // The completion point offers the way home instead of deferring.
    expect(
      screen.getByRole('link', { name: demoEnUS.accountSetup.home }),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('link', { name: demoEnUS.accountSetup.later }),
    ).not.toBeInTheDocument()
  })

  it('names the email address when it is the only login name', async () => {
    bindProfile(
      profileOf({
        password_set: true,
        identifiers: [
          {
            id: 'id-3',
            kind: 'email',
            value: 'member@example.com',
            verified_at: '2026-01-01T00:00:00Z',
            verified_via: 'code',
          },
        ],
      }),
    )
    renderSetup()

    expect(
      await screen.findByText(demoEnUS.accountSetup.completeWithEmail),
    ).toBeInTheDocument()
    // The account has no username, so the completion copy must not tell it
    // to sign in with one.
    expect(
      screen.queryByText(demoEnUS.accountSetup.complete),
    ).not.toBeInTheDocument()
  })

  it('treats a verified email as a login name', async () => {
    bindProfile(
      profileOf({
        password_set: false,
        identifiers: [
          {
            id: 'id-3',
            kind: 'email',
            value: 'member@example.com',
            verified_at: '2026-01-01T00:00:00Z',
            verified_via: 'code',
          },
        ],
      }),
    )
    renderSetup()

    expect(
      await screen.findByLabelText(accountEn.password.new.label),
    ).toBeInTheDocument()
  })
})
