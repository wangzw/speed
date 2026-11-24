import { beforeEach, describe, expect, it } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import type { RequestFn } from '@speed/api-client'
import { bindRequestFn } from '@speed/api-sdk/runtime'
import { authUiResources } from '@speed/auth-ui'
import type { AuthnMethodList, AuthSession } from '@speed/auth-core'
import { PASSWORD_RESET_PATH } from '../paths.js'
import { renderDemoApp } from '../test-utils/render.js'
import { createTestSession } from '../test-utils/session.js'

const authEn = authUiResources['en-US'] as unknown as {
  readonly passwordReset: {
    readonly title: string
    readonly channel: { readonly email: string; readonly sms: string }
    readonly emailLabel: string
    readonly phoneLabel: string
    readonly codeLabel: string
    readonly newPasswordLabel: string
    readonly unavailable: string
  }
}

function resetSession(methods: readonly ('email' | 'sms')[]): AuthSession {
  const passwordReset = { enabled: methods.length > 0, methods: [...methods] }
  return Object.assign(createTestSession(), {
    listMethods: (): Promise<AuthnMethodList> =>
      Promise.resolve({
        methods: [{ name: 'password', kind: 'form' }],
        signup: { enabled: false, password: { enabled: false } },
        mfa: { factors: [] },
        human: {
          phone: { required: false, provider: 'off' },
          other: { required: false, provider: 'off' },
        },
        password_reset: passwordReset,
      }),
  })
}

function notFound(url: string): never {
  throw Object.assign(new Error(`scripted backend: ${url} not found`), {
    status: 404,
  })
}

beforeEach(() => {
  window.history.replaceState({}, '', PASSWORD_RESET_PATH)
  bindRequestFn(((url: string) => notFound(url)) as RequestFn)
})

describe('the password reset page', () => {
  it('offers the published channels and the identifier field', async () => {
    renderDemoApp({ session: resetSession(['email', 'sms']) })

    expect(
      await screen.findByRole('heading', { name: authEn.passwordReset.title }),
    ).toBeInTheDocument()
    expect(
      await screen.findByRole('button', {
        name: authEn.passwordReset.channel.email,
      }),
    ).toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: authEn.passwordReset.channel.sms }),
    ).toBeInTheDocument()
    expect(
      screen.getByRole('textbox', { name: authEn.passwordReset.emailLabel }),
    ).toBeInTheDocument()
  })

  it('renders the unavailable notice when the deployment publishes no channel', async () => {
    renderDemoApp({ session: resetSession([]) })

    expect(
      await screen.findByRole('heading', { name: authEn.passwordReset.title }),
    ).toBeInTheDocument()
    expect(screen.getByText(authEn.passwordReset.unavailable)).toBeInTheDocument()
    expect(
      screen.queryByRole('textbox', { name: authEn.passwordReset.emailLabel }),
    ).toBeNull()
  })

  it('resumes a proved record onto the new-password fields alone', async () => {
    bindRequestFn(((url: string) => {
      if (url === '/api/v1/auth/password-reset/current') {
        return Promise.resolve({ state: 'verified', expires_in: 600 })
      }
      return notFound(url)
    }) as RequestFn)
    renderDemoApp({ session: resetSession(['email']) })

    expect(
      await screen.findByLabelText(authEn.passwordReset.newPasswordLabel),
    ).toBeInTheDocument()
    await waitFor(() =>
      expect(
        screen.queryByLabelText(authEn.passwordReset.codeLabel),
      ).toBeNull(),
    )
  })
})
