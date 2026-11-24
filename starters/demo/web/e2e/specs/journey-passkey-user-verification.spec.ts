// @journey journey-passkey-user-verification.md
// @covers REQ-security-039, REQ-security-008, REQ-signin-024
import type { BrowserContext, Locator, Page } from '@playwright/test'

import { createAccounts, type Accounts } from '../helpers/accounts'
import { expect, test } from '../fixtures/demo'
import { scaledTimeout } from '../fixtures/time-scale'
import {
  expandAccountRows,
  openAccountSection,
  selectAccountSection,
} from '../helpers/account-sections'
import { copy } from '../helpers/i18n'
import { fillFactorName } from '../helpers/mfa'
import { totpCode } from '../helpers/totp'
import {
  addVirtualAuthenticator,
  type VirtualAuthenticator,
} from '../helpers/virtual-authenticator'

// Registration, two factor enrolments, several sign-ins, a second account assembly and fixture assembly exceed the tier's default; keep this bound above it.
test.describe.configure({ timeout: scaledTimeout(300_000) })

const PASSKEY_FACTOR_NAME = 'Backup passkey'

function homeHeading(page: Page): Locator {
  return page.getByRole('heading', {
    level: 1,
    name: copy('demo', 'home.heading'),
    exact: true,
  })
}

function signInHeading(page: Page): Locator {
  return page.getByRole('heading', {
    level: 1,
    name: copy('demo', 'signIn.heading'),
    exact: true,
  })
}

function accountHeading(page: Page): Locator {
  return page.getByRole('heading', {
    level: 1,
    name: copy('demo', 'account.sections.profile'),
    exact: true,
  })
}

function section(page: Page, title: string): Locator {
  return page.getByRole('region', { name: title, exact: true })
}

function mfaSection(page: Page): Locator {
  return section(page, copy('demo', 'account.rows.label.mfa'))
}

type MfaKind = 'totp' | 'webauthn' | 'sms'

function mfaGroup(page: Page, kind: MfaKind): Locator {
  return mfaSection(page).getByRole('region', {
    name: copy('accountUi', `mfa.factor.kind.${kind}`),
    exact: true,
  })
}

function addFactorAction(page: Page, kind: MfaKind): Locator {
  const method = copy('accountUi', `mfa.factor.kind.${kind}`)
  return mfaGroup(page, kind).getByRole('button', {
    name: copy('accountUi', 'mfa.addAria', { method }),
    exact: true,
  })
}

function passkeyEntry(page: Page): Locator {
  return page.getByRole('button', {
    name: copy('authUi', 'signInFlow.factorKind.webauthn'),
    exact: true,
  })
}

function factorStepTitle(page: Page): Locator {
  return page.getByText(copy('authUi', 'signInFlow.factorTitle'), {
    exact: true,
  })
}

function passkeyFailure(page: Page): Locator {
  return page.getByText(copy('authUi', 'signInFlow.passkeyCancelled'), {
    exact: true,
  })
}

let lastCodeStep = -1

test.beforeEach(() => {
  lastCodeStep = -1
})

// A code for the next window this spec has not used; a code accepted once is refused in the same window.
async function nextTotpCode(secret: string): Promise<string> {
  const stepMs = 30_000
  let step = Math.floor(Date.now() / stepMs)
  if (step <= lastCodeStep) {
    const resumeAt = (lastCodeStep + 1) * stepMs + 500
    const wait = resumeAt - Date.now()
    if (wait > 0) {
      await new Promise<void>((resolve) => setTimeout(resolve, wait))
    }
    step = Math.floor(Date.now() / stepMs)
  }
  lastCodeStep = step
  return totpCode(secret, new Date(step * stepMs))
}

