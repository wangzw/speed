import { describe, expect, it, vi } from 'vitest'
import { restoreRealTimers } from '@speed/test-utils/timers'
import { useState } from 'react'
import { act, fireEvent, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ApiError } from '@speed/api-client'
import type { AuthSession } from '@speed/auth-core'
import {
  CAPTCHA_CHALLENGE,
  apiError,
  captchaChallenge,
  makeHarness,
} from '@speed/test-utils/session-harness'
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
import uiKitZhCN from '../../ui-kit/src/locales/zh-CN.json' with { type: 'json' }

const MAIL = 'alice@example.com'
const CODE = '123456'
const NEW_PASSWORD = 'n3w-s3cret-pass'

const SENT = {
  state: 'sent' as const,
  expiresIn: 600,
  resendAfter: 30,
}
const VERIFIED = { state: 'verified' as const, expiresIn: 600 }

function waitSentence(seconds: number): string {
  const [before = '', after = ''] = zhCN.throttle.retryAfter.split('{{seconds}}')
  return `${before}${seconds}${after}`
}

function visibleSentence(alert: HTMLElement): HTMLElement | null {
  return alert.querySelector<HTMLElement>('span[aria-hidden="true"]')
}

function stableSentence(alert: HTMLElement): HTMLElement | null {
  return alert.querySelector<HTMLElement>('span:not([aria-hidden])')
}

function deferredSend() {
  let reject: (error: unknown) => void = () => undefined
  const requestReset = vi.fn(
    () =>
      new Promise<PasswordResetSent>((_resolve, settle) => {
        reject = settle
      }),
  )
  return { requestReset, reject: (error: unknown) => reject(error) }
}

function deferredComplete() {
  let settle: () => void = () => undefined
  const completeReset = vi.fn(
    () =>
      new Promise<void>((resolve) => {
        settle = resolve
      }),
  )
  return { completeReset, settle: () => settle() }
}

function deferredVerify() {
  let settle: (value: PasswordResetVerified) => void = () => undefined
  const verifyReset = vi.fn(
    () =>
      new Promise<PasswordResetVerified>((resolve) => {
        settle = resolve
      }),
  )
  return { verifyReset, settle: () => settle(VERIFIED) }
}

const OPEN_GATE = {
  phone: { required: false as const, provider: 'off' as const },
  other: { required: false as const, provider: 'off' as const },
}

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

