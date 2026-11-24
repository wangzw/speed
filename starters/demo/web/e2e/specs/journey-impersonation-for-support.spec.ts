// @journey journey-impersonation-for-support.md
// @covers REQ-platform-admin-001, REQ-platform-admin-003, REQ-platform-admin-021, REQ-platform-admin-022, REQ-platform-admin-023, REQ-platform-admin-024, REQ-platform-admin-027, REQ-platform-admin-029, REQ-platform-admin-078, REQ-platform-admin-081, REQ-platform-admin-082, REQ-platform-admin-084, REQ-platform-admin-109, REQ-platform-admin-110, REQ-security-014, REQ-security-015, REQ-tenancy-024, REQ-tenancy-038
import type { Locator, Page } from '@playwright/test'

import { e2eImpersonationFactors } from '../fixtures/config'
import { expect, test } from '../fixtures/demo'
import { waitForVerificationCode } from '../fixtures/log'
import type { E2EServer } from '../fixtures/server'
import { scaledTimeout } from '../fixtures/time-scale'
import {
  expandAccountRows,
  selectAccountSection,
} from '../helpers/account-sections'
import {
  adminEntry,
  closeAccountMenu,
  openAccountMenu,
  organizationRow,
} from '../helpers/account-menu'
import { copy, copyPattern } from '../helpers/i18n'
import { chooseSelectOption } from '../helpers/select-option'
import { totpCode } from '../helpers/totp'

// The admin tables render their low-priority columns only at desktop widths; keep the viewport wide enough for every column this spec reads.
test.use({ seedImpersonation: true, viewport: { width: 1920, height: 1080 } })

const OPERATOR = {
  identifier: 'operator@demo.test',
  username: 'operator',
  password: 'operator-dev-secret',
  displayName: 'Demo Operator',
} as const

const TARGET = { displayName: 'Demo Member', username: 'member' } as const

const TENANT = 'acme'

const OPERATOR_TENANT = 'system'

const OPERATOR_PHONE = '+8613900139000'

const OPERATOR_PHONE_COUNTRY = '86'
const OPERATOR_PHONE_NATIONAL = '13900139000'

const TOTP_SEED = e2eImpersonationFactors.totpSeed

const REASON = 'Support ticket 4711: reproduce the reported inbox problem.'

const OVERLONG_REASON = 'x'.repeat(501)

const ACTION_START = 'authn.impersonation.start'
const ACTION_END = 'authn.impersonation.end'

function homeHeading(page: Page): Locator {
  return page.getByRole('heading', {
    level: 1,
    name: copy('demo', 'home.heading'),
    exact: true,
  })
}

async function expectAccountMenuName(
  trigger: Locator,
  identity: string,
): Promise<void> {
  await expect(trigger).toHaveAccessibleName(new RegExp(escapeRegExp(identity)))
}

function adminCard(page: Page): Locator {
  return page.getByRole('heading', {
    level: 2,
    name: copy('demo', 'home.cards.admin.title'),
    exact: true,
  })
}

function adminNav(page: Page): Locator {
  return page.getByRole('navigation', {
    name: copy('adminUi', 'shell.navLabel'),
    exact: true,
  })
}

function banner(page: Page): Locator {
  return page.getByRole('region', {
    name: copy('authUi', 'impersonation.regionLabel'),
    exact: true,
  })
}

function preferencesMatrix(page: Page): Locator {
  return page.getByRole('table', {
    name: copy('notificationUi', 'preferences.title'),
    exact: true,
  })
}

function stepUpDialog(page: Page): Locator {
  return page.getByRole('dialog', {
    name: copy('adminUi', 'stepUp.title'),
    exact: true,
  })
}

function tenantSelector(page: Page): Locator {
  return page.getByRole('combobox', {
    name: copy('adminUi', 'common.tenant'),
    exact: true,
  })
}

