import { useState } from 'react'
import { describe, expect, it, vi } from 'vitest'
import { act, fireEvent, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { AuthnField, AuthnHumanGate, AuthnStep } from '@speed/auth-core'
import { createAppTheme } from '@speed/ui-kit'
import { makeHarness } from '@speed/test-utils/session-harness'
import { installFakeTurnstile } from '@speed/test-utils/turnstile'
import zhCN from '../locales/zh-CN.json' with { type: 'json' }
import {
  expectNoAxeViolations,
} from '../../test-utils/axe.js'
import { renderWithProviders } from '../../test-utils/render.js'
import { FlowStepFields, HumanGatedFlowStep } from './flow-step.js'
import type { OpenFlowState } from './flow.js'

function step(next: AuthnStep): OpenFlowState {
  return { seq: 1, state: 'enroll', next }
}

function render(next: AuthnStep, onUnsupported: () => void = vi.fn()) {
  return renderWithProviders(
    <FlowStepFields
      step={step(next)}
      busy={false}
      onAnswer={vi.fn()}
      onUnsupported={onUnsupported}
    />,
  )
}

function installCredentials(credentials: unknown): () => void {
  const original = Object.getOwnPropertyDescriptor(window.navigator, 'credentials')
  Object.defineProperty(window.navigator, 'credentials', {
    value: credentials,
    configurable: true,
  })
  return () => {
    if (original === undefined) {
      delete (window.navigator as { credentials?: unknown }).credentials
    } else {
      Object.defineProperty(window.navigator, 'credentials', original)
    }
  }
}

function challengeStep(fields: AuthnField[]): OpenFlowState {
  return { seq: 2, state: 'signup', next: { kind: 'challenge', fields } }
}

describe('FlowStepFields challenge fields', () => {
  it('labels the username a first login asks for and explains its shape', () => {
    renderWithProviders(
      <FlowStepFields
        step={challengeStep([
          { name: 'username', kind: 'text', required: true },
        ])}
        busy={false}
        onAnswer={vi.fn()}
        onUnsupported={vi.fn()}
      />,
    )

    const input = screen.getByLabelText(zhCN.signInFlow.usernameLabel)
    expect(input).toBeInTheDocument()
    expect(input).toHaveAccessibleDescription(zhCN.signInFlow.usernameHelper)
  })

  it('shows an unknown field name verbatim and offers it no hint', () => {
    renderWithProviders(
      <FlowStepFields
        step={challengeStep([
          { name: 'employee_id', kind: 'text', required: true },
        ])}
        busy={false}
        onAnswer={vi.fn()}
        onUnsupported={vi.fn()}
      />,
    )

    expect(screen.getByLabelText('employee_id')).toBeInTheDocument()
    expect(screen.queryByText(zhCN.signInFlow.usernameHelper)).toBeNull()
  })
})

describe('FlowStepFields challenge submit', () => {
  it('keeps the submit focusable and busy while the answer is in flight, swallowing click and form submits', async () => {
    const onAnswer = vi.fn()
    const user = userEvent.setup()
    renderWithProviders(
      <FlowStepFields
        step={challengeStep([
          { name: 'username', kind: 'text', required: true },
        ])}
        busy
        onAnswer={onAnswer}
        onUnsupported={vi.fn()}
      />,
    )
    const submit = screen.getByRole('button', {
      name: zhCN.signInFlow.submit,
    })
    expect(submit).toHaveAttribute('aria-busy', 'true')
    expect(submit).toHaveAttribute('aria-disabled', 'true')
    expect(submit).not.toBeDisabled()

    const username = screen.getByLabelText(zhCN.signInFlow.usernameLabel)
    await user.type(username, 'ada')
    await user.click(submit)
    expect(onAnswer).not.toHaveBeenCalled()

    await user.type(username, '{Enter}')
    expect(onAnswer).not.toHaveBeenCalled()

    await act(async () => {
      fireEvent.submit(submit.closest('form') as HTMLFormElement)
    })
    expect(onAnswer).not.toHaveBeenCalled()
  })

  it('sends the answer once the submit is idle again', async () => {
    const onAnswer = vi.fn()
    const user = userEvent.setup()
    renderWithProviders(
      <FlowStepFields
        step={challengeStep([
          { name: 'username', kind: 'text', required: true },
        ])}
        busy={false}
        onAnswer={onAnswer}
        onUnsupported={vi.fn()}
      />,
    )
    await user.type(
      screen.getByLabelText(zhCN.signInFlow.usernameLabel),
      'ada',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.signInFlow.submit }),
    )
    expect(onAnswer).toHaveBeenCalledWith({ username: 'ada' })
  })
})

describe('FlowStepFields account-opening submit', () => {
  const fields: AuthnField[] = [
    { name: 'username', kind: 'text', required: true },
    { name: 'password', kind: 'password', secret: true, required: true },
  ]

  it('keeps the submit focusable and busy while the answer is in flight, swallowing the form submit', async () => {
    const onAnswer = vi.fn()
    const user = userEvent.setup()
    renderWithProviders(
      <FlowStepFields
        step={{
          seq: 2,
          state: 'signup',
          next: { kind: 'challenge', fields },
        }}
        busy
        onAnswer={onAnswer}
        onUnsupported={vi.fn()}
      />,
    )
    const submit = screen.getByRole('button', {
      name: zhCN.signInFlow.signupAccountSubmit,
    })
    expect(submit).toHaveAttribute('aria-busy', 'true')
    expect(submit).toHaveAttribute('aria-disabled', 'true')
    expect(submit).not.toBeDisabled()

    await user.type(
      screen.getByLabelText(zhCN.signInFlow.usernameLabel),
      'ada',
    )
    await user.type(
      screen.getByLabelText(zhCN.passwordSignIn.passwordLabel),
      'correct-horse',
    )
    const confirmation = screen.getByLabelText(
      zhCN.register.confirmPasswordLabel,
    )
    await user.type(confirmation, 'correct-horse')
    await user.type(confirmation, '{Enter}')
    expect(onAnswer).not.toHaveBeenCalled()

    await act(async () => {
      fireEvent.submit(submit.closest('form') as HTMLFormElement)
    })
    expect(onAnswer).not.toHaveBeenCalled()
  })
})

