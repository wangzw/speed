/**
 * usage-example.test.tsx -- the README quick start, compiled and run.
 *
 * The quick start wires the account page for a signed-in viewer: the
 * bilingual i18n instance with both namespaces registered (the
 * account-ui namespace for this package's strings, the ui-kit namespace
 * because the confirm dialogs speak ui-kit-namespace keys), the
 * AppThemeProvider tree and a QueryClientProvider (the account surfaces
 * read their data through the react-query hooks generated into
 * @speed/api-sdk over the host's client), and the host's own account page
 * composing the sections under a signed-in session.
 *
 * Why a real client: the component suites drive each surface in isolation
 * through the same real-client rig; the wiring the README documents -- the
 * session, the client bound to the api-sdk runtime, the providers, the
 * i18n instance, the theme and query providers, and the sections side by
 * side -- is a composition no single-component suite exercises. This file
 * is that journey: it renders the composed page over a real
 * @speed/api-client whose scripted fetch answers genuine Response
 * objects, walks one session revoke, and switches the whole page to the
 * other supported language.
 */

import { describe, expect, it } from 'vitest'
import { act, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { switchLanguage } from '@speed/i18n'
import type { AuthSession } from '@speed/auth-core'
import zhCN from './locales/zh-CN.json' with { type: 'json' }
import enUS from './locales/en-US.json' with { type: 'json' }
import {
  jsonResponse,
  makeRealClientRig,
  type RealCall,
  type RealResponder,
} from '../test-utils/real-client.js'
import { renderWithProviders } from '../test-utils/render.js'
import { expectNoAxeViolations } from '../test-utils/axe.js'
import { ProfileSection } from './ProfileSection.js'
import { PasswordSection } from './PasswordSection.js'
import { PhoneSection } from './PhoneSection.js'
import { IdentifiersSection } from './IdentifiersSection.js'
import { ContactVerificationSection } from './ContactVerificationSection.js'
import { PreferencesSection } from './PreferencesSection.js'
import { SessionsSection } from './SessionsSection.js'
import { SocialBindingsSection } from './SocialBindingsSection.js'
import { MfaSection } from './MfaSection.js'

const PROFILE_PATH = '/api/v1/auth/me'
const SESSIONS_PATH = '/api/v1/auth/sessions'
const FACTORS_PATH = '/api/v1/auth/factors'
const METHODS_PATH = '/api/v1/auth/methods'

/** The host's binding callback route: the flow's top-level return_to. */
const ACCOUNT_CALLBACK = '/account/callback'

/** The host's account page in the example: the sections the quick start
 * documents, under one signed-in session. The sections that drive a flow
 * (a bind, a step-up, an enroll) take the session; the profile and
 * preferences sections write without a step-up, so they take none. */
function AccountPage({ session }: { readonly session: AuthSession }) {
  return (
    <div>
      <ProfileSection />
      <PasswordSection session={session} />
      <PhoneSection session={session} />
      <IdentifiersSection session={session} />
      <ContactVerificationSection session={session} />
      <PreferencesSection />
      <SessionsSection />
      <SocialBindingsSection
        session={session}
        returnTo={ACCOUNT_CALLBACK}
      />
      <MfaSection session={session} />
    </div>
  )
}

describe('the README quick start, exercised over a real api-client', () => {
  it('renders the composed account page, revokes a session and switches language', async () => {
    let ipadRevoked = false
    const respond: RealResponder = (call: RealCall) => {
      switch (call.path) {
        case PROFILE_PATH:
          return jsonResponse(200, {
            account_id: 'user-1',
            kind: 'person',
            display_name: 'Ada',
            mfa_required: false,
            password_set: true,
            created_at: '2026-01-01T00:00:00Z',
            attributes: {},
            locale: '',
            time_zone: '',
            identifiers: [
              {
                id: 'id-email',
                kind: 'email',
                value: 'ada@example.test',
                proven: true,
                verified_at: '2026-01-01T00:00:00Z',
                verified_via: 'idp:github',
              },
            ],
            links: [
              { id: 'github-1', provider: 'github', email: 'ada@example.test' },
            ],
            verification: { state: 'none', email: '', since: '' },
          })
        case SESSIONS_PATH:
          return jsonResponse(200, {
            sessions: [
              {
                id: 'session-current',
                kind: 'browser',
                current: true,
                created_at: '2026-08-01T02:05:00.000Z',
                last_seen_at: '2026-08-03T09:12:00.000Z',
                expires_at: '2026-09-01T02:05:00.000Z',
                client_ip: '198.51.100.4',
                user_agent: '',
              },
              ...(ipadRevoked
                ? []
                : [
                    {
                      id: 'session-ipad',
                      kind: 'browser',
                      current: false,
                      created_at: '2026-07-28T01:10:00.000Z',
                      last_seen_at: '2026-08-02T20:40:00.000Z',
                      expires_at: '2026-09-01T01:10:00.000Z',
                      client_ip: '203.0.113.55',
                      user_agent: '',
                    },
                  ]),
            ],
          })
        case `${SESSIONS_PATH}/session-ipad`:
          ipadRevoked = true
          return new Response(null, { status: 204 })
        case FACTORS_PATH:
          return jsonResponse(200, {
            factors: [
              {
                id: 'factor-1',
                kind: 'totp',
                label: 'My authenticator',
                status: 'active',
                created_at: '2026-01-01T00:00:00Z',
              },
            ],
          })
        case METHODS_PATH:
          // The deployment's discovery document: its two redirect methods
          // are the bindings add area's channels, and its mfa.factors are
          // the enroll entries (the passkey entry is not offered).
          return jsonResponse(200, {
            methods: [
              { name: 'github', kind: 'redirect', label: 'GitHub' },
              { name: 'wechat', kind: 'redirect', label: 'WeChat' },
            ],
            signup: { enabled: false, password: { enabled: false } },
            mfa: { factors: ['totp'] },
            human: { required: false, provider: 'off' },
          })
      }
      throw new Error(`no scripted answer for ${call.method} ${call.path}`)
    }

    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    const { i18n } = renderWithProviders(<AccountPage session={rig.session} />)
    const user = userEvent.setup()

    // The surfaces settle on the server's answers.
    for (const title of [
      zhCN.profile.title,
      zhCN.password.title,
      zhCN.phone.title,
      zhCN.identifiers.title,
      zhCN.preferences.title,
      zhCN.sessions.title,
      zhCN.bindings.title,
      zhCN.mfa.title,
    ]) {
      expect(await screen.findByRole('heading', { name: title })).toBeTruthy()
    }
    expect(screen.getByText(zhCN.sessions.current)).toBeTruthy()
    expect(screen.getByText(zhCN.bindings.provider.github)).toBeTruthy()
    expect(screen.getByText('My authenticator')).toBeTruthy()
    await expectNoAxeViolations()

    // Two rows carry the unknown-device label; only the non-current one
    // has a revoke action, named after that label.
    const revokeAria = zhCN.sessions.revokeAriaWithDevice.replace(
      '{{device}}',
      zhCN.sessions.deviceUnknown,
    )
    await user.click(screen.getByRole('button', { name: revokeAria }))
    await waitFor(() => {
      expect(screen.queryByRole('button', { name: revokeAria })).toBeNull()
    })

    // The same page in the other supported language.
    await act(async () => {
      await switchLanguage(i18n, 'en-US')
    })
    for (const title of [
      enUS.profile.title,
      enUS.password.title,
      enUS.phone.title,
      enUS.identifiers.title,
      enUS.preferences.title,
      enUS.sessions.title,
      enUS.bindings.title,
      enUS.mfa.title,
    ]) {
      expect(screen.getByRole('heading', { name: title })).toBeTruthy()
    }
    // The composed page renders every account surface over one scripted
    // client and two languages; the explicit budget covers the render and
    // axe cost of that class on a loaded runner.
  }, 20000)
})
