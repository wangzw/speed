/**
 * PasswordResetScreen behaviour: the anonymous reset surface drives the
 * host-bound operations through its four states -- request, sent,
 * verified and done -- with the code and password fields appearing
 * together once a code was requested, a resume through currentReset, the
 * server's collapsed wrong-code refusal attached to the code field, the
 * password policy attached to the new-password field (which leaves the
 * flow verified so the retry writes no code again), a stated Retry-After
 * wait, and the closed / unavailable / invalid-flow exits. Text
 * expectations read the shipped bundles, never inline language.
 */

import { describe, expect, it, vi } from 'vitest'
import { useState } from 'react'
import { act, fireEvent, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ApiError } from '@speed/api-client'
import type { AuthSession } from '@speed/auth-core'
import { apiError, makeHarness } from '@speed/test-utils/session-harness'
import { installFakeTurnstile } from '@speed/test-utils/turnstile'
import { PasswordResetScreen } from './PasswordResetScreen.js'
import type {
  PasswordResetOperations,
  PasswordResetSent,
  PasswordResetVerified,
} from './PasswordResetScreen.js'
import { renderWithProviders } from '../test-utils/render.js'
import { expectNoAxeViolations } from '../test-utils/axe.js'
import zhCN from './locales/zh-CN.json' with { type: 'json' }
import enUS from './locales/en-US.json' with { type: 'json' }

const MAIL = 'alice@example.com'
const CODE = '123456'
const NEW_PASSWORD = 'n3w-s3cret-pass'

const SENT = {
  state: 'sent' as const,
  expiresIn: 600,
  resendAfter: 30,
}
const VERIFIED = { state: 'verified' as const, expiresIn: 600 }

function resetOperations(
  overrides: Partial<PasswordResetOperations> = {},
): PasswordResetOperations {
  return {
    requestReset: () => Promise.resolve(SENT),
    verifyReset: () => Promise.resolve(VERIFIED),
    completeReset: () => Promise.resolve(),
    ...overrides,
  }
}

function session() {
  return makeHarness({}).session
}

async function fillIdentifier() {
  const user = userEvent.setup()
  await user.type(screen.getByLabelText(zhCN.passwordReset.emailLabel), MAIL)
  return user
}

/** Render the request state, submit the email, and wait for the sent
 * surface. */
async function reachSent(
  operations: PasswordResetOperations,
  props: {
    readonly onBackToSignIn?: () => void
    readonly onDone?: () => void
  } = {},
) {
  const user = userEvent.setup()
  renderWithProviders(
    <PasswordResetScreen
      session={session()}
      channels={['email']}
      operations={operations}
      {...props}
    />,
  )
  await user.type(screen.getByLabelText(zhCN.passwordReset.emailLabel), MAIL)
  await user.click(screen.getByRole('button', { name: zhCN.passwordReset.send }))
  await screen.findByText(zhCN.passwordReset.sentNotice)
  return user
}

async function fillCodeAndPassword(
  user: ReturnType<typeof userEvent.setup>,
) {
  await user.type(screen.getByLabelText(zhCN.passwordReset.codeLabel), CODE)
  await user.type(
    screen.getByLabelText(zhCN.passwordReset.newPasswordLabel),
    NEW_PASSWORD,
  )
  await user.type(
    screen.getByLabelText(zhCN.passwordReset.confirmPasswordLabel),
    NEW_PASSWORD,
  )
  await user.click(
    screen.getByRole('button', { name: zhCN.passwordReset.submit }),
  )
}

/** A host that binds the operations inline and can re-render itself, so
 * every host render hands the screen new operation and resume identities. */
function RebindingHost({
  session,
  resume,
}: {
  readonly session: AuthSession
  readonly resume: () => Promise<PasswordResetSent | PasswordResetVerified>
}) {
  const [tick, setTick] = useState(0)
  return (
    <>
      <PasswordResetScreen
        session={session}
        channels={['email']}
        operations={{
          requestReset: () => Promise.resolve(SENT),
          verifyReset: () => Promise.resolve(VERIFIED),
          completeReset: () => Promise.resolve(),
          currentReset: () => resume(),
        }}
      />
      <button
        type="button"
        aria-label={`tick-${tick}`}
        onClick={() => setTick(tick + 1)}
      />
    </>
  )
}

