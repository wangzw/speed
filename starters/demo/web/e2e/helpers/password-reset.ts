// The resend window is held by the destination across purposes, so an earlier proof's code can suppress the first reset send (the product still answers its sent notice). Read the first send when it produced a code; otherwise wait out the resend control and press it. Never a fixed sleep.

import { expect, type Locator, type Page } from '@playwright/test'

import { waitForVerificationCode } from '../fixtures/log'
import { scaledTimeout } from '../fixtures/time-scale'
import { copy } from './i18n'

export type ResetChannel = 'email' | 'sms'

export type ResetServerLog = Parameters<typeof waitForVerificationCode>[0]

export function forgotPasswordEntry(page: Page): Locator {
  return page.getByRole('button', {
    name: copy('authUi', 'passwordSignIn.forgotPassword'),
    exact: true,
  })
}

export function resetHeading(page: Page): Locator {
  return page.getByRole('heading', {
    name: copy('authUi', 'passwordReset.title'),
    exact: true,
  })
}

function channelChoice(page: Page, channel: ResetChannel): Locator {
  return page.getByRole('button', {
    name: copy('authUi', `passwordReset.channel.${channel}`),
    exact: true,
  })
}

export function identifierField(page: Page, channel: ResetChannel): Locator {
  const label =
    channel === 'sms'
      ? copy('authUi', 'passwordReset.phoneLabel')
      : copy('authUi', 'passwordReset.emailLabel')
  return page.getByRole('textbox', { name: label, exact: true })
}

export function sendButton(page: Page): Locator {
  return page.getByRole('button', {
    name: copy('authUi', 'passwordReset.send'),
    exact: true,
  })
}

export function sentNotice(page: Page): Locator {
  return page.getByText(copy('authUi', 'passwordReset.sentNotice'), {
    exact: true,
  })
}

export function codeField(page: Page): Locator {
  return page.getByRole('textbox', {
    name: copy('authUi', 'passwordReset.codeLabel'),
    exact: true,
  })
}

export function newPasswordField(page: Page): Locator {
  return page.getByLabel(copy('authUi', 'passwordReset.newPasswordLabel'), {
    exact: true,
  })
}

export function confirmPasswordField(page: Page): Locator {
  return page.getByLabel(copy('authUi', 'passwordReset.confirmPasswordLabel'), {
    exact: true,
  })
}

export function submitButton(page: Page): Locator {
  return page.getByRole('button', {
    name: copy('authUi', 'passwordReset.submit'),
    exact: true,
  })
}

export function doneHeading(page: Page): Locator {
  return page.getByRole('heading', {
    name: copy('authUi', 'passwordReset.doneTitle'),
    exact: true,
  })
}

export function backToSignInButton(page: Page): Locator {
  return page.getByRole('button', {
    name: copy('authUi', 'passwordReset.backToSignIn'),
    exact: true,
  })
}

export async function openReset(
  page: Page,
  channel: ResetChannel,
): Promise<void> {
  await forgotPasswordEntry(page).click()
  await expect(resetHeading(page)).toBeVisible()
  const choice = channelChoice(page, channel)
  if (await choice.isVisible()) {
    await choice.click()
  }
}

// Take the log cursor before this call so an earlier code to the same address cannot satisfy the read.
export async function requestResetCode(
  page: Page,
  channel: ResetChannel,
  identifier: string,
): Promise<void> {
  await identifierField(page, channel).fill(identifier)
  await sendButton(page).click()
  await expect(sentNotice(page)).toBeVisible()
}

// Both outcomes are observable, so branch on the product's state: a delivered send writes a code, a suppressed one leaves the resend control counting down. The first state to appear decides.
export async function readResetCode(
  page: Page,
  log: ResetServerLog,
  kind: 'sms' | 'mail',
  address: string,
  afterSend: number,
): Promise<string> {
  const resend = page.getByRole('button', {
    name: copy('authUi', 'passwordReset.resend'),
    exact: true,
  })
  const firstCode = waitForVerificationCode(log, kind, address, {
    after: afterSend,
  }).then(
    (message) => message.code,
    () => 'window',
  )
  const windowElapsed = expect(resend)
    .toBeEnabled({ timeout: scaledTimeout(60_000) })
    .then(
      () => 'window',
      () => 'window',
    )
  const branch = await Promise.race([firstCode, windowElapsed])
  if (branch !== 'window') {
    return branch
  }
  await expect(resend).toBeEnabled({ timeout: scaledTimeout(60_000) })
  const afterResend = log.mark()
  await resend.click()
  await expect(sentNotice(page)).toBeVisible()
  const resent = await waitForVerificationCode(log, kind, address, {
    after: afterResend,
  })
  return resent.code
}

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

// The password write moves `valid_after` to the next second, so a session authenticated in the same wall-clock second is issued and then discarded. Cross the boundary before signing in.
export async function waitOutTheResetSecond(page: Page): Promise<void> {
  await page.waitForTimeout(1_200)
}

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