function leadingBackRow(): Element {
  const back = screen.getByRole('button', {
    name: zhCN.passwordReset.backToSignIn,
  })
  let row: Element | null = back.parentElement
  while (row !== null && row.querySelector('button[type="submit"]') === null) {
    row = row.parentElement
  }
  expect(row).not.toBeNull()
  return row as Element
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

  it('land on the sent surface when the identifier submits by Enter', async () => {
    const user = userEvent.setup()
    renderWithProviders(
      <PasswordResetScreen
        session={session()}
        channels={['email']}
        operations={resetOperations()}
        humanGate={OPEN_GATE}
      />,
    )
    const identifier = screen.getByLabelText(zhCN.passwordReset.emailLabel)
    await user.type(identifier, MAIL)
    await user.keyboard('{Enter}')
    await screen.findByText(zhCN.passwordReset.sentNotice)

    expect(
      screen.getByLabelText(zhCN.passwordReset.codeLabel),
    ).toHaveFocus()
  })

  it('leave focus the viewer moved while a send is in flight', async () => {
    let resolveSend: (value: PasswordResetSent) => void = () => undefined
    const requestReset = vi.fn(
      () =>
        new Promise<PasswordResetSent>((resolve) => {
          resolveSend = resolve
        }),
    )
    const user = userEvent.setup()
    renderWithProviders(
      <>
        <PasswordResetScreen
          session={session()}
          channels={['email']}
          operations={resetOperations({ requestReset })}
          humanGate={OPEN_GATE}
        />
        <button type="button">outside</button>
      </>,
    )
    const identifier = screen.getByLabelText(zhCN.passwordReset.emailLabel)
    await user.type(identifier, MAIL)
    await user.keyboard('{Enter}')
    const outside = screen.getByRole('button', { name: 'outside' })
    await user.click(outside)
    expect(outside).toHaveFocus()

    await act(async () => {
      resolveSend(SENT)
      await Promise.resolve()
    })
    await screen.findByText(zhCN.passwordReset.sentNotice)
    expect(outside).toHaveFocus()
  })

  it('keep the phone control on the SMS channel alone and refuse a short number locally', async () => {
    const requestReset = vi.fn(() => Promise.resolve(SENT))
    renderWithProviders(
      <PasswordResetScreen
        session={session()}
        channels={['email', 'sms']}
        operations={resetOperations({ requestReset })}
      />,
    )
    const user = userEvent.setup()
    expect(
      screen.queryByRole('combobox', { name: zhCN.smsSignIn.countryLabel }),
    ).not.toBeInTheDocument()

    await user.click(
      screen.getByRole('button', { name: zhCN.passwordReset.channel.sms }),
    )
    await user.type(screen.getByLabelText(zhCN.passwordReset.phoneLabel), '13')
    await user.click(
      screen.getByRole('button', { name: zhCN.passwordReset.send }),
    )
    expect(requestReset).not.toHaveBeenCalled()
    expect(
      screen.getByLabelText(zhCN.passwordReset.phoneLabel),
    ).toHaveAttribute('aria-invalid', 'true')
  })

  it('lead the form actions with the way back, and keep it alone once done', async () => {
    const onBackToSignIn = vi.fn()
    const operations = resetOperations({
      requestReset: () => Promise.resolve(SENT),
      verifyReset: () => Promise.resolve(VERIFIED),
      completeReset: () => Promise.resolve(),
    })
    const user = await reachSent(operations, { onBackToSignIn })

    const row = leadingBackRow()
    expect(row.querySelector('input')).toBeNull()
    const back = screen.getByRole('button', {
      name: zhCN.passwordReset.backToSignIn,
    })
    const submit = row.querySelector('button[type="submit"]') as Element
    expect(
      back.compareDocumentPosition(submit) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy()
    await user.click(back)
    expect(onBackToSignIn).toHaveBeenCalledTimes(1)

    await fillCodeAndPassword(user)
    await screen.findByText(zhCN.passwordReset.doneTitle)
    expect(document.querySelector('button[type="submit"]')).toBeNull()
    expect(
      screen.getByRole('button', { name: zhCN.passwordReset.backToSignIn }),
    ).toBeInTheDocument()
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
    expect(
      screen.getByLabelText(zhCN.passwordReset.emailLabel),
    ).toBeInTheDocument()
    await user.click(
      screen.getByRole('button', { name: zhCN.passwordReset.channel.sms }),
    )
    expect(
      screen.getByRole('combobox', { name: zhCN.smsSignIn.countryLabel }),
    ).toBeInTheDocument()
    expect(
      screen.getByLabelText(zhCN.passwordReset.phoneLabel),
    ).toBeInTheDocument()
    expect(screen.getByText(zhCN.smsSignIn.phoneHint)).toBeInTheDocument()
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

  it('drops the channel failure and its stated wait when the channel switches', async () => {
    vi.useFakeTimers()
    try {
      const requestReset = vi.fn(() =>
        Promise.reject(
          new ApiError({
            status: 429,
            code: 'authn.throttled',
            attempts: 1,
            retryAfterMs: 30000,
          }),
        ),
      )
      renderWithProviders(
        <PasswordResetScreen
          session={session()}
          channels={['email', 'sms']}
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
      expect(screen.getByRole('alert')).toHaveTextContent(waitSentence(30))
      expect(
        screen.getByRole('button', { name: zhCN.passwordReset.send }),
      ).toHaveAttribute('aria-disabled', 'true')

      fireEvent.click(
        screen.getByRole('button', { name: zhCN.passwordReset.channel.sms }),
      )

      expect(screen.queryByRole('alert')).not.toBeInTheDocument()
      expect(
        screen.getByRole('button', { name: zhCN.passwordReset.send }),
      ).not.toHaveAttribute('aria-disabled', 'true')
    } finally {
      await restoreRealTimers()
    }
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
            phone: { required: true, provider: 'turnstile', site_key: 'site-1' },
            other: { required: true, provider: 'turnstile', site_key: 'site-1' },
          }}
        />,
      )
      const user = userEvent.setup()
      await fillIdentifier()
      expect(fake.widgets).toHaveLength(0)
      const send = screen.getByRole('button', {
        name: zhCN.passwordReset.send,
      })
      await user.click(send)
      await waitFor(() => expect(fake.widgets).toHaveLength(1))
      expect(fake.widgets[0]?.siteKey).toBe('site-1')
      expect(send).toHaveFocus()
      expect(send).toBeEnabled()
      expect(send).toHaveAttribute('aria-disabled', 'true')
      expect(send).toHaveAttribute('aria-busy', 'true')
      expect(requestReset).not.toHaveBeenCalled()

      act(() => {
        fake.token('proof-1')
      })
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

  it('keep the send control focused and reachable through the send and a stated wait', async () => {
    const { requestReset, reject } = deferredSend()
    renderWithProviders(
      <PasswordResetScreen
        session={session()}
        channels={['email']}
        operations={resetOperations({ requestReset })}
        humanGate={OPEN_GATE}
      />,
    )
    const user = userEvent.setup()
    await user.type(screen.getByLabelText(zhCN.passwordReset.emailLabel), MAIL)
    const send = screen.getByRole('button', { name: zhCN.passwordReset.send })
    await user.click(send)

    expect(send).toHaveFocus()
    expect(send).toBeEnabled()
    expect(send).toHaveAttribute('aria-disabled', 'true')
    expect(send).toHaveAttribute('aria-busy', 'true')
    const form = send.closest('form')
    expect(form).not.toBeNull()
    fireEvent.submit(form as HTMLFormElement)
    expect(requestReset).toHaveBeenCalledTimes(1)

    await act(async () => {
      reject(
        new ApiError({
          status: 429,
          code: 'authn.throttled',
          attempts: 1,
          retryAfterMs: 30000,
        }),
      )
      await Promise.resolve()
    })

    expect(send).toHaveFocus()
    expect(send).toBeEnabled()
    expect(send).toHaveAttribute('aria-disabled', 'true')
    expect(send).not.toHaveAttribute('aria-busy')
    expect(send).toHaveAccessibleDescription(waitSentence(30))
    await user.click(send)
    expect(requestReset).toHaveBeenCalledTimes(1)
    fireEvent.submit(form as HTMLFormElement)
    expect(requestReset).toHaveBeenCalledTimes(1)

    send.blur()
    await user.tab()
    await user.tab()
    expect(send).toHaveFocus()
  })

  it('keep the resend control focused through its send and the stated wait', async () => {
    const { requestReset: resendRequest, reject } = deferredSend()
    const requestReset = vi
      .fn()
      .mockResolvedValueOnce({ ...SENT, resendAfter: 0 })
      .mockImplementationOnce(resendRequest)
    renderWithProviders(
      <PasswordResetScreen
        session={session()}
        channels={['email']}
        operations={resetOperations({ requestReset })}
        humanGate={OPEN_GATE}
      />,
    )
    const user = userEvent.setup()
    await user.type(screen.getByLabelText(zhCN.passwordReset.emailLabel), MAIL)
    await user.click(
      screen.getByRole('button', { name: zhCN.passwordReset.send }),
    )
    await screen.findByText(zhCN.passwordReset.sentNotice)
    const resend = screen.getByRole('button', {
      name: zhCN.passwordReset.resend,
    })
    await user.click(resend)

    expect(resend).toHaveFocus()
    expect(resend).toBeEnabled()
    expect(resend).toHaveAttribute('aria-disabled', 'true')
    expect(resend).toHaveAttribute('aria-busy', 'true')
    const submit = screen.getByRole('button', {
      name: zhCN.passwordReset.submit,
    })
    expect(submit).toHaveAttribute('aria-disabled', 'true')
    expect(submit).not.toHaveAttribute('aria-busy')
    expect(submit).not.toHaveAttribute('aria-describedby')

    await act(async () => {
      reject(
        new ApiError({
          status: 429,
          code: 'authn.throttled',
          attempts: 1,
          retryAfterMs: 30000,
        }),
      )
      await Promise.resolve()
    })

    expect(resend).toHaveFocus()
    expect(resend).toBeEnabled()
    expect(resend).toHaveAttribute('aria-disabled', 'true')
    expect(resend).not.toHaveAttribute('aria-busy')
    expect(resend).toHaveAccessibleDescription(waitSentence(30))
    await user.click(resend)
    expect(requestReset).toHaveBeenCalledTimes(2)
  })

  it('describe the resend with the stated wait when it is throttled again', async () => {
    vi.useFakeTimers()
    try {
      let rejectResend: (error: unknown) => void = () => undefined
      const requestReset = vi
        .fn()
        .mockResolvedValueOnce({ ...SENT, resendAfter: 0 })
        .mockImplementationOnce(
          () =>
            new Promise<PasswordResetSent>((_resolve, reject) => {
              rejectResend = reject
            }),
        )
        .mockRejectedValueOnce(
          new ApiError({
            status: 429,
            code: 'authn.throttled',
            attempts: 1,
            retryAfterMs: 3000,
          }),
        )
      renderWithProviders(
        <PasswordResetScreen
          session={session()}
          channels={['email']}
          operations={resetOperations({ requestReset })}
          humanGate={OPEN_GATE}
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
      const resend = screen.getByRole('button', {
        name: zhCN.passwordReset.resend,
      })

      await act(async () => {
        fireEvent.click(resend)
        await Promise.resolve()
      })
      await act(async () => {
        rejectResend(
          new ApiError({
            status: 429,
            code: 'authn.throttled',
            attempts: 1,
            retryAfterMs: 3000,
          }),
        )
        await Promise.resolve()
      })
      expect(resend).toHaveAttribute('aria-disabled', 'true')
      expect(resend).toHaveAccessibleDescription(waitSentence(3))

      act(() => {
        vi.advanceTimersByTime(3000)
      })
      expect(resend).not.toHaveAttribute('aria-disabled', 'true')
      expect(resend).not.toHaveAccessibleDescription(waitSentence(3))
      await act(async () => {
        fireEvent.click(resend)
        await Promise.resolve()
        await Promise.resolve()
      })
      expect(resend).toHaveAttribute('aria-disabled', 'true')
      expect(resend).toHaveAccessibleDescription(waitSentence(3))
    } finally {
      await restoreRealTimers()
    }
  })

  it('land on the done heading and leave the way back next', async () => {
    const onBackToSignIn = vi.fn()
    const user = await reachSent(resetOperations(), { onBackToSignIn })
    await fillCodeAndPassword(user)

    const heading = await screen.findByRole('heading', {
      name: zhCN.passwordReset.doneTitle,
    })
    expect(heading).toHaveAttribute('tabindex', '-1')
    expect(heading).toHaveFocus()
    await user.tab()
    expect(
      screen.getByRole('button', { name: zhCN.passwordReset.backToSignIn }),
    ).toHaveFocus()
  })

  it('land on the done heading when the host binds no exit', async () => {
    const user = await reachSent(resetOperations())
    await fillCodeAndPassword(user)

    const heading = await screen.findByRole('heading', {
      name: zhCN.passwordReset.doneTitle,
    })
    expect(heading).toHaveAttribute('tabindex', '-1')
    expect(heading).toHaveFocus()
  })

  it('leave the focus the viewer moved during the write where it is', async () => {
    const { completeReset, settle } = deferredComplete()
    const user = userEvent.setup()
    renderWithProviders(
      <>
        <PasswordResetScreen
          session={session()}
          channels={['email']}
          operations={resetOperations({ completeReset })}
        />
        <button type="button">outside</button>
      </>,
    )
    await user.type(screen.getByLabelText(zhCN.passwordReset.emailLabel), MAIL)
    await user.click(
      screen.getByRole('button', { name: zhCN.passwordReset.send }),
    )
    await screen.findByText(zhCN.passwordReset.sentNotice)
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
    const outside = screen.getByRole('button', { name: 'outside' })
    await user.click(outside)
    expect(outside).toHaveFocus()

    await act(async () => {
      settle()
      await Promise.resolve()
    })
    await screen.findByRole('heading', { name: zhCN.passwordReset.doneTitle })
    expect(outside).toHaveFocus()
  })

  it('keep the code submit available while a held resend waits for its proof', async () => {
    const fake = installFakeTurnstile()
    try {
      const verifyReset = vi.fn(() => Promise.resolve(VERIFIED))
      const requestReset = vi
        .fn()
        .mockResolvedValueOnce({ ...SENT, resendAfter: 0 })
        .mockResolvedValueOnce(SENT)
      renderWithProviders(
        <PasswordResetScreen
          session={session()}
          channels={['email']}
          operations={resetOperations({ requestReset, verifyReset })}
          humanGate={{
            phone: { required: false, provider: 'off' },
            other: { required: true, provider: 'turnstile', site_key: 'site-1' },
          }}
        />,
      )
      const user = userEvent.setup()
      await user.type(
        screen.getByLabelText(zhCN.passwordReset.emailLabel),
        MAIL,
      )
      await user.click(
        screen.getByRole('button', { name: zhCN.passwordReset.send }),
      )
      await waitFor(() => expect(fake.widgets).toHaveLength(1))
      act(() => {
        fake.token('proof-1')
      })
      await screen.findByText(zhCN.passwordReset.sentNotice)
      await user.type(
        screen.getByLabelText(zhCN.passwordReset.codeLabel),
        CODE,
      )
      await user.type(
        screen.getByLabelText(zhCN.passwordReset.newPasswordLabel),
        NEW_PASSWORD,
      )
      await user.type(
        screen.getByLabelText(zhCN.passwordReset.confirmPasswordLabel),
        NEW_PASSWORD,
      )

      const resend = screen.getByRole('button', {
        name: zhCN.passwordReset.resend,
      })
      await user.click(resend)
      await waitFor(() => expect(fake.widgets).toHaveLength(2))
      expect(resend).toHaveFocus()
      expect(resend).toHaveAttribute('aria-disabled', 'true')
      expect(resend).toHaveAttribute('aria-busy', 'true')

      const submit = screen.getByRole('button', {
        name: zhCN.passwordReset.submit,
      })
      expect(submit).not.toHaveAttribute('aria-disabled', 'true')
      expect(submit).not.toHaveAttribute('aria-busy')
      await user.click(submit)
      await waitFor(() =>
        expect(verifyReset).toHaveBeenCalledWith({ code: CODE }),
      )
      expect(requestReset).toHaveBeenCalledTimes(1)
    } finally {
      fake.restore()
    }
  })

  it('hold the resend, not busy, while the code submit writes', async () => {
    const { verifyReset, settle } = deferredVerify()
    const requestReset = vi
      .fn()
      .mockResolvedValueOnce({ ...SENT, resendAfter: 0 })
    renderWithProviders(
      <PasswordResetScreen
        session={session()}
        channels={['email']}
        operations={resetOperations({ requestReset, verifyReset })}
        humanGate={OPEN_GATE}
      />,
    )
    const user = userEvent.setup()
    await user.type(screen.getByLabelText(zhCN.passwordReset.emailLabel), MAIL)
    await user.click(
      screen.getByRole('button', { name: zhCN.passwordReset.send }),
    )
    await screen.findByText(zhCN.passwordReset.sentNotice)
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

    const resend = screen.getByRole('button', {
      name: zhCN.passwordReset.resend,
    })
    expect(resend).toHaveAttribute('aria-disabled', 'true')
    expect(resend).not.toHaveAttribute('aria-busy')
    expect(resend).not.toHaveAttribute('aria-describedby')
    await user.click(resend)
    fireEvent.keyDown(resend, { key: 'Enter' })
    expect(requestReset).toHaveBeenCalledTimes(1)

    await act(async () => {
      settle()
      await Promise.resolve()
    })
    await screen.findByRole('heading', { name: zhCN.passwordReset.doneTitle })
  })

  it('runs the other surface for an email reset', async () => {
    const harness = makeHarness({
      [CAPTCHA_CHALLENGE]: () => captchaChallenge(),
    })
    renderWithProviders(
      <PasswordResetScreen
        session={harness.session}
        channels={['email']}
        operations={resetOperations()}
        humanGate={{
          phone: { required: false, provider: 'off' },
          other: { required: true, provider: 'captcha' },
        }}
      />,
    )
    const user = userEvent.setup()
    await user.type(screen.getByLabelText(zhCN.passwordReset.emailLabel), MAIL)
    await user.click(
      screen.getByRole('button', { name: zhCN.passwordReset.send }),
    )
    expect(
      await screen.findByLabelText(
        uiKitZhCN.humanVerification.captcha.answerLabel,
      ),
    ).toBeInTheDocument()
    expect(
      harness.calls.filter(
        (call) => call.path === '/api/v1/auth/human/captcha/challenge',
      ),
    ).toHaveLength(1)
  })

  it('runs the phone surface for an SMS reset', async () => {
    const harness = makeHarness({
      [CAPTCHA_CHALLENGE]: () => captchaChallenge(),
    })
    renderWithProviders(
      <PasswordResetScreen
        session={harness.session}
        channels={['sms']}
        operations={resetOperations()}
        humanGate={{
          phone: { required: true, provider: 'captcha' },
          other: { required: false, provider: 'off' },
        }}
      />,
    )
    const user = userEvent.setup()
    await user.type(
      screen.getByLabelText(zhCN.passwordReset.phoneLabel),
      '+8613800138000',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.passwordReset.send }),
    )
    expect(
      await screen.findByLabelText(
        uiKitZhCN.humanVerification.captcha.answerLabel,
      ),
    ).toBeInTheDocument()
    expect(
      harness.calls.filter(
        (call) => call.path === '/api/v1/auth/human/captcha/challenge',
      ),
    ).toHaveLength(1)
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
    const user = await reachSent(operations, { onDone, onBackToSignIn: () => undefined })
    await fillCodeAndPassword(user)

    await waitFor(() =>
      expect(
        screen.getByText(zhCN.passwordReset.policyFailure),
      ).toBeInTheDocument(),
    )
    expect(leadingBackRow().querySelector('input')).toBeNull()
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
      const resendWaiting = screen.getByRole('button', {
        name: zhCN.passwordReset.resendIn.replace('{{seconds}}', '30'),
      })
      expect(resendWaiting).toHaveAttribute('aria-disabled', 'true')
      expect(resendWaiting).toBeEnabled()
      fireEvent.click(resendWaiting)
      expect(requestReset).toHaveBeenCalledTimes(1)
      act(() => {
        vi.advanceTimersByTime(29000)
      })
      expect(
        screen.getByRole('button', {
          name: zhCN.passwordReset.resendIn.replace('{{seconds}}', '1'),
        }),
      ).toHaveAttribute('aria-disabled', 'true')
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
      await restoreRealTimers()
    }
  })

  it('count a stated throttled retry down, re-enable the send and keep the banner', async () => {
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
      expect(send).toHaveAttribute('aria-disabled', 'true')

      const alert = screen.getByRole('alert')
      expect(visibleSentence(alert)).toHaveTextContent(waitSentence(3))
      expect(stableSentence(alert)).toHaveTextContent(waitSentence(3))

      act(() => {
        vi.advanceTimersByTime(1000)
      })
      expect(visibleSentence(alert)).toHaveTextContent(waitSentence(2))
      expect(stableSentence(alert)).toHaveTextContent(waitSentence(3))
      expect(send).toHaveAttribute('aria-disabled', 'true')

      act(() => {
        vi.advanceTimersByTime(1999)
      })
      expect(send).toHaveAttribute('aria-disabled', 'true')

      act(() => {
        vi.advanceTimersByTime(1)
      })
      expect(send).not.toHaveAttribute('aria-disabled', 'true')
      expect(visibleSentence(alert)).toHaveTextContent(
        zhCN.errors.authn.throttled,
      )
      expect(visibleSentence(alert)).not.toHaveTextContent('0')
      expect(stableSentence(alert)).toHaveTextContent(waitSentence(3))
      expect(screen.getByRole('alert')).toBe(alert)
    } finally {
      await restoreRealTimers()
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
