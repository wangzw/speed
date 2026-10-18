/**
 * The invitation landing (/invitations/:token): the public route an
 * invitee's link points at.
 *
 * The host gates the landing on a session: an anonymous viewer meets the
 * host's sign-in surface, and a signed-in viewer meets the accept screen,
 * whose two invitation operations reach the backend through the app's
 * generated SDK surface (src/app-api). The accept surface itself is public
 * -- the token is the whole credential -- so that gate is the host's
 * composition, not what binds the acceptance. The suite binds a scripted
 * request function over the api-sdk runtime seam so the real generated
 * operations run against responses it controls; accepting then navigates
 * the viewer home.
 */

import { beforeEach, describe, expect, it } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { RequestFn, RequestOptions } from '@speed/api-client'
import type { AuthSession, AuthSnapshot, FeatureState } from '@speed/auth-core'
import { bindRequestFn } from '@speed/api-sdk/runtime'
import {
  FEATURES_GET,
  LOGIN_ANSWER,
  LOGIN_METHODS,
  LOGIN_START,
  PROFILE_GET,
  authenticatedCompletion,
  completeFlow,
  makeHarness,
  methodList,
  openFlow,
  principal,
} from '@speed/test-utils/session-harness'
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
    if (path === `/api/v1/auth/invitations/${TOKEN}` && method === 'GET') {
      return Promise.resolve(VIEW as T)
    }
    if (path === `/api/v1/auth/invitations/${TOKEN}` && method === 'POST') {
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

/**
 * The harness session with an already-settled feature document. A real page
 * load reads the document while the visitor is still anonymous, so the
 * invitation landing -- whose declaration is feature-gated -- renders before
 * the visitor signs in. The harness session reads features on a login commit
 * only, so this wrapper supplies the same initial answer.
 */
function sessionWithFeatures(
  session: AuthSession,
  names: readonly string[],
): AuthSession {
  const features: FeatureState = { state: 'ready', names: new Set(names) }
  let snapshot: AuthSnapshot = { ...session.getSnapshot(), features }
  const listeners = new Set<(value: AuthSnapshot) => void>()
  session.subscribe((next) => {
    snapshot = { ...next, features }
    for (const listener of listeners) {
      listener(snapshot)
    }
  })
  return {
    ...session,
    getSnapshot: () => snapshot,
    subscribe: (listener) => {
      listeners.add(listener)
      return () => {
        listeners.delete(listener)
      }
    },
  }
}

beforeEach(() => {
  window.history.replaceState({}, '', PATH)
})

describe('the invitation landing', () => {
  it('shows the host sign-in surface to an anonymous viewer', () => {
    renderDemoApp()

    // The host gates the landing on a session: an anonymous viewer meets
    // the sign-in / register surface, never the accept screen.
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

  it('stays on the invitation landing after a registration through SMS', async () => {
    // The URL carries the token: the register mode's landing belongs to the
    // application's own sign-in page, not to a sign-in surface embedded in
    // this page. The session flip must re-render the accept screen in place.
    const harness = makeHarness({
      [FEATURES_GET]: () => ({
        features: ['authn.invitations', 'workspace.personal'],
      }),
      [LOGIN_METHODS]: () =>
        methodList(
          [{ name: 'sms', kind: 'form', opens_account: true }],
          { signup: true },
        ),
      [LOGIN_START]: () =>
        openFlow({
          seq: 1,
          state: 'first',
          next: {
            kind: 'challenge',
            fields: [
              { name: 'code', kind: 'code', required: true, secret: true },
            ],
          },
        }),
      [LOGIN_ANSWER]: (call) =>
        (call.options?.body as { step?: string }).step === 'complete'
          ? authenticatedCompletion()
          : completeFlow(2),
      [PROFILE_GET]: () => principal(),
      [`GET /api/v1/auth/invitations/${TOKEN}`]: () => VIEW,
    })
    const user = userEvent.setup()
    renderDemoApp({
      session: sessionWithFeatures(harness.session, [
        'authn.invitations',
        'workspace.personal',
      ]),
    })

    await user.click(
      await screen.findByRole('button', {
        name: authUiEnUS.mode.registerEntry,
      }),
    )
    await user.type(
      screen.getByLabelText(authUiEnUS.smsSignIn.phoneLabel),
      '+14155550123',
    )
    await user.click(
      screen.getByRole('button', { name: authUiEnUS.smsSignIn.sendCode }),
    )
    await user.type(
      await screen.findByLabelText(authUiEnUS.smsSignIn.codeLabel),
      '123456',
    )
    const form = screen
      .getByLabelText(authUiEnUS.smsSignIn.codeLabel)
      .closest('form') as HTMLFormElement
    await user.click(
      within(form).getByRole('button', { name: authUiEnUS.smsSignIn.submit }),
    )

    // The registration stayed on the invitation URL and the page re-rendered
    // onto its accept screen, which read the token's invitation.
    await waitFor(() => expect(window.location.pathname).toBe(PATH))
    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: authUiEnUS.acceptInvitation.title,
      }),
    ).toBeInTheDocument()
  })
})
