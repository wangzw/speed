// @journey journey-account-security-hardening.md
// @covers REQ-account-007, REQ-account-023, REQ-account-026, REQ-security-001, REQ-security-003, REQ-security-008, REQ-security-013, REQ-security-014, REQ-security-022, REQ-security-023, REQ-security-025, REQ-security-029, REQ-security-030, REQ-signin-006
import type { BrowserContext, Locator, Page } from '@playwright/test'

import { createAccounts, type Accounts } from '../helpers/accounts'
import {
  expandAccountRows,
  openAccountSection,
  selectAccountSection,
} from '../helpers/account-sections'
import { expect, test } from '../fixtures/demo'
import { scaledTimeout } from '../fixtures/time-scale'
import { copy, copyPattern } from '../helpers/i18n'
import { fillFactorName } from '../helpers/mfa'
import { totpCode } from '../helpers/totp'
import {
  addVirtualAuthenticator,
  type VirtualAuthenticator,
} from '../helpers/virtual-authenticator'

test.use({ recentWindow: '1m', viewport: { width: 1440, height: 1200 } })

// Fixture assembly and the freshness-window wait exceed the tier's default; keep this bound above it.
test.describe.configure({ timeout: scaledTimeout(300_000) })

const RECENT_WINDOW_MS = 60_000

const WINDOW_MARGIN_MS = 3_000

const NEW_PASSWORD = 'e2e-Password-456'

const WRONG_CURRENT_PASSWORD = 'e2e-Wrong-000'

const PASSKEY_FACTOR_NAME = 'Backup passkey'


function homeHeading(page: Page): Locator {
  return page.getByRole('heading', {
    level: 1,
    name: copy('demo', 'home.heading'),
    exact: true,
  })
}

function paneHeading(
  page: Page,
  section: 'profile' | 'security' | 'sessions' | 'preferences',
): Locator {
  return page.getByRole('heading', {
    level: 1,
    name: copy('demo', `account.sections.${section}`),
    exact: true,
  })
}

function section(page: Page, title: string): Locator {
  return page.getByRole('region', { name: title, exact: true })
}

