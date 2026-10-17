/**
 * usage-example.test.tsx -- compiles and runs the wiring the README's
 * quick start documents, end to end over a real @speed/api-client.
 *
 * Every step of the quick start executes here, in the order the README
 * shows: the bilingual i18n instance with both namespaces registered (the
 * auth-ui namespace for this package's strings, the ui-kit namespace
 * because the form fields render through ui-kit's FormField), the
 * AppThemeProvider tree, createClient over a fetch stand-in, the memory
 * access-token store, refreshAccessToken: () => session.refresh() bound
 * through the api-sdk runtime seam (bindRequestFn -- the same seam a
 * host's generated calls use), attachSession, and the host gate that
 * switches between the sign-in surface and the app on the auth-core hooks.
 * The journey then scripts a password sign-in (a flow started and taken to
 * its ready step's result), a protected request refused with
 * authn.token_expired (a stale access token) that the client silently
 * refreshes -- the refresh travels credential-less, by declaration -- and
 * a later refusal: the session died server-side (authn.session_revoked),
 * so the refresh itself is refused and the api-client machinery converges
 * the session to the "ended" state its host observes.
 *
 * Why this file runs the real client while @speed/auth-core's own
 * usage-example drives its flows through the scripted harness: auth-core
 * ships session.test.ts whose real-client legs prove the composition
 * there, and auth-ui has no session leg of its own -- this file is it. The
 * fetch stand-in answers with genuine Response objects, the pattern of
 * session.test.ts's real-client legs; component tests in this package keep
 * driving through the scripted session-harness, which is the right tool
 * when a test must script raw ApiErrors or inspect bodies. Journey
 * coverage beyond this example (the SMS channel, explicit sign-out, the
 * social callback route) lives in session-journey.test.tsx over the same
 * rig and gate.
 */