function targetField(page: Page): Locator {
  return page.getByRole('combobox', {
    name: copy('adminUi', 'impersonations.start.accountLabel'),
    exact: true,
  })
}
function reasonField(page: Page): Locator {
  return page.getByRole('textbox', {
    name: copy('adminUi', 'impersonations.start.reasonLabel'),
    exact: true,
  })
}
function ttlField(page: Page): Locator {
  return page.getByRole('textbox', {
    name: copy('adminUi', 'impersonations.start.ttlLabel'),
    exact: true,
  })
}
function startButton(page: Page): Locator {
  return page.getByRole('button', {
    name: copy('adminUi', 'impersonations.start.submit'),
    exact: true,
  })
}

function targetLabel(): string {
  return `${TARGET.displayName} (${TARGET.username})`
}

async function chooseTarget(page: Page): Promise<void> {
  const field = targetField(page)
  await field.click()
  await field.fill(TARGET.username)
  await page
    .getByRole('option', { name: new RegExp(escapeRegExp(TARGET.displayName)) })
    .click()
}

async function accountIdFromRoster(
  page: Page,
  displayName: string,
): Promise<string> {
  const response = await page.waitForResponse(
    (candidate) =>
      candidate.request().method() === 'GET' &&
      new URL(candidate.url()).pathname === '/api/v1/system/auth/accounts',
  )
  expect(response.ok()).toBe(true)
  const body = (await response.json()) as {
    readonly accounts: readonly {
      readonly display_name: string
      readonly id: string
    }[]
  }
  const row = body.accounts.find((account) => account.display_name === displayName)
  if (row === undefined) {
    throw new Error(`the account roster carries no row named ${displayName}`)
  }
  return row.id
}

function escapeRegExp(text: string): string {
  return text.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}

function emailSection(page: Page): Locator {
  return page.getByRole('region', {
    name: copy('demo', 'account.rows.label.email'),
    exact: true,
  })
}

function phoneSection(page: Page): Locator {
  return page.getByRole('region', {
    name: copy('demo', 'account.rows.label.phone'),
    exact: true,
  })
}

async function proveOperatorIdentifiers(
  page: Page,
  server: E2EServer,
): Promise<void> {
  await selectAccountSection(page, 'profile')
  await expandAccountRows(page)

  const phone = phoneSection(page)
  await phone
    .getByRole('button', {
      name: copy('demo', 'account.action.add'),
      exact: true,
    })
    .click()
  await phone
    .getByRole('combobox', {
      name: copy('accountUi', 'phone.country.label'),
      exact: true,
    })
    .fill(OPERATOR_PHONE_COUNTRY)
  await page.getByRole('option', { name: /\+86$/ }).click()
  await phone
    .getByRole('textbox', {
      name: copy('accountUi', 'phone.phone.label'),
      exact: true,
    })
    .fill(OPERATOR_PHONE_NATIONAL)
  const phoneAfter = server.log.mark()
  await phone
    .getByRole('button', {
      name: copy('accountUi', 'phone.send'),
      exact: true,
    })
    .click()
  const phoneCode = await waitForVerificationCode(
    server.log,
    'sms',
    OPERATOR_PHONE,
    { after: phoneAfter },
  )
  await phone
    .getByRole('textbox', {
      name: copy('accountUi', 'phone.code.label'),
      exact: true,
    })
    .fill(phoneCode.code)
  await phone
    .getByRole('button', {
      name: copy('accountUi', 'phone.verify'),
      exact: true,
    })
    .click()
  await expect(
    page.getByText(copy('accountUi', 'phone.added', { phone: OPERATOR_PHONE }), {
      exact: true,
    }),
  ).toBeVisible()

  const email = emailSection(page)
  const emailAfter = server.log.mark()
  await email
    .getByRole('button', {
      name: copy('accountUi', 'verification.send'),
      exact: true,
    })
    .click()
  const emailCode = await waitForVerificationCode(
    server.log,
    'mail',
    OPERATOR.identifier,
    { after: emailAfter },
  )
  await email
    .getByRole('textbox', {
      name: copy('accountUi', 'verification.codeLabel'),
      exact: true,
    })
    .fill(emailCode.code)
  await email
    .getByRole('button', {
      name: copy('accountUi', 'verification.verify'),
      exact: true,
    })
    .click()
  await expect(
    email.getByText(copy('accountUi', 'identifiers.verified'), {
      exact: true,
    }),
  ).toBeVisible()
}