function identifiersSection(page: Page): Locator {
  return section(page, copy('demo', 'account.rows.label.loginWays'))
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

function replaceFactorAction(page: Page): Locator {
  return mfaGroup(page, 'totp').getByRole('button', {
    name: copy('accountUi', 'mfa.replaceAria'),
    exact: true,
  })
}

function passwordSection(page: Page): Locator {
  return section(page, copy('demo', 'account.rows.label.password'))
}

function sessionsSection(page: Page): Locator {
  return section(page, copy('demo', 'account.blocks.devices'))
}

function stepUpDialog(page: Page): Locator {
  return page.getByRole('dialog', {
    name: copy('accountUi', 'mfa.stepUp.title'),
    exact: true,
  })
}

function sessionEndedHeading(page: Page): Locator {
  return page.getByRole('heading', {
    level: 1,
    name: copy('authUi', 'sessionEnded.title'),
    exact: true,
  })
}

async function expectSessionEnded(page: Page): Promise<void> {
  const ended = sessionEndedHeading(page)
  await expect(async () => {
    if (await ended.isVisible()) {
      return
    }
    const trigger = page.getByTestId('account-entry-trigger')
    if ((await trigger.count()) > 0) {
      await trigger.click({ timeout: scaledTimeout(1_000) }).catch(() => undefined)
      await page
        .getByRole('menuitem', {
          name: copy('demo', 'account.sections.profile'),
          exact: true,
        })
        .click({ timeout: scaledTimeout(1_000) })
        .catch(() => undefined)
    } else {
      await page.reload().catch(() => undefined)
    }
    if (!(await ended.isVisible())) {
      throw new Error(
        'the ended device still shows an authenticated surface after a request',
      )
    }
  }).toPass({ timeout: scaledTimeout(15_000), intervals: [500] })
  await expect(ended).toBeVisible()
  await expect(
    page.getByRole('button', {
      name: copy('authUi', 'sessionEnded.signInAction'),
      exact: true,
    }),
  ).toBeVisible()
  await expect(homeHeading(page)).toHaveCount(0)
}

function identifierField(page: Page): Locator {
  return page.getByRole('textbox', {
    name: copy('authUi', 'passwordSignIn.identifierLabel'),
    exact: true,
  })
}
function passwordField(page: Page): Locator {
  return page.getByRole('textbox', {
    name: copy('authUi', 'passwordSignIn.passwordLabel'),
    exact: true,
  })
}
function signInButton(page: Page): Locator {
  return page.getByRole('button', {
    name: copy('authUi', 'passwordSignIn.submit'),
    exact: true,
  })
}

// TOTP codes are single use per thirty-second window, and a window is shorter than some of these steps; track the window last used and wait for the next before presenting another.
let lastCodeStep = -1

// The tracker belongs to one test's run; a repeat must not inherit an earlier window.
test.beforeEach(() => {
  lastCodeStep = -1
})

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

async function waitUntil(instant: number): Promise<void> {
  const wait = instant - Date.now()
  if (wait > 0) {
    await new Promise<void>((resolve) => setTimeout(resolve, wait))
  }
}

async function openSignIn(page: Page, url: string): Promise<void> {
  await page.goto(`${url}/`)
  await expect(
    page.getByRole('heading', {
      name: copy('demo', 'signIn.heading'),
      exact: true,
    }),
  ).toBeVisible()
}

test('account-security-hardening: an account enrols two factors, ends another device and changes its password through a step-up', async ({
  demo,
  browser,
}) => {

  const { page, server } = demo
  const accounts: Accounts = demo.accounts
  const account = accounts.unique('security')
  let secret = ''
  let deviceBContext: BrowserContext | undefined
  let oldPasswordContext: BrowserContext | undefined
  let deviceBPage: Page | undefined
  let authenticator: VirtualAuthenticator | undefined
  let step6AuthenticatedAt = 0

  try {
    await test.step('precondition: an ordinary account is registered and its email address is proved', async () => {
      await accounts.registerAndVerify(account)
      await expect(paneHeading(page, 'profile')).toBeVisible()
      await expect(
        page.getByText(
          copy('accountUi', 'verification.verifiedDescription', {
            email: account.email,
          }),
          { exact: true },
        ),
      ).toBeVisible()
      authenticator = await addVirtualAuthenticator(page.context(), page)
    })

    await test.step('step 1: the account page lists the identifiers with the email proved, and the empty two-step surface offers the enrolment entries', async () => {
      await selectAccountSection(page, 'security')
      await expandAccountRows(page)
      const identifiers = identifiersSection(page)
      await expect(
        identifiers.getByRole('heading', {
          name: copy('accountUi', 'identifiers.kind.username'),
          exact: true,
        }),
      ).toBeVisible()
      // The address contains the username, so only the exact sentence tells the username usage apart.
      await expect(
        identifiers.getByText(
          copy('accountUi', 'identifiers.usage.loginAs', {
            kind: copy('accountUi', 'identifiers.kind.username'),
            value: account.username,
          }),
          { exact: true },
        ),
      ).toBeVisible()
      await expect(
        identifiers.getByText(
          copy('accountUi', 'identifiers.usernamePermanent'),
          { exact: true },
        ),
      ).toBeVisible()
      await expect(
        identifiers
          .getByRole('group', {
            name: copy('accountUi', 'identifiers.kind.username'),
            exact: true,
          })
          .getByRole('button'),
      ).toHaveCount(0)
      await expect(
        identifiers.getByRole('combobox', {
          name: copy('accountUi', 'identifiers.kind.email'),
          exact: true,
        }),
      ).toContainText(account.email)
      await expect(
        identifiers.getByText(
          copy('accountUi', 'identifiers.usage.phoneNone'),
          { exact: true },
        ),
      ).toBeVisible()

      const servedMethods = ['totp', 'webauthn', 'sms'] as const
      for (const kind of servedMethods) {
        const group = mfaGroup(page, kind)
        await expect(
          group.getByRole('heading', {
            level: 3,
            name: copy('accountUi', `mfa.factor.kind.${kind}`),
            exact: true,
          }),
        ).toBeVisible()
        const add = addFactorAction(page, kind)
        await expect(add).toBeVisible()
        await expect(add).toHaveText(copy('accountUi', 'mfa.action.add'))
        await expect(group.getByRole('listitem')).toHaveCount(0)
      }
    })

    await test.step('step 2: the authenticator app is enrolled with a current code and becomes active', async () => {
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
      secret = await revealedTotpSecret(mfa)

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
      const totpGroup = mfaGroup(page, 'totp')
      await expect(totpGroup.getByRole('listitem')).toHaveCount(1)
      // The group's heading carries the same words, so scope the assertion to the row.
      await expect(
        totpGroup
          .getByRole('listitem')
          .getByText(copy('accountUi', 'mfa.factor.kind.totp'), { exact: true }),
      ).toBeVisible()
      await expect(replaceFactorAction(page)).toHaveText(
        copy('accountUi', 'mfa.action.replace'),
      )
    })

    await test.step('step 3: the account signs out of device A and returns to the sign-in surface', async () => {
      await accounts.signOut()
      await expect(sessionEndedHeading(page)).toBeVisible()
    })

    await test.step('step 4: a password sign-in stops at the second factor, and the authenticator app completes it', async () => {
      await accounts.signIn({
        identifier: account.username,
        password: account.password,
      })
      await expect(
        page.getByText(copy('authUi', 'signInFlow.factorTitle'), {
          exact: true,
        }),
      ).toBeVisible()
      await expect(homeHeading(page)).toHaveCount(0)
      await completeTotpFactor(page, secret)
    })

    await test.step('step 4b: cancelling an enrollment wizard leaves the factor list as it was', async () => {
      await openAccountSection(page, 'security')
      await expandAccountRows(page)
      const mfa = mfaSection(page)
      await expect(
        mfa
          .getByRole('listitem')
          .getByText(copy('accountUi', 'mfa.factor.kind.totp'), { exact: true }),
      ).toBeVisible()
      const rowsBefore = await mfa.getByRole('listitem').allInnerTexts()
      expect(rowsBefore).toHaveLength(1)
      expect(rowsBefore.join('\n')).toContain(
        copy('accountUi', 'mfa.factor.kind.totp'),
      )
      await expect(
        mfa.getByText(copy('accountUi', 'mfa.factor.status.pending'), {
          exact: true,
        }),
      ).toHaveCount(0)

      await replaceFactorAction(page).click()
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

      const released = page.waitForResponse(
        (response) =>
          response.request().method() === 'DELETE' &&
          new URL(response.url()).pathname === '/api/v1/auth/flows/current',
      )
      await mfa
        .getByRole('button', {
          name: copy('accountUi', 'mfa.enroll.cancelLabel'),
          exact: true,
        })
        .click()
      expect((await released).ok()).toBe(true)

      await expect(replaceFactorAction(page)).toBeVisible()
      await openAccountSection(page, 'security')
      await expandAccountRows(page)
      const reread = mfaSection(page)
      await expect(reread.getByRole('listitem')).toHaveCount(rowsBefore.length)
      expect(await reread.getByRole('listitem').allInnerTexts()).toEqual(
        rowsBefore,
      )
      await expect(
        reread
          .getByRole('listitem')
          .getByText(copy('accountUi', 'mfa.factor.kind.totp'), { exact: true }),
      ).toHaveCount(1)
      await expect(
        reread.getByText(copy('accountUi', 'mfa.factor.status.pending'), {
          exact: true,
        }),
      ).toHaveCount(0)
    })

    await test.step('step 5: a passkey is added after the waiting state, its cancel and its retry', async () => {
      await openAccountSection(page, 'security')
      await expandAccountRows(page)
      const mfa = mfaSection(page)

      await authenticator?.setPresent(false)
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
        mfa.getByText(copy('accountUi', 'flowStep.passkeyWaiting'), {
          exact: true,
        }),
      ).toBeVisible()
      const ceremony = mfa
        .getByText(copy('accountUi', 'flowStep.passkeyRegisterPrompt'), {
          exact: true,
        })
        .locator('xpath=..')
      const ceremonyCancel = ceremony.getByRole('button', {
        name: copy('accountUi', 'flowStep.passkeyCancel'),
        exact: true,
      })
      await expect(ceremonyCancel).toBeVisible()

      await ceremonyCancel.click()
      await expect(
        mfa.getByText(copy('accountUi', 'flowStep.passkeyCancelled'), {
          exact: true,
        }),
      ).toBeVisible()
      await authenticator?.setPresent(true)
      await mfa
        .getByRole('button', {
          name: copy('accountUi', 'flowStep.passkeyRetry'),
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
      const passkeyGroup = mfaGroup(page, 'webauthn')
      await expect(passkeyGroup.getByRole('listitem')).toHaveCount(1)
      await expect(
        passkeyGroup.getByText(PASSKEY_FACTOR_NAME, { exact: true }),
      ).toBeVisible()
      await expect(addFactorAction(page, 'webauthn')).toHaveText(
        copy('accountUi', 'mfa.action.add'),
      )
    })

    await test.step('step 6: device A signs in again with its password and the authenticator app', async () => {
      await accounts.signOut()
      await accounts.signIn({
        identifier: account.username,
        password: account.password,
      })
      await completeTotpFactor(page, secret)
      step6AuthenticatedAt = Date.now()
    })

    await test.step('step 6b: a wrong current password is refused and leaves the password unchanged', async () => {
      await openAccountSection(page, 'security')
      await expandAccountRows(page)
      const password = passwordSection(page)
      await password
        .getByRole('textbox', {
          name: copy('accountUi', 'password.current.label'),
          exact: true,
        })
        .fill(WRONG_CURRENT_PASSWORD)
      await password
        .getByRole('textbox', {
          name: copy('accountUi', 'password.new.label'),
          exact: true,
        })
        .fill(NEW_PASSWORD)
      await password
        .getByRole('textbox', {
          name: copy('accountUi', 'password.confirm.label'),
          exact: true,
        })
        .fill(NEW_PASSWORD)
      await password
        .getByRole('button', {
          name: copy('accountUi', 'password.submit'),
          exact: true,
        })
        .click()

      await expect(
        password.getByText(copy('accountUi', 'password.currentRejected'), {
          exact: true,
        }),
      ).toBeVisible()
      await expect(
        page.getByText(copy('accountUi', 'password.changed'), {
          exact: true,
        }),
      ).toHaveCount(0)
    })

    await test.step('step 7: device B signs in with the same account and both devices stay signed in', async () => {
      const options = {
        locale: 'en-US',
        timezoneId: 'UTC',
        viewport: page.viewportSize(),
      }
      deviceBContext = await browser.newContext(options)
      deviceBPage = await deviceBContext.newPage()
      const deviceB = createAccounts({ page: deviceBPage, server })
      await deviceB.signIn({
        identifier: account.username,
        password: account.password,
      })
      await completeTotpFactor(deviceBPage, secret)
      await expect(paneHeading(page, 'security')).toBeVisible()
    })

    await test.step('step 8: the session list marks the current device and shows both sessions', async () => {
      await openAccountSection(page, 'sessions')
      const sessions = sessionsSection(page)
      await expect(sessions.getByRole('listitem')).toHaveCount(2)
      await expect(sessions.getByRole('table')).toHaveCount(0)
      await expect(sessions.getByRole('columnheader')).toHaveCount(0)
      const activityPrefix = copy('accountUi', 'sessions.rowActivity', {
        time: '',
      })
      await expect(
        sessions.getByText(activityPrefix, { exact: false }),
      ).toHaveCount(2)

      await expect(
        sessions.getByRole('button', {
          name: copyPattern('accountUi', 'sessions.revokeAriaWithDevice', {
            device: '.+',
          }),
        }),
      ).toHaveCount(1)
      await expect(
        sessions.getByText(copy('accountUi', 'sessions.current'), {
          exact: true,
        }),
      ).toHaveCount(1)
    })

    await test.step('step 9: the password change is refused for freshness and the step-up replays it after the passkey', async () => {
      await waitUntil(
        step6AuthenticatedAt + RECENT_WINDOW_MS + WINDOW_MARGIN_MS,
      )

      await selectAccountSection(page, 'security')
      await expandAccountRows(page)
      const password = passwordSection(page)
      await password
        .getByRole('textbox', {
          name: copy('accountUi', 'password.current.label'),
          exact: true,
        })
        .fill(account.password)
      await password
        .getByRole('textbox', {
          name: copy('accountUi', 'password.new.label'),
          exact: true,
        })
        .fill(NEW_PASSWORD)
      await password
        .getByRole('textbox', {
          name: copy('accountUi', 'password.confirm.label'),
          exact: true,
        })
        .fill(NEW_PASSWORD)
      await password
        .getByRole('button', {
          name: copy('accountUi', 'password.submit'),
          exact: true,
        })
        .click()

      const dialog = stepUpDialog(page)
      await expect(dialog).toBeVisible()
      await expect(
        dialog.getByText(copy('accountUi', 'mfa.stepUp.description'), {
          exact: true,
        }),
      ).toBeVisible()
      await expect(
        dialog.getByRole('button', {
          name: copy('accountUi', 'mfa.factor.kind.webauthn'),
          exact: true,
        }),
      ).toHaveCount(1)
      await expect(
        dialog.getByRole('button', {
          name: copy('accountUi', 'mfa.factor.kind.totp'),
          exact: true,
        }),
      ).toHaveCount(0)

      await dialog
        .getByRole('button', {
          name: copy('accountUi', 'mfa.factor.kind.webauthn'),
          exact: true,
        })
        .click()

      await expect(
        dialog.getByText(copy('accountUi', 'flowStep.passkeyPrompt'), {
          exact: true,
        }),
      ).toHaveCount(0)
      await expect(dialog).toHaveCount(0)

      await expect(
        page.getByText(copy('accountUi', 'password.changed'), {
          exact: true,
        }),
      ).toBeVisible()
      await expect(
        page.getByText(copy('accountUi', 'password.currentRejected'), {
          exact: true,
        }),
      ).toHaveCount(0)
      await expect(paneHeading(page, 'security')).toBeVisible()
      await selectAccountSection(page, 'sessions')
      await expect(sessionsSection(page).getByRole('listitem')).toHaveCount(1)
    })

    await test.step("step 10: the password change ends device B's session while device A stays signed in", async () => {
      if (deviceBPage === undefined) {
        throw new Error('device B was never opened')
      }
      await expectSessionEnded(deviceBPage)
      await expect(paneHeading(page, 'sessions')).toBeVisible()
      await expect(sessionsSection(page).getByRole('listitem')).toHaveCount(1)
    })

    await test.step('step 11: device B signs in with the new password and device A revokes that session', async () => {
      if (deviceBPage === undefined) {
        throw new Error('device B was never opened')
      }
      const deviceB = createAccounts({ page: deviceBPage, server })
      await deviceB.signIn({
        identifier: account.username,
        password: NEW_PASSWORD,
      })
      await completeTotpFactor(deviceBPage, secret)

      await openAccountSection(page, 'sessions')
      const sessions = sessionsSection(page)
      await expect(sessions.getByRole('listitem')).toHaveCount(2)
      const revoke = sessions.getByRole('button', {
        name: copyPattern('accountUi', 'sessions.revokeAriaWithDevice', {
          device: '.+',
        }),
      })
      await expect(revoke).toHaveCount(1)
      await expect(
        sessions.getByText(copy('accountUi', 'sessions.current'), {
          exact: true,
        }),
      ).toHaveCount(1)
      await revoke.click()
      await expect(sessions.getByRole('listitem')).toHaveCount(1)
      await expect(
        sessions.getByText(
          copyPattern('accountUi', 'sessions.revokedOne', { device: '.+' }),
        ),
      ).toBeVisible()
      await expect(
        sessions.getByText(copy('accountUi', 'sessions.current'), {
          exact: true,
        }),
      ).toHaveCount(1)

      await expectSessionEnded(deviceBPage)
    })

    await test.step('result: the old password is refused', async () => {
      const options = {
        locale: 'en-US',
        timezoneId: 'UTC',
        viewport: page.viewportSize(),
      }
      oldPasswordContext = await browser.newContext(options)
      const oldPasswordPage = await oldPasswordContext.newPage()
      await openSignIn(oldPasswordPage, server.url)
      await identifierField(oldPasswordPage).fill(account.username)
      await passwordField(oldPasswordPage).fill(account.password)
      await signInButton(oldPasswordPage).click()
      await expect(
        oldPasswordPage.getByText(copy('authUi', 'passwordSignIn.loginFailed'), {
          exact: true,
        }),
      ).toBeVisible()
    })
  } finally {
    await authenticator?.close().catch(() => undefined)
    await oldPasswordContext?.close().catch(() => undefined)
    await deviceBContext?.close().catch(() => undefined)
  }
})
