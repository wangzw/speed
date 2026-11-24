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

const IDENTIFIER_LABEL_ZH = zhCN.passwordSignIn.identifierLabel
const PASSWORD_LABEL_ZH = zhCN.passwordSignIn.passwordLabel
const SUBMIT_ZH = zhCN.passwordSignIn.submit
const SESSION_ENDED_TITLE_ZH = zhCN.sessionEnded.title
const SIGN_IN_AGAIN_ZH = zhCN.sessionEnded.signInAction
const IDENTIFIER_LABEL_EN = enUS.passwordSignIn.identifierLabel
const SUBMIT_EN = enUS.passwordSignIn.submit

const START_FLOW = '/api/v1/auth/flows'
const ANSWER_FLOW = '/api/v1/auth/flows/answer'
const METHODS = '/api/v1/auth/methods'
const ME = '/api/v1/auth/me'
const FEATURES = '/api/v1/features'
const TOKEN = '/api/v1/auth/token'

const METHODS_BODY = methodList([
  { name: 'password', kind: 'form' },
  { name: 'sms', kind: 'form' },
])

function ProtectedView(): ReactElement {
  const [status, setStatus] = useState<'idle' | 'checking' | 'ok'>('idle')

  const runCheck = async (): Promise<void> => {
    setStatus('checking')
    try {
      await authnProfileGet()
      setStatus('ok')
    } catch {
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
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})

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
            return errorResponse(401, 'authn.token_expired')
          }
          if (meAttempts === 4) {
            return errorResponse(401, 'authn.session_revoked')
          }
          return jsonResponse(200, principal())
      }
      throw new Error(`no scripted answer for ${call.method} ${call.path}`)
    })

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

    expect(await screen.findByRole('tab', { name: zhCN.passwordSignIn.title }))
      .toBeInTheDocument()
    await user.type(
      await screen.findByLabelText(IDENTIFIER_LABEL_ZH),
      'alice@example.com',
    )
    await user.type(screen.getByLabelText(PASSWORD_LABEL_ZH), 's3cret-pass')
    await user.click(screen.getByRole('button', { name: SUBMIT_ZH }))

    const check = await screen.findByRole('button', { name: 'Check session' })
    expect(rig.store.get()).toBe('access-1')
    expect(screen.queryByText(SESSION_ENDED_TITLE_ZH)).not.toBeInTheDocument()

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
    expect(screen.getByRole('button', { name: 'Check session' }))
      .toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Check session' }))
    expect(await screen.findByText(SESSION_ENDED_TITLE_ZH))
      .toBeInTheDocument()
    expect(rig.store.get()).toBeNull()
    meCalls = rig.calls.filter((call) => call.path === ME)
    tokenCalls = rig.calls.filter((call) => call.path === TOKEN)
    expect(meCalls).toHaveLength(4)
    expect(meCalls[3]?.authorization).toBe('Bearer access-2')
    expect(tokenCalls).toHaveLength(2)
    expect(warn).toHaveBeenCalledWith(
      'access token refresh failed',
      expect.objectContaining({ status: 401, code: 'authn.session_revoked' }),
    )

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

    await user.click(screen.getByRole('button', { name: SIGN_IN_AGAIN_ZH }))
    expect(await screen.findByLabelText(IDENTIFIER_LABEL_ZH)).toBeInTheDocument()

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