describe('PasswordResetScreen', () => {
  it('request the reset with the channel and identifier, then show the sent surface', async () => {
    const requestReset = vi.fn(() => Promise.resolve(SENT))
    const operations = resetOperations({ requestReset })
    const onDone = vi.fn()
    const user = userEvent.setup()
    renderWithProviders(
      <PasswordResetScreen
        session={session()}
        channels={['email', 'sms']}
        operations={operations}
        onDone={onDone}
      />,
    )

    expect(
      screen.getByRole('heading', { name: zhCN.passwordReset.title }),
    ).toBeInTheDocument()
    await fillIdentifier()
    await user.click(
      screen.getByRole('button', { name: zhCN.passwordReset.send }),
    )

    await screen.findByText(zhCN.passwordReset.sentNotice)
    expect(requestReset).toHaveBeenCalledWith({
      channel: 'email',
      identifier: MAIL,
    })
    // The code and the new password are one surface: the flow is
    // submitted once, not step by step.
    expect(
      screen.getByLabelText(zhCN.passwordReset.codeLabel),
    ).toBeInTheDocument()
    expect(
      screen.getByLabelText(zhCN.passwordReset.newPasswordLabel),
    ).toBeInTheDocument()
    expect(
      screen.getByLabelText(zhCN.passwordReset.confirmPasswordLabel),
    ).toBeInTheDocument()
    expect(
      screen.queryByLabelText(zhCN.passwordReset.emailLabel),
    ).not.toBeInTheDocument()
    expect(onDone).not.toHaveBeenCalled()
  })

  it('switch the identifier field with the chosen channel', async () => {
    const requestReset = vi.fn(() => Promise.resolve(SENT))
    renderWithProviders(
      <PasswordResetScreen
        session={session()}
        channels={['email', 'sms']}
        operations={resetOperations({ requestReset })}
      />,
    )
    const user = userEvent.setup()
    // Email is the first published channel, so it is the initial choice.
    expect(
      screen.getByLabelText(zhCN.passwordReset.emailLabel),
    ).toBeInTheDocument()
    await user.click(
      screen.getByRole('button', { name: zhCN.passwordReset.channel.sms }),
    )
    expect(
      screen.getByLabelText(zhCN.passwordReset.phoneLabel),
    ).toBeInTheDocument()
    expect(screen.getByText(zhCN.passwordReset.phoneHint)).toBeInTheDocument()
    await user.type(
      screen.getByLabelText(zhCN.passwordReset.phoneLabel),
      '+8613800138000',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.passwordReset.send }),
    )
    await waitFor(() =>
      expect(requestReset).toHaveBeenCalledWith({
        channel: 'sms',
        identifier: '+8613800138000',
      }),
    )
  })

  it('present the human proof the gate holds and consume it after the send', async () => {
    const fake = installFakeTurnstile()
    try {
      const requestReset = vi.fn(() => Promise.resolve(SENT))
      renderWithProviders(
        <PasswordResetScreen
          session={session()}
          channels={['email']}
          operations={resetOperations({ requestReset })}
          humanGate={{
            required: true,
            provider: 'turnstile',
            site_key: 'site-1',
          }}
        />,
      )
      const user = userEvent.setup()
      await fillIdentifier()
      const send = screen.getByRole('button', {
        name: zhCN.passwordReset.send,
      })
      // No token yet: the required verifier blocks the send.
      expect(send).toBeDisabled()
      act(() => {
        fake.token('proof-1')
      })
      expect(send).toBeEnabled()
      await user.click(send)
      await waitFor(() =>
        expect(requestReset).toHaveBeenCalledWith({
          channel: 'email',
          identifier: MAIL,
          humanProof: 'proof-1',
        }),
      )
    } finally {
      fake.restore()
    }
  })

  it('attach the collapsed wrong-code refusal to the code field and write no password', async () => {
    const completeReset = vi.fn(() => Promise.resolve())
    const operations = resetOperations({
      verifyReset: () => Promise.reject(apiError(401, 'authn.login_failed')),
      completeReset,
    })
    const user = await reachSent(operations)
    await fillCodeAndPassword(user)

    const code = screen.getByLabelText(zhCN.passwordReset.codeLabel)
    await waitFor(() => expect(code).toHaveFocus())
    expect(code).toHaveAttribute('aria-invalid', 'true')
    expect(
      screen.getByText(zhCN.passwordReset.codeRejected),
    ).toBeInTheDocument()
    expect(completeReset).not.toHaveBeenCalled()
    // The record is still sent; the surface did not advance.
    expect(screen.getByText(zhCN.passwordReset.sentNotice)).toBeInTheDocument()
  })

  it('keep a policy refusal on the verified surface and retry only the password write', async () => {
    const verifyReset = vi.fn(() => Promise.resolve(VERIFIED))
    const completeReset = vi
      .fn()
      .mockRejectedValueOnce(apiError(400, 'authn.bad_request'))
      .mockResolvedValueOnce(undefined)
    const operations = resetOperations({ verifyReset, completeReset })
    const onDone = vi.fn()
    const user = await reachSent(operations, { onDone })
    await fillCodeAndPassword(user)

    await waitFor(() =>
      expect(
        screen.getByText(zhCN.passwordReset.policyFailure),
      ).toBeInTheDocument(),
    )
    expect(
      screen.queryByLabelText(zhCN.passwordReset.codeLabel),
    ).not.toBeInTheDocument()
    expect(verifyReset).toHaveBeenCalledTimes(1)
    await expectNoAxeViolations()

    await user.click(
      screen.getByRole('button', { name: zhCN.passwordReset.submit }),
    )
    await waitFor(() => expect(onDone).toHaveBeenCalledTimes(1))
    expect(verifyReset).toHaveBeenCalledTimes(1)
    expect(completeReset).toHaveBeenCalledTimes(2)
    expect(completeReset).toHaveBeenLastCalledWith({
      newPassword: NEW_PASSWORD,
    })
    expect(
      screen.getByRole('heading', { name: zhCN.passwordReset.doneTitle }),
    ).toBeInTheDocument()
  })

  it('refuse a mismatched confirmation before any request', async () => {
    const verifyReset = vi.fn(() => Promise.resolve(VERIFIED))
    const user = await reachSent(resetOperations({ verifyReset }))
    await user.type(screen.getByLabelText(zhCN.passwordReset.codeLabel), CODE)
    await user.type(
      screen.getByLabelText(zhCN.passwordReset.newPasswordLabel),
      NEW_PASSWORD,
    )
    await user.type(
      screen.getByLabelText(zhCN.passwordReset.confirmPasswordLabel),
      `${NEW_PASSWORD}-different`,
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.passwordReset.submit }),
    )
    expect(
      screen.getByText(zhCN.passwordReset.mismatch),
    ).toBeInTheDocument()
    expect(verifyReset).not.toHaveBeenCalled()
  })

  it('state the resend interval and resend the same identifier after it', async () => {
    vi.useFakeTimers()
    try {
      const requestReset = vi.fn(() => Promise.resolve(SENT))
      renderWithProviders(
        <PasswordResetScreen
          session={session()}
          channels={['email']}
          operations={resetOperations({ requestReset })}
        />,
      )
      fireEvent.change(
        screen.getByLabelText(zhCN.passwordReset.emailLabel),
        { target: { value: MAIL } },
      )
      await act(async () => {
        fireEvent.click(
          screen.getByRole('button', { name: zhCN.passwordReset.send }),
        )
        await Promise.resolve()
      })
      expect(
        screen.getByRole('button', {
          name: zhCN.passwordReset.resendIn.replace('{{seconds}}', '30'),
        }),
      ).toBeDisabled()
      act(() => {
        vi.advanceTimersByTime(29000)
      })
      expect(
        screen.getByRole('button', {
          name: zhCN.passwordReset.resendIn.replace('{{seconds}}', '1'),
        }),
      ).toBeDisabled()
      act(() => {
        vi.advanceTimersByTime(1000)
      })
      await act(async () => {
        fireEvent.click(
          screen.getByRole('button', { name: zhCN.passwordReset.resend }),
        )
        await Promise.resolve()
      })
      expect(requestReset).toHaveBeenCalledTimes(2)
      expect(requestReset).toHaveBeenLastCalledWith({
        channel: 'email',
        identifier: MAIL,
      })
    } finally {
      vi.useRealTimers()
    }
  })

  it('wait out a stated throttled retry and re-enable the send', async () => {
    vi.useFakeTimers()
    try {
      const requestReset = vi.fn(() =>
        Promise.reject(
          new ApiError({
            status: 429,
            code: 'authn.throttled',
            attempts: 1,
            retryAfterMs: 3000,
          }),
        ),
      )
      renderWithProviders(
        <PasswordResetScreen
          session={session()}
          channels={['email']}
          operations={resetOperations({ requestReset })}
        />,
      )
      fireEvent.change(
        screen.getByLabelText(zhCN.passwordReset.emailLabel),
        { target: { value: MAIL } },
      )
      await act(async () => {
        fireEvent.click(
          screen.getByRole('button', { name: zhCN.passwordReset.send }),
        )
        await Promise.resolve()
      })
      const send = screen.getByRole('button', {
        name: zhCN.passwordReset.send,
      })
      expect(send).toBeDisabled()
      expect(screen.getByRole('alert')).toHaveTextContent(
        zhCN.passwordReset.retryAfter.replace('{{seconds}}', '3'),
      )
      act(() => {
        vi.advanceTimersByTime(2999)
      })
      expect(send).toBeDisabled()
      act(() => {
        vi.advanceTimersByTime(1)
      })
      expect(send).toBeEnabled()
      expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    } finally {
      vi.useRealTimers()
    }
  })

  it('render the closed deployment under its own problem text', async () => {
    renderWithProviders(
      <PasswordResetScreen
        session={session()}
        channels={['email']}
        operations={resetOperations({
          requestReset: () =>
            Promise.reject(apiError(403, 'authn.password_reset_closed')),
        })}
      />,
    )
    const user = await fillIdentifier()
    await user.click(
      screen.getByRole('button', { name: zhCN.passwordReset.send }),
    )
    expect(
      await screen.findByText(zhCN.errors.authn.password_reset_closed),
    ).toBeInTheDocument()
  })

  it('attach an invalid identifier refusal to the identifier field', async () => {
    renderWithProviders(
      <PasswordResetScreen
        session={session()}
        channels={['email']}
        operations={resetOperations({
          requestReset: () =>
            Promise.reject(apiError(400, 'authn.bad_request')),
        })}
      />,
    )
    const user = await fillIdentifier()
    await user.click(
      screen.getByRole('button', { name: zhCN.passwordReset.send }),
    )
    const identifier = screen.getByLabelText(zhCN.passwordReset.emailLabel)
    await waitFor(() => expect(identifier).toHaveFocus())
    expect(identifier).toHaveAttribute('aria-invalid', 'true')
    expect(
      screen.getByText(zhCN.passwordReset.invalidIdentifier),
    ).toBeInTheDocument()
  })

  it('show the resume state while the bound record is read', () => {
    renderWithProviders(
      <PasswordResetScreen
        session={session()}
        channels={['email']}
        operations={resetOperations({
          currentReset: () =>
            new Promise(() => {
              // Never settles: the surface stays on its resume state.
            }),
        })}
      />,
    )
    expect(screen.getByText(zhCN.passwordReset.loading)).toBeInTheDocument()
  })

  it('resume a verified reset and write only the password', async () => {
    const completeReset = vi.fn(() => Promise.resolve())
    const verifyReset = vi.fn(() => Promise.resolve(VERIFIED))
    const onDone = vi.fn()
    renderWithProviders(
      <PasswordResetScreen
        session={session()}
        channels={['email']}
        operations={resetOperations({
          currentReset: () => Promise.resolve(VERIFIED),
          completeReset,
          verifyReset,
        })}
        onDone={onDone}
      />,
    )
    const user = userEvent.setup()
    await user.type(
      await screen.findByLabelText(zhCN.passwordReset.newPasswordLabel),
      NEW_PASSWORD,
    )
    await user.type(
      screen.getByLabelText(zhCN.passwordReset.confirmPasswordLabel),
      NEW_PASSWORD,
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.passwordReset.submit }),
    )
    await waitFor(() => expect(onDone).toHaveBeenCalledTimes(1))
    expect(completeReset).toHaveBeenCalledWith({ newPassword: NEW_PASSWORD })
    expect(verifyReset).not.toHaveBeenCalled()
    expect(
      screen.queryByLabelText(zhCN.passwordReset.emailLabel),
    ).not.toBeInTheDocument()
  })

  it('resume a sent reset through its code and write the password', async () => {
    const verifyReset = vi.fn(() => Promise.resolve(VERIFIED))
    const completeReset = vi.fn(() => Promise.resolve())
    renderWithProviders(
      <PasswordResetScreen
        session={session()}
        channels={['email']}
        operations={resetOperations({
          currentReset: () => Promise.resolve(SENT),
          verifyReset,
          completeReset,
        })}
      />,
    )
    const user = userEvent.setup()
    await screen.findByText(zhCN.passwordReset.sentNotice)
    // The resumed record names no channel or identifier, so resending is
    // not offered: the host cannot reconstruct what to resend.
    expect(
      screen.queryByRole('button', { name: zhCN.passwordReset.resend }),
    ).not.toBeInTheDocument()
    await fillCodeAndPassword(user)
    await waitFor(() =>
      expect(completeReset).toHaveBeenCalledWith({ newPassword: NEW_PASSWORD }),
    )
    expect(verifyReset).toHaveBeenCalledWith({ code: CODE })
  })

  it('fall back to the request state when the resume is refused', async () => {
    renderWithProviders(
      <PasswordResetScreen
        session={session()}
        channels={['email']}
        operations={resetOperations({
          currentReset: () =>
            Promise.reject(apiError(401, 'authn.flow_invalid')),
        })}
      />,
    )
    expect(
      await screen.findByLabelText(zhCN.passwordReset.emailLabel),
    ).toBeInTheDocument()
  })

  it('read the bound record once when the host rebinds its operations', async () => {
    const resume = vi.fn(() => Promise.resolve(VERIFIED))
    const harness = makeHarness({})
    renderWithProviders(
      <RebindingHost session={harness.session} resume={resume} />,
    )
    await screen.findByLabelText(zhCN.passwordReset.newPasswordLabel)
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: 'tick-0' }))
    await act(async () => {
      await Promise.resolve()
    })
    expect(resume).toHaveBeenCalledTimes(1)
    await user.click(screen.getByRole('button', { name: 'tick-1' }))
    await act(async () => {
      await Promise.resolve()
    })
    expect(resume).toHaveBeenCalledTimes(1)
  })

  it('offer a start-over exit when the flow is no longer valid', async () => {
    const abandonReset = vi.fn(() => Promise.resolve())
    const user = await reachSent(
      resetOperations({
        verifyReset: () =>
          Promise.reject(apiError(401, 'authn.flow_invalid')),
        abandonReset,
      }),
    )
    await fillCodeAndPassword(user)
    expect(
      await screen.findByText(zhCN.passwordReset.flowInvalid),
    ).toBeInTheDocument()
    await user.click(
      screen.getByRole('button', { name: zhCN.passwordReset.startOver }),
    )
    expect(
      await screen.findByLabelText(zhCN.passwordReset.emailLabel),
    ).toBeInTheDocument()
    expect(abandonReset).toHaveBeenCalledTimes(1)
  })

  it('offer a start-over exit when the account is no longer eligible', async () => {
    const abandonReset = vi.fn(() => Promise.resolve())
    const user = await reachSent(
      resetOperations({
        verifyReset: () => Promise.resolve(VERIFIED),
        completeReset: () =>
          Promise.reject(apiError(403, 'authn.password_reset_unavailable')),
        abandonReset,
      }),
    )
    await fillCodeAndPassword(user)
    expect(
      await screen.findByText(zhCN.errors.authn.password_reset_unavailable),
    ).toBeInTheDocument()
    await user.click(
      screen.getByRole('button', { name: zhCN.passwordReset.startOver }),
    )
    expect(
      await screen.findByLabelText(zhCN.passwordReset.emailLabel),
    ).toBeInTheDocument()
    expect(abandonReset).toHaveBeenCalledTimes(1)
  })

  it('abandon an in-flight reset when the viewer returns to sign-in', async () => {
    const abandonReset = vi.fn(() => Promise.resolve())
    const onBackToSignIn = vi.fn()
    const user = await reachSent(resetOperations({ abandonReset }), {
      onBackToSignIn,
    })
    await user.click(
      screen.getByRole('button', { name: zhCN.passwordReset.backToSignIn }),
    )
    expect(abandonReset).toHaveBeenCalledTimes(1)
    expect(onBackToSignIn).toHaveBeenCalledTimes(1)
  })

  it('leave through sign-in without abandoning when no flow started', async () => {
    const abandonReset = vi.fn(() => Promise.resolve())
    const onBackToSignIn = vi.fn()
    renderWithProviders(
      <PasswordResetScreen
        session={session()}
        channels={['email']}
        operations={resetOperations({ abandonReset })}
        onBackToSignIn={onBackToSignIn}
      />,
    )
    const user = userEvent.setup()
    await user.click(
      screen.getByRole('button', { name: zhCN.passwordReset.backToSignIn }),
    )
    expect(abandonReset).not.toHaveBeenCalled()
    expect(onBackToSignIn).toHaveBeenCalledTimes(1)
  })

  it('render the unavailable notice when the deployment publishes no channel', async () => {
    renderWithProviders(
      <PasswordResetScreen
        session={session()}
        channels={[]}
        operations={resetOperations()}
        onBackToSignIn={() => undefined}
      />,
    )
    expect(
      screen.getByText(zhCN.passwordReset.unavailable),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: zhCN.passwordReset.send }),
    ).not.toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: zhCN.passwordReset.backToSignIn }),
    ).toBeInTheDocument()
  })

  it('render in the en-US bundle on an English-starting instance', async () => {
    renderWithProviders(
      <PasswordResetScreen
        session={session()}
        channels={['email']}
        operations={resetOperations()}
      />,
      { language: 'en-US' },
    )
    expect(
      screen.getByRole('button', { name: enUS.passwordReset.send }),
    ).toBeInTheDocument()
    expect(
      screen.getByText(enUS.passwordReset.description),
    ).toBeInTheDocument()
  })

  it('pass axe in the request, sent and done states', async () => {
    const operations = resetOperations()
    const user = userEvent.setup()
    renderWithProviders(
      <PasswordResetScreen
        session={session()}
        channels={['email', 'sms']}
        operations={operations}
        onBackToSignIn={() => undefined}
      />,
    )
    await screen.findByRole('button', { name: zhCN.passwordReset.send })
    await expectNoAxeViolations()

    await fillIdentifier()
    await user.click(
      screen.getByRole('button', { name: zhCN.passwordReset.send }),
    )
    await screen.findByText(zhCN.passwordReset.sentNotice)
    await expectNoAxeViolations()

    await fillCodeAndPassword(user)
    await screen.findByRole('heading', {
      name: zhCN.passwordReset.doneTitle,
    })
    await expectNoAxeViolations()
  })
})
