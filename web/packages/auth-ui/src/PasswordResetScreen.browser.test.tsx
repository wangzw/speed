/**
 * PasswordResetScreen's action row in a real browser: the send/submit key
 * and, once a code is out, the back and resend controls share one group
 * with the one spacing token between adjacent controls, and the primary
 * closes the row at the right edge. The old space-between row put the back
 * entry and the primary hundreds of pixels apart; these cases measure the
 * boxes jsdom cannot lay out.
 */

import { describe, expect, it } from 'vitest'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { page } from 'vitest/browser'
import { makeHarness } from '@speed/test-utils/session-harness'
import { renderWithProviders } from '../test-utils/render.js'
import { PasswordResetScreen } from './PasswordResetScreen.js'
import type {
  PasswordResetOperations,
  PasswordResetSent,
} from './PasswordResetScreen.js'
import enUS from './locales/en-US.json' with { type: 'json' }

/** The one spacing token an action row puts between adjacent controls. */
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

/** The row an action belongs to, found by the row's own marker. */
function actionRowOf(action: Element): HTMLElement {
  const row = action.closest<HTMLElement>('[data-action-row]')
  if (row === null) {
    throw new Error('the action is not inside an action row')
  }
  return row
}

/** The row's right content edge (the row may carry its own padding). */
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
    // All three controls are members of the one row.
    expect(actionRowOf(back)).toBe(row)
    expect(actionRowOf(resend)).toBe(row)

    // One line.
    expect(Math.abs(backBox.top - submitBox.top)).toBeLessThanOrEqual(1)
    expect(Math.abs(resendBox.top - submitBox.top)).toBeLessThanOrEqual(1)
    // Adjacent, one spacing token apart, primary last and right.
    expect(resendBox.left - backBox.right).toBeCloseTo(ACTION_GAP, 0)
    expect(submitBox.left - resendBox.right).toBeCloseTo(ACTION_GAP, 0)
    expect(Math.abs(submitBox.right - contentRightOf(row))).toBeLessThanOrEqual(
      1,
    )
  })
})
