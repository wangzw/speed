// @journey journey-sms-template-rollout.md
// @covers REQ-notifications-023, REQ-platform-config-010, REQ-platform-config-012, REQ-platform-config-013, REQ-platform-config-014, REQ-platform-config-015, REQ-platform-config-034, REQ-platform-config-036, REQ-presentation-027, REQ-presentation-028
import type { Locator, Page } from '@playwright/test'

import { e2eOrdinaryMember, smsRolloutOperator } from '../fixtures/config'
import { expect, test } from '../fixtures/demo'
import { waitForVerificationCode } from '../fixtures/log'
import { NOTIFICATION_INTERVAL_MS } from '../fixtures/notification-fixture'
import type { E2EServer } from '../fixtures/server'
import { scaledTimeout } from '../fixtures/time-scale'
import { createAccounts } from '../helpers/accounts'
import {
  adminEntry,
  closeAccountMenu,
  openAccountMenu,
} from '../helpers/account-menu'
import {
  expandAccountRows,
  selectAccountSection,
} from '../helpers/account-sections'
import { copy } from '../helpers/i18n'
import { expectClippedAnnouncement } from '../helpers/tenant-switch'

const TEMPLATE = 'demo-notice'

const EN_LOCALE = 'en-US'
const ZH_LOCALE = 'zh-CN'
const EN_CODE = 'demo-notice-en'
const ZH_CODE = 'demo-notice-zh'

const TYPE_KEY = 'demo.notice'

const PHONE_NATIONAL = '13800138000'
const PHONE_E164 = '+8613800138000'

const CONFIG_TEMPLATE = 'authn.code.login'

function verificationSection(page: Page): Locator {
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

function adminNav(page: Page): Locator {
  return page.getByRole('navigation', {
    name: copy('adminUi', 'shell.navLabel'),
    exact: true,
  })
}

function smsHeading(page: Page): Locator {
  return page.getByRole('heading', {
    name: copy('adminUi', 'sms.title'),
    exact: true,
  })
}

function registerSection(page: Page): Locator {
  return page.getByRole('region', {
    name: copy('adminUi', 'sms.register.title'),
    exact: true,
  })
}

function refreshButton(page: Page): Locator {
  return page.getByRole('button', {
    name: copy('adminUi', 'common.refresh'),
    exact: true,
  })
}

function templateRow(page: Page, name: string): Locator {
  return page.getByRole('row').filter({ hasText: name })
}

const ADMIN_TENANT = 'system'

function currentOrganizationRow(page: Page): ReturnType<Page['getByRole']> {
  return page
    .getByRole('menu')
    .getByRole('menuitem', { expanded: false })
    .first()
}

async function switchToTenant(page: Page, target: string): Promise<void> {
  const menu = await openAccountMenu(page)
  const current = currentOrganizationRow(page)
  await expect(current).toBeVisible()
  await current.click()
  const others = menu.getByRole('group', {
    name: copy('demo', 'userMenu.organizations'),
    exact: true,
  })
  await expect(others).toBeVisible()
  const row = others.getByRole('menuitem', { name: target, exact: true })
  if ((await row.count()) === 0) {
    await closeAccountMenu(page)
    return
  }
  await row.click()
  await expectClippedAnnouncement(
    page,
    copy('tenancyUi', 'tenantSwitcher.switchedTo', { tenant: target }),
  )
  // The pick closes the menu; wait for it to leave so the next open matches exactly one menu.
  await closeAccountMenu(page)
  await openAccountMenu(page)
  await expect(
    page.getByRole('menu').getByRole('menuitem', { name: target, exact: true }),
  ).toBeVisible()
  await closeAccountMenu(page)
}

async function openSmsPage(page: Page): Promise<void> {
  await switchToTenant(page, ADMIN_TENANT)
  await openAccountMenu(page)
  await expect(adminEntry(page)).toBeVisible()
  await adminEntry(page).click()
  await expect(adminNav(page)).toBeVisible()
  await adminNav(page)
    .getByRole('link', { name: copy('adminUi', 'sms.title'), exact: true })
    .click()
  await expect(smsHeading(page)).toBeVisible()
}

function approvalText(locale: string, stateKey: string): string {
  return `${locale}: ${copy('adminUi', `sms.approval.${stateKey}`)}`
}

async function refreshUntil(
  page: Page,
  name: string,
  wanted: string,
): Promise<void> {
  await expect
    .poll(
      async () => {
        const text = (await templateRow(page, name).textContent()) ?? ''
        if (text.includes(wanted)) return text
        const refresh = refreshButton(page)
        if (await refresh.isEnabled()) await refresh.click()
        return (await templateRow(page, name).textContent()) ?? ''
      },
      {
        message: `the ${name} row never showed ${wanted}`,
      },
    )
    .toContain(wanted)
}

async function proveEmail(
  page: Page,
  server: E2EServer,
  email: string,
): Promise<void> {
  await selectAccountSection(page, 'profile')
  await expandAccountRows(page)
  const verification = verificationSection(page)
  const emailAfter = server.log.mark()
  await verification
    .getByRole('button', {
      name: copy('accountUi', 'verification.send'),
      exact: true,
    })
    .click()
  await expect(
    page.getByText(copy('accountUi', 'verification.sent', { email }), {
      exact: true,
    }),
  ).toBeVisible()
  const emailCode = await waitForVerificationCode(server.log, 'mail', email, {
    after: emailAfter,
  })
  await verification
    .getByRole('textbox', {
      name: copy('accountUi', 'verification.codeLabel'),
      exact: true,
    })
    .fill(emailCode.code)
  await verification
    .getByRole('button', {
      name: copy('accountUi', 'verification.verify'),
      exact: true,
    })
    .click()
  await expect(
    verification.getByText(email, { exact: true }),
  ).toBeVisible()
  await expect(
    verification.getByText(copy('accountUi', 'identifiers.verified'), {
      exact: true,
    }),
  ).toBeVisible()
}

async function provePhone(page: Page, server: E2EServer): Promise<void> {
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
    .fill('86')
  await page.getByRole('option', { name: /\+86$/ }).click()
  await phone
    .getByRole('textbox', {
      name: copy('accountUi', 'phone.phone.label'),
      exact: true,
    })
    .fill(PHONE_NATIONAL)
  const phoneAfter = server.log.mark()
  await phone
    .getByRole('button', {
      name: copy('accountUi', 'phone.send'),
      exact: true,
    })
    .click()
  await expect(
    page.getByText(copy('accountUi', 'phone.sent', { phone: PHONE_E164 }), {
      exact: true,
    }),
  ).toBeVisible()
  const phoneCode = await waitForVerificationCode(server.log, 'sms', PHONE_E164, {
    after: phoneAfter,
  })
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
    page.getByText(copy('accountUi', 'phone.added', { phone: PHONE_E164 }), {
      exact: true,
    }),
  ).toBeVisible()
}

