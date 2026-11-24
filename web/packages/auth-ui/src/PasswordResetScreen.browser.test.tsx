import { describe, expect, it, vi } from 'vitest'
import { act, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { page, userEvent as browserUserEvent } from 'vitest/browser'
import { makeHarness } from '@speed/test-utils/session-harness'
import { installFakeTurnstile } from '@speed/test-utils/turnstile'
import { renderWithProviders } from '../test-utils/render.js'
import { PasswordResetScreen } from './PasswordResetScreen.js'
import type {
  PasswordResetOperations,
  PasswordResetSent,
} from './PasswordResetScreen.js'
import enUS from './locales/en-US.json' with { type: 'json' }
import uiKitEnUS from '../../ui-kit/src/locales/en-US.json' with { type: 'json' }

const ACTION_GAP = 8

const SENT: PasswordResetSent = {
  state: 'sent',
  expiresIn: 600,
  resendAfter: 30,
}

const OPERATIONS: PasswordResetOperations = {
  requestReset: () => Promise.resolve(SENT),
  verifyReset: () => Promise.reject(new Error('not used')),
  completeReset: () => Promise.resolve(),
}

const RESET_OPERATIONS: PasswordResetOperations = {
  requestReset: () => Promise.resolve(SENT),
  verifyReset: () =>
    Promise.resolve({ state: 'verified' as const, expiresIn: 600 }),
  completeReset: () => Promise.resolve(),
}

const NEW_PASSWORD = 'n3w-s3cret-pass'
const CODE = '123456'

function deferredRequest() {
  let settle: (value: PasswordResetSent) => void = () => undefined
  const requestReset = vi.fn(
    () =>
      new Promise<PasswordResetSent>((resolve) => {
        settle = resolve
      }),
  )
  return { requestReset, settle: () => settle(SENT) }
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

async function writeThePassword(
  user: ReturnType<typeof userEvent.setup>,
): Promise<void> {
  const email = screen.getByLabelText(enUS.passwordReset.emailLabel)
  await user.type(email, 'alice@example.com')
  await user.click(
    screen.getByRole('button', { name: enUS.passwordReset.send }),
  )
  await screen.findByText(enUS.passwordReset.sentNotice)
  await user.type(
    screen.getByLabelText(enUS.passwordReset.codeLabel),
    CODE,
  )
  await user.type(
    screen.getByLabelText(enUS.passwordReset.newPasswordLabel),
    NEW_PASSWORD,
  )
  await user.type(
    screen.getByLabelText(enUS.passwordReset.confirmPasswordLabel),
    NEW_PASSWORD,
  )
  await user.click(
    screen.getByRole('button', { name: enUS.passwordReset.submit }),
  )
}

function actionRowOf(action: Element): HTMLElement {
  const row = action.closest<HTMLElement>('[data-action-row]')
  if (row === null) {
    throw new Error('the action is not inside an action row')
  }
  return row
}

function contentRightOf(row: HTMLElement): number {
  const rowBox = row.getBoundingClientRect()
  return rowBox.right - Number.parseFloat(getComputedStyle(row).paddingRight)
}

describe('PasswordResetScreen action row in Chromium', () => {
  it('keeps the request submit alone and closed on the right', async () => {
    await page.viewport(900, 900)
    renderWithProviders(
      <PasswordResetScreen
        session={makeHarness({}).session}
        channels={['email']}
        operations={OPERATIONS}
      />,
      { language: 'en-US' },
    )

    const submit = await screen.findByRole('button', {
      name: enUS.passwordReset.send,
    })
    const row = actionRowOf(submit)
    expect(
      Math.abs(submit.getBoundingClientRect().right - contentRightOf(row)),
    ).toBeLessThanOrEqual(1)
  })

  it('lands focus on the sent surface when the identifier submits by Enter', async () => {
    const user = userEvent.setup()
    renderWithProviders(
      <PasswordResetScreen
        session={makeHarness({}).session}
        channels={['email']}
        operations={OPERATIONS}
      />,
      { language: 'en-US' },
    )
    const email = screen.getByLabelText(enUS.passwordReset.emailLabel)
    await user.type(email, 'alice@example.com')
    await user.keyboard('{Enter}')
    await screen.findByText(enUS.passwordReset.sentNotice)

    expect(document.activeElement).toBe(
      screen.getByLabelText(enUS.passwordReset.codeLabel),
    )
  })

  it('leaves focus on the persistent back entry when the send lands', async () => {
    const { requestReset, settle } = deferredRequest()
    const user = userEvent.setup()
    renderWithProviders(
      <PasswordResetScreen
        session={makeHarness({}).session}
        channels={['email']}
        operations={{ ...OPERATIONS, requestReset }}
        onBackToSignIn={() => undefined}
      />,
      { language: 'en-US' },
    )
    const email = screen.getByLabelText(enUS.passwordReset.emailLabel)
    await user.type(email, 'alice@example.com')
    await user.keyboard('{Enter}')
    await waitFor(() => expect(requestReset).toHaveBeenCalledTimes(1))
    const back = screen.getByRole('button', {
      name: enUS.passwordReset.backToSignIn,
    })
    await user.tab()
    expect(document.activeElement).toBe(back)

    await act(async () => {
      settle()
      await Promise.resolve()
    })
    await screen.findByText(enUS.passwordReset.sentNotice)
    expect(document.activeElement).toBe(back)
  })

  it('keeps the resend focused and busy while its proof is outstanding', async () => {
    const fake = installFakeTurnstile()
    try {
      const user = userEvent.setup()
      renderWithProviders(
        <PasswordResetScreen
          session={makeHarness({}).session}
          channels={['email']}
          operations={{
            ...OPERATIONS,
            requestReset: () =>
              Promise.resolve({ ...SENT, resendAfter: 0 }),
          }}
          humanGate={{
            phone: { required: false, provider: 'off' },
            other: { required: true, provider: 'turnstile', site_key: 'site-1' },
          }}
        />,
        { language: 'en-US' },
      )
      await user.type(
        screen.getByLabelText(enUS.passwordReset.emailLabel),
        'alice@example.com',
      )
      await user.click(
        screen.getByRole('button', { name: enUS.passwordReset.send }),
      )
      await waitFor(() => expect(fake.widgets).toHaveLength(1))
      act(() => {
        fake.token('proof-1')
      })
      await screen.findByText(enUS.passwordReset.sentNotice)
      const resend = screen.getByRole('button', {
        name: enUS.passwordReset.resend,
      })
      resend.focus()
      expect(document.activeElement).toBe(resend)
      await browserUserEvent.keyboard('{Enter}')
      await waitFor(() => expect(fake.widgets).toHaveLength(2))
      await waitFor(() => expect(resend).toHaveAttribute('aria-busy', 'true'))
      expect(
        screen.getByRole('group', {
          name: uiKitEnUS.humanVerification.groupLabel,
        }),
      ).toBeInTheDocument()
      expect(resend).toHaveFocus()
      expect(resend).not.toHaveAttribute('disabled')
    } finally {
      fake.restore()
    }
  })

  it('groups back, resend and submit one token apart, as one right-aligned row', async () => {
    await page.viewport(900, 900)
    const user = userEvent.setup()
    renderWithProviders(
      <PasswordResetScreen
        session={makeHarness({}).session}
        channels={['email']}
        operations={OPERATIONS}
        onBackToSignIn={() => undefined}
      />,
      { language: 'en-US' },
    )

    await user.type(
      screen.getByLabelText(enUS.passwordReset.emailLabel),
      'alice@example.com',
    )
    await user.click(
      screen.getByRole('button', { name: enUS.passwordReset.send }),
    )
    await screen.findByText(enUS.passwordReset.sentNotice)

    const back = screen.getByRole('button', {
      name: enUS.passwordReset.backToSignIn,
    })
    const resend = screen.getByRole('button', {
      name: enUS.passwordReset.resendIn.replace('{{seconds}}', '30'),
    })
    const submit = screen.getByRole('button', {
      name: enUS.passwordReset.submit,
    })

    const backBox = back.getBoundingClientRect()
    const resendBox = resend.getBoundingClientRect()
    const submitBox = submit.getBoundingClientRect()
    const row = actionRowOf(submit)
    expect(actionRowOf(back)).toBe(row)
    expect(actionRowOf(resend)).toBe(row)

    expect(Math.abs(backBox.top - submitBox.top)).toBeLessThanOrEqual(1)
    expect(Math.abs(resendBox.top - submitBox.top)).toBeLessThanOrEqual(1)
    expect(resendBox.left - backBox.right).toBeCloseTo(ACTION_GAP, 0)
    expect(submitBox.left - resendBox.right).toBeCloseTo(ACTION_GAP, 0)
    expect(Math.abs(submitBox.right - contentRightOf(row))).toBeLessThanOrEqual(
      1,
    )
  })

  it('lands focus on the done heading when the form is replaced', async () => {
    const user = userEvent.setup()
    renderWithProviders(
      <PasswordResetScreen
        session={makeHarness({}).session}
        channels={['email']}
        operations={RESET_OPERATIONS}
      />,
      { language: 'en-US' },
    )
    await writeThePassword(user)

    const heading = await screen.findByRole('heading', {
      name: enUS.passwordReset.doneTitle,
    })
    expect(heading).toHaveAttribute('tabindex', '-1')
    expect(document.activeElement).toBe(heading)
  })

  it('leaves the focus the viewer moved during the write where it is', async () => {
    const { completeReset, settle } = deferredComplete()
    const user = userEvent.setup()
    renderWithProviders(
      <>
        <PasswordResetScreen
          session={makeHarness({}).session}
          channels={['email']}
          operations={{ ...RESET_OPERATIONS, completeReset }}
        />
        <button type="button">outside</button>
      </>,
      { language: 'en-US' },
    )
    await writeThePassword(user)
    const outside = screen.getByRole('button', { name: 'outside' })
    await user.click(outside)
    expect(document.activeElement).toBe(outside)

    await act(async () => {
      settle()
      await Promise.resolve()
    })
    await screen.findByRole('heading', {
      name: enUS.passwordReset.doneTitle,
    })
    expect(document.activeElement).toBe(outside)
  })
})