import { useState } from 'react'
import type { ReactElement } from 'react'
import { describe, expect, it, vi } from 'vitest'
import { act, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import {
  I18nextProvider,
  createI18n,
  registerNamespace,
  switchLanguage,
} from '@speed/i18n'
import {
  UI_KIT_NAMESPACE,
  uiKitResources,
  AppThemeProvider,
} from '@speed/ui-kit'
import { attachSession } from '@speed/auth-core'
import { authnProfileGet } from '@speed/api-sdk'
import { SessionGate } from '../test-utils/session-gate.js'
import {
  errorResponse,
  jsonResponse,
  makeRealClientRig,
} from '../test-utils/real-client.js'
import {
  authenticatedCompletion,
  completeFlow,
  methodList,
  principal,
} from '@speed/test-utils/session-harness'
import { AUTH_UI_NAMESPACE, authUiResources } from './resources.js'
import { SignInScreen } from './SignInScreen.js'
import zhCN from './locales/zh-CN.json' with { type: 'json' }
import enUS from './locales/en-US.json' with { type: 'json' }

// The quick start starts zh-CN; the journey's last leg switches the
// running instance to en-US and asserts the English copy.
const IDENTIFIER_LABEL_ZH = zhCN.passwordSignIn.identifierLabel
const PASSWORD_LABEL_ZH = zhCN.passwordSignIn.passwordLabel
const SUBMIT_ZH = zhCN.passwordSignIn.submit
const SESSION_ENDED_TITLE_ZH = zhCN.sessionEnded.title
const SIGN_IN_AGAIN_ZH = zhCN.sessionEnded.signInAction
const IDENTIFIER_LABEL_EN = enUS.passwordSignIn.identifierLabel
const SUBMIT_EN = enUS.passwordSignIn.submit

const START_FLOW = '/auth/flows'
const ANSWER_FLOW = '/auth/flows/answer'
const METHODS = '/auth/methods'
const ME = '/auth/me'
const FEATURES = '/features'
const TOKEN = '/auth/token'

/** The deployment's method document: both form channels, so the surface
 * renders the tab strip. The sign-in surface reads it on mount. */
const METHODS_BODY = methodList([
  { name: 'password', kind: 'form' },
  { name: 'sms', kind: 'form' },
])

/** The host's app view in the example: one protected action that reads
 * the caller's own profile through the generated /me operation -- routed,
 * like every generated call, through the bound real client. */
function ProtectedView(): ReactElement {
  const [status, setStatus] = useState<'idle' | 'checking' | 'ok'>('idle')

  const runCheck = async (): Promise<void> => {
    setStatus('checking')
    try {
      await authnProfileGet()
      setStatus('ok')
    } catch {
      // A refused check whose refresh was refused signed the session out;
      // the gate has already replaced this view with the session-ended
      // screen, so there is nothing left to render on this branch.
    }
  }

  return (
    <div>
      <h1>Account</h1>
      <p>Signed in</p>
      <button
        type="button"
        disabled={status === 'checking'}
        onClick={() => void runCheck()}
      >
        Check session
      </button>
      {status === 'checking' ? <p>Checking session</p> : null}
      {status === 'ok' ? <p>Session ok</p> : null}
    </div>
  )
}

describe('the README quick start, exercised over a real api-client', () => {
  it('signs in, survives a silent refresh and surfaces a refused one as the session-ended screen', async () => {
    // The api-client reports a refused refresh through the Reporter seam
    // by design; spy on the default console sink so the journey's
    // refusal leg is asserted behaviour, not test noise.
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})

    // The scripted server: the password flow is ready at its first step,
    // so one `complete` answer issues the tokens; the /me endpoint
    // refuses the first and third checks -- the stale access-1 with
    // authn.token_expired, the dead session with authn.session_revoked --
    // and the refresh endpoint rotates the pair once and refuses the
    // second refresh (400): the two legs of the journey.
    let meAttempts = 0
    let refreshAttempts = 0
    const rig = makeRealClientRig((call) => {
      if (call.path === FEATURES) {
        return jsonResponse(200, { features: [] })
      }
      switch (call.path) {
        case METHODS:
          return jsonResponse(200, METHODS_BODY)
        case START_FLOW:
          return jsonResponse(200, completeFlow(1))
        case ANSWER_FLOW:
          return jsonResponse(200, authenticatedCompletion())
        case TOKEN:
          refreshAttempts += 1
          if (refreshAttempts === 1) {
            return jsonResponse(200, {
              access_token: 'access-2',
              token_type: 'Bearer',
              expires_in: 3600,
              refresh_token: 'refresh-2',
            })
          }
          return errorResponse(400, 'authn.refresh_token_invalid')
        case ME:
          meAttempts += 1
          if (meAttempts === 2) {
            // The stale access-1 is refused: its token expired -- the
            // leg the client's silent refresh covers.
            return errorResponse(401, 'authn.token_expired')
          }
          if (meAttempts === 4) {
            // The session died server-side after the refresh, so the
            // last /me refusal is a revoked session.
            return errorResponse(401, 'authn.session_revoked')
          }
          return jsonResponse(200, principal())
      }
      throw new Error(`no scripted answer for ${call.method} ${call.path}`)
    })

    // The quick start's bootstrap: attach the session to the hooks and
    // mount the host tree -- the i18n instance with both namespaces, the
    // theme provider, and the gate between the sign-in surface and the
    // app.
    attachSession(rig.session)
    const i18n = createI18n({
      supportedLanguages: ['zh-CN', 'en-US'],
      defaultLanguage: 'zh-CN',
      storage: null,
      urlParameterName: null,
      navigatorLanguages: [],
    })
    registerNamespace(i18n, AUTH_UI_NAMESPACE, authUiResources)
    registerNamespace(i18n, UI_KIT_NAMESPACE, uiKitResources)
    render(
      <I18nextProvider i18n={i18n}>
        <AppThemeProvider i18n={i18n}>
          <SessionGate
            app={<ProtectedView />}
            signIn={<SignInScreen session={rig.session} />}
          />
        </AppThemeProvider>
      </I18nextProvider>,
    )
    const user = userEvent.setup()

    // Anonymous start: the sign-in surface in the instance's zh-CN (the
    // channels appear once the discovery read settles).
    expect(await screen.findByRole('tab', { name: zhCN.passwordSignIn.title }))
      .toBeInTheDocument()
    await user.type(
      await screen.findByLabelText(IDENTIFIER_LABEL_ZH),
      'alice@example.com',
    )
    await user.type(screen.getByLabelText(PASSWORD_LABEL_ZH), 's3cret-pass')
    await user.click(screen.getByRole('button', { name: SUBMIT_ZH }))

    // Authenticated: the gate flipped to the app and the store holds the
    // issued access token.
    const check = await screen.findByRole('button', { name: 'Check session' })
    expect(rig.store.get()).toBe('access-1')
    expect(screen.queryByText(SESSION_ENDED_TITLE_ZH)).not.toBeInTheDocument()

    // First check: the stale access-1 is refused (authn.token_expired);
    // the api-client silently refreshes -- the refresh request travels
    // credential-less, by declaration -- rotates the pair in the store
    // and retries the request once with the fresh token.
    await user.click(check)
    expect(await screen.findByText('Session ok')).toBeInTheDocument()
    expect(rig.store.get()).toBe('access-2')
    let meCalls = rig.calls.filter((call) => call.path === ME)
    let tokenCalls = rig.calls.filter((call) => call.path === TOKEN)
    expect(meCalls).toHaveLength(3)
    expect(meCalls[0]?.authorization).toBe('Bearer access-1')
    expect(meCalls[1]?.authorization).toBe('Bearer access-1')
    expect(meCalls[2]?.authorization).toBe('Bearer access-2')
    expect(tokenCalls).toHaveLength(1)
    // Still authenticated: the app view stayed up through the refresh.
    expect(screen.getByRole('button', { name: 'Check session' }))
      .toBeInTheDocument()

    // Second check: the session died server-side -- the /me refusal is
    // authn.session_revoked -- so the refresh itself is refused.
    // refresh() resolves false and signs the session out; the gate's
    // anonymous snapshot at the app view is the session-ended screen.
    await user.click(screen.getByRole('button', { name: 'Check session' }))
    expect(await screen.findByText(SESSION_ENDED_TITLE_ZH))
      .toBeInTheDocument()
    expect(rig.store.get()).toBeNull()
    meCalls = rig.calls.filter((call) => call.path === ME)
    tokenCalls = rig.calls.filter((call) => call.path === TOKEN)
    expect(meCalls).toHaveLength(4)
    expect(meCalls[3]?.authorization).toBe('Bearer access-2')
    expect(tokenCalls).toHaveLength(2)
    // The reporter carries the refused original request's envelope --
    // the dead-session /me answer, authn.session_revoked, never the
    // refresh's own refusal code.
    expect(warn).toHaveBeenCalledWith(
      'access token refresh failed',
      expect.objectContaining({ status: 401, code: 'authn.session_revoked' }),
    )

    // The whole exchange, in order -- including that the refused check
    // was never retried: the session-ended state is terminal until the
    // viewer signs in again.
    expect(rig.calls.map((call) => `${call.method} ${call.path}`)).toEqual([
      `GET ${METHODS}`,
      `POST ${START_FLOW}`,
      `POST ${ANSWER_FLOW}`,
      `GET ${ME}`,
      `GET ${FEATURES}`,
      `GET ${ME}`,
      `POST ${TOKEN}`,
      `GET ${ME}`,
      `GET ${ME}`,
      `POST ${TOKEN}`,
    ])

    // Sign in again: the gate hands the viewer back to the sign-in
    // surface.
    await user.click(screen.getByRole('button', { name: SIGN_IN_AGAIN_ZH }))
    expect(await screen.findByLabelText(IDENTIFIER_LABEL_ZH)).toBeInTheDocument()

    // The same surface in the other supported language.
    await act(async () => {
      await switchLanguage(i18n, 'en-US')
    })
    expect(await screen.findByRole('tab', { name: enUS.passwordSignIn.title }))
      .toBeInTheDocument()
    expect(await screen.findByLabelText(IDENTIFIER_LABEL_EN)).toBeInTheDocument()
    expect(await screen.findByRole('button', { name: SUBMIT_EN }))
      .toBeInTheDocument()

    warn.mockRestore()
  })
})