describe('FlowStepFields username step', () => {
  const fields: AuthnField[] = [{ name: 'username', kind: 'text', required: true }]

  function renderSignup(failure: string | null = null) {
    return renderWithProviders(
      <FlowStepFields
        step={challengeStep(fields)}
        busy={false}
        onAnswer={vi.fn()}
        onUnsupported={vi.fn()}
        failure={failure}
      />,
    )
  }

  it('renders a titled step with its explanation, rule help and submit', () => {
    renderSignup()

    expect(
      screen.getByRole('heading', {
        level: 2,
        name: zhCN.signInFlow.signupTitle,
      }),
    ).toBeInTheDocument()
    expect(
      screen.getByText(zhCN.signInFlow.signupDescription),
    ).toBeInTheDocument()
    expect(
      screen.getByLabelText(zhCN.signInFlow.usernameLabel),
    ).toHaveAccessibleDescription(zhCN.signInFlow.usernameHelper)
    expect(
      screen.getByRole('button', { name: zhCN.signInFlow.submit }),
    ).toBeInTheDocument()
  })

  it.each([
    ['authn.username_taken', zhCN.signInFlow.usernameTaken],
    ['authn.username_reserved', zhCN.signInFlow.usernameReserved],
    ['authn.username_invalid', zhCN.signInFlow.usernameInvalid],
  ])('renders %s under the username field', (code, text) => {
    renderSignup(code)

    const field = screen.getByLabelText(zhCN.signInFlow.usernameLabel)
    expect(field).toHaveAttribute('aria-invalid', 'true')
    expect(field).toHaveAccessibleDescription(text)
    expect(field).toHaveFocus()
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('leaves the field without an error for any other code', () => {
    for (const code of ['authn.bad_request', 'authn.throttled']) {
      const { unmount } = renderSignup(code)
      const field = screen.getByLabelText(zhCN.signInFlow.usernameLabel)
      expect(field).not.toHaveAttribute('aria-invalid', 'true')
      expect(field).toHaveAccessibleDescription(zhCN.signInFlow.usernameHelper)
      unmount()
    }
  })
})

describe('FlowStepFields social account-opening face', () => {
  const socialFields: AuthnField[] = [
    { name: 'username', kind: 'text', required: true },
    { name: 'password', kind: 'password', secret: true, required: true },
  ]

  interface FaceIdentity {
    readonly method?: string
    readonly displayName?: string
    readonly email?: string
  }

  function socialStep(identity: FaceIdentity = {}): OpenFlowState {
    return {
      seq: 2,
      state: 'signup',
      next: {
        kind: 'challenge',
        fields: socialFields,
        ...(identity.method === undefined
          ? {}
          : { provided_method: identity.method }),
        ...(identity.displayName === undefined
          ? {}
          : { provided_display_name: identity.displayName }),
        ...(identity.email === undefined
          ? {}
          : { provided_email: identity.email }),
      },
    }
  }

  function renderFace(
    options: {
      readonly method?: string
      readonly displayName?: string
      readonly email?: string
      readonly failure?: string | null
      readonly onAnswer?: (input: Record<string, string>) => void
    } = {},
  ) {
    return renderWithProviders(
      <FlowStepFields
        step={socialStep({
          method: options.method,
          displayName: options.displayName,
          email: options.email,
        })}
        busy={false}
        onAnswer={options.onAnswer ?? vi.fn()}
        onUnsupported={vi.fn()}
        failure={options.failure ?? null}
      />,
    )
  }

  function identitySentence(options: {
    readonly provider: string
    readonly name?: string
  }): string {
    if (options.name === undefined) {
      return zhCN.signInFlow.signupIdentityWithoutName.replace(
        '{{provider}}',
        options.provider,
      )
    }
    return zhCN.signInFlow.signupIdentity
      .replace('{{provider}}', options.provider)
      .replace('{{name}}', options.name)
  }

  function findIdentityParagraph(options: {
    readonly provider: string
    readonly name?: string
  }): HTMLElement | null {
    const expected = identitySentence(options)
    return screen.queryByText((_content, element) => {
      if (element === null || element.tagName !== 'P') {
        return false
      }
      return (
        (element.textContent ?? '').replace(/\s+/g, ' ').trim() === expected
      )
    })
  }

  const emailLabel = zhCN.register.emailLabel

  it('renders no step title, the instruction sentence and the submit row', () => {
    renderFace({
      method: 'google',
      displayName: 'Ada Lovelace',
      email: 'ada@example.com',
    })

    expect(screen.queryByRole('heading')).toBeNull()
    expect(
      screen.getByText(zhCN.signInFlow.signupAccountInstruction, {
        selector: 'p',
      }),
    ).toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: zhCN.signInFlow.signupAccountSubmit }),
    ).toBeInTheDocument()
  })

  it('composes the identity sentence around the provider name', () => {
    renderFace({
      method: 'google',
      displayName: 'Ada Lovelace',
      email: 'ada@example.com',
    })

    expect(
      findIdentityParagraph({
        provider: zhCN.social.provider.google,
        name: 'Ada Lovelace',
      }),
    ).toBeInTheDocument()
  })

  it('paints the provider account name in the primary color at weight 600', () => {
    renderFace({
      method: 'google',
      displayName: 'Ada Lovelace',
      email: 'ada@example.com',
    })

    const { palette } = createAppTheme().theme
    expect(screen.getByText('Ada Lovelace', { selector: 'span' })).toHaveStyle({
      color: palette.text.primary,
      fontWeight: '600',
    })
  })

  it('orders the fields username, address, password, confirmation', () => {
    renderFace({
      method: 'google',
      displayName: 'Ada Lovelace',
      email: 'ada@example.com',
    })

    const username = screen.getByLabelText(zhCN.signInFlow.usernameLabel)
    const address = screen.getByRole('textbox', { name: emailLabel })
    const password = screen.getByLabelText(zhCN.passwordSignIn.passwordLabel)
    const confirm = screen.getByLabelText(zhCN.register.confirmPasswordLabel)
    for (const [before, after] of [
      [username, address],
      [address, password],
      [password, confirm],
    ] as const) {
      expect(
        before.compareDocumentPosition(after) &
          Node.DOCUMENT_POSITION_FOLLOWING,
      ).toBeTruthy()
    }
  })

  it('renders the provider address as a read-only text box, not an editable input', () => {
    renderFace({
      method: 'google',
      displayName: 'Ada Lovelace',
      email: 'ada@example.com',
    })

    const address = screen.getByRole('textbox', { name: emailLabel })
    expect(address).toHaveAttribute('aria-readonly', 'true')
    expect(address).toHaveTextContent('ada@example.com')
    expect(screen.queryByDisplayValue('ada@example.com')).toBeNull()
    expect(
      screen.getByText(
        zhCN.signInFlow.signupEmailSource.replace(
          '{{provider}}',
          zhCN.social.provider.google,
        ),
      ),
    ).toBeInTheDocument()
    address.focus()
    expect(address).toHaveFocus()
  })

  it('keeps a long provider address whole, wrapping instead of truncating', () => {
    const longAddress = `${'a'.repeat(80)}@example-domain.test`
    renderFace({
      method: 'google',
      displayName: 'Ada Lovelace',
      email: longAddress,
    })

    const address = screen.getByRole('textbox', { name: emailLabel })
    expect(address.textContent?.trim()).toBe(longAddress)
    expect(address).toHaveStyle({ overflowWrap: 'anywhere' })
  })

  it('names the method through the sign-in vocabulary', () => {
    renderFace({ method: 'github' })

    expect(
      findIdentityParagraph({ provider: zhCN.social.provider.github }),
    ).toBeInTheDocument()
  })

  it('keeps the two WeChat modes apart on the face', () => {
    for (const [method, label] of [
      ['wechat.mp', zhCN.social.provider.wechatOfficialAccount],
      ['wechat.web', zhCN.social.provider.wechatWeb],
    ] as const) {
      const { unmount } = renderFace({ method })
      expect(
        findIdentityParagraph({ provider: label }),
      ).toBeInTheDocument()
      expect(
        findIdentityParagraph({ provider: zhCN.social.provider.wechat }),
      ).toBeNull()
      unmount()
    }
  })

  it('shows a method outside the vocabulary verbatim', () => {
    renderFace({ method: 'acme.sso' })

    expect(
      findIdentityParagraph({ provider: 'acme.sso' }),
    ).toBeInTheDocument()
  })

  it('falls back to naming the method alone when the provider gave no name', () => {
    renderFace({ method: 'google', email: 'ada@example.com' })

    expect(
      findIdentityParagraph({ provider: zhCN.social.provider.google }),
    ).toBeInTheDocument()
    expect(screen.queryByText('Ada Lovelace')).toBeNull()
  })

  it('renders no identity sentence when the step carries no method', () => {
    renderFace({ displayName: 'Ada Lovelace', email: 'ada@example.com' })

    const leadPrefix = zhCN.signInFlow.signupIdentity.slice(
      0,
      zhCN.signInFlow.signupIdentity.indexOf('{{provider}}'),
    )
    expect(screen.queryByText((text) => text.includes(leadPrefix))).toBeNull()
    expect(
      screen.getByText(zhCN.signInFlow.signupAccountInstruction, {
        selector: 'p',
      }),
    ).toBeInTheDocument()
  })

  it('leaves the address block out when the provider shared none', () => {
    renderFace({ method: 'google', displayName: 'Ada Lovelace' })

    expect(screen.queryByRole('textbox', { name: emailLabel })).toBeNull()
    const username = screen.getByLabelText(zhCN.signInFlow.usernameLabel)
    const password = screen.getByLabelText(zhCN.passwordSignIn.passwordLabel)
    const confirm = screen.getByLabelText(zhCN.register.confirmPasswordLabel)
    expect(
      username.compareDocumentPosition(password) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy()
    expect(
      password.compareDocumentPosition(confirm) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy()
  })

  it('compares the confirmation locally and sends nothing when they differ', async () => {
    const onAnswer = vi.fn()
    renderFace({ email: 'ada@example.com', onAnswer })

    await userEvent.setup().type(
      screen.getByLabelText(zhCN.signInFlow.usernameLabel),
      'ada',
    )
    await userEvent.setup().type(
      screen.getByLabelText(zhCN.passwordSignIn.passwordLabel),
      'correct-horse',
    )
    await userEvent.setup().type(
      screen.getByLabelText(zhCN.register.confirmPasswordLabel),
      'different-horse',
    )
    await userEvent
      .setup()
      .click(
        screen.getByRole('button', {
          name: zhCN.signInFlow.signupAccountSubmit,
        }),
      )

    expect(onAnswer).not.toHaveBeenCalled()
    const confirm = screen.getByLabelText(zhCN.register.confirmPasswordLabel)
    expect(confirm).toHaveAttribute('aria-invalid', 'true')
    expect(confirm).toHaveAccessibleDescription(
      zhCN.signInFlow.signupPasswordMismatch,
    )
    await userEvent.setup().clear(confirm)
    await userEvent
      .setup()
      .type(confirm, 'correct-horse')
    await userEvent
      .setup()
      .click(
        screen.getByRole('button', {
          name: zhCN.signInFlow.signupAccountSubmit,
        }),
      )
    expect(onAnswer).toHaveBeenCalledWith({
      username: 'ada',
      password: 'correct-horse',
    })
  })

  it('renders a password-policy refusal under the password field', () => {
    renderFace({ email: 'ada@example.com', failure: 'authn.bad_request' })

    const password = screen.getByLabelText(zhCN.passwordSignIn.passwordLabel)
    expect(password).toHaveAttribute('aria-invalid', 'true')
    expect(password).toHaveAccessibleDescription(
      zhCN.signInFlow.signupPasswordPolicy,
    )
    expect(password).toHaveFocus()
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it.each([
    ['authn.username_taken', zhCN.signInFlow.usernameTaken],
    ['authn.username_reserved', zhCN.signInFlow.usernameReserved],
    ['authn.username_invalid', zhCN.signInFlow.usernameInvalid],
  ])('renders %s under the username field', (code, text) => {
    renderFace({ email: 'ada@example.com', failure: code })

    const username = screen.getByLabelText(zhCN.signInFlow.usernameLabel)
    expect(username).toHaveAttribute('aria-invalid', 'true')
    expect(username).toHaveAccessibleDescription(text)
    expect(username).toHaveFocus()
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('requires the password before the account can be opened', async () => {
    const onAnswer = vi.fn()
    renderFace({ method: 'google', onAnswer })

    const user = userEvent.setup()
    await user.type(
      screen.getByLabelText(zhCN.signInFlow.usernameLabel),
      'ada',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.signInFlow.signupAccountSubmit }),
    )

    expect(onAnswer).not.toHaveBeenCalled()
    expect(
      screen.getByLabelText(zhCN.passwordSignIn.passwordLabel),
    ).toHaveAttribute('aria-invalid', 'true')
  })

  it('renders no display-name field on the social face', () => {
    renderFace({ method: 'google', displayName: 'Ada Lovelace' })

    expect(
      screen.queryByLabelText(zhCN.register.displayNameLabel),
    ).toBeNull()
  })

  it('keeps the username-only face for enterprise signup', () => {
    renderWithProviders(
      <FlowStepFields
        step={challengeStep([
          { name: 'username', kind: 'text', required: true },
        ])}
        busy={false}
        onAnswer={vi.fn()}
        onUnsupported={vi.fn()}
      />,
    )

    expect(
      screen.getByRole('heading', {
        level: 2,
        name: zhCN.signInFlow.signupTitle,
      }),
    ).toBeInTheDocument()
    expect(
      screen.getByText(zhCN.signInFlow.signupDescription),
    ).toBeInTheDocument()
    expect(
      screen.queryByText(zhCN.signInFlow.signupAccountInstruction),
    ).toBeNull()
    expect(screen.queryByRole('textbox', { name: emailLabel })).toBeNull()
    expect(
      screen.queryByLabelText(zhCN.passwordSignIn.passwordLabel),
    ).toBeNull()
    expect(
      screen.queryByLabelText(zhCN.register.confirmPasswordLabel),
    ).toBeNull()
  })
})

describe('FlowStepFields phone signup step', () => {
  const phoneFields: AuthnField[] = [
    { name: 'username', kind: 'text', required: true },
    { name: 'display_name', kind: 'text' },
    { name: 'password', kind: 'password', secret: true, required: true },
  ]

  function renderPhoneSignup(
    onAnswer: (input: Record<string, string>) => void = vi.fn(),
  ) {
    return renderWithProviders(
      <FlowStepFields
        step={challengeStep(phoneFields)}
        busy={false}
        onAnswer={onAnswer}
        onUnsupported={vi.fn()}
      />,
    )
  }

  async function fillAndSubmit(displayName?: string) {
    const user = userEvent.setup()
    await user.type(
      screen.getByLabelText(zhCN.signInFlow.usernameLabel),
      'ada',
    )
    if (displayName !== undefined) {
      await user.type(
        screen.getByLabelText(zhCN.register.displayNameLabel),
        displayName,
      )
    }
    await user.type(
      screen.getByLabelText(zhCN.passwordSignIn.passwordLabel),
      'correct-horse',
    )
    await user.type(
      screen.getByLabelText(zhCN.register.confirmPasswordLabel),
      'correct-horse',
    )
    await user.click(
      screen.getByRole('button', {
        name: zhCN.signInFlow.signupAccountSubmit,
      }),
    )
  }

  it('renders the optional display-name field the server offers', () => {
    renderPhoneSignup()

    const username = screen.getByLabelText(zhCN.signInFlow.usernameLabel)
    const displayName = screen.getByLabelText(zhCN.register.displayNameLabel)

    expect(displayName).not.toBeRequired()
    expect(displayName).toHaveAccessibleDescription(
      zhCN.signInFlow.signupDisplayNameHelp,
    )
    expect(displayName).toHaveAttribute('autocomplete', 'name')
    expect(displayName).toHaveAttribute('maxlength', '100')
    expect(
      username.compareDocumentPosition(displayName) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy()
  })

  it('orders the phone step username, display name, password, confirmation', () => {
    renderPhoneSignup()

    const username = screen.getByLabelText(zhCN.signInFlow.usernameLabel)
    const displayName = screen.getByLabelText(zhCN.register.displayNameLabel)
    const password = screen.getByLabelText(zhCN.passwordSignIn.passwordLabel)
    const confirm = screen.getByLabelText(zhCN.register.confirmPasswordLabel)
    for (const [before, after] of [
      [username, displayName],
      [displayName, password],
      [password, confirm],
    ] as const) {
      expect(
        before.compareDocumentPosition(after) &
          Node.DOCUMENT_POSITION_FOLLOWING,
      ).toBeTruthy()
    }
  })

  it('sends a filled display name with the signup answer', async () => {
    const onAnswer = vi.fn()
    renderPhoneSignup(onAnswer)

    await fillAndSubmit('Ada Lovelace')

    expect(onAnswer).toHaveBeenCalledWith({
      username: 'ada',
      display_name: 'Ada Lovelace',
      password: 'correct-horse',
    })
  })

  it('omits the optional display name when it is left empty', async () => {
    const onAnswer = vi.fn()
    renderPhoneSignup(onAnswer)

    await fillAndSubmit()

    expect(onAnswer).toHaveBeenCalledWith({
      username: 'ada',
      password: 'correct-horse',
    })
  })

  it('omits an optional display name that is only whitespace', async () => {
    const onAnswer = vi.fn()
    renderPhoneSignup(onAnswer)

    await fillAndSubmit('   ')

    expect(onAnswer).toHaveBeenCalledWith({
      username: 'ada',
      password: 'correct-horse',
    })
  })
})

describe('FlowStepFields password account-opening face', () => {
  const accountFields: AuthnField[] = [
    { name: 'username', kind: 'text', required: true },
    { name: 'display_name', kind: 'text' },
    { name: 'password', kind: 'password', secret: true, required: true },
  ]

  it('renders and sends a display name the server asks for beside the password', async () => {
    const onAnswer = vi.fn()
    renderWithProviders(
      <FlowStepFields
        step={challengeStep(accountFields)}
        busy={false}
        onAnswer={onAnswer}
        onUnsupported={vi.fn()}
      />,
    )
    const user = userEvent.setup()

    const displayName = screen.getByLabelText(zhCN.register.displayNameLabel)
    const password = screen.getByLabelText(zhCN.passwordSignIn.passwordLabel)
    expect(
      displayName.compareDocumentPosition(password) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy()

    await user.type(
      screen.getByLabelText(zhCN.signInFlow.usernameLabel),
      'ada',
    )
    await user.type(displayName, 'Ada Lovelace')
    await user.type(password, 'correct-horse')
    await user.type(
      screen.getByLabelText(zhCN.register.confirmPasswordLabel),
      'correct-horse',
    )
    await user.click(
      screen.getByRole('button', {
        name: zhCN.signInFlow.signupAccountSubmit,
      }),
    )

    expect(onAnswer).toHaveBeenCalledWith({
      username: 'ada',
      display_name: 'Ada Lovelace',
      password: 'correct-horse',
    })
  })
})

describe('FlowStepFields labels', () => {
  it('translates known factor kinds and shows an unknown kind as given', () => {
    render({
      kind: 'factor',
      factors: [
        { id: 'f1', kind: 'totp' },
        { id: 'f2', kind: 'magic' },
      ],
    })

    expect(
      screen.getByRole('button', { name: zhCN.signInFlow.factorKind.totp }),
    ).toBeTruthy()
    expect(screen.getByRole('button', { name: 'magic' })).toBeTruthy()
    expect(
      screen.queryByRole('button', { name: zhCN.signInFlow.factorKind.other }),
    ).toBeNull()
  })

  it('offers the administrator-reset door under the factor choices', () => {
    render({ kind: 'factor', factors: [{ id: 'f1', kind: 'totp' }] })

    expect(
      screen.getByText(zhCN.signInFlow.lostFactorHint),
    ).toBeInTheDocument()
  })

  it('hides the reset door when the step offers no factor at all', () => {
    render({ kind: 'factor', factors: [] })

    expect(screen.queryByText(zhCN.signInFlow.lostFactorHint)).toBeNull()
  })

  it('reads a sole factor as its kind even when it carries a name', () => {
    render({
      kind: 'factor',
      factors: [{ id: 'f1', kind: 'totp', label: 'My app' }],
    })

    expect(
      screen.getByRole('button', { name: zhCN.signInFlow.factorKind.totp }),
    ).toBeTruthy()
    expect(screen.queryByRole('button', { name: 'My app' })).toBeNull()
  })

  it('labels same-named factors of different kinds by their kinds', () => {
    render({
      kind: 'factor',
      factors: [
        { id: 'f1', kind: 'webauthn', label: 'Chrome · macOS' },
        { id: 'f2', kind: 'totp', label: 'Chrome · macOS' },
      ],
    })

    expect(
      screen.getByRole('button', { name: zhCN.signInFlow.factorKind.webauthn }),
    ).toBeTruthy()
    expect(
      screen.getByRole('button', { name: zhCN.signInFlow.factorKind.totp }),
    ).toBeTruthy()
    expect(
      screen.queryAllByRole('button', { name: 'Chrome · macOS' }),
    ).toHaveLength(0)
  })

  it('distinguishes a repeated kind by name and by ordinal', () => {
    render({
      kind: 'factor',
      factors: [
        { id: 'f1', kind: 'webauthn', label: 'Work MacBook' },
        { id: 'f2', kind: 'webauthn' },
      ],
    })

    const kind = zhCN.signInFlow.factorKind.webauthn
    const named = zhCN.signInFlow.factorOptionNamed
      .replace('{{kind}}', kind)
      .replace('{{name}}', 'Work MacBook')
    const ordinal = zhCN.signInFlow.factorOptionOrdinal
      .replace('{{kind}}', kind)
      .replace('{{position}}', '2')
    expect(screen.getByRole('button', { name: named })).toBeTruthy()
    expect(screen.getByRole('button', { name: ordinal })).toBeTruthy()
  })

  it('translates a known enroll kind and shows an unknown kind as given', () => {
    render({ kind: 'enroll', kinds: ['webauthn', 'magic'] })

    expect(
      screen.getByRole('button', { name: zhCN.signInFlow.factorKind.webauthn }),
    ).toBeTruthy()
    expect(screen.getByRole('button', { name: 'magic' })).toBeTruthy()
  })
})

describe('FlowStepFields passkey arrival', () => {
  it('starts an assertion ceremony on arrival and does not restart it on a re-render', async () => {
    const get = vi.fn().mockReturnValue(new Promise(() => {}))
    const restore = installCredentials({ get, create: vi.fn() })
    try {
      const onAnswer = vi.fn()
      function Harness() {
        const [, setTick] = useState(0)
        return (
          <>
            <button type="button" onClick={() => setTick((tick) => tick + 1)}>
              rerender
            </button>
            <FlowStepFields
              step={step({
                kind: 'webauthn',
                options: { challenge: 'Y2hhbGxlbmdl' },
              })}
              busy={false}
              onAnswer={onAnswer}
              onUnsupported={vi.fn()}
            />
          </>
        )
      }
      renderWithProviders(<Harness />)

      expect(
        await screen.findByText(zhCN.signInFlow.passkeyWaiting),
      ).toBeInTheDocument()
      expect(
        screen.queryByRole('button', { name: zhCN.signInFlow.submit }),
      ).toBeNull()
      expect(get).toHaveBeenCalledTimes(1)

      await userEvent
        .setup()
        .click(screen.getByRole('button', { name: 'rerender' }))
      expect(get).toHaveBeenCalledTimes(1)
      expect(onAnswer).not.toHaveBeenCalled()
    } finally {
      restore()
    }
  })

  it('restarts the arrival ceremony when the server re-sends the same challenge', async () => {
    const get = vi.fn().mockReturnValue(new Promise(() => {}))
    const restore = installCredentials({ get, create: vi.fn() })
    try {
      function Harness() {
        const [seq, setSeq] = useState(1)
        return (
          <>
            <button type="button" onClick={() => setSeq((value) => value + 1)}>
              resend
            </button>
            <FlowStepFields
              step={{
                seq,
                state: 'second',
                next: {
                  kind: 'webauthn',
                  options: { challenge: 'Y2hhbGxlbmdl' },
                },
              }}
              busy={false}
              onAnswer={vi.fn()}
              onUnsupported={vi.fn()}
            />
          </>
        )
      }
      renderWithProviders(<Harness />)

      expect(
        await screen.findByText(zhCN.signInFlow.passkeyWaiting),
      ).toBeInTheDocument()
      expect(get).toHaveBeenCalledTimes(1)

      await userEvent
        .setup()
        .click(screen.getByRole('button', { name: 'resend' }))
      await waitFor(() => expect(get).toHaveBeenCalledTimes(2))
    } finally {
      restore()
    }
  })

  it('starts no assertion ceremony when a genuinely different step arrives', async () => {
    const get = vi.fn().mockReturnValue(new Promise(() => {}))
    const restore = installCredentials({ get, create: vi.fn() })
    try {
      function Harness() {
        const [step, setStep] = useState<AuthnStep>({
          kind: 'webauthn',
          options: { challenge: 'Y2hhbGxlbmdl' },
        })
        return (
          <>
            <button
              type="button"
              onClick={() =>
                setStep({
                  kind: 'challenge',
                  fields: [{ name: 'code', kind: 'code' }],
                })
              }
            >
              different
            </button>
            <FlowStepFields
              step={{ seq: 2, state: 'second', next: step }}
              busy={false}
              onAnswer={vi.fn()}
              onUnsupported={vi.fn()}
            />
          </>
        )
      }
      renderWithProviders(<Harness />)

      expect(
        await screen.findByText(zhCN.signInFlow.passkeyWaiting),
      ).toBeInTheDocument()
      expect(get).toHaveBeenCalledTimes(1)

      await userEvent
        .setup()
        .click(screen.getByRole('button', { name: 'different' }))
      expect(get).toHaveBeenCalledTimes(1)
      expect(
        screen.getByLabelText(zhCN.smsSignIn.codeLabel),
      ).toBeInTheDocument()
    } finally {
      restore()
    }
  })

  it('waits for a click before a registration ceremony starts', async () => {
    const create = vi.fn().mockReturnValue(new Promise(() => {}))
    const restore = installCredentials({ get: vi.fn(), create })
    try {
      render({
        kind: 'webauthn',
        options: {
          challenge: 'Y2hhbGxlbmdl',
          user: { id: 'AQID', name: 'ada' },
        },
      })
      expect(
        screen.getByRole('button', { name: zhCN.signInFlow.submit }),
      ).toBeTruthy()
      expect(create).not.toHaveBeenCalled()

      await userEvent
        .setup()
        .click(screen.getByRole('button', { name: zhCN.signInFlow.submit }))
      await waitFor(() => expect(create).toHaveBeenCalledTimes(1))
    } finally {
      restore()
    }
  })
})

describe('FlowStepFields passkey controls', () => {
  const REGISTER_OPTIONS = {
    challenge: 'Y2hhbGxlbmdl',
    user: { id: 'AQID', name: 'ada' },
  }

  it('holds the cancel control unavailable, without a reason, while the answer is in flight', async () => {
    const create = vi.fn().mockReturnValue(new Promise(() => {}))
    const restore = installCredentials({ get: vi.fn(), create })
    let setBusy: ((busy: boolean) => void) | null = null
    try {
      function Harness() {
        const [busy, updateBusy] = useState(false)
        setBusy = updateBusy
        return (
          <FlowStepFields
            step={step({ kind: 'webauthn', options: REGISTER_OPTIONS })}
            busy={busy}
            onAnswer={vi.fn()}
            onUnsupported={vi.fn()}
            onClientFailure={vi.fn()}
          />
        )
      }
      const user = userEvent.setup()
      renderWithProviders(<Harness />)

      await user.click(
        screen.getByRole('button', { name: zhCN.signInFlow.submit }),
      )
      const cancel = await screen.findByRole('button', {
        name: zhCN.signInFlow.passkeyCancel,
      })
      expect(cancel).not.toBeDisabled()
      expect(cancel).not.toHaveAttribute('aria-disabled')

      act(() => setBusy?.(true))
      expect(cancel).toHaveAttribute('aria-disabled', 'true')
      expect(cancel).not.toHaveAttribute('aria-busy')
      expect(cancel).not.toHaveAttribute('aria-describedby')
      expect(cancel).not.toBeDisabled()

      await user.click(cancel)
      expect(cancel).toBeInTheDocument()
      expect(
        await screen.findByText(zhCN.signInFlow.passkeyWaiting),
      ).toBeInTheDocument()
    } finally {
      restore()
    }
  })

  it('keeps the start control focusable and busy while the answer is in flight, starting no ceremony', async () => {
    const create = vi.fn()
    const restore = installCredentials({ get: vi.fn(), create })
    try {
      const user = userEvent.setup()
      renderWithProviders(
        <FlowStepFields
          step={step({ kind: 'webauthn', options: REGISTER_OPTIONS })}
          busy
          onAnswer={vi.fn()}
          onUnsupported={vi.fn()}
        />,
      )
      const start = screen.getByRole('button', {
        name: zhCN.signInFlow.submit,
      })
      expect(start).toHaveAttribute('aria-busy', 'true')
      expect(start).toHaveAttribute('aria-disabled', 'true')
      expect(start).not.toBeDisabled()

      start.focus()
      expect(start).toHaveFocus()
      await user.click(start)
      expect(create).not.toHaveBeenCalled()
    } finally {
      restore()
    }
  })
})

const TOTP_URI =
  'otpauth://totp/Example%20Co:alice%40example.com?secret=JBSWY3DPEHPK3PXP&issuer=Example%20Co'
const TOTP_SECRET_DISPLAY = 'JBSW Y3DP EHPK 3PXP'

describe('FlowStepFields TOTP enrolment', () => {
  function renderTotp(onAnswer: (input: Record<string, string>) => void = vi.fn()) {
    return renderWithProviders(
      <FlowStepFields
        step={step({ kind: 'webauthn', options: { otpauth_uri: TOTP_URI } })}
        busy={false}
        onAnswer={onAnswer}
        onUnsupported={vi.fn()}
      />,
    )
  }

  it('renders the otpauth URI as a QR symbol, never as text', () => {
    renderTotp()

    expect(
      screen.getByRole('img', { name: zhCN.signInFlow.totpQrLabel }),
    ).toBeInTheDocument()
    expect(screen.getByText(zhCN.signInFlow.enrollTotpHelp)).toBeInTheDocument()
    expect(
      screen.getByLabelText(zhCN.signInFlow.codeLabel),
    ).toBeInTheDocument()
    expect(screen.queryByText(/otpauth:\/\//)).toBeNull()
  })

  it('draws the symbol from the step’s own URI, not a fixed decoration', () => {
    const first = renderTotp()
    const firstPath = first.container
      .querySelector('svg[role="img"] path')
      ?.getAttribute('d')
    expect(firstPath).toBeTruthy()
    first.unmount()

    const other = renderWithProviders(
      <FlowStepFields
        step={step({
          kind: 'webauthn',
          options: {
            otpauth_uri: 'otpauth://totp/speed:ada?secret=KRSXG5CTMVRXEZLU',
          },
        })}
        busy={false}
        onAnswer={vi.fn()}
        onUnsupported={vi.fn()}
      />,
    )
    const otherPath = other.container
      .querySelector('svg[role="img"] path')
      ?.getAttribute('d')
    expect(otherPath).toBeTruthy()
    expect(otherPath).not.toBe(firstPath)
  })

  it('shows the parsed secret and parameters behind the manual-entry disclosure', async () => {
    const user = userEvent.setup()
    renderTotp()

    await user.click(
      screen.getByRole('button', { name: zhCN.signInFlow.totpManualToggle }),
    )
    expect(screen.getByText(zhCN.signInFlow.totpSecretLabel)).toBeInTheDocument()
    expect(screen.getByText(TOTP_SECRET_DISPLAY)).toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: zhCN.signInFlow.totpCopyLabel }),
    ).toBeInTheDocument()
    for (const [label, value] of [
      [zhCN.signInFlow.totpAlgorithm, 'SHA1'],
      [zhCN.signInFlow.totpDigits, '6'],
      [zhCN.signInFlow.totpPeriod, '30'],
    ] as const) {
      expect(screen.getByText(label)).toBeInTheDocument()
      expect(screen.getByText(value)).toBeInTheDocument()
    }
    expect(screen.queryByText(/otpauth:\/\//)).toBeNull()
  })

  it('keeps the code field when the URI cannot be parsed', () => {
    renderWithProviders(
      <FlowStepFields
        step={step({
          kind: 'webauthn',
          options: { otpauth_uri: 'otpauth://hotp/acme?secret=abc' },
        })}
        busy={false}
        onAnswer={vi.fn()}
        onUnsupported={vi.fn()}
      />,
    )

    expect(screen.queryByRole('img')).toBeNull()
    expect(screen.queryByText(zhCN.signInFlow.enrollTotpHelp)).toBeNull()
    expect(
      screen.getByLabelText(zhCN.signInFlow.codeLabel),
    ).toBeInTheDocument()
    expect(screen.queryByText(/otpauth:\/\//)).toBeNull()
  })

  it('answers the flow with the typed code', async () => {
    const onAnswer = vi.fn()
    const user = userEvent.setup()
    renderTotp(onAnswer)

    await user.type(
      screen.getByLabelText(zhCN.signInFlow.codeLabel),
      '123456',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.signInFlow.submit }),
    )

    expect(onAnswer).toHaveBeenCalledWith({ code: '123456' })
  })

  it('keeps the confirm control focusable, explains an empty code and sends nothing', async () => {
    const onAnswer = vi.fn()
    const user = userEvent.setup()
    renderTotp(onAnswer)

    const confirm = screen.getByRole('button', {
      name: zhCN.signInFlow.submit,
    })
    expect(confirm).toHaveAttribute('aria-disabled', 'true')
    expect(confirm).not.toBeDisabled()
    expect(confirm).toHaveAccessibleDescription(
      zhCN.signInFlow.totpCodeRequired,
    )

    confirm.focus()
    expect(confirm).toHaveFocus()
    await user.click(confirm)
    expect(onAnswer).not.toHaveBeenCalled()

    await user.type(screen.getByLabelText(zhCN.signInFlow.codeLabel), '123456')
    expect(confirm).not.toHaveAttribute('aria-disabled', 'true')
    await user.click(confirm)
    expect(onAnswer).toHaveBeenCalledWith({ code: '123456' })
  })

  it('marks the confirm control busy, not natively disabled, while the answer is in flight', () => {
    renderWithProviders(
      <FlowStepFields
        step={step({ kind: 'webauthn', options: { otpauth_uri: TOTP_URI } })}
        busy
        onAnswer={vi.fn()}
        onUnsupported={vi.fn()}
      />,
    )

    const confirm = screen.getByRole('button', {
      name: zhCN.signInFlow.submit,
    })
    expect(confirm).toHaveAttribute('aria-busy', 'true')
    expect(confirm).toHaveAttribute('aria-disabled', 'true')
    expect(confirm).not.toBeDisabled()
    expect(confirm).not.toHaveAccessibleDescription(
      zhCN.signInFlow.totpCodeRequired,
    )
  })

  it('passes axe with the manual-entry disclosure open', async () => {
    const user = userEvent.setup()
    renderWithProviders(
      <>
        <h1>Two-factor setup</h1>
        <FlowStepFields
          step={step({ kind: 'webauthn', options: { otpauth_uri: TOTP_URI } })}
          busy={false}
          onAnswer={vi.fn()}
          onUnsupported={vi.fn()}
        />
      </>,
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.signInFlow.totpManualToggle }),
    )
    await expectNoAxeViolations({
      decidedInBrowser: 'flowStepFieldsManualDisclosure',
    })
  })
})

describe('FlowStepFields passkey failure wording', () => {
  function renderPasskey(
    options: unknown,
    onClientFailure: (key: string | null) => void,
  ) {
    return renderWithProviders(
      <FlowStepFields
        step={step({ kind: 'webauthn', options })}
        busy={false}
        onAnswer={vi.fn()}
        onUnsupported={vi.fn()}
        clientFailure={null}
        onClientFailure={onClientFailure}
      />,
    )
  }

  it('names an authenticator that already holds the credential', async () => {
    const create = vi
      .fn()
      .mockRejectedValue(
        new DOMException(
          'the authenticator already holds this credential',
          'InvalidStateError',
        ),
      )
    const restore = installCredentials({ create, get: vi.fn() })
    try {
      const onClientFailure = vi.fn()
      renderPasskey(
        { challenge: 'Y2hhbGxlbmdl', user: { id: 'AQID', name: 'ada' } },
        onClientFailure,
      )

      await userEvent
        .setup()
        .click(screen.getByRole('button', { name: zhCN.signInFlow.submit }))

      await waitFor(() =>
        expect(onClientFailure).toHaveBeenCalledWith(
          'signInFlow.passkeyAlreadyRegistered',
        ),
      )
    } finally {
      restore()
    }
  })

  it('renders the retry through the shared primary action mapping', async () => {
    renderWithProviders(
      <FlowStepFields
        step={step({
          kind: 'webauthn',
          options: { challenge: 'Y2hhbGxlbmdl' },
        })}
        busy={false}
        onAnswer={vi.fn()}
        onUnsupported={vi.fn()}
        clientFailure="signInFlow.passkeyFailedSignIn"
        onClientFailure={vi.fn()}
      />,
    )

    const retry = await screen.findByRole('button', {
      name: zhCN.signInFlow.passkeyRetry,
    })
    expect(retry).toHaveClass('MuiButton-contained')
  })

  it('names the ceremony in the generic failure sentence', async () => {
    const boom = (): Promise<never> => Promise.reject(new Error('boom'))
    const restore = installCredentials({
      create: vi.fn().mockImplementation(boom),
      get: vi.fn().mockImplementation(boom),
    })
    try {
      const registerFailure = vi.fn()
      const register = renderPasskey(
        { challenge: 'Y2hhbGxlbmdl', user: { id: 'AQID', name: 'ada' } },
        registerFailure,
      )
      await userEvent
        .setup()
        .click(screen.getByRole('button', { name: zhCN.signInFlow.submit }))
      await waitFor(() =>
        expect(registerFailure).toHaveBeenCalledWith(
          'signInFlow.passkeyFailedRegister',
        ),
      )
      register.unmount()

      const signInFailure = vi.fn()
      renderPasskey({ challenge: 'Y2hhbGxlbmdl' }, signInFailure)
      await waitFor(() =>
        expect(signInFailure).toHaveBeenCalledWith(
          'signInFlow.passkeyFailedSignIn',
        ),
      )
    } finally {
      restore()
    }
  })
})

describe('FlowStepFields unsupported step', () => {
  it('render the notice with an exit control that reports upward', async () => {
    const user = userEvent.setup()
    const onUnsupported = vi.fn()
    render(
      {
        kind: 'redirect',
        url: 'https://idp.example/authorize',
        method: 'oidc',
      },
      onUnsupported,
    )

    expect(
      screen.getByText(zhCN.signInFlow.unsupportedStep),
    ).toBeInTheDocument()
    await user.click(
      screen.getByRole('button', {
        name: zhCN.signInFlow.unsupportedStepAction,
      }),
    )
    expect(onUnsupported).toHaveBeenCalledTimes(1)
  })
})

const PHONE_TURNSTILE: AuthnHumanGate = {
  phone: { required: true, provider: 'turnstile', site_key: 'site-1' },
  other: { required: false, provider: 'off' },
}

describe('HumanGatedFlowStep held answer', () => {
  it('cancel the held answer and leave the step answerable', async () => {
    const fake = installFakeTurnstile()
    try {
      const onAnswer = vi.fn()
      const harness = makeHarness({})
      const factorStep: OpenFlowState = {
        seq: 2,
        state: 'second',
        next: {
          kind: 'factor',
          factors: [{ id: 'mfa-1', kind: 'sms' }],
        },
      }
      renderWithProviders(
        <>
          <h1>Two-factor sign-in</h1>
          <HumanGatedFlowStep
            session={harness.session}
            humanGate={PHONE_TURNSTILE}
            step={factorStep}
            busy={false}
            onAnswer={onAnswer}
            onUnsupported={vi.fn()}
          />
        </>,
      )
      const user = userEvent.setup()
      const smsChoice = screen.getByRole('button', {
        name: zhCN.signInFlow.factorKind.sms,
      })

      await user.click(smsChoice)
      await waitFor(() =>
        expect(smsChoice).toHaveAttribute('aria-disabled', 'true'),
      )
      expect(smsChoice).toHaveAttribute('aria-busy', 'true')
      expect(fake.widgets).toHaveLength(1)
      expect(onAnswer).not.toHaveBeenCalled()

      await user.click(
        screen.getByRole('button', { name: zhCN.signInFlow.passkeyCancel }),
      )
      await waitFor(() =>
        expect(smsChoice).not.toHaveAttribute('aria-disabled', 'true'),
      )
      expect(smsChoice).toHaveFocus()
      expect(
        screen.queryByRole('button', { name: zhCN.signInFlow.passkeyCancel }),
      ).toBeNull()
      expect(onAnswer).not.toHaveBeenCalled()

      await user.click(smsChoice)
      await waitFor(() => expect(fake.widgets).toHaveLength(2))
      act(() => {
        fake.token('token-1')
      })
      await waitFor(() =>
        expect(onAnswer).toHaveBeenCalledWith(
          { factor: 'mfa-1' },
          'token-1',
        ),
      )
      expect(onAnswer).toHaveBeenCalledTimes(1)
      await expectNoAxeViolations()
    } finally {
      fake.restore()
    }
  })

  it('marks only the chosen factor busy and holds its sibling without a reason', async () => {
    const fake = installFakeTurnstile()
    try {
      const onAnswer = vi.fn()
      const harness = makeHarness({})
      renderWithProviders(
        <>
          <h1>Two-factor sign-in</h1>
          <HumanGatedFlowStep
            session={harness.session}
            humanGate={PHONE_TURNSTILE}
            step={{
              seq: 2,
              state: 'second',
              next: {
                kind: 'factor',
                factors: [
                  { id: 'mfa-1', kind: 'sms' },
                  { id: 'mfa-2', kind: 'totp' },
                ],
              },
            }}
            busy={false}
            onAnswer={onAnswer}
            onUnsupported={vi.fn()}
          />
        </>,
      )
      const user = userEvent.setup()
      const sms = screen.getByRole('button', {
        name: zhCN.signInFlow.factorKind.sms,
      })
      const totp = screen.getByRole('button', {
        name: zhCN.signInFlow.factorKind.totp,
      })

      await user.click(sms)
      await waitFor(() => expect(sms).toHaveAttribute('aria-busy', 'true'))
      expect(sms).toHaveAttribute('aria-disabled', 'true')
      expect(sms).not.toBeDisabled()

      expect(totp).toHaveAttribute('aria-disabled', 'true')
      expect(totp).not.toHaveAttribute('aria-busy')
      expect(totp).not.toHaveAttribute('aria-describedby')
      expect(totp).not.toBeDisabled()

      await user.click(totp)
      totp.focus()
      await user.keyboard('{Enter}')
      expect(onAnswer).not.toHaveBeenCalled()
    } finally {
      fake.restore()
    }
  })
})
