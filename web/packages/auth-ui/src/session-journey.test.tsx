/**
 * session-journey.test.tsx -- the sign-in family's journeys beyond the
 * usage example: the SMS channel end to end, an explicit sign-out, and
 * the social callback route, each asserting the UI-visible states a
 * viewer would see.
 *
 * The journeys compose the same rig and gate as usage-example.test.tsx --
 * a real @speed/api-client over a fetch stand-in answering with genuine
 * Response objects, the memory store, refreshAccessToken:
 * () => session.refresh() bound through the api-sdk runtime seam, and the
 * host gate flipping between the sign-in surface and the app on the
 * auth-core snapshot -- so every leg runs the transport a host's wiring
 * runs. The gate and the network rig live in test-utils/ (shared-helper
 * rule); this file is named for the behaviour it chronicles rather than
 * for one source file, because it crosses the whole composed family.
 */

import { useState } from 'react'
import type { ReactElement } from 'react'
import { describe, expect, it } from 'vitest'
import { act, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { attachSession } from '@speed/auth-core'
import type { AuthSession } from '@speed/auth-core'
import { SessionGate } from '../test-utils/session-gate.js'
import {
  jsonResponse,
  makeRealClientRig,
} from '../test-utils/real-client.js'
import { renderWithProviders } from '../test-utils/render.js'
import {
  authenticatedCompletion,
  completeFlow,
  methodList,
  openFlow,
  principal,
} from '@speed/test-utils/session-harness'
import { SignInScreen } from './SignInScreen.js'
import { SignOutButton } from './SignOutButton.js'
import { SocialCallbackHandler } from './SocialCallbackHandler.js'
import zhCN from './locales/zh-CN.json' with { type: 'json' }

const PHONE = '+8613800138000'
const PHONE_LABEL_ZH = zhCN.smsSignIn.phoneLabel
const SEND_CODE_ZH = zhCN.smsSignIn.sendCode
const SENT_NOTICE_ZH = zhCN.smsSignIn.sentNotice.replace('{{phone}}', PHONE)
const CODE_LABEL_ZH = zhCN.smsSignIn.codeLabel
const SMS_TAB_ZH = zhCN.smsSignIn.title
const IDENTIFIER_LABEL_ZH = zhCN.passwordSignIn.identifierLabel
const PASSWORD_LABEL_ZH = zhCN.passwordSignIn.passwordLabel
const SUBMIT_ZH = zhCN.passwordSignIn.submit
const SIGN_OUT_ZH = zhCN.signOut.label
const SESSION_ENDED_TITLE_ZH = zhCN.sessionEnded.title
const SESSION_ENDED_DESCRIPTION_ZH = zhCN.sessionEnded.description
const SIGN_IN_AGAIN_ZH = zhCN.sessionEnded.signInAction
const GOOGLE_LABEL_ZH = zhCN.social.provider.google
const PENDING_ZH = zhCN.socialCallback.pending

const START_FLOW = '/api/v1/auth/flows'
const ANSWER_FLOW = '/api/v1/auth/flows/answer'
const CURRENT_FLOW = '/api/v1/auth/flows/current'
const METHODS = '/api/v1/auth/methods'
const ME = '/api/v1/auth/me'
const FEATURES = '/api/v1/features'
const LOGOUT = '/api/v1/auth/logout'

/** The process's feature document: this journey's deployment serves an
 * empty vocabulary. Every login commit and anonymous-start restore reads
 * it, so every responder answers it. */
const FEATURES_BODY = { features: [] }

/** The deployment's method document: both form channels. The sign-in
 * surface reads it on mount, so every responder answers it. */
const METHODS_BODY = methodList([
  { name: 'password', kind: 'form' },
  { name: 'sms', kind: 'form' },
])

/** The host's app content the journeys land in after a sign-in: a
 * heading and the sign-out action in app chrome. */
function AppView({ session }: { session: AuthSession }): ReactElement {
  return (
    <div>
      <h1>Signed in</h1>
      <SignOutButton session={session} />
    </div>
  )
}

describe('session journeys over the composed sign-in family', () => {
  it('signs in with an SMS code, from the phone step to the app', async () => {
    const rig = makeRealClientRig((call) => {
      if (call.path === FEATURES) {
        return jsonResponse(200, FEATURES_BODY)
      }
      switch (call.path) {
        case METHODS:
          return jsonResponse(200, METHODS_BODY)
        case START_FLOW:
          // The sms method's start sends a code and asks for it back.
          return jsonResponse(
            200,
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
          )
        case ANSWER_FLOW:
          return jsonResponse(
            200,
            (JSON.parse(call.rawBody ?? '{}') as { step?: string }).step ===
              'complete'
              ? authenticatedCompletion()
              : completeFlow(2),
          )
        case ME:
          return jsonResponse(200, principal())
      }
      throw new Error(`no scripted answer for ${call.method} ${call.path}`)
    })
    attachSession(rig.session)
    renderWithProviders(
      <SessionGate
        app={<AppView session={rig.session} />}
        signIn={<SignInScreen session={rig.session} />}
      />,
    )
    const user = userEvent.setup()

    // The SMS channel: the phone step begins the flow. The form channels
    // appear once the discovery read settles.
    await user.click(await screen.findByRole('tab', { name: SMS_TAB_ZH }))
    await user.type(screen.getByLabelText(PHONE_LABEL_ZH), PHONE)
    await user.click(screen.getByRole('button', { name: SEND_CODE_ZH }))
    // The sent notice announces the receiving number.
    const sent = await screen.findByRole('status')
    expect(sent).toHaveTextContent(SENT_NOTICE_ZH)
    // Starting the flow is not a session: the gate still shows the
    // sign-in surface for the code step.
    expect(rig.store.get()).toBeNull()

    // The code step completes the sign-in.
    await user.type(screen.getByLabelText(CODE_LABEL_ZH), '123456')
    await user.click(screen.getByRole('button', { name: SUBMIT_ZH }))
    expect(
      await screen.findByRole('heading', { name: 'Signed in' }),
    ).toBeInTheDocument()
    expect(rig.store.get()).toBe('access-1')
    expect(
      rig.calls.map((call) => `${call.method} ${call.path}`),
    ).toEqual([
      `GET ${METHODS}`,
      `POST ${START_FLOW}`,
      `POST ${ANSWER_FLOW}`,
      `POST ${ANSWER_FLOW}`,
      `GET ${ME}`,
      `GET ${FEATURES}`,
    ])
    // The start and the code answer of an anonymous SMS sign-in travel
    // credential-less.
    expect(rig.calls[1]?.authorization).toBeNull()
    expect(rig.calls[2]?.authorization).toBeNull()
  })

  it('signs out from the app chrome and lands back on the sign-in surface', async () => {
    const rig = makeRealClientRig((call) => {
      if (call.path === FEATURES) {
        return jsonResponse(200, FEATURES_BODY)
      }
      switch (call.path) {
        case METHODS:
          return jsonResponse(200, METHODS_BODY)
        case START_FLOW:
          return jsonResponse(200, completeFlow(1))
        case ANSWER_FLOW:
          return jsonResponse(200, authenticatedCompletion())
        case ME:
          return jsonResponse(200, principal())
        case LOGOUT:
          // 204 answers carry no body by construction (the fetch spec
          // forbids one), and this environment's Response constructor
          // enforces that.
          return new Response(null, { status: 204 })
      }
      throw new Error(`no scripted answer for ${call.method} ${call.path}`)
    })
    attachSession(rig.session)
    renderWithProviders(
      <SessionGate
        app={<AppView session={rig.session} />}
        signIn={<SignInScreen session={rig.session} />}
      />,
    )
    const user = userEvent.setup()

    // Sign in through the password channel to reach the app.
    await user.type(
      await screen.findByLabelText(IDENTIFIER_LABEL_ZH),
      'alice@example.com',
    )
    await user.type(screen.getByLabelText(PASSWORD_LABEL_ZH), 's3cret-pass')
    await user.click(screen.getByRole('button', { name: SUBMIT_ZH }))
    await screen.findByRole('heading', { name: 'Signed in' })
    expect(rig.store.get()).toBe('access-1')

    // The explicit sign-out is quiet in the button; its success is the
    // snapshot flip the gate observes -- the app view becomes the
    // session-ended screen.
    await user.click(screen.getByRole('button', { name: SIGN_OUT_ZH }))
    expect(await screen.findByText(SESSION_ENDED_TITLE_ZH))
      .toBeInTheDocument()
    expect(screen.getByText(SESSION_ENDED_DESCRIPTION_ZH))
      .toBeInTheDocument()
    expect(rig.store.get()).toBeNull()
    expect(
      rig.calls.map((call) => `${call.method} ${call.path}`),
    ).toEqual([
      `GET ${METHODS}`,
      `POST ${START_FLOW}`,
      `POST ${ANSWER_FLOW}`,
      `GET ${ME}`,
      `GET ${FEATURES}`,
      `POST ${LOGOUT}`,
    ])
    // The logout of an authenticated session travels with its access
    // token.
    expect(rig.calls[5]?.authorization).toBe('Bearer access-1')

    // Sign in again from the ended screen: the gate hands the viewer
    // back to the sign-in surface.
    await user.click(screen.getByRole('button', { name: SIGN_IN_AGAIN_ZH }))
    expect(await screen.findByLabelText(IDENTIFIER_LABEL_ZH)).toBeInTheDocument()
  })

  it('completes a social sign-in from the authorize URL to the app', async () => {
    const CALLBACK_ROUTE = '/api/v1/auth/social/callback'
    const authorizeUrl =
      'https://accounts.google.com/o/oauth2/v2/auth' +
      '?response_type=code' +
      '&client_id=demo-client' +
      '&scope=openid%20email' +
      '&state=state-abc-123'

    // The flow read stays open until the test resolves it, so the pending
    // state of the callback route is assertable.
    let resolveRead: (response: Response) => void = () => {}
    const readPending = new Promise<Response>((resolve) => {
      resolveRead = resolve
    })
    const rig = makeRealClientRig((call) => {
      if (call.path === FEATURES) {
        return jsonResponse(200, FEATURES_BODY)
      }
      switch (call.path) {
        case METHODS:
          return jsonResponse(200, METHODS_BODY)
        case START_FLOW:
          return jsonResponse(
            200,
            openFlow({ next: { kind: 'redirect', url: authorizeUrl, method: 'GET' } }),
          )
        case CURRENT_FLOW:
          return readPending
        case ANSWER_FLOW:
          return jsonResponse(200, authenticatedCompletion())
        case ME:
          return jsonResponse(200, principal())
      }
      throw new Error(`no scripted answer for ${call.method} ${call.path}`)
    })

    /** The sign-in surface of the social journey, standing in for the
     * host's two routes: the sign-in screen, then -- once the authorize
     * URL is reported -- the callback route the provider's redirect lands
     * on. */
    const SocialSurface = ({
      session,
      onAuthorizeUrl,
    }: {
      session: AuthSession
      onAuthorizeUrl: (authorizeUrl: string) => void
    }): ReactElement => {
      const [redirected, setRedirected] = useState(false)
      if (!redirected) {
        return (
          <SignInScreen
            session={session}
            social={{
              providers: [{ provider: 'google', redirectUri: CALLBACK_ROUTE }],
              onAuthorizeUrl: (_provider, url) => {
                onAuthorizeUrl(url)
                setRedirected(true)
              },
            }}
          />
        )
      }
      return <SocialCallbackHandler session={session} />
    }

    let capturedAuthorizeUrl: string | null = null
    attachSession(rig.session)
    renderWithProviders(
      <SessionGate
        app={<AppView session={rig.session} />}
        signIn={
          <SocialSurface
            session={rig.session}
            onAuthorizeUrl={(url) => {
              capturedAuthorizeUrl = url
            }}
          />
        }
      />,
    )
    const user = userEvent.setup()

    // The provider button begins the flow and hands the redirect URL to
    // the host (the host opens it; the fixture captures it). It appears
    // once the discovery read settles.
    await user.click(
      await screen.findByRole('button', { name: GOOGLE_LABEL_ZH }),
    )
    const gotAuthorizeUrl = await waitFor(() => {
      expect(capturedAuthorizeUrl).not.toBeNull()
      return capturedAuthorizeUrl ?? ''
    })
    expect(gotAuthorizeUrl).toBe(authorizeUrl)
    // The provider is the flow's method and the host callback route is
    // its return_to.
    expect(rig.calls[1]?.rawBody).toContain('"method":"google"')
    expect(rig.calls[1]?.rawBody).toContain(
      `"return_to":"${CALLBACK_ROUTE}"`,
    )

    // The host's callback route mounted the handler; the read is pending
    // while the flow is read back.
    expect(await screen.findByText(PENDING_ZH)).toBeInTheDocument()
    const currentCalls = (): typeof rig.calls =>
      rig.calls.filter((call) => call.path === CURRENT_FLOW)
    await waitFor(() => expect(currentCalls()).toHaveLength(1))
    // The read of an anonymous session travels credential-less.
    expect(currentCalls()[0]?.authorization).toBeNull()

    // The flow is ready: its result is taken and the gate lands the app
    // view.
    await act(async () => {
      resolveRead(jsonResponse(200, completeFlow(1)))
    })
    expect(
      await screen.findByRole('heading', { name: 'Signed in' }),
    ).toBeInTheDocument()
    expect(rig.store.get()).toBe('access-1')
    expect(
      rig.calls.map((call) => `${call.method} ${call.path}`),
    ).toEqual([
      `GET ${METHODS}`,
      `POST ${START_FLOW}`,
      `GET ${CURRENT_FLOW}`,
      `POST ${ANSWER_FLOW}`,
      `GET ${ME}`,
      `GET ${FEATURES}`,
    ])
  })
})