test('sms-template-rollout: an operator settles one language and a disabled template is refused locally', async ({
  demo,
  browser,
}) => {
  const { page, server, accounts } = demo

  await test.step('the operator signs in and proves the account email', async () => {
    await accounts.signIn({
      identifier: smsRolloutOperator.username,
      password: smsRolloutOperator.password,
    })
    await expect(
      page.getByRole('heading', {
        level: 1,
        name: copy('demo', 'home.heading'),
        exact: true,
      }),
    ).toBeVisible()
    await proveEmail(page, server, smsRolloutOperator.identifier)
  })

  await test.step('the configuration template reads as read-only and refuses a write', async () => {
    await openSmsPage(page)

    const configRow = templateRow(page, CONFIG_TEMPLATE)
    await expect(configRow).toHaveCount(1)
    await expect(configRow).toContainText(
      copy('adminUi', 'sms.source.config'),
    )
    for (const state of ['pending', 'approved', 'rejected', 'withdrawn']) {
      await expect(configRow).not.toContainText(
        copy('adminUi', `sms.approval.${state}`),
      )
    }
    await configRow
      .getByRole('switch', {
        name: copy('adminUi', 'sms.disableAria', { name: CONFIG_TEMPLATE }),
        exact: true,
      })
      .click()
    await expect(
      page
        .getByText(copy('adminUi', 'errors.sms.config_template'), {
          exact: true,
        })
        .first(),
    ).toBeVisible()
    await expect(configRow).toContainText(
      copy('adminUi', 'sms.source.config'),
    )
  })

  await test.step('the operator registers the runtime template and its languages read their configured verdicts', async () => {
    const register = registerSection(page)
    await register
      .getByRole('textbox', {
        name: copy('adminUi', 'sms.register.nameLabel'),
        exact: true,
      })
      .fill(TEMPLATE)
    await register
      .getByRole('textbox', {
        name: copy('adminUi', 'sms.register.paramsLabel'),
        exact: true,
      })
      .fill('to')
    await register
      .getByRole('textbox', {
        name: copy('adminUi', 'sms.register.codesLabel'),
        exact: true,
      })
      .fill(`${EN_LOCALE}:${EN_CODE}, ${ZH_LOCALE}:${ZH_CODE}`)
    await register
      .getByRole('button', {
        name: copy('adminUi', 'sms.register.submit'),
        exact: true,
      })
      .click()
    await expect(
      page.getByText(copy('adminUi', 'sms.registered', { name: TEMPLATE }), {
        exact: true,
      }),
    ).toBeVisible()

    await refreshUntil(page, TEMPLATE, approvalText(EN_LOCALE, 'approved'))
    const row = templateRow(page, TEMPLATE)
    await expect(row).toHaveCount(1)
    await expect(row).toContainText(copy('adminUi', 'sms.source.runtime'))
    await expect(row).toContainText(copy('adminUi', 'sms.enabledYes'))
    await expect(row).toContainText(approvalText(EN_LOCALE, 'approved'))
    await expect(row).toContainText(approvalText(ZH_LOCALE, 'pending'))
  })

  await test.step('disabling the template leaves the review states unchanged', async () => {
    const row = templateRow(page, TEMPLATE)
    await row
      .getByRole('switch', {
        name: copy('adminUi', 'sms.disableAria', { name: TEMPLATE }),
        exact: true,
      })
      .click()
    await expect(
      page.getByText(
        copy('adminUi', 'sms.disabledToast', { name: TEMPLATE }),
        { exact: true },
      ),
    ).toBeVisible()
    await refreshUntil(page, TEMPLATE, copy('adminUi', 'sms.enabledNo'))
    await expect(row).toContainText(copy('adminUi', 'sms.enabledNo'))
    await expect(row).toContainText(approvalText(EN_LOCALE, 'approved'))
    await expect(row).toContainText(approvalText(ZH_LOCALE, 'pending'))
  })

  await test.step('enabling the template leaves the review states unchanged', async () => {
    const row = templateRow(page, TEMPLATE)
    await row
      .getByRole('switch', {
        name: copy('adminUi', 'sms.enableAria', { name: TEMPLATE }),
        exact: true,
      })
      .click()
    await expect(
      page.getByText(copy('adminUi', 'sms.enabledToast', { name: TEMPLATE }), {
        exact: true,
      }),
    ).toBeVisible()
    await refreshUntil(page, TEMPLATE, copy('adminUi', 'sms.enabledYes'))
    await expect(row).toContainText(copy('adminUi', 'sms.enabledYes'))
    await expect(row).toContainText(approvalText(EN_LOCALE, 'approved'))
    await expect(row).toContainText(approvalText(ZH_LOCALE, 'pending'))
  })

  await test.step('the recipient proves the phone the dispatch needs', async () => {
    const recipientContext = await browser.newContext()
    try {
      const recipientPage = await recipientContext.newPage()
      const recipient = createAccounts({ page: recipientPage, server })
      await recipient.signIn({
        identifier: e2eOrdinaryMember.identifier,
        password: e2eOrdinaryMember.password,
      })
      await provePhone(recipientPage, server)
    } finally {
      await recipientContext.close()
    }
  })

  await test.step('the periodic dispatch sends the approved language to the SMS carrier', async () => {
    const logAfter = server.log.mark()

    const carrier = await server.waitForCarrierMessage({
      to: PHONE_E164,
      template: TEMPLATE,
      after: logAfter,
      timeoutMs: scaledTimeout(3 * NOTIFICATION_INTERVAL_MS),
    })
    expect(carrier.locale).toBe('en-US')

    const outbound = await server.waitForOutboundRecord({
      channel: 'sms',
      typeKey: TYPE_KEY,
      outcome: 'sent',
      after: logAfter,
      timeoutMs: scaledTimeout(3 * NOTIFICATION_INTERVAL_MS),
    })
    expect(outbound.providerID).not.toBe('')
    expect(outbound.durationMS).toBeGreaterThanOrEqual(0)
  })

  await test.step('a disabled template is refused locally and no message leaves', async () => {
    const row = templateRow(page, TEMPLATE)
    await row
      .getByRole('switch', {
        name: copy('adminUi', 'sms.disableAria', { name: TEMPLATE }),
        exact: true,
      })
      .click()
    await expect(
      page.getByText(
        copy('adminUi', 'sms.disabledToast', { name: TEMPLATE }),
        { exact: true },
      ),
    ).toBeVisible()
    await refreshUntil(page, TEMPLATE, copy('adminUi', 'sms.enabledNo'))
    await expect(row).toContainText(copy('adminUi', 'sms.enabledNo'))
    await expect(row).toContainText(approvalText(EN_LOCALE, 'approved'))
    await expect(row).toContainText(approvalText(ZH_LOCALE, 'pending'))

    const logAfter = server.log.mark()
    const windowMs = scaledTimeout(2 * NOTIFICATION_INTERVAL_MS)
    const startedAt = Date.now()

    await expect(async () => {
      expect(
        server.countCarrierMessages({
          to: PHONE_E164,
          template: TEMPLATE,
          after: logAfter,
        }),
      ).toBe(0)
      expect(
        server.countOutboundRecords({
          channel: 'sms',
          typeKey: TYPE_KEY,
          after: logAfter,
        }),
      ).toBe(0)
      expect(Date.now() - startedAt).toBeGreaterThanOrEqual(windowMs)
    }).toPass({
      timeout: windowMs + scaledTimeout(NOTIFICATION_INTERVAL_MS),
      intervals: [1_000],
    })
  })
})