async function completeTotpFactor(page: Page, secret: string): Promise<void> {
  const panel = page.getByRole('tabpanel', {
    name: copy('authUi', 'passwordSignIn.title'),
    exact: true,
  })
  const choice = panel.getByRole('button', {
    name: copy('authUi', 'signInFlow.factorKind.totp'),
    exact: true,
  })
  await expect(choice).toBeVisible()
  await choice.click()

  const codeField = panel.getByRole('textbox', {
    name: copy('authUi', 'smsSignIn.codeLabel'),
    exact: true,
  })
  await expect(codeField).toBeVisible()
  await codeField.fill(await nextTotpCode(secret))
  await panel
    .getByRole('button', {
      name: copy('authUi', 'signInFlow.submit'),
      exact: true,
    })
    .click()
  await expect(homeHeading(page)).toBeVisible()
}

async function revealedTotpSecret(mfa: Locator): Promise<string> {
  await mfa
    .getByRole('button', {
      name: copy('accountUi', 'flowStep.totpManualToggle'),
      exact: true,
    })
    .click()
  const secret = mfa.locator('code')
  await expect(secret).toHaveCount(1)
  await expect(secret).toBeVisible()
  return (await secret.innerText()).replace(/\s+/gu, '')
}

async function enrolTotp(page: Page): Promise<string> {
  const mfa = mfaSection(page)
  await addFactorAction(page, 'totp').click()
  await expect(
    mfa.getByRole('textbox', {
      name: copy('accountUi', 'mfa.enroll.labelField'),
      exact: true,
    }),
  ).toHaveCount(0)

  await expect(
    mfa.getByRole('img', {
      name: copy('accountUi', 'flowStep.totpQrLabel'),
      exact: true,
    }),
  ).toBeVisible()
  const secret = await revealedTotpSecret(mfa)

  const codeField = mfa.getByRole('textbox', {
    name: copy('accountUi', 'flowStep.codeLabel'),
    exact: true,
  })
  await expect(codeField).toBeVisible()
  await codeField.fill(await nextTotpCode(secret))
  await mfa
    .getByRole('button', {
      name: copy('accountUi', 'mfa.enroll.confirmLabel'),
      exact: true,
    })
    .click()
  await expect(codeField).toHaveCount(0)

  await expect(
    mfa.getByText(
      copy('accountUi', 'mfa.enroll.enrolled', {
        factor: copy('accountUi', 'mfa.factor.kind.totp'),
      }),
      { exact: true },
    ),
  ).toBeVisible()
  const group = mfaGroup(page, 'totp')
  await expect(group.getByRole('listitem')).toHaveCount(1)
  // The group's heading carries the same words, so scope the assertion to the row.
  await expect(
    group
      .getByRole('listitem')
      .getByText(copy('accountUi', 'mfa.factor.kind.totp'), { exact: true }),
  ).toBeVisible()
  return secret
}

async function enrolPasskey(page: Page): Promise<void> {
  const mfa = mfaSection(page)
  await addFactorAction(page, 'webauthn').click()
  await fillFactorName(mfa, PASSKEY_FACTOR_NAME)
  await expect(
    mfa.getByText(copy('accountUi', 'flowStep.passkeyRegisterPrompt'), {
      exact: true,
    }),
  ).toBeVisible()
  await mfa
    .getByRole('button', {
      name: copy('accountUi', 'mfa.enroll.confirmLabel'),
      exact: true,
    })
    .click()
  await expect(
    mfa.getByText(copy('accountUi', 'flowStep.passkeyRegisterPrompt'), {
      exact: true,
    }),
  ).toHaveCount(0)
  await expect(
    mfa.getByText(
      copy('accountUi', 'mfa.enroll.enrolled', {
        factor: copy('accountUi', 'mfa.factor.kind.webauthn'),
      }),
      { exact: true },
    ),
  ).toBeVisible()
  const group = mfaGroup(page, 'webauthn')
  await expect(group.getByRole('listitem')).toHaveCount(1)
  await expect(
    group.getByText(PASSKEY_FACTOR_NAME, { exact: true }),
  ).toBeVisible()
  await expect(addFactorAction(page, 'webauthn')).toHaveText(
    copy('accountUi', 'mfa.action.add'),
  )
}

async function choosePasskeySignIn(page: Page): Promise<void> {
  const entry = passkeyEntry(page)
  await expect(entry).toBeVisible()
  await entry.click()
}