function term(page: Page, label: string): Locator {
  return page.locator('dt', {
    hasText: new RegExp(`^${escapeRegExp(label)}$`),
  })
}

function fieldValue(page: Page, label: string): Locator {
  return term(page, label).locator('xpath=following-sibling::dd[1]')
}

test('impersonation-for-support: the operator acts as a member and the record and audit trail survive the end', async ({
  demo,
}) => {
  test.setTimeout(scaledTimeout(180_000))

  const { page, server, accounts } = demo
  let operatorAccountId = ''

  await test.step('precondition: the operator signs in with the password and the authenticator code', async () => {
    await accounts.signIn({
      identifier: OPERATOR.username,
      password: OPERATOR.password,
    })
    const passwordPanel = page.getByRole('tabpanel', {
      name: copy('authUi', 'passwordSignIn.title'),
      exact: true,
    })
    await passwordPanel
      .getByRole('button', {
        name: copy('authUi', 'signInFlow.factorKind.totp'),
        exact: true,
      })
      .click()
    const codeField = passwordPanel.getByRole('textbox', {
      name: copy('authUi', 'smsSignIn.codeLabel'),
      exact: true,
    })
    await expect(codeField).toBeVisible()
    await codeField.fill(totpCode(TOTP_SEED))
    await passwordPanel
      .getByRole('button', {
        name: copy('authUi', 'signInFlow.submit'),
        exact: true,
      })
      .click()
    await expect(homeHeading(page)).toBeVisible()

    await proveOperatorIdentifiers(page, server)
    await page.goto(`${server.url}/`)
    await expect(homeHeading(page)).toBeVisible()

    await expect(adminCard(page)).toBeVisible()
    await openAccountMenu(page)
    await expect(adminEntry(page)).toBeVisible()
    await closeAccountMenu(page)
  })

  await test.step('step 1: the operator opens the impersonation page and it reports no impersonation', async () => {
    await openAccountMenu(page)
    await adminEntry(page).click()
    await expect(adminNav(page)).toBeVisible()
    await adminNav(page)
      .getByRole('link', {
        name: copy('adminUi', 'impersonations.title'),
        exact: true,
      })
      .click()
    await expect(
      page.getByRole('heading', {
        level: 1,
        name: copy('adminUi', 'impersonations.title'),
        exact: true,
      }),
    ).toBeVisible()

    await expect(
      page.getByText(copy('adminUi', 'impersonations.current.none'), {
        exact: true,
      }),
    ).toBeVisible()
    await expect(
      page.getByText(copy('adminUi', 'impersonations.current.error.title'), {
        exact: true,
      }),
    ).toHaveCount(0)
    await expect(
      page.getByRole('button', {
        name: copy('adminUi', 'impersonations.retry'),
        exact: true,
      }),
    ).toHaveCount(0)

    await expect(
      page.getByText(copy('adminUi', 'impersonations.start.precondition'), {
        exact: true,
      }),
    ).toBeVisible()
    await expect(
      page.getByText(copy('adminUi', 'impersonations.start.tenantHint'), {
        exact: true,
      }),
    ).toBeVisible()
    await expect(startButton(page)).toBeDisabled()
  })

  await test.step('step 2: the target organization and account are chosen, and the start is submitted', async () => {
    const operatorIdRead = accountIdFromRoster(page, OPERATOR.displayName)
    const targetIdRead = accountIdFromRoster(page, TARGET.displayName)
    await adminNav(page)
      .getByRole('link', {
        name: copy('adminUi', 'accounts.title'),
        exact: true,
      })
      .click()
    await expect(
      page.getByRole('heading', {
        level: 1,
        name: copy('adminUi', 'accounts.title'),
        exact: true,
      }),
    ).toBeVisible()
    operatorAccountId = await operatorIdRead
    expect(await targetIdRead).not.toBe(operatorAccountId)

    await adminNav(page)
      .getByRole('link', {
        name: copy('adminUi', 'impersonations.title'),
        exact: true,
      })
      .click()
    await expect(
      page.getByRole('heading', {
        level: 1,
        name: copy('adminUi', 'impersonations.title'),
        exact: true,
      }),
    ).toBeVisible()

    await chooseSelectOption(
      tenantSelector(page),
      page.getByRole('option', { name: TENANT, exact: true }),
    )
    await chooseTarget(page)
    await expect(startButton(page)).toBeEnabled()

    await expect(ttlField(page)).toHaveValue('')

    await reasonField(page).fill(OVERLONG_REASON)
    await startButton(page).click()

    const restricted = page.getByText(
      copy('adminUi', 'errors.authn.impersonation_restricted'),
      { exact: true },
    )
    const dialog = stepUpDialog(page)

    const submitProbe = async (): Promise<void> => {
      await chooseTarget(page)
      await reasonField(page).fill(OVERLONG_REASON)
      await expect(startButton(page)).toBeEnabled()
      await startButton(page).click()
    }

    // Wait for whichever answer the server gives first: the in-window refusal or the step-up dialog (the same rule after the window elapsed). Both are caught, so the race settles as soon as either appears.
    await Promise.race([
      restricted
        .waitFor({ state: 'visible', timeout: scaledTimeout(20_000) })
        .catch(() => undefined),
      dialog
        .waitFor({ state: 'visible', timeout: scaledTimeout(20_000) })
        .catch(() => undefined),
    ])

    if (!(await dialog.isVisible()) && !(await restricted.isVisible())) {
      await expect(async () => {
        if (await dialog.isVisible()) return
        if (await restricted.isVisible()) return
        await submitProbe()
        throw new Error('the start has answered neither outcome yet')
      }).toPass({ timeout: scaledTimeout(120_000), intervals: [2_000] })
    }

    if (await restricted.isVisible()) {
      await expect(restricted).toBeVisible()
      await expect(async () => {
        if (await dialog.isVisible()) return
        await submitProbe()
        throw new Error('the step-up dialog has not opened yet')
      }).toPass({ timeout: scaledTimeout(120_000), intervals: [2_000] })
    } else {
      test.info().annotations.push({
        type: 'freshness-window',
        description:
          'the impersonation max-auth-age window had already elapsed before ' +
          'the first start; the in-window refusal was not asserted',
      })
    }

    await dialog
      .getByRole('button', {
        name: copy('adminUi', 'common.cancel'),
        exact: true,
      })
      .click()
    await expect(dialog).toHaveCount(0)
  })

  await test.step('step 3: the start is refused for freshness and the step-up replays it after SMS', async () => {
    const dialog = stepUpDialog(page)
    await expect(async () => {
      if (await dialog.isVisible()) {
        return
      }
      await chooseTarget(page)
      await reasonField(page).fill(REASON)
      await expect(startButton(page)).toBeEnabled()
      await startButton(page).click({ timeout: scaledTimeout(3_000) })
      await expect(dialog).toBeVisible({ timeout: scaledTimeout(8_000) })
    }).toPass({ timeout: scaledTimeout(30_000), intervals: [1_000] })
    const beforeSmsCode = server.log.mark()
    await dialog
      .getByRole('button', {
        name: copy('adminUi', 'stepUp.factorKind.sms'),
        exact: true,
      })
      .click()

    const codeField = dialog.getByRole('textbox', {
      name: 'code',
      exact: true,
    })
    await expect(codeField).toBeVisible()
    const { code } = await waitForVerificationCode(
      server.log,
      'sms',
      OPERATOR_PHONE,
      { after: beforeSmsCode },
    )
    await codeField.fill(code)
    const verify = dialog.getByRole('button', {
      name: copy('adminUi', 'stepUp.submit'),
      exact: true,
    })
    await verify.click()
    await expect(codeField).toHaveCount(0)
    await expect(dialog).toHaveCount(0)

    await expect(homeHeading(page)).toBeVisible()
    expect(new URL(page.url()).pathname).toBe('/')
  })

  await test.step('step 4: the impersonation lands on the tenant home with its banner and indicators', async () => {
    const notice = banner(page)
    await expect(notice).toBeVisible()

    await expect(notice).toContainText(
      copy('authUi', 'impersonation.actingAs', {
        subject: targetLabel(),
      }),
    )
    await expect(notice).toContainText(
      copy('authUi', 'impersonation.tenant', { tenant: TENANT }),
    )
    await expect(notice).toContainText(
      copy('authUi', 'impersonation.managementUnavailable'),
    )
    await expect(
      notice.getByText(
        copyPattern('authUi', 'impersonation.expiresAt', {
          expiresAt: '.+',
        }),
      ),
    ).toBeVisible()
    await expect(
      notice.getByRole('button', {
        name: copy('authUi', 'impersonation.end'),
        exact: true,
      }),
    ).toBeVisible()

    await expect(
      page.getByText(copy('authUi', 'impersonation.impersonating'), {
        exact: true,
      }),
    ).toBeVisible()
    const menuTrigger = page.getByTestId('account-entry-trigger')
    await expectAccountMenuName(menuTrigger, TARGET.displayName)
    await menuTrigger.click()
    await expect(
      page.getByRole('menuitem', {
        name: copy('authUi', 'impersonation.end'),
        exact: true,
      }),
    ).toBeVisible()

    await expect(adminEntry(page)).toHaveCount(0)
    await expect(organizationRow(page, TENANT)).toHaveCount(0)
    await closeAccountMenu(page, 'sidebar')
    await expect(adminCard(page)).toHaveCount(0)

    await page.goto(
      new URL('/admin/impersonations?tenant=acme', page.url()).toString(),
    )
    await expect(homeHeading(page)).toBeVisible()
    expect(new URL(page.url()).pathname).toBe('/')
    await expect(banner(page)).toBeVisible()
    await expect(adminNav(page)).toHaveCount(0)
  })

  await test.step('step 5: the notification inbox opens under the impersonated identity', async () => {
    await openAccountMenu(page)
    await page
      .getByRole('menu')
      .getByRole('menuitem', {
        name: copy('demo', 'userMenu.messages'),
        exact: true,
      })
      .click()
    await expect(
      page.getByRole('heading', {
        name: copy('notificationUi', 'inbox.title'),
        exact: true,
      }),
    ).toBeVisible()

    await expect(
      page.getByText(copy('notificationUi', 'inbox.empty.title'), {
        exact: true,
      }),
    ).toBeVisible()
    await expect(
      page.getByText(copy('notificationUi', 'inbox.empty.description'), {
        exact: true,
      }),
    ).toBeVisible()
    await expect(
      page.getByText(copy('notificationUi', 'tenantUnavailable.title'), {
        exact: true,
      }),
    ).toHaveCount(0)
    await expect(banner(page)).toBeVisible()
  })

  await test.step('step 5b: a full page load keeps the notification surface inside the grant tenant', async () => {
    await page.reload()
    await expect(
      page.getByRole('heading', {
        name: copy('notificationUi', 'inbox.title'),
        exact: true,
      }),
    ).toBeVisible()

    await expect(preferencesMatrix(page)).toBeVisible()
    await expect(
      page.getByText(copy('notificationUi', 'tenantUnavailable.title'), {
        exact: true,
      }),
    ).toHaveCount(0)
    await expect(
      page.getByText(copy('notificationUi', 'tenantUnavailable.description'), {
        exact: true,
      }),
    ).toHaveCount(0)
    await expect(banner(page)).toBeVisible()
  })

  await test.step("step 6: the banner ends the impersonation and the operator's own identity returns", async () => {
    await banner(page)
      .getByRole('button', {
        name: copy('authUi', 'impersonation.end'),
        exact: true,
      })
      .click()

    await expect(banner(page)).toHaveCount(0)
    await expect(
      page.getByText(copy('authUi', 'impersonation.ended'), { exact: true }),
    ).toBeVisible()

    await expect(
      page.getByText(copy('authUi', 'impersonation.impersonating'), {
        exact: true,
      }),
    ).toHaveCount(0)
    await expectAccountMenuName(
      page.getByTestId('account-entry-trigger'),
      OPERATOR.displayName,
    )
    await openAccountMenu(page)
    await expect(adminEntry(page)).toBeVisible()
    await expect(organizationRow(page, OPERATOR_TENANT)).toBeVisible()
    await closeAccountMenu(page)

    await expect(
      page.getByRole('heading', {
        name: copy('authUi', 'sessionEnded.title'),
        exact: true,
      }),
    ).toHaveCount(0)
  })

  await test.step('step 7: the impersonation record and the audit trail carry the start and the end', async () => {
    await openAccountMenu(page)
    await adminEntry(page).click()
    await expect(adminNav(page)).toBeVisible()
    await adminNav(page)
      .getByRole('link', {
        name: copy('adminUi', 'impersonations.title'),
        exact: true,
      })
      .click()

    await expect(
      page.getByText(copy('adminUi', 'impersonations.current.none'), {
        exact: true,
      }),
    ).toBeVisible()

    await chooseSelectOption(
      tenantSelector(page),
      page.getByRole('option', { name: TENANT, exact: true }),
    )

    const table = page.getByRole('table')
    const rows = table.locator('tbody tr')
    await expect(rows).toHaveCount(1)
    const record = rows.first()
    await expect(
      record.getByText(`${OPERATOR.displayName} (${OPERATOR.username})`, {
        exact: true,
      }),
    ).toBeVisible()
    await expect(
      record.getByText(targetLabel(), { exact: true }),
    ).toBeVisible()
    await expect(record.getByText(TENANT, { exact: true })).toBeVisible()
    await expect(
      record.getByText(
        copy('adminUi', 'impersonations.ended', {
          reason: copy('adminUi', 'impersonations.endReason.explicit'),
        }),
        { exact: true },
      ),
    ).toBeVisible()
    const cells = record.getByRole('cell')
    await expect(cells.nth(3)).toHaveText(/\d/u)
    await expect(cells.nth(4)).toHaveText(/\d/u)
    await expect(
      record.getByRole('button', {
        name: copy('adminUi', 'impersonations.terminate'),
        exact: true,
      }),
    ).toBeDisabled()

    await adminNav(page)
      .getByRole('link', {
        name: copy('adminUi', 'audit.title'),
        exact: true,
      })
      .click()
    await expect(
      page.getByRole('heading', {
        level: 1,
        name: copy('adminUi', 'audit.title'),
        exact: true,
      }),
    ).toBeVisible()

    const auditTable = page.getByRole('table')
    await expect(auditTable.locator('tbody tr').first()).toBeVisible()
    await page
      .getByLabel(copy('adminUi', 'audit.filters.action'), { exact: true })
      .fill(ACTION_END)
    await page
      .getByLabel(copy('adminUi', 'audit.filters.outcome'), { exact: true })
      .fill('success')
    await page
      .getByLabel(copy('adminUi', 'audit.filters.actor'), { exact: true })
      .fill(operatorAccountId)
    expect(await auditTable.locator('tbody tr').count()).toBeGreaterThan(1)
    await page
      .getByRole('button', {
        name: copy('adminUi', 'audit.filters.apply'),
        exact: true,
      })
      .click()
    await expect(auditTable.locator('tbody tr')).toHaveCount(1)

    await auditTable
      .getByRole('link', { name: ACTION_END, exact: true })
      .click()
    await expect(
      page.getByRole('heading', {
        level: 1,
        name: copy('adminUi', 'auditDetail.title'),
        exact: true,
      }),
    ).toBeVisible()

    await expect(
      fieldValue(page, copy('adminUi', 'auditDetail.field.action')),
    ).toHaveText(ACTION_END)
    await expect(
      fieldValue(page, copy('adminUi', 'auditDetail.field.outcome')),
    ).toHaveText('success')
    await expect(
      fieldValue(page, copy('adminUi', 'auditDetail.field.actor')),
    ).toHaveText(`${OPERATOR.displayName} (${OPERATOR.username})`)
    await expect(
      fieldValue(page, copy('adminUi', 'auditDetail.field.object')),
    ).toHaveText(targetLabel())
    await expect(
      fieldValue(page, copy('adminUi', 'auditDetail.field.purpose')),
    ).toHaveText(copy('adminUi', 'common.emptyValue'))

    await expect(term(page, 'grant_id')).toBeVisible()
    await expect(term(page, 'tenant')).toBeVisible()
    await expect(term(page, 'sid')).toBeVisible()
    await expect(fieldValue(page, 'tenant')).toHaveText(TENANT)
    await expect(fieldValue(page, 'sid')).not.toHaveText('')
    const endGrantId =
      (await fieldValue(page, 'grant_id').textContent())?.trim() ?? ''
    expect(endGrantId).not.toBe('')

    await page
      .getByRole('link', {
        name: copy('adminUi', 'auditDetail.back'),
        exact: true,
      })
      .click()
    await expect(
      page.getByRole('heading', {
        level: 1,
        name: copy('adminUi', 'audit.title'),
        exact: true,
      }),
    ).toBeVisible()
    await page
      .getByLabel(copy('adminUi', 'audit.filters.action'), { exact: true })
      .fill(ACTION_START)
    await page
      .getByLabel(copy('adminUi', 'audit.filters.outcome'), { exact: true })
      .fill('granted')
    await page
      .getByLabel(copy('adminUi', 'audit.filters.actor'), { exact: true })
      .fill(operatorAccountId)
    await page
      .getByRole('button', {
        name: copy('adminUi', 'audit.filters.apply'),
        exact: true,
      })
      .click()
    await expect(auditTable.locator('tbody tr')).toHaveCount(1)
    const startRow = auditTable.locator('tbody tr').first()
    await expect(
      startRow.getByText(`${OPERATOR.displayName} (${OPERATOR.username})`, {
        exact: true,
      }),
    ).toBeVisible()
    await expect(
      startRow.getByText(targetLabel(), { exact: true }),
    ).toBeVisible()
    await expect(
      startRow.getByText('granted', { exact: true }),
    ).toBeVisible()

    await auditTable
      .getByRole('link', { name: ACTION_START, exact: true })
      .click()
    await expect(
      page.getByRole('heading', {
        level: 1,
        name: copy('adminUi', 'auditDetail.title'),
        exact: true,
      }),
    ).toBeVisible()
    await expect(
      fieldValue(page, copy('adminUi', 'auditDetail.field.action')),
    ).toHaveText(ACTION_START)
    await expect(
      fieldValue(page, copy('adminUi', 'auditDetail.field.outcome')),
    ).toHaveText('granted')
    await expect(
      fieldValue(page, copy('adminUi', 'auditDetail.field.purpose')),
    ).toHaveText(REASON)
    await expect(fieldValue(page, 'grant_id')).toHaveText(endGrantId)
    await expect(term(page, 'ttl_seconds')).toBeVisible()
    await expect(fieldValue(page, 'sid')).not.toHaveText('')
    await expect(fieldValue(page, 'tenant')).toHaveText(TENANT)
  })
})
