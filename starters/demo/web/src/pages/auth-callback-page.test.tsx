import { screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it } from 'vitest'
import { authUiResources } from '@speed/auth-ui'
import { bindRequestFn } from '@speed/api-sdk/runtime'
import { openFlow } from '../test-utils/session-harness.js'
import demoEnUS from '../locales/en-US.json' with { type: 'json' }
import { createBackend } from '../a11y/backends.js'
import {
  setCurrentTenant,
  setTenantNames,
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

const TENANT_ID = 'acme'

const authEn = authUiResources['en-US'] as unknown as {
  readonly signInFlow: {
    readonly signupAccountInstruction: string
  }
  readonly social: {
    readonly provider: {
      readonly google: string
    }
  }
}

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
  setCurrentTenant(TENANT_ID)
  setTenantNames(new Map([[TENANT_ID, TENANT_ID]]))
})

describe('the auth callback route', () => {
  it("renders exactly one main landmark when the host's absent view shows", async () => {
    bindRequestFn(createBackend('data'))
    const { container } = renderDemoApp({
      session: createTestSession(authenticatedSnapshot()),
    })

    expect(
      await screen.findByRole('heading', { level: 1, name: 'Page not found' }),
    ).toBeInTheDocument()
    expect(container.querySelectorAll('main')).toHaveLength(1)
  })

  it('renders the account-opening step in the sign-in card', async () => {
    bindRequestFn(createBackend('data'))
    renderDemoApp({ session: signupSession() })

    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: demoEnUS.signIn.heading,
      }),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('heading', { level: 2 }),
    ).toBeNull()
    expect(
      screen.getByText(authEn.signInFlow.signupAccountInstruction),
    ).toBeInTheDocument()
    expect(screen.getByText('Ada Lovelace')).toBeInTheDocument()
    expect(screen.getByText('ada@example.com')).toBeInTheDocument()
  })

  it('returns a viewer who left an invitation link to that landing', async () => {
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

    await waitFor(() => expect(window.location.pathname).toBe('/'))
  })
})
