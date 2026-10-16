/**
 * The invitation landing (/invitations/:token): the public route an
 * invitee's link points at.
 *
 * The page gates on the session: an anonymous viewer meets the host's
 * sign-in surface, and a signed-in viewer meets the accept screen, whose two
 * invitation operations reach the backend through the app's generated SDK
 * surface (src/app-api). The suite binds a scripted request function over the
 * api-sdk runtime seam so the real generated operations run against
 * responses it controls; accepting then navigates the viewer home.
 */

import { beforeEach, describe, expect, it } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { RequestFn, RequestOptions } from '@speed/api-client'
import { bindRequestFn } from '@speed/api-sdk/runtime'
import authUiEnUS from '../../../../../web/packages/auth-ui/src/locales/en-US.json' with { type: 'json' }
import demoEnUS from '../locales/en-US.json' with { type: 'json' }
import { renderDemoApp } from '../test-utils/render.js'
import { authenticatedSnapshot, createTestSession } from '../test-utils/session.js'
import { INVITATION_PREFIX } from '../paths.js'

const TOKEN = 'tok-123'
const PATH = `${INVITATION_PREFIX}/${TOKEN}`

/** The acceptView answer for a pending invitation. */
const VIEW = {
  invitation: {
    id: 'inv-1',
    tenant: 'Acme',
    email: 'invitee@example.com',
    status: 'pending',
    invited_by: 'acc-1',
    expires_at: '2026-10-20T12:00:00Z',
    accepted_at: '0001-01-01T00:00:00Z',
    created_at: '2026-10-15T12:00:00Z',
  },
}

/**
 * The scripted invitation endpoints: a GET answers the pending invitation
 * and a POST accepts it (recording that it was redeemed).
 */
function makeInvitationBackend() {
  const accepted: string[] = []
  const fn: RequestFn = <T,>(path: string, options?: RequestOptions) => {
    const method = options?.method ?? 'GET'
    if (path === `/auth/invitations/${TOKEN}` && method === 'GET') {
      return Promise.resolve(VIEW as T)
    }
    if (path === `/auth/invitations/${TOKEN}` && method === 'POST') {
      accepted.push(TOKEN)
      return Promise.resolve({
        account: 'acc-1',
        tenant: 'Acme',
        active: true,
        source: 'invite',
        sso_exempt: false,
      } as T)
    }
    return Promise.resolve(undefined as T)
  }
  return { fn, accepted }
}

beforeEach(() => {
  window.history.replaceState({}, '', PATH)
})

describe('the invitation landing', () => {
  it('shows the host sign-in surface to an anonymous viewer', () => {
    renderDemoApp()

    // The invitee must accept as themselves: an anonymous viewer meets the
    // sign-in / register surface, never the accept screen.
    expect(
      screen.getByRole('heading', { level: 1, name: demoEnUS.signIn.heading }),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('heading', {
        name: authUiEnUS.acceptInvitation.title,
      }),
    ).not.toBeInTheDocument()
  })

  it('accepts the invitation for a signed-in viewer and navigates home', async () => {
    const backend = makeInvitationBackend()
    bindRequestFn(backend.fn)
    const user = userEvent.setup()
    renderDemoApp({ session: createTestSession(authenticatedSnapshot()) })

    // The accept screen renders the pending invitation (read through the
    // generated operation) beside its accept action.
    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: authUiEnUS.acceptInvitation.title,
      }),
    ).toBeInTheDocument()

    await user.click(
      screen.getByRole('button', { name: authUiEnUS.acceptInvitation.accept }),
    )
    await waitFor(() => {
      expect(backend.accepted).toEqual([TOKEN])
    })
    // Acceptance navigates the viewer home (the tenant shell's business slot).
    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: demoEnUS.home.heading,
      }),
    ).toBeInTheDocument()
  })
})
