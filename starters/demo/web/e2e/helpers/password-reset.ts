/**
 * The rendered surface of the anonymous password reset, driven the way a person
 * drives it: the forgot-password entry, the channel choice, the identifier and
 * code fields, the new password and the done state.
 *
 * The one non-UI read is the verification code, which the review carriers write
 * to the process log (fixtures/log.ts). The review build lowers the inbox and
 * number resend intervals to their floor, and the resend window is held by the
 * destination itself across purposes: the code the account proved its own email
 * or phone with shortly before can still hold the window open, so the first
 * reset send is suppressed (the product still answers its sent notice). The
 * surface's resend control states when another request is allowed; this helper
 * reads the first send when it produced a code, and otherwise waits out that
 * control and presses it, then reads the code the resend delivered. That is the
 * product's own countdown, never a fixed sleep.
 */

import { expect, type Locator, type Page } from '@playwright/test'

import { waitForVerificationCode } from '../fixtures/log'
import { copy } from './i18n'

/** The reset channel a case drives. */
export type ResetChannel = 'email' | 'sms'

/**
 * The process log the code reader marks and waits on. Derived from the log
 * fixture's own function so this helper imports no type it is not allowed to.
 */
export type ResetServerLog = Parameters<typeof waitForVerificationCode>[0]

/** The sign-in surface's forgot-password entry. */
export function forgotPasswordEntry(page: Page): Locator {
  return page.getByRole('button', {
    name: copy('authUi', 'passwordSignIn.forgotPassword'),
    exact: true,
  })
}

/** The reset page's own heading. */
export function resetHeading(page: Page): Locator {
  return page.getByRole('heading', {
    name: copy('authUi', 'passwordReset.title'),
    exact: true,
  })
}

/** One channel's choice on the request form. */
function channelChoice(page: Page, channel: ResetChannel): Locator {
  return page.getByRole('button', {
    name: copy('authUi', `passwordReset.channel.${channel}`),
    exact: true,
  })
}

/** The identifier field a channel asks for. */
export function identifierField(page: Page, channel: ResetChannel): Locator {
  const label =
    channel === 'sms'
      ? copy('authUi', 'passwordReset.phoneLabel')
      : copy('authUi', 'passwordReset.emailLabel')
  return page.getByRole('textbox', { name: label, exact: true })
}

/** The send control. */
export function sendButton(page: Page): Locator {
  return page.getByRole('button', {
    name: copy('authUi', 'passwordReset.send'),
    exact: true,
  })
}

/** The same-shape "a code was sent" notice. */
export function sentNotice(page: Page): Locator {
  return page.getByText(copy('authUi', 'passwordReset.sentNotice'), {
    exact: true,
  })
}

/** The code field of the sent form. */
export function codeField(page: Page): Locator {
  return page.getByRole('textbox', {
    name: copy('authUi', 'passwordReset.codeLabel'),
    exact: true,
  })
}

/** The new-password field of the sent or proved form. */
export function newPasswordField(page: Page): Locator {
  return page.getByLabel(copy('authUi', 'passwordReset.newPasswordLabel'), {
    exact: true,
  })
}

/** The confirmation field. */
export function confirmPasswordField(page: Page): Locator {
  return page.getByLabel(copy('authUi', 'passwordReset.confirmPasswordLabel'), {
    exact: true,
  })
}

/** The submit control of the sent or proved form. */
export function submitButton(page: Page): Locator {
  return page.getByRole('button', {
    name: copy('authUi', 'passwordReset.submit'),
    exact: true,
  })
}

/** The done state's title. */
export function doneHeading(page: Page): Locator {
  return page.getByRole('heading', {
    name: copy('authUi', 'passwordReset.doneTitle'),
    exact: true,
  })
}

/** The way back to the sign-in surface, present in the done state. */
export function backToSignInButton(page: Page): Locator {
  return page.getByRole('button', {
    name: copy('authUi', 'passwordReset.backToSignIn'),
    exact: true,
  })
}

/**
 * Open the reset page from the sign-in surface and choose a channel. The entry
 * exists only while the deployment publishes a reset channel.
 */
export async function openReset(
  page: Page,
  channel: ResetChannel,
): Promise<void> {
  await forgotPasswordEntry(page).click()
  await expect(resetHeading(page)).toBeVisible()
  // Both channels render a choice; a single-channel deployment renders no
  // toggle, so the click is offered only where it exists.
  const choice = channelChoice(page, channel)
  if (await choice.isVisible()) {
    await choice.click()
  }
}

/**
 * Fill the chosen channel's identifier and ask for a code. The caller takes
 * the log cursor before this call, so the code read cannot accept an earlier
 * message to the same address.
 */
export async function requestResetCode(
  page: Page,
  channel: ResetChannel,
  identifier: string,
): Promise<void> {
  await identifierField(page, channel).fill(identifier)
  await sendButton(page).click()
  await expect(sentNotice(page)).toBeVisible()
}

/**
 * Read the code the reset send produced, pressing the resend control when the
 * first send was suppressed by the destination's outstanding resend window.
 * The return value is the newest code for the destination at or after the
 * cursor the caller took before the first send (or before the resend).
 */
export async function readResetCode(
  page: Page,
  log: ResetServerLog,
  kind: 'sms' | 'mail',
  address: string,
  afterSend: number,
): Promise<string> {
  try {
    const first = await waitForVerificationCode(log, kind, address, {
      after: afterSend,
      timeoutMs: 8_000,
    })
    return first.code
  } catch {
    // The first send met the destination's resend window and was suppressed.
    // Wait for the surface's own countdown, then resend and read that message.
  }
  const resend = page.getByRole('button', {
    name: copy('authUi', 'passwordReset.resend'),
    exact: true,
  })
  await expect(resend).toBeEnabled({ timeout: 45_000 })
  const afterResend = log.mark()
  await resend.click()
  await expect(sentNotice(page)).toBeVisible()
  const resent = await waitForVerificationCode(log, kind, address, {
    after: afterResend,
  })
  return resent.code
}

/**
 * Answer the sent form: the code, the new password and its confirmation in the
 * surface's one submit action, then wait for the done state.
 */
export async function completeReset(
  page: Page,
  code: string,
  newPassword: string,
): Promise<void> {
  await codeField(page).fill(code)
  await newPasswordField(page).fill(newPassword)
  await confirmPasswordField(page).fill(newPassword)
  await submitButton(page).click()
  await expect(doneHeading(page)).toBeVisible()
}

/**
 * Wait past the second boundary the completion's `valid_after` advance draws.
 *
 * The password write moves the account's `valid_after` to the second after the
 * write, so every session authenticated in that same wall-clock second is
 * already stale — a sign-in performed immediately after the done state would be
 * issued and then discarded. A person cannot sign in faster than this, and the
 * product exposes no countdown; the test therefore crosses the boundary before
 * it signs in. The wait is anchored to the moment the done state appeared, not
 * to the run's start.
 */
export async function waitOutTheResetSecond(page: Page): Promise<void> {
  await page.waitForTimeout(1_200)
}

/**
 * Submit a code to a suppressed record: every failure is the same field
 * refusal, so an unregistered identifier and a wrong code are one observation.
 */
export async function expectCodeRefused(
  page: Page,
  code: string,
  newPassword: string,
): Promise<void> {
  await codeField(page).fill(code)
  await newPasswordField(page).fill(newPassword)
  await confirmPasswordField(page).fill(newPassword)
  await submitButton(page).click()
  await expect(
    page.getByText(copy('authUi', 'passwordReset.codeRejected'), {
      exact: true,
    }),
  ).toBeVisible()
}
