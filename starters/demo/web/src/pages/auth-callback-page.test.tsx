/**
 * The auth callback route: its landmark structure and where a completed
 * provider round trip lands.
 *
 * The route renders outside the tenant shell, so the page itself is the
 * document's main landmark -- except when the host's absent view shows: that
 * view is the tenant frame's 404, and the frame renders the main landmark
 * itself. Wrapping it in the page's own main would put two `main` landmarks on
 * one document, a defect a screen reader's landmark list surfaces as a
 * duplicate. The sweep's axe gate runs landmark-no-duplicate-main, but this
 * case pins the structure directly so a regression is named at the route that
 * carries it rather than only in a scan report.
 *
 * The success callback lands the viewer home, unless the provider detour
 * started from an invitation landing: that landing is remembered in the tab
 * before the provider takes over, returned to once, and then cleared.
 */

import { screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it } from 'vitest'
import { authUiResources } from '@speed/auth-ui'
import { bindRequestFn } from '@speed/api-sdk/runtime'
import { openFlow } from '../test-utils/session-harness.js'
import demoEnUS from '../locales/en-US.json' with { type: 'json' }
import { createBackend } from '../a11y/backends.js'
import {
  setCurrentTenantId,
  setTenantAliases,
} from '../chrome.js'
import {
  pendingInvitationLanding,
  rememberInvitationLanding,
} from '../invitation-return.js'
import { AUTH_CALLBACK_PATH } from '../routes.js'
import { methodList } from '../test-utils/session-harness.js'
import { renderDemoApp } from '../test-utils/render.js'
import {
  authenticatedSnapshot,
  createTestSession,
} from '../test-utils/session.js'

/** The tenant the callback route renders over, with the alias the host has
 * recorded for it; the host holds no default roster, so a current tenant must
 * be nameable. */
const TENANT_ID = 'acme'

/** The auth-ui copy the account-opening step renders, read from the bundle
 * the screen renders. */
const authEn = authUiResources['en-US'] as unknown as {
  readonly signInFlow: {
    readonly signupAccountTitle: string
    readonly signupProvider: string
  }
  readonly social: {
    readonly provider: {
      readonly google: string
    }
  }
}

/** An anonymous session whose flow read parks at the social
 * account-opening step. */
function signupSession() {
  return Object.assign(createTestSession(), {
    readFlow: () =>
      Promise.resolve(
        openFlow({
          seq: 1,
          state: 'signup',
          next: {
            kind: 'challenge',
            fields: [
              { name: 'username', kind: 'text', required: true },
              {
                name: 'password',
                kind: 'password',
                secret: true,
                required: true,
              },
            ],
            provided_method: 'google',
            provided_display_name: 'Ada Lovelace',
            provided_email: 'ada@example.com',
          },
        }),
      ),
  })
}

/**
 * An authenticated session whose deployment offers a redirect method. The
 * callback handler's authenticated re-entry consults the discovery document
 * before it fires the host's success callback: a deployment with no method
 * that could have returned here settles on the host's absent view instead.
 */
function providerSession() {
  return Object.assign(createTestSession(authenticatedSnapshot()), {
    listMethods: () =>
      Promise.resolve(
        methodList(
          [
            {
              name: 'github',
              kind: 'redirect',
              protocol: 'oauth2',
              label: 'GitHub',
            },
          ],
          { signup: false },
        ),
      ),
  })
}

beforeEach(() => {
  window.history.replaceState({}, '', AUTH_CALLBACK_PATH)
  window.sessionStorage.clear()
  setCurrentTenantId(TENANT_ID)
  setTenantAliases(new Map([[TENANT_ID, TENANT_ID]]))
})

describe('the auth callback route', () => {
  it("renders exactly one main landmark when the host's absent view shows", async () => {
    bindRequestFn(createBackend('data'))
    // An authenticated session answers the discovery read with the skeleton's
    // password-only document -- no redirect or enterprise method -- so the
    // handler settles on the host's absent view: the tenant frame's 404.
    const { container } = renderDemoApp({
      session: createTestSession(authenticatedSnapshot()),
    })

    expect(
      await screen.findByRole('heading', { level: 1, name: 'Page not found' }),
    ).toBeInTheDocument()
    // The heading is the tenant frame's own content: it is a descendant of the
    // main landmark AppShell renders around its children, so both arrive in
    // the commit that resolves this heading. Nothing after that commit adds or
    // removes a main -- the frame's profile read feeds only the banner region
    // -- so counting here judges the route's structure, and a page root that
    // opened a second main would already be counted.
    expect(container.querySelectorAll('main')).toHaveLength(1)
  })

  it('renders the account-opening step in the sign-in card', async () => {
    // The first social login's account-opening step is not a page of its
    // own: it renders inside the sign-in surface's card, brand and page
    // heading included, with the step supplying only the card's content.
    bindRequestFn(createBackend('data'))
    renderDemoApp({ session: signupSession() })

    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: demoEnUS.signIn.heading,
      }),
    ).toBeInTheDocument()
    expect(
      screen.getByRole('heading', {
        level: 2,
        name: authEn.signInFlow.signupAccountTitle,
      }),
    ).toBeInTheDocument()
    expect(
      screen.getByText(
        authEn.signInFlow.signupProvider.replace(
          '{{provider}}',
          authEn.social.provider.google,
        ),
      ),
    ).toBeInTheDocument()
    expect(screen.getByText('Ada Lovelace')).toBeInTheDocument()
    expect(screen.getByText('ada@example.com')).toBeInTheDocument()
  })

  it('returns a viewer who left an invitation link to that landing', async () => {
    // The provider's round trip replaced the document that carried the token,
    // so the landing has to come from the tab's own record. It is consumed
    // once: a later re-entry to this route must not repeat the move.
    const landing = '/invitations/tok-invite'
    rememberInvitationLanding(landing)
    bindRequestFn(createBackend('data'))
    renderDemoApp({ session: providerSession() })

    await waitFor(() => expect(window.location.pathname).toBe(landing))
    expect(pendingInvitationLanding()).toBeNull()
  })

  it('lands a viewer home when the provider detour started elsewhere', async () => {
    bindRequestFn(createBackend('data'))
    renderDemoApp({ session: providerSession() })

    // The tenant shell's business slot is the destination the callback has
    // always named when no invitation landing was left behind.
    await waitFor(() => expect(window.location.pathname).toBe('/'))
  })
})
