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

const FEATURES_BODY = { features: [] }

const METHODS_BODY = methodList([
  { name: 'password', kind: 'form' },
  { name: 'sms', kind: 'form' },
])

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

    await user.click(await screen.findByRole('tab', { name: SMS_TAB_ZH }))
    await user.type(screen.getByLabelText(PHONE_LABEL_ZH), PHONE)
    await user.click(screen.getByRole('button', { name: SEND_CODE_ZH }))
    expect(await screen.findByText(SENT_NOTICE_ZH)).toBeInTheDocument()
    expect(rig.store.get()).toBeNull()

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

    await user.type(
      await screen.findByLabelText(IDENTIFIER_LABEL_ZH),
      'alice@example.com',
    )
    await user.type(screen.getByLabelText(PASSWORD_LABEL_ZH), 's3cret-pass')
    await user.click(screen.getByRole('button', { name: SUBMIT_ZH }))
    await screen.findByRole('heading', { name: 'Signed in' })
    expect(rig.store.get()).toBe('access-1')

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
    expect(rig.calls[5]?.authorization).toBe('Bearer access-1')

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

    await user.click(
      await screen.findByRole('button', { name: GOOGLE_LABEL_ZH }),
    )
    const gotAuthorizeUrl = await waitFor(() => {
      expect(capturedAuthorizeUrl).not.toBeNull()
      return capturedAuthorizeUrl ?? ''
    })
    expect(gotAuthorizeUrl).toBe(authorizeUrl)
    expect(rig.calls[1]?.rawBody).toContain('"method":"google"')
    expect(rig.calls[1]?.rawBody).toContain(
      `"return_to":"${CALLBACK_ROUTE}"`,
    )

    expect(await screen.findByText(PENDING_ZH)).toBeInTheDocument()
    const currentCalls = (): typeof rig.calls =>
      rig.calls.filter((call) => call.path === CURRENT_FLOW)
    await waitFor(() => expect(currentCalls()).toHaveLength(1))
    expect(currentCalls()[0]?.authorization).toBeNull()

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
