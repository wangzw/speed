/**
 * FlowStepFields behaviour: the factor, enroll and challenge vocabulary.
 *
 * What this suite pins: a known factor or enroll kind renders its
 * translated name, a server-supplied factor label wins over it, and a
 * kind the family has no wording for shows the kind as given -- the
 * deployment's own vocabulary, never a generic stand-in. That last rule
 * is the one the three flow-step copies share: account-ui and console-ui
 * render an unknown kind verbatim, so this package does too, and this
 * suite is the auth-ui side of the registered sync obligation.
 *
 * The same rule holds for a challenge's fields: a name the family knows
 * renders the bundle's label and the explanatory text under it, and an
 * unknown name renders verbatim with no hint.
 */

import { useState } from 'react'
import { describe, expect, it, vi } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { AuthnField, AuthnStep } from '@speed/auth-core'
import zhCN from '../locales/zh-CN.json' with { type: 'json' }
import { renderWithProviders } from '../../test-utils/render.js'
import { FlowStepFields } from './flow-step.js'
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

/** Installs a `navigator.credentials` stub, answering the restorer. */
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

/** A challenge step the flow parks while it waits for the given fields. */
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
    // The refusal is the field's, never the whole-attempt banner.
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('leaves the field without an error for any other code', () => {
    // authn.bad_request is not a username refusal: on this step it belongs
    // to the surface's whole-attempt banner.
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
    readonly provider?: string
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
        ...(identity.provider === undefined
          ? {}
          : { provided_provider: identity.provider }),
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
      readonly provider?: string
      readonly displayName?: string
      readonly email?: string
      readonly failure?: string | null
      readonly onAnswer?: (input: Record<string, string>) => void
    } = {},
  ) {
    return renderWithProviders(
      <FlowStepFields
        step={socialStep({
          provider: options.provider,
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

  it('renders the title, the one-sentence description and the signed-in submit row', () => {
    renderFace({
      provider: 'google',
      displayName: 'Ada Lovelace',
      email: 'ada@example.com',
    })

    expect(
      screen.getByRole('heading', {
        level: 2,
        name: zhCN.signInFlow.signupAccountTitle,
      }),
    ).toBeInTheDocument()
    expect(
      screen.getByText(zhCN.signInFlow.signupAccountDescription),
    ).toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: zhCN.signInFlow.signupAccountSubmit }),
    ).toBeInTheDocument()
  })

  it('renders the three identity lines in order, all read-only', () => {
    renderFace({
      provider: 'google',
      displayName: 'Ada Lovelace',
      email: 'ada@example.com',
    })

    const provider = screen.getByText(
      zhCN.signInFlow.signupProvider.replace(
        '{{provider}}',
        zhCN.social.provider.google,
      ),
    )
    const name = screen.getByText('Ada Lovelace')
    const email = screen.getByText('ada@example.com')
    // The provider line comes first, then the provider's account name, then
    // the address it shared; none is an editable control.
    expect(
      provider.compareDocumentPosition(name) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy()
    expect(
      name.compareDocumentPosition(email) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy()
    expect(screen.queryByDisplayValue('ada@example.com')).toBeNull()
    expect(screen.queryByRole('textbox', { name: 'ada@example.com' })).toBeNull()
  })

  it('renders only the provider line when the provider shared nothing else', () => {
    renderFace({ provider: 'google' })

    expect(
      screen.getByText(
        zhCN.signInFlow.signupProvider.replace(
          '{{provider}}',
          zhCN.social.provider.google,
        ),
      ),
    ).toBeInTheDocument()
    expect(screen.queryByText('ada@example.com')).toBeNull()
  })

  it('leaves the account-name line out when the provider gave no name', () => {
    renderFace({ provider: 'google', email: 'ada@example.com' })

    expect(
      screen.getByText(
        zhCN.signInFlow.signupProvider.replace(
          '{{provider}}',
          zhCN.social.provider.google,
        ),
      ),
    ).toBeInTheDocument()
    expect(screen.getByText('ada@example.com')).toBeInTheDocument()
  })

  it('leaves the address line out when the provider shared none', () => {
    renderFace({ provider: 'google', displayName: 'Ada Lovelace' })

    expect(screen.getByText('Ada Lovelace')).toBeInTheDocument()
    expect(screen.queryByText('ada@example.com')).toBeNull()
  })

  it('no longer renders the address as a read-only form field', () => {
    renderFace({
      provider: 'google',
      displayName: 'Ada Lovelace',
      email: 'ada@example.com',
    })

    // The address is an identity line, not a form field: no control carries
    // it as its value, and the face's only text inputs are the username,
    // the password and the confirmation.
    expect(screen.queryByDisplayValue('ada@example.com')).toBeNull()
    expect(screen.getByText('ada@example.com')).toBeInTheDocument()
    expect(
      screen.getByRole('textbox', { name: zhCN.signInFlow.usernameLabel }),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('textbox', { name: zhCN.register.confirmPasswordLabel }),
    ).toBeNull()
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
    // The confirmation never travels, so a matching pair sends only the two
    // fields the server asked for.
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
    // The refusal is the field's, never the whole-attempt banner.
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

  it('keeps the username-only face for SMS and enterprise signup', () => {
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
      screen.queryByText(zhCN.signInFlow.signupAccountDescription),
    ).toBeNull()
    expect(
      screen.queryByLabelText(zhCN.passwordSignIn.passwordLabel),
    ).toBeNull()
    expect(
      screen.queryByLabelText(zhCN.register.confirmPasswordLabel),
    ).toBeNull()
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
    // The unknown kind is the deployment's own name, not the generic
    // "other" label.
    expect(screen.getByRole('button', { name: 'magic' })).toBeTruthy()
    expect(
      screen.queryByRole('button', { name: zhCN.signInFlow.factorKind.other }),
    ).toBeNull()
  })

  it('lets a server-supplied factor label win over the vocabulary', () => {
    render({
      kind: 'factor',
      factors: [{ id: 'f1', kind: 'totp', label: 'My app' }],
    })

    expect(screen.getByRole('button', { name: 'My app' })).toBeTruthy()
    expect(
      screen.queryByRole('button', { name: zhCN.signInFlow.factorKind.totp }),
    ).toBeNull()
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
    // The passkey entry the viewer clicked is the only control: the
    // assertion step it opens starts the browser's ceremony itself.
    const get = vi.fn().mockReturnValue(new Promise(() => {}))
    const restore = installCredentials({ get, create: vi.fn() })
    try {
      const onAnswer = vi.fn()
      // A state update re-renders the renderer with a fresh step object,
      // which is what a parent's ordinary re-render looks like.
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
      // No second click: the start control is not offered at all.
      expect(
        screen.queryByRole('button', { name: zhCN.signInFlow.submit }),
      ).toBeNull()
      expect(get).toHaveBeenCalledTimes(1)

      // The same challenge in a fresh options object is the same ceremony:
      // an ordinary re-render leaves it running.
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
    // The challenge string alone does not identify a ceremony: the server can
    // send the same challenge again in the next step. The sequence number
    // advances with every reply the flow parks, so a re-sent challenge starts
    // the ceremony again while an ordinary re-render leaves it alone.
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
    // Only an assertion step starts on arrival. A different step the flow
    // parks is not an assertion ceremony, whatever sequence it carries.
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
    // A creation ceremony needs the user activation some browsers require,
    // so a registration step stays parked until the viewer clicks.
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