test('passkey-user-verification: a user-verifying passkey signs in without a second factor', async ({
  demo,
  browser,
}) => {
  const { page, server } = demo
  const accounts: Accounts = demo.accounts
  const account = accounts.unique('passkey-uv')
  const passkeyOnly = accounts.unique('passkey-only')
  let authenticator: VirtualAuthenticator | undefined
  let otherContext: BrowserContext | undefined
  let otherAuthenticator: VirtualAuthenticator | undefined
  let otherPage: Page | undefined
  let secret = ''

  try {
    await test.step('precondition: an ordinary account is registered and its email address is proved', async () => {
      await accounts.registerAndVerify(account)
      await expect(accountHeading(page)).toBeVisible()
      authenticator = await addVirtualAuthenticator(page.context(), page)
    })

    await test.step('step 1: the account enrols the authenticator app as its second factor', async () => {
      await selectAccountSection(page, 'security')
      await expandAccountRows(page)
      secret = await enrolTotp(page)
      await expect(
        mfaGroup(page, 'totp').getByRole('listitem'),
      ).toHaveCount(1)
    })

    await test.step('step 2: a fresh password sign-in proves the second factor, which makes the session recent enough to write the passkey', async () => {
      await accounts.signOut()
      await accounts.signIn({
        identifier: account.username,
        password: account.password,
      })
      await completeTotpFactor(page, secret)
      await openAccountSection(page, 'security')
      await expandAccountRows(page)
    })

    await test.step('step 3: the account enrols a discoverable passkey beside the second factor', async () => {
      await enrolPasskey(page)
      await expect(
        mfaGroup(page, 'webauthn').getByText(PASSKEY_FACTOR_NAME, {
          exact: true,
        }),
      ).toBeVisible()
    })

    await test.step('step 4: choosing the passkey entry completes the login with user verification and no second factor', async () => {
      await accounts.signOut()
      await accounts.openSignIn()
      await choosePasskeySignIn(page)
      await expect(homeHeading(page)).toBeVisible()
      await expect(factorStepTitle(page)).toHaveCount(0)
      await expect(signInHeading(page)).toHaveCount(0)
    })

    await test.step('step 5: an attempt made without user verification is refused and stays on the sign-in surface', async () => {
      await accounts.signOut()
      await accounts.openSignIn()
      await authenticator?.setUserVerified(false)
      await choosePasskeySignIn(page)
      await expect(passkeyFailure(page)).toBeVisible()
      await expect(homeHeading(page)).toHaveCount(0)
      await expect(signInHeading(page)).toBeVisible()
      await expect(factorStepTitle(page)).toHaveCount(0)
      await authenticator?.setUserVerified(true)
    })

    await test.step('step 6: an account whose only factor is a passkey signs in with one ceremony', async () => {
      const options = {
        locale: 'en-US',
        timezoneId: 'UTC',
        viewport: page.viewportSize(),
      }
      otherContext = await browser.newContext(options)
      otherPage = await otherContext.newPage()
      const otherAccounts = createAccounts({ page: otherPage, server })
      await otherAccounts.registerAndVerify(passkeyOnly)
      await expect(accountHeading(otherPage)).toBeVisible()
      otherAuthenticator = await addVirtualAuthenticator(otherContext, otherPage)

      await selectAccountSection(otherPage, 'security')
      await expandAccountRows(otherPage)
      await enrolPasskey(otherPage)
      await expect(
        mfaGroup(otherPage, 'webauthn').getByText(PASSKEY_FACTOR_NAME, {
          exact: true,
        }),
      ).toBeVisible()

      await otherAccounts.signOut()
      await otherAccounts.openSignIn()
      await choosePasskeySignIn(otherPage)
      await expect(homeHeading(otherPage)).toBeVisible()
      await expect(factorStepTitle(otherPage)).toHaveCount(0)
      await expect(signInHeading(otherPage)).toHaveCount(0)
    })
  } finally {
    await otherAuthenticator?.close().catch(() => undefined)
    await otherContext?.close().catch(() => undefined)
    await authenticator?.close().catch(() => undefined)
  }
})
